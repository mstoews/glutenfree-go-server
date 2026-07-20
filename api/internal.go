package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/token"
	"github.com/mstoews/glutenfree-server/util"
)

func validStoreStatus(s string) bool {
	switch db.StoreStatus(s) {
	case db.StoreStatusDraft, db.StoreStatusPending, db.StoreStatusApproved, db.StoreStatusRejected:
		return true
	}
	return false
}

// ---- auth ----

type internalLoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func (server *Server) internalLogin(ctx *gin.Context) {
	var req internalLoginRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	admin, err := server.store.GetInternalAdminByEmail(ctx, req.Email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ctx.JSON(http.StatusUnauthorized, errorResponse(errInvalidCredentials))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	if err := util.CheckPassword(req.Password, admin.PasswordHash); err != nil {
		ctx.JSON(http.StatusUnauthorized, errorResponse(errInvalidCredentials))
		return
	}

	server.issueInternalSession(ctx, admin)
}

type internalLoginResponse struct {
	AccessToken           string    `json:"access_token"`
	AccessTokenExpiresAt  time.Time `json:"access_token_expires_at"`
	RefreshToken          string    `json:"refresh_token"`
	RefreshTokenExpiresAt time.Time `json:"refresh_token_expires_at"`
	SessionID             uuid.UUID `json:"session_id"`
	Email                 string    `json:"email"`
}

// issueInternalSession mints an access + refresh token for an operator and
// persists the refresh session, enabling silent renewal and revocation.
func (server *Server) issueInternalSession(ctx *gin.Context, admin db.InternalAdmin) {
	accessToken, accessPayload, err := server.tokenMaker.CreateRoleToken(
		admin.ID, admin.Email, token.RoleInternal, nil, server.config.AccessTokenDuration)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	refreshToken, refreshPayload, err := server.tokenMaker.CreateRoleToken(
		admin.ID, admin.Email, token.RoleInternal, nil, server.config.RefreshTokenDuration)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	session, err := server.store.CreateInternalSession(ctx, db.CreateInternalSessionParams{
		ID:           refreshPayload.ID,
		AdminID:      admin.ID,
		RefreshToken: refreshToken,
		UserAgent:    ctx.Request.UserAgent(),
		ClientIp:     ctx.ClientIP(),
		IsBlocked:    false,
		ExpiresAt:    pgtype.Timestamptz{Time: refreshPayload.ExpiredAt, Valid: true},
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	ctx.JSON(http.StatusOK, internalLoginResponse{
		AccessToken:           accessToken,
		AccessTokenExpiresAt:  accessPayload.ExpiredAt,
		RefreshToken:          refreshToken,
		RefreshTokenExpiresAt: refreshPayload.ExpiredAt,
		SessionID:             session.ID,
		Email:                 admin.Email,
	})
}

// renewInternalAccessToken exchanges a valid operator refresh token for a fresh
// access token, validated against the stored internal session.
func (server *Server) renewInternalAccessToken(ctx *gin.Context) {
	var req renewAccessTokenRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	refreshPayload, err := server.tokenMaker.VerifyToken(req.RefreshToken)
	if err != nil {
		ctx.JSON(http.StatusUnauthorized, errorResponse(err))
		return
	}
	if refreshPayload.Role != token.RoleInternal {
		ctx.JSON(http.StatusUnauthorized, errorResponse(errors.New("not an internal refresh token")))
		return
	}

	session, err := server.store.GetInternalSession(ctx, refreshPayload.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ctx.JSON(http.StatusNotFound, errorResponse(errors.New("session not found")))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	if session.IsBlocked {
		ctx.JSON(http.StatusUnauthorized, errorResponse(errors.New("blocked session")))
		return
	}
	if session.AdminID != refreshPayload.UserID {
		ctx.JSON(http.StatusUnauthorized, errorResponse(errors.New("incorrect session user")))
		return
	}
	if session.RefreshToken != req.RefreshToken {
		ctx.JSON(http.StatusUnauthorized, errorResponse(errors.New("mismatched session token")))
		return
	}
	if time.Now().After(session.ExpiresAt.Time) {
		ctx.JSON(http.StatusUnauthorized, errorResponse(errors.New("expired session")))
		return
	}

	accessToken, accessPayload, err := server.tokenMaker.CreateRoleToken(
		refreshPayload.UserID, refreshPayload.Email, token.RoleInternal, nil, server.config.AccessTokenDuration)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	ctx.JSON(http.StatusOK, renewAccessTokenResponse{
		AccessToken:          accessToken,
		AccessTokenExpiresAt: accessPayload.ExpiredAt,
	})
}

// internalLogout revokes a refresh session. Idempotent: an unknown or already
// invalid token still returns success.
func (server *Server) internalLogout(ctx *gin.Context) {
	var req renewAccessTokenRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}
	if payload, err := server.tokenMaker.VerifyToken(req.RefreshToken); err == nil {
		_, _ = server.store.DeleteInternalSession(ctx, payload.ID)
	}
	ctx.JSON(http.StatusOK, gin.H{"logged_out": true})
}

// ---- provisioning ----

type provisionStoreAdminRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
	WardID   int32  `json:"ward_id" binding:"required"`
	Name     string `json:"name" binding:"required"`
}

// provisionStoreAdmin onboards a new store partner: it creates a draft store
// and a store-admin account scoped to it. The partner then fills in details and
// submits via /admin/*.
func (server *Server) provisionStoreAdmin(ctx *gin.Context) {
	var req provisionStoreAdminRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	// Reject a duplicate admin email up-front so we don't create an orphan store.
	if _, err := server.store.GetStoreAdminByEmail(ctx, req.Email); err == nil {
		ctx.JSON(http.StatusConflict, errorResponse(errors.New("store admin email already exists")))
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	store, err := server.store.CreateStore(ctx, db.CreateStoreParams{
		WardID:       req.WardID,
		Name:         req.Name,
		Address:      "",
		Latitude:     0,
		Longitude:    0,
		IsGfOriented: false,
		OpeningHours: []byte("[]"),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" { // FK violation
			ctx.JSON(http.StatusBadRequest, errorResponse(errors.New("unknown ward_id")))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	hash, err := util.HashPassword(req.Password)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	admin, err := server.store.CreateStoreAdmin(ctx, db.CreateStoreAdminParams{
		StoreID:      store.ID,
		Email:        req.Email,
		PasswordHash: hash,
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	ctx.JSON(http.StatusCreated, gin.H{
		"store_id":       store.ID,
		"store_admin_id": admin.ID,
		"email":          admin.Email,
		"status":         string(store.Status),
	})
}

// ---- review queue ----

type internalStoreRow struct {
	ID              uuid.UUID  `json:"id"`
	Name            string     `json:"name"`
	Ward            wardRef    `json:"ward"`
	Address         string     `json:"address"`
	IsGfOriented    bool       `json:"is_gf_oriented"`
	Status          string     `json:"status"`
	RejectionReason *string    `json:"rejection_reason"`
	CreatedAt       *time.Time `json:"created_at"`
}

func (server *Server) internalListStores(ctx *gin.Context) {
	statusStr := ctx.DefaultQuery("status", "pending")
	if !validStoreStatus(statusStr) {
		ctx.JSON(http.StatusBadRequest, errorResponse(errors.New("invalid status")))
		return
	}

	rows, err := server.store.ListStoresByStatus(ctx, db.StoreStatus(statusStr))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	out := make([]internalStoreRow, len(rows))
	for i, r := range rows {
		row := internalStoreRow{
			ID:           r.ID,
			Name:         r.Name,
			Ward:         wardRef{ID: r.WardID, NameJa: r.WardNameJa, NameEn: r.WardNameEn},
			Address:      r.Address,
			IsGfOriented: r.IsGfOriented,
			Status:       string(r.Status),
		}
		if r.RejectionReason.Valid {
			rr := r.RejectionReason.String
			row.RejectionReason = &rr
		}
		if r.CreatedAt.Valid {
			t := r.CreatedAt.Time
			row.CreatedAt = &t
		}
		out[i] = row
	}
	ctx.JSON(http.StatusOK, gin.H{"status": statusStr, "stores": out})
}

func (server *Server) approveStore(ctx *gin.Context) {
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(errInvalidStoreID))
		return
	}
	s, err := server.store.ApproveStore(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ctx.JSON(http.StatusConflict, errorResponse(errors.New("store not found, or already approved/rejected")))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	respondAdminStore(ctx, http.StatusOK, s)
}

type rejectStoreRequest struct {
	Reason string `json:"reason" binding:"required"`
}

func (server *Server) rejectStore(ctx *gin.Context) {
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(errInvalidStoreID))
		return
	}
	var req rejectStoreRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}
	s, err := server.store.RejectStore(ctx, db.RejectStoreParams{
		ID:              id,
		RejectionReason: textOrNull(req.Reason),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ctx.JSON(http.StatusConflict, errorResponse(errors.New("store not found or not pending")))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	respondAdminStore(ctx, http.StatusOK, s)
}

// ---- store CRUD (operator, full field access) ----

// internalStoreRequest is the write body for operator create/update. Unlike the
// store-admin updateStoreRequest, it carries every column: status, ward, and the
// curated rating/review_count.
type internalStoreRequest struct {
	WardID         int32         `json:"ward_id" binding:"required"`
	Name           string        `json:"name" binding:"required"`
	Address        string        `json:"address"`
	Latitude       float64       `json:"latitude"`
	Longitude      float64       `json:"longitude"`
	IsGfOriented   bool          `json:"is_gf_oriented"`
	OpeningHours   []openingHour `json:"opening_hours"`
	Status         string        `json:"status" binding:"required,oneof=draft pending approved rejected"`
	Cuisine        string        `json:"cuisine"`
	PriceLevel     int32         `json:"price_level" binding:"omitempty,min=1,max=3"`
	Rating         float32       `json:"rating" binding:"gte=0,lte=5"`
	ReviewCount    int32         `json:"review_count" binding:"gte=0"`
	NearestStation string        `json:"nearest_station"`
	Blurb          string        `json:"blurb"`
	GfStatus       string        `json:"gf_status" binding:"required,oneof=certified on_request contains_hidden_gluten"`
	PhotoURL       string        `json:"photo_url"`
	NameEn         string        `json:"name_en"`
	Phone          string        `json:"phone"`
	SourceURL      string        `json:"source_url"`
	Notes          string        `json:"notes"`
}

func (req internalStoreRequest) hoursJSON() ([]byte, error) {
	if req.OpeningHours == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(req.OpeningHours)
}

func (req internalStoreRequest) priceLevel() int32 {
	if req.PriceLevel == 0 {
		return 2
	}
	return req.PriceLevel
}

// isWardFKViolation reports whether err is a foreign-key violation, i.e. an
// unknown ward_id on a store write.
func isWardFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func (server *Server) internalCreateStore(ctx *gin.Context) {
	var req internalStoreRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}
	hoursJSON, err := req.hoursJSON()
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}
	s, err := server.store.CreateStoreFull(ctx, db.CreateStoreFullParams{
		WardID:         req.WardID,
		Name:           req.Name,
		Address:        req.Address,
		Latitude:       req.Latitude,
		Longitude:      req.Longitude,
		IsGfOriented:   req.IsGfOriented,
		OpeningHours:   hoursJSON,
		Status:         db.StoreStatus(req.Status),
		Cuisine:        req.Cuisine,
		PriceLevel:     req.priceLevel(),
		Rating:         req.Rating,
		ReviewCount:    req.ReviewCount,
		NearestStation: req.NearestStation,
		Blurb:          req.Blurb,
		GfStatus:       db.GfStatus(req.GfStatus),
		PhotoUrl:       textOrNull(req.PhotoURL),
		NameEn:         req.NameEn,
		Phone:          req.Phone,
		SourceUrl:      req.SourceURL,
		Notes:          req.Notes,
	})
	if err != nil {
		if isWardFKViolation(err) {
			ctx.JSON(http.StatusBadRequest, errorResponse(errors.New("unknown ward_id")))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	respondAdminStore(ctx, http.StatusCreated, s)
}

func (server *Server) internalGetStore(ctx *gin.Context) {
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(errInvalidStoreID))
		return
	}
	s, err := server.store.GetStoreByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ctx.JSON(http.StatusNotFound, errorResponse(errStoreNotFound))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	respondAdminStore(ctx, http.StatusOK, s)
}

func (server *Server) internalUpdateStore(ctx *gin.Context) {
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(errInvalidStoreID))
		return
	}
	var req internalStoreRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}
	hoursJSON, err := req.hoursJSON()
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}
	s, err := server.store.UpdateStoreFull(ctx, db.UpdateStoreFullParams{
		ID:             id,
		WardID:         req.WardID,
		Name:           req.Name,
		Address:        req.Address,
		Latitude:       req.Latitude,
		Longitude:      req.Longitude,
		IsGfOriented:   req.IsGfOriented,
		OpeningHours:   hoursJSON,
		Status:         db.StoreStatus(req.Status),
		Cuisine:        req.Cuisine,
		PriceLevel:     req.priceLevel(),
		Rating:         req.Rating,
		ReviewCount:    req.ReviewCount,
		NearestStation: req.NearestStation,
		Blurb:          req.Blurb,
		GfStatus:       db.GfStatus(req.GfStatus),
		PhotoUrl:       textOrNull(req.PhotoURL),
		NameEn:         req.NameEn,
		Phone:          req.Phone,
		SourceUrl:      req.SourceURL,
		Notes:          req.Notes,
	})
	if err != nil {
		if isWardFKViolation(err) {
			ctx.JSON(http.StatusBadRequest, errorResponse(errors.New("unknown ward_id")))
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			ctx.JSON(http.StatusNotFound, errorResponse(errStoreNotFound))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	respondAdminStore(ctx, http.StatusOK, s)
}

func (server *Server) internalDeleteStore(ctx *gin.Context) {
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(errInvalidStoreID))
		return
	}
	n, err := server.store.DeleteStore(ctx, id)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	if n == 0 {
		ctx.JSON(http.StatusNotFound, errorResponse(errStoreNotFound))
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"deleted": true})
}

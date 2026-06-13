package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/token"
	"github.com/mstoews/glutenfree-server/util"
)

var (
	errInvalidMenuID  = errors.New("invalid menu item id")
	errMenuNotFound   = errors.New("menu item not found")
	errNoStoreScope   = errors.New("token is missing store scope")
	errStoreNotInPath = errors.New("store is not in a submittable state (must be draft or rejected)")
)

// adminStoreID returns the store_id scoped into the store-admin token.
func adminStoreID(ctx *gin.Context) (uuid.UUID, bool) {
	payload := ctx.MustGet(authorizationPayloadKey).(*token.Payload)
	if payload.StoreID == nil {
		return uuid.Nil, false
	}
	return *payload.StoreID, true
}

func textOrNull(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// ---- auth ----

type adminLoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type adminLoginResponse struct {
	AccessToken          string    `json:"access_token"`
	AccessTokenExpiresAt time.Time `json:"access_token_expires_at"`
	StoreID              uuid.UUID `json:"store_id"`
	Email                string    `json:"email"`
}

func (server *Server) adminLogin(ctx *gin.Context) {
	var req adminLoginRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	admin, err := server.store.GetStoreAdminByEmail(ctx, req.Email)
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

	storeID := admin.StoreID
	accessToken, payload, err := server.tokenMaker.CreateRoleToken(
		admin.ID, admin.Email, token.RoleStoreAdmin, &storeID, server.config.AccessTokenDuration)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	ctx.JSON(http.StatusOK, adminLoginResponse{
		AccessToken:          accessToken,
		AccessTokenExpiresAt: payload.ExpiredAt,
		StoreID:              storeID,
		Email:                admin.Email,
	})
}

// ---- store profile ----

type adminStoreResponse struct {
	ID              uuid.UUID     `json:"id"`
	WardID          int32         `json:"ward_id"`
	Name            string        `json:"name"`
	Address         string        `json:"address"`
	Latitude        float64       `json:"latitude"`
	Longitude       float64       `json:"longitude"`
	IsGfOriented    bool          `json:"is_gf_oriented"`
	OpeningHours    []openingHour `json:"opening_hours"`
	Status          string        `json:"status"`
	RejectionReason *string       `json:"rejection_reason"`
	ApprovedAt      *time.Time    `json:"approved_at"`
}

func newAdminStoreResponse(s db.Store) (adminStoreResponse, error) {
	hours, err := parseOpeningHours(s.OpeningHours)
	if err != nil {
		return adminStoreResponse{}, err
	}
	resp := adminStoreResponse{
		ID:           s.ID,
		WardID:       s.WardID,
		Name:         s.Name,
		Address:      s.Address,
		Latitude:     s.Latitude,
		Longitude:    s.Longitude,
		IsGfOriented: s.IsGfOriented,
		OpeningHours: hours,
		Status:       string(s.Status),
	}
	if s.RejectionReason.Valid {
		r := s.RejectionReason.String
		resp.RejectionReason = &r
	}
	if s.ApprovedAt.Valid {
		t := s.ApprovedAt.Time
		resp.ApprovedAt = &t
	}
	return resp, nil
}

func (server *Server) adminGetStore(ctx *gin.Context) {
	storeID, ok := adminStoreID(ctx)
	if !ok {
		ctx.JSON(http.StatusBadRequest, errorResponse(errNoStoreScope))
		return
	}
	s, err := server.store.GetStoreByID(ctx, storeID)
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

type updateStoreRequest struct {
	Name         string        `json:"name" binding:"required"`
	Address      string        `json:"address" binding:"required"`
	Latitude     float64       `json:"latitude"`
	Longitude    float64       `json:"longitude"`
	IsGfOriented bool          `json:"is_gf_oriented"`
	OpeningHours []openingHour `json:"opening_hours"`
}

// adminUpdateStore edits the store profile. Edits to an already-approved store
// go live immediately (no re-review) since approved stores are served live.
func (server *Server) adminUpdateStore(ctx *gin.Context) {
	storeID, ok := adminStoreID(ctx)
	if !ok {
		ctx.JSON(http.StatusBadRequest, errorResponse(errNoStoreScope))
		return
	}

	var req updateStoreRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	hoursJSON := []byte("[]")
	if req.OpeningHours != nil {
		b, err := json.Marshal(req.OpeningHours)
		if err != nil {
			ctx.JSON(http.StatusBadRequest, errorResponse(err))
			return
		}
		hoursJSON = b
	}

	s, err := server.store.UpdateStoreProfile(ctx, db.UpdateStoreProfileParams{
		ID:           storeID,
		Name:         req.Name,
		Address:      req.Address,
		Latitude:     req.Latitude,
		Longitude:    req.Longitude,
		IsGfOriented: req.IsGfOriented,
		OpeningHours: hoursJSON,
	})
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

// adminSubmitStore moves a draft/rejected store into the pending review queue.
func (server *Server) adminSubmitStore(ctx *gin.Context) {
	storeID, ok := adminStoreID(ctx)
	if !ok {
		ctx.JSON(http.StatusBadRequest, errorResponse(errNoStoreScope))
		return
	}
	s, err := server.store.SubmitStore(ctx, storeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ctx.JSON(http.StatusConflict, errorResponse(errStoreNotInPath))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	respondAdminStore(ctx, http.StatusOK, s)
}

func respondAdminStore(ctx *gin.Context, code int, s db.Store) {
	resp, err := newAdminStoreResponse(s)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	ctx.JSON(code, resp)
}

// ---- menu ----

type adminMenuItemResponse struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	PriceYen    int32     `json:"price_yen"`
	ImageURL    *string   `json:"image_url"`
	GfStatus    string    `json:"gf_status"`
	GfNote      *string   `json:"gf_note"`
	SortOrder   int32     `json:"sort_order"`
	IsAvailable bool      `json:"is_available"`
}

func newAdminMenuItem(m db.MenuItem) adminMenuItemResponse {
	r := adminMenuItemResponse{
		ID:          m.ID,
		Name:        m.Name,
		PriceYen:    m.PriceYen,
		GfStatus:    string(m.GfStatus),
		SortOrder:   m.SortOrder,
		IsAvailable: m.IsAvailable,
	}
	if m.ImageUrl.Valid {
		s := m.ImageUrl.String
		r.ImageURL = &s
	}
	if m.GfNote.Valid {
		s := m.GfNote.String
		r.GfNote = &s
	}
	return r
}

type menuItemRequest struct {
	Name        string `json:"name" binding:"required"`
	PriceYen    int32  `json:"price_yen" binding:"gte=0"`
	ImageURL    string `json:"image_url"`
	GfStatus    string `json:"gf_status" binding:"required,oneof=certified on_request contains_hidden_gluten"`
	GfNote      string `json:"gf_note"`
	SortOrder   int32  `json:"sort_order"`
	IsAvailable *bool  `json:"is_available"`
}

func (req menuItemRequest) available() bool {
	if req.IsAvailable == nil {
		return true
	}
	return *req.IsAvailable
}

func (server *Server) adminListMenu(ctx *gin.Context) {
	storeID, ok := adminStoreID(ctx)
	if !ok {
		ctx.JSON(http.StatusBadRequest, errorResponse(errNoStoreScope))
		return
	}
	items, err := server.store.ListMenuItemsByStore(ctx, storeID)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	out := make([]adminMenuItemResponse, len(items))
	for i := range items {
		out[i] = newAdminMenuItem(items[i])
	}
	ctx.JSON(http.StatusOK, gin.H{"items": out})
}

func (server *Server) adminCreateMenu(ctx *gin.Context) {
	storeID, ok := adminStoreID(ctx)
	if !ok {
		ctx.JSON(http.StatusBadRequest, errorResponse(errNoStoreScope))
		return
	}
	var req menuItemRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}
	m, err := server.store.CreateMenuItem(ctx, db.CreateMenuItemParams{
		StoreID:     storeID,
		Name:        req.Name,
		PriceYen:    req.PriceYen,
		ImageUrl:    textOrNull(req.ImageURL),
		GfStatus:    db.GfStatus(req.GfStatus),
		GfNote:      textOrNull(req.GfNote),
		SortOrder:   req.SortOrder,
		IsAvailable: req.available(),
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	ctx.JSON(http.StatusCreated, newAdminMenuItem(m))
}

func (server *Server) adminUpdateMenu(ctx *gin.Context) {
	storeID, ok := adminStoreID(ctx)
	if !ok {
		ctx.JSON(http.StatusBadRequest, errorResponse(errNoStoreScope))
		return
	}
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(errInvalidMenuID))
		return
	}
	var req menuItemRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}
	m, err := server.store.UpdateMenuItem(ctx, db.UpdateMenuItemParams{
		ID:          id,
		StoreID:     storeID,
		Name:        req.Name,
		PriceYen:    req.PriceYen,
		ImageUrl:    textOrNull(req.ImageURL),
		GfStatus:    db.GfStatus(req.GfStatus),
		GfNote:      textOrNull(req.GfNote),
		SortOrder:   req.SortOrder,
		IsAvailable: req.available(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ctx.JSON(http.StatusNotFound, errorResponse(errMenuNotFound))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	ctx.JSON(http.StatusOK, newAdminMenuItem(m))
}

func (server *Server) adminDeleteMenu(ctx *gin.Context) {
	storeID, ok := adminStoreID(ctx)
	if !ok {
		ctx.JSON(http.StatusBadRequest, errorResponse(errNoStoreScope))
		return
	}
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(errInvalidMenuID))
		return
	}
	n, err := server.store.DeleteMenuItem(ctx, db.DeleteMenuItemParams{ID: id, StoreID: storeID})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	if n == 0 {
		ctx.JSON(http.StatusNotFound, errorResponse(errMenuNotFound))
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"deleted": true})
}

package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

	accessToken, payload, err := server.tokenMaker.CreateRoleToken(
		admin.ID, admin.Email, token.RoleInternal, nil, server.config.AccessTokenDuration)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"access_token":            accessToken,
		"access_token_expires_at": payload.ExpiredAt,
		"email":                   admin.Email,
	})
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
			ctx.JSON(http.StatusConflict, errorResponse(errors.New("store not found or not pending")))
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

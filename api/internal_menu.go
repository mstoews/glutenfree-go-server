package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
)

// Operator menu CRUD for any store (/internal/stores/:id/menu). The store-admin
// equivalents in admin.go are scoped to the admin's own store via their token;
// here the store comes from the path, so an operator can curate a restaurant
// that has no partner account -- which is every CSV-imported one.
//
// The underlying queries are already store-scoped (item id AND store id), so a
// mismatched pair simply affects no rows rather than touching another store's
// menu.

// storeIDFromPath parses the :id path param.
func storeIDFromPath(ctx *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(errInvalidStoreID))
		return uuid.Nil, false
	}
	return id, true
}

// menuItemIDFromPath parses the :item_id path param.
func menuItemIDFromPath(ctx *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(ctx.Param("item_id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(errInvalidMenuID))
		return uuid.Nil, false
	}
	return id, true
}

func (server *Server) internalListMenu(ctx *gin.Context) {
	storeID, ok := storeIDFromPath(ctx)
	if !ok {
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

func (server *Server) internalCreateMenu(ctx *gin.Context) {
	storeID, ok := storeIDFromPath(ctx)
	if !ok {
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
		if isWardFKViolation(err) { // FK violation here means the store doesn't exist
			ctx.JSON(http.StatusNotFound, errorResponse(errStoreNotFound))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	ctx.JSON(http.StatusCreated, newAdminMenuItem(m))
}

func (server *Server) internalUpdateMenu(ctx *gin.Context) {
	storeID, ok := storeIDFromPath(ctx)
	if !ok {
		return
	}
	itemID, ok := menuItemIDFromPath(ctx)
	if !ok {
		return
	}
	var req menuItemRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}
	m, err := server.store.UpdateMenuItem(ctx, db.UpdateMenuItemParams{
		ID:          itemID,
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

func (server *Server) internalDeleteMenu(ctx *gin.Context) {
	storeID, ok := storeIDFromPath(ctx)
	if !ok {
		return
	}
	itemID, ok := menuItemIDFromPath(ctx)
	if !ok {
		return
	}
	n, err := server.store.DeleteMenuItem(ctx, db.DeleteMenuItemParams{ID: itemID, StoreID: storeID})
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

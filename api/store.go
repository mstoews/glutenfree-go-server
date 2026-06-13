package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
)

const storesPageLimit = 20

var (
	errInvalidStoreID = errors.New("invalid store id")
	errStoreNotFound  = errors.New("store not found")
)

// openingHour is one entry of a store's opening_hours JSONB array.
type openingHour struct {
	Day   int    `json:"day"`   // 0 = Sunday
	Open  string `json:"open"`  // "HHMM"
	Close string `json:"close"` // "HHMM"
}

type wardRef struct {
	ID     int32  `json:"id"`
	NameJa string `json:"name_ja"`
	NameEn string `json:"name_en"`
}

// storeCard is one row in the browse list. Paid-only fields are pointers and
// omitted for free-tier users (design: the free list returns name + ward only).
type storeCard struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Ward         wardRef   `json:"ward"`
	IsGfOriented *bool     `json:"is_gf_oriented,omitempty"`
	Address      *string   `json:"address,omitempty"`
}

type listStoresResponse struct {
	Tier       string      `json:"tier"`
	NextCursor *uuid.UUID  `json:"next_cursor"`
	Stores     []storeCard `json:"stores"`
}

// listStores returns a paginated, ward-filterable list of approved stores.
// Free tier sees name + ward only; paid tier also gets address + GF flag.
func (server *Server) listStores(ctx *gin.Context) {
	user, err := server.currentUser(ctx)
	if err != nil {
		respondUserLookupError(ctx, err)
		return
	}

	// Optional ?ward_id= filter.
	var wardID pgtype.Int4
	if raw := ctx.Query("ward_id"); raw != "" {
		n, perr := strconv.Atoi(raw)
		if perr != nil {
			ctx.JSON(http.StatusBadRequest, errorResponse(errors.New("ward_id must be an integer")))
			return
		}
		wardID = pgtype.Int4{Int32: int32(n), Valid: true}
	}

	// Optional ?cursor= (last seen store id). Absent => zero UUID = from start.
	cursor := uuid.Nil
	if raw := ctx.Query("cursor"); raw != "" {
		c, perr := uuid.Parse(raw)
		if perr != nil {
			ctx.JSON(http.StatusBadRequest, errorResponse(errors.New("cursor must be a valid uuid")))
			return
		}
		cursor = c
	}

	// Fetch one extra row to detect whether a further page exists.
	rows, err := server.store.ListApprovedStores(ctx, db.ListApprovedStoresParams{
		WardID:    wardID,
		Cursor:    cursor,
		PageLimit: storesPageLimit + 1,
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	var next *uuid.UUID
	if len(rows) > storesPageLimit {
		last := rows[storesPageLimit-1].ID
		next = &last
		rows = rows[:storesPageLimit]
	}

	paid := isPaidUser(user)
	cards := make([]storeCard, len(rows))
	for i, r := range rows {
		card := storeCard{
			ID:   r.ID,
			Name: r.Name,
			Ward: wardRef{ID: r.WardID, NameJa: r.WardNameJa, NameEn: r.WardNameEn},
		}
		if paid {
			gf := r.IsGfOriented
			addr := r.Address
			card.IsGfOriented = &gf
			card.Address = &addr
		}
		cards[i] = card
	}

	ctx.JSON(http.StatusOK, listStoresResponse{
		Tier:       string(user.SubscriptionStatus),
		NextCursor: next,
		Stores:     cards,
	})
}

type storeDetailResponse struct {
	ID           uuid.UUID     `json:"id"`
	Name         string        `json:"name"`
	Ward         wardRef       `json:"ward"`
	Address      string        `json:"address"`
	Latitude     float64       `json:"latitude"`
	Longitude    float64       `json:"longitude"`
	IsGfOriented bool          `json:"is_gf_oriented"`
	OpeningHours []openingHour `json:"opening_hours"`
	ApprovedAt   *time.Time    `json:"approved_at"`
}

// getStore returns full detail for one approved store. Available to any
// authenticated user — the paid gate is on the menu, not on store detail.
func (server *Server) getStore(ctx *gin.Context) {
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(errInvalidStoreID))
		return
	}

	row, err := server.store.GetApprovedStore(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ctx.JSON(http.StatusNotFound, errorResponse(errStoreNotFound))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	hours, err := parseOpeningHours(row.OpeningHours)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	resp := storeDetailResponse{
		ID:           row.ID,
		Name:         row.Name,
		Ward:         wardRef{ID: row.WardID, NameJa: row.WardNameJa, NameEn: row.WardNameEn},
		Address:      row.Address,
		Latitude:     row.Latitude,
		Longitude:    row.Longitude,
		IsGfOriented: row.IsGfOriented,
		OpeningHours: hours,
	}
	if row.ApprovedAt.Valid {
		t := row.ApprovedAt.Time
		resp.ApprovedAt = &t
	}
	ctx.JSON(http.StatusOK, resp)
}

type menuItemResponse struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	PriceYen  int32     `json:"price_yen"`
	ImageURL  *string   `json:"image_url"`
	GfStatus  string    `json:"gf_status"`
	GfNote    *string   `json:"gf_note"`
	SortOrder int32     `json:"sort_order"`
}

// getStoreMenu returns a store's available menu items. Paid-only: free-tier
// users get HTTP 402 so the client can surface the paywall on first menu tap.
func (server *Server) getStoreMenu(ctx *gin.Context) {
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(errInvalidStoreID))
		return
	}

	user, err := server.currentUser(ctx)
	if err != nil {
		respondUserLookupError(ctx, err)
		return
	}

	// Paid content gate.
	if !isPaidUser(user) {
		ctx.JSON(http.StatusPaymentRequired, gin.H{
			"error":   "subscription required to view menus",
			"tier":    string(user.SubscriptionStatus),
			"upgrade": true,
		})
		return
	}

	// Only expose menus for approved stores.
	if _, err := server.store.GetApprovedStore(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ctx.JSON(http.StatusNotFound, errorResponse(errStoreNotFound))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	items, err := server.store.ListAvailableMenuItems(ctx, id)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	resp := make([]menuItemResponse, len(items))
	for i := range items {
		m := items[i]
		mi := menuItemResponse{
			ID:        m.ID,
			Name:      m.Name,
			PriceYen:  m.PriceYen,
			GfStatus:  string(m.GfStatus),
			SortOrder: m.SortOrder,
		}
		if m.ImageUrl.Valid {
			mi.ImageURL = &m.ImageUrl.String
		}
		if m.GfNote.Valid {
			mi.GfNote = &m.GfNote.String
		}
		resp[i] = mi
	}

	ctx.JSON(http.StatusOK, gin.H{"store_id": id, "items": resp})
}

// parseOpeningHours decodes the opening_hours JSONB column, defaulting to an
// empty slice (never null) so the client always receives an array.
func parseOpeningHours(raw []byte) ([]openingHour, error) {
	hours := []openingHour{}
	if len(raw) == 0 {
		return hours, nil
	}
	if err := json.Unmarshal(raw, &hours); err != nil {
		return nil, fmt.Errorf("invalid opening_hours: %w", err)
	}
	return hours, nil
}

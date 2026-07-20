package api

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/geocode"
)

// maxImportBytes caps the uploaded CSV so a bad request can't exhaust memory.
const maxImportBytes = 8 << 20 // 8 MiB

// importRowError explains why a single CSV row didn't import.
type importRowError struct {
	Row    int    `json:"row"` // 1-based data row (header excluded)
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// importResult summarises an import run for the operator.
type importResult struct {
	Created   int              `json:"created"`
	Skipped   int              `json:"skipped"`
	Failed    int              `json:"failed"`
	MenuItems int              `json:"menu_items"`
	Errors    []importRowError `json:"errors"`
}

// internalImportStores bulk-imports restaurants from the curated / web-scraped
// CSV format:
//
//	name,name_en,ward,address,gluten_free_items,phone,source_url,image_url,notes,date_added
//
// Columns are located by header name (order-independent) and unknown columns are
// ignored, so the future scraper can emit the same shape. New rows land as
// drafts for operator review; a row whose name already exists in that ward is
// skipped, making re-imports idempotent. Each row's semicolon-separated
// gluten_free_items become menu_items on the new store.
func (server *Server) internalImportStores(ctx *gin.Context) {
	body := http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxImportBytes)

	reader := csv.NewReader(body)
	reader.FieldsPerRecord = -1 // rows are indexed by header, so allow ragged
	reader.LazyQuotes = true

	header, err := reader.Read()
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(errors.New("could not read CSV header")))
		return
	}
	col := make(map[string]int, len(header))
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, required := range []string{"name", "ward"} {
		if _, ok := col[required]; !ok {
			ctx.JSON(http.StatusBadRequest, errorResponse(fmt.Errorf("CSV missing required column %q", required)))
			return
		}
	}

	// Ward name -> id, accepting either the Japanese or English name.
	wards, err := server.store.ListWards(ctx)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	wardIDs := make(map[string]int32, len(wards)*2)
	for _, w := range wards {
		wardIDs[strings.TrimSpace(w.NameJa)] = w.ID
		wardIDs[strings.ToLower(strings.TrimSpace(w.NameEn))] = w.ID
	}

	field := func(rec []string, name string) string {
		i, ok := col[name]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	result := importResult{Errors: []importRowError{}}
	row := 0
	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		row++
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, importRowError{
				Row: row, Reason: "malformed CSV row: " + err.Error(),
			})
			continue
		}

		name := field(rec, "name")
		if name == "" {
			result.Failed++
			result.Errors = append(result.Errors, importRowError{Row: row, Reason: "missing name"})
			continue
		}
		wardName := field(rec, "ward")
		wardID, ok := wardIDs[wardName]
		if !ok {
			wardID, ok = wardIDs[strings.ToLower(wardName)]
		}
		if !ok {
			result.Failed++
			result.Errors = append(result.Errors, importRowError{
				Row: row, Name: name, Reason: fmt.Sprintf("unknown ward %q", wardName),
			})
			continue
		}

		// Idempotency: same name already in this ward -> leave it alone.
		existing, err := server.store.CountStoresByNameWard(ctx, db.CountStoresByNameWardParams{
			Lower: name, WardID: wardID,
		})
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, importRowError{
				Row: row, Name: name, Reason: "duplicate check failed: " + err.Error(),
			})
			continue
		}
		if existing > 0 {
			result.Skipped++
			continue
		}

		// Imported rows are drafts: unreviewed scrape data, no coordinates yet,
		// and a conservative GF assurance until an operator verifies it.
		store, err := server.store.CreateStoreFull(ctx, db.CreateStoreFullParams{
			WardID:         wardID,
			Name:           name,
			Address:        field(rec, "address"),
			Latitude:       0,
			Longitude:      0,
			IsGfOriented:   false,
			OpeningHours:   []byte("[]"),
			Status:         db.StoreStatusDraft,
			Cuisine:        "",
			PriceLevel:     2,
			Rating:         0,
			ReviewCount:    0,
			NearestStation: "",
			Blurb:          "",
			GfStatus:       db.GfStatusOnRequest,
			PhotoUrl:       textOrNull(field(rec, "image_url")),
			NameEn:         field(rec, "name_en"),
			Phone:          field(rec, "phone"),
			SourceUrl:      field(rec, "source_url"),
			Notes:          field(rec, "notes"),
		})
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, importRowError{
				Row: row, Name: name, Reason: "create failed: " + err.Error(),
			})
			continue
		}
		result.Created++

		// gluten_free_items -> menu_items. Price is unknown from the CSV (some
		// rows carry it inside the item text), so it starts at 0 for review.
		for i, item := range splitSemicolonList(field(rec, "gluten_free_items")) {
			if _, err := server.store.CreateMenuItem(ctx, db.CreateMenuItemParams{
				StoreID:     store.ID,
				Name:        item,
				PriceYen:    0,
				ImageUrl:    pgtype.Text{},
				GfStatus:    db.GfStatusOnRequest,
				GfNote:      pgtype.Text{},
				SortOrder:   int32(i),
				IsAvailable: true,
			}); err != nil {
				result.Errors = append(result.Errors, importRowError{
					Row: row, Name: name, Reason: "menu item skipped: " + err.Error(),
				})
				continue
			}
			result.MenuItems++
		}
	}

	ctx.JSON(http.StatusOK, result)
}

// splitSemicolonList splits a "a; b; c" list, trimming blanks.
func splitSemicolonList(s string) []string {
	parts := strings.Split(s, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---- geocoding ----

const (
	defaultGeocodeLimit = 100
	maxGeocodeLimit     = 500
)

// geocodeRowError explains why one store couldn't be placed on the map.
type geocodeRowError struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Reason  string `json:"reason"`
}

// geocodeResult summarises a geocode backfill run.
type geocodeResult struct {
	Geocoded  int               `json:"geocoded"`
	NotFound  int               `json:"not_found"`
	TooVague  int               `json:"too_vague"`
	Failed    int               `json:"failed"`
	Remaining int               `json:"remaining"`
	Errors    []geocodeRowError `json:"errors"`
}

// internalGeocodeStores fills in coordinates for stores that have an address but
// no lat/lng yet (CSV imports land at 0,0). Ward-centroid matches are rejected
// rather than stored: a pin in the middle of Shibuya is worse than no pin, and
// leaving the store at 0,0 keeps it in the queue for a corrected address.
//
// Processes at most ?limit= stores per call (default 100) so a run stays within
// request timeouts; `remaining` reports whether another pass is needed.
func (server *Server) internalGeocodeStores(ctx *gin.Context) {
	limit := defaultGeocodeLimit
	if raw := ctx.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			ctx.JSON(http.StatusBadRequest, errorResponse(errors.New("limit must be a positive integer")))
			return
		}
		if n > maxGeocodeLimit {
			n = maxGeocodeLimit
		}
		limit = n
	}

	// Fetch one extra to tell whether more work remains after this batch.
	stores, err := server.store.ListStoresMissingCoords(ctx, int32(limit+1))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	result := geocodeResult{Errors: []geocodeRowError{}}
	if len(stores) > limit {
		result.Remaining = len(stores) - limit
		stores = stores[:limit]
	}

	for _, s := range stores {
		res, err := server.geocoder.Geocode(ctx, s.Address)
		switch {
		case errors.Is(err, geocode.ErrNotFound):
			result.NotFound++
			result.Errors = append(result.Errors, geocodeRowError{
				Name: s.Name, Address: s.Address, Reason: "no match for this address",
			})
			continue
		case err != nil:
			result.Failed++
			result.Errors = append(result.Errors, geocodeRowError{
				Name: s.Name, Address: s.Address, Reason: "geocoder error: " + err.Error(),
			})
			continue
		}
		if !res.Precise {
			result.TooVague++
			result.Errors = append(result.Errors, geocodeRowError{
				Name:    s.Name,
				Address: s.Address,
				Reason:  fmt.Sprintf("address too vague — only matched %q; needs a chome/banchi", res.MatchedAddress),
			})
			continue
		}

		if _, err := server.store.UpdateStoreCoords(ctx, db.UpdateStoreCoordsParams{
			ID: s.ID, Latitude: res.Lat, Longitude: res.Lng,
		}); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, geocodeRowError{
				Name: s.Name, Address: s.Address, Reason: "save failed: " + err.Error(),
			})
			continue
		}
		result.Geocoded++
	}

	ctx.JSON(http.StatusOK, result)
}

// ---- single-address lookup (for the store form's map) ----

type geocodeAddressRequest struct {
	Address string `json:"address" binding:"required"`
}

type geocodeAddressResponse struct {
	Lat             float64 `json:"lat"`
	Lng             float64 `json:"lng"`
	MatchedAddress  string  `json:"matched_address"`
	Precise         bool    `json:"precise"`
	NormalizedQuery string  `json:"normalized_query"`
}

// internalGeocodeAddress resolves a single address without saving anything, so
// the store form can drop a pin from the address field. Unlike the batch
// backfill this returns imprecise (ward-centroid) matches too, flagged via
// `precise`, letting the operator decide whether to accept the pin.
func (server *Server) internalGeocodeAddress(ctx *gin.Context) {
	var req geocodeAddressRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	res, err := server.geocoder.Geocode(ctx, req.Address)
	if errors.Is(err, geocode.ErrNotFound) {
		ctx.JSON(http.StatusNotFound, errorResponse(errors.New("no match for this address")))
		return
	}
	if err != nil {
		ctx.JSON(http.StatusBadGateway, errorResponse(err))
		return
	}

	ctx.JSON(http.StatusOK, geocodeAddressResponse{
		Lat:             res.Lat,
		Lng:             res.Lng,
		MatchedAddress:  res.MatchedAddress,
		Precise:         res.Precise,
		NormalizedQuery: geocode.Normalize(req.Address),
	})
}

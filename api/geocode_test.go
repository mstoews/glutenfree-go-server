package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/geocode"
	"github.com/mstoews/glutenfree-server/token"
)

// fakeGeocoder returns a canned answer per address.
type fakeGeocoder struct {
	fn func(ctx context.Context, address string) (geocode.Result, error)
}

func (f fakeGeocoder) Geocode(ctx context.Context, address string) (geocode.Result, error) {
	return f.fn(ctx, address)
}

func storeNeedingCoords(name, address string) db.Store {
	return db.Store{
		ID: uuid.New(), WardID: 13, Name: name, Address: address,
		OpeningHours: []byte("[]"), Status: db.StoreStatusDraft, GfStatus: db.GfStatusOnRequest,
	}
}

func TestGeocodeStores_SavesPreciseCoords(t *testing.T) {
	s := storeNeedingCoords("カフェA", "東京都渋谷区上原1-1-20")
	var saved db.UpdateStoreCoordsParams
	store := &fakeStore{
		listStoresMissingCoords: func(_ context.Context, limit int32) ([]db.Store, error) {
			return []db.Store{s}, nil
		},
		updateStoreCoords: func(_ context.Context, arg db.UpdateStoreCoordsParams) (int64, error) {
			saved = arg
			return 1, nil
		},
	}
	server := newTestServer(t, store)
	server.geocoder = fakeGeocoder{fn: func(_ context.Context, addr string) (geocode.Result, error) {
		return geocode.Result{Lat: 35.668423, Lng: 139.687424, MatchedAddress: "東京都渋谷区上原一丁目１番２０号", Precise: true}, nil
	}}

	rec := serveJSON(t, server, http.MethodPost, "/internal/stores/geocode",
		authHeader(t, server, token.RoleInternal, nil), nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if body := decodeBody(t, rec); body["geocoded"] != float64(1) {
		t.Errorf("geocoded = %v, want 1", body["geocoded"])
	}
	if saved.ID != s.ID || saved.Latitude != 35.668423 || saved.Longitude != 139.687424 {
		t.Errorf("saved coords = %+v", saved)
	}
}

// A ward-centroid match must NOT be written: leaving 0,0 keeps the store in the
// backfill queue instead of pinning it to the middle of the ward.
func TestGeocodeStores_RejectsWardCentroid(t *testing.T) {
	store := &fakeStore{
		listStoresMissingCoords: func(context.Context, int32) ([]db.Store, error) {
			return []db.Store{storeNeedingCoords("Vague Cafe", "東京都渋谷区原宿 ※駅から徒歩8分")}, nil
		},
		updateStoreCoords: func(context.Context, db.UpdateStoreCoordsParams) (int64, error) {
			t.Fatal("must not save an imprecise (ward-centroid) coordinate")
			return 0, nil
		},
	}
	server := newTestServer(t, store)
	server.geocoder = fakeGeocoder{fn: func(context.Context, string) (geocode.Result, error) {
		return geocode.Result{Lat: 35.66367, Lng: 139.697723, MatchedAddress: "東京都渋谷区", Precise: false}, nil
	}}

	rec := serveJSON(t, server, http.MethodPost, "/internal/stores/geocode",
		authHeader(t, server, token.RoleInternal, nil), nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeBody(t, rec)
	if body["too_vague"] != float64(1) || body["geocoded"] != float64(0) {
		t.Errorf("too_vague=%v geocoded=%v, want 1/0", body["too_vague"], body["geocoded"])
	}
	if errs, _ := body["errors"].([]any); len(errs) != 1 {
		t.Errorf("errors = %d, want 1", len(errs))
	}
}

func TestGeocodeStores_NotFoundIsReported(t *testing.T) {
	store := &fakeStore{
		listStoresMissingCoords: func(context.Context, int32) ([]db.Store, error) {
			return []db.Store{storeNeedingCoords("Ghost", "存在しない住所")}, nil
		},
		updateStoreCoords: func(context.Context, db.UpdateStoreCoordsParams) (int64, error) {
			t.Fatal("must not save when the address has no match")
			return 0, nil
		},
	}
	server := newTestServer(t, store)
	server.geocoder = fakeGeocoder{fn: func(context.Context, string) (geocode.Result, error) {
		return geocode.Result{}, geocode.ErrNotFound
	}}

	rec := serveJSON(t, server, http.MethodPost, "/internal/stores/geocode",
		authHeader(t, server, token.RoleInternal, nil), nil)

	if body := decodeBody(t, rec); body["not_found"] != float64(1) {
		t.Errorf("not_found = %v, want 1", body["not_found"])
	}
}

// One bad address must not abort the batch.
func TestGeocodeStores_ContinuesPastFailures(t *testing.T) {
	good := storeNeedingCoords("Good", "東京都渋谷区上原1-1-20")
	bad := storeNeedingCoords("Bad", "東京都渋谷区どこか")
	saved := 0
	store := &fakeStore{
		listStoresMissingCoords: func(context.Context, int32) ([]db.Store, error) {
			return []db.Store{bad, good}, nil
		},
		updateStoreCoords: func(context.Context, db.UpdateStoreCoordsParams) (int64, error) {
			saved++
			return 1, nil
		},
	}
	server := newTestServer(t, store)
	server.geocoder = fakeGeocoder{fn: func(_ context.Context, addr string) (geocode.Result, error) {
		if addr == bad.Address {
			return geocode.Result{}, geocode.ErrNotFound
		}
		return geocode.Result{Lat: 35.6, Lng: 139.6, Precise: true}, nil
	}}

	rec := serveJSON(t, server, http.MethodPost, "/internal/stores/geocode",
		authHeader(t, server, token.RoleInternal, nil), nil)

	body := decodeBody(t, rec)
	if body["geocoded"] != float64(1) || body["not_found"] != float64(1) {
		t.Errorf("geocoded=%v not_found=%v, want 1/1", body["geocoded"], body["not_found"])
	}
	if saved != 1 {
		t.Errorf("saved = %d, want 1", saved)
	}
}

// The batch limit caps work and reports what's left.
func TestGeocodeStores_LimitAndRemaining(t *testing.T) {
	var askedLimit int32
	store := &fakeStore{
		listStoresMissingCoords: func(_ context.Context, limit int32) ([]db.Store, error) {
			askedLimit = limit
			// Return limit+1 rows so the handler detects leftovers.
			out := make([]db.Store, 0, limit)
			for i := int32(0); i < limit; i++ {
				out = append(out, storeNeedingCoords("S", "東京都渋谷区上原1-1-20"))
			}
			return out, nil
		},
		updateStoreCoords: func(context.Context, db.UpdateStoreCoordsParams) (int64, error) { return 1, nil },
	}
	server := newTestServer(t, store)
	server.geocoder = fakeGeocoder{fn: func(context.Context, string) (geocode.Result, error) {
		return geocode.Result{Lat: 35.6, Lng: 139.6, Precise: true}, nil
	}}

	rec := serveJSON(t, server, http.MethodPost, "/internal/stores/geocode?limit=2",
		authHeader(t, server, token.RoleInternal, nil), nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// Handler fetches limit+1 to detect a further batch.
	if askedLimit != 3 {
		t.Errorf("fetch limit = %d, want 3 (limit+1)", askedLimit)
	}
	body := decodeBody(t, rec)
	if body["geocoded"] != float64(2) {
		t.Errorf("geocoded = %v, want 2", body["geocoded"])
	}
	if body["remaining"] != float64(1) {
		t.Errorf("remaining = %v, want 1", body["remaining"])
	}
}

func TestGeocodeStores_BadLimit(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	rec := serveJSON(t, server, http.MethodPost, "/internal/stores/geocode?limit=abc",
		authHeader(t, server, token.RoleInternal, nil), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGeocodeStores_RequiresInternalRole(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	storeID := uuid.New()
	rec := serveJSON(t, server, http.MethodPost, "/internal/stores/geocode",
		authHeader(t, server, token.RoleStoreAdmin, &storeID), nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// ---- single-address lookup ----

func TestGeocodeAddress_ReturnsCoordsWithoutSaving(t *testing.T) {
	server := newTestServer(t, &fakeStore{
		updateStoreCoords: func(context.Context, db.UpdateStoreCoordsParams) (int64, error) {
			t.Fatal("address lookup must not write to the DB")
			return 0, nil
		},
	})
	var gotAddr string
	server.geocoder = fakeGeocoder{fn: func(_ context.Context, addr string) (geocode.Result, error) {
		gotAddr = addr
		return geocode.Result{Lat: 35.668423, Lng: 139.687424, MatchedAddress: "東京都渋谷区上原一丁目１番２０号", Precise: true}, nil
	}}

	rec := serveJSON(t, server, http.MethodPost, "/internal/geocode",
		authHeader(t, server, token.RoleInternal, nil),
		map[string]any{"address": "〒151-0064 東京都渋谷区上原1-1-20"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["lat"] != 35.668423 || body["lng"] != 139.687424 {
		t.Errorf("coords = %v,%v", body["lat"], body["lng"])
	}
	if body["precise"] != true {
		t.Errorf("precise = %v, want true", body["precise"])
	}
	if gotAddr != "〒151-0064 東京都渋谷区上原1-1-20" {
		t.Errorf("geocoder got %q (handler should pass the raw address; the geocoder normalizes)", gotAddr)
	}
	// The response echoes the normalized query so the operator can see what was searched.
	if body["normalized_query"] != "東京都渋谷区上原1-1-20" {
		t.Errorf("normalized_query = %v", body["normalized_query"])
	}
}

// An imprecise match is still returned (flagged) so the operator can judge it —
// unlike the batch backfill, which refuses to save one.
func TestGeocodeAddress_ReturnsImpreciseFlagged(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	server.geocoder = fakeGeocoder{fn: func(context.Context, string) (geocode.Result, error) {
		return geocode.Result{Lat: 35.66367, Lng: 139.697723, MatchedAddress: "東京都渋谷区", Precise: false}, nil
	}}

	rec := serveJSON(t, server, http.MethodPost, "/internal/geocode",
		authHeader(t, server, token.RoleInternal, nil), map[string]any{"address": "東京都渋谷区原宿"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := decodeBody(t, rec); body["precise"] != false {
		t.Errorf("precise = %v, want false", body["precise"])
	}
}

func TestGeocodeAddress_NotFound(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	server.geocoder = fakeGeocoder{fn: func(context.Context, string) (geocode.Result, error) {
		return geocode.Result{}, geocode.ErrNotFound
	}}

	rec := serveJSON(t, server, http.MethodPost, "/internal/geocode",
		authHeader(t, server, token.RoleInternal, nil), map[string]any{"address": "存在しない"})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGeocodeAddress_MissingAddress(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	rec := serveJSON(t, server, http.MethodPost, "/internal/geocode",
		authHeader(t, server, token.RoleInternal, nil), map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

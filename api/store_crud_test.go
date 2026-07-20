package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/token"
)

// validCreateBody is a complete, valid POST /internal/stores payload. Tests
// tweak or drop individual keys to exercise defaults and validation.
func validCreateBody() map[string]any {
	return map[string]any{
		"ward_id":         13,
		"name":            "Test Store",
		"address":         "Shibuya 1-1",
		"latitude":        35.6595,
		"longitude":       139.7005,
		"is_gf_oriented":  true,
		"opening_hours":   []map[string]any{{"day": 1, "open": "1100", "close": "2200"}},
		"status":          "approved",
		"cuisine":         "Ramen",
		"price_level":     2,
		"rating":          4.5,
		"review_count":    128,
		"nearest_station": "Shibuya Stn",
		"blurb":           "Great GF ramen",
		"gf_status":       "certified",
		"photo_url":       "https://cdn.example/x.jpg",
	}
}

// ---- operator create ----

func TestInternalCreateStore_MapsAllFields(t *testing.T) {
	var got db.CreateStoreFullParams
	store := &fakeStore{
		createStoreFull: func(_ context.Context, arg db.CreateStoreFullParams) (db.Store, error) {
			got = arg
			return sampleStore(uuid.New()), nil
		},
	}
	server := newTestServer(t, store)

	rec := serveJSON(t, server, http.MethodPost, "/internal/stores",
		authHeader(t, server, token.RoleInternal, nil), validCreateBody())

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if got.WardID != 13 {
		t.Errorf("WardID = %d, want 13", got.WardID)
	}
	if got.Status != db.StoreStatusApproved {
		t.Errorf("Status = %q, want approved", got.Status)
	}
	if got.GfStatus != db.GfStatusCertified {
		t.Errorf("GfStatus = %q, want certified", got.GfStatus)
	}
	if got.Rating != 4.5 {
		t.Errorf("Rating = %v, want 4.5", got.Rating)
	}
	if got.ReviewCount != 128 {
		t.Errorf("ReviewCount = %d, want 128", got.ReviewCount)
	}
	if got.PriceLevel != 2 {
		t.Errorf("PriceLevel = %d, want 2", got.PriceLevel)
	}
	if !got.PhotoUrl.Valid || got.PhotoUrl.String != "https://cdn.example/x.jpg" {
		t.Errorf("PhotoUrl = %+v, want the cdn url", got.PhotoUrl)
	}
	if string(got.OpeningHours) == "" || string(got.OpeningHours) == "[]" {
		t.Errorf("OpeningHours = %s, want the marshalled hour", got.OpeningHours)
	}
}

func TestInternalCreateStore_DefaultsPriceLevel(t *testing.T) {
	var got db.CreateStoreFullParams
	store := &fakeStore{
		createStoreFull: func(_ context.Context, arg db.CreateStoreFullParams) (db.Store, error) {
			got = arg
			return sampleStore(uuid.New()), nil
		},
	}
	server := newTestServer(t, store)

	body := validCreateBody()
	delete(body, "price_level") // omitted -> defaults to 2

	rec := serveJSON(t, server, http.MethodPost, "/internal/stores",
		authHeader(t, server, token.RoleInternal, nil), body)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if got.PriceLevel != 2 {
		t.Errorf("PriceLevel = %d, want default 2", got.PriceLevel)
	}
}

func TestInternalCreateStore_ForbiddenForStoreAdmin(t *testing.T) {
	store := &fakeStore{
		createStoreFull: func(context.Context, db.CreateStoreFullParams) (db.Store, error) {
			t.Fatal("store should not be called when role is forbidden")
			return db.Store{}, nil
		},
	}
	server := newTestServer(t, store)
	storeID := uuid.New()

	rec := serveJSON(t, server, http.MethodPost, "/internal/stores",
		authHeader(t, server, token.RoleStoreAdmin, &storeID), validCreateBody())

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestInternalCreateStore_RequiresAuth(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	rec := serveJSON(t, server, http.MethodPost, "/internal/stores", "", validCreateBody())
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestInternalCreateStore_ValidationRejectsBadEnum(t *testing.T) {
	store := &fakeStore{
		createStoreFull: func(context.Context, db.CreateStoreFullParams) (db.Store, error) {
			t.Fatal("store should not be called on a binding error")
			return db.Store{}, nil
		},
	}
	server := newTestServer(t, store)

	body := validCreateBody()
	body["gf_status"] = "definitely_not_valid"

	rec := serveJSON(t, server, http.MethodPost, "/internal/stores",
		authHeader(t, server, token.RoleInternal, nil), body)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestInternalCreateStore_UnknownWardIsBadRequest(t *testing.T) {
	store := &fakeStore{
		createStoreFull: func(context.Context, db.CreateStoreFullParams) (db.Store, error) {
			return db.Store{}, &pgconn.PgError{Code: "23503"} // FK violation
		},
	}
	server := newTestServer(t, store)

	rec := serveJSON(t, server, http.MethodPost, "/internal/stores",
		authHeader(t, server, token.RoleInternal, nil), validCreateBody())

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// ---- operator update ----

func TestInternalUpdateStore_MapsIDAndFields(t *testing.T) {
	id := uuid.New()
	var got db.UpdateStoreFullParams
	store := &fakeStore{
		updateStoreFull: func(_ context.Context, arg db.UpdateStoreFullParams) (db.Store, error) {
			got = arg
			return sampleStore(id), nil
		},
	}
	server := newTestServer(t, store)

	rec := serveJSON(t, server, http.MethodPut, "/internal/stores/"+id.String(),
		authHeader(t, server, token.RoleInternal, nil), validCreateBody())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got.ID != id {
		t.Errorf("ID = %s, want %s", got.ID, id)
	}
	if got.Rating != 4.5 || got.ReviewCount != 128 {
		t.Errorf("curated fields not mapped: rating=%v review_count=%d", got.Rating, got.ReviewCount)
	}
}

func TestInternalUpdateStore_NotFound(t *testing.T) {
	store := &fakeStore{
		updateStoreFull: func(context.Context, db.UpdateStoreFullParams) (db.Store, error) {
			return db.Store{}, pgx.ErrNoRows
		},
	}
	server := newTestServer(t, store)

	rec := serveJSON(t, server, http.MethodPut, "/internal/stores/"+uuid.New().String(),
		authHeader(t, server, token.RoleInternal, nil), validCreateBody())

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestInternalUpdateStore_InvalidIDIsBadRequest(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	rec := serveJSON(t, server, http.MethodPut, "/internal/stores/not-a-uuid",
		authHeader(t, server, token.RoleInternal, nil), validCreateBody())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// ---- operator delete ----

func TestInternalDeleteStore(t *testing.T) {
	tests := []struct {
		name     string
		rows     int64
		wantCode int
	}{
		{"deleted", 1, http.StatusOK},
		{"missing", 0, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{
				deleteStore: func(context.Context, uuid.UUID) (int64, error) {
					return tt.rows, nil
				},
			}
			server := newTestServer(t, store)

			rec := serveJSON(t, server, http.MethodDelete, "/internal/stores/"+uuid.New().String(),
				authHeader(t, server, token.RoleInternal, nil), nil)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if tt.wantCode == http.StatusOK {
				if body := decodeBody(t, rec); body["deleted"] != true {
					t.Errorf("body = %v, want deleted:true", body)
				}
			}
		})
	}
}

// ---- operator get ----

func TestInternalGetStore(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		getStoreByID: func(_ context.Context, gotID uuid.UUID) (db.Store, error) {
			if gotID != id {
				t.Errorf("GetStoreByID id = %s, want %s", gotID, id)
			}
			return sampleStore(id), nil
		},
	}
	server := newTestServer(t, store)

	rec := serveJSON(t, server, http.MethodGet, "/internal/stores/"+id.String(),
		authHeader(t, server, token.RoleInternal, nil), nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if body := decodeBody(t, rec); body["id"] != id.String() {
		t.Errorf("id = %v, want %s", body["id"], id)
	}
}

func TestInternalGetStore_NotFound(t *testing.T) {
	store := &fakeStore{
		getStoreByID: func(context.Context, uuid.UUID) (db.Store, error) {
			return db.Store{}, pgx.ErrNoRows
		},
	}
	server := newTestServer(t, store)

	rec := serveJSON(t, server, http.MethodGet, "/internal/stores/"+uuid.New().String(),
		authHeader(t, server, token.RoleInternal, nil), nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// ---- store-admin self-serve display fields ----

// The store-admin PUT /admin/store must forward the display fields it owns and
// fall back to column defaults for omitted non-null fields (price_level -> 2,
// gf_status -> on_request), never writing 0 / "".
func TestAdminUpdateStore_ForwardsDisplayFieldsWithDefaults(t *testing.T) {
	storeID := uuid.New()
	var got db.UpdateStoreProfileParams
	store := &fakeStore{
		updateStoreProfile: func(_ context.Context, arg db.UpdateStoreProfileParams) (db.Store, error) {
			got = arg
			return sampleStore(storeID), nil
		},
	}
	server := newTestServer(t, store)

	// name + address are required by binding; display fields omitted on purpose.
	body := map[string]any{
		"name":      "Partner Cafe",
		"address":   "Meguro 2-2",
		"cuisine":   "Bakery",
		"photo_url": "https://cdn.example/cafe.jpg",
	}
	rec := serveJSON(t, server, http.MethodPut, "/admin/store",
		authHeader(t, server, token.RoleStoreAdmin, &storeID), body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got.ID != storeID {
		t.Errorf("scoped ID = %s, want %s", got.ID, storeID)
	}
	if got.Cuisine != "Bakery" {
		t.Errorf("Cuisine = %q, want Bakery", got.Cuisine)
	}
	if !got.PhotoUrl.Valid || got.PhotoUrl.String != "https://cdn.example/cafe.jpg" {
		t.Errorf("PhotoUrl = %+v, want the cdn url", got.PhotoUrl)
	}
	if got.PriceLevel != 2 {
		t.Errorf("PriceLevel = %d, want default 2", got.PriceLevel)
	}
	if got.GfStatus != db.GfStatusOnRequest {
		t.Errorf("GfStatus = %q, want default on_request", got.GfStatus)
	}
}

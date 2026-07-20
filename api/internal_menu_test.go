package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/token"
)

func menuItemBody() map[string]any {
	return map[string]any{
		"name":       "米粉のピザ",
		"price_yen":  1200,
		"gf_status":  "certified",
		"gf_note":    "国産米粉100%",
		"sort_order": 2,
		"image_url":  "https://storage.googleapis.com/gurufuri-images/menu/abc.jpg",
	}
}

func TestInternalListMenu(t *testing.T) {
	storeID := uuid.New()
	var gotStore uuid.UUID
	server := newTestServer(t, &fakeStore{
		listMenuItemsByStore: func(_ context.Context, id uuid.UUID) ([]db.MenuItem, error) {
			gotStore = id
			return []db.MenuItem{{ID: uuid.New(), StoreID: id, Name: "GF Ramen", PriceYen: 1450, GfStatus: db.GfStatusCertified}}, nil
		},
	})

	rec := serveJSON(t, server, http.MethodGet, "/internal/stores/"+storeID.String()+"/menu",
		authHeader(t, server, token.RoleInternal, nil), nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if gotStore != storeID {
		t.Errorf("listed store %s, want %s", gotStore, storeID)
	}
	items, _ := decodeBody(t, rec)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
}

func TestInternalCreateMenu_MapsFields(t *testing.T) {
	storeID := uuid.New()
	var got db.CreateMenuItemParams
	server := newTestServer(t, &fakeStore{
		createMenuItem: func(_ context.Context, arg db.CreateMenuItemParams) (db.MenuItem, error) {
			got = arg
			return db.MenuItem{ID: uuid.New(), StoreID: arg.StoreID, Name: arg.Name}, nil
		},
	})

	rec := serveJSON(t, server, http.MethodPost, "/internal/stores/"+storeID.String()+"/menu",
		authHeader(t, server, token.RoleInternal, nil), menuItemBody())

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if got.StoreID != storeID {
		t.Errorf("StoreID = %s, want the path store %s", got.StoreID, storeID)
	}
	if got.Name != "米粉のピザ" || got.PriceYen != 1200 {
		t.Errorf("name/price = %q/%d", got.Name, got.PriceYen)
	}
	if got.GfStatus != db.GfStatusCertified {
		t.Errorf("GfStatus = %q, want certified", got.GfStatus)
	}
	if !got.ImageUrl.Valid || got.ImageUrl.String == "" {
		t.Errorf("ImageUrl not stored: %+v", got.ImageUrl)
	}
	// is_available omitted -> defaults to true, not false.
	if !got.IsAvailable {
		t.Error("IsAvailable should default to true when omitted")
	}
}

// The item is scoped by BOTH ids, so a wrong pair must not touch another store.
func TestInternalUpdateMenu_ScopesToPathStore(t *testing.T) {
	storeID, itemID := uuid.New(), uuid.New()
	var got db.UpdateMenuItemParams
	server := newTestServer(t, &fakeStore{
		updateMenuItem: func(_ context.Context, arg db.UpdateMenuItemParams) (db.MenuItem, error) {
			got = arg
			return db.MenuItem{ID: arg.ID, StoreID: arg.StoreID, Name: arg.Name}, nil
		},
	})

	rec := serveJSON(t, server, http.MethodPut,
		"/internal/stores/"+storeID.String()+"/menu/"+itemID.String(),
		authHeader(t, server, token.RoleInternal, nil), menuItemBody())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got.ID != itemID || got.StoreID != storeID {
		t.Errorf("scoped to item=%s store=%s, want item=%s store=%s", got.ID, got.StoreID, itemID, storeID)
	}
}

func TestInternalUpdateMenu_NotFound(t *testing.T) {
	server := newTestServer(t, &fakeStore{
		updateMenuItem: func(context.Context, db.UpdateMenuItemParams) (db.MenuItem, error) {
			return db.MenuItem{}, pgx.ErrNoRows
		},
	})
	rec := serveJSON(t, server, http.MethodPut,
		"/internal/stores/"+uuid.New().String()+"/menu/"+uuid.New().String(),
		authHeader(t, server, token.RoleInternal, nil), menuItemBody())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestInternalDeleteMenu(t *testing.T) {
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
			server := newTestServer(t, &fakeStore{
				deleteMenuItem: func(context.Context, db.DeleteMenuItemParams) (int64, error) { return tt.rows, nil },
			})
			rec := serveJSON(t, server, http.MethodDelete,
				"/internal/stores/"+uuid.New().String()+"/menu/"+uuid.New().String(),
				authHeader(t, server, token.RoleInternal, nil), nil)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantCode)
			}
		})
	}
}

func TestInternalMenu_BadIDs(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	good := uuid.New().String()

	if rec := serveJSON(t, server, http.MethodGet, "/internal/stores/not-a-uuid/menu",
		authHeader(t, server, token.RoleInternal, nil), nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad store id -> %d, want 400", rec.Code)
	}
	if rec := serveJSON(t, server, http.MethodPut, "/internal/stores/"+good+"/menu/not-a-uuid",
		authHeader(t, server, token.RoleInternal, nil), menuItemBody()); rec.Code != http.StatusBadRequest {
		t.Errorf("bad item id -> %d, want 400", rec.Code)
	}
}

func TestInternalMenu_ValidationRejectsBadGfStatus(t *testing.T) {
	server := newTestServer(t, &fakeStore{
		createMenuItem: func(context.Context, db.CreateMenuItemParams) (db.MenuItem, error) {
			t.Fatal("must not create on a binding error")
			return db.MenuItem{}, nil
		},
	})
	body := menuItemBody()
	body["gf_status"] = "totally_gluten_free"

	rec := serveJSON(t, server, http.MethodPost, "/internal/stores/"+uuid.New().String()+"/menu",
		authHeader(t, server, token.RoleInternal, nil), body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestInternalMenu_RequiresInternalRole(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	storeID := uuid.New()
	rec := serveJSON(t, server, http.MethodGet, "/internal/stores/"+storeID.String()+"/menu",
		authHeader(t, server, token.RoleStoreAdmin, &storeID), nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/token"
)

const importCSVHeader = "name,name_en,ward,address,gluten_free_items,phone,source_url,image_url,notes,date_added\n"

// noDuplicates reports every row as new.
func noDuplicates(context.Context, db.CountStoresByNameWardParams) (int64, error) { return 0, nil }

func TestImportStores_CreatesDraftsWithMappedFields(t *testing.T) {
	var created []db.CreateStoreFullParams
	var menu []db.CreateMenuItemParams
	store := &fakeStore{
		listWards:             testWards,
		countStoresByNameWard: noDuplicates,
		createStoreFull: func(_ context.Context, arg db.CreateStoreFullParams) (db.Store, error) {
			created = append(created, arg)
			return db.Store{ID: uuid.New(), Name: arg.Name}, nil
		},
		createMenuItem: func(_ context.Context, arg db.CreateMenuItemParams) (db.MenuItem, error) {
			menu = append(menu, arg)
			return db.MenuItem{ID: uuid.New()}, nil
		},
	}
	server := newTestServer(t, store)

	csv := importCSVHeader +
		`カフェA,Cafe A,渋谷区,Shibuya 1-1,米粉のピザ; 米粉パスタ,03-1111-2222,https://src.example/a,,研究メモA,2026-06-24` + "\n" +
		`Cafe B,Cafe B,新宿区,Shinjuku 2-2,rice bread,,https://src.example/b,,note B,2026-06-24` + "\n"

	rec := serveRaw(t, server, http.MethodPost, "/internal/stores/import",
		authHeader(t, server, token.RoleInternal, nil), "text/csv", csv)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["created"] != float64(2) {
		t.Errorf("created = %v, want 2", body["created"])
	}
	if body["failed"] != float64(0) {
		t.Errorf("failed = %v, want 0", body["failed"])
	}
	// 2 items on row 1 + 1 on row 2.
	if body["menu_items"] != float64(3) {
		t.Errorf("menu_items = %v, want 3", body["menu_items"])
	}

	if len(created) != 2 {
		t.Fatalf("CreateStoreFull calls = %d, want 2", len(created))
	}
	a := created[0]
	if a.Name != "カフェA" || a.NameEn != "Cafe A" {
		t.Errorf("name/name_en = %q/%q", a.Name, a.NameEn)
	}
	if a.WardID != 13 {
		t.Errorf("WardID = %d, want 13 (渋谷区)", a.WardID)
	}
	if a.Status != db.StoreStatusDraft {
		t.Errorf("Status = %q, want draft", a.Status)
	}
	if a.Phone != "03-1111-2222" || a.SourceUrl != "https://src.example/a" || a.Notes != "研究メモA" {
		t.Errorf("provenance fields not mapped: phone=%q source=%q notes=%q", a.Phone, a.SourceUrl, a.Notes)
	}
	if a.GfStatus != db.GfStatusOnRequest {
		t.Errorf("GfStatus = %q, want conservative on_request", a.GfStatus)
	}
	// image_url is empty in the CSV -> photo_url must be NULL, not "".
	if a.PhotoUrl.Valid {
		t.Errorf("PhotoUrl = %+v, want NULL for an empty image_url", a.PhotoUrl)
	}
	// English ward name must resolve too.
	if created[1].WardID != 4 {
		t.Errorf("row 2 WardID = %d, want 4 (新宿区)", created[1].WardID)
	}

	if len(menu) != 3 {
		t.Fatalf("menu item calls = %d, want 3", len(menu))
	}
	if menu[0].Name != "米粉のピザ" || menu[1].Name != "米粉パスタ" {
		t.Errorf("semicolon list not split: %q, %q", menu[0].Name, menu[1].Name)
	}
	if menu[1].SortOrder != 1 {
		t.Errorf("SortOrder = %d, want 1", menu[1].SortOrder)
	}
}

func TestImportStores_SkipsExistingByNameAndWard(t *testing.T) {
	store := &fakeStore{
		listWards: testWards,
		countStoresByNameWard: func(context.Context, db.CountStoresByNameWardParams) (int64, error) {
			return 1, nil // already imported
		},
		createStoreFull: func(context.Context, db.CreateStoreFullParams) (db.Store, error) {
			t.Fatal("must not create a store that already exists")
			return db.Store{}, nil
		},
	}
	server := newTestServer(t, store)

	csv := importCSVHeader + `カフェA,Cafe A,渋谷区,addr,,,https://src.example/a,,note,2026-06-24` + "\n"
	rec := serveRaw(t, server, http.MethodPost, "/internal/stores/import",
		authHeader(t, server, token.RoleInternal, nil), "text/csv", csv)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeBody(t, rec)
	if body["skipped"] != float64(1) || body["created"] != float64(0) {
		t.Errorf("skipped=%v created=%v, want 1/0", body["skipped"], body["created"])
	}
}

func TestImportStores_UnknownWardIsRowErrorNotFatal(t *testing.T) {
	store := &fakeStore{
		listWards:             testWards,
		countStoresByNameWard: noDuplicates,
		createStoreFull: func(_ context.Context, arg db.CreateStoreFullParams) (db.Store, error) {
			return db.Store{ID: uuid.New(), Name: arg.Name}, nil
		},
		createMenuItem: func(context.Context, db.CreateMenuItemParams) (db.MenuItem, error) {
			return db.MenuItem{}, nil
		},
	}
	server := newTestServer(t, store)

	// Row 1 has a bogus ward; row 2 is fine -> import continues.
	csv := importCSVHeader +
		`Ghost,Ghost,Nowhere-ku,addr,,,https://x,,n,2026-06-24` + "\n" +
		`Cafe B,Cafe B,新宿区,addr,,,https://y,,n,2026-06-24` + "\n"

	rec := serveRaw(t, server, http.MethodPost, "/internal/stores/import",
		authHeader(t, server, token.RoleInternal, nil), "text/csv", csv)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeBody(t, rec)
	if body["failed"] != float64(1) || body["created"] != float64(1) {
		t.Errorf("failed=%v created=%v, want 1/1", body["failed"], body["created"])
	}
	errs, _ := body["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("errors = %d, want 1", len(errs))
	}
}

func TestImportStores_MissingRequiredColumn(t *testing.T) {
	server := newTestServer(t, &fakeStore{
		listWards: func(context.Context) ([]db.Ward, error) {
			t.Fatal("should reject before touching the DB")
			return nil, nil
		},
	})

	rec := serveRaw(t, server, http.MethodPost, "/internal/stores/import",
		authHeader(t, server, token.RoleInternal, nil), "text/csv", "name,address\nFoo,Bar\n")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestImportStores_RequiresInternalRole(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	storeID := uuid.New()

	rec := serveRaw(t, server, http.MethodPost, "/internal/stores/import",
		authHeader(t, server, token.RoleStoreAdmin, &storeID), "text/csv", importCSVHeader)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

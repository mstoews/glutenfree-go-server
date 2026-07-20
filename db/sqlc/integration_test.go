//go:build integration

// DB-backed integration tests for the store queries. These run only under the
// `integration` build tag against a real Postgres pointed at by TEST_DB_SOURCE
// (see `make test-integration`). They exercise the actual sqlc-generated SQL --
// including the FK ON DELETE CASCADE that the fake-store handler tests can't.
package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testQueries *Queries

func TestMain(m *testing.M) {
	source := os.Getenv("TEST_DB_SOURCE")
	if source == "" {
		fmt.Fprintln(os.Stderr, "TEST_DB_SOURCE not set; skipping db integration tests")
		return
	}
	pool, err := pgxpool.New(context.Background(), source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect TEST_DB_SOURCE: %v\n", err)
		os.Exit(1)
	}
	testQueries = New(pool)
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }

// TestStoreCRUD_Integration drives CreateStoreFull -> Get -> UpdateStoreFull ->
// DeleteStore against a real Postgres, and proves the store's menu items and
// store-admin account cascade away on delete.
func TestStoreCRUD_Integration(t *testing.T) {
	q := testQueries
	ctx := context.Background()

	// A ward is required (serial 1..23 seeded by migration 000002).
	created, err := q.CreateStoreFull(ctx, CreateStoreFullParams{
		WardID:         1,
		Name:           "Integration Test Store",
		Address:        "Chiyoda 1-1",
		Latitude:       35.6895,
		Longitude:      139.6917,
		IsGfOriented:   true,
		OpeningHours:   []byte(`[{"day":1,"open":"1100","close":"2200"}]`),
		Status:         StoreStatusApproved,
		Cuisine:        "Ramen",
		PriceLevel:     3,
		Rating:         4.5,
		ReviewCount:    42,
		NearestStation: "Tokyo",
		Blurb:          "GF ramen",
		GfStatus:       GfStatusCertified,
		PhotoUrl:       text("https://cdn.example/it.jpg"),
	})
	if err != nil {
		t.Fatalf("CreateStoreFull: %v", err)
	}
	t.Cleanup(func() { _, _ = q.DeleteStore(ctx, created.ID) }) // in case an assertion aborts before delete

	if created.ID == uuid.Nil {
		t.Fatal("expected a generated store id")
	}
	if created.Rating != 4.5 || created.ReviewCount != 42 || created.PriceLevel != 3 {
		t.Errorf("curated fields didn't round-trip: rating=%v reviews=%d price=%d",
			created.Rating, created.ReviewCount, created.PriceLevel)
	}
	if created.GfStatus != GfStatusCertified || created.Status != StoreStatusApproved {
		t.Errorf("enum round-trip: gf=%q status=%q", created.GfStatus, created.Status)
	}
	if !created.PhotoUrl.Valid || created.PhotoUrl.String != "https://cdn.example/it.jpg" {
		t.Errorf("PhotoUrl round-trip: %+v", created.PhotoUrl)
	}

	// Read back.
	got, err := q.GetStoreByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetStoreByID: %v", err)
	}
	if got.Name != "Integration Test Store" {
		t.Errorf("Name = %q", got.Name)
	}

	// Create dependents that must cascade on delete.
	menu, err := q.CreateMenuItem(ctx, CreateMenuItemParams{
		StoreID:     created.ID,
		Name:        "GF Shoyu Ramen",
		PriceYen:    1200,
		GfStatus:    GfStatusCertified,
		SortOrder:   0,
		IsAvailable: true,
	})
	if err != nil {
		t.Fatalf("CreateMenuItem: %v", err)
	}
	if menu.StoreID != created.ID {
		t.Errorf("menu.StoreID = %s, want %s", menu.StoreID, created.ID)
	}
	adminEmail := fmt.Sprintf("it-admin-%s@test.local", uuid.NewString())
	if _, err := q.CreateStoreAdmin(ctx, CreateStoreAdminParams{
		StoreID:      created.ID,
		Email:        adminEmail,
		PasswordHash: "x",
	}); err != nil {
		t.Fatalf("CreateStoreAdmin: %v", err)
	}

	if items, err := q.ListMenuItemsByStore(ctx, created.ID); err != nil {
		t.Fatalf("ListMenuItemsByStore (before delete): %v", err)
	} else if len(items) != 1 {
		t.Fatalf("menu items before delete = %d, want 1", len(items))
	}

	// Update every field, including curated ones, and clear the photo.
	updated, err := q.UpdateStoreFull(ctx, UpdateStoreFullParams{
		ID:             created.ID,
		WardID:         2,
		Name:           "Renamed Store",
		Address:        got.Address,
		Latitude:       got.Latitude,
		Longitude:      got.Longitude,
		IsGfOriented:   false,
		OpeningHours:   []byte("[]"),
		Status:         StoreStatusPending,
		Cuisine:        "Bakery",
		PriceLevel:     1,
		Rating:         2.0,
		ReviewCount:    7,
		NearestStation: "Otemachi",
		Blurb:          "changed",
		GfStatus:       GfStatusOnRequest,
		PhotoUrl:       pgtype.Text{}, // NULL
	})
	if err != nil {
		t.Fatalf("UpdateStoreFull: %v", err)
	}
	if updated.Name != "Renamed Store" || updated.WardID != 2 || updated.Status != StoreStatusPending {
		t.Errorf("update not applied: name=%q ward=%d status=%q", updated.Name, updated.WardID, updated.Status)
	}
	if updated.Rating != 2.0 || updated.ReviewCount != 7 {
		t.Errorf("curated update not applied: rating=%v reviews=%d", updated.Rating, updated.ReviewCount)
	}
	if updated.PhotoUrl.Valid {
		t.Errorf("PhotoUrl should be NULL after clearing, got %+v", updated.PhotoUrl)
	}

	// Delete -> exactly one row.
	rows, err := q.DeleteStore(ctx, created.ID)
	if err != nil {
		t.Fatalf("DeleteStore: %v", err)
	}
	if rows != 1 {
		t.Fatalf("DeleteStore rows = %d, want 1", rows)
	}

	// Store gone.
	if _, err := q.GetStoreByID(ctx, created.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("GetStoreByID after delete: err = %v, want ErrNoRows", err)
	}
	// Menu item cascaded (menu_items FK ON DELETE CASCADE).
	if items, err := q.ListMenuItemsByStore(ctx, created.ID); err != nil {
		t.Fatalf("ListMenuItemsByStore (after delete): %v", err)
	} else if len(items) != 0 {
		t.Errorf("menu items after store delete = %d, want 0 (cascade)", len(items))
	}
	// Store admin cascaded (store_admins FK ON DELETE CASCADE).
	if _, err := q.GetStoreAdminByEmail(ctx, adminEmail); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("store admin after store delete: err = %v, want ErrNoRows (cascade)", err)
	}
}

// TestUpdateStoreFull_UnknownStore_Integration confirms a full update of a
// non-existent store returns no rows (the handler maps this to 404).
func TestUpdateStoreFull_UnknownStore_Integration(t *testing.T) {
	q := testQueries
	_, err := q.UpdateStoreFull(context.Background(), UpdateStoreFullParams{
		ID:           uuid.New(),
		WardID:       1,
		Name:         "ghost",
		OpeningHours: []byte("[]"),
		Status:       StoreStatusDraft,
		PriceLevel:   2,
		GfStatus:     GfStatusOnRequest,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("UpdateStoreFull on missing id: err = %v, want ErrNoRows", err)
	}
}

// draftStoreParams builds a minimal valid draft store for approval tests.
func draftStoreParams(name string) CreateStoreFullParams {
	return CreateStoreFullParams{
		WardID:       1,
		Name:         name,
		Address:      "東京都千代田区丸の内1-9-1",
		OpeningHours: []byte("[]"),
		Status:       StoreStatusDraft,
		PriceLevel:   2,
		GfStatus:     GfStatusOnRequest,
	}
}

// updateParamsFrom mirrors a store into an UpdateStoreFull payload (what the
// operator form sends back), with the status swapped.
func updateParamsFrom(s Store, status StoreStatus) UpdateStoreFullParams {
	return UpdateStoreFullParams{
		ID: s.ID, WardID: s.WardID, Name: s.Name, Address: s.Address,
		Latitude: s.Latitude, Longitude: s.Longitude, IsGfOriented: s.IsGfOriented,
		OpeningHours: s.OpeningHours, Status: status, Cuisine: s.Cuisine,
		PriceLevel: s.PriceLevel, Rating: s.Rating, ReviewCount: s.ReviewCount,
		NearestStation: s.NearestStation, Blurb: s.Blurb, GfStatus: s.GfStatus,
		PhotoUrl: s.PhotoUrl, NameEn: s.NameEn, Phone: s.Phone,
		SourceUrl: s.SourceUrl, Notes: s.Notes,
	}
}

// TestApprovedAt_Integration pins the approved_at semantics the operator form
// depends on: stamped on the transition into 'approved', preserved across later
// edits of an already-approved store, and cleared when it leaves 'approved'.
func TestApprovedAt_Integration(t *testing.T) {
	q := testQueries
	ctx := context.Background()

	created, err := q.CreateStoreFull(ctx, draftStoreParams("ApprovedAt Test"))
	if err != nil {
		t.Fatalf("CreateStoreFull: %v", err)
	}
	t.Cleanup(func() { _, _ = q.DeleteStore(ctx, created.ID) })

	if created.ApprovedAt.Valid {
		t.Error("a freshly created draft must not have approved_at set")
	}

	// draft -> approved: stamped.
	upd := updateParamsFrom(created, StoreStatusApproved)
	approved, err := q.UpdateStoreFull(ctx, upd)
	if err != nil {
		t.Fatalf("UpdateStoreFull(approved): %v", err)
	}
	if !approved.ApprovedAt.Valid {
		t.Fatal("approving via the form must stamp approved_at (this was the bug)")
	}
	firstStamp := approved.ApprovedAt.Time
	if time.Since(firstStamp) > time.Minute {
		t.Errorf("approved_at = %v, expected ~now", firstStamp)
	}

	// Editing an already-approved store must NOT move the date.
	upd.Name = "ApprovedAt Test (edited)"
	upd.Blurb = "edited after approval"
	edited, err := q.UpdateStoreFull(ctx, upd)
	if err != nil {
		t.Fatalf("UpdateStoreFull(edit while approved): %v", err)
	}
	if !edited.ApprovedAt.Valid || !edited.ApprovedAt.Time.Equal(firstStamp) {
		t.Errorf("editing an approved store moved approved_at: %v -> %+v", firstStamp, edited.ApprovedAt)
	}

	// Leaving 'approved' clears it: approved_at is set iff currently approved.
	upd.Status = StoreStatusDraft
	unapproved, err := q.UpdateStoreFull(ctx, upd)
	if err != nil {
		t.Fatalf("UpdateStoreFull(back to draft): %v", err)
	}
	if unapproved.ApprovedAt.Valid {
		t.Errorf("moving out of approved must clear approved_at, got %v", unapproved.ApprovedAt.Time)
	}

	// Re-approving stamps a fresh date.
	upd.Status = StoreStatusApproved
	reapproved, err := q.UpdateStoreFull(ctx, upd)
	if err != nil {
		t.Fatalf("UpdateStoreFull(re-approve): %v", err)
	}
	if !reapproved.ApprovedAt.Valid {
		t.Error("re-approving must stamp approved_at again")
	}
}

// TestApproveStore_FromDraft_Integration covers the Approve button path: it must
// work on imported drafts, not just partner-submitted (pending) stores.
func TestApproveStore_FromDraft_Integration(t *testing.T) {
	q := testQueries
	ctx := context.Background()

	draft, err := q.CreateStoreFull(ctx, draftStoreParams("Approve From Draft"))
	if err != nil {
		t.Fatalf("CreateStoreFull: %v", err)
	}
	t.Cleanup(func() { _, _ = q.DeleteStore(ctx, draft.ID) })

	approved, err := q.ApproveStore(ctx, draft.ID)
	if err != nil {
		t.Fatalf("ApproveStore on a draft must succeed, got: %v", err)
	}
	if approved.Status != StoreStatusApproved {
		t.Errorf("status = %q, want approved", approved.Status)
	}
	if !approved.ApprovedAt.Valid {
		t.Error("ApproveStore must stamp approved_at")
	}

	// Approving again is a no-op: already approved is not an approvable state.
	if _, err := q.ApproveStore(ctx, draft.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("approving an already-approved store: err = %v, want ErrNoRows", err)
	}
}

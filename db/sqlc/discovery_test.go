package db

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Use only a disposable database with migrations applied.
func TestDiscoveryConcurrentRetries(t *testing.T) {
	source := os.Getenv("DISCOVERY_TEST_DB")
	if source == "" {
		t.Skip("set DISCOVERY_TEST_DB to an isolated migrated test database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := NewStore(pool)
	key := uuid.NewString()
	arg := CreateStoreFullParams{WardID: 13, Name: "Discovery " + key, Address: "Shibuya " + key, SourceUrl: "https://example.com/" + key, OpeningHours: []byte("[]"), PriceLevel: 2, Status: StoreStatusApproved, GfStatus: GfStatusCertified, IsGfOriented: true}
	defer pool.Exec(ctx, "DELETE FROM stores WHERE source_url=$1", arg.SourceUrl)
	var created atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, newRow, err := repo.CreateDiscoveryDraft(ctx, arg)
			if err != nil {
				t.Error(err)
				return
			}
			if newRow {
				created.Add(1)
			}
			if s.Status != StoreStatusDraft || s.GfStatus != GfStatusOnRequest || s.IsGfOriented {
				t.Error("draft invariant violated")
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatalf("created %d rows", created.Load())
	}
	// A different source for the same name/ward is also a duplicate.
	arg.SourceUrl += "/alternate"
	if _, newRow, err := repo.CreateDiscoveryDraft(ctx, arg); err != nil || newRow {
		t.Fatalf("duplicate retry: created=%v err=%v", newRow, err)
	}
}

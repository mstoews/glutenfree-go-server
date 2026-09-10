package db

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateDiscoveryDraft serializes duplicate checks across server instances and
// commits each candidate independently. A timed-out batch can safely be retried.
func (s *SQLStore) CreateDiscoveryDraft(ctx context.Context, arg CreateStoreFullParams) (Store, bool, error) {
	tx, err := s.connPool.Begin(ctx)
	if err != nil {
		return Store{}, false, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	// A dedicated namespace lock, held only for this short database transaction.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(724198, 1)"); err != nil {
		return Store{}, false, err
	}
	var existingID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM stores WHERE source_url = $1 OR
  (ward_id = $2 AND (lower(btrim(name)) = lower($3) OR
  (btrim(address) <> '' AND lower(btrim(address)) = lower($4)))) LIMIT 1`, arg.SourceUrl, arg.WardID, strings.TrimSpace(arg.Name), strings.TrimSpace(arg.Address)).Scan(&existingID)
	q := New(tx)
	if err == nil {
		existing, err := q.GetStoreByID(ctx, existingID)
		if err != nil {
			return Store{}, false, err
		}
		if err = tx.Commit(ctx); err != nil {
			return Store{}, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Store{}, false, err
	}
	// Enforce draft semantics at the persistence boundary, regardless of caller.
	arg.Status = StoreStatusDraft
	arg.GfStatus = GfStatusOnRequest
	arg.IsGfOriented = false
	created, err := q.CreateStoreFull(ctx, arg)
	if err != nil {
		return Store{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Store{}, false, err
	}
	return created, true, nil
}

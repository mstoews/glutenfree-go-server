package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/token"
	"github.com/mstoews/glutenfree-server/util"
)

// internalRefreshToken mints a real internal-role token from the server's maker.
func internalRefreshToken(t *testing.T, server *Server, adminID uuid.UUID, email, role string) (string, uuid.UUID) {
	t.Helper()
	tok, payload, err := server.tokenMaker.CreateRoleToken(adminID, email, role, nil, time.Hour)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	return tok, payload.ID
}

// ---- login ----

func TestInternalLogin_IssuesAccessAndRefresh(t *testing.T) {
	adminID := uuid.New()
	hash, err := util.HashPassword("secret123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var gotSession db.CreateInternalSessionParams
	store := &fakeStore{
		getInternalAdminByEmail: func(context.Context, string) (db.InternalAdmin, error) {
			return db.InternalAdmin{ID: adminID, Email: "ops@example.com", PasswordHash: hash}, nil
		},
		createInternalSession: func(_ context.Context, arg db.CreateInternalSessionParams) (db.InternalSession, error) {
			gotSession = arg
			return db.InternalSession{ID: arg.ID, AdminID: arg.AdminID}, nil
		},
	}
	server := newTestServer(t, store)

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/login", "",
		map[string]any{"email": "ops@example.com", "password": "secret123"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["access_token"] == "" || body["access_token"] == nil {
		t.Error("missing access_token")
	}
	refresh, _ := body["refresh_token"].(string)
	if refresh == "" {
		t.Fatal("missing refresh_token")
	}
	if body["session_id"] == "" || body["session_id"] == nil {
		t.Error("missing session_id")
	}
	// The persisted session must match the returned refresh token and admin.
	if gotSession.AdminID != adminID {
		t.Errorf("session AdminID = %s, want %s", gotSession.AdminID, adminID)
	}
	if gotSession.RefreshToken != refresh {
		t.Error("persisted refresh token != returned refresh token")
	}
	if !gotSession.ExpiresAt.Valid {
		t.Error("session ExpiresAt not set")
	}
}

func TestInternalLogin_BadPassword(t *testing.T) {
	hash, _ := util.HashPassword("the-right-one")
	store := &fakeStore{
		getInternalAdminByEmail: func(context.Context, string) (db.InternalAdmin, error) {
			return db.InternalAdmin{ID: uuid.New(), Email: "ops@example.com", PasswordHash: hash}, nil
		},
		createInternalSession: func(context.Context, db.CreateInternalSessionParams) (db.InternalSession, error) {
			t.Fatal("no session should be created on a bad password")
			return db.InternalSession{}, nil
		},
	}
	server := newTestServer(t, store)

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/login", "",
		map[string]any{"email": "ops@example.com", "password": "wrong"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// ---- refresh ----

func TestRenewInternalAccessToken_Valid(t *testing.T) {
	server := newTestServer(t, nil) // store set below once we can mint a token
	adminID := uuid.New()
	refreshTok, jti := internalRefreshToken(t, server, adminID, "ops@example.com", token.RoleInternal)

	store := &fakeStore{
		getInternalSession: func(_ context.Context, id uuid.UUID) (db.InternalSession, error) {
			if id != jti {
				t.Errorf("GetInternalSession id = %s, want %s", id, jti)
			}
			return db.InternalSession{
				ID:           jti,
				AdminID:      adminID,
				RefreshToken: refreshTok,
				IsBlocked:    false,
				ExpiresAt:    pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
			}, nil
		},
	}
	server.store = store

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/refresh", "",
		map[string]any{"refresh_token": refreshTok})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if tok, _ := decodeBody(t, rec)["access_token"].(string); tok == "" {
		t.Error("missing access_token in renew response")
	}
}

func TestRenewInternalAccessToken_WrongRole(t *testing.T) {
	server := newTestServer(t, &fakeStore{
		getInternalSession: func(context.Context, uuid.UUID) (db.InternalSession, error) {
			t.Fatal("session lookup should not happen for a non-internal token")
			return db.InternalSession{}, nil
		},
	})
	// A store-admin refresh token must be refused at the internal endpoint.
	refreshTok, _ := internalRefreshToken(t, server, uuid.New(), "partner@example.com", token.RoleStoreAdmin)

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/refresh", "",
		map[string]any{"refresh_token": refreshTok})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestRenewInternalAccessToken_MismatchedStoredToken(t *testing.T) {
	server := newTestServer(t, nil)
	adminID := uuid.New()
	refreshTok, jti := internalRefreshToken(t, server, adminID, "ops@example.com", token.RoleInternal)

	server.store = &fakeStore{
		getInternalSession: func(context.Context, uuid.UUID) (db.InternalSession, error) {
			return db.InternalSession{
				ID:           jti,
				AdminID:      adminID,
				RefreshToken: "a-different-token", // token rotated / stolen
				ExpiresAt:    pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
			}, nil
		},
	}

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/refresh", "",
		map[string]any{"refresh_token": refreshTok})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// ---- logout ----

func TestInternalLogout_RevokesSession(t *testing.T) {
	server := newTestServer(t, nil)
	refreshTok, jti := internalRefreshToken(t, server, uuid.New(), "ops@example.com", token.RoleInternal)

	var deletedID uuid.UUID
	server.store = &fakeStore{
		deleteInternalSession: func(_ context.Context, id uuid.UUID) (int64, error) {
			deletedID = id
			return 1, nil
		},
	}

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/logout", "",
		map[string]any{"refresh_token": refreshTok})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if decodeBody(t, rec)["logged_out"] != true {
		t.Error("expected logged_out: true")
	}
	if deletedID != jti {
		t.Errorf("deleted session id = %s, want %s", deletedID, jti)
	}
}

func TestInternalLogout_JunkTokenIsIdempotent(t *testing.T) {
	server := newTestServer(t, &fakeStore{
		deleteInternalSession: func(context.Context, uuid.UUID) (int64, error) {
			t.Fatal("delete should not be called for an unverifiable token")
			return 0, nil
		},
	})

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/logout", "",
		map[string]any{"refresh_token": "not-a-real-token"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if decodeBody(t, rec)["logged_out"] != true {
		t.Error("expected logged_out: true")
	}
}

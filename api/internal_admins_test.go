package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/mailer"
	"github.com/mstoews/glutenfree-server/token"
	"github.com/mstoews/glutenfree-server/util"
)

// internalAuthHeader mints an internal-role Bearer token bound to a specific
// admin id, which the self-service handlers read out of the payload.
func internalAuthHeader(t *testing.T, server *Server, adminID uuid.UUID, email string) string {
	t.Helper()
	tok, _ := internalRefreshToken(t, server, adminID, email, token.RoleInternal)
	return "bearer " + tok
}

// sampleInternalAdmin returns a populated operator row.
func sampleInternalAdmin(id uuid.UUID, hash string) db.InternalAdmin {
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	return db.InternalAdmin{
		ID:           id,
		Email:        "ops@example.com",
		PasswordHash: hash,
		Name:         "Ops Person",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// passwordChangeStore wires the three writes replaceAdminPassword performs and
// records what each was called with.
type passwordChangeStore struct {
	updated      db.UpdateInternalAdminPasswordParams
	sessionsFor  []uuid.UUID
	resetsFor    []uuid.UUID
	updateCalled bool
}

func (p *passwordChangeStore) attach(f *fakeStore) {
	f.updateInternalAdminPass = func(_ context.Context, arg db.UpdateInternalAdminPasswordParams) (db.InternalAdmin, error) {
		p.updated = arg
		p.updateCalled = true
		return db.InternalAdmin{ID: arg.ID, PasswordHash: arg.PasswordHash}, nil
	}
	f.deleteInternalSessionsFor = func(_ context.Context, id uuid.UUID) (int64, error) {
		p.sessionsFor = append(p.sessionsFor, id)
		return 2, nil
	}
	f.deleteInternalPassResets = func(_ context.Context, id uuid.UUID) (int64, error) {
		p.resetsFor = append(p.resetsFor, id)
		return 1, nil
	}
}

// ---- list ----

func TestListInternalAdmins_NeverLeaksPasswordHash(t *testing.T) {
	server := newTestServer(t, &fakeStore{
		listInternalAdmins: func(context.Context) ([]db.InternalAdmin, error) {
			return []db.InternalAdmin{
				sampleInternalAdmin(uuid.New(), "$2a$10$averyrealbcrypthashvalue"),
				sampleInternalAdmin(uuid.New(), "$2a$10$anotherrealbcrypthash"),
			}, nil
		},
	})

	rec := serveJSON(t, server, http.MethodGet, "/internal/admins",
		authHeader(t, server, token.RoleInternal, nil), nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// A substring check catches the hash under any field name, not just the one
	// the response struct happens to omit today.
	if strings.Contains(rec.Body.String(), "password_hash") || strings.Contains(rec.Body.String(), "$2a$10$") {
		t.Fatalf("list response leaked a password hash: %s", rec.Body.String())
	}

	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list body %q: %v", rec.Body.String(), err)
	}
	if len(out) != 2 {
		t.Fatalf("admins = %d, want 2", len(out))
	}
	if out[0]["email"] != "ops@example.com" {
		t.Errorf("email = %v, want ops@example.com", out[0]["email"])
	}
}

// ---- create ----

func TestCreateInternalAdmin_StoresHashNotPlaintext(t *testing.T) {
	const plaintext = "correct-horse-battery"
	var got db.CreateInternalAdminParams
	server := newTestServer(t, &fakeStore{
		createInternalAdmin: func(_ context.Context, arg db.CreateInternalAdminParams) (db.InternalAdmin, error) {
			got = arg
			return sampleInternalAdmin(uuid.New(), arg.PasswordHash), nil
		},
	})

	rec := serveJSON(t, server, http.MethodPost, "/internal/admins",
		authHeader(t, server, token.RoleInternal, nil),
		map[string]any{"email": "new@example.com", "password": plaintext, "name": " New Op "})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if got.Email != "new@example.com" || got.Name != "New Op" {
		t.Errorf("email/name = %q/%q, want trimmed values", got.Email, got.Name)
	}
	if got.PasswordHash == plaintext {
		t.Fatal("password was stored in plaintext")
	}
	if err := util.CheckPassword(plaintext, got.PasswordHash); err != nil {
		t.Fatalf("stored hash does not verify against the plaintext: %v", err)
	}
	if strings.Contains(rec.Body.String(), got.PasswordHash) {
		t.Errorf("create response echoed the password hash: %s", rec.Body.String())
	}
}

func TestCreateInternalAdmin_Validation(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{"bad email", map[string]any{"email": "not-an-email", "password": "longenough1"}},
		{"short password", map[string]any{"email": "new@example.com", "password": "short7c"}},
		{"missing password", map[string]any{"email": "new@example.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newTestServer(t, &fakeStore{
				createInternalAdmin: func(context.Context, db.CreateInternalAdminParams) (db.InternalAdmin, error) {
					t.Fatal("must not create on a binding error")
					return db.InternalAdmin{}, nil
				},
			})
			rec := serveJSON(t, server, http.MethodPost, "/internal/admins",
				authHeader(t, server, token.RoleInternal, nil), tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestCreateInternalAdmin_DuplicateEmail(t *testing.T) {
	server := newTestServer(t, &fakeStore{
		createInternalAdmin: func(context.Context, db.CreateInternalAdminParams) (db.InternalAdmin, error) {
			return db.InternalAdmin{}, &pgconn.PgError{Code: uniqueViolation, Message: "duplicate key value"}
		},
	})

	rec := serveJSON(t, server, http.MethodPost, "/internal/admins",
		authHeader(t, server, token.RoleInternal, nil),
		map[string]any{"email": "taken@example.com", "password": "longenough1"})

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	// The raw driver message must not reach the caller.
	if msg, _ := decodeBody(t, rec)["error"].(string); !strings.Contains(msg, "already exists") {
		t.Errorf("error = %q, want the friendly conflict message", msg)
	}
}

func TestCreateInternalAdmin_RequiresInternalRole(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	storeID := uuid.New()

	rec := serveJSON(t, server, http.MethodPost, "/internal/admins",
		authHeader(t, server, token.RoleStoreAdmin, &storeID),
		map[string]any{"email": "new@example.com", "password": "longenough1"})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestCreateInternalAdmin_RequiresAuth(t *testing.T) {
	server := newTestServer(t, &fakeStore{})

	rec := serveJSON(t, server, http.MethodPost, "/internal/admins", "",
		map[string]any{"email": "new@example.com", "password": "longenough1"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// ---- set another admin's password ----

func TestSetInternalAdminPassword_RevokesTargetSessions(t *testing.T) {
	targetID := uuid.New()
	writes := &passwordChangeStore{}
	store := &fakeStore{
		getInternalAdminByID: func(_ context.Context, id uuid.UUID) (db.InternalAdmin, error) {
			return sampleInternalAdmin(id, "$2a$10$existinghash"), nil
		},
	}
	writes.attach(store)
	server := newTestServer(t, store)

	rec := serveJSON(t, server, http.MethodPut, "/internal/admins/"+targetID.String()+"/password",
		authHeader(t, server, token.RoleInternal, nil),
		map[string]any{"password": "brand-new-password"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if decodeBody(t, rec)["password_updated"] != true {
		t.Error("expected password_updated: true")
	}
	if writes.updated.ID != targetID {
		t.Errorf("updated admin = %s, want the path admin %s", writes.updated.ID, targetID)
	}
	if err := util.CheckPassword("brand-new-password", writes.updated.PasswordHash); err != nil {
		t.Errorf("stored hash does not verify: %v", err)
	}
	// A reset is worthless if the target's stolen refresh tokens survive it.
	if len(writes.sessionsFor) != 1 || writes.sessionsFor[0] != targetID {
		t.Errorf("sessions revoked for %v, want exactly [%s]", writes.sessionsFor, targetID)
	}
	if len(writes.resetsFor) != 1 || writes.resetsFor[0] != targetID {
		t.Errorf("reset tokens cleared for %v, want exactly [%s]", writes.resetsFor, targetID)
	}
}

func TestSetInternalAdminPassword_NotFound(t *testing.T) {
	server := newTestServer(t, &fakeStore{
		getInternalAdminByID: func(context.Context, uuid.UUID) (db.InternalAdmin, error) {
			return db.InternalAdmin{}, pgx.ErrNoRows
		},
		updateInternalAdminPass: func(context.Context, db.UpdateInternalAdminPasswordParams) (db.InternalAdmin, error) {
			t.Fatal("must not write a password for an admin that does not exist")
			return db.InternalAdmin{}, nil
		},
	})

	rec := serveJSON(t, server, http.MethodPut, "/internal/admins/"+uuid.New().String()+"/password",
		authHeader(t, server, token.RoleInternal, nil),
		map[string]any{"password": "brand-new-password"})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSetInternalAdminPassword_BadID(t *testing.T) {
	server := newTestServer(t, &fakeStore{})

	rec := serveJSON(t, server, http.MethodPut, "/internal/admins/not-a-uuid/password",
		authHeader(t, server, token.RoleInternal, nil),
		map[string]any{"password": "brand-new-password"})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSetInternalAdminPassword_ShortPassword(t *testing.T) {
	server := newTestServer(t, &fakeStore{
		getInternalAdminByID: func(context.Context, uuid.UUID) (db.InternalAdmin, error) {
			t.Fatal("must not look up the admin on a binding error")
			return db.InternalAdmin{}, nil
		},
	})

	rec := serveJSON(t, server, http.MethodPut, "/internal/admins/"+uuid.New().String()+"/password",
		authHeader(t, server, token.RoleInternal, nil), map[string]any{"password": "short7c"})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// ---- change your own password ----

func TestChangeInternalPassword_WrongCurrentPassword(t *testing.T) {
	adminID := uuid.New()
	hash, err := util.HashPassword("the-right-one")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	server := newTestServer(t, &fakeStore{
		getInternalAdminByID: func(_ context.Context, id uuid.UUID) (db.InternalAdmin, error) {
			return sampleInternalAdmin(id, hash), nil
		},
		updateInternalAdminPass: func(context.Context, db.UpdateInternalAdminPasswordParams) (db.InternalAdmin, error) {
			t.Fatal("must not change the password without the current one")
			return db.InternalAdmin{}, nil
		},
	})

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/change-password",
		internalAuthHeader(t, server, adminID, "ops@example.com"),
		map[string]any{"current_password": "the-wrong-one", "new_password": "a-new-password"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestChangeInternalPassword_IssuesFreshSession(t *testing.T) {
	adminID := uuid.New()
	hash, err := util.HashPassword("the-right-one")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	writes := &passwordChangeStore{}
	var newSession db.CreateInternalSessionParams
	store := &fakeStore{
		getInternalAdminByID: func(_ context.Context, id uuid.UUID) (db.InternalAdmin, error) {
			return sampleInternalAdmin(id, hash), nil
		},
		createInternalSession: func(_ context.Context, arg db.CreateInternalSessionParams) (db.InternalSession, error) {
			newSession = arg
			return db.InternalSession{ID: arg.ID, AdminID: arg.AdminID}, nil
		},
	}
	writes.attach(store)
	server := newTestServer(t, store)

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/change-password",
		internalAuthHeader(t, server, adminID, "ops@example.com"),
		map[string]any{"current_password": "the-right-one", "new_password": "a-new-password"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	refresh, _ := body["refresh_token"].(string)
	if access, _ := body["access_token"].(string); access == "" || refresh == "" {
		t.Fatalf("expected a fresh token pair, got %s", rec.Body.String())
	}
	if err := util.CheckPassword("a-new-password", writes.updated.PasswordHash); err != nil {
		t.Errorf("stored hash does not verify against the new password: %v", err)
	}
	// The old sessions go, and the replacement is minted for the same admin —
	// otherwise the caller would be signed out by their own password change.
	if len(writes.sessionsFor) != 1 || writes.sessionsFor[0] != adminID {
		t.Errorf("sessions revoked for %v, want exactly [%s]", writes.sessionsFor, adminID)
	}
	if newSession.AdminID != adminID || newSession.RefreshToken != refresh {
		t.Errorf("new session = %+v, want one for %s holding the returned refresh token", newSession, adminID)
	}
}

func TestChangeInternalPassword_RequiresAuth(t *testing.T) {
	server := newTestServer(t, &fakeStore{})

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/change-password", "",
		map[string]any{"current_password": "whatever", "new_password": "a-new-password"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// ---- forgot password ----

// stubMailer captures sent messages in place of a provider. Enabled() is true
// so the handler takes the delivery path.
type stubMailer struct {
	sent []mailer.Message
	err  error
}

func (s *stubMailer) Enabled() bool { return true }

func (s *stubMailer) Send(_ context.Context, msg mailer.Message) error {
	s.sent = append(s.sent, msg)
	return s.err
}

// newMailTestServer builds a server with mail delivery and a portal URL wired,
// which the default newTestServer deliberately leaves unconfigured.
func newMailTestServer(t *testing.T, store db.Repository) (*Server, *stubMailer) {
	t.Helper()
	server := newTestServer(t, store)
	stub := &stubMailer{}
	server.mailer = stub
	server.config.AdminPortalURL = "https://admin.gurufuri.test/"
	server.config.PasswordResetTokenDuration = time.Hour
	return server, stub
}

// resetLinkToken pulls the raw token back out of a reset email, undoing the
// query escaping applied when the link was built.
func resetLinkToken(t *testing.T, msg mailer.Message) string {
	t.Helper()
	match := regexp.MustCompile(`reset-password\?token=(\S+)`).FindStringSubmatch(msg.Text)
	if match == nil {
		t.Fatalf("no reset link in mail body: %s", msg.Text)
	}
	raw, err := url.QueryUnescape(match[1])
	if err != nil {
		t.Fatalf("unescape token %q: %v", match[1], err)
	}
	return raw
}

func TestForgotInternalPassword_NotConfigured(t *testing.T) {
	// newTestServer sets no ResendAPIKey, so NewServer installs the noop sender.
	server := newTestServer(t, &fakeStore{
		getInternalAdminByEmail: func(context.Context, string) (db.InternalAdmin, error) {
			t.Fatal("must not look up an admin when mail is unconfigured")
			return db.InternalAdmin{}, nil
		},
	})

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/forgot-password", "",
		map[string]any{"email": "ops@example.com"})

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body=%s", rec.Code, rec.Body.String())
	}
}

func TestForgotInternalPassword_UnknownEmailDoesNotEnumerate(t *testing.T) {
	server, stub := newMailTestServer(t, &fakeStore{
		getInternalAdminByEmail: func(context.Context, string) (db.InternalAdmin, error) {
			return db.InternalAdmin{}, pgx.ErrNoRows
		},
		createInternalPassReset: func(context.Context, db.CreateInternalPasswordResetParams) (db.InternalPasswordReset, error) {
			t.Fatal("must not mint a reset token for an unknown address")
			return db.InternalPasswordReset{}, nil
		},
	})

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/forgot-password", "",
		map[string]any{"email": "nobody@example.com"})

	// Byte-identical to the known-address response, or the endpoint becomes an
	// account-existence oracle.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if decodeBody(t, rec)["sent"] != true {
		t.Errorf("body = %s, want {\"sent\":true}", rec.Body.String())
	}
	if len(stub.sent) != 0 {
		t.Errorf("sent %d mails to an unknown address, want 0", len(stub.sent))
	}
}

func TestForgotInternalPassword_SendsLinkAndStoresOnlyTheHash(t *testing.T) {
	adminID := uuid.New()
	var stored db.CreateInternalPasswordResetParams
	server, stub := newMailTestServer(t, &fakeStore{
		getInternalAdminByEmail: func(_ context.Context, email string) (db.InternalAdmin, error) {
			if email != "ops@example.com" {
				t.Errorf("looked up %q, want the requested address", email)
			}
			return sampleInternalAdmin(adminID, "$2a$10$existinghash"), nil
		},
		createInternalPassReset: func(_ context.Context, arg db.CreateInternalPasswordResetParams) (db.InternalPasswordReset, error) {
			stored = arg
			return db.InternalPasswordReset{ID: uuid.New(), AdminID: arg.AdminID, TokenHash: arg.TokenHash}, nil
		},
	})

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/forgot-password", "",
		map[string]any{"email": "ops@example.com"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if decodeBody(t, rec)["sent"] != true {
		t.Errorf("body = %s, want {\"sent\":true}", rec.Body.String())
	}
	if len(stub.sent) != 1 {
		t.Fatalf("sent %d mails, want 1", len(stub.sent))
	}
	msg := stub.sent[0]
	if msg.To != "ops@example.com" {
		t.Errorf("mail To = %q, want the admin address", msg.To)
	}
	// The link must point at the portal, with the trailing slash collapsed.
	if !strings.Contains(msg.Text, "https://admin.gurufuri.test/reset-password?token=") {
		t.Errorf("mail body has no portal reset link: %s", msg.Text)
	}
	if !strings.Contains(msg.HTML, "https://admin.gurufuri.test/reset-password?token=") {
		t.Errorf("html body has no portal reset link: %s", msg.HTML)
	}

	raw := resetLinkToken(t, msg)
	if raw == "" {
		t.Fatal("empty reset token in link")
	}
	// Only the digest is persisted: a database leak must not yield usable links.
	sum := sha256.Sum256([]byte(raw))
	if want := hex.EncodeToString(sum[:]); stored.TokenHash != want {
		t.Errorf("stored TokenHash = %q, want sha256(raw) = %q", stored.TokenHash, want)
	}
	if strings.Contains(stored.TokenHash, raw) {
		t.Error("the raw reset token was persisted")
	}
	if stored.AdminID != adminID {
		t.Errorf("reset AdminID = %s, want %s", stored.AdminID, adminID)
	}
	if !stored.ExpiresAt.Valid || !stored.ExpiresAt.Time.After(time.Now()) {
		t.Errorf("reset ExpiresAt = %+v, want a future timestamp", stored.ExpiresAt)
	}
}

// A provider failure is logged, not surfaced — the error would confirm the
// address exists.
func TestForgotInternalPassword_SendFailureStillReportsSent(t *testing.T) {
	server, stub := newMailTestServer(t, &fakeStore{
		getInternalAdminByEmail: func(context.Context, string) (db.InternalAdmin, error) {
			return sampleInternalAdmin(uuid.New(), "$2a$10$existinghash"), nil
		},
		createInternalPassReset: func(_ context.Context, arg db.CreateInternalPasswordResetParams) (db.InternalPasswordReset, error) {
			return db.InternalPasswordReset{ID: uuid.New(), AdminID: arg.AdminID}, nil
		},
	})
	stub.err = mailer.ErrNotConfigured

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/forgot-password", "",
		map[string]any{"email": "ops@example.com"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if decodeBody(t, rec)["sent"] != true {
		t.Errorf("body = %s, want {\"sent\":true}", rec.Body.String())
	}
}

func TestForgotInternalPassword_BadEmail(t *testing.T) {
	server, _ := newMailTestServer(t, &fakeStore{})

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/forgot-password", "",
		map[string]any{"email": "not-an-email"})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// ---- reset password ----

func TestResetInternalPassword_Rejects(t *testing.T) {
	adminID := uuid.New()
	tests := []struct {
		name   string
		reset  db.InternalPasswordReset
		lookup error
		claim  int64
	}{
		{
			name:   "unknown token",
			lookup: pgx.ErrNoRows,
		},
		{
			name: "expired",
			reset: db.InternalPasswordReset{
				ID:        uuid.New(),
				AdminID:   adminID,
				ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
			},
			claim: 1,
		},
		{
			name: "already used",
			reset: db.InternalPasswordReset{
				ID:        uuid.New(),
				AdminID:   adminID,
				ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
				UsedAt:    pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
			},
			claim: 1,
		},
		{
			// Two redemptions raced; the UPDATE ... WHERE used_at IS NULL matched
			// no rows, so this caller lost and must not get a password change.
			name: "lost the claim race",
			reset: db.InternalPasswordReset{
				ID:        uuid.New(),
				AdminID:   adminID,
				ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
			},
			claim: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newTestServer(t, &fakeStore{
				getInternalPassResetByHash: func(context.Context, string) (db.InternalPasswordReset, error) {
					return tt.reset, tt.lookup
				},
				markInternalPassResetUsed: func(context.Context, uuid.UUID) (int64, error) {
					return tt.claim, nil
				},
				updateInternalAdminPass: func(context.Context, db.UpdateInternalAdminPasswordParams) (db.InternalAdmin, error) {
					t.Fatal("must not change a password on an unusable reset token")
					return db.InternalAdmin{}, nil
				},
			})

			rec := serveJSON(t, server, http.MethodPost, "/internal/auth/reset-password", "",
				map[string]any{"token": "some-token", "new_password": "a-new-password"})

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			// One message for every failure mode, so a caller cannot tell them apart.
			if msg, _ := decodeBody(t, rec)["error"].(string); msg != "this reset link is invalid or has expired" {
				t.Errorf("error = %q, want the collapsed failure message", msg)
			}
		})
	}
}

func TestResetInternalPassword_ClaimsTokenAndUpdatesPassword(t *testing.T) {
	adminID, resetID := uuid.New(), uuid.New()
	writes := &passwordChangeStore{}
	var lookedUpHash string
	var claimedID uuid.UUID
	store := &fakeStore{
		getInternalPassResetByHash: func(_ context.Context, hash string) (db.InternalPasswordReset, error) {
			lookedUpHash = hash
			return db.InternalPasswordReset{
				ID:        resetID,
				AdminID:   adminID,
				TokenHash: hash,
				ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
			}, nil
		},
		markInternalPassResetUsed: func(_ context.Context, id uuid.UUID) (int64, error) {
			claimedID = id
			if !writes.updateCalled {
				return 1, nil
			}
			t.Error("token was claimed after the password write, not before")
			return 1, nil
		},
	}
	writes.attach(store)
	server := newTestServer(t, store)

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/reset-password", "",
		map[string]any{"token": "raw-token-from-the-link", "new_password": "a-new-password"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if decodeBody(t, rec)["password_updated"] != true {
		t.Error("expected password_updated: true")
	}
	// The lookup is by digest, never by the raw token from the link.
	sum := sha256.Sum256([]byte("raw-token-from-the-link"))
	if want := hex.EncodeToString(sum[:]); lookedUpHash != want {
		t.Errorf("looked up %q, want sha256(token) = %q", lookedUpHash, want)
	}
	if claimedID != resetID {
		t.Errorf("claimed reset %s, want %s", claimedID, resetID)
	}
	if writes.updated.ID != adminID {
		t.Errorf("updated admin = %s, want the token's admin %s", writes.updated.ID, adminID)
	}
	if err := util.CheckPassword("a-new-password", writes.updated.PasswordHash); err != nil {
		t.Errorf("stored hash does not verify against the new password: %v", err)
	}
	if len(writes.sessionsFor) != 1 || writes.sessionsFor[0] != adminID {
		t.Errorf("sessions revoked for %v, want exactly [%s]", writes.sessionsFor, adminID)
	}
}

func TestResetInternalPassword_ShortPassword(t *testing.T) {
	server := newTestServer(t, &fakeStore{
		getInternalPassResetByHash: func(context.Context, string) (db.InternalPasswordReset, error) {
			t.Fatal("must not look up a token on a binding error")
			return db.InternalPasswordReset{}, nil
		},
	})

	rec := serveJSON(t, server, http.MethodPost, "/internal/auth/reset-password", "",
		map[string]any{"token": "some-token", "new_password": "short7c"})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

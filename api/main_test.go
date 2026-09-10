package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/util"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// fakeStore is a db.Repository whose store methods are backed by optional
// function fields. Methods left nil fall through to the embedded (nil) Querier
// and panic if a handler calls them, so each test wires only what it exercises.
type fakeStore struct {
	db.Repository
	createStoreFull         func(context.Context, db.CreateStoreFullParams) (db.Store, error)
	updateStoreFull         func(context.Context, db.UpdateStoreFullParams) (db.Store, error)
	deleteStore             func(context.Context, uuid.UUID) (int64, error)
	getStoreByID            func(context.Context, uuid.UUID) (db.Store, error)
	updateStoreProfile      func(context.Context, db.UpdateStoreProfileParams) (db.Store, error)
	getInternalAdminByEmail func(context.Context, string) (db.InternalAdmin, error)
	createInternalSession   func(context.Context, db.CreateInternalSessionParams) (db.InternalSession, error)
	getInternalSession      func(context.Context, uuid.UUID) (db.InternalSession, error)
	deleteInternalSession   func(context.Context, uuid.UUID) (int64, error)

	createInternalAdmin        func(context.Context, db.CreateInternalAdminParams) (db.InternalAdmin, error)
	getInternalAdminByID       func(context.Context, uuid.UUID) (db.InternalAdmin, error)
	listInternalAdmins         func(context.Context) ([]db.InternalAdmin, error)
	updateInternalAdminPass    func(context.Context, db.UpdateInternalAdminPasswordParams) (db.InternalAdmin, error)
	deleteInternalSessionsFor  func(context.Context, uuid.UUID) (int64, error)
	createInternalPassReset    func(context.Context, db.CreateInternalPasswordResetParams) (db.InternalPasswordReset, error)
	getInternalPassResetByHash func(context.Context, string) (db.InternalPasswordReset, error)
	markInternalPassResetUsed  func(context.Context, uuid.UUID) (int64, error)
	deleteInternalPassResets   func(context.Context, uuid.UUID) (int64, error)
	listWards                  func(context.Context) ([]db.Ward, error)
	listStoresMissingCoords    func(context.Context, int32) ([]db.Store, error)
	updateStoreCoords          func(context.Context, db.UpdateStoreCoordsParams) (int64, error)
	countStoresByNameWard      func(context.Context, db.CountStoresByNameWardParams) (int64, error)
	createMenuItem             func(context.Context, db.CreateMenuItemParams) (db.MenuItem, error)
	listMenuItemsByStore       func(context.Context, uuid.UUID) ([]db.MenuItem, error)
	updateMenuItem             func(context.Context, db.UpdateMenuItemParams) (db.MenuItem, error)
	deleteMenuItem             func(context.Context, db.DeleteMenuItemParams) (int64, error)
}

func (f *fakeStore) CreateStoreFull(ctx context.Context, arg db.CreateStoreFullParams) (db.Store, error) {
	return f.createStoreFull(ctx, arg)
}

func (f *fakeStore) UpdateStoreFull(ctx context.Context, arg db.UpdateStoreFullParams) (db.Store, error) {
	return f.updateStoreFull(ctx, arg)
}

func (f *fakeStore) DeleteStore(ctx context.Context, id uuid.UUID) (int64, error) {
	return f.deleteStore(ctx, id)
}

func (f *fakeStore) GetStoreByID(ctx context.Context, id uuid.UUID) (db.Store, error) {
	return f.getStoreByID(ctx, id)
}

func (f *fakeStore) UpdateStoreProfile(ctx context.Context, arg db.UpdateStoreProfileParams) (db.Store, error) {
	return f.updateStoreProfile(ctx, arg)
}

func (f *fakeStore) GetInternalAdminByEmail(ctx context.Context, email string) (db.InternalAdmin, error) {
	return f.getInternalAdminByEmail(ctx, email)
}

func (f *fakeStore) CreateInternalSession(ctx context.Context, arg db.CreateInternalSessionParams) (db.InternalSession, error) {
	return f.createInternalSession(ctx, arg)
}

func (f *fakeStore) GetInternalSession(ctx context.Context, id uuid.UUID) (db.InternalSession, error) {
	return f.getInternalSession(ctx, id)
}

func (f *fakeStore) DeleteInternalSession(ctx context.Context, id uuid.UUID) (int64, error) {
	return f.deleteInternalSession(ctx, id)
}

func (f *fakeStore) CreateInternalAdmin(ctx context.Context, arg db.CreateInternalAdminParams) (db.InternalAdmin, error) {
	return f.createInternalAdmin(ctx, arg)
}

func (f *fakeStore) GetInternalAdminByID(ctx context.Context, id uuid.UUID) (db.InternalAdmin, error) {
	return f.getInternalAdminByID(ctx, id)
}

func (f *fakeStore) ListInternalAdmins(ctx context.Context) ([]db.InternalAdmin, error) {
	return f.listInternalAdmins(ctx)
}

func (f *fakeStore) UpdateInternalAdminPassword(ctx context.Context, arg db.UpdateInternalAdminPasswordParams) (db.InternalAdmin, error) {
	return f.updateInternalAdminPass(ctx, arg)
}

func (f *fakeStore) DeleteInternalSessionsForAdmin(ctx context.Context, adminID uuid.UUID) (int64, error) {
	return f.deleteInternalSessionsFor(ctx, adminID)
}

func (f *fakeStore) CreateInternalPasswordReset(ctx context.Context, arg db.CreateInternalPasswordResetParams) (db.InternalPasswordReset, error) {
	return f.createInternalPassReset(ctx, arg)
}

func (f *fakeStore) GetInternalPasswordResetByTokenHash(ctx context.Context, tokenHash string) (db.InternalPasswordReset, error) {
	return f.getInternalPassResetByHash(ctx, tokenHash)
}

func (f *fakeStore) MarkInternalPasswordResetUsed(ctx context.Context, id uuid.UUID) (int64, error) {
	return f.markInternalPassResetUsed(ctx, id)
}

func (f *fakeStore) DeleteInternalPasswordResetsForAdmin(ctx context.Context, adminID uuid.UUID) (int64, error) {
	return f.deleteInternalPassResets(ctx, adminID)
}

func (f *fakeStore) ListWards(ctx context.Context) ([]db.Ward, error) {
	return f.listWards(ctx)
}

func (f *fakeStore) ListStoresMissingCoords(ctx context.Context, limit int32) ([]db.Store, error) {
	return f.listStoresMissingCoords(ctx, limit)
}

func (f *fakeStore) UpdateStoreCoords(ctx context.Context, arg db.UpdateStoreCoordsParams) (int64, error) {
	return f.updateStoreCoords(ctx, arg)
}

func (f *fakeStore) CountStoresByNameWard(ctx context.Context, arg db.CountStoresByNameWardParams) (int64, error) {
	return f.countStoresByNameWard(ctx, arg)
}

func (f *fakeStore) CreateMenuItem(ctx context.Context, arg db.CreateMenuItemParams) (db.MenuItem, error) {
	return f.createMenuItem(ctx, arg)
}

func (f *fakeStore) ListMenuItemsByStore(ctx context.Context, storeID uuid.UUID) ([]db.MenuItem, error) {
	return f.listMenuItemsByStore(ctx, storeID)
}

func (f *fakeStore) UpdateMenuItem(ctx context.Context, arg db.UpdateMenuItemParams) (db.MenuItem, error) {
	return f.updateMenuItem(ctx, arg)
}

func (f *fakeStore) DeleteMenuItem(ctx context.Context, arg db.DeleteMenuItemParams) (int64, error) {
	return f.deleteMenuItem(ctx, arg)
}

// serveRaw runs a request with a verbatim (non-JSON) body, e.g. a CSV upload.
func serveRaw(t *testing.T, server *Server, method, path, auth, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(method, path, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	if auth != "" {
		req.Header.Set("authorization", auth)
	}
	rec := httptest.NewRecorder()
	server.router.ServeHTTP(rec, req)
	return rec
}

// testWards is the minimal ward set used by import tests.
func testWards(context.Context) ([]db.Ward, error) {
	return []db.Ward{
		{ID: 13, NameJa: "渋谷区", NameEn: "Shibuya"},
		{ID: 4, NameJa: "新宿区", NameEn: "Shinjuku"},
	}, nil
}

func newTestServer(t *testing.T, store db.Repository) *Server {
	t.Helper()
	config := util.Config{
		TokenSymmetricKey:   "12345678901234567890123456789012", // 32 chars: HS256 minimum
		AccessTokenDuration: time.Minute,
	}
	server, err := NewServer(config, store)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return server
}

// authHeader mints a Bearer token for the given role (and optional store scope),
// formatted as the middleware expects.
func authHeader(t *testing.T, server *Server, role string, storeID *uuid.UUID) string {
	t.Helper()
	tok, _, err := server.tokenMaker.CreateRoleToken(uuid.New(), "actor@example.com", role, storeID, time.Minute)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return "bearer " + tok
}

// serveJSON runs a JSON request through the router and returns the recorder.
// A nil body sends an empty request body; a non-nil body is JSON-encoded.
func serveJSON(t *testing.T, server *Server, method, path, auth string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		payload = raw
	}
	req, err := http.NewRequest(method, path, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("authorization", auth)
	}
	rec := httptest.NewRecorder()
	server.router.ServeHTTP(rec, req)
	return rec
}

// decodeBody unmarshals a recorder's JSON body into a generic map for assertions.
func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return out
}

// sampleStore returns a valid stores row. opening_hours is defaulted so the
// response builder (parseOpeningHours) can parse it.
func sampleStore(id uuid.UUID) db.Store {
	return db.Store{
		ID:           id,
		WardID:       13,
		Name:         "Sample",
		Address:      "Shibuya 1-1",
		OpeningHours: []byte("[]"),
		Status:       db.StoreStatusApproved,
		GfStatus:     db.GfStatusOnRequest,
		PriceLevel:   2,
	}
}

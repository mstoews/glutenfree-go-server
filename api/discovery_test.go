package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/discovery"
	"github.com/mstoews/glutenfree-server/token"
)

type discoveryRepo struct {
	*fakeStore
	save func(context.Context, db.CreateStoreFullParams) (db.Store, bool, error)
}

func (r *discoveryRepo) CreateDiscoveryDraft(c context.Context, p db.CreateStoreFullParams) (db.Store, bool, error) {
	return r.save(c, p)
}

type fakeDiscovery struct{}

func (fakeDiscovery) Search(context.Context, string, int) ([]string, error) {
	return []string{"https://example.com"}, nil
}
func (fakeDiscovery) Capture(_ context.Context, u string) (discovery.Candidate, error) {
	if u == "https://fail.example" {
		return discovery.Candidate{}, errors.New("capture failed")
	}
	return discovery.Candidate{Name: "Rice Cafe", Address: "Shibuya 1-2-3", SourceURL: u, Evidence: "GF options", Images: []string{"https://example.com/photo.jpg"}, MenuText: "Rice bread ¥500"}, nil
}
func TestDiscoveryBatch(t *testing.T) {
	calls := 0
	repo := &discoveryRepo{fakeStore: &fakeStore{listWards: func(context.Context) ([]db.Ward, error) {
		return []db.Ward{{ID: 1, NameEn: "Shibuya", NameJa: "渋谷区"}}, nil
	}}, save: func(_ context.Context, p db.CreateStoreFullParams) (db.Store, bool, error) {
		calls++
		if p.Status != db.StoreStatusDraft || p.GfStatus != db.GfStatusOnRequest || p.PhotoUrl.Valid {
			t.Fatalf("unsafe defaults %+v", p)
		}
		return db.Store{ID: uuid.New()}, calls == 1, nil
	}}
	s := newTestServer(t, repo)
	s.discovery = fakeDiscovery{}
	body := map[string]any{"ward_id": 1, "urls": []string{"https://example.com", "https://fail.example", "https://duplicate.example"}}
	rec := serveJSON(t, s, http.MethodPost, "/internal/discovery", authHeader(t, s, token.RoleInternal, nil), body)
	if rec.Code != 200 || calls != 2 {
		t.Fatalf("%d %s calls=%d", rec.Code, rec.Body, calls)
	}
	for _, want := range []string{`"status":"created"`, `"status":"failed"`, `"status":"duplicate"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("missing %s: %s", want, rec.Body)
		}
	}
}
func TestDiscoveryValidationAndAuth(t *testing.T) {
	repo := &discoveryRepo{fakeStore: &fakeStore{listWards: func(context.Context) ([]db.Ward, error) { return []db.Ward{{ID: 1, NameEn: "Shibuya"}}, nil }}}
	s := newTestServer(t, repo)
	s.discovery = fakeDiscovery{}
	for _, tc := range []struct {
		auth   string
		body   map[string]any
		status int
	}{
		{"", map[string]any{"ward_id": 1}, 401},
		{authHeader(t, s, token.RoleStoreAdmin, nil), map[string]any{"ward_id": 1}, 403},
		{authHeader(t, s, token.RoleInternal, nil), map[string]any{"ward_id": 1, "urls": []string{"http://127.0.0.1"}}, 400},
		{authHeader(t, s, token.RoleInternal, nil), map[string]any{"ward_id": 999}, 400},
		{authHeader(t, s, token.RoleInternal, nil), map[string]any{"ward_id": 1}, 503},
	} {
		rec := serveJSON(t, s, http.MethodPost, "/internal/discovery", tc.auth, tc.body)
		if rec.Code != tc.status {
			t.Errorf("got %d want %d: %s", rec.Code, tc.status, rec.Body)
		}
	}
}

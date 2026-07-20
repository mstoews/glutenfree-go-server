package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/mstoews/glutenfree-server/blobstore"
	"github.com/mstoews/glutenfree-server/token"
)

// fakeUploader records what it was asked to store.
type fakeUploader struct {
	gotPrefix, gotContentType string
	gotBytes                  []byte
	err                       error
}

func (f *fakeUploader) Upload(_ context.Context, prefix, contentType string, r io.Reader) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	b, _ := io.ReadAll(r)
	f.gotPrefix, f.gotContentType, f.gotBytes = prefix, contentType, b
	return "https://storage.googleapis.com/gurufuri-images/" + prefix + "/" + uuid.NewString() + ".jpg", nil
}

// tinyPNG is a real 1x1 PNG — the magic bytes matter, since the handler sniffs.
var tinyPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

// uploadReq builds a multipart request with the given file bytes.
func uploadReq(t *testing.T, server *Server, path, filename string, body []byte, declaredType string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
	if declaredType != "" {
		h["Content-Type"] = []string{declaredType}
	}
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatalf("write part: %v", err)
	}
	w.Close()

	req, err := http.NewRequest(http.MethodPost, path, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("authorization", authHeader(t, server, token.RoleInternal, nil))
	rec := httptest.NewRecorder()
	server.router.ServeHTTP(rec, req)
	return rec
}

func TestUploadImage_StoresAndReturnsURL(t *testing.T) {
	up := &fakeUploader{}
	server := newTestServer(t, &fakeStore{})
	server.uploader = up

	rec := uploadReq(t, server, "/internal/uploads/image?kind=store", "photo.png", tinyPNG, "image/png")

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	url, _ := decodeBody(t, rec)["url"].(string)
	if url == "" {
		t.Fatal("no url returned")
	}
	if up.gotPrefix != "stores" {
		t.Errorf("prefix = %q, want stores", up.gotPrefix)
	}
	if up.gotContentType != "image/png" {
		t.Errorf("contentType = %q, want image/png", up.gotContentType)
	}
	// The whole file must reach the uploader — sniffing reads the head, so a
	// missing rewind would truncate it.
	if !bytes.Equal(up.gotBytes, tinyPNG) {
		t.Errorf("uploaded %d bytes, want the full %d (rewind after sniff?)", len(up.gotBytes), len(tinyPNG))
	}
}

func TestUploadImage_MenuKindUsesMenuPrefix(t *testing.T) {
	up := &fakeUploader{}
	server := newTestServer(t, &fakeStore{})
	server.uploader = up

	if rec := uploadReq(t, server, "/internal/uploads/image?kind=menu", "meal.png", tinyPNG, "image/png"); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if up.gotPrefix != "menu" {
		t.Errorf("prefix = %q, want menu", up.gotPrefix)
	}
}

// A caller must not be able to write outside the known prefixes.
func TestUploadImage_RejectsUnknownKind(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	server.uploader = &fakeUploader{}

	if rec := uploadReq(t, server, "/internal/uploads/image?kind=../secrets", "x.png", tinyPNG, "image/png"); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// The declared Content-Type is attacker-controlled; the magic bytes decide.
func TestUploadImage_RejectsNonImageClaimingToBeOne(t *testing.T) {
	up := &fakeUploader{}
	server := newTestServer(t, &fakeStore{})
	server.uploader = up

	evil := []byte("#!/bin/sh\necho pwned\n")
	rec := uploadReq(t, server, "/internal/uploads/image?kind=store", "photo.png", evil, "image/png")

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415 for a non-image", rec.Code)
	}
	if up.gotBytes != nil {
		t.Error("a non-image must never reach the bucket")
	}
}

func TestUploadImage_MissingFileField(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	server.uploader = &fakeUploader{}

	req, _ := http.NewRequest(http.MethodPost, "/internal/uploads/image", bytes.NewReader(nil))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=zzz")
	req.Header.Set("authorization", authHeader(t, server, token.RoleInternal, nil))
	rec := httptest.NewRecorder()
	server.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// Unconfigured bucket => 501, mirroring the StoreKit/Apple routes.
func TestUploadImage_NotConfigured(t *testing.T) {
	server := newTestServer(t, &fakeStore{}) // uploader left nil

	if rec := uploadReq(t, server, "/internal/uploads/image", "p.png", tinyPNG, "image/png"); rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestUploadImage_UploaderErrorIs500(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	server.uploader = &fakeUploader{err: errors.New("bucket exploded")}

	if rec := uploadReq(t, server, "/internal/uploads/image", "p.png", tinyPNG, "image/png"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestUploadImage_UnsupportedTypeFromUploaderIs415(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	server.uploader = &fakeUploader{err: blobstore.ErrUnsupportedType}

	if rec := uploadReq(t, server, "/internal/uploads/image", "p.png", tinyPNG, "image/png"); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
}

func TestUploadImage_RequiresInternalRole(t *testing.T) {
	server := newTestServer(t, &fakeStore{})
	server.uploader = &fakeUploader{}
	storeID := uuid.New()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", "p.png")
	_, _ = part.Write(tinyPNG)
	w.Close()

	req, _ := http.NewRequest(http.MethodPost, "/internal/uploads/image", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("authorization", authHeader(t, server, token.RoleStoreAdmin, &storeID))
	rec := httptest.NewRecorder()
	server.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// Package blobstore stores uploaded images (restaurant photos, and later meal
// photos) in Google Cloud Storage and returns the public URL to persist in
// stores.photo_url / menu_items.image_url.
//
// Uploads are proxied through the API rather than going direct-to-bucket with a
// signed URL. On Cloud Run the runtime credentials come from the metadata
// server and carry no private key, so signing would need the IAM SignBlob API
// plus a Token Creator role. The admin uploads a handful of client-resized
// images (~300KB), so proxying costs nothing and keeps auth to the existing
// bearer middleware. Swap to signed URLs if volume ever justifies it.
package blobstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
)

// ErrUnsupportedType is returned for a content type we won't store.
var ErrUnsupportedType = errors.New("unsupported image type")

// extByContentType is the allow-list: what the admin may upload, and the
// extension we give the object.
var extByContentType = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// Uploader stores an image and returns its public URL.
type Uploader interface {
	Upload(ctx context.Context, prefix, contentType string, r io.Reader) (string, error)
}

// GCS uploads to a public Cloud Storage bucket.
type GCS struct {
	client *storage.Client
	bucket string
}

// NewGCS connects using Application Default Credentials — the Cloud Run runtime
// service account in production, `gcloud auth application-default login` locally.
func NewGCS(ctx context.Context, bucket string) (*GCS, error) {
	if bucket == "" {
		return nil, errors.New("bucket name is empty")
	}
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage client: %w", err)
	}
	return &GCS{client: client, bucket: bucket}, nil
}

// Upload writes r to <prefix>/<uuid><ext> and returns its public URL. The name
// is a random UUID: it keeps paths unguessable and avoids collisions, since the
// original filename is attacker-controlled and worthless to us.
func (g *GCS) Upload(ctx context.Context, prefix, contentType string, r io.Reader) (string, error) {
	ext, ok := extByContentType[contentType]
	if !ok {
		return "", ErrUnsupportedType
	}

	object := path.Join(prefix, uuid.NewString()+ext)
	w := g.client.Bucket(g.bucket).Object(object).NewWriter(ctx)
	w.ContentType = contentType
	// Images are immutable (a new upload gets a new UUID), so let clients and
	// CDNs keep them for a year.
	w.CacheControl = "public, max-age=31536000, immutable"

	if _, err := io.Copy(w, r); err != nil {
		_ = w.Close()
		return "", fmt.Errorf("upload: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("finalise upload: %w", err)
	}
	return fmt.Sprintf("https://storage.googleapis.com/%s/%s", g.bucket, object), nil
}

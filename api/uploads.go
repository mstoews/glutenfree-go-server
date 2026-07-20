package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mstoews/glutenfree-server/blobstore"
)

var errUploadsNotConfigured = errors.New("image uploads are not configured")

// maxUploadBytes caps an upload. The admin resizes client-side before sending
// (~300KB typical), so this is a generous backstop against an unresized
// original, not the expected size.
const maxUploadBytes = 8 << 20 // 8 MiB

// uploadPrefixes maps the ?kind= query to an object prefix. Restricted to a
// known set so a caller can't write to arbitrary paths in the bucket.
var uploadPrefixes = map[string]string{
	"store": "stores",
	"menu":  "menu",
}

type uploadResponse struct {
	URL string `json:"url"`
}

// internalUploadImage stores an uploaded image and returns its public URL for
// the caller to persist in stores.photo_url / menu_items.image_url. The image
// is sent as multipart form field "file".
//
// Note the bucket is public-read, which suits restaurant photos (the store list
// and detail are free to any signed-in user). Meal photos are the only paid
// content, so `menu` uploads rely on unguessable UUID paths rather than real
// access control -- if that isn't enough, they need signed read URLs.
func (server *Server) internalUploadImage(ctx *gin.Context) {
	if server.uploader == nil {
		ctx.JSON(http.StatusNotImplemented, errorResponse(errUploadsNotConfigured))
		return
	}

	kind := ctx.DefaultQuery("kind", "store")
	prefix, ok := uploadPrefixes[kind]
	if !ok {
		ctx.JSON(http.StatusBadRequest, errorResponse(errors.New(`kind must be "store" or "menu"`)))
		return
	}

	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxUploadBytes)
	header, err := ctx.FormFile("file")
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(errors.New("expected a multipart form field \"file\"")))
		return
	}
	if header.Size > maxUploadBytes {
		ctx.JSON(http.StatusRequestEntityTooLarge, errorResponse(errors.New("image is too large (max 8MB)")))
		return
	}

	file, err := header.Open()
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}
	defer file.Close()

	// Trust the sniffed type over the client-declared one: Content-Type here is
	// attacker-controlled, and the allow-list is what keeps non-images out.
	contentType, err := sniffImageType(file)
	if err != nil {
		ctx.JSON(http.StatusUnsupportedMediaType, errorResponse(err))
		return
	}

	url, err := server.uploader.Upload(ctx, prefix, contentType, file)
	if err != nil {
		if errors.Is(err, blobstore.ErrUnsupportedType) {
			ctx.JSON(http.StatusUnsupportedMediaType, errorResponse(err))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	ctx.JSON(http.StatusCreated, uploadResponse{URL: url})
}

// sniffImageType detects the real content type from the file's magic bytes and
// rewinds so the caller can still read the whole file.
func sniffImageType(f multipartFile) (string, error) {
	head := make([]byte, 512)
	n, err := f.Read(head)
	if err != nil && n == 0 {
		return "", errors.New("could not read the uploaded file")
	}
	if _, err := f.Seek(0, 0); err != nil {
		return "", errors.New("could not rewind the uploaded file")
	}

	switch ct := http.DetectContentType(head[:n]); ct {
	case "image/jpeg", "image/png", "image/webp":
		return ct, nil
	default:
		return "", errors.New("only JPEG, PNG and WebP images are accepted")
	}
}

// multipartFile is the subset of multipart.File we need (read + rewind).
type multipartFile interface {
	Read(p []byte) (int, error)
	Seek(offset int64, whence int) (int64, error)
}

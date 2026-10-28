package catalogadmin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/pinnacleverve/api/internal/store"
)

const (
	MaxImageBytes    = 10 << 20
	MaxImagesPerItem = 12
)

var ErrTooLarge = errors.New("catalogadmin: image too large")

// Allowed types are detected from the bytes, never taken from the client's Content-Type.
var imageExt = map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp"}

// UploadImage stores the file, then records it (with audit) in one transaction. If the database step
// fails the stored file is removed again.
func (s *Service) UploadImage(ctx context.Context, productID uuid.UUID, alt string, body io.Reader) error {
	if _, err := authorize(ctx); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(body, MaxImageBytes+1))
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) || len(data) > MaxImageBytes {
		return ErrTooLarge
	}
	if err != nil {
		return err
	}
	ext, ok := imageExt[http.DetectContentType(data)]
	if !ok {
		return invalid("Upload a JPEG, PNG or WebP image.")
	}
	if _, err := s.q.AdminGetProduct(ctx, productID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if n, err := s.q.AdminCountProductImages(ctx, productID); err != nil {
		return err
	} else if n >= MaxImagesPerItem {
		return invalid("A product can have at most %d images. Remove one first.", MaxImagesPerItem)
	}

	b := make([]byte, 12)
	_, _ = rand.Read(b)
	key := "products/" + productID.String() + "/" + hex.EncodeToString(b) + "." + ext
	url, err := s.store.Put(ctx, key, bytes.NewReader(data))
	if err != nil {
		return err
	}
	err = s.write(ctx, "product.image_upload", "product", func(q *store.Queries) (string, any, error) {
		_, err := q.AdminInsertProductImage(ctx, store.AdminInsertProductImageParams{ProductID: productID, Url: url, Alt: strings.TrimSpace(alt), StorageKey: key})
		return productID.String(), map[string]any{"url": url, "bytes": len(data)}, err
	})
	if err != nil {
		_ = s.store.Delete(ctx, key)
	}
	return err
}

type ImageOrder struct {
	ID  uuid.UUID
	Alt string
}

// SetImages applies a new order and alt texts; the list must contain every image exactly once.
func (s *Service) SetImages(ctx context.Context, productID uuid.UUID, order []ImageOrder) error {
	return s.write(ctx, "product.images", "product", func(q *store.Queries) (string, any, error) {
		if _, err := q.AdminGetProduct(ctx, productID); errors.Is(err, pgx.ErrNoRows) {
			return "", nil, ErrNotFound
		} else if err != nil {
			return "", nil, err
		}
		current, err := q.AdminListProductImages(ctx, productID)
		if err != nil {
			return "", nil, err
		}
		if len(current) != len(order) {
			return "", nil, invalid("List every image of the product exactly once.")
		}
		seen := map[uuid.UUID]bool{}
		for i, o := range order {
			if seen[o.ID] {
				return "", nil, invalid("List every image of the product exactly once.")
			}
			seen[o.ID] = true
			n, err := q.AdminUpdateProductImage(ctx, store.AdminUpdateProductImageParams{ID: o.ID, ProductID: productID, Alt: strings.TrimSpace(o.Alt), Position: int32(i)})
			if err != nil {
				return "", nil, err
			}
			if n == 0 {
				return "", nil, invalid("Image %s doesn't belong to this product.", o.ID)
			}
		}
		return productID.String(), map[string]any{"images": len(order)}, nil
	})
}

// DeleteImage removes the row and, after commit, the stored file (fixture images have no key).
func (s *Service) DeleteImage(ctx context.Context, productID, imageID uuid.UUID) error {
	var key string
	err := s.write(ctx, "product.image_delete", "product", func(q *store.Queries) (string, any, error) {
		k, err := q.AdminDeleteProductImage(ctx, store.AdminDeleteProductImageParams{ID: imageID, ProductID: productID})
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, ErrNotFound
		}
		key = k
		return productID.String(), map[string]any{"imageId": imageID}, err
	})
	if err == nil && key != "" {
		_ = s.store.Delete(ctx, key) // best effort: an orphaned file is harmless, a missing row isn't
	}
	return err
}

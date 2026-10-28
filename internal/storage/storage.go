// Package storage abstracts where uploaded media lives (decision D35). Local disk is used until hosting
// is chosen; an S3-compatible implementation (R2/S3) will satisfy the same interface.
package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var ErrBadKey = errors.New("storage: invalid key")

// Store persists objects by key and returns the public URL clients load them from.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader) (url string, err error)
	Delete(ctx context.Context, key string) error
}

// Keys are generated server-side; this pattern rejects anything that could escape the root.
var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9/_-]*\.(jpg|png|webp)$`)

func validKey(key string) bool {
	return keyPattern.MatchString(key) && !strings.Contains(key, "..") && !strings.Contains(key, "//")
}

// Local stores files under Dir and serves them at URLPrefix (e.g. /media).
type Local struct {
	Dir       string
	URLPrefix string
}

func (l Local) Put(_ context.Context, key string, r io.Reader) (string, error) {
	if !validKey(key) {
		return "", ErrBadKey
	}
	dst := filepath.Join(l.Dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return path.Join(l.URLPrefix, key), nil
}

func (l Local) Delete(_ context.Context, key string) error {
	if !validKey(key) {
		return ErrBadKey
	}
	err := os.Remove(filepath.Join(l.Dir, filepath.FromSlash(key)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Handler serves stored files (GET/HEAD only, no directory listings). Keys are random and never
// reused, so responses are cacheable forever.
func (l Local) Handler() http.Handler {
	files := http.StripPrefix(l.URLPrefix+"/", http.FileServer(http.Dir(l.Dir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, l.URLPrefix+"/")
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || !validKey(key) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	})
}

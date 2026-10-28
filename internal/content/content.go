// Package content stores editable storefront copy (phase A6): the site chrome (announcement bar,
// navigation, footer), homepage text and content pages such as the legal pages.
//
// The API stores what editors save; the storefront ships the original copy and uses it until a document
// exists. Page bodies are plain text (the storefront renders paragraphs, "## " headings and "- " bullets),
// never HTML, and links may only point to site paths, https, mailto or tel. Saving revalidates the
// storefront's "content" cache tag.
package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/platform/database"
	"github.com/pinnacleverve/api/internal/store"
)

var ErrNotFound = errors.New("content: not found")

// Documents are the editable copy blocks; each has its own schema in the API contract.
var Documents = []string{"site", "home"}

// InvalidError is a business-rule violation with a message an editor can act on.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &InvalidError{Msg: fmt.Sprintf(format, a...)} }

// Revalidator tells the storefront that cached pages using the given tags are stale.
type Revalidator interface{ Revalidate(tags ...string) }

type Service struct {
	pool  *pgxpool.Pool
	q     *store.Queries
	audit *admin.Service
	rv    Revalidator
}

func NewService(pool *pgxpool.Pool, q *store.Queries, audit *admin.Service, rv Revalidator) *Service {
	return &Service{pool: pool, q: q, audit: audit, rv: rv}
}

func authorize(ctx context.Context) (store.AdminUser, error) {
	me := admin.Current(ctx)
	if !admin.Can(me.Role, admin.AreaContent) {
		return me, admin.ErrForbidden
	}
	return me, nil
}

var (
	sitePath = regexp.MustCompile(`^/([^/\\\s][^\s]*)?$`) // "/collections", "/" — not "//evil.example"
	external = regexp.MustCompile(`^(https://[^\s/]+[^\s]*|mailto:[^\s]+|tel:\+?[0-9 ()-]+)$`)
)

// CheckLinks rejects anything but site paths, https, mailto and tel (e.g. javascript: URLs).
func CheckLinks(hrefs ...string) error {
	for _, h := range hrefs {
		if !sitePath.MatchString(h) && !external.MatchString(h) {
			return invalid("%q isn't an allowed link. Use a site path like /collections, or an https://, mailto: or tel: link.", h)
		}
	}
	return nil
}

type Document struct {
	Data      json.RawMessage
	UpdatedAt time.Time
	UpdatedBy string
}

// Document returns a saved copy block; public reads don't need a session.
func (s *Service) Document(ctx context.Context, key string) (Document, error) {
	row, err := s.q.GetContentDocument(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrNotFound
	}
	if err != nil {
		return Document{}, err
	}
	return Document{Data: row.Data, UpdatedAt: row.UpdatedAt.Time, UpdatedBy: row.UpdatedByName}, nil
}

func (s *Service) AdminDocument(ctx context.Context, key string) (Document, error) {
	if _, err := authorize(ctx); err != nil {
		return Document{}, err
	}
	return s.Document(ctx, key)
}

// SaveDocument stores a block after checking every link it contains (the contract checks the shape).
func (s *Service) SaveDocument(ctx context.Context, key string, data any, links []string) (Document, error) {
	me, err := authorize(ctx)
	if err != nil {
		return Document{}, err
	}
	if err := CheckLinks(links...); err != nil {
		return Document{}, err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return Document{}, err
	}
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.UpsertContentDocument(ctx, store.UpsertContentDocumentParams{Key: key, Data: raw, UpdatedBy: pgtype.UUID{Bytes: me.ID, Valid: true}}); err != nil {
			return err
		}
		s.audit.AuditWith(ctx, q, &me.ID, "content.update", "content", key, map[string]any{"bytes": len(raw)})
		return nil
	})
	if err != nil {
		return Document{}, err
	}
	s.rv.Revalidate("content")
	return s.Document(ctx, key)
}

// ---- pages ----

type Page struct {
	Slug, Title, Body string
	Published         bool
	UpdatedAt         time.Time
	UpdatedBy         string
}

func (s *Service) Pages(ctx context.Context) ([]store.ListContentPagesRow, error) {
	if _, err := authorize(ctx); err != nil {
		return nil, err
	}
	return s.q.ListContentPages(ctx)
}

func (s *Service) AdminPage(ctx context.Context, slug string) (Page, error) {
	if _, err := authorize(ctx); err != nil {
		return Page{}, err
	}
	row, err := s.q.GetContentPage(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Page{}, ErrNotFound
	}
	if err != nil {
		return Page{}, err
	}
	return Page{Slug: row.Slug, Title: row.Title, Body: row.Body, Published: row.Published, UpdatedAt: row.UpdatedAt.Time, UpdatedBy: row.UpdatedByName}, nil
}

// PublishedPage is the public read; unpublished drafts are not found.
func (s *Service) PublishedPage(ctx context.Context, slug string) (Page, error) {
	row, err := s.q.GetPublishedContentPage(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Page{}, ErrNotFound
	}
	if err != nil {
		return Page{}, err
	}
	return Page{Slug: row.Slug, Title: row.Title, Body: row.Body, Published: true, UpdatedAt: row.UpdatedAt.Time}, nil
}

func (s *Service) SavePage(ctx context.Context, p Page) (Page, error) {
	me, err := authorize(ctx)
	if err != nil {
		return Page{}, err
	}
	p.Title, p.Body = strings.TrimSpace(p.Title), strings.TrimSpace(strings.ReplaceAll(p.Body, "\r\n", "\n"))
	if p.Title == "" {
		return Page{}, invalid("Give the page a title.")
	}
	if p.Published && p.Body == "" {
		return Page{}, invalid("Add the page text before publishing it.")
	}
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.UpsertContentPage(ctx, store.UpsertContentPageParams{
			Slug: p.Slug, Title: p.Title, Body: p.Body, Published: p.Published, UpdatedBy: pgtype.UUID{Bytes: me.ID, Valid: true},
		}); err != nil {
			return err
		}
		s.audit.AuditWith(ctx, q, &me.ID, "page.update", "page", p.Slug, map[string]any{"published": p.Published, "chars": len(p.Body)})
		return nil
	})
	if err != nil {
		return Page{}, err
	}
	s.rv.Revalidate("content")
	return s.AdminPage(ctx, p.Slug)
}

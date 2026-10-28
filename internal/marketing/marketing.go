// Package marketing implements review moderation and the newsletter list for the admin (phase A5).
//
// Moderators decide whether a review is shown; they never edit what a customer wrote, and staff cannot
// author reviews. Publishing or withdrawing a review recomputes the product's rating in the same
// transaction, then the storefront's cached product data is revalidated.
package marketing

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/platform/database"
	"github.com/pinnacleverve/api/internal/store"
)

const PerPage = 25

var ErrNotFound = errors.New("marketing: not found")

// ReviewStatuses a moderator can set.
var ReviewStatuses = []string{"pending", "published", "rejected"}

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

func text(p *string) pgtype.Text {
	if p == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *p, Valid: true}
}

// ---- reviews ----

type ReviewQuery struct {
	Q           string
	Status      *string
	ProductID   *uuid.UUID
	Rating      *int
	WrittenOnly bool
	Page        int
}

func (s *Service) Reviews(ctx context.Context, rq ReviewQuery) (rows []store.AdminListReviewsRow, total, pending int, err error) {
	if _, err := authorize(ctx); err != nil {
		return nil, 0, 0, err
	}
	p := store.AdminListReviewsParams{
		Q: strings.TrimSpace(rq.Q), Status: text(rq.Status), WrittenOnly: rq.WrittenOnly,
		Lim: PerPage, Off: int32((rq.Page - 1) * PerPage),
	}
	if rq.ProductID != nil {
		p.ProductID = pgtype.UUID{Bytes: *rq.ProductID, Valid: true}
	}
	if rq.Rating != nil {
		p.Rating = pgtype.Int2{Int16: int16(*rq.Rating), Valid: true}
	}
	if rows, err = s.q.AdminListReviews(ctx, p); err != nil {
		return nil, 0, 0, err
	}
	n, err := s.q.AdminCountPendingReviews(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(rows) > 0 {
		total = int(rows[0].TotalCount)
	}
	return rows, total, int(n), nil
}

// SetReviewStatus publishes, rejects or returns a review to pending, and keeps the product rating in step.
func (s *Service) SetReviewStatus(ctx context.Context, id uuid.UUID, status string) error {
	me, err := authorize(ctx)
	if err != nil {
		return err
	}
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		before, err := q.AdminGetReviewStatus(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		row, err := q.AdminSetReviewStatus(ctx, store.AdminSetReviewStatusParams{ID: id, Status: status, ModeratedBy: pgtype.UUID{Bytes: me.ID, Valid: true}})
		if err != nil {
			return err
		}
		if err := q.RecomputeProductRating(ctx, row.ProductID); err != nil {
			return err
		}
		s.audit.AuditWith(ctx, q, &me.ID, "review.status", "review", id.String(), map[string]any{"from": before, "to": status, "productId": row.ProductID})
		return nil
	})
	if err != nil {
		return err
	}
	s.rv.Revalidate("catalog", "reviews") // ratings show on product cards and pages
	return nil
}

// ---- newsletter ----

func (s *Service) Subscribers(ctx context.Context, q string, page int) ([]store.AdminListSubscribersRow, int, error) {
	if _, err := authorize(ctx); err != nil {
		return nil, 0, err
	}
	rows, err := s.q.AdminListSubscribers(ctx, store.AdminListSubscribersParams{Q: strings.TrimSpace(q), Lim: PerPage, Off: int32((page - 1) * PerPage)})
	if err != nil || len(rows) == 0 {
		return rows, 0, err
	}
	return rows, int(rows[0].TotalCount), nil
}

// ExportSubscribers writes every subscriber as CSV (email, subscribed_at in IST). Exports are audited
// because the file contains personal data.
func (s *Service) ExportSubscribers(ctx context.Context, w io.Writer) (int, error) {
	me, err := authorize(ctx)
	if err != nil {
		return 0, err
	}
	rows, err := s.q.AdminAllSubscribers(ctx)
	if err != nil {
		return 0, err
	}
	ist := time.FixedZone("IST", 5*3600+1800)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"email", "subscribed_at"})
	for _, r := range rows {
		_ = cw.Write([]string{csvSafe(r.Email), r.CreatedAt.Time.In(ist).Format("2006-01-02 15:04:05")})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return 0, err
	}
	s.audit.Audit(ctx, &me.ID, "newsletter.export", "newsletter", "", map[string]any{"rows": len(rows)})
	return len(rows), nil
}

// csvSafe stops spreadsheet apps from treating a cell as a formula (CSV injection).
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// RemoveSubscriber deletes an address (e.g. on the person's request).
func (s *Service) RemoveSubscriber(ctx context.Context, email string) error {
	me, err := authorize(ctx)
	if err != nil {
		return err
	}
	return database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		n, err := q.AdminDeleteSubscriber(ctx, email)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		// The audit keeps a masked address: enough to answer "was it removed?", not a copy of the data.
		s.audit.AuditWith(ctx, q, &me.ID, "newsletter.remove", "newsletter", mask(email), nil)
		return nil
	})
}

func mask(email string) string {
	local, domain, ok := strings.Cut(strings.ToLower(email), "@")
	if !ok || local == "" {
		return "***"
	}
	return fmt.Sprintf("%c***@%s", local[0], domain)
}

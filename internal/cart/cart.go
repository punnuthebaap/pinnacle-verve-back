// Package cart manages the session cart. Prices are always read live from the catalogue and totals are
// computed server-side by the pricing package.
package cart

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pinnacleverve/api/internal/catalog"
	"github.com/pinnacleverve/api/internal/platform/config"
	"github.com/pinnacleverve/api/internal/platform/database"
	"github.com/pinnacleverve/api/internal/platform/session"
	"github.com/pinnacleverve/api/internal/pricing"
	"github.com/pinnacleverve/api/internal/store"
)

// MaxLineQuantity mirrors the API contract limit per cart line.
const MaxLineQuantity = 10

var (
	ErrNotFound     = errors.New("cart: item or variant not found")
	ErrOutOfStock   = errors.New("cart: insufficient stock")
	ErrLineLimit    = errors.New("cart: per-line quantity limit exceeded")
	ErrUnknownAddon = errors.New("cart: unknown add-on")
)

type Line struct {
	store.ListCartLinesRow
	VariantLabel string
	LineTotal    int64
	MaxQuantity  int
}

type Addon = store.ListActiveAddonsByCodesRow

type Cart struct {
	ID                  uuid.UUID // zero when the session has no cart yet
	Lines               []Line
	Addons              []Addon
	Totals              pricing.Totals
	ItemCount           int
	WhiteGloveThreshold int64
	WhiteGloveRemaining int64
}

type Service struct {
	pool *pgxpool.Pool
	q    *store.Queries
	cfg  config.Config
}

func NewService(pool *pgxpool.Pool, q *store.Queries, cfg config.Config) *Service {
	return &Service{pool: pool, q: q, cfg: cfg}
}

// Persister adapts the store for session.Middleware.
func Persister(q *store.Queries) session.Persister {
	return func(ctx context.Context, id []byte, exp time.Time) error {
		return q.UpsertSession(ctx, store.UpsertSessionParams{ID: id, ExpiresAt: pgtype.Timestamptz{Time: exp, Valid: true}})
	}
}

func (s *Service) Get(ctx context.Context) (Cart, error) {
	sid := session.ID(ctx)
	if sid == nil {
		return s.empty(), nil
	}
	row, err := s.q.GetCartBySession(ctx, sid)
	if errors.Is(err, pgx.ErrNoRows) {
		return s.empty(), nil
	}
	if err != nil {
		return Cart{}, err
	}
	return Load(ctx, s.q, row.ID, row.AddonCodes, s.cfg)
}

func (s *Service) Add(ctx context.Context, variantID uuid.UUID, qty int) (Cart, error) {
	cartID, err := s.ensureCart(ctx)
	if err != nil {
		return Cart{}, err
	}
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		v, err := q.GetPurchasableVariant(ctx, variantID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		existing, err := q.GetCartItemQuantity(ctx, store.GetCartItemQuantityParams{CartID: cartID, VariantID: variantID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		next := int(existing) + qty
		if next > MaxLineQuantity {
			return ErrLineLimit
		}
		if next > int(v.Stock) {
			return ErrOutOfStock
		}
		return q.UpsertCartItem(ctx, store.UpsertCartItemParams{CartID: cartID, VariantID: variantID, Quantity: int32(next)})
	})
	if err != nil {
		return Cart{}, err
	}
	return s.Get(ctx)
}

func (s *Service) Update(ctx context.Context, itemID uuid.UUID, qty int) (Cart, error) {
	cartID, err := s.existingCart(ctx)
	if err != nil {
		return Cart{}, err
	}
	stock, err := s.q.GetCartItemStock(ctx, store.GetCartItemStockParams{ID: itemID, CartID: cartID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Cart{}, ErrNotFound
	}
	if err != nil {
		return Cart{}, err
	}
	if qty > int(stock) {
		return Cart{}, ErrOutOfStock
	}
	if _, err := s.q.UpdateCartItemQuantity(ctx, store.UpdateCartItemQuantityParams{ID: itemID, CartID: cartID, Quantity: int32(qty)}); err != nil {
		return Cart{}, err
	}
	return s.Get(ctx)
}

func (s *Service) Remove(ctx context.Context, itemID uuid.UUID) (Cart, error) {
	cartID, err := s.existingCart(ctx)
	if err != nil {
		return Cart{}, err
	}
	n, err := s.q.DeleteCartItem(ctx, store.DeleteCartItemParams{ID: itemID, CartID: cartID})
	if err != nil {
		return Cart{}, err
	}
	if n == 0 {
		return Cart{}, ErrNotFound
	}
	return s.Get(ctx)
}

func (s *Service) SetAddons(ctx context.Context, codes []string) (Cart, error) {
	found, err := s.q.ListActiveAddonsByCodes(ctx, codes)
	if err != nil {
		return Cart{}, err
	}
	if len(found) != len(codes) {
		return Cart{}, ErrUnknownAddon
	}
	cartID, err := s.ensureCart(ctx)
	if err != nil {
		return Cart{}, err
	}
	if err := s.q.SetCartAddons(ctx, store.SetCartAddonsParams{ID: cartID, AddonCodes: codes}); err != nil {
		return Cart{}, err
	}
	return s.Get(ctx)
}

func (s *Service) Addons(ctx context.Context) ([]store.ListActiveAddonsRow, error) {
	return s.q.ListActiveAddons(ctx)
}

func (s *Service) ensureCart(ctx context.Context) (uuid.UUID, error) {
	sid, err := session.Ensure(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	row, err := s.q.CreateCart(ctx, sid)
	return row.ID, err
}

func (s *Service) existingCart(ctx context.Context) (uuid.UUID, error) {
	sid := session.ID(ctx)
	if sid == nil {
		return uuid.Nil, ErrNotFound
	}
	row, err := s.q.GetCartBySession(ctx, sid)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return row.ID, err
}

func (s *Service) empty() Cart { return Build(uuid.Nil, nil, nil, s.cfg) }

// Load reads a cart's lines and add-ons with q (which may be transaction-bound) and prices them.
func Load(ctx context.Context, q *store.Queries, cartID uuid.UUID, addonCodes []string, cfg config.Config) (Cart, error) {
	rows, err := q.ListCartLines(ctx, cartID)
	if err != nil {
		return Cart{}, err
	}
	addons := []Addon{}
	if len(addonCodes) > 0 {
		if addons, err = q.ListActiveAddonsByCodes(ctx, addonCodes); err != nil {
			return Cart{}, err
		}
	}
	return Build(cartID, rows, addons, cfg), nil
}

// Build prices cart rows. It is pure so order creation prices exactly what the cart showed.
func Build(cartID uuid.UUID, rows []store.ListCartLinesRow, addons []Addon, cfg config.Config) Cart {
	c := Cart{ID: cartID, Lines: []Line{}, Addons: addons, WhiteGloveThreshold: cfg.WhiteGloveThresholdPaise}
	if c.Addons == nil {
		c.Addons = []Addon{}
	}
	in := pricing.Input{AddonGSTRateBps: cfg.AddonGSTRateBps, ShippingPaise: cfg.ShippingPaise}
	for _, r := range rows {
		l := pricing.Line{UnitPaise: r.PricePaise, Quantity: int64(r.Quantity), GSTRateBps: int64(r.GstRateBps)}
		in.Lines = append(in.Lines, l)
		c.Lines = append(c.Lines, Line{
			ListCartLinesRow: r,
			VariantLabel:     catalog.VariantLabel(r.ProductOptions, r.Options),
			LineTotal:        pricing.LineTotal(l),
			MaxQuantity:      min(MaxLineQuantity, int(r.Stock)),
		})
		c.ItemCount += int(r.Quantity)
	}
	for _, a := range addons {
		in.AddonPaise = append(in.AddonPaise, a.PricePaise)
	}
	c.Totals = pricing.Compute(in)
	c.WhiteGloveRemaining = pricing.Remaining(c.Totals.SubtotalPaise, cfg.WhiteGloveThresholdPaise)
	return c
}

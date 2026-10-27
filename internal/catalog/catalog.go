// Package catalog serves categories, facets and products (read-only storefront model).
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pinnacleverve/api/internal/store"
)

var ErrNotFound = errors.New("catalog: not found")

// LowStockThreshold is the stock level at or below which listings show "Only N left".
const LowStockThreshold = 3

type Service struct{ q *store.Queries }

func NewService(q *store.Queries) *Service { return &Service{q: q} }

type Query struct {
	Category *string
	Filters  []string // "key:value"
	MinPrice *int64
	MaxPrice *int64
	InStock  bool
	Sort     string
	Page     int
	PageSize int
}

type Product struct {
	store.GetProductBySlugRow
	Images   []store.ListProductImagesRow
	Variants []store.ListProductVariantsRow
}

type Facet struct {
	Key, Label, Kind string
	Values           []store.ListCategoryFacetValuesRow
}

func (s *Service) Categories(ctx context.Context) ([]store.ListCategoriesRow, error) {
	return s.q.ListCategories(ctx)
}

func (s *Service) Category(ctx context.Context, slug string) (store.GetCategoryBySlugRow, []Facet, error) {
	c, err := s.q.GetCategoryBySlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, nil, ErrNotFound
	}
	if err != nil {
		return c, nil, err
	}
	rows, err := s.q.ListCategoryFacetValues(ctx, c.ID)
	if err != nil {
		return c, nil, err
	}
	var facets []Facet
	for _, r := range rows {
		if len(facets) == 0 || facets[len(facets)-1].Key != r.Key {
			facets = append(facets, Facet{Key: r.Key, Label: r.Label, Kind: r.Kind})
		}
		f := &facets[len(facets)-1]
		f.Values = append(f.Values, r)
	}
	return c, facets, nil
}

func (s *Service) Products(ctx context.Context, q Query) ([]store.ListProductsRow, int, error) {
	var keys []string
	for _, f := range q.Filters {
		if k, _, ok := strings.Cut(f, ":"); ok && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	p := store.ListProductsParams{
		InStock:    q.InStock,
		FilterKeys: keys,
		Filters:    q.Filters,
		Sort:       q.Sort,
		Lim:        int32(q.PageSize),
		Off:        int32((q.Page - 1) * q.PageSize),
	}
	if q.Category != nil {
		p.Category = pgtype.Text{String: *q.Category, Valid: true}
	}
	if q.MinPrice != nil {
		p.MinPrice = pgtype.Int8{Int64: *q.MinPrice, Valid: true}
	}
	if q.MaxPrice != nil {
		p.MaxPrice = pgtype.Int8{Int64: *q.MaxPrice, Valid: true}
	}
	rows, err := s.q.ListProducts(ctx, p)
	if err != nil || len(rows) == 0 {
		return rows, 0, err
	}
	return rows, int(rows[0].TotalCount), nil
}

func (s *Service) Product(ctx context.Context, slug string) (Product, error) {
	row, err := s.q.GetProductBySlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Product{}, ErrNotFound
	}
	if err != nil {
		return Product{}, err
	}
	p := Product{GetProductBySlugRow: row}
	if p.Images, err = s.q.ListProductImages(ctx, row.ID); err != nil {
		return p, err
	}
	if p.Variants, err = s.q.ListProductVariants(ctx, row.ID); err != nil {
		return p, err
	}
	if len(p.Variants) == 0 {
		return p, ErrNotFound
	}
	return p, nil
}

// Option mirrors products.options JSON.
type Option struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Values []struct {
		Code      string `json:"code"`
		Label     string `json:"label"`
		Detail    string `json:"detail,omitempty"`
		SwatchHex string `json:"swatchHex,omitempty"`
	} `json:"values"`
}

// VariantLabel renders a variant's option values in product option order, e.g.
// `Large 36" × 24" • Brushed Champagne Brass`. Unknown codes are skipped.
func VariantLabel(productOptions, variantOptions json.RawMessage) string {
	var opts []Option
	var chosen map[string]string
	if json.Unmarshal(productOptions, &opts) != nil || json.Unmarshal(variantOptions, &chosen) != nil {
		return ""
	}
	var parts []string
	for _, o := range opts {
		for _, v := range o.Values {
			if v.Code == chosen[o.Code] {
				label := v.Label
				if v.Detail != "" {
					label += " " + v.Detail
				}
				parts = append(parts, label)
			}
		}
	}
	return strings.Join(parts, " • ")
}

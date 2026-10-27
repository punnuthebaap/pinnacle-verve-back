// Package catalogadmin implements catalogue management for the admin panel (phase A2): categories and
// their filters, products, variants, filter values and pairings. Every write is validated, audited in the
// same transaction, and followed by a storefront cache revalidation (decision D36).
package catalogadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/catalog"
	"github.com/pinnacleverve/api/internal/platform/database"
	"github.com/pinnacleverve/api/internal/store"
)

const ProductsPerPage = 25

var (
	ErrNotFound  = errors.New("catalogadmin: not found")
	ErrSlugTaken = errors.New("catalogadmin: slug already in use")
	ErrSKUTaken  = errors.New("catalogadmin: SKU already in use")
)

// InvalidError is a business-rule violation with a message an admin can act on.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &InvalidError{Msg: fmt.Sprintf(format, a...)} }

// Revalidator tells the storefront that cached catalogue pages are stale.
type Revalidator interface{ CatalogChanged() }

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
	if !admin.Can(me.Role, admin.AreaCatalog) {
		return me, admin.ErrForbidden
	}
	return me, nil
}

// write runs fn in a transaction, records the audit entry in the same transaction, then revalidates.
func (s *Service) write(ctx context.Context, action, entity string, fn func(q *store.Queries) (entityID string, data any, err error)) error {
	me, err := authorize(ctx)
	if err != nil {
		return err
	}
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		id, data, err := fn(q)
		if err != nil {
			return err
		}
		s.audit.AuditWith(ctx, q, &me.ID, action, entity, id, data)
		return nil
	})
	if err = mapDBError(err); err != nil {
		return err
	}
	s.rv.CatalogChanged()
	return nil
}

func mapDBError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch {
	case pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "sku"):
		return ErrSKUTaken
	case pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "slug"):
		return ErrSlugTaken
	case pgErr.Code == "23503":
		return invalid("A referenced record does not exist (%s).", pgErr.ConstraintName)
	case pgErr.Code == "23514":
		return invalid("A value is out of range (%s).", pgErr.ConstraintName)
	}
	return err
}

// ---- categories ----

type Category struct {
	store.AdminListCategoriesRow
	Facets []store.CategoryFacet
}

type CategoryInput struct {
	Slug, Name, Eyebrow, Description string
	Position                         int32
	IsActive                         bool
}

type FacetInput struct{ Key, Label, Kind string }

func (s *Service) Categories(ctx context.Context) ([]Category, error) {
	if _, err := authorize(ctx); err != nil {
		return nil, err
	}
	rows, err := s.q.AdminListCategories(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Category, 0, len(rows))
	for _, r := range rows {
		facets, err := s.q.AdminListCategoryFacets(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, Category{AdminListCategoriesRow: r, Facets: facets})
	}
	return out, nil
}

func (s *Service) Category(ctx context.Context, id uuid.UUID) (Category, error) {
	cats, err := s.Categories(ctx)
	if err != nil {
		return Category{}, err
	}
	for _, c := range cats {
		if c.ID == id {
			return c, nil
		}
	}
	return Category{}, ErrNotFound
}

func (s *Service) CreateCategory(ctx context.Context, in CategoryInput) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.write(ctx, "category.create", "category", func(q *store.Queries) (string, any, error) {
		c, err := q.AdminInsertCategory(ctx, store.AdminInsertCategoryParams{
			Slug: in.Slug, Name: strings.TrimSpace(in.Name), Eyebrow: strings.TrimSpace(in.Eyebrow),
			Description: strings.TrimSpace(in.Description), Position: in.Position, IsActive: in.IsActive,
		})
		id = c.ID
		return c.ID.String(), in, err
	})
	return id, err
}

func (s *Service) UpdateCategory(ctx context.Context, id uuid.UUID, in CategoryInput) error {
	return s.write(ctx, "category.update", "category", func(q *store.Queries) (string, any, error) {
		_, err := q.AdminUpdateCategory(ctx, store.AdminUpdateCategoryParams{
			ID: id, Slug: in.Slug, Name: strings.TrimSpace(in.Name), Eyebrow: strings.TrimSpace(in.Eyebrow),
			Description: strings.TrimSpace(in.Description), Position: in.Position, IsActive: in.IsActive,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, ErrNotFound
		}
		return id.String(), in, err
	})
}

func (s *Service) SetFacets(ctx context.Context, id uuid.UUID, facets []FacetInput) error {
	seen := map[string]bool{}
	for _, f := range facets {
		if seen[f.Key] {
			return invalid("Filter key %q is used twice.", f.Key)
		}
		seen[f.Key] = true
	}
	return s.write(ctx, "category.facets", "category", func(q *store.Queries) (string, any, error) {
		if _, err := q.AdminGetCategory(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return "", nil, ErrNotFound
		} else if err != nil {
			return "", nil, err
		}
		if err := q.AdminDeleteCategoryFacets(ctx, id); err != nil {
			return "", nil, err
		}
		for i, f := range facets {
			if err := q.AdminInsertCategoryFacet(ctx, store.AdminInsertCategoryFacetParams{
				CategoryID: id, Key: f.Key, Label: strings.TrimSpace(f.Label), Kind: f.Kind, Position: int32(i),
			}); err != nil {
				return "", nil, err
			}
		}
		return id.String(), facets, nil
	})
}

// ---- products ----

type ListQuery struct {
	Q          string
	CategoryID *uuid.UUID
	Active     *bool
	Page       int
}

func (s *Service) Products(ctx context.Context, lq ListQuery) ([]store.AdminListProductsRow, int, error) {
	if _, err := authorize(ctx); err != nil {
		return nil, 0, err
	}
	p := store.AdminListProductsParams{Q: strings.TrimSpace(lq.Q), Lim: ProductsPerPage, Off: int32((lq.Page - 1) * ProductsPerPage)}
	if lq.CategoryID != nil {
		p.CategoryID = pgtype.UUID{Bytes: *lq.CategoryID, Valid: true}
	}
	if lq.Active != nil {
		p.Active = pgtype.Bool{Bool: *lq.Active, Valid: true}
	}
	rows, err := s.q.AdminListProducts(ctx, p)
	if err != nil || len(rows) == 0 {
		return rows, 0, err
	}
	return rows, int(rows[0].TotalCount), nil
}

type Product struct {
	store.Product
	Variants   []store.ProductVariant
	Attributes []store.AdminListAttributesRow
	Related    []store.AdminListRelationsRow
	Images     []store.AdminListProductImagesRow
}

func (s *Service) Product(ctx context.Context, id uuid.UUID) (Product, error) {
	if _, err := authorize(ctx); err != nil {
		return Product{}, err
	}
	row, err := s.q.AdminGetProduct(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Product{}, ErrNotFound
	}
	if err != nil {
		return Product{}, err
	}
	p := Product{Product: row}
	if p.Variants, err = s.q.AdminListVariants(ctx, id); err != nil {
		return p, err
	}
	if p.Attributes, err = s.q.AdminListAttributes(ctx, id); err != nil {
		return p, err
	}
	if p.Related, err = s.q.AdminListRelations(ctx, id); err != nil {
		return p, err
	}
	p.Images, err = s.q.AdminListProductImages(ctx, id)
	return p, err
}

// ProductInput carries content fields as JSON already shaped like the storefront contract.
type ProductInput struct {
	Slug, Name, Subtitle, Series, MaterialLabel, SizeLabel, Description, ShippingNote string
	CategoryID                                                                        uuid.UUID
	Badges, Features, Options, Specs, Care, Facts                                     json.RawMessage
	GSTRateBps, Rank                                                                  int32
	IsActive                                                                          bool
}

func validateOptions(raw json.RawMessage) ([]catalog.Option, error) {
	var opts []catalog.Option
	if err := json.Unmarshal(raw, &opts); err != nil {
		return nil, invalid("Options are malformed.")
	}
	codes := map[string]bool{}
	for _, o := range opts {
		if o.Code == "" || codes[o.Code] {
			return nil, invalid("Option codes must be present and unique (%q).", o.Code)
		}
		codes[o.Code] = true
		if len(o.Values) == 0 {
			return nil, invalid("Option %q needs at least one value.", o.Name)
		}
		values := map[string]bool{}
		for _, v := range o.Values {
			if v.Code == "" || values[v.Code] {
				return nil, invalid("Values of option %q must have unique codes.", o.Name)
			}
			values[v.Code] = true
		}
	}
	return opts, nil
}

func (s *Service) CreateProduct(ctx context.Context, in ProductInput) (uuid.UUID, error) {
	if _, err := validateOptions(in.Options); err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err := s.write(ctx, "product.create", "product", func(q *store.Queries) (string, any, error) {
		var err error
		id, err = q.AdminInsertProduct(ctx, store.AdminInsertProductParams{
			Slug: in.Slug, CategoryID: in.CategoryID, Name: strings.TrimSpace(in.Name), Subtitle: in.Subtitle, Series: in.Series,
			MaterialLabel: in.MaterialLabel, SizeLabel: in.SizeLabel, Description: in.Description, ShippingNote: in.ShippingNote,
			Badges: in.Badges, Features: in.Features, Options: in.Options, Specs: in.Specs, Care: in.Care, Facts: in.Facts,
			GstRateBps: in.GSTRateBps, Rank: in.Rank, IsActive: in.IsActive,
		})
		return id.String(), map[string]any{"slug": in.Slug, "name": in.Name}, err
	})
	return id, err
}

func (s *Service) UpdateProduct(ctx context.Context, id uuid.UUID, in ProductInput) error {
	opts, err := validateOptions(in.Options)
	if err != nil {
		return err
	}
	return s.write(ctx, "product.update", "product", func(q *store.Queries) (string, any, error) {
		if _, err := q.AdminGetProduct(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return "", nil, ErrNotFound
		} else if err != nil {
			return "", nil, err
		}
		// Changing options must not orphan existing active variants.
		variants, err := q.AdminListVariants(ctx, id)
		if err != nil {
			return "", nil, err
		}
		for _, v := range variants {
			if v.IsActive {
				if err := checkVariantOptions(opts, v.Sku, v.Options); err != nil {
					return "", nil, invalid("%s Update or deactivate that variant first.", err.Error())
				}
			}
		}
		err = q.AdminUpdateProduct(ctx, store.AdminUpdateProductParams{
			ID: id, Slug: in.Slug, CategoryID: in.CategoryID, Name: strings.TrimSpace(in.Name), Subtitle: in.Subtitle, Series: in.Series,
			MaterialLabel: in.MaterialLabel, SizeLabel: in.SizeLabel, Description: in.Description, ShippingNote: in.ShippingNote,
			Badges: in.Badges, Features: in.Features, Options: in.Options, Specs: in.Specs, Care: in.Care, Facts: in.Facts,
			GstRateBps: in.GSTRateBps, Rank: in.Rank, IsActive: in.IsActive,
		})
		return id.String(), map[string]any{"slug": in.Slug, "name": in.Name, "isActive": in.IsActive}, err
	})
}

type VariantInput struct {
	ID         *uuid.UUID
	SKU        string
	Options    map[string]string
	PricePaise int64
	MRPPaise   int64
	Stock      int32
	IsActive   bool
}

// checkVariantOptions requires exactly one valid value for every product option.
func checkVariantOptions(opts []catalog.Option, sku string, raw json.RawMessage) error {
	var chosen map[string]string
	if err := json.Unmarshal(raw, &chosen); err != nil {
		return invalid("Variant %s has malformed options.", sku)
	}
	if len(chosen) != len(opts) {
		return invalid("Variant %s must choose exactly one value for each option.", sku)
	}
	for _, o := range opts {
		v, ok := chosen[o.Code]
		if !ok || !slices.ContainsFunc(o.Values, func(x catalog.OptionValue) bool { return x.Code == v }) {
			return invalid("Variant %s has no valid value for option %q.", sku, o.Name)
		}
	}
	return nil
}

func comboKey(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + m[k] + ";")
	}
	return b.String()
}

func (s *Service) SetVariants(ctx context.Context, productID uuid.UUID, in []VariantInput) error {
	return s.write(ctx, "product.variants", "product", func(q *store.Queries) (string, any, error) {
		p, err := q.AdminGetProduct(ctx, productID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, ErrNotFound
		}
		if err != nil {
			return "", nil, err
		}
		var opts []catalog.Option
		_ = json.Unmarshal(p.Options, &opts)
		existing, err := q.AdminListVariants(ctx, productID)
		if err != nil {
			return "", nil, err
		}
		owned := map[uuid.UUID]bool{}
		for _, v := range existing {
			owned[v.ID] = true
		}
		skus, combos := map[string]bool{}, map[string]string{}
		keep := make([]uuid.UUID, 0, len(in))
		for i, v := range in {
			if v.MRPPaise < v.PricePaise {
				return "", nil, invalid("Variant %s: MRP must be at least the selling price.", v.SKU)
			}
			if skus[v.SKU] {
				return "", nil, invalid("SKU %s appears twice.", v.SKU)
			}
			skus[v.SKU] = true
			optJSON, _ := json.Marshal(v.Options)
			if err := checkVariantOptions(opts, v.SKU, optJSON); err != nil {
				return "", nil, err
			}
			if v.IsActive {
				if other, dup := combos[comboKey(v.Options)]; dup {
					return "", nil, invalid("Variants %s and %s have the same options.", other, v.SKU)
				}
				combos[comboKey(v.Options)] = v.SKU
			}
			id := uuid.New()
			if v.ID != nil {
				if !owned[*v.ID] {
					return "", nil, invalid("Variant %s does not belong to this product.", v.SKU)
				}
				id = *v.ID
			}
			keep = append(keep, id)
			if err := q.AdminUpsertVariant(ctx, store.AdminUpsertVariantParams{
				ID: id, ProductID: productID, Sku: v.SKU, Options: optJSON, PricePaise: v.PricePaise,
				MrpPaise: v.MRPPaise, Stock: v.Stock, Position: int32(i), IsActive: v.IsActive,
			}); err != nil {
				return "", nil, err
			}
		}
		if err := q.AdminDeactivateVariantsNotIn(ctx, store.AdminDeactivateVariantsNotInParams{ProductID: productID, Keep: keep}); err != nil {
			return "", nil, err
		}
		return productID.String(), map[string]any{"variants": len(in)}, nil
	})
}

type AttributeInput struct{ Key, Value, Label, SwatchHex string }

func (s *Service) SetAttributes(ctx context.Context, productID uuid.UUID, in []AttributeInput) error {
	return s.write(ctx, "product.attributes", "product", func(q *store.Queries) (string, any, error) {
		p, err := q.AdminGetProduct(ctx, productID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, ErrNotFound
		}
		if err != nil {
			return "", nil, err
		}
		facets, err := q.AdminListCategoryFacets(ctx, p.CategoryID)
		if err != nil {
			return "", nil, err
		}
		allowed := map[string]bool{}
		for _, f := range facets {
			allowed[f.Key] = true
		}
		seen := map[string]bool{}
		if err := q.AdminDeleteAttributes(ctx, productID); err != nil {
			return "", nil, err
		}
		for _, a := range in {
			if !allowed[a.Key] {
				return "", nil, invalid("Filter %q is not defined for this product's category.", a.Key)
			}
			if seen[a.Key+":"+a.Value] {
				return "", nil, invalid("Filter value %s:%s appears twice.", a.Key, a.Value)
			}
			seen[a.Key+":"+a.Value] = true
			if err := q.AdminInsertAttribute(ctx, store.AdminInsertAttributeParams{
				ProductID: productID, Key: a.Key, Value: a.Value, Label: strings.TrimSpace(a.Label), SwatchHex: a.SwatchHex,
			}); err != nil {
				return "", nil, err
			}
		}
		return productID.String(), map[string]any{"attributes": len(in)}, nil
	})
}

func (s *Service) SetRelated(ctx context.Context, productID uuid.UUID, ids []uuid.UUID) error {
	if slices.Contains(ids, productID) {
		return invalid("A product can't be paired with itself.")
	}
	return s.write(ctx, "product.related", "product", func(q *store.Queries) (string, any, error) {
		if _, err := q.AdminGetProduct(ctx, productID); errors.Is(err, pgx.ErrNoRows) {
			return "", nil, ErrNotFound
		} else if err != nil {
			return "", nil, err
		}
		if err := q.AdminDeleteRelations(ctx, productID); err != nil {
			return "", nil, err
		}
		for i, rid := range ids {
			if err := q.AdminInsertRelation(ctx, store.AdminInsertRelationParams{ProductID: productID, RelatedID: rid, Position: int32(i)}); err != nil {
				return "", nil, err
			}
		}
		return productID.String(), map[string]any{"related": len(ids)}, nil
	})
}

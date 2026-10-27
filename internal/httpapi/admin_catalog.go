package httpapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/api"
	"github.com/pinnacleverve/api/internal/catalogadmin"
)

var (
	pSlugTaken = problem(409, "slug_taken", "That URL slug is already used by another record")
	pSKUTaken  = problem(409, "sku_taken", "That SKU is already used by another variant")
)

// catalogProblem maps catalogue-admin errors; ok=false means the error is unexpected (→ 500).
func catalogProblem(err error) (api.Problem, bool) {
	var inv *catalogadmin.InvalidError
	switch {
	case errors.Is(err, admin.ErrForbidden):
		return pForbidden, true
	case errors.Is(err, catalogadmin.ErrNotFound):
		return pNotFound, true
	case errors.Is(err, catalogadmin.ErrSlugTaken):
		return pSlugTaken, true
	case errors.Is(err, catalogadmin.ErrSKUTaken):
		return pSKUTaken, true
	case errors.As(err, &inv):
		return problem(422, "invalid", inv.Msg), true
	}
	return api.Problem{}, false
}

// productResult loads the product after a write and returns it, or maps the write's error.
func (s *Server) productResult(ctx context.Context, id uuid.UUID, err error, pick func(api.Problem) any) (any, error) {
	if err != nil {
		if p, ok := catalogProblem(err); ok {
			return pick(p), nil
		}
		return nil, err
	}
	p, err := s.CatalogAdmin.Product(ctx, id)
	if err != nil {
		return nil, err
	}
	return adminProductJSON(p), nil
}

func (s *Server) AdminListCategories(ctx context.Context, _ api.AdminListCategoriesRequestObject) (api.AdminListCategoriesResponseObject, error) {
	cats, err := s.CatalogAdmin.Categories(ctx)
	if errors.Is(err, admin.ErrForbidden) {
		return api.AdminListCategories403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pForbidden)}, nil
	}
	if err != nil {
		return nil, err
	}
	out := api.AdminListCategories200JSONResponse{Items: make([]api.AdminCategory, 0, len(cats))}
	for _, c := range cats {
		out.Items = append(out.Items, adminCategoryJSON(c))
	}
	return out, nil
}

func categoryInput(b api.AdminCategoryInput) catalogadmin.CategoryInput {
	return catalogadmin.CategoryInput{Slug: b.Slug, Name: b.Name, Eyebrow: b.Eyebrow, Description: b.Description, Position: int32(b.Position), IsActive: b.IsActive}
}

func (s *Server) AdminCreateCategory(ctx context.Context, r api.AdminCreateCategoryRequestObject) (api.AdminCreateCategoryResponseObject, error) {
	id, err := s.CatalogAdmin.CreateCategory(ctx, categoryInput(*r.Body))
	if err != nil {
		p, ok := catalogProblem(err)
		switch {
		case !ok:
			return nil, err
		case p.Status == 403:
			return api.AdminCreateCategory403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}, nil
		default:
			return api.AdminCreateCategory409ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	c, err := s.CatalogAdmin.Category(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.AdminCreateCategory201JSONResponse(adminCategoryJSON(c)), nil
}

func (s *Server) AdminUpdateCategory(ctx context.Context, r api.AdminUpdateCategoryRequestObject) (api.AdminUpdateCategoryResponseObject, error) {
	if err := s.CatalogAdmin.UpdateCategory(ctx, r.Id, categoryInput(*r.Body)); err != nil {
		p, ok := catalogProblem(err)
		switch {
		case !ok:
			return nil, err
		case p.Status == 403:
			return api.AdminUpdateCategory403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}, nil
		case p.Status == 404:
			return api.AdminUpdateCategory404ApplicationProblemPlusJSONResponse(p), nil
		default:
			return api.AdminUpdateCategory409ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	c, err := s.CatalogAdmin.Category(ctx, r.Id)
	if err != nil {
		return nil, err
	}
	return api.AdminUpdateCategory200JSONResponse(adminCategoryJSON(c)), nil
}

func (s *Server) AdminSetCategoryFacets(ctx context.Context, r api.AdminSetCategoryFacetsRequestObject) (api.AdminSetCategoryFacetsResponseObject, error) {
	in := make([]catalogadmin.FacetInput, 0, len(r.Body.Facets))
	for _, f := range r.Body.Facets {
		in = append(in, catalogadmin.FacetInput{Key: f.Key, Label: f.Label, Kind: string(f.Kind)})
	}
	if err := s.CatalogAdmin.SetFacets(ctx, r.Id, in); err != nil {
		p, ok := catalogProblem(err)
		switch {
		case !ok:
			return nil, err
		case p.Status == 403:
			return api.AdminSetCategoryFacets403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}, nil
		case p.Status == 404:
			return api.AdminSetCategoryFacets404ApplicationProblemPlusJSONResponse(p), nil
		default:
			return api.AdminSetCategoryFacets422ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	c, err := s.CatalogAdmin.Category(ctx, r.Id)
	if err != nil {
		return nil, err
	}
	return api.AdminSetCategoryFacets200JSONResponse(adminCategoryJSON(c)), nil
}

func (s *Server) AdminListProducts(ctx context.Context, r api.AdminListProductsRequestObject) (api.AdminListProductsResponseObject, error) {
	lq := catalogadmin.ListQuery{CategoryID: r.Params.CategoryId, Active: r.Params.Active, Page: 1}
	if r.Params.Q != nil {
		lq.Q = *r.Params.Q
	}
	if r.Params.Page != nil {
		lq.Page = *r.Params.Page
	}
	rows, total, err := s.CatalogAdmin.Products(ctx, lq)
	if errors.Is(err, admin.ErrForbidden) {
		return api.AdminListProducts403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pForbidden)}, nil
	}
	if err != nil {
		return nil, err
	}
	out := api.AdminListProducts200JSONResponse{Page: lq.Page, Total: total, Items: make([]api.AdminProductSummary, 0, len(rows))}
	for _, p := range rows {
		out.Items = append(out.Items, api.AdminProductSummary{
			Id: p.ID, Slug: p.Slug, Name: p.Name, IsActive: p.IsActive, CategoryId: p.CategoryID, CategoryName: p.CategoryName,
			VariantCount: int(p.VariantCount), TotalStock: int(p.TotalStock), MinPricePaise: p.MinPricePaise, ImageUrl: p.ImageUrl,
		})
	}
	return out, nil
}

func productInput(b api.AdminProductInput) catalogadmin.ProductInput {
	j := func(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
	return catalogadmin.ProductInput{
		Slug: b.Slug, CategoryID: b.CategoryId, Name: b.Name, Subtitle: b.Subtitle, Series: b.Series,
		MaterialLabel: b.MaterialLabel, SizeLabel: b.SizeLabel, Description: b.Description, ShippingNote: b.ShippingNote,
		Badges: j(b.Badges), Features: j(b.Features), Options: j(b.Options), Specs: j(b.Specs), Care: j(b.Care), Facts: j(b.Facts),
		GSTRateBps: int32(b.GstRateBps), Rank: int32(b.Rank), IsActive: b.IsActive,
	}
}

func (s *Server) AdminCreateProduct(ctx context.Context, r api.AdminCreateProductRequestObject) (api.AdminCreateProductResponseObject, error) {
	id, err := s.CatalogAdmin.CreateProduct(ctx, productInput(*r.Body))
	return writeProduct(s, ctx, id, err,
		func(p api.AdminProduct) api.AdminCreateProductResponseObject {
			return api.AdminCreateProduct201JSONResponse{AdminProductJSONResponse: api.AdminProductJSONResponse(p)}
		},
		func(p api.Problem) api.AdminCreateProductResponseObject {
			switch p.Status {
			case 403:
				return api.AdminCreateProduct403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}
			case 409:
				return api.AdminCreateProduct409ApplicationProblemPlusJSONResponse(p)
			}
			return api.AdminCreateProduct422ApplicationProblemPlusJSONResponse(p)
		})
}

func (s *Server) AdminGetProduct(ctx context.Context, r api.AdminGetProductRequestObject) (api.AdminGetProductResponseObject, error) {
	p, err := s.CatalogAdmin.Product(ctx, r.Id)
	if prob, ok := catalogProblem(err); ok {
		if prob.Status == 403 {
			return api.AdminGetProduct403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(prob)}, nil
		}
		return api.AdminGetProduct404ApplicationProblemPlusJSONResponse(prob), nil
	}
	if err != nil {
		return nil, err
	}
	return api.AdminGetProduct200JSONResponse{AdminProductJSONResponse: api.AdminProductJSONResponse(adminProductJSON(p))}, nil
}

// writeProduct runs a product write and returns the fresh product, or a problem built by pick.
func writeProduct[R any](s *Server, ctx context.Context, id uuid.UUID, err error, ok200 func(api.AdminProduct) R, pick func(api.Problem) R) (R, error) {
	var zero R
	res, err := s.productResult(ctx, id, err, func(p api.Problem) any { return pick(p) })
	if err != nil {
		return zero, err
	}
	if p, ok := res.(api.AdminProduct); ok {
		return ok200(p), nil
	}
	return res.(R), nil
}

func (s *Server) AdminUpdateProduct(ctx context.Context, r api.AdminUpdateProductRequestObject) (api.AdminUpdateProductResponseObject, error) {
	err := s.CatalogAdmin.UpdateProduct(ctx, r.Id, productInput(*r.Body))
	return writeProduct(s, ctx, r.Id, err,
		func(p api.AdminProduct) api.AdminUpdateProductResponseObject {
			return api.AdminUpdateProduct200JSONResponse{AdminProductJSONResponse: api.AdminProductJSONResponse(p)}
		},
		func(p api.Problem) api.AdminUpdateProductResponseObject {
			switch p.Status {
			case 403:
				return api.AdminUpdateProduct403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}
			case 404:
				return api.AdminUpdateProduct404ApplicationProblemPlusJSONResponse(p)
			case 409:
				return api.AdminUpdateProduct409ApplicationProblemPlusJSONResponse(p)
			}
			return api.AdminUpdateProduct422ApplicationProblemPlusJSONResponse(p)
		})
}

func (s *Server) AdminSetVariants(ctx context.Context, r api.AdminSetVariantsRequestObject) (api.AdminSetVariantsResponseObject, error) {
	in := make([]catalogadmin.VariantInput, 0, len(r.Body.Variants))
	for _, v := range r.Body.Variants {
		in = append(in, catalogadmin.VariantInput{ID: v.Id, SKU: v.Sku, Options: v.Options, PricePaise: v.PricePaise, MRPPaise: v.MrpPaise, Stock: int32(v.Stock), IsActive: v.IsActive})
	}
	err := s.CatalogAdmin.SetVariants(ctx, r.Id, in)
	return writeProduct(s, ctx, r.Id, err,
		func(p api.AdminProduct) api.AdminSetVariantsResponseObject {
			return api.AdminSetVariants200JSONResponse{AdminProductJSONResponse: api.AdminProductJSONResponse(p)}
		},
		func(p api.Problem) api.AdminSetVariantsResponseObject {
			switch p.Status {
			case 403:
				return api.AdminSetVariants403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}
			case 404:
				return api.AdminSetVariants404ApplicationProblemPlusJSONResponse(p)
			case 409:
				return api.AdminSetVariants409ApplicationProblemPlusJSONResponse(p)
			}
			return api.AdminSetVariants422ApplicationProblemPlusJSONResponse(p)
		})
}

func (s *Server) AdminSetAttributes(ctx context.Context, r api.AdminSetAttributesRequestObject) (api.AdminSetAttributesResponseObject, error) {
	in := make([]catalogadmin.AttributeInput, 0, len(r.Body.Attributes))
	for _, a := range r.Body.Attributes {
		hex := ""
		if a.SwatchHex != nil {
			hex = *a.SwatchHex
		}
		in = append(in, catalogadmin.AttributeInput{Key: a.Key, Value: a.Value, Label: a.Label, SwatchHex: hex})
	}
	err := s.CatalogAdmin.SetAttributes(ctx, r.Id, in)
	return writeProduct(s, ctx, r.Id, err,
		func(p api.AdminProduct) api.AdminSetAttributesResponseObject {
			return api.AdminSetAttributes200JSONResponse{AdminProductJSONResponse: api.AdminProductJSONResponse(p)}
		},
		func(p api.Problem) api.AdminSetAttributesResponseObject {
			switch p.Status {
			case 403:
				return api.AdminSetAttributes403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}
			case 404:
				return api.AdminSetAttributes404ApplicationProblemPlusJSONResponse(p)
			}
			return api.AdminSetAttributes422ApplicationProblemPlusJSONResponse(p)
		})
}

func (s *Server) AdminSetRelated(ctx context.Context, r api.AdminSetRelatedRequestObject) (api.AdminSetRelatedResponseObject, error) {
	err := s.CatalogAdmin.SetRelated(ctx, r.Id, r.Body.ProductIds)
	return writeProduct(s, ctx, r.Id, err,
		func(p api.AdminProduct) api.AdminSetRelatedResponseObject {
			return api.AdminSetRelated200JSONResponse{AdminProductJSONResponse: api.AdminProductJSONResponse(p)}
		},
		func(p api.Problem) api.AdminSetRelatedResponseObject {
			switch p.Status {
			case 403:
				return api.AdminSetRelated403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}
			case 404:
				return api.AdminSetRelated404ApplicationProblemPlusJSONResponse(p)
			}
			return api.AdminSetRelated422ApplicationProblemPlusJSONResponse(p)
		})
}

func adminCategoryJSON(c catalogadmin.Category) api.AdminCategory {
	out := api.AdminCategory{
		Id: c.ID, Slug: c.Slug, Name: c.Name, Eyebrow: c.Eyebrow, Description: c.Description,
		Position: int(c.Position), IsActive: c.IsActive, ProductCount: int(c.ProductCount),
		Facets: make([]api.FacetDefinition, 0, len(c.Facets)),
	}
	for _, f := range c.Facets {
		out.Facets = append(out.Facets, api.FacetDefinition{Key: f.Key, Label: f.Label, Kind: api.FacetDefinitionKind(f.Kind)})
	}
	return out
}

func adminProductJSON(p catalogadmin.Product) api.AdminProduct {
	out := api.AdminProduct{
		Id: p.ID, Slug: p.Slug, CategoryId: p.CategoryID, Name: p.Name, Subtitle: p.Subtitle, Series: p.Series,
		MaterialLabel: p.MaterialLabel, SizeLabel: p.SizeLabel, Description: p.Description, ShippingNote: p.ShippingNote,
		GstRateBps: int(p.GstRateBps), Rank: int(p.Rank), IsActive: p.IsActive, RatingAvg: p.RatingAvg, RatingCount: int(p.RatingCount),
		Badges: []api.Badge{}, Features: []api.ProductFeature{}, Options: []api.ProductOption{}, Specs: []api.SpecSection{},
		Care: []api.CareNote{}, Facts: []api.ProductFact{},
		Variants: make([]api.AdminVariant, 0, len(p.Variants)), Attributes: make([]api.AdminAttribute, 0, len(p.Attributes)),
		Images: make([]api.AdminImage, 0, len(p.Images)),
	}
	_ = json.Unmarshal(p.Badges, &out.Badges)
	_ = json.Unmarshal(p.Features, &out.Features)
	_ = json.Unmarshal(p.Options, &out.Options)
	_ = json.Unmarshal(p.Specs, &out.Specs)
	_ = json.Unmarshal(p.Care, &out.Care)
	_ = json.Unmarshal(p.Facts, &out.Facts)
	for _, v := range p.Variants {
		opts := map[string]string{}
		_ = json.Unmarshal(v.Options, &opts)
		out.Variants = append(out.Variants, api.AdminVariant{
			Id: v.ID, Sku: v.Sku, Options: opts, PricePaise: v.PricePaise, MrpPaise: v.MrpPaise,
			Stock: int(v.Stock), Position: int(v.Position), IsActive: v.IsActive,
		})
	}
	for _, a := range p.Attributes {
		out.Attributes = append(out.Attributes, api.AdminAttribute{Key: a.Key, Value: a.Value, Label: a.Label, SwatchHex: nonEmpty(a.SwatchHex)})
	}
	for _, r := range p.Related {
		out.Related = append(out.Related, struct {
			Id   uuid.UUID `json:"id"`
			Name string    `json:"name"`
			Slug string    `json:"slug"`
		}{Id: r.RelatedID, Name: r.Name, Slug: r.Slug})
	}
	if out.Related == nil {
		out.Related = []struct {
			Id   uuid.UUID `json:"id"`
			Name string    `json:"name"`
			Slug string    `json:"slug"`
		}{}
	}
	for _, i := range p.Images {
		out.Images = append(out.Images, api.AdminImage{Id: i.ID, Url: i.Url, Alt: i.Alt, Position: int(i.Position)})
	}
	return out
}

package httpapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/pinnacleverve/api/internal/api"
	"github.com/pinnacleverve/api/internal/catalog"
	"github.com/pinnacleverve/api/internal/store"
)

func (s *Server) ListCategories(ctx context.Context, _ api.ListCategoriesRequestObject) (api.ListCategoriesResponseObject, error) {
	rows, err := s.Catalog.Categories(ctx)
	if err != nil {
		return nil, err
	}
	out := api.ListCategories200JSONResponse{Items: make([]api.Category, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, api.Category{
			Slug: r.Slug, Name: r.Name, Eyebrow: &r.Eyebrow, Description: &r.Description, ProductCount: int(r.ProductCount),
		})
	}
	return out, nil
}

func (s *Server) GetCategory(ctx context.Context, r api.GetCategoryRequestObject) (api.GetCategoryResponseObject, error) {
	c, facets, err := s.Catalog.Category(ctx, r.Slug)
	if errors.Is(err, catalog.ErrNotFound) {
		return api.GetCategory404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pNotFound)}, nil
	}
	if err != nil {
		return nil, err
	}
	out := api.GetCategory200JSONResponse{
		Slug: c.Slug, Name: c.Name, Eyebrow: &c.Eyebrow, Description: &c.Description,
		ProductCount: int(c.ProductCount), Facets: make([]api.Facet, 0, len(facets)),
	}
	for _, f := range facets {
		af := api.Facet{Key: f.Key, Label: f.Label, Kind: api.FacetKind(f.Kind), Values: make([]api.FacetValue, 0, len(f.Values))}
		for _, v := range f.Values {
			af.Values = append(af.Values, api.FacetValue{Value: v.Value, Label: v.ValueLabel, SwatchHex: nonEmpty(v.SwatchHex), Count: int(v.ProductCount)})
		}
		out.Facets = append(out.Facets, af)
	}
	return out, nil
}

func (s *Server) ListProducts(ctx context.Context, r api.ListProductsRequestObject) (api.ListProductsResponseObject, error) {
	p := r.Params
	q := catalog.Query{
		Category: p.Category, MinPrice: p.MinPrice, MaxPrice: p.MaxPrice,
		Sort: "featured", Page: 1, PageSize: 12,
	}
	if p.Filter != nil {
		q.Filters = *p.Filter
	}
	if p.InStock != nil {
		q.InStock = *p.InStock
	}
	if p.Sort != nil {
		q.Sort = string(*p.Sort)
	}
	if p.Page != nil {
		q.Page = *p.Page
	}
	if p.PageSize != nil {
		q.PageSize = *p.PageSize
	}
	rows, total, err := s.Catalog.Products(ctx, q)
	if err != nil {
		return nil, err
	}
	out := api.ListProducts200JSONResponse{Page: q.Page, PageSize: q.PageSize, Total: total, Items: make([]api.ProductSummary, 0, len(rows))}
	for _, row := range rows {
		out.Items = append(out.Items, summaryJSON(store.ListRelatedProductsRow(row)))
	}
	return out, nil
}

func (s *Server) ListRelatedProducts(ctx context.Context, r api.ListRelatedProductsRequestObject) (api.ListRelatedProductsResponseObject, error) {
	rows, err := s.Catalog.Related(ctx, r.Slug)
	if errors.Is(err, catalog.ErrNotFound) {
		return api.ListRelatedProducts404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pNotFound)}, nil
	}
	if err != nil {
		return nil, err
	}
	out := api.ListRelatedProducts200JSONResponse{Items: make([]api.ProductSummary, 0, len(rows))}
	for _, row := range rows {
		out.Items = append(out.Items, summaryJSON(row))
	}
	return out, nil
}

func (s *Server) ListProductReviews(ctx context.Context, r api.ListProductReviewsRequestObject) (api.ListProductReviewsResponseObject, error) {
	page, size := 1, 5
	if r.Params.Page != nil {
		page = *r.Params.Page
	}
	if r.Params.PageSize != nil {
		size = *r.Params.PageSize
	}
	sum, rows, total, err := s.Catalog.Reviews(ctx, r.Slug, page, size)
	if errors.Is(err, catalog.ErrNotFound) {
		return api.ListProductReviews404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pNotFound)}, nil
	}
	if err != nil {
		return nil, err
	}
	out := api.ListProductReviews200JSONResponse{Page: page, PageSize: size, Total: total, Items: make([]api.Review, 0, len(rows))}
	out.Summary.RatingAvg, out.Summary.RatingCount = sum.Avg, sum.Count
	for i, n := range sum.Distribution {
		out.Summary.Distribution = append(out.Summary.Distribution, struct {
			Count int `json:"count"`
			Stars int `json:"stars"`
		}{Count: n, Stars: 5 - i})
	}
	for _, rv := range rows {
		out.Items = append(out.Items, api.Review{
			Id: rv.ID, Rating: int(rv.Rating), AuthorName: rv.AuthorName, AuthorMeta: nonEmpty(rv.AuthorMeta),
			Body: rv.Body, VariantLabel: nonEmpty(rv.VariantLabel), Verified: rv.Verified, CreatedAt: rv.CreatedAt.Time,
		})
	}
	return out, nil
}

func summaryJSON(row store.ListRelatedProductsRow) api.ProductSummary {
	return api.ProductSummary{
		Id: row.ID, Slug: row.Slug, Name: row.Name, Subtitle: nonEmpty(row.Subtitle),
		MaterialLabel: nonEmpty(row.MaterialLabel), SizeLabel: nonEmpty(row.SizeLabel),
		Image: api.Image{Url: row.ImageUrl, Alt: row.ImageAlt}, Badges: badges(row.Badges),
		PricePaise: row.PricePaise, MrpPaise: row.MrpPaise,
		RatingAvg: row.RatingAvg, RatingCount: int(row.RatingCount),
		InStock: row.TotalStock > 0, LowStock: lowStock(row.TotalStock),
		ShippingNote: nonEmpty(row.ShippingNote), DefaultVariantId: row.VariantID,
	}
}

func (s *Server) GetProduct(ctx context.Context, r api.GetProductRequestObject) (api.GetProductResponseObject, error) {
	p, err := s.Catalog.Product(ctx, r.Slug)
	if errors.Is(err, catalog.ErrNotFound) {
		return api.GetProduct404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pNotFound)}, nil
	}
	if err != nil {
		return nil, err
	}
	def := p.Variants[0]
	var total int32
	variants := make([]api.Variant, 0, len(p.Variants))
	for _, v := range p.Variants {
		total += v.Stock
		var opts map[string]string
		_ = json.Unmarshal(v.Options, &opts)
		variants = append(variants, api.Variant{
			Id: v.ID, Sku: v.Sku, Options: opts, PricePaise: v.PricePaise, MrpPaise: v.MrpPaise,
			InStock: v.Stock > 0, LowStock: lowStock(v.Stock),
		})
	}
	images := make([]api.Image, 0, len(p.Images))
	for _, i := range p.Images {
		images = append(images, api.Image{Url: i.Url, Alt: i.Alt})
	}
	if len(images) == 0 {
		images = append(images, api.Image{Url: "", Alt: p.Name})
	}
	out := api.GetProduct200JSONResponse{
		Id: p.ID, Slug: p.Slug, Name: p.Name, Subtitle: nonEmpty(p.Subtitle), Series: nonEmpty(p.Series),
		MaterialLabel: nonEmpty(p.MaterialLabel), SizeLabel: nonEmpty(p.SizeLabel), Description: p.Description,
		ShippingNote: nonEmpty(p.ShippingNote), Badges: badges(p.Badges),
		Category: api.CategoryRef{Slug: p.CategorySlug, Name: p.CategoryName},
		Image:    images[0], Images: images, Variants: variants,
		PricePaise: def.PricePaise, MrpPaise: def.MrpPaise, DefaultVariantId: def.ID,
		RatingAvg: p.RatingAvg, RatingCount: int(p.RatingCount),
		InStock: total > 0, LowStock: lowStock(total),
		GstRatePercent: float64(p.GstRateBps) / 100,
		Options:        []api.ProductOption{}, Features: []api.ProductFeature{},
		Specs: []api.SpecSection{}, Care: []api.CareNote{}, Facts: []api.ProductFact{},
	}
	_ = json.Unmarshal(p.Options, &out.Options)
	_ = json.Unmarshal(p.Features, &out.Features)
	_ = json.Unmarshal(p.Specs, &out.Specs)
	_ = json.Unmarshal(p.Care, &out.Care)
	_ = json.Unmarshal(p.Facts, &out.Facts)
	return out, nil
}

func badges(raw json.RawMessage) []api.Badge {
	out := []api.Badge{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func lowStock(stock int32) *int {
	if stock > 0 && stock <= catalog.LowStockThreshold {
		n := int(stock)
		return &n
	}
	return nil
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// imageOf builds an API image from a stored url/alt pair.
func imageOf(url, alt string) api.Image { return api.Image{Url: url, Alt: alt} }

package httpapi

import (
	"context"
	"errors"

	"github.com/pinnacleverve/api/internal/api"
	"github.com/pinnacleverve/api/internal/cart"
)

func (s *Server) GetCart(ctx context.Context, _ api.GetCartRequestObject) (api.GetCartResponseObject, error) {
	c, err := s.Cart.Get(ctx)
	if err != nil {
		return nil, err
	}
	return api.GetCart200JSONResponse{CartJSONResponse: cartJSON(c)}, nil
}

func (s *Server) AddCartItem(ctx context.Context, r api.AddCartItemRequestObject) (api.AddCartItemResponseObject, error) {
	c, err := s.Cart.Add(ctx, r.Body.VariantId, r.Body.Quantity)
	switch {
	case errors.Is(err, cart.ErrNotFound):
		return api.AddCartItem404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pNotFound)}, nil
	case errors.Is(err, cart.ErrOutOfStock):
		return api.AddCartItem409ApplicationProblemPlusJSONResponse(pOutOfStock), nil
	case errors.Is(err, cart.ErrLineLimit):
		return api.AddCartItem422ApplicationProblemPlusJSONResponse(pLineLimit), nil
	case err != nil:
		return nil, err
	}
	return api.AddCartItem200JSONResponse{CartJSONResponse: cartJSON(c)}, nil
}

func (s *Server) UpdateCartItem(ctx context.Context, r api.UpdateCartItemRequestObject) (api.UpdateCartItemResponseObject, error) {
	c, err := s.Cart.Update(ctx, r.ItemId, r.Body.Quantity)
	switch {
	case errors.Is(err, cart.ErrNotFound):
		return api.UpdateCartItem404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pNotFound)}, nil
	case errors.Is(err, cart.ErrOutOfStock):
		return api.UpdateCartItem409ApplicationProblemPlusJSONResponse(pOutOfStock), nil
	case err != nil:
		return nil, err
	}
	return api.UpdateCartItem200JSONResponse{CartJSONResponse: cartJSON(c)}, nil
}

func (s *Server) RemoveCartItem(ctx context.Context, r api.RemoveCartItemRequestObject) (api.RemoveCartItemResponseObject, error) {
	c, err := s.Cart.Remove(ctx, r.ItemId)
	if errors.Is(err, cart.ErrNotFound) {
		return api.RemoveCartItem404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pNotFound)}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.RemoveCartItem200JSONResponse{CartJSONResponse: cartJSON(c)}, nil
}

func (s *Server) SetCartAddons(ctx context.Context, r api.SetCartAddonsRequestObject) (api.SetCartAddonsResponseObject, error) {
	c, err := s.Cart.SetAddons(ctx, r.Body.Codes)
	if errors.Is(err, cart.ErrUnknownAddon) {
		return api.SetCartAddons422ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pUnknownAddon)}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.SetCartAddons200JSONResponse{CartJSONResponse: cartJSON(c)}, nil
}

func (s *Server) ListAddons(ctx context.Context, _ api.ListAddonsRequestObject) (api.ListAddonsResponseObject, error) {
	rows, err := s.Cart.Addons(ctx)
	if err != nil {
		return nil, err
	}
	out := api.ListAddons200JSONResponse{Items: make([]api.Addon, 0, len(rows))}
	for _, a := range rows {
		out.Items = append(out.Items, api.Addon{Code: a.Code, Name: a.Name, Description: a.Description, PricePaise: a.PricePaise})
	}
	return out, nil
}

func cartJSON(c cart.Cart) api.CartJSONResponse {
	out := api.CartJSONResponse{
		Items: make([]api.CartItem, 0, len(c.Lines)), Addons: make([]api.Addon, 0, len(c.Addons)),
		Totals: totalsJSON(c.Totals.SubtotalPaise, c.Totals.DiscountPaise, c.Totals.ShippingPaise,
			c.Totals.AddonsPaise, c.Totals.TaxIncludedPaise, c.Totals.TotalPaise),
		ItemCount:  c.ItemCount,
		WhiteGlove: &api.WhiteGloveProgress{ThresholdPaise: c.WhiteGloveThreshold, RemainingPaise: c.WhiteGloveRemaining},
	}
	for _, l := range c.Lines {
		out.Items = append(out.Items, api.CartItem{
			Id: l.ID, VariantId: l.VariantID, ProductSlug: l.ProductSlug, ProductName: l.ProductName,
			Eyebrow: nonEmpty(l.Series), VariantLabel: l.VariantLabel, Image: imageOf(l.ImageUrl, l.ImageAlt),
			UnitPricePaise: l.PricePaise, Quantity: int(l.Quantity), LineTotalPaise: l.LineTotal, MaxQuantity: l.MaxQuantity,
		})
	}
	for _, a := range c.Addons {
		out.Addons = append(out.Addons, api.Addon{Code: a.Code, Name: a.Name, Description: a.Description, PricePaise: a.PricePaise})
	}
	return out
}

func totalsJSON(subtotal, discount, shipping, addons, tax, total int64) api.Totals {
	return api.Totals{
		SubtotalPaise: subtotal, DiscountPaise: discount, ShippingPaise: shipping,
		AddonsPaise: addons, TaxIncludedPaise: tax, TotalPaise: total,
	}
}

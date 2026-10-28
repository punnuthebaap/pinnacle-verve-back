package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/api"
	"github.com/pinnacleverve/api/internal/settings"
	"github.com/pinnacleverve/api/internal/store"
)

// settingsProblem maps settings errors; ok=false means the error is unexpected (→ 500).
func settingsProblem(err error) (api.Problem, bool) {
	var inv *settings.InvalidError
	switch {
	case errors.Is(err, admin.ErrForbidden):
		return pForbidden, true
	case errors.As(err, &inv):
		return problem(422, "invalid", inv.Msg), true
	}
	return api.Problem{}, false
}

func (s *Server) GetCheckoutOptions(ctx context.Context, _ api.GetCheckoutOptionsRequestObject) (api.GetCheckoutOptionsResponseObject, error) {
	rules, err := s.Settings.Current(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := api.GetCheckoutOptions200JSONResponse{WhiteGloveThresholdPaise: rules.WhiteGloveThresholdPaise, PaymentMethods: []api.PaymentMethod{}}
	for _, m := range rules.PaymentMethods {
		out.PaymentMethods = append(out.PaymentMethods, api.PaymentMethod(m))
	}
	return out, nil
}

func (s *Server) GetContact(ctx context.Context, _ api.GetContactRequestObject) (api.GetContactResponseObject, error) {
	rules, err := s.Settings.Current(ctx, nil)
	if err != nil {
		return nil, err
	}
	return api.GetContact200JSONResponse{WhatsappNumber: nonEmpty(rules.WhatsAppNumber)}, nil
}

func settingsJSON(st settings.Settings) api.StoreSettingsJSONResponse {
	out := api.StoreSettingsJSONResponse{
		WhiteGloveThresholdPaise: st.WhiteGloveThresholdPaise, ShippingPaise: st.ShippingPaise, AddonGstRateBps: int(st.AddonGSTRateBps),
		PendingOrderTtlMinutes: int(st.PendingOrderTTL.Minutes()), WhatsappNumber: st.WhatsAppNumber, UpdatedAt: st.UpdatedAt, UpdatedBy: nonEmpty(st.UpdatedBy),
		PaymentMethods: []api.StoreSettingsPaymentMethods{}, SupportedPaymentMethods: []api.PaymentMethod{},
	}
	for _, m := range st.PaymentMethods {
		out.PaymentMethods = append(out.PaymentMethods, api.StoreSettingsPaymentMethods(m))
	}
	for _, m := range settings.PaymentMethods {
		out.SupportedPaymentMethods = append(out.SupportedPaymentMethods, api.PaymentMethod(m))
	}
	return out
}

func (s *Server) AdminGetSettings(ctx context.Context, _ api.AdminGetSettingsRequestObject) (api.AdminGetSettingsResponseObject, error) {
	st, err := s.Settings.Get(ctx)
	if errors.Is(err, admin.ErrForbidden) {
		return api.AdminGetSettings403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pForbidden)}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.AdminGetSettings200JSONResponse{StoreSettingsJSONResponse: settingsJSON(st)}, nil
}

func (s *Server) AdminUpdateSettings(ctx context.Context, r api.AdminUpdateSettingsRequestObject) (api.AdminUpdateSettingsResponseObject, error) {
	b := r.Body
	in := settings.Commercial{
		WhiteGloveThresholdPaise: b.WhiteGloveThresholdPaise, ShippingPaise: b.ShippingPaise, AddonGSTRateBps: int64(b.AddonGstRateBps),
		PendingOrderTTL: time.Duration(b.PendingOrderTtlMinutes) * time.Minute, WhatsAppNumber: b.WhatsappNumber,
	}
	for _, m := range b.PaymentMethods {
		in.PaymentMethods = append(in.PaymentMethods, string(m))
	}
	st, err := s.Settings.Update(ctx, in)
	if err != nil {
		p, ok := settingsProblem(err)
		switch {
		case !ok:
			return nil, err
		case p.Status == 403:
			return api.AdminUpdateSettings403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}, nil
		}
		return api.AdminUpdateSettings422ApplicationProblemPlusJSONResponse(p), nil
	}
	return api.AdminUpdateSettings200JSONResponse{StoreSettingsJSONResponse: settingsJSON(st)}, nil
}

func addonsJSON(rows []store.Addon) api.AdminAddonsJSONResponse {
	out := api.AdminAddonsJSONResponse{Items: make([]api.AdminAddon, 0, len(rows))}
	for _, a := range rows {
		out.Items = append(out.Items, api.AdminAddon{
			Code: a.Code, Name: a.Name, Description: a.Description, PricePaise: a.PricePaise, IsActive: a.IsActive, Position: int(a.Position),
		})
	}
	return out
}

func (s *Server) AdminListAddons(ctx context.Context, _ api.AdminListAddonsRequestObject) (api.AdminListAddonsResponseObject, error) {
	rows, err := s.Settings.Addons(ctx)
	if errors.Is(err, admin.ErrForbidden) {
		return api.AdminListAddons403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pForbidden)}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.AdminListAddons200JSONResponse{AdminAddonsJSONResponse: addonsJSON(rows)}, nil
}

func (s *Server) AdminSaveAddon(ctx context.Context, r api.AdminSaveAddonRequestObject) (api.AdminSaveAddonResponseObject, error) {
	b := r.Body
	rows, err := s.Settings.SaveAddon(ctx, settings.AddonInput{
		Code: r.Code, Name: b.Name, Description: b.Description, PricePaise: b.PricePaise, IsActive: b.IsActive, Position: int32(b.Position),
	})
	if err != nil {
		p, ok := settingsProblem(err)
		switch {
		case !ok:
			return nil, err
		case p.Status == 403:
			return api.AdminSaveAddon403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(p)}, nil
		}
		return api.AdminSaveAddon422ApplicationProblemPlusJSONResponse(p), nil
	}
	return api.AdminSaveAddon200JSONResponse{AdminAddonsJSONResponse: addonsJSON(rows)}, nil
}

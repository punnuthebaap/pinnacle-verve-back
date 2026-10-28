package httpapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/api"
	"github.com/pinnacleverve/api/internal/content"
)

// contentProblem maps content errors; ok=false means the error is unexpected (→ 500).
func contentProblem(err error) (api.Problem, bool) {
	var inv *content.InvalidError
	switch {
	case errors.Is(err, admin.ErrForbidden):
		return pForbidden, true
	case errors.Is(err, content.ErrNotFound):
		return pNotFound, true
	case errors.As(err, &inv):
		return problem(422, "invalid", inv.Msg), true
	}
	return api.Problem{}, false
}

func siteLinks(c api.SiteContent) []string {
	var out []string
	for _, l := range c.PrimaryNav {
		out = append(out, l.Href)
	}
	for _, col := range c.FooterColumns {
		for _, l := range col.Links {
			out = append(out, l.Href)
		}
	}
	for _, l := range c.LegalLinks {
		out = append(out, l.Href)
	}
	return out
}

// ---- public ----

func (s *Server) GetSiteContent(ctx context.Context, _ api.GetSiteContentRequestObject) (api.GetSiteContentResponseObject, error) {
	d, err := s.Content.Document(ctx, "site")
	if errors.Is(err, content.ErrNotFound) {
		return api.GetSiteContent404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pNotFound)}, nil
	}
	if err != nil {
		return nil, err
	}
	var out api.GetSiteContent200JSONResponse
	return out, json.Unmarshal(d.Data, &out)
}

func (s *Server) GetHomeContent(ctx context.Context, _ api.GetHomeContentRequestObject) (api.GetHomeContentResponseObject, error) {
	d, err := s.Content.Document(ctx, "home")
	if errors.Is(err, content.ErrNotFound) {
		return api.GetHomeContent404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pNotFound)}, nil
	}
	if err != nil {
		return nil, err
	}
	var out api.GetHomeContent200JSONResponse
	return out, json.Unmarshal(d.Data, &out)
}

func (s *Server) GetContentPage(ctx context.Context, r api.GetContentPageRequestObject) (api.GetContentPageResponseObject, error) {
	p, err := s.Content.PublishedPage(ctx, r.Slug)
	if errors.Is(err, content.ErrNotFound) {
		return api.GetContentPage404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pNotFound)}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.GetContentPage200JSONResponse{Slug: p.Slug, Title: p.Title, Body: p.Body, UpdatedAt: p.UpdatedAt}, nil
}

// ---- admin: documents ----

func (s *Server) AdminGetSiteContent(ctx context.Context, _ api.AdminGetSiteContentRequestObject) (api.AdminGetSiteContentResponseObject, error) {
	d, err := s.Content.AdminDocument(ctx, "site")
	if p, ok := contentProblem(err); ok {
		if p.Status == 403 {
			return api.AdminGetSiteContent403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: forbidden()}, nil
		}
		return api.AdminGetSiteContent404ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	out := api.AdminGetSiteContent200JSONResponse{UpdatedAt: d.UpdatedAt, UpdatedBy: nonEmpty(d.UpdatedBy)}
	return out, json.Unmarshal(d.Data, &out.Content)
}

func (s *Server) AdminSaveSiteContent(ctx context.Context, r api.AdminSaveSiteContentRequestObject) (api.AdminSaveSiteContentResponseObject, error) {
	d, err := s.Content.SaveDocument(ctx, "site", r.Body, siteLinks(*r.Body))
	if p, ok := contentProblem(err); ok {
		if p.Status == 403 {
			return api.AdminSaveSiteContent403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: forbidden()}, nil
		}
		return api.AdminSaveSiteContent422ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	out := api.AdminSaveSiteContent200JSONResponse{UpdatedAt: d.UpdatedAt, UpdatedBy: nonEmpty(d.UpdatedBy)}
	return out, json.Unmarshal(d.Data, &out.Content)
}

func (s *Server) AdminGetHomeContent(ctx context.Context, _ api.AdminGetHomeContentRequestObject) (api.AdminGetHomeContentResponseObject, error) {
	d, err := s.Content.AdminDocument(ctx, "home")
	if p, ok := contentProblem(err); ok {
		if p.Status == 403 {
			return api.AdminGetHomeContent403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: forbidden()}, nil
		}
		return api.AdminGetHomeContent404ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	out := api.AdminGetHomeContent200JSONResponse{UpdatedAt: d.UpdatedAt, UpdatedBy: nonEmpty(d.UpdatedBy)}
	return out, json.Unmarshal(d.Data, &out.Content)
}

func (s *Server) AdminSaveHomeContent(ctx context.Context, r api.AdminSaveHomeContentRequestObject) (api.AdminSaveHomeContentResponseObject, error) {
	d, err := s.Content.SaveDocument(ctx, "home", r.Body, nil)
	if p, ok := contentProblem(err); ok {
		if p.Status == 403 {
			return api.AdminSaveHomeContent403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: forbidden()}, nil
		}
		return api.AdminSaveHomeContent422ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	out := api.AdminSaveHomeContent200JSONResponse{UpdatedAt: d.UpdatedAt, UpdatedBy: nonEmpty(d.UpdatedBy)}
	return out, json.Unmarshal(d.Data, &out.Content)
}

// ---- admin: pages ----

func adminPageJSON(p content.Page) api.AdminContentPage {
	return api.AdminContentPage{Slug: p.Slug, Title: p.Title, Body: p.Body, Published: p.Published, UpdatedAt: p.UpdatedAt, UpdatedBy: nonEmpty(p.UpdatedBy)}
}

func (s *Server) AdminListContentPages(ctx context.Context, _ api.AdminListContentPagesRequestObject) (api.AdminListContentPagesResponseObject, error) {
	rows, err := s.Content.Pages(ctx)
	if errors.Is(err, admin.ErrForbidden) {
		return api.AdminListContentPages403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: forbidden()}, nil
	}
	if err != nil {
		return nil, err
	}
	type item = struct {
		Slug      string `json:"slug"`
		Title     string `json:"title"`
		Published bool   `json:"published"`
		UpdatedAt any    `json:"updatedAt"`
		UpdatedBy string `json:"updatedBy,omitempty"`
	}
	items := make([]item, 0, len(rows))
	for _, r := range rows {
		items = append(items, item{Slug: r.Slug, Title: r.Title, Published: r.Published, UpdatedAt: r.UpdatedAt.Time, UpdatedBy: r.UpdatedByName})
	}
	// Round-trip through JSON into the generated (anonymous) item type.
	raw, _ := json.Marshal(map[string]any{"items": items})
	var out api.AdminListContentPages200JSONResponse
	return out, json.Unmarshal(raw, &out)
}

func (s *Server) AdminGetContentPage(ctx context.Context, r api.AdminGetContentPageRequestObject) (api.AdminGetContentPageResponseObject, error) {
	p, err := s.Content.AdminPage(ctx, r.Slug)
	if pr, ok := contentProblem(err); ok {
		if pr.Status == 403 {
			return api.AdminGetContentPage403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: forbidden()}, nil
		}
		return api.AdminGetContentPage404ApplicationProblemPlusJSONResponse(pr), nil
	}
	if err != nil {
		return nil, err
	}
	return api.AdminGetContentPage200JSONResponse(adminPageJSON(p)), nil
}

func (s *Server) AdminSaveContentPage(ctx context.Context, r api.AdminSaveContentPageRequestObject) (api.AdminSaveContentPageResponseObject, error) {
	p, err := s.Content.SavePage(ctx, content.Page{Slug: r.Slug, Title: r.Body.Title, Body: r.Body.Body, Published: r.Body.Published})
	if pr, ok := contentProblem(err); ok {
		if pr.Status == 403 {
			return api.AdminSaveContentPage403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: forbidden()}, nil
		}
		return api.AdminSaveContentPage422ApplicationProblemPlusJSONResponse(pr), nil
	}
	if err != nil {
		return nil, err
	}
	return api.AdminSaveContentPage200JSONResponse(adminPageJSON(p)), nil
}

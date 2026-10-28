package httpapi

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/api"
	"github.com/pinnacleverve/api/internal/marketing"
)

func forbidden() api.ProblemApplicationProblemPlusJSONResponse {
	return api.ProblemApplicationProblemPlusJSONResponse(pForbidden)
}

func (s *Server) AdminListReviews(ctx context.Context, r api.AdminListReviewsRequestObject) (api.AdminListReviewsResponseObject, error) {
	rq := marketing.ReviewQuery{Page: 1, WrittenOnly: true, ProductID: r.Params.ProductId, Rating: r.Params.Rating}
	if r.Params.Q != nil {
		rq.Q = *r.Params.Q
	}
	if r.Params.Status != nil {
		st := string(*r.Params.Status)
		rq.Status = &st
	}
	if r.Params.WrittenOnly != nil {
		rq.WrittenOnly = *r.Params.WrittenOnly
	}
	if r.Params.Page != nil {
		rq.Page = *r.Params.Page
	}
	rows, total, pending, err := s.Marketing.Reviews(ctx, rq)
	if errors.Is(err, admin.ErrForbidden) {
		return api.AdminListReviews403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: forbidden()}, nil
	}
	if err != nil {
		return nil, err
	}
	out := api.AdminListReviews200JSONResponse{Page: rq.Page, Total: total, Pending: pending, Items: make([]api.AdminReview, 0, len(rows))}
	for _, rv := range rows {
		item := api.AdminReview{
			Id: rv.ID, Rating: int(rv.Rating), AuthorName: rv.AuthorName, AuthorMeta: rv.AuthorMeta, Body: rv.Body,
			VariantLabel: rv.VariantLabel, Verified: rv.Verified, Status: api.ReviewStatus(rv.Status), CreatedAt: rv.CreatedAt.Time,
			ModeratedBy: nonEmpty(rv.ModeratedByName),
		}
		item.Product.Id, item.Product.Slug, item.Product.Name = rv.ProductID, rv.ProductSlug, rv.ProductName
		if rv.ModeratedAt.Valid {
			item.ModeratedAt = &rv.ModeratedAt.Time
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (s *Server) AdminSetReviewStatus(ctx context.Context, r api.AdminSetReviewStatusRequestObject) (api.AdminSetReviewStatusResponseObject, error) {
	err := s.Marketing.SetReviewStatus(ctx, r.Id, string(r.Body.Status))
	switch {
	case errors.Is(err, admin.ErrForbidden):
		return api.AdminSetReviewStatus403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: forbidden()}, nil
	case errors.Is(err, marketing.ErrNotFound):
		return api.AdminSetReviewStatus404ApplicationProblemPlusJSONResponse(pNotFound), nil
	case err != nil:
		return nil, err
	}
	return api.AdminSetReviewStatus204Response{}, nil
}

func (s *Server) AdminListSubscribers(ctx context.Context, r api.AdminListSubscribersRequestObject) (api.AdminListSubscribersResponseObject, error) {
	q, page := "", 1
	if r.Params.Q != nil {
		q = *r.Params.Q
	}
	if r.Params.Page != nil {
		page = *r.Params.Page
	}
	rows, total, err := s.Marketing.Subscribers(ctx, q, page)
	if errors.Is(err, admin.ErrForbidden) {
		return api.AdminListSubscribers403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: forbidden()}, nil
	}
	if err != nil {
		return nil, err
	}
	out := api.AdminListSubscribers200JSONResponse{Page: page, Total: total}
	out.Items = make([]struct {
		CreatedAt time.Time `json:"createdAt"`
		Email     string    `json:"email"`
	}, 0, len(rows))
	for _, row := range rows {
		out.Items = append(out.Items, struct {
			CreatedAt time.Time `json:"createdAt"`
			Email     string    `json:"email"`
		}{CreatedAt: row.CreatedAt.Time, Email: row.Email})
	}
	return out, nil
}

func (s *Server) AdminExportSubscribers(ctx context.Context, _ api.AdminExportSubscribersRequestObject) (api.AdminExportSubscribersResponseObject, error) {
	var buf bytes.Buffer
	_, err := s.Marketing.ExportSubscribers(ctx, &buf)
	if errors.Is(err, admin.ErrForbidden) {
		return api.AdminExportSubscribers403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: forbidden()}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.AdminExportSubscribers200TextcsvResponse{Body: &buf, ContentLength: int64(buf.Len())}, nil
}

func (s *Server) AdminRemoveSubscriber(ctx context.Context, r api.AdminRemoveSubscriberRequestObject) (api.AdminRemoveSubscriberResponseObject, error) {
	err := s.Marketing.RemoveSubscriber(ctx, r.Email)
	switch {
	case errors.Is(err, admin.ErrForbidden):
		return api.AdminRemoveSubscriber403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: forbidden()}, nil
	case errors.Is(err, marketing.ErrNotFound):
		return api.AdminRemoveSubscriber404ApplicationProblemPlusJSONResponse(pNotFound), nil
	case err != nil:
		return nil, err
	}
	return api.AdminRemoveSubscriber204Response{}, nil
}

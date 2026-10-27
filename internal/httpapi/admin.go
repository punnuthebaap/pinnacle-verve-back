package httpapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/pinnacleverve/api/internal/admin"
	"github.com/pinnacleverve/api/internal/api"
	"github.com/pinnacleverve/api/internal/store"
)

var (
	pInvalidCredentials = problem(401, "invalid_credentials", "Incorrect email or password")
	pLocked             = problem(429, "account_locked", "Too many failed attempts. Try again in 15 minutes.")
	pForbidden          = problem(403, "forbidden", "Your role does not allow this action")
	pEmailTaken         = problem(409, "email_taken", "An admin with this email already exists")
	pLastOwner          = problem(409, "last_owner", "The last active owner cannot be demoted or disabled")
	pWrongPassword      = problem(422, "wrong_password", "Current password is incorrect")
)

func (s *Server) AdminLogin(ctx context.Context, r api.AdminLoginRequestObject) (api.AdminLoginResponseObject, error) {
	u, err := s.Admin.Login(ctx, r.Body.Email, r.Body.Password)
	switch {
	case errors.Is(err, admin.ErrInvalidCredentials):
		return api.AdminLogin401ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pInvalidCredentials)}, nil
	case errors.Is(err, admin.ErrLocked):
		return api.AdminLogin429ApplicationProblemPlusJSONResponse(pLocked), nil
	case err != nil:
		return nil, err
	}
	return api.AdminLogin200JSONResponse(adminJSON(u)), nil
}

func (s *Server) AdminLogout(ctx context.Context, _ api.AdminLogoutRequestObject) (api.AdminLogoutResponseObject, error) {
	if err := s.Admin.Logout(ctx); err != nil {
		return nil, err
	}
	return api.AdminLogout204Response{}, nil
}

func (s *Server) AdminMe(ctx context.Context, _ api.AdminMeRequestObject) (api.AdminMeResponseObject, error) {
	return api.AdminMe200JSONResponse(adminJSON(admin.Current(ctx))), nil
}

func (s *Server) AdminChangePassword(ctx context.Context, r api.AdminChangePasswordRequestObject) (api.AdminChangePasswordResponseObject, error) {
	err := s.Admin.ChangeOwnPassword(ctx, r.Body.CurrentPassword, r.Body.NewPassword)
	if errors.Is(err, admin.ErrInvalidCredentials) {
		return api.AdminChangePassword422ApplicationProblemPlusJSONResponse(pWrongPassword), nil
	}
	if err != nil {
		return nil, err
	}
	return api.AdminChangePassword204Response{}, nil
}

func (s *Server) AdminListUsers(ctx context.Context, _ api.AdminListUsersRequestObject) (api.AdminListUsersResponseObject, error) {
	users, err := s.Admin.Users(ctx)
	if errors.Is(err, admin.ErrForbidden) {
		return api.AdminListUsers403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pForbidden)}, nil
	}
	if err != nil {
		return nil, err
	}
	out := api.AdminListUsers200JSONResponse{Items: make([]api.AdminUser, 0, len(users))}
	for _, u := range users {
		out.Items = append(out.Items, adminJSON(u))
	}
	return out, nil
}

func (s *Server) AdminCreateUser(ctx context.Context, r api.AdminCreateUserRequestObject) (api.AdminCreateUserResponseObject, error) {
	u, err := s.Admin.CreateUser(ctx, admin.NewUser{Email: string(r.Body.Email), Name: r.Body.Name, Role: string(r.Body.Role), Password: r.Body.Password})
	switch {
	case errors.Is(err, admin.ErrForbidden):
		return api.AdminCreateUser403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pForbidden)}, nil
	case errors.Is(err, admin.ErrEmailTaken):
		return api.AdminCreateUser409ApplicationProblemPlusJSONResponse(pEmailTaken), nil
	case err != nil:
		return nil, err
	}
	return api.AdminCreateUser201JSONResponse(adminJSON(u)), nil
}

func (s *Server) AdminUpdateUser(ctx context.Context, r api.AdminUpdateUserRequestObject) (api.AdminUpdateUserResponseObject, error) {
	p := admin.UserPatch{Name: r.Body.Name, IsActive: r.Body.IsActive, Password: r.Body.Password}
	if r.Body.Role != nil {
		role := string(*r.Body.Role)
		p.Role = &role
	}
	u, err := s.Admin.UpdateUser(ctx, r.Id, p)
	switch {
	case errors.Is(err, admin.ErrForbidden):
		return api.AdminUpdateUser403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pForbidden)}, nil
	case errors.Is(err, admin.ErrNotFound):
		return api.AdminUpdateUser404ApplicationProblemPlusJSONResponse(pNotFound), nil
	case errors.Is(err, admin.ErrLastOwner):
		return api.AdminUpdateUser409ApplicationProblemPlusJSONResponse(pLastOwner), nil
	case err != nil:
		return nil, err
	}
	return api.AdminUpdateUser200JSONResponse(adminJSON(u)), nil
}

func (s *Server) AdminListAudit(ctx context.Context, r api.AdminListAuditRequestObject) (api.AdminListAuditResponseObject, error) {
	page := 1
	if r.Params.Page != nil {
		page = *r.Params.Page
	}
	rows, total, err := s.Admin.AuditLog(ctx, page)
	if errors.Is(err, admin.ErrForbidden) {
		return api.AdminListAudit403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: api.ProblemApplicationProblemPlusJSONResponse(pForbidden)}, nil
	}
	if err != nil {
		return nil, err
	}
	out := api.AdminListAudit200JSONResponse{Page: page, Total: total, Items: make([]api.AuditEntry, 0, len(rows))}
	for _, e := range rows {
		data := map[string]any{}
		_ = json.Unmarshal(e.Data, &data)
		out.Items = append(out.Items, api.AuditEntry{
			Id: e.ID, Action: e.Action, Entity: e.Entity, EntityId: e.EntityID, AdminEmail: e.AdminEmail,
			Ip: e.Ip, Data: data, CreatedAt: e.CreatedAt.Time,
		})
	}
	return out, nil
}

func adminJSON(u store.AdminUser) api.AdminUser {
	out := api.AdminUser{Id: u.ID, Email: u.Email, Name: u.Name, Role: api.AdminRole(u.Role), IsActive: u.IsActive, CreatedAt: u.CreatedAt.Time}
	if u.LastLoginAt.Valid {
		out.LastLoginAt = &u.LastLoginAt.Time
	}
	return out
}

package api

import (
	"context"
	"errors"

	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"

	connect "connectrpc.com/connect"
)

var _ opensightv1connect.AuthServiceHandler = (*Server)(nil)

// Logout deletes the current session row and clears the cookie. Not in
// publicProcedures, so the session interceptor already validated the session
// before this runs.
func (s *Server) Logout(ctx context.Context, req *connect.Request[opensightv1.LogoutRequest]) (*connect.Response[opensightv1.LogoutResponse], error) {
	if raw := sessionTokenFromHeader(req.Header()); raw != "" {
		if err := s.store.DeleteSession(ctx, hashSessionToken(raw)); err != nil {
			return nil, s.rpcError("logout: delete session", err)
		}
	}

	res := connect.NewResponse(&opensightv1.LogoutResponse{})
	res.Header().Add("Set-Cookie", s.expiredSessionCookie().String())
	return res, nil
}

// GetMe returns the global identity and every account membership. Account
// billing/business context is loaded separately after the user selects one.
func (s *Server) GetMe(ctx context.Context, req *connect.Request[opensightv1.GetMeRequest]) (*connect.Response[opensightv1.GetMeResponse], error) {
	su, ok := sessionUserFromContext(ctx)
	if !ok {
		// The interceptor should have set this for every non-public procedure;
		// reaching here means the wiring is broken, not a normal auth failure.
		return nil, s.rpcError("rpc: get me: missing session context", errors.New("missing session context"))
	}
	memberships, err := s.store.ListAccountMemberships(ctx, su.UserID)
	if err != nil {
		return nil, s.rpcError("me: list account memberships", err)
	}
	resp := &opensightv1.GetMeResponse{
		User:        &opensightv1.User{Id: su.UserID.String(), Email: su.Email},
		Memberships: make([]*opensightv1.AccountMembershipSummary, 0, len(memberships)),
	}
	for _, m := range memberships {
		resp.Memberships = append(resp.Memberships, &opensightv1.AccountMembershipSummary{
			Account: &opensightv1.Account{Id: m.Account.ID.String(), Name: m.Account.Name, Slug: m.Account.Slug},
			Role:    accountRoleToProto(m.Role),
		})
	}
	return connect.NewResponse(resp), nil
}

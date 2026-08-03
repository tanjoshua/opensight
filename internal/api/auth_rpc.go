package api

import (
	"context"
	"errors"

	"opensight/internal/billing"
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

// GetMe returns the current session's user, tenant, businesses, access, and
// plan — one authoritative payload, and the SPA's one call site for its own billing state. It does not extend expiry.
func (s *Server) GetMe(ctx context.Context, req *connect.Request[opensightv1.GetMeRequest]) (*connect.Response[opensightv1.GetMeResponse], error) {
	su, ok := sessionUserFromContext(ctx)
	if !ok {
		// The interceptor should have set this for every non-public procedure;
		// reaching here means the wiring is broken, not a normal auth failure.
		return nil, s.rpcError("rpc: get me: missing session context", errors.New("missing session context"))
	}
	access, ok := accessFromContext(ctx)
	if !ok {
		return nil, s.rpcError("rpc: get me: missing access context", errors.New("missing access context"))
	}

	businesses, err := s.store.ListBusinesses(ctx, su.TenantID)
	if err != nil {
		return nil, s.rpcError("me: list businesses", err)
	}
	// No store round trip: the plan_code came off the session.
	plan, err := billing.PlanFor(su.PlanCode)
	if err != nil {
		return nil, s.rpcInternal("me: resolve plan", err)
	}

	resp := &opensightv1.GetMeResponse{
		User:       &opensightv1.User{Id: su.UserID.String(), Email: su.Email},
		Tenant:     &opensightv1.Tenant{Id: su.TenantID.String(), Name: su.TenantName},
		Businesses: make([]*opensightv1.BusinessSummary, 0, len(businesses)),
		Access:     accessToProto(access),
		Plan:       planToProto(plan),
	}
	for _, b := range businesses {
		resp.Businesses = append(resp.Businesses, &opensightv1.BusinessSummary{
			Id:     b.ID.String(),
			Name:   b.Name,
			Status: businessStatusToProto(b.Status),
		})
	}
	return connect.NewResponse(resp), nil
}

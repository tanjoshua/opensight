package api

import (
	"context"
	"errors"
	"strings"

	"opensight/internal/auth"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
)

var _ opensightv1connect.AuthServiceHandler = (*Server)(nil)

// rpcLoginFailed is the single uniform auth failure — identical for unknown
// email, no credential, and wrong password, so there's no user-existence
// oracle. No Meta, no details: anything added here becomes an oracle.
func rpcLoginFailed() *connect.Error {
	return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid email or password"))
}

// Login authenticates email+password and, on success, mints a fresh session
// and sets the cookie. All three failure shapes — unknown email, user with no
// password_hash, and wrong password — return a byte-identical error and each
// burns one argon2id verify, so neither the response nor gross timing reveals
// whether an account exists. Mirrors handleLogin (auth.go) statement-for-
// statement.
func (s *Server) Login(ctx context.Context, req *connect.Request[opensightv1.LoginRequest]) (*connect.Response[opensightv1.LoginResponse], error) {
	email := strings.TrimSpace(req.Msg.Email)
	if email == "" || req.Msg.Password == "" {
		return nil, rpcInvalidArgument("email and password are required")
	}

	creds, err := s.auth.GetUserCredentials(ctx, email)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// Unknown email: burn a verify against the dummy hash so timing matches
		// the real-user path, then fail uniformly.
		_, _ = auth.VerifyPassword(auth.DummyHash, req.Msg.Password)
		return nil, rpcLoginFailed()
	case err != nil:
		return nil, s.rpcError("login: get user credentials", err)
	}

	hash := auth.DummyHash
	if creds.PasswordHash != nil {
		hash = *creds.PasswordHash
	}
	ok, err := auth.VerifyPassword(hash, req.Msg.Password)
	if err != nil || !ok || creds.PasswordHash == nil {
		// Malformed stored hash, mismatch, or a user with no credential yet all
		// fail identically. The verify above already ran for timing uniformity.
		return nil, rpcLoginFailed()
	}

	raw, tokenHash, err := newSessionToken()
	if err != nil {
		return nil, s.rpcError("login: mint session token", err)
	}
	if err := s.auth.CreateSession(ctx, store.CreateSessionParams{
		TokenHash: tokenHash,
		UserID:    creds.UserID,
		ExpiresAt: nowUTC().Add(s.sessionTTL),
	}); err != nil {
		return nil, s.rpcError("login: create session", err)
	}

	res := connect.NewResponse(&opensightv1.LoginResponse{
		User:   &opensightv1.User{Id: creds.UserID.String(), Email: creds.Email},
		Tenant: &opensightv1.Tenant{Id: creds.TenantID.String(), Name: creds.TenantName},
	})
	res.Header().Add("Set-Cookie", s.sessionCookie(raw).String())
	return res, nil
}

// Logout deletes the current session row and clears the cookie. Not in
// publicProcedures, so the session interceptor already validated the session
// before this runs.
func (s *Server) Logout(ctx context.Context, req *connect.Request[opensightv1.LogoutRequest]) (*connect.Response[opensightv1.LogoutResponse], error) {
	if raw := sessionTokenFromHeader(req.Header()); raw != "" {
		if err := s.auth.DeleteSession(ctx, hashSessionToken(raw)); err != nil {
			return nil, s.rpcError("logout: delete session", err)
		}
	}

	res := connect.NewResponse(&opensightv1.LogoutResponse{})
	res.Header().Add("Set-Cookie", s.expiredSessionCookie().String())
	return res, nil
}

// GetMe returns the current session's user, tenant, businesses, and plan
// prompt limit. It does not extend expiry.
func (s *Server) GetMe(ctx context.Context, req *connect.Request[opensightv1.GetMeRequest]) (*connect.Response[opensightv1.GetMeResponse], error) {
	if s.businesses == nil || s.plans == nil {
		return nil, s.rpcError("me: store missing", errors.New("business and plan stores are required"))
	}

	su, ok := sessionUserFromContext(ctx)
	if !ok {
		// The interceptor should have set this for every non-public procedure;
		// reaching here means the wiring is broken, not a normal auth failure.
		return nil, s.rpcError("rpc: get me: missing session context", errors.New("missing session context"))
	}

	businesses, err := s.businesses.ListBusinesses(ctx, su.TenantID)
	if err != nil {
		return nil, s.rpcError("me: list businesses", err)
	}
	plan, err := s.plans.GetTenantPlan(ctx, su.TenantID)
	if err != nil {
		return nil, s.rpcError("me: get tenant plan", err)
	}

	resp := &opensightv1.GetMeResponse{
		User:        &opensightv1.User{Id: su.UserID.String(), Email: su.Email},
		Tenant:      &opensightv1.Tenant{Id: su.TenantID.String(), Name: su.TenantName},
		Businesses:  make([]*opensightv1.BusinessSummary, 0, len(businesses)),
		PromptLimit: int32(plan.PromptLimit),
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

// businessStatusToProto maps the store's string-typed business status to the
// generated proto enum.
func businessStatusToProto(s store.BusinessStatus) opensightv1.BusinessStatus {
	switch s {
	case store.BusinessStatusDraft:
		return opensightv1.BusinessStatus_BUSINESS_STATUS_DRAFT
	case store.BusinessStatusActive:
		return opensightv1.BusinessStatus_BUSINESS_STATUS_ACTIVE
	default:
		return opensightv1.BusinessStatus_BUSINESS_STATUS_UNSPECIFIED
	}
}

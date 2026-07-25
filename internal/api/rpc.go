package api

import (
	"context"
	"errors"
	"net/http"

	"opensight/internal/domain"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
	"github.com/google/uuid"
)

// maxRPCRequestBytes matches today's REST body cap (64 KiB, applied via
// http.MaxBytesReader in ~9 handlers).
const maxRPCRequestBytes = 64 << 10

// publicProcedures is the default-deny allowlist: every procedure not listed
// here requires a live session. Keyed by generated procedure constants so a
// renamed/removed RPC breaks the build instead of silently changing access.
var publicProcedures = map[string]struct{}{
	opensightv1connect.AuthServiceLoginProcedure: {},
}

// sessionInterceptor resolves the session cookie into store.SessionUser and
// injects it into the request context, unless the procedure is in
// publicProcedures.
func (s *Server) sessionInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if _, public := publicProcedures[req.Spec().Procedure]; public {
				return next(ctx, req)
			}
			su, err := s.sessionFromHeader(ctx, req.Header())
			if err != nil {
				return nil, s.rpcError("rpc: resolve session", err)
			}
			return next(withSessionUser(ctx, su), req)
		}
	}
}

// rpcHandler builds the /rpc sub-tree. Chi's Mount doesn't rewrite
// r.URL.Path, but the generated Connect handler routes on it, so
// http.StripPrefix is mandatory or every request 404s.
func (s *Server) rpcHandler() http.Handler {
	opts := []connect.HandlerOption{
		connect.WithInterceptors(s.sessionInterceptor()),
		connect.WithRequireConnectProtocolHeader(),
		connect.WithReadMaxBytes(maxRPCRequestBytes),
	}
	mux := http.NewServeMux()
	mux.Handle(opensightv1connect.NewAuthServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewBusinessServiceHandler(s, opts...))
	// RPC-5/6 will each add one more mux.Handle(...) line here.
	return http.StripPrefix("/rpc", mux)
}

// rpcID parses a request's id field, mirroring pathID's 400 on a malformed
// UUID. Returns a concrete *connect.Error (never a typed-nil through the
// error interface) — every call site must check `if cerr != nil`.
func rpcID(name, raw string) (domain.ID, *connect.Error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, rpcInvalidArgument(name + " must be a UUID")
	}
	return id, nil
}

// rpcSessionUser pulls the session user the interceptor injected. A miss
// means broken wiring (the procedure would have to be in publicProcedures
// otherwise), not a normal auth failure — so it's Internal.
func (s *Server) rpcSessionUser(ctx context.Context, op string) (store.SessionUser, *connect.Error) {
	su, ok := sessionUserFromContext(ctx)
	if !ok {
		return store.SessionUser{}, s.rpcInternal(op+": missing session context", errors.New("missing session context"))
	}
	return su, nil
}

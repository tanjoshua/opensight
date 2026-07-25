package api

import (
	"context"
	"net/http"

	"opensight/internal/gen/opensight/v1/opensightv1connect"

	connect "connectrpc.com/connect"
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
	// RPC-4/5/6 will each add one more mux.Handle(...) line here.
	return http.StripPrefix("/rpc", mux)
}

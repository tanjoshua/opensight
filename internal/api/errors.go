package api

import (
	"errors"
	"log/slog"

	connect "connectrpc.com/connect"
)

// rpcError is the single translation from an internal error to a
// *connect.Error. Never leaks error detail to the client: the real error goes
// to slog, a generic message goes to the client. RPC-4/5/6 extend this switch
// with their own store sentinels as each service reaches them — this story
// only implements what AuthService actually reaches (no/expired session, and
// everything else as internal).
func (s *Server) rpcError(op string, err error) *connect.Error {
	if errors.Is(err, errNoSession) {
		// Today's REST middleware doesn't log this either — an absent session
		// is an expected client state, not an operational error.
		cerr := connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
		cerr.Meta().Add("Set-Cookie", s.expiredSessionCookie().String())
		return cerr
	}

	slog.Error("api: "+op, "error", err)
	return connect.NewError(connect.CodeInternal, errors.New("an unexpected error occurred"))
}

// rpcInvalidArgument is the client-fault 400 equivalent.
func rpcInvalidArgument(msg string) *connect.Error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New(msg))
}

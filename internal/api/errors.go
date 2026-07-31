package api

import (
	"errors"
	"log/slog"

	"opensight/internal/billing"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
	"go.temporal.io/api/serviceerror"
)

// rpcError is the single translation from an internal error to a
// *connect.Error. Never leaks error detail to the client: the real error goes
// to slog, a generic message goes to the client. All 7 services are now
// mounted; this switch covers every store sentinel reachable from any of
// them.
func (s *Server) rpcError(op string, err error) *connect.Error {
	switch {
	case errors.Is(err, errNoSession):
		// Today's REST middleware doesn't log this either — an absent session
		// is an expected client state, not an operational error.
		cerr := connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
		cerr.Meta().Add("Set-Cookie", s.expiredSessionCookie().String())
		return cerr
	case errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("not found"))
	case errors.Is(err, store.ErrBusinessNotDraft):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("business is already active"))
	case errors.Is(err, store.ErrPromptNotActive):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("prompt is not active"))
	case errors.Is(err, store.ErrPromptLimitExceeded):
		return connect.NewError(connect.CodeResourceExhausted, errors.New("prompt count exceeds the plan limit"))
	case errors.Is(err, store.ErrEmailTaken):
		// Deliberately specific: signup is already an enumeration oracle (design
		// 08), so a vague message here just costs the honest user a support
		// ticket. Unlike Login's rpcLoginFailed, this is not a uniform-failure
		// surface.
		return connect.NewError(connect.CodeAlreadyExists, errors.New("that email is already registered"))
	}

	var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &alreadyStarted) {
		return connect.NewError(connect.CodeAlreadyExists, errors.New("profile generation is already in progress"))
	}

	return s.rpcInternal(op, err)
}

// rpcInternal is the writeInternalError equivalent: always CodeInternal, never
// sentinel-mapped. Used where even a store sentinel should be treated as our
// bug, not the client's — e.g. a failed plan lookup during business creation.
func (s *Server) rpcInternal(op string, err error) *connect.Error {
	slog.Error("api: "+op, "error", err)
	return connect.NewError(connect.CodeInternal, errors.New("an unexpected error occurred"))
}

// rpcInvalidArgument is the client-fault 400 equivalent.
func rpcInvalidArgument(msg string) *connect.Error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New(msg))
}

// rpcFailedPrecondition is the client-fault "wrong state" equivalent of
// today's 409 conflict responses — distinct from a duplicate-start conflict
// (CodeAlreadyExists).
func rpcFailedPrecondition(msg string) *connect.Error {
	return connect.NewError(connect.CodeFailedPrecondition, errors.New(msg))
}

// rpcAccessDenied is the write gate's rejection (BILL-6, design 08
// "Enforcement gate 1"): a is the access the caller actually has, carried as
// an error detail so the SPA can render the right billing state rather than
// parse the message string.
func rpcAccessDenied(a billing.Access) *connect.Error {
	msg := "access denied"
	switch a {
	case billing.AccessNever:
		msg = "this account has no active subscription"
	case billing.AccessLapsed:
		msg = "your subscription has lapsed; changes are paused"
	}
	cerr := connect.NewError(connect.CodeFailedPrecondition, errors.New(msg))
	// NewErrorDetail can only fail to marshal msg into an Any, and
	// AccessDenied is a single well-known enum field — this cannot fail.
	if detail, err := connect.NewErrorDetail(&opensightv1.AccessDenied{Access: accessToProto(a)}); err == nil {
		cerr.AddDetail(detail)
	}
	return cerr
}

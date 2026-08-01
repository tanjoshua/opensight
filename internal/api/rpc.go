package api

import (
	"context"
	"errors"
	"net/http"

	"opensight/internal/billing"
	"opensight/internal/domain"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
	"github.com/google/uuid"
)

// maxRPCRequestBytes matches today's REST body cap (64 KiB, applied via
// http.MaxBytesReader in ~9 handlers).
const maxRPCRequestBytes = 64 << 10

// accessClass is a procedure's required billing access (design 08 "Write
// gate"). Order is significant only in that classPublic must be the zero
// value's opposite — see procedureAccess's total-map property below, not in
// any numeric comparison between classes.
type accessClass int

const (
	// classPublic needs no session at all (Login, Signup).
	classPublic accessClass = iota
	// classBilling is reachable at any access, including "never paid" — the
	// billing/account-lifecycle surface itself.
	classBilling
	// classRead needs at least AccessLapsed: history stays readable after a
	// subscription lapses.
	classRead
	// classWrite needs AccessFull.
	classWrite
)

// satisfiedBy encodes design 08's access table: billing at any access, read
// at full or lapsed, write at full only. A small method rather than inlining
// this at the interceptor call site, so the table has exactly one home.
func (c accessClass) satisfiedBy(a billing.Access) bool {
	switch c {
	case classBilling:
		return true
	case classRead:
		return a == billing.AccessFull || a == billing.AccessLapsed
	case classWrite:
		return a == billing.AccessFull
	default:
		return false
	}
}

// procedureAccess is the default-deny classification registry: every
// procedure in the schema must be a key here, keyed by generated procedure
// constants so a renamed/removed RPC breaks the build instead of silently
// changing access. Completeness (every procedure classified, not just every
// classified procedure valid) is enforced at test time by
// TestEveryProcedureIsClassified (rpc_test.go) and at runtime by
// accessInterceptor's default-deny fallthrough below. This replaces the old
// publicProcedures allowlist with one total map spanning all four classes.
var procedureAccess = map[string]accessClass{
	// classPublic — no session.
	opensightv1connect.AuthServiceLoginProcedure:  classPublic,
	opensightv1connect.AuthServiceSignupProcedure: classPublic,

	// classBilling — reachable at any access.
	//
	// GetMe is billing, not read: it's how a never-paid or lapsed SPA learns
	// its own state. Gating it on read access would make the billing page
	// unrenderable for exactly the users who need it.
	opensightv1connect.AuthServiceGetMeProcedure: classBilling,
	// Logout is billing: a lapsed customer must always be able to log out.
	opensightv1connect.AuthServiceLogoutProcedure:             classBilling,
	opensightv1connect.BillingServiceStartCheckoutProcedure:   classBilling,
	opensightv1connect.BillingServiceConfirmCheckoutProcedure: classBilling,
	// CreatePortalSession is billing, not read/write: a lapsed customer must
	// still reach invoices and reactivate, and a never-paid tenant is
	// refused by the handler's no-Customer check rather than by the access
	// gate (design 08 "Customer Portal").
	opensightv1connect.BillingServiceCreatePortalSessionProcedure: classBilling,

	// classRead — needs full or lapsed.
	opensightv1connect.BusinessServiceGetBusinessProcedure:         classRead,
	opensightv1connect.BusinessServiceGetProposalProcedure:         classRead,
	opensightv1connect.PromptServiceListPromptsProcedure:           classRead,
	opensightv1connect.PromptServiceGetPromptProcedure:             classRead,
	opensightv1connect.CompetitorServiceListCompetitorsProcedure:   classRead,
	opensightv1connect.OverviewServiceGetOverviewProcedure:         classRead,
	opensightv1connect.CitationServiceListCitationSourcesProcedure: classRead,
	opensightv1connect.ResultServiceListRunsProcedure:              classRead,
	opensightv1connect.ResultServiceListResultsProcedure:           classRead,
	opensightv1connect.ResultServiceGetResultProcedure:             classRead,

	// classWrite — needs full.
	//
	// CreateBusiness/RegenerateProposal are writes because they start
	// GenerateProfileWorkflow (LLM spend), not merely because they mutate a
	// row. ApplyProposal is a write: activates the business, inserts
	// prompts, creates the Temporal Schedule.
	opensightv1connect.BusinessServiceCreateBusinessProcedure:            classWrite,
	opensightv1connect.BusinessServiceUpdateBusinessProcedure:            classWrite,
	opensightv1connect.BusinessServiceRegenerateProposalProcedure:        classWrite,
	opensightv1connect.BusinessServiceApplyProposalProcedure:             classWrite,
	opensightv1connect.PromptServiceAddPromptProcedure:                   classWrite,
	opensightv1connect.PromptServiceReplacePromptProcedure:               classWrite,
	opensightv1connect.CompetitorServiceAddCompetitorProcedure:           classWrite,
	opensightv1connect.CompetitorServiceSetCompetitorStatusProcedure:     classWrite,
	opensightv1connect.CompetitorServiceReviewSuggestedAliasProcedure:    classWrite,
	opensightv1connect.CompetitorServiceUpdateCompetitorAliasesProcedure: classWrite,
}

// accessInterceptor is the write gate (BILL-6, design 08 "Enforcement gate
// 1"): every procedure carries one of four access classes, resolved once per
// request alongside the session, with no extra round trip.
//
//  1. An unclassified procedure is denied — default-deny, so adding an RPC
//     without classifying it fails closed rather than admitting it.
//  2. classPublic passes through with no session at all.
//  3. Every other class resolves the session first (unchanged 401/cookie-clear
//     behavior on failure).
//  4. Access is derived fresh from the session's billing state and the
//     current time — never cached on the session — which is what makes the
//     dunning bound take effect the moment it passes, with no scheduled job.
//  5. A class the derived access doesn't satisfy is rejected with the access
//     the caller actually has, so the SPA can render the right billing state.
func (s *Server) accessInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			class, ok := procedureAccess[req.Spec().Procedure]
			if !ok {
				return nil, s.rpcInternal("rpc: unclassified procedure", errors.New("no access class registered for "+req.Spec().Procedure))
			}
			if class == classPublic {
				return next(ctx, req)
			}

			su, err := s.sessionFromHeader(ctx, req.Header())
			if err != nil {
				return nil, s.rpcError("rpc: resolve session", err)
			}

			access := billing.DeriveAccess(su.Billing, nowUTC())
			if !class.satisfiedBy(access) {
				return nil, rpcAccessDenied(access)
			}

			return next(withAccess(withSessionUser(ctx, su), access), req)
		}
	}
}

// rpcHandler builds the /rpc sub-tree. Chi's Mount doesn't rewrite
// r.URL.Path, but the generated Connect handler routes on it, so
// http.StripPrefix is mandatory or every request 404s.
func (s *Server) rpcHandler() http.Handler {
	opts := []connect.HandlerOption{
		connect.WithInterceptors(s.accessInterceptor()),
		connect.WithRequireConnectProtocolHeader(),
		connect.WithReadMaxBytes(maxRPCRequestBytes),
	}
	mux := http.NewServeMux()
	mux.Handle(opensightv1connect.NewAuthServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewBillingServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewBusinessServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewOverviewServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewCitationServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewPromptServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewCompetitorServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewResultServiceHandler(s, opts...))
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

// rpcPaging normalizes a list RPC's limit/offset. Unlike REST's
// positiveIntParam/nonNegativeIntParam, nothing here errors: proto3 cannot
// distinguish an omitted int32 from an explicit 0, so non-positive limit
// means "not provided" and takes the default; an over-max limit clamps; a
// negative offset floors to 0 (load-bearing: prevents slice-arithmetic panics
// downstream).
func rpcPaging(limit, offset int32, defaultLimit, maxLimit int) (int, int) {
	l := int(limit)
	if l <= 0 {
		l = defaultLimit
	}
	if l > maxLimit {
		l = maxLimit
	}
	o := int(offset)
	if o < 0 {
		o = 0
	}
	return l, o
}

// rpcSessionUser pulls the session user the interceptor injected. A miss
// means broken wiring (the procedure would have to be classPublic otherwise,
// which never reaches a handler that calls this), not a normal auth failure
// — so it's Internal.
func (s *Server) rpcSessionUser(ctx context.Context, op string) (store.SessionUser, *connect.Error) {
	su, ok := sessionUserFromContext(ctx)
	if !ok {
		return store.SessionUser{}, s.rpcInternal(op+": missing session context", errors.New("missing session context"))
	}
	return su, nil
}

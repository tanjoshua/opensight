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

// maxRPCRequestBytes caps a decoded RPC request body at 64 KiB.
const maxRPCRequestBytes = 64 << 10

// accessClass is a procedure's required billing access (design 08 "Access
// gate"). Classes are named after what they require, not what the procedure
// does — classSubscriber admits both reads and edits, because both are free
// once a account has paid at least once; only classActive costs money. There
// is no "public, no session" class: sign-in itself is a plain HTTP redirect
// route (google_auth.go), not an RPC, so every procedure in the schema
// requires a resolved session.
type accessClass int

const (
	// classAccount is reachable at any access, including "never paid" — the
	// billing/account-lifecycle surface itself.
	classAccount accessClass = iota
	// classSubscriber needs at least AccessLapsed: has paid at some point.
	// Every read plus every edit that costs nothing lives here — history
	// stays readable and curatable after a subscription lapses.
	classSubscriber
	// classActive needs AccessFull: a live subscription. Reserved for the
	// procedures that reach an LLM or start a River Schedule — the gate
	// protects spend, not data.
	classActive
)

// satisfiedBy encodes design 08's access table: account at any access,
// subscriber at full or lapsed, active at full only. A small method rather
// than inlining this at the interceptor call site, so the table has exactly
// one home.
func (c accessClass) satisfiedBy(a billing.Access) bool {
	switch c {
	case classAccount:
		return true
	case classSubscriber:
		return a == billing.AccessFull || a == billing.AccessLapsed
	case classActive:
		return a.Active()
	default:
		return false
	}
}

type procedureScope int

const (
	scopeIdentity procedureScope = iota
	scopeAccount
)

type procedurePolicy struct {
	scope  procedureScope
	role   store.AccountRole
	access accessClass
}

func policy(scope procedureScope, role store.AccountRole, access accessClass) procedurePolicy {
	return procedurePolicy{scope: scope, role: role, access: access}
}

func roleAtLeast(have, need store.AccountRole) bool {
	rank := map[store.AccountRole]int{store.AccountRoleViewer: 1, store.AccountRoleMember: 2, store.AccountRoleAdmin: 3, store.AccountRoleOwner: 4}
	return rank[have] >= rank[need]
}

// procedureAccess is the default-deny classification registry: every
// procedure in the schema must be a key here, keyed by generated procedure
// constants so a renamed/removed RPC breaks the build instead of silently
// changing access. Completeness (every procedure classified, not just every
// classified procedure valid) is enforced at test time by
// TestEveryProcedureIsClassified (rpc_test.go) and at runtime by
// accessInterceptor's default-deny fallthrough below.
var procedureAccess = map[string]procedurePolicy{
	// classAccount — reachable at any access.
	//
	// GetMe is account, not subscriber: it's how a never-paid or lapsed SPA
	// learns its own state. Gating it on subscriber access would make the
	// billing page unrenderable for exactly the users who need it.
	opensightv1connect.AuthServiceGetMeProcedure: policy(scopeIdentity, "", classAccount),
	// Logout is account: a lapsed customer must always be able to log out.
	opensightv1connect.AuthServiceLogoutProcedure:               policy(scopeIdentity, "", classAccount),
	opensightv1connect.AccountServiceCreateAccountProcedure:     policy(scopeIdentity, "", classAccount),
	opensightv1connect.BillingServiceConfirmCheckoutProcedure:   policy(scopeIdentity, "", classAccount),
	opensightv1connect.AccountServiceGetAccountContextProcedure: policy(scopeAccount, store.AccountRoleViewer, classAccount),
	opensightv1connect.AccountServiceListMembersProcedure:       policy(scopeAccount, store.AccountRoleViewer, classAccount),
	opensightv1connect.AccountServiceAddMemberProcedure:         policy(scopeAccount, store.AccountRoleAdmin, classAccount),
	opensightv1connect.AccountServiceUpdateMemberRoleProcedure:  policy(scopeAccount, store.AccountRoleAdmin, classAccount),
	opensightv1connect.AccountServiceRemoveMemberProcedure:      policy(scopeAccount, store.AccountRoleAdmin, classAccount),
	opensightv1connect.BillingServiceGetBillingProcedure:        policy(scopeAccount, store.AccountRoleOwner, classAccount),
	opensightv1connect.BillingServiceStartCheckoutProcedure:     policy(scopeAccount, store.AccountRoleOwner, classAccount),
	// CreatePortalSession is account, not subscriber/active: a lapsed
	// customer must still reach invoices and reactivate, and a never-paid
	// account is refused by the handler's no-Customer check rather than by
	// the access gate (design 08 "Customer Portal").
	opensightv1connect.BillingServiceCreatePortalSessionProcedure: policy(scopeAccount, store.AccountRoleOwner, classAccount),

	// classSubscriber — needs full or lapsed. Reads, plus every edit that
	// costs nothing: no run fires while lapsed, so these are row changes
	// with no downstream spend.
	opensightv1connect.BusinessServiceGetBusinessProcedure:               policy(scopeAccount, store.AccountRoleViewer, classSubscriber),
	opensightv1connect.BusinessServiceGetProposalProcedure:               policy(scopeAccount, store.AccountRoleViewer, classSubscriber),
	opensightv1connect.BusinessServiceUpdateBusinessProcedure:            policy(scopeAccount, store.AccountRoleMember, classSubscriber),
	opensightv1connect.PromptServiceListPromptsProcedure:                 policy(scopeAccount, store.AccountRoleViewer, classSubscriber),
	opensightv1connect.PromptServiceGetPromptProcedure:                   policy(scopeAccount, store.AccountRoleViewer, classSubscriber),
	opensightv1connect.PromptServiceAddPromptProcedure:                   policy(scopeAccount, store.AccountRoleMember, classSubscriber),
	opensightv1connect.PromptServiceReplacePromptProcedure:               policy(scopeAccount, store.AccountRoleMember, classSubscriber),
	opensightv1connect.CompetitorServiceListCompetitorsProcedure:         policy(scopeAccount, store.AccountRoleViewer, classSubscriber),
	opensightv1connect.CompetitorServiceAddCompetitorProcedure:           policy(scopeAccount, store.AccountRoleMember, classSubscriber),
	opensightv1connect.CompetitorServiceSetCompetitorStatusProcedure:     policy(scopeAccount, store.AccountRoleMember, classSubscriber),
	opensightv1connect.CompetitorServiceReviewSuggestedAliasProcedure:    policy(scopeAccount, store.AccountRoleMember, classSubscriber),
	opensightv1connect.CompetitorServiceUpdateCompetitorAliasesProcedure: policy(scopeAccount, store.AccountRoleMember, classSubscriber),
	opensightv1connect.CompetitorServiceClaimCompetitorAsSelfProcedure:   policy(scopeAccount, store.AccountRoleMember, classSubscriber),
	opensightv1connect.OverviewServiceGetOverviewProcedure:               policy(scopeAccount, store.AccountRoleViewer, classSubscriber),
	opensightv1connect.CitationServiceListCitationSourcesProcedure:       policy(scopeAccount, store.AccountRoleViewer, classSubscriber),
	opensightv1connect.ResultServiceListRunsProcedure:                    policy(scopeAccount, store.AccountRoleViewer, classSubscriber),
	opensightv1connect.ResultServiceListResultsProcedure:                 policy(scopeAccount, store.AccountRoleViewer, classSubscriber),
	opensightv1connect.ResultServiceGetResultProcedure:                   policy(scopeAccount, store.AccountRoleViewer, classSubscriber),
	opensightv1connect.ImproveServiceListActionsProcedure:                policy(scopeAccount, store.AccountRoleViewer, classSubscriber),
	opensightv1connect.ImproveServiceGetActionProcedure:                  policy(scopeAccount, store.AccountRoleViewer, classSubscriber),
	opensightv1connect.ImproveServiceGetChecklistProcedure:               policy(scopeAccount, store.AccountRoleViewer, classSubscriber),
	opensightv1connect.ImproveServiceSetActionStatusProcedure:            policy(scopeAccount, store.AccountRoleMember, classSubscriber),

	// classActive — needs full. Exactly the procedures that reach an LLM or
	// start a River Schedule.
	//
	// CreateBusiness/RegenerateProposal need full because they start
	// GenerateProfileWorker (LLM spend), not merely because they mutate a
	// row. ApplyProposal needs full: activates the business, inserts
	// prompts, creates the River Schedule. GenerateQuestions needs full for
	// the same reason as CreateBusiness/RegenerateProposal: it is an LLM call
	// (design 03), even though it does not enqueue a background job.
	opensightv1connect.BusinessServiceCreateBusinessProcedure:     policy(scopeAccount, store.AccountRoleAdmin, classActive),
	opensightv1connect.BusinessServiceRegenerateProposalProcedure: policy(scopeAccount, store.AccountRoleAdmin, classActive),
	opensightv1connect.BusinessServiceApplyProposalProcedure:      policy(scopeAccount, store.AccountRoleAdmin, classActive),
	opensightv1connect.BusinessServiceGenerateQuestionsProcedure:  policy(scopeAccount, store.AccountRoleAdmin, classActive),
}

// accessInterceptor is the RPC access gate (design 08 "Enforcement gate 1"):
// every procedure carries one of four access classes, resolved once per
// request alongside the session, with no extra round trip.
//
//  1. An unclassified procedure is denied — default-deny, so adding an RPC
//     without classifying it fails closed rather than admitting it.
//  2. Every procedure resolves the session first (401 and a cookie clear on
//     failure) — there is no public/no-session class; sign-in itself is the
//     HTTP redirect route in google_auth.go, not an RPC.
//  3. Access is derived fresh from the session's billing state and the
//     current time — never cached on the session — which is what makes the
//     dunning bound take effect the moment it passes, with no scheduled job.
//  4. A class the derived access doesn't satisfy is rejected with the access
//     the caller actually has, so the SPA can render the right billing state.
func (s *Server) accessInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			p, ok := procedureAccess[req.Spec().Procedure]
			if !ok {
				return nil, s.rpcInternal("rpc: unclassified procedure", errors.New("no access class registered for "+req.Spec().Procedure))
			}

			su, err := s.sessionFromHeader(ctx, req.Header())
			if err != nil {
				return nil, s.rpcError("rpc: resolve session", err)
			}

			if p.scope == scopeIdentity {
				return next(withAccess(withSessionUser(ctx, su), billing.AccessNever), req)
			}
			slug := req.Header().Get("X-OpenSight-Account-Slug")
			if slug == "" {
				return nil, connect.NewError(connect.CodeNotFound, errors.New("account not found"))
			}
			su, err = s.store.ResolveAccountSession(ctx, su, slug, isPlatformOwner(su.Email))
			if err != nil {
				return nil, s.rpcError("rpc: resolve account", err)
			}
			if !roleAtLeast(su.Role, p.role) {
				return nil, connect.NewError(connect.CodePermissionDenied, errors.New("account role does not permit this action"))
			}
			access := billing.DeriveAccess(su.Billing, nowUTC())
			if !p.access.satisfiedBy(access) {
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
	mux.Handle(opensightv1connect.NewAccountServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewBillingServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewBusinessServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewOverviewServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewCitationServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewPromptServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewCompetitorServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewResultServiceHandler(s, opts...))
	mux.Handle(opensightv1connect.NewImproveServiceHandler(s, opts...))
	return http.StripPrefix("/rpc", mux)
}

// rpcID parses a request's id field, rejecting a malformed UUID with
// InvalidArgument. Returns a concrete *connect.Error (never a typed-nil through the
// error interface) — every call site must check `if cerr != nil`.
func rpcID(name, raw string) (domain.ID, *connect.Error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, rpcInvalidArgument(name + " must be a UUID")
	}
	return id, nil
}

// rpcPaging normalizes a list RPC's limit/offset. Nothing here errors: proto3
// cannot distinguish an omitted int32 from an explicit 0, so non-positive limit
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
// means broken wiring — every procedure resolves a session, so a handler
// that calls this is always reached with one set — not a normal auth
// failure, so it's Internal.
func (s *Server) rpcSessionUser(ctx context.Context, op string) (store.SessionUser, *connect.Error) {
	su, ok := sessionUserFromContext(ctx)
	if !ok {
		return store.SessionUser{}, s.rpcInternal(op+": missing session context", errors.New("missing session context"))
	}
	return su, nil
}

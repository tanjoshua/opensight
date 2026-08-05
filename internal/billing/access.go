package billing

import "time"

// Access is the account's derived billing authorization (design 08 "Access").
// It is never stored — always recomputed from a State and the current time.
type Access int

const (
	// AccessNever means signed up, never paid: nothing to read.
	AccessNever Access = iota
	// AccessFull means the account has a live subscription, so reads, edits,
	// profile generation, and monitoring runs are available.
	AccessFull
	// AccessLapsed means the account was once full but is not now: reads and
	// cost-free edits stay available, while generation and runs are blocked.
	AccessLapsed
)

// Action is the one billing operation a account may take from the billing
// page. It is derived alongside Access so the API and SPA cannot disagree
// about whether an existing Stripe subscription should be managed or a new
// one should be created.
type Action int

const (
	ActionNone Action = iota
	ActionCheckout
	ActionPortal
)

// Active reports whether the account currently has a live subscription. Every
// spend-side safeguard asks this one question — never a per-feature variant.
func (a Access) Active() bool { return a == AccessFull }

// String renders Access for logging; it is not the wire format (see
// internal/api's accessToProto for that).
func (a Access) String() string {
	switch a {
	case AccessNever:
		return "never"
	case AccessFull:
		return "full"
	case AccessLapsed:
		return "lapsed"
	default:
		return "unknown"
	}
}

// State is the access-relevant slice of a subscriptions row. It is a
// primitives struct rather than store.Subscription because internal/store
// imports internal/billing (the plan catalog) — internal/billing cannot
// import store back without a cycle. store.Subscription.AccessState() is the
// one adapter that bridges the two.
type State struct {
	Comped               bool
	StripeSubscriptionID string
	StripeStatus         string // verbatim Stripe
	PastDueSince         time.Time
}

// DunningBound is how long past_due keeps full access (design 08: 21 days,
// deliberately clear of Stripe's 2-week retry window — a backstop against the
// dashboard-only end-of-dunning setting, not a policy).
const DunningBound = 21 * 24 * time.Hour

// DeriveAccess answers never | full | lapsed for a State at now, exactly per
// design 08's access table. Arm order matters: the empty-subscription-id
// "never" arm sits after the status arms and before the lapsed default,
// matching the doc's row order so a reordering shows up as a diff against it.
func DeriveAccess(st State, now time.Time) Access {
	if st.Comped {
		return AccessFull
	}

	switch st.StripeStatus {
	case "active", "trialing":
		return AccessFull
	case "past_due":
		// A zero PastDueSince is unreachable through normal operation —
		// reconcile writes the anchor on the same row-write that sets the
		// status — and can only come from a hand-edited row. Treat it as
		// full ("in dunning, start unknown, assume recent") rather than
		// lapsed, which would lock a paying customer out over our own
		// bookkeeping. This does not weaken the backstop: the scenario the
		// bound guards against (dashboard set to leave past due, so Stripe
		// never cancels) always has an anchor, because reconcile wrote one
		// when the status first flipped.
		if st.PastDueSince.IsZero() || now.Sub(st.PastDueSince) < DunningBound {
			return AccessFull
		}
		return AccessLapsed
	}

	if st.StripeSubscriptionID == "" {
		return AccessNever
	}

	// canceled, unpaid, incomplete, incomplete_expired, paused, and anything
	// else: was paid, history stays readable.
	return AccessLapsed
}

// DeriveAction prevents a account from creating a second Stripe subscription
// alongside one the Customer Portal could still recover. The dividing line is
// whether the subscription has ever been paid: past_due, unpaid and paused
// all follow a successful first charge, so the portal can revive them and
// owns them regardless of current access. Everything else — no subscription
// at all, canceled, or an initial payment that never landed (incomplete,
// incomplete_expired) — has no portal affordance, so Checkout owns it.
//
// incomplete belongs on the Checkout side deliberately: Stripe voids it
// automatically (~23h) and never charged for it, so routing a customer who
// is actively trying to pay into a portal that cannot recover it would
// strand them for a day for no benefit.
func DeriveAction(st State) Action {
	if st.Comped {
		return ActionNone
	}
	if st.StripeSubscriptionID == "" {
		return ActionCheckout
	}
	switch st.StripeStatus {
	case "canceled", "incomplete", "incomplete_expired":
		return ActionCheckout
	}
	// past_due, unpaid, paused, active, trialing: the portal owns it.
	return ActionPortal
}

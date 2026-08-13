// Package billing is the versioned plan catalog (design 08 "Entitlements move
// from a table to code"). A limit and the Stripe Price it is sold against must
// ship as one unit, so entitlements are Go values covered by tests rather than
// a seeded table row — dev/prod drift between a limit and its price becomes
// impossible instead of merely undesirable.
//
// This file is intentionally dependency-free: internal/store imports it to
// resolve prompt limits and schedule intervals, and a future story adds a
// Stripe adapter in an internal/billing/stripe subpackage so stripe-go never
// becomes a transitive dependency of internal/store.
package billing

import "fmt"

// Plan is a account's entitlements: how many prompts it may run, how often
// monitoring runs, which platforms it covers, and the Stripe Price it is sold
// against.
type Plan struct {
	// Code is the stable string shared with Stripe (Price metadata, subscription
	// metadata) and persisted as subscriptions.plan_code.
	Code string
	Name string
	// PromptLimit bounds the account's active prompts (design 02).
	PromptLimit int
	// RunInterval drives the River Schedule spec (design 04). A plain string,
	// not an enum: the domain of valid intervals lives in the catalog, not the
	// wire format.
	RunInterval string
	Platforms   []string
	// PriceEnvKey names the env var holding this plan's Stripe Price id.
	PriceEnvKey string
}

// Starter is the only plan sold at launch (design 08 "Commercial model").
var Starter = Plan{
	Code:        "starter",
	Name:        "OpenSight Starter",
	PromptLimit: 20,
	RunInterval: "weekly",
	Platforms:   []string{"chatgpt"},
	PriceEnvKey: "STRIPE_PRICE_STARTER_MONTHLY",
}

// ErrUnknownPlanCode is returned by PlanFor when code does not match any
// catalog entry. An unknown plan_code is an error, never a silent default
// (design 08) — the same rule as an unknown run_interval (04).
var ErrUnknownPlanCode = fmt.Errorf("unknown plan code")

// PlanFor resolves a persisted subscriptions.plan_code against the catalog.
// The returned Plan's Platforms is a copy, so a caller mutating it can never
// mutate the catalog.
func PlanFor(code string) (Plan, error) {
	switch code {
	case Starter.Code:
		return copyPlan(Starter), nil
	default:
		return Plan{}, fmt.Errorf("%w: %q", ErrUnknownPlanCode, code)
	}
}

// Plans returns every catalog plan, copies safe for a caller to hold onto.
// Config (07) iterates this to build the price-id map from each plan's
// PriceEnvKey, so a plan added to the catalog is required in config for free.
func Plans() []Plan {
	return []Plan{copyPlan(Starter)}
}

func copyPlan(p Plan) Plan {
	platforms := make([]string, len(p.Platforms))
	copy(platforms, p.Platforms)
	p.Platforms = platforms
	return p
}

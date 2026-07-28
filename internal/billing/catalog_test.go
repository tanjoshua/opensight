package billing

import (
	"errors"
	"testing"
)

func TestPlanForStarter(t *testing.T) {
	plan, err := PlanFor("starter")
	if err != nil {
		t.Fatalf("PlanFor(starter): %v", err)
	}
	if plan.Code != "starter" || plan.Name != "OpenSight Starter" || plan.PromptLimit != 20 ||
		plan.RunInterval != "weekly" || len(plan.Platforms) != 1 || plan.Platforms[0] != "chatgpt" ||
		plan.PriceEnvKey != "STRIPE_PRICE_STARTER_MONTHLY" {
		t.Fatalf("PlanFor(starter) = %+v, want the documented Starter values", plan)
	}

	// The returned slice must be a copy: mutating it must not mutate the catalog.
	plan.Platforms[0] = "mutated"
	again, err := PlanFor("starter")
	if err != nil {
		t.Fatalf("PlanFor(starter) second call: %v", err)
	}
	if again.Platforms[0] != "chatgpt" {
		t.Fatalf("catalog mutated via returned slice: Platforms = %v", again.Platforms)
	}
}

func TestPlansReturnsCatalog(t *testing.T) {
	plans := Plans()
	if len(plans) != 1 || plans[0].Code != "starter" {
		t.Fatalf("Plans() = %+v, want a single starter plan", plans)
	}

	// The returned slice's Plan.Platforms must be a copy, same guarantee as PlanFor.
	plans[0].Platforms[0] = "mutated"
	if Starter.Platforms[0] != "chatgpt" {
		t.Fatalf("catalog mutated via Plans(): Starter.Platforms = %v", Starter.Platforms)
	}
}

func TestPlanForUnknownCode(t *testing.T) {
	_, err := PlanFor("enterprise")
	if !errors.Is(err, ErrUnknownPlanCode) {
		t.Fatalf("PlanFor(enterprise) err = %v, want ErrUnknownPlanCode", err)
	}
}

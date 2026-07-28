package billing

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestStubProviderFunnel exercises the full local funnel a stub provider
// exists to support: create customer -> checkout -> get session ->
// subscription active -> portal. This is the implementation later stories'
// integration tests depend on (BILL-4/5/8), so it needs to be right.
func TestStubProviderFunnel(t *testing.T) {
	p := NewStubProvider()
	ctx := context.Background()

	cus, err := p.CreateCustomer(ctx, CreateCustomerParams{TenantID: "tenant-1", Email: "a@example.com"})
	if err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}
	if cus.ID == "" {
		t.Fatal("CreateCustomer returned empty id")
	}

	checkout, err := p.CreateCheckoutSession(ctx, CreateCheckoutSessionParams{
		CustomerID: cus.ID,
		PriceID:    "price_starter",
		TenantID:   "tenant-1",
		SuccessURL: "https://app.example.com/checkout/return?session_id={CHECKOUT_SESSION_ID}",
		CancelURL:  "https://app.example.com/billing",
	})
	if err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if checkout.Status != "complete" {
		t.Fatalf("checkout Status = %q, want complete", checkout.Status)
	}
	if !strings.Contains(checkout.URL, checkout.ID) {
		t.Fatalf("checkout URL %q does not substitute session id %q", checkout.URL, checkout.ID)
	}
	if checkout.ClientReferenceID != "tenant-1" {
		t.Fatalf("ClientReferenceID = %q, want tenant-1", checkout.ClientReferenceID)
	}

	got, err := p.GetCheckoutSession(ctx, checkout.ID)
	if err != nil {
		t.Fatalf("GetCheckoutSession: %v", err)
	}
	if got != checkout {
		t.Fatalf("GetCheckoutSession = %+v, want %+v", got, checkout)
	}

	sub, err := p.GetSubscriptionForCustomer(ctx, cus.ID)
	if err != nil {
		t.Fatalf("GetSubscriptionForCustomer: %v", err)
	}
	if sub.Status != "active" || sub.CurrentPeriodEnd.IsZero() {
		t.Fatalf("subscription = %+v, want active with a period end", sub)
	}

	portal, err := p.CreatePortalSession(ctx, CreatePortalSessionParams{CustomerID: cus.ID, ReturnURL: "https://app.example.com/billing"})
	if err != nil {
		t.Fatalf("CreatePortalSession: %v", err)
	}
	if portal.URL != "https://app.example.com/billing" {
		t.Fatalf("portal URL = %q, want the return URL unchanged", portal.URL)
	}

	if _, err := p.GetSubscriptionForCustomer(ctx, "cus_unknown"); !errors.Is(err, ErrNoSubscription) {
		t.Fatalf("unknown customer err = %v, want ErrNoSubscription", err)
	}
	if _, err := p.GetCheckoutSession(ctx, "cs_unknown"); !errors.Is(err, ErrCheckoutSessionNotFound) {
		t.Fatalf("unknown session err = %v, want ErrCheckoutSessionNotFound", err)
	}
}

// TestStubProviderLifecycleSetters drives past_due, cancel_at_period_end,
// and canceled through the test-only setters later stories (BILL-5/6/7/10)
// need to exercise reconcile and access without a real Stripe call.
func TestStubProviderLifecycleSetters(t *testing.T) {
	p := NewStubProvider()
	ctx := context.Background()

	cus, err := p.CreateCustomer(ctx, CreateCustomerParams{TenantID: "tenant-1", Email: "a@example.com"})
	if err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}
	if _, err := p.CreateCheckoutSession(ctx, CreateCheckoutSessionParams{
		CustomerID: cus.ID,
		SuccessURL: "https://app.example.com/checkout/return?session_id={CHECKOUT_SESSION_ID}",
	}); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}

	p.SetSubscriptionStatus(cus.ID, "past_due")
	if sub, err := p.GetSubscriptionForCustomer(ctx, cus.ID); err != nil || sub.Status != "past_due" {
		t.Fatalf("after SetSubscriptionStatus(past_due): sub=%+v err=%v", sub, err)
	}

	p.SetCancelAtPeriodEnd(cus.ID, true)
	if sub, err := p.GetSubscriptionForCustomer(ctx, cus.ID); err != nil || !sub.CancelAtPeriodEnd {
		t.Fatalf("after SetCancelAtPeriodEnd(true): sub=%+v err=%v", sub, err)
	}

	p.SetSubscriptionStatus(cus.ID, "canceled")
	if sub, err := p.GetSubscriptionForCustomer(ctx, cus.ID); err != nil || sub.Status != "canceled" {
		t.Fatalf("after SetSubscriptionStatus(canceled): sub=%+v err=%v", sub, err)
	}
}

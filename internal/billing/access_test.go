package billing

import (
	"testing"
	"time"
)

// TestDeriveAccess tables design 08's access table exactly, in the doc's row
// order, so a reordering of the arms shows up as a diff against the doc. No
// Stripe, no DB — this discharges BILL-6's first AC.
func TestDeriveAccess(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	withinBound := now.Add(-1 * time.Hour)
	atBound := now.Add(-DunningBound)
	pastBound := now.Add(-DunningBound - time.Hour)

	tests := []struct {
		name string
		st   State
		want Access
	}{
		{"comped is always full", State{Comped: true, StripeStatus: "canceled"}, AccessFull},
		{"active is full", State{StripeSubscriptionID: "sub_1", StripeStatus: "active"}, AccessFull},
		{"trialing is full", State{StripeSubscriptionID: "sub_1", StripeStatus: "trialing"}, AccessFull},
		{"past_due within the bound is full", State{StripeSubscriptionID: "sub_1", StripeStatus: "past_due", PastDueSince: withinBound}, AccessFull},
		{"past_due exactly at the bound is lapsed", State{StripeSubscriptionID: "sub_1", StripeStatus: "past_due", PastDueSince: atBound}, AccessLapsed},
		{"past_due beyond the bound is lapsed", State{StripeSubscriptionID: "sub_1", StripeStatus: "past_due", PastDueSince: pastBound}, AccessLapsed},
		{"empty subscription id is never", State{StripeSubscriptionID: "", StripeStatus: ""}, AccessNever},
		{"canceled is lapsed", State{StripeSubscriptionID: "sub_1", StripeStatus: "canceled"}, AccessLapsed},
		{"unpaid is lapsed", State{StripeSubscriptionID: "sub_1", StripeStatus: "unpaid"}, AccessLapsed},
		{"incomplete is lapsed", State{StripeSubscriptionID: "sub_1", StripeStatus: "incomplete"}, AccessLapsed},
		{"incomplete_expired is lapsed", State{StripeSubscriptionID: "sub_1", StripeStatus: "incomplete_expired"}, AccessLapsed},
		{"paused is lapsed", State{StripeSubscriptionID: "sub_1", StripeStatus: "paused"}, AccessLapsed},
		{"past_due with a zero anchor is full, not lapsed", State{StripeSubscriptionID: "sub_1", StripeStatus: "past_due"}, AccessFull},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeriveAccess(tt.st, now); got != tt.want {
				t.Fatalf("DeriveAccess(%+v) = %s, want %s", tt.st, got, tt.want)
			}
		})
	}
}

func TestDeriveAction(t *testing.T) {
	tests := []struct {
		name  string
		state State
		want  Action
	}{
		{"comped has no Stripe action", State{Comped: true}, ActionNone},
		{"never paid starts checkout", State{}, ActionCheckout},
		{"canceled starts a new checkout", State{StripeSubscriptionID: "sub_1", StripeStatus: "canceled"}, ActionCheckout},
		{"expired incomplete starts a new checkout", State{StripeSubscriptionID: "sub_1", StripeStatus: "incomplete_expired"}, ActionCheckout},
		{"incomplete starts a new checkout", State{StripeSubscriptionID: "sub_1", StripeStatus: "incomplete"}, ActionCheckout},
		{"active is managed", State{StripeSubscriptionID: "sub_1", StripeStatus: "active"}, ActionPortal},
		{"past due is managed", State{StripeSubscriptionID: "sub_1", StripeStatus: "past_due"}, ActionPortal},
		{"unpaid is managed", State{StripeSubscriptionID: "sub_1", StripeStatus: "unpaid"}, ActionPortal},
		{"paused is managed", State{StripeSubscriptionID: "sub_1", StripeStatus: "paused"}, ActionPortal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveAction(tc.state); got != tc.want {
				t.Fatalf("DeriveAction() = %v, want %v", got, tc.want)
			}
		})
	}
}

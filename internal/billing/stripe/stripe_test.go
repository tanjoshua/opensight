package stripe

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	stripesdk "github.com/stripe/stripe-go/v86"

	"opensight/internal/billing"
)

// TestAPIVersionMatchesSDK pins the API version design 08 requires: an
// stripe-go upgrade that moves stripesdk.APIVersion (the value the SDK
// unconditionally sends as Stripe-Version, with no override) must fail this
// test rather than silently changing behavior between deploys.
func TestAPIVersionMatchesSDK(t *testing.T) {
	if APIVersion != stripesdk.APIVersion {
		t.Fatalf("APIVersion = %q, SDK APIVersion = %q; a Stripe API version bump must be a deliberate edit to this constant", APIVersion, stripesdk.APIVersion)
	}
}

func TestNewRejectsEmptyAPIKey(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New with empty API key: want error, got nil")
	}
}

// capturedRequest records the last request an httptest.Server received, with
// its form/query parameters merged into one url.Values so subtests can
// assert on request shape regardless of HTTP method.
type capturedRequest struct {
	method string
	path   string
	values url.Values
	header http.Header
}

func newTestProvider(t *testing.T, respond http.HandlerFunc) (*Provider, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse request body as form: %v", err)
		}
		for k, v := range r.URL.Query() {
			values[k] = v
		}

		captured.method = r.Method
		captured.path = r.URL.Path
		captured.values = values
		captured.header = r.Header

		respond(w, r)
	}))
	t.Cleanup(srv.Close)

	p, err := New(Config{APIKey: "sk_test_123", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, captured
}

func jsonResponse(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

func TestProviderCreateCustomer(t *testing.T) {
	p, captured := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, `{"id":"cus_1"}`)
	})

	cus, err := p.CreateCustomer(context.Background(), billing.CreateCustomerParams{TenantID: "tenant-1", Email: "a@example.com"})
	if err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}
	if cus.ID != "cus_1" {
		t.Fatalf("Customer.ID = %q, want cus_1", cus.ID)
	}
	if captured.method != http.MethodPost || captured.path != "/v1/customers" {
		t.Fatalf("request = %s %s, want POST /v1/customers", captured.method, captured.path)
	}
	if got := captured.values.Get("metadata[tenant_id]"); got != "tenant-1" {
		t.Fatalf("metadata[tenant_id] = %q, want tenant-1", got)
	}
	if got := captured.header.Get("Stripe-Version"); got != APIVersion {
		t.Fatalf("Stripe-Version header = %q, want %q", got, APIVersion)
	}
}

// TestProviderCreateCheckoutSession is the load-bearing test in this file
// (design 08, required by BILL-4): payment_method_types must never appear in
// the checkout session request body, so eligible methods stay entirely
// dashboard-configured. It also covers the other checkout fields design 08
// pins down: mode=subscription, client_reference_id, and
// subscription_data.metadata.tenant_id.
func TestProviderCreateCheckoutSession(t *testing.T) {
	p, captured := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, `{"id":"cs_1","url":"https://checkout.stripe.com/x","status":"open","client_reference_id":"tenant-1","customer":"cus_1","subscription":"sub_1"}`)
	})

	got, err := p.CreateCheckoutSession(context.Background(), billing.CreateCheckoutSessionParams{
		CustomerID:            "cus_1",
		PriceID:               "price_starter",
		TenantID:              "tenant-1",
		IntegrationIdentifier: "opensight-signup-abcdefgh",
		SuccessURL:            "https://app.example.com/checkout/return?session_id={CHECKOUT_SESSION_ID}",
		CancelURL:             "https://app.example.com/billing",
	})
	if err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	want := billing.CheckoutSession{
		ID: "cs_1", URL: "https://checkout.stripe.com/x", Status: "open",
		CustomerID: "cus_1", SubscriptionID: "sub_1", ClientReferenceID: "tenant-1",
	}
	if got != want {
		t.Fatalf("CheckoutSession = %+v, want %+v", got, want)
	}

	if captured.method != http.MethodPost || captured.path != "/v1/checkout/sessions" {
		t.Fatalf("request = %s %s, want POST /v1/checkout/sessions", captured.method, captured.path)
	}

	for key := range captured.values {
		if strings.Contains(key, "payment_method_types") {
			t.Fatalf("request body contains %q; payment_method_types must never be sent (design 08)", key)
		}
	}
	if got := captured.values.Get("mode"); got != "subscription" {
		t.Fatalf("mode = %q, want subscription", got)
	}
	if got := captured.values.Get("client_reference_id"); got != "tenant-1" {
		t.Fatalf("client_reference_id = %q, want tenant-1", got)
	}
	if got := captured.values.Get("subscription_data[metadata][tenant_id]"); got != "tenant-1" {
		t.Fatalf("subscription_data[metadata][tenant_id] = %q, want tenant-1", got)
	}
	if got := captured.values.Get("line_items[0][price]"); got != "price_starter" {
		t.Fatalf("line_items[0][price] = %q, want price_starter", got)
	}
	if got := captured.values.Get("integration_identifier"); got != "opensight-signup-abcdefgh" {
		t.Fatalf("integration_identifier = %q, want opensight-signup-abcdefgh", got)
	}
}

func TestProviderGetCheckoutSession(t *testing.T) {
	p, captured := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, `{"id":"cs_1","url":"https://checkout.stripe.com/x","status":"complete","client_reference_id":"tenant-1","customer":"cus_1","subscription":"sub_1"}`)
	})

	got, err := p.GetCheckoutSession(context.Background(), "cs_1")
	if err != nil {
		t.Fatalf("GetCheckoutSession: %v", err)
	}
	if got.Status != "complete" {
		t.Fatalf("Status = %q, want complete", got.Status)
	}
	if captured.method != http.MethodGet || captured.path != "/v1/checkout/sessions/cs_1" {
		t.Fatalf("request = %s %s, want GET /v1/checkout/sessions/cs_1", captured.method, captured.path)
	}
}

// TestProviderGetCheckoutSessionNotFound asserts the real adapter maps
// Stripe's resource_missing error to billing.ErrCheckoutSessionNotFound, the
// same sentinel the stub provider returns for an unknown id — BILL-4's
// ConfirmCheckout must get consistent error semantics regardless of which
// Provider is active.
func TestProviderGetCheckoutSessionNotFound(t *testing.T) {
	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		if _, err := w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"resource_missing","message":"No such checkout session: 'cs_unknown'"}}`)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	})

	_, err := p.GetCheckoutSession(context.Background(), "cs_unknown")
	if !errors.Is(err, billing.ErrCheckoutSessionNotFound) {
		t.Fatalf("err = %v, want ErrCheckoutSessionNotFound", err)
	}
}

func TestProviderGetSubscriptionForCustomer(t *testing.T) {
	p, captured := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, `{"object":"list","data":[{"id":"sub_1","status":"active","cancel_at_period_end":false,"customer":"cus_1","items":{"object":"list","data":[{"id":"si_1","current_period_end":1750000000}]}}],"has_more":false}`)
	})

	sub, err := p.GetSubscriptionForCustomer(context.Background(), "cus_1")
	if err != nil {
		t.Fatalf("GetSubscriptionForCustomer: %v", err)
	}
	if sub.ID != "sub_1" || sub.Status != "active" || sub.CustomerID != "cus_1" {
		t.Fatalf("Subscription = %+v", sub)
	}
	if want := time.Unix(1750000000, 0).UTC(); !sub.CurrentPeriodEnd.Equal(want) {
		t.Fatalf("CurrentPeriodEnd = %v, want %v (read off the item, not the subscription)", sub.CurrentPeriodEnd, want)
	}

	if captured.method != http.MethodGet || captured.path != "/v1/subscriptions" {
		t.Fatalf("request = %s %s, want GET /v1/subscriptions", captured.method, captured.path)
	}
	if got := captured.values.Get("status"); got != "all" {
		t.Fatalf("status = %q, want all (a canceled subscription must still be found)", got)
	}
	if got := captured.values.Get("customer"); got != "cus_1" {
		t.Fatalf("customer = %q, want cus_1", got)
	}
}

func TestProviderGetSubscriptionForCustomerNoSubscription(t *testing.T) {
	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, `{"object":"list","data":[],"has_more":false}`)
	})

	if _, err := p.GetSubscriptionForCustomer(context.Background(), "cus_1"); !errors.Is(err, billing.ErrNoSubscription) {
		t.Fatalf("err = %v, want ErrNoSubscription", err)
	}
}

func TestProviderCreatePortalSessionRequiresConfiguration(t *testing.T) {
	requests := 0
	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		jsonResponse(t, w, `{}`)
	})

	_, err := p.CreatePortalSession(context.Background(), billing.CreatePortalSessionParams{
		CustomerID: "cus_1",
		ReturnURL:  "https://app.example.com/billing",
	})
	if err == nil || !strings.Contains(err.Error(), "configuration ID is required") {
		t.Fatalf("CreatePortalSession error = %v, want missing-configuration error", err)
	}
	if requests != 0 {
		t.Fatalf("Stripe requests = %d, want 0", requests)
	}
}

// TestProviderCreatePortalSessionWithConfiguration covers BILL-8: a pinned
// ConfigurationID must reach Stripe as the configuration form param, so the
// session's behavior comes from the repo-owned configuration and never the
// account default.
func TestProviderCreatePortalSessionWithConfiguration(t *testing.T) {
	p, captured := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, `{"id":"bps_1","url":"https://billing.stripe.com/p/session/x","return_url":"https://app.example.com/billing"}`)
	})

	if _, err := p.CreatePortalSession(context.Background(), billing.CreatePortalSessionParams{
		CustomerID:      "cus_1",
		ReturnURL:       "https://app.example.com/billing",
		ConfigurationID: "bpc_1",
	}); err != nil {
		t.Fatalf("CreatePortalSession: %v", err)
	}
	if got := captured.values.Get("configuration"); got != "bpc_1" {
		t.Fatalf("configuration = %q, want bpc_1", got)
	}
	if got := captured.values.Get("customer"); got != "cus_1" {
		t.Fatalf("customer = %q, want cus_1", got)
	}
	if got := captured.values.Get("return_url"); got != "https://app.example.com/billing" {
		t.Fatalf("return_url = %q, want https://app.example.com/billing", got)
	}
}

func TestApplyPortalConfigurationUpdatesExactID(t *testing.T) {
	requests := 0
	p, captured := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		jsonResponse(t, w, `{"id":"bpc_exact"}`)
	})

	desired := billing.PortalConfig{
		PaymentMethodUpdate: true,
		InvoiceHistory:      true,
		SubscriptionCancel:  true,
		SubscriptionUpdate:  false,
		DefaultReturnURL:    "https://app.example.com/billing",
	}
	for range 2 {
		if err := p.ApplyPortalConfiguration(context.Background(), "bpc_exact", desired); err != nil {
			t.Fatalf("ApplyPortalConfiguration: %v", err)
		}
	}
	if requests != 2 {
		t.Fatalf("Stripe requests = %d, want 2 exact-id updates", requests)
	}
	if captured.method != http.MethodPost || captured.path != "/v1/billing_portal/configurations/bpc_exact" {
		t.Fatalf("last request = %s %s, want POST /v1/billing_portal/configurations/bpc_exact", captured.method, captured.path)
	}
	if got := captured.values.Get("active"); got != "true" {
		t.Fatalf("active = %q, want true", got)
	}
	if got := captured.values.Get("default_return_url"); got != desired.DefaultReturnURL {
		t.Fatalf("default_return_url = %q, want %q", got, desired.DefaultReturnURL)
	}
	if got := captured.values.Get("features[payment_method_update][enabled]"); got != "true" {
		t.Fatalf("payment_method_update enabled = %q, want true", got)
	}
	if got := captured.values.Get("features[invoice_history][enabled]"); got != "true" {
		t.Fatalf("invoice_history enabled = %q, want true", got)
	}
	if got := captured.values.Get("features[subscription_cancel][enabled]"); got != "true" {
		t.Fatalf("subscription_cancel enabled = %q, want true", got)
	}
	if got := captured.values.Get("features[subscription_cancel][mode]"); got != "at_period_end" {
		t.Fatalf("subscription_cancel mode = %q, want at_period_end", got)
	}
	if got := captured.values.Get("features[subscription_update][enabled]"); got != "false" {
		t.Fatalf("subscription_update enabled = %q, want false", got)
	}
}

func TestApplyPortalConfigurationUnknownID(t *testing.T) {
	p, captured := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		jsonResponse(t, w, `{"error":{"type":"invalid_request_error","code":"resource_missing","message":"No such configuration"}}`)
	})

	err := p.ApplyPortalConfiguration(context.Background(), "bpc_missing", billing.DesiredPortalConfig)
	if err == nil || !strings.Contains(err.Error(), "bpc_missing") {
		t.Fatalf("error = %v, want unknown configuration id", err)
	}
	if captured.method != http.MethodPost || captured.path != "/v1/billing_portal/configurations/bpc_missing" {
		t.Fatalf("request = %s %s, want exact missing-id update", captured.method, captured.path)
	}
}

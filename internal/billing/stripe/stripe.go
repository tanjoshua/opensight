// Package stripe is the Stripe client adapter (design 08 "Stripe integration
// — Client"). It is
// a separate package from internal/billing, not a file in it, so stripe-go
// never becomes a transitive dependency of internal/store (which imports
// internal/billing for the plan catalog).
package stripe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	stripesdk "github.com/stripe/stripe-go/v86"

	"opensight/internal/billing"
)

// APIVersion pins the Stripe API version this adapter is written against
// (design 08 "Client"). stripe-go always sends stripesdk.APIVersion as the
// Stripe-Version header, with no per-request or per-client override, so the
// only way to pin is to declare our own constant and assert in a test that
// it still matches the SDK's — an SDK upgrade that moves the API version
// then fails CI instead of silently changing behavior between deploys.
const APIVersion = "2026-06-24.dahlia"

// Config configures a Provider.
type Config struct {
	APIKey string
	// HTTPClient overrides the default HTTP client used by the Stripe
	// backend. Nil uses the SDK's default.
	HTTPClient *http.Client
	// BaseURL overrides the Stripe API base URL. Tests point it at an
	// httptest.Server; empty uses the real Stripe API.
	BaseURL string
}

// Provider is the concrete Stripe billing client. Its consumers declare the
// narrow interfaces they need.
type Provider struct {
	client *stripesdk.Client
}

// New constructs a Provider. An empty API key is a construction error: a
// misconfigured runtime must fail at startup, not at the first checkout.
func New(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("stripe: API key is required")
	}

	backendConfig := &stripesdk.BackendConfig{HTTPClient: cfg.HTTPClient}
	if cfg.BaseURL != "" {
		backendConfig.URL = stripesdk.String(cfg.BaseURL)
	}
	backends := &stripesdk.Backends{
		API: stripesdk.GetBackendWithConfig(stripesdk.APIBackend, backendConfig),
	}

	return &Provider{client: stripesdk.NewClient(cfg.APIKey, stripesdk.WithBackends(backends))}, nil
}

// CreateCustomer creates a Stripe Customer with metadata.tenant_id set
// (design 08 "Customer").
func (p *Provider) CreateCustomer(ctx context.Context, params billing.CreateCustomerParams) (billing.Customer, error) {
	cus, err := p.client.V1Customers.Create(ctx, &stripesdk.CustomerCreateParams{
		Email:    stripesdk.String(params.Email),
		Metadata: map[string]string{"tenant_id": params.TenantID},
	})
	if err != nil {
		return billing.Customer{}, fmt.Errorf("stripe: create customer: %w", err)
	}
	return billing.Customer{ID: cus.ID}, nil
}

// CreateCheckoutSession creates a `mode: subscription` Checkout Session
// (design 08 "Checkout Session"). It never sets PaymentMethodTypes:
// eligible methods come from dashboard configuration, not this code.
func (p *Provider) CreateCheckoutSession(ctx context.Context, params billing.CreateCheckoutSessionParams) (billing.CheckoutSession, error) {
	session, err := p.client.V1CheckoutSessions.Create(ctx, &stripesdk.CheckoutSessionCreateParams{
		Mode:                  stripesdk.String(string(stripesdk.CheckoutSessionModeSubscription)),
		Customer:              stripesdk.String(params.CustomerID),
		ClientReferenceID:     stripesdk.String(params.TenantID),
		IntegrationIdentifier: stripesdk.String(params.IntegrationIdentifier),
		SuccessURL:            stripesdk.String(params.SuccessURL),
		CancelURL:             stripesdk.String(params.CancelURL),
		LineItems: []*stripesdk.CheckoutSessionCreateLineItemParams{
			{
				Price:    stripesdk.String(params.PriceID),
				Quantity: stripesdk.Int64(1),
			},
		},
		SubscriptionData: &stripesdk.CheckoutSessionCreateSubscriptionDataParams{
			Metadata: map[string]string{"tenant_id": params.TenantID},
		},
	})
	if err != nil {
		return billing.CheckoutSession{}, fmt.Errorf("stripe: create checkout session: %w", err)
	}
	return checkoutSessionFromStripe(session), nil
}

// GetCheckoutSession retrieves a Checkout Session by id (design 08
// "Checkout return"). An unknown id maps to billing.ErrCheckoutSessionNotFound
// so callers (ConfirmCheckout) get the same error semantics
// regardless of which Provider is active — the stub returns the same
// sentinel for an unknown id.
func (p *Provider) GetCheckoutSession(ctx context.Context, sessionID string) (billing.CheckoutSession, error) {
	session, err := p.client.V1CheckoutSessions.Retrieve(ctx, sessionID, nil)
	if err != nil {
		var stripeErr *stripesdk.Error
		if errors.As(err, &stripeErr) && stripeErr.Code == stripesdk.ErrorCodeResourceMissing {
			return billing.CheckoutSession{}, fmt.Errorf("%w: %q", billing.ErrCheckoutSessionNotFound, sessionID)
		}
		return billing.CheckoutSession{}, fmt.Errorf("stripe: get checkout session: %w", err)
	}
	return checkoutSessionFromStripe(session), nil
}

// GetSubscriptionForCustomer lists a customer's subscriptions with
// status=all (design 08 "Reconcile") — required or a canceled subscription
// would look like no subscription at all — and returns the newest one.
func (p *Provider) GetSubscriptionForCustomer(ctx context.Context, customerID string) (billing.Subscription, error) {
	list := p.client.V1Subscriptions.List(ctx, &stripesdk.SubscriptionListParams{
		ListParams: stripesdk.ListParams{Limit: stripesdk.Int64(1)},
		Customer:   stripesdk.String(customerID),
		Status:     stripesdk.String("all"),
	})
	subs := list.Data()
	if err := list.Err(); err != nil {
		return billing.Subscription{}, fmt.Errorf("stripe: list subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return billing.Subscription{}, fmt.Errorf("%w: customer %q", billing.ErrNoSubscription, customerID)
	}
	return subscriptionFromStripe(subs[0]), nil
}

// GetPrice retrieves a Stripe Price for display (design 08 "RPC surface").
// Prices are immutable in Stripe — a changed amount is a new Price
// object — so this never needs to distinguish "changed" from "stale."
func (p *Provider) GetPrice(ctx context.Context, priceID string) (billing.Price, error) {
	price, err := p.client.V1Prices.Retrieve(ctx, priceID, nil)
	if err != nil {
		return billing.Price{}, fmt.Errorf("stripe: get price %q: %w", priceID, err)
	}
	return priceFromStripe(price), nil
}

func priceFromStripe(p *stripesdk.Price) billing.Price {
	price := billing.Price{
		UnitAmount: p.UnitAmount,
		Currency:   string(p.Currency),
	}
	// Recurring is nil for a one-time Price. The Starter Price is always
	// recurring, but tolerating nil here (rather than assuming) matches
	// subscriptionFromStripe's rule of never panicking on a sub-struct Stripe
	// didn't populate.
	if p.Recurring != nil {
		price.Interval = string(p.Recurring.Interval)
	}
	return price
}

// CreatePortalSession starts a Customer Portal session pinned to the
// explicitly provisioned configuration (design 08 "Customer Portal").
func (p *Provider) CreatePortalSession(ctx context.Context, params billing.CreatePortalSessionParams) (billing.PortalSession, error) {
	if params.ConfigurationID == "" {
		return billing.PortalSession{}, errors.New("stripe: portal configuration ID is required")
	}

	createParams := &stripesdk.BillingPortalSessionCreateParams{
		Customer:      stripesdk.String(params.CustomerID),
		ReturnURL:     stripesdk.String(params.ReturnURL),
		Configuration: stripesdk.String(params.ConfigurationID),
	}
	session, err := p.client.V1BillingPortalSessions.Create(ctx, createParams)
	if err != nil {
		return billing.PortalSession{}, fmt.Errorf("stripe: create portal session: %w", err)
	}
	return billing.PortalSession{URL: session.URL}, nil
}

// ApplyPortalConfiguration updates the explicitly provisioned Billing Portal
// Configuration to match cfg. It deliberately lives on *Provider, not
// the serving interfaces: it is an admin-only operation the serving path never
// calls, and adding it to the runtime seam would force a meaningless fake
// implementation.
func (p *Provider) ApplyPortalConfiguration(ctx context.Context, configurationID string, cfg billing.PortalConfig) error {
	if configurationID == "" {
		return errors.New("stripe: portal configuration ID is required")
	}
	params := &stripesdk.BillingPortalConfigurationUpdateParams{
		Active:           stripesdk.Bool(true),
		DefaultReturnURL: stripesdk.String(cfg.DefaultReturnURL),
		Features:         portalConfigurationUpdateFeatures(cfg),
	}
	_, err := p.client.V1BillingPortalConfigurations.Update(ctx, configurationID, params)
	if err != nil {
		return fmt.Errorf("stripe: update portal configuration %q: %w", configurationID, err)
	}
	return nil
}

// portalConfigurationUpdateFeatures builds the update-params Features from
// cfg — a distinct SDK type from the create-params one, same field shape.
func portalConfigurationUpdateFeatures(cfg billing.PortalConfig) *stripesdk.BillingPortalConfigurationUpdateFeaturesParams {
	return &stripesdk.BillingPortalConfigurationUpdateFeaturesParams{
		PaymentMethodUpdate: &stripesdk.BillingPortalConfigurationUpdateFeaturesPaymentMethodUpdateParams{
			Enabled: stripesdk.Bool(cfg.PaymentMethodUpdate),
		},
		InvoiceHistory: &stripesdk.BillingPortalConfigurationUpdateFeaturesInvoiceHistoryParams{
			Enabled: stripesdk.Bool(cfg.InvoiceHistory),
		},
		SubscriptionCancel: &stripesdk.BillingPortalConfigurationUpdateFeaturesSubscriptionCancelParams{
			Enabled: stripesdk.Bool(cfg.SubscriptionCancel),
			Mode:    stripesdk.String(string(stripesdk.BillingPortalConfigurationFeaturesSubscriptionCancelModeAtPeriodEnd)),
		},
		SubscriptionUpdate: &stripesdk.BillingPortalConfigurationUpdateFeaturesSubscriptionUpdateParams{
			Enabled: stripesdk.Bool(cfg.SubscriptionUpdate),
		},
	}
}

func checkoutSessionFromStripe(s *stripesdk.CheckoutSession) billing.CheckoutSession {
	cs := billing.CheckoutSession{
		ID:                s.ID,
		URL:               s.URL,
		Status:            string(s.Status),
		ClientReferenceID: s.ClientReferenceID,
	}
	if s.Customer != nil {
		cs.CustomerID = s.Customer.ID
	}
	if s.Subscription != nil {
		cs.SubscriptionID = s.Subscription.ID
	}
	return cs
}

func subscriptionFromStripe(s *stripesdk.Subscription) billing.Subscription {
	sub := billing.Subscription{
		ID:                s.ID,
		Status:            string(s.Status),
		CancelAtPeriodEnd: s.CancelAtPeriodEnd,
	}
	if s.Customer != nil {
		sub.CustomerID = s.Customer.ID
	}
	// The trap (design 08): Subscription has no top-level CurrentPeriodEnd
	// in this API version — it lives on the first item. Empty Items.Data
	// means "period end unknown," handled as the zero time, not a crash.
	if s.Items != nil && len(s.Items.Data) > 0 {
		sub.CurrentPeriodEnd = time.Unix(s.Items.Data[0].CurrentPeriodEnd, 0).UTC()
	}
	// Flexible billing mode — the default from API version 2025-09-30.clover
	// onward, which our pinned 2026-06-24.dahlia postdates — represents a
	// Customer Portal "cancel at period end" as CancelAt set to the
	// effective end instant, leaving CancelAtPeriodEnd false (verified
	// against a live sandbox subscriptions.update call; classic
	// mode sets both). A non-zero CancelAt is therefore itself a scheduled
	// cancellation, and it's the better end-date source when set: unlike the
	// item's CurrentPeriodEnd, it doesn't drift if a later period starts.
	if s.CancelAt != 0 {
		sub.CancelAtPeriodEnd = true
		sub.CurrentPeriodEnd = time.Unix(s.CancelAt, 0).UTC()
	}
	return sub
}

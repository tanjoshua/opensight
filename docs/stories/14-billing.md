# Epic 14 — Self-Serve Signup & Billing (BILL)

Self-serve signup with payment taken up front, Stripe subscription lifecycle, and entitlement enforcement. Phase 4 — the first epic that lets a customer arrive, pay, and onboard with no operator involved.

Acceptance criteria state what must be true when the story is done. The mechanism — types, call shapes, where code lives — is decided when the story is built, against the constraints and rationale in [design 08](../design/08-billing.md).

---

## BILL-1 — Billing schema and plan catalog

As the developer, I want entitlements in a versioned code catalog and Stripe state in one table, so that a plan's limits and the price it is sold against can never drift apart.

- [x] `internal/billing` catalog: `Starter` plan (`code`, `prompt_limit` 20, `run_interval` weekly, `platforms` chatgpt, price env key). Unknown `plan_code` is an error, never a default.
- [x] Migrations create `subscriptions` (tenant_id PK, plan_code, stripe_customer_id, stripe_subscription_id, stripe_status, past_due_since, comped, current_period_end, cancel_at_period_end); webhook deliveries are not retained locally.
- [x] Same migration backfills one `subscriptions` row per existing tenant with `comped = true`, then **drops `tenants.plan_id` and the `plans` table**.
- [x] tenant-keyed subscription read/upsert on `store.Store`; `GetTenantPlan` and every `plans` reference removed app-wide (prompt limit, profile generation, schedule interval all read the catalog).

Deps: — · Phase 4 · Ref: design 08 (Entitlements move from a table to code; Schema), 02 (Plans and tenancy)

## BILL-2 — Stripe adapter and test fake

As the developer, I want one concrete Stripe runtime client with narrow consumer-owned test seams and an in-memory fake, so that local development exercises the sandbox while the test suite remains deterministic.

- [x] `github.com/stripe/stripe-go/v86` adapter declaring its own `APIVersion` constant (`2026-06-24.dahlia`), asserted equal to the SDK's in a test. Production authenticates with a **restricted key** (`rk_`), never a secret key — a go-live checklist item (BILL-12), not a code-enforced prefix check, since sandbox keys are `sk_test_` and a hard check would break local dev.
- [x] The API and reconciler declare only the Stripe operations they consume; `internal/billing/stripe.Provider` satisfies those seams and the in-memory fake keeps tests deterministic.
- [x] The serving path always uses Stripe: sandbox locally and a restricted live key in production. The in-memory provider is injected only by tests, which hit no Stripe endpoint.
- [x] Config: `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_PRICE_STARTER_MONTHLY`, and `APP_BASE_URL` loaded in `internal/config`; `serve` fails fast on missing runtime values without making unrelated commands require them.

Deps: BILL-1 · Phase 4 · Ref: design 08 (Stripe integration — Client; Local development), 07 (Secrets and config)

## BILL-3 — Self-serve signup

As a prospective customer, I want to create an account from the marketing site, so that I can start without talking to anyone.

- [x] Email and password are the whole form; a successful signup lands authenticated, on the Starter plan, with no payment taken yet and no Stripe objects created.
- [x] Signing up twice with the same email is refused with a clear reason. Signup is knowingly an email-enumeration oracle; login's uniform-failure guarantee must remain intact (regression test).
- [x] A half-created account is impossible: either the whole tenant exists or none of it does.
- [x] The account survives with no business — onboarding names it later.

Deps: BILL-1 · Phase 4 · Ref: design 08 (Signup), 07 (Auth and accounts)

## BILL-4 — Checkout and paid-state confirmation

As a new customer, I want to pay for the Starter plan immediately after signing up, so that I can get to my first results the same day.

- [x] From the app, a customer can reach Stripe Checkout for Starter and pay; eligible payment methods are whatever the Stripe dashboard offers that customer, never a hardcoded list (asserted by `TestProviderCreateCheckoutSession`, `internal/billing/stripe/stripe_test.go` — unchanged by this story).
- [x] The tenant's Stripe Customer exists before the customer is sent to Stripe, is tagged with the tenant, and is permanent — every later checkout, invoice and portal session reuses it.
- [x] Returning from a successful checkout, the customer sees a paid account without waiting on webhook delivery.
- [x] A customer already paying cannot start a second checkout.
- [x] A checkout session belonging to another tenant can never be confirmed.

Deps: BILL-2, BILL-3 · Phase 4 · Ref: design 08 (Stripe integration — Checkout Session, Checkout return)

## BILL-5 — Subscription state converges on Stripe

As the operator, I want subscription state to converge on Stripe's truth regardless of delivery order or duplication, so that billing state is never wrong for long and never wrong permanently.

- [x] Stripe can deliver the subscription lifecycle to the app; a delivery that fails signature verification is rejected and changes nothing, including leaving no trace to replay.
- [x] Redelivery only repeats desired-state reconciliation; it cannot apply an additive effect.
- [x] Out-of-order delivery cannot resurrect a dead subscription — proven by an integration test that delivers `updated` after `deleted`.
- [x] The delivered payload's own state is never written; only Stripe's current state is (design 08 — the payload carries identity, not truth).
- [x] Every reconcile asserts whether monitoring must be running or paused, idempotently, tolerating a tenant with no business or no schedule yet.
- [x] Billing state changes through exactly one path, shared with the checkout return (BILL-4) — the two entry points cannot diverge.
- [x] Dunning is anchored once when it begins and cleared when it ends; repeated updates during the same dunning cycle must not push the anchor forward, or BILL-6's bound never expires (test).
- [x] Stripe retries only when reconcile genuinely failed; anything unrecognised is accepted and ignored.

Deps: BILL-4 · Phase 4 · Ref: design 08 (Stripe integration — Webhook, Reconcile; Enforcement gate 2), 04 (Schedules)

## BILL-6 — Access derivation and the RPC access gate

As a customer whose subscription lapsed, I want my history to stay readable while spend is blocked, so that I keep the evidence I paid for.

- [x] One derivation answers `never | full | lapsed` for any billing row at any moment, exactly per design 08's table, and is tested over every Stripe status and both sides of the dunning bound without needing Stripe or a database. Landed in BILL-4 (`internal/billing/access.go`, `internal/billing/access_test.go`) — pulled forward because the second-checkout guard is itself an access question.
- [x] The dunning bound takes effect the moment it passes, with no scheduled job: a `past_due` tenant beyond the bound is denied spend-triggering RPCs and runs even though its schedule is still running.
- [x] Access is available wherever a session is, without an extra round trip per request.
- [x] Every RPC is classified as account / subscriber / active and rejected below the access it requires; the rejection tells the SPA which billing state caused it.
- [x] Classification is default-deny and cannot rot: adding an RPC without classifying it fails the build or the test suite.
- [x] The client learns its access and its plan entitlements from one authoritative payload rather than a bare prompt limit.

Deps: BILL-1, BILL-3 · Phase 4 · Ref: design 08 (Access; Enforcement gate 1)

## BILL-7 — Spend backstop on runs

As the operator, I want an unentitled run to cost nothing even if every other gate failed, so that a missed webhook can never spend money.

- [x] A run that starts for a tenant without full access stops before spending anything: no run row, no prompt executed, no analysis. Integration test proves zero rows written and zero LLM calls.
- [x] The skip is legible as a skip, not as a failure, wherever runs are observed.
- [x] A run already in flight when access drops is allowed to finish — the period was paid for, and a killed run leaves a partial history.

Deps: BILL-5, BILL-6 · Phase 4 · Ref: design 08 (Enforcement gate 3), 04 (RunWorkflow)

## BILL-8 — Customer Portal

As a customer, I want to update my card, see invoices, and cancel myself, so that I am never blocked on support for my own billing.

- [x] A customer with a Stripe Customer can reach the Stripe-hosted portal and come back to the billing page; one without is refused rather than sent somewhere broken.
- [x] The portal offers card updates, invoice history and cancellation, and does not offer plan switching. The configuration is recorded, not tribal knowledge.
- [x] Cancelling in the portal keeps access until the period ends, then lapses it — verified end to end against the sandbox, not assumed.

Deps: BILL-5 · Phase 4 · Ref: design 08 (Customer Portal; Lapse and reactivation)

## BILL-8A — ID-addressed portal configuration deployment

As the operator, I want deployment and runtime to use one explicitly provisioned Stripe Portal Configuration, so that a deployment cannot create duplicates or update a different configuration from the one customers receive.

- [ ] Each Stripe environment has one Portal Configuration provisioned during environment bootstrap; its `bpc_...` id is stored as protected `STRIPE_PORTAL_CONFIGURATION_ID` configuration, with the sandbox id in local `.env` and the live id in the production deployment environment.
- [x] `opensight stripe portal-config` requires `STRIPE_PORTAL_CONFIGURATION_ID` and idempotently updates that exact configuration to `DesiredPortalConfig` with `active=true`; it never lists configurations, discovers them through metadata, or creates one during deployment, and an unknown id fails clearly.
- [ ] A protected pre-deploy job runs the release binary with the configuration id, `APP_BASE_URL`, and a command-only Stripe administration key. The serving environment never receives that administration key.
- [x] `opensight serve` requires the same configuration id at startup and every portal session pins to it, with tests covering exact-id update, missing/unknown-id failure, and propagation from server configuration to Stripe session creation.

Deps: BILL-8, FND-5 · Phase 4 · Ref: design 08 (Customer Portal; Config and secrets), 07 (Deployment)

## BILL-9 — Signup, checkout, and billing UI

As a new customer, I want signup → payment → onboarding to be one uninterrupted path, so that nothing about setup requires help.

- [x] A signup page consistent with login; a new account reaches payment without a detour.
- [x] Returning from a successful payment shows a brief settling state and then continues into onboarding; abandoning checkout returns to billing with no error framing — nothing went wrong.
- [x] A billing page states plan, price, status and the renewal or end date, and offers the server-derived action: manage an already-paid subscription in the portal, or start Checkout when none exists or the prior one never completed a payment.
- [x] An account that has never paid cannot wander into the app; it arrives at billing instead.

Deps: BILL-4, BILL-6, BILL-8 · Phase 4 · Ref: design 08 (The funnel; RPC surface), 06 (Frontend stack)

## BILL-10 — Lapsed access and reactivation

As a lapsed customer, I want to browse everything I collected with a clear path back, so that returning is one click and my history is visibly intact.

- [x] Every read view renders normally while lapsed; editing stays available (the gate protects spend, not data — no run fires while lapsed, so a prompt or competitor edit costs nothing); only new runs and profile generation stop.
- [x] A persistent, generic banner in the app shell says monitoring has stopped and offers reactivation — non-dated, since the exact lapse/renewal date already lives one click away on `/billing`.
- [x] The uncollected period renders as a gap in trends — never interpolated, never back-filled.
- [x] Reactivating touches billing only: same Customer, business, prompts and history untouched, monitoring resumes (end-to-end test).

Deps: BILL-9, BILL-7 · Phase 4 · Ref: design 08 (Lapse and reactivation), 04 (charts show observed values only)

## BILL-12 — Stripe go-live configuration and marketing wiring

As the operator, I want the live Stripe account and the marketing funnel configured and rehearsed, so that the first real payment is not the first test.

*Configuration, not code — the checklist is design 08's; this story is doing it.*

- [ ] Live Product + SGD 50.00/month Price created; restricted key minted with minimum scopes; webhook endpoint registered for the four subscription events; all ids and secrets in the VPS `.env`.
- [ ] Dunning set by hand in the Dashboard for **live mode specifically** — Smart Retries on with a 2-week window, end-of-dunning action **cancel** (never *leave past due*). Neither setting has an API, so neither can be scripted or asserted; sandbox settings do not carry over.
- [ ] `automatic_tax` stays off, with the switch-on procedure recorded in design 08 rather than in someone's head.
- [ ] Marketing CTAs point at signup; pricing and FAQ copy match what is actually charged and what cancellation actually does.
- [ ] Sandbox rehearsal passes end to end: signup → checkout → onboarding → first run → portal cancel → monitoring paused with history readable → reactivate → monitoring resumed.

Deps: BILL-10 · Phase 4 · Ref: design 08 (Go-live checklist; Commercial model)

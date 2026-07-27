# Epic 14 — Self-Serve Signup & Billing (BILL)

Self-serve signup with payment taken up front, Stripe subscription lifecycle, and entitlement enforcement. Phase 4 — the first epic that lets a customer arrive, pay, and onboard with no operator involved.

---

## BILL-1 — Billing schema and plan catalog

As the developer, I want entitlements in a versioned code catalog and Stripe state in one table, so that a plan's limits and the price it is sold against can never drift apart.

- [ ] `internal/billing` catalog: `Starter` plan (`code`, `prompt_limit` 20, `run_interval` weekly, `platforms` chatgpt, price env key). Unknown `plan_code` is an error, never a default.
- [ ] Migration creates `subscriptions` (tenant_id PK, plan_code, stripe_customer_id, stripe_subscription_id, stripe_status, past_due_since, comped, current_period_end, cancel_at_period_end) and `stripe_events` (id PK, type, payload, received_at, processed_at).
- [ ] Same migration backfills one `subscriptions` row per existing tenant with `comped = true`, then **drops `tenants.plan_id` and the `plans` table**.
- [ ] `store.SubscriptionStore` with tenant-keyed read/upsert; `GetTenantPlan` and every `plans` reference removed app-wide (prompt limit, profile generation, schedule interval all read the catalog).

Deps: — · Phase 4 · Ref: design 08 (Entitlements move from a table to code; Schema), 02 (Plans and tenancy)

## BILL-2 — Stripe adapter, config, and stub provider

As the developer, I want one Stripe adapter behind an interface with a stub mode, so that local dev and the whole test suite run without network or spend.

- [ ] `github.com/stripe/stripe-go/v86` adapter pinning API version `2026-06-24.dahlia`; authenticates with a **restricted key** (`rk_`), never a secret key.
- [ ] `billing.Provider` interface: create customer, create checkout session, get checkout session, get subscription, create portal session.
- [ ] `BILLING_PROVIDER=stub` returns canned URLs and a locally driven subscription state — mirrors the `PROMPT_RUNNER_MODE` pattern; `make up` and integration tests hit no Stripe endpoint.
- [ ] Config: `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_PRICE_STARTER_MONTHLY`, `APP_BASE_URL`, `BILLING_PROVIDER` loaded in `internal/config` with `stripe` mode failing fast on missing values.

Deps: BILL-1 · Phase 4 · Ref: design 08 (Stripe integration — Client; Local development), 07 (Secrets and config)

## BILL-3 — Self-serve signup

As a prospective customer, I want to create an account from the marketing site, so that I can start without talking to anyone.

- [ ] `AuthService.Signup(email, password)` creates tenant + user (argon2id) + `subscriptions` row (`plan_code=starter`, no Stripe objects) in one transaction, then mints a session exactly as `Login` does.
- [ ] `tenants.name` defaults to the email local part; onboarding's business creation replaces it.
- [ ] Duplicate email returns a clear `AlreadyExists` — signup is knowingly an enumeration oracle; `Login`'s uniform-failure guarantee stays untouched (regression test).
- [ ] Added to `publicProcedures`; password minimum length only, no composition rules; per-IP rate limit at Caddy alongside `/rpc`.

Deps: BILL-1 · Phase 4 · Ref: design 08 (Signup), 07 (Auth and accounts)

## BILL-4 — Checkout session and return reconcile

As a new customer, I want to pay for the Starter plan immediately after signing up, so that I can get to my first results the same day.

- [ ] `BillingService.StartCheckout` lazily creates the tenant's Stripe Customer (`metadata.tenant_id`, id persisted before redirect), then a `mode: subscription` session with `client_reference_id`, `subscription_data.metadata.tenant_id`, `integration_identifier` (stable label + 8 random letters), and success/cancel URLs off `APP_BASE_URL`.
- [ ] **No `payment_method_types` parameter anywhere** — eligible methods come from dashboard configuration. Enforced by a test asserting the parameter is never set.
- [ ] A tenant that already has an active subscription cannot start a second checkout (`FailedPrecondition`).
- [ ] `BillingService.ConfirmCheckout(session_id)` retrieves the session, verifies it belongs to the calling tenant, and runs the shared reconcile — so a delayed webhook never shows a paying customer an unpaid screen.

Deps: BILL-2, BILL-3 · Phase 4 · Ref: design 08 (Stripe integration — Checkout Session, Checkout return)

## BILL-5 — Webhook endpoint and reconcile

As the operator, I want subscription state to converge on Stripe's truth regardless of delivery order or duplication, so that billing state is never wrong for long and never wrong permanently.

- [ ] `POST /webhooks/stripe` (chi route outside `/rpc`, no session): reads the **raw body** before any parsing, verifies the signature, rejects unverified events with 400 and persists nothing.
- [ ] Insert into `stripe_events` first; PK conflict ⇒ already delivered ⇒ 200 without reprocessing.
- [ ] Handler takes only identity from the event and **re-fetches the subscription from the API** before writing — out-of-order `updated`/`deleted` cannot resurrect a dead subscription (integration test delivers them reversed).
- [ ] Subscribed events: `checkout.session.completed`, `customer.subscription.{created,updated,deleted}`. Unhandled types 200; genuine reconcile failure 500 so Stripe retries.
- [ ] One `Reconcile(ctx, stripeCustomerID)` used by both the webhook and `ConfirmCheckout`; it writes the row and pauses/unpauses the business's monitoring Schedule when access changes. Pause/unpause is idempotent and tolerates a missing schedule.
- [ ] Reconcile sets `past_due_since` when `stripe_status` first becomes `past_due` and clears it on any other status — it must not be re-stamped by repeated `updated` events during the same dunning cycle, or the bound never expires (test).

Deps: BILL-4 · Phase 4 · Ref: design 08 (Stripe integration — Webhook, Reconcile; Enforcement gate 2), 04 (Schedules)

## BILL-6 — Access derivation and the write gate

As a customer whose subscription lapsed, I want my history to stay readable while changes are blocked, so that I keep the evidence I paid for.

- [ ] Pure `Access(subscription, now) → never | full | lapsed` per the design's table: `comped` ⇒ full; `active|trialing` ⇒ full; `past_due` ⇒ full only within 21 days of `past_due_since`, else lapsed; no subscription id ⇒ never; everything else ⇒ lapsed. Table-driven test covers every Stripe status and both sides of the dunning bound.
- [ ] The dunning bound needs **no scheduled job**: `now` is a parameter and gates 1 and 3 recompute per request and per run start. A `past_due` tenant past the bound is denied writes and runs even though its schedule is still unpaused.
- [ ] Access resolves in the same query as the session (`store.SessionUser` extended) — one round trip, available in every handler.
- [ ] Connect interceptor classifies procedures `billing` | `read` | `write`; writes below `full` return `FailedPrecondition` with a detail the SPA renders. Default-deny map keyed by generated procedure constants.
- [ ] `TestEveryRPCHasAccessClass` walks the compiled descriptors and fails when any RPC lacks a class — the map cannot rot as RPCs are added.
- [ ] `GetMeResponse` drops bare `prompt_limit` for an `access` enum + `Plan` message (code, prompt_limit, run_interval, platforms).

Deps: BILL-1, BILL-3 · Phase 4 · Ref: design 08 (Access; Enforcement gate 1)

## BILL-7 — RunWorkflow spend backstop

As the operator, I want an unentitled run to cost nothing even if every other gate failed, so that a missed webhook can never spend money.

- [ ] `RunWorkflow`'s first activity resolves the business's tenant access and returns immediately — creating **no** `monitoring_runs` row and executing no prompt — when access is not `full`.
- [ ] Logged distinctly (not an error) so a paused-schedule race is visible in the Temporal UI without looking like a failure.
- [ ] A run already in flight when access drops is allowed to finish (a paid period's cost, and a killed run leaves a partial history).
- [ ] Integration test: lapsed tenant + manually triggered workflow ⇒ zero rows written, zero LLM calls.

Deps: BILL-5, BILL-6 · Phase 4 · Ref: design 08 (Enforcement gate 3), 04 (RunWorkflow)

## BILL-8 — Customer Portal

As a customer, I want to update my card, see invoices, and cancel myself, so that I am never blocked on support for my own billing.

- [ ] `BillingService.CreatePortalSession` returns a portal URL with `return_url` to `/billing`; rejected for a tenant with no Stripe Customer.
- [ ] Portal configuration documented and applied: payment method update, invoice history, cancellation **on**; plan switching **off**.
- [ ] Cancellation via the portal sets `cancel_at_period_end`; access stays `full` until period end, then the `deleted` webhook flips it to lapsed (verified end to end in the sandbox).

Deps: BILL-5 · Phase 4 · Ref: design 08 (Customer Portal; Lapse and reactivation)

## BILL-9 — Signup, checkout, and billing UI

As a new customer, I want signup → payment → onboarding to be one uninterrupted path, so that nothing about setup requires help.

- [ ] `/signup` page (shadcn form, matching `/login`); success routes straight to checkout.
- [ ] `/billing` page: plan, price, status, renewal or end date, "Subscribe" (access `never`) or "Manage billing" (portal) or "Reactivate" (lapsed).
- [ ] `/checkout/return` calls `ConfirmCheckout`, shows a brief settling state, then redirects to `/onboarding`; the cancel URL returns to `/billing` without an error state.
- [ ] `AppLayout` routes on `access` from `GetMe`: `never` ⇒ forced to `/billing`; `full` ⇒ normal app.

Deps: BILL-4, BILL-6, BILL-8 · Phase 4 · Ref: design 08 (The funnel; RPC surface), 06 (Frontend stack)

## BILL-10 — Lapsed read-only mode

As a lapsed customer, I want to browse everything I collected with a clear path back, so that returning is one click and my history is visibly intact.

- [ ] `access = lapsed`: every read view renders normally; write affordances (prompt add/replace, competitor triage, profile edit, manual run) are disabled with a consistent reason, not hidden.
- [ ] Persistent banner states monitoring has stopped and offers reactivation; while `cancel_at_period_end` is set on an active subscription, it states the date instead.
- [ ] The collection gap renders as a **gap** in trend charts — never interpolated, never back-filled.
- [ ] Reactivation reuses the existing Customer; business, prompts and history are untouched and the schedule resumes (end-to-end test).

Deps: BILL-9, BILL-7 · Phase 4 · Ref: design 08 (Lapse and reactivation), 04 (charts show observed values only)

## BILL-11 — Operator comps and dev seed

As the operator, I want to grant access without Stripe objects, so that design partners and dev tenants work without fake subscriptions.

- [ ] `opensight tenant comp --tenant <id> [--off]` toggles `comped`.
- [ ] `opensight user set-password` — the documented password-reset path while no email provider exists.
- [ ] `opensight seed dev` creates a comped tenant, so the local onboarding path stays exercised with no billing setup.
- [ ] `opensight tenant create` no longer references a plan row.

Deps: BILL-1 · Phase 4 · Ref: design 08 (Operator comps; Signup — password reset deferred)

## BILL-12 — Stripe go-live configuration and marketing wiring

As the operator, I want the live Stripe account and the marketing funnel configured and rehearsed, so that the first real payment is not the first test.

- [ ] Live Product + SGD 50.00/month Price created; restricted key minted with minimum scopes; webhook endpoint registered for the four subscription events; all ids/secrets in the VPS `.env`.
- [ ] Dunning configured **by hand in the Dashboard** (Stripe exposes neither setting via API, so this step cannot be scripted or asserted in a test): Smart Retries on with a **2-week** window (`/revenue_recovery/retries`), end-of-dunning action **cancel the subscription** (`/settings/billing/automatic` → Manage failed payments). Never *leave past due*. Re-done separately for live mode — sandbox settings do not carry over.
- [ ] `automatic_tax` stays **off**, with the switch-on procedure (GST registration → Tax Registration → enable → pricing-page presentation) recorded in the design doc, not in someone's head.
- [ ] Marketing CTAs ("Check my AI visibility") point at `/signup`; pricing and FAQ copy match what is actually charged and what cancellation does.
- [ ] Sandbox rehearsal passes end to end: signup → checkout → onboarding → first run → portal cancel → schedule paused + history readable → reactivate → schedule resumed.

Deps: BILL-10, BILL-11 · Phase 4 · Ref: design 08 (Go-live checklist; Commercial model)

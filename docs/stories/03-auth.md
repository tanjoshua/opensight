# Epic 03 — Auth & Accounts (AUTH)

Google is the only sign-in method. Phase 1 — operator-provisioned accounts; self-serve provisioning on first Google sign-in arrives in [epic 14](14-billing.md).

---

## AUTH-1 — Google sign-in and server-side sessions

As a user, I want to sign in with my Google account and stay logged in, so that my accounts' data is private to their members and I never touch a password.

- [x] `GET /auth/google/start` and `GET /auth/google/callback` (plain HTTP, not RPC): authorization-code flow with PKCE and a CSRF state cookie.
- [x] `sessions` table (random token, user_id, expiry); logout deletes the row. Session cookie: `HttpOnly, Secure, SameSite=Lax`. No JWTs.
- [x] `AuthService.Logout`, `AuthService.GetMe` (Connect RPC, `/rpc`); no public procedure — sign-in itself sits outside `/rpc`.
- [x] Identity resolves by `users.google_sub`, falling back to `email` (linking the sub on match) — an operator-created row and a pre-Google row both keep working.

Deps: SCH-1 · Phase 1 · Ref: design 07 (Auth and accounts)

## AUTH-2 — Invite-only account CLI

As the operator, I want `opensight account create` and `opensight account member add --account …`, so that comped accounts and memberships can exist without signup UI.

- [x] CLI creates an account and grants a role to a global user with no Google identity yet; the first Google sign-in with that email links it without an invitation email.
- [x] No password or verification-email machinery exists anywhere in the system.
- [x] Documented one-liner for creating an account.

Deps: AUTH-1 · Phase 1 · Ref: design 07 (Auth — invite-only)

## AUTH-3 — Auth middleware, account context, CSRF

As the developer, I want every account API handler to receive verified membership context and reject cross-site writes, so that scoping and CSRF are structural.

- [x] A Connect interceptor resolves session → global user; account-scoped RPCs additionally resolve the URL/header slug → membership → account and reject unauthorized slugs as not-found.
- [x] Every RPC requires the `Connect-Protocol-Version` header (`connect.WithRequireConnectProtocolHeader()`),
      which a cross-origin form POST cannot set — the CSRF guarantee. `/auth/google/*` carries its own CSRF
      protection (state + PKCE cookie) since it isn't an RPC.
- [x] Handlers receive account context; repository calls require it (meshes with SCH-4).

## AUTH-4 — Multi-account memberships and roles

As a user, I want one identity to belong to multiple accounts with a role in each, so that I can collaborate across organizations without duplicate logins.

- [x] Roles are `owner`, `admin`, `member`, and `viewer`, stored on the membership rather than the user.
- [x] Membership grants access to every business in its account; business ACLs are deferred.
- [x] Accounts support multiple owners and reject removal or demotion of the final owner.
- [x] `/accounts` selects among memberships and `/a/:accountSlug/...` scopes every product route.
- [x] Errors map to `connect.Error` codes app-wide (`rpcError`).

Deps: AUTH-1, SCH-4 · Phase 1 · Ref: design 07 (Auth — CSRF), 06 (API conventions)

## AUTH-4 — Sign-in UI

As a user, I want a sign-in page and a signed-in app shell, so that I can reach my dashboard.

- [x] `/login` page: a single "Continue with Google" link to `/auth/google/start` — no form, no password field.
- [x] Signed-out users hitting app routes are redirected to `/login`; logout control in the shell.
- [x] `/signup` redirects to `/login` (the marketing site still links to `/signup`).

Deps: AUTH-3, WEB-1 · Phase 1 · Ref: design 07 (Auth), 06 (Frontend stack)

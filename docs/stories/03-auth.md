# Epic 03 — Auth & Accounts (AUTH)

Hand-rolled email+password sessions, invite-only. Phase 1.

---

## AUTH-1 — Password auth and server-side sessions

As a user, I want to log in with email and password and stay logged in, so that my tenant's data is private to me.

- [x] Passwords hashed with argon2id; `sessions` table (random token, user_id, expiry); logout deletes the row.
- [x] Session cookie: `HttpOnly, Secure, SameSite=Lax`. No JWTs.
- [x] `POST /api/v1/login`, `POST /api/v1/logout`, `GET /api/v1/me`.
- [x] Login failures are uniform (no user-exists oracle); basic per-IP backoff acceptable via Caddy limit (FND-5).

Deps: SCH-1 · Phase 1 · Ref: design 07 (Auth and accounts)

## AUTH-2 — Invite-only account CLI

As the operator, I want `opensight tenant create` and `opensight user create --tenant …`, so that accounts exist without any signup surface.

- [x] CLI creates a tenant (on the starter plan) and a user with a set/generated password.
- [x] No signup or password-reset endpoints exist.
- [x] Documented one-liner for creating an account.

Deps: AUTH-1 · Phase 1 · Ref: design 07 (Auth — invite-only)

## AUTH-3 — Auth middleware, tenant context, CSRF

As the developer, I want every API handler to receive resolved tenant context and reject cross-site writes, so that scoping and CSRF are structural.

- [x] Middleware resolves session → user → tenant; unauthenticated API requests get 401 problem+json.
- [x] State-changing endpoints require the `X-Requested-With` custom header; requests without it are rejected.
- [x] Handlers receive tenant context; repository calls require it (meshes with SCH-4).
- [x] Errors follow RFC 7807 problem+json app-wide.

Deps: AUTH-1, SCH-4 · Phase 1 · Ref: design 07 (Auth — CSRF), 06 (API conventions)

## AUTH-4 — Login UI

As a user, I want a login page and a signed-in app shell, so that I can reach my dashboard.

- [ ] `/login` page (shadcn form); errors surfaced; redirect to `/responses` on success while Overview is unbuilt (switch the landing route to `/overview` when INS-1 ships).
- [ ] Signed-out users hitting app routes are redirected to `/login`; logout control in the shell.
- [ ] API client sends `X-Requested-With` on all mutating calls.

Deps: AUTH-3, WEB-1 · Phase 1 · Ref: design 07 (Auth), 06 (Frontend stack)

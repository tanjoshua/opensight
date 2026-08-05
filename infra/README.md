# Infra — OpenSight on an OVHcloud VPS

Config-only IaC: the VPS is created by hand in the OVH panel, and Ansible
takes it from bare Ubuntu to running OpenSight. No OVH API credentials are
used, and nothing here is OVH-specific — the same playbooks work against any
provider's Ubuntu box.

Stack per VPS (design 01-D8, 07 "Deployment"): `caddy` (the only public
listener, auto-HTTPS), `app` (`serve`), `worker` (`work`), `postgres`,
`temporal`, one-shot `temporal-schema`/`temporal-namespace` bootstrap
containers, and `temporal-ui` (bound to `127.0.0.1:8233`, reached over an SSH
tunnel). CI already builds and pushes the image to GHCR on every push to
`main` (`.github/workflows/ci.yml`); the VPS only ever pulls — there is no
build toolchain on the box.

## Prerequisites (once, on your laptop)

```
pip install --user ansible
cd infra && ansible-galaxy collection install -r requirements.yml
```

Install `sops` and `age` (e.g. `brew install sops age`, or your distro's
packages). You do not need Ansible or SOPS on the VPS itself — everything
runs from your laptop over SSH.

## Provisioning a new VPS

1. Create the VPS in the OVHcloud panel: Ubuntu 24.04 LTS, at least **4 GB
   RAM**, Singapore region. Note its public IP.
2. Point DNS at it: in Cloudflare (the zone is already there for the
   marketing site), add an **A record for `dashboard`, proxied
   (orange-cloud)**, pointing at the VPS IP. Set SSL/TLS mode to
   **Full (Strict)** under SSL/TLS → Overview — Caddy issues a real Let's
   Encrypt cert on the origin and Cloudflare validates against it. The
   HTTP-01 challenge still reaches the origin through the proxy, so no DNS-01
   setup is needed.
3. Edit `infra/inventory/hosts.yml`: set `ansible_host` to the VPS IP.
4. Edit `infra/inventory/group_vars/opensight/vars.yml`: set
   `deploy_ssh_public_key` to your SSH public key (e.g. contents of
   `~/.ssh/id_ed25519.pub`); adjust `app_image_repo` if the GHCR repository
   ever moves.
5. Stripe bootstrap — **do this before the first deploy**: `opensight serve`
   refuses to start unless `STRIPE_WEBHOOK_SECRET` and
   `STRIPE_PORTAL_CONFIGURATION_ID` are already set, so both must exist
   before `make infra-deploy` runs. Neither command needs the VPS — run them
   from your laptop with `STRIPE_SECRET_KEY` set to Stripe's command-only
   administration key (design 08) and `APP_BASE_URL=https://dashboard.opensight.app`:
   - In the Stripe dashboard, manually create one live Billing Portal
     Configuration (design 08 — this one step has no API equivalent) and note
     its id.
   - `opensight stripe portal-config` — applies the repo-owned Portal
     settings onto that id. Put the id in `vars.yml`'s
     `stripe_portal_configuration_id`.
   - `opensight stripe webhook-config` — creates the webhook endpoint at
     `https://dashboard.opensight.app/webhooks/stripe` and prints
     `STRIPE_WEBHOOK_SECRET=whsec_...`. Stripe returns this signing secret
     only once, at creation — capture it now into `secrets.sops.yml`
     (`make infra-secrets`), there is no way to retrieve it again later.

   Both commands are idempotent and safe to re-run any time settings change
   (e.g. after editing `internal/billing.DesiredPortalConfig`); a re-run of
   `webhook-config` against the same URL updates the event list without
   touching the already-stored secret.
6. Set up the remaining secrets — see "Secrets" below.
7. Provision (first run only, connecting as whatever OVH set up — usually
   `ubuntu` or `root`; every run after this uses the `deploy` user Ansible
   creates):
   ```
   ANSIBLE_USER=ubuntu make infra-provision
   ```
   This is idempotent — safe to re-run any time (e.g. after moving to a new
   VPS, or to reapply hardening).
8. Deploy:
   ```
   make infra-deploy
   ```
   This renders `.env`/`compose.yml`/`Caddyfile`, pulls the `main`-tagged
   image, runs migrations, brings the stack up, and fails if
   `https://dashboard.opensight.app/healthz` doesn't return `ok`.

## Redeploy (every release)

CI deploys automatically: every push to `main` that passes tests builds the
image, pushes it to GHCR, then runs `deploy.yml` pinned to that commit's
`sha-<short>` tag (`.github/workflows/ci.yml`, `deploy` job). Expect a few
seconds of downtime while containers restart (design 07 accepts this — no
blue/green).

For a manual or emergency redeploy, run the same playbook from your laptop:

```
make infra-deploy
```

Safe to re-run — rendering, pulling, and `docker compose up -d` are all
idempotent.

## Rollback

CI tags every image pushed from `main` both `main` and `sha-<short>`
(`docker/metadata-action`). Roll back with no rebuild:

```
make infra-deploy TAG=sha-abc1234
```

## Temporal UI

Not public — bound to `127.0.0.1:8233` on the VPS. Reach it with:

```
make infra-tunnel
```

then open `http://localhost:8233` locally. `Ctrl-C` to close the tunnel.

## Secrets

Secrets live encrypted in the repo
(`infra/inventory/group_vars/opensight/secrets.sops.yml`), via
[SOPS](https://github.com/getsops/sops) + [age](https://github.com/FiloSottile/age).
`community.sops`'s inventory vars plugin decrypts the file transparently on
every playbook run (`infra/ansible.cfg` enables it) — there is no separate
decrypt step.

The file is already encrypted against a real age key (`.sops.yaml` holds the
public half). Edit it only via `make infra-secrets` (opens it decrypted in
`$EDITOR`, re-encrypts on save) — never hand-edit the encrypted file, and
never commit a decrypted copy. The **private** key
(`~/.config/sops/age/keys.txt`) never enters the repo — it is the single
point of failure for both "rebuild on a new VPS" and CI's ability to deploy,
and belongs in a password manager, not just on one laptop.

CI needs its own copies of two secrets to run `deploy.yml` on every push to
`main` (repo → Settings → Secrets and variables → Actions):

| Secret | Value |
|---|---|
| `SSH_PRIVATE_KEY` | Contents of the private half of `deploy_ssh_public_key` (e.g. `~/.ssh/opensight_vps1`) |
| `SOPS_AGE_KEY` | Contents of `~/.config/sops/age/keys.txt` (the `AGE-SECRET-KEY-...` line) |

Both are copies of credentials that already exist above — rotating either one
(a new VPS SSH key, or a new age key pair) means updating the matching GitHub
secret too.

Only genuine credentials live here — values that let someone act as you if
leaked. Resource identifiers that are inert without a credential (a Stripe
Price id, a Portal Configuration id) or public by design (an OAuth client id
is visible in the browser's redirect URL during login) live in plaintext
`vars.yml` instead, so they're reviewable in a normal diff with no editor
round-trip needed.

Secrets required, and where they come from:

| Key | Source |
|---|---|
| `postgres_password` | Generate one (e.g. `openssl rand -hex 32`) |
| `openai_api_key` | OpenAI dashboard — project-scoped key, monthly budget cap set (design 07) |
| `stripe_secret_key` | Stripe dashboard — restricted key (`rk_`), scoped per design 08 |
| `stripe_webhook_secret` | Output of `opensight stripe webhook-config` on first run (see "Stripe bootstrap" above) |
| `google_client_secret` | Google Cloud Console OAuth client, with `https://dashboard.opensight.app/auth/google/callback` as an authorized redirect URI |
| `ghcr_pat` | GitHub → Settings → Developer settings → PAT, `read:packages` scope only |

Identifiers, in `vars.yml`, not secret:

| Key | Source |
|---|---|
| `stripe_price_starter_monthly` | Stripe dashboard — the Starter monthly Price id |
| `stripe_portal_configuration_id` | Manually created in the Stripe dashboard (design 08 — no API for this step), then `opensight stripe portal-config` applies settings to it — see "Stripe bootstrap" above |
| `google_client_id` | Same Google Cloud Console OAuth client as `google_client_secret` above |

Losing the age private key with no other copy means every secret above must
be rotated and re-entered — SOPS ciphertext with no matching key is
unrecoverable by design.

## Rate limiting and WAF: Cloudflare, not Caddy

`dashboard.opensight.app` is proxied through Cloudflare, which is where
design 07's `/rpc/` and `/webhooks/stripe` rate limits are enforced, using
Cloudflare's free-tier rate-limiting rules — this also brings WAF and DDoS
absorption along with no custom Caddy build to maintain. Configure the rules
by hand in the Cloudflare dashboard (Security → WAF → Rate limiting rules)
once DNS is live; there's nothing to script here.

`infra/roles/base` restricts UFW's 80/443 rules to Cloudflare's published IP
ranges (`infra/inventory/group_vars/opensight/vars.yml`), so the origin can't
be reached directly, bypassing those limits. Cloudflare's ranges change
rarely but do change — recheck against
[cloudflare.com/ips](https://www.cloudflare.com/ips/) occasionally and update
`vars.yml`.

## What's deliberately not here

- **Backups.** Out of scope for this story — see OPS-1/OPS-2.
- **Sentry.** FND-6. Only its log-rotation half landed here (Docker's
  `json-file` driver, capped at 10 MB × 3 files per container, configured in
  `infra/roles/docker`).
- **Egress policy.** UFW here governs inbound only. `internal/workflows/fetch_site.go`
  fetches arbitrary customer-supplied URLs; that SSRF exposure is unchanged by
  this story.

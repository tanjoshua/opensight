# Production infrastructure

Ansible deploys OpenSight to one manually created Ubuntu 24.04 VPS. The stack has three services:

- `app`: `opensight serve`, including HTTP and River workers;
- `postgres`: the single `opensight` database, including River job state;
- `caddy`: the only public listener, with automatic HTTPS.

CI builds the application image in GHCR. The VPS only pulls images; it has no build toolchain and no separate queue service.

## Laptop prerequisites

```sh
pip install --user ansible
cd infra
ansible-galaxy collection install -r requirements.yml
```

Install SOPS and age locally. They are not required on the VPS.

## Provision a VPS

1. Create an Ubuntu 24.04 VPS with at least 4 GB RAM in Singapore.
2. Add a proxied Cloudflare `A` record for `dashboard.opensight.app` and select Full (Strict) TLS.
3. Set the VPS address in `inventory/hosts.yml`.
4. Set `deploy_ssh_public_key` and review public identifiers in `inventory/group_vars/opensight/vars.yml`.
5. Create the Stripe Billing Portal Configuration, apply it with `opensight stripe portal-config`, create the webhook with `opensight stripe webhook-config`, and store the returned signing secret immediately.
6. Fill the encrypted secret inventory through `make infra-secrets`.
7. Provision and deploy:

```sh
ANSIBLE_USER=ubuntu make infra-provision
make infra-deploy
```

Provisioning installs Docker, creates the `deploy` user, disables password/root SSH, configures unattended upgrades, restricts inbound traffic with UFW, and creates swap. Both playbooks are idempotent.

## Deploy and rollback

`make infra-deploy TAG=...` renders `.env`, Compose, and Caddy configuration; logs into GHCR; pulls images; starts PostgreSQL; stops the app; runs `opensight migrate`; starts the stack; and verifies `/healthz`.

Application migrations run before River's bundled migrations. The app is stopped during migration so an incompatible schema never overlaps an old binary. PostgreSQL remains online and the deployment accepts brief application downtime.

CI deploys the commit-specific `sha-<short>` image after `main` passes. Manual deploy uses `main` unless `TAG` is supplied. Roll back without rebuilding:

```sh
make infra-deploy TAG=sha-abc1234
```

## Secrets

Secrets live in `inventory/group_vars/opensight/secrets.sops.yml`, encrypted with SOPS and age. Edit only through:

```sh
make infra-secrets
```

The age private key belongs in a password manager and never in the repository. GitHub Actions requires `SSH_PRIVATE_KEY` for the deploy user and `SOPS_AGE_KEY` for inventory decryption.

Encrypted credentials:

| Key | Source |
|---|---|
| `postgres_password` | generated random password |
| `openai_api_key` | project-scoped OpenAI key with a budget cap |
| `stripe_secret_key` | restricted Stripe runtime key |
| `stripe_webhook_secret` | one-time output from webhook creation |
| `google_client_secret` | Google OAuth web client |
| `ghcr_pat` | GitHub PAT with `read:packages` |

Public or credential-less identifiers remain in `vars.yml`: Stripe Price and Portal Configuration ids, Google client id, domain, and image location.

## Cloudflare

Cloudflare owns WAF and rate limiting for `/rpc/` and `/webhooks/stripe`. The base role allows ports 80 and 443 only from Cloudflare's published IPv4/IPv6 ranges, preventing direct-origin bypass. Recheck those ranges periodically. SSH remains open on port 22 with key-only authentication.

## Queue operations

River has no separate dashboard. Use structured app logs and the query from [Design 04](../docs/design/04-monitoring.md):

```sql
SELECT id, kind, queue, state, attempt, max_attempts, scheduled_at, attempted_at, errors
FROM river_job
WHERE state NOT IN ('completed', 'cancelled', 'discarded')
ORDER BY scheduled_at, id;
```

The query and River client are safe across multiple identical app instances if horizontal scaling is added later.

## Backup and restore

Back up only the `opensight` database; it contains product data and River state. The nightly job runs `pg_dump --format=custom`, encrypts the result through restic, and sends it offsite. Do not create separate queue or visibility backups.

Restore into a fresh PostgreSQL volume, run the release binary's `opensight migrate`, start the three-service stack, verify `/healthz`, and confirm scheduled/retryable rows in `river_job`. Perform this drill on a scratch VPS before go-live and periodically afterward.

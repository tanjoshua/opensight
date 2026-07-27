# opensight-www

Marketing site for [opensight.app](https://opensight.app). Static HTML + Tailwind CSS v4 — no framework, no server.

## Pages

- `/` — homepage: AI visibility for local businesses.
- `/pricing/` — the single Starter plan and what happens after signup.
- `/faq/` — full FAQ.

The header/footer are duplicated in each page (no templating) — edit all three when changing them. New pages follow the same pattern; a page with the animated chat demo needs a `#chat-scenarios` JSON block (see `index.html`, "Why it matters" section).

## Develop

```sh
npm install
npm run dev       # watches CSS + serves on http://localhost:8081 with live reload
```

`npm run dev` runs the Tailwind watcher and a static server (`live-server`) together. If you only need the CSS rebuilt, `npm run watch` alone works with any static server. Port 8081 (not 8080) so this can run alongside `make up`, whose Go API dev server listens on `:8080`.

The marketing dev server is also started by `make up` from the repository root.

## Build

```sh
npm run build     # writes minified assets/style.css
```

The built `assets/style.css` is committed, so the site deploys as plain static files with no build step required on the host.

## Deploy

Any static host works. This site lives in the `marketing/` subdirectory of the [opensight](https://github.com/tanjoshua/opensight) monorepo. Cloudflare Pages (free tier):

1. Cloudflare Pages → Create project → connect the `opensight` repo.
2. Framework preset: **None**. Root directory: `marketing`. Build command: empty (or `npm run build`). Output directory: `/`.
3. Add `opensight.app` as the custom domain.

## Things to update as the product evolves

- **App URLs**: all CTAs point at `https://app.opensight.app/signup` and sign-in at `https://app.opensight.app` — adjust if the app lives elsewhere.
- **Contact email**: `hello@opensight.app` in the footer.
- **Testimonial**: intentionally omitted for v1; add a section between "Every number has a receipt" and the final CTA once a real quote exists.
- **Copy discipline**: the pages deliberately claim only what the shipped product does (ChatGPT only, 20 prompts, weekly runs, same-day first run). Update copy when capabilities change.

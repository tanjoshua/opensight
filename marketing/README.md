# opensight-www

Marketing site for [opensight.app](https://opensight.app). Astro + Tailwind CSS v4, built as a fully static site.

## Pages

- `/` — homepage: the pitch, with both products as its centrepiece and detail pushed to their pages.
- `/monitor/` — product page for measurement: 20 questions, weekly runs, competitors, sources, history.
- `/improve/` — product page for the action queue, the evidence comparison, and the 17-check checklist.
- `/pricing/` — the single Starter plan (both products) and what happens after signup.
- `/faq/` — full FAQ.

A page with the animated chat demo needs a `#chat-scenarios` JSON block (see `src/pages/index.astro`).

## Structure

- `src/pages/` contains the site routes.
- `src/layouts/BaseLayout.astro` owns shared metadata, fonts, scripts, and the page shell.
- `src/components/Header.astro` and `src/components/Footer.astro` are the shared navigation components.
- `src/styles/global.css` contains the Tailwind theme and global component styles.
- `src/scripts/site.js` contains the small framework-free interactions.
- `public/` contains static assets copied into the build unchanged.

The header derives its active state from the current route. Its Products dropdown is driven by `src/scripts/site.js`: the `#products-menu` wrapper, a `[data-menu-trigger]` button, and a `[data-menu-panel]` panel that starts with the `hidden` attribute. Mobile uses the separate `#nav-menu` list instead, so the dropdown only ever opens at `md` and up.

## Develop

```sh
npm install
npm run dev       # serves on http://localhost:8081 with hot reload
```

Port 8081 (not 8080) lets the site run alongside `make up`, whose Go API dev server listens on `:8080`.

The marketing dev server is also started by `make up` from the repository root.

## Build

```sh
npm run build     # writes the static production site to dist/
npm run preview   # previews the production build on port 8081
```

Astro pre-renders every route to static HTML. The generated `dist/` directory is not committed.

## Deploy

Any static host works. This site lives in the `marketing/` subdirectory of the [opensight](https://github.com/tanjoshua/opensight) monorepo. Cloudflare Pages (free tier):

1. Cloudflare Pages → Create project → connect the `opensight` repo.
2. Framework preset: **Astro**. Root directory: `marketing`. Build command: `npm run build`. Output directory: `dist`.
3. Add `opensight.app` as the custom domain.

## Things to update as the product evolves

- **App URLs**: all CTAs point at `https://dashboard.opensight.app/signup` and sign-in at `https://dashboard.opensight.app` — adjust if the app lives elsewhere.
- **Contact email**: `hello@opensight.app` in the footer.
- **Testimonial**: intentionally omitted for v1; add a section between "Every number has a receipt" and the final CTA once a real quote exists.
- **Copy discipline**: the pages claim only what the shipped product does (ChatGPT only, weekly runs, same-day first run, no score or grade, no causal claims), and deliberately state no fixed counts — no prompt quota, check total, or category count — so the copy survives the product growing. Keep it that way; numbers belong in the app, not the pitch.
- **Concision**: every section has to earn its screen. Prefer cutting to adding — the pitch is clearer short, and filler reads as padding rather than proof.

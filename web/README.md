# web/

Vite + React + TypeScript SPA (design 06). shadcn/ui preset `bLTjNXma`
(style rhea, stone base, Lucide icons, Roboto), TanStack Query for server
state, react-router for the section routes.

- `src/api/` — typed client (`client.ts`) + one module per section.
- `src/components/` — app shell (`app-layout`, `app-sidebar`) and shared UI;
  `components/ui/` is shadcn CLI-generated (update via `npx shadcn add`).
- `src/pages/` — one directory per section.
- `src/lib/` — utilities.

Dev: `make up` at the repo root starts the dev server on :5173 with `/api`
proxied to the Go server (override the target with `OPENSIGHT_API_URL`).

Build: `npm run build` writes `dist/`, which `assets.go` embeds into the Go
binary. Git tracks only a placeholder `dist/index.html` so a plain Go
build/test works without Node; the Dockerfile's Node stage builds the real
SPA into the production image.

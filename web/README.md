# Probara Web UI

The operator UI: a Next.js (App Router) application for monitors, alerts,
incidents, status pages, locations and workspace settings. The conventions it
follows (shared controls, formatting, tones, overlays) are in the repository's
root `CLAUDE.md` under "Web UI conventions"; the surface and token contract is
the comment at the top of `app/globals.css`.

## Development

```bash
npm ci            # install
npm run dev       # http://localhost:3000, proxies /api to the API service
npm run lint      # eslint
npx tsc --noEmit  # typecheck
npm test          # node:test unit tests for lib/
npm run build     # production build
```

CI runs lint, typecheck, tests and build. The usual way to run the whole
stack is `make start-all-local` from the repository root.

## Environment variables

- `NEXT_PUBLIC_API_URL`: base URL for the API (defaults to `/api`, served by
  the app's own proxy route).
- `NEXT_PUBLIC_STATUS_PAGE_URL`: public base URL for status page links; in
  local development the UI falls back to `http://localhost:8082`.
- `API_PROXY_TARGET`: where the proxy routes forward to (server side).

## Authentication

Operators sign in at `/login` with a username and password or through OIDC
single sign-on; the session lives in HttpOnly cookies and is refreshed by the
API client. On a fresh install the login page creates the first
administrator. `/connect` accepts a tenant API key instead, for API-key-only
access.

## Project structure

- `app/`: routes. `app/api/[...path]` proxies to the API service.
- `components/`: React components, grouped by area (`monitors/`, `alerts/`,
  `incidents/`, `status-pages/`, `settings/`, `layout/`, …). `components/ui/`
  holds the shared controls.
- `lib/`: everything without JSX. The API client (`api.ts`), wire types
  (`types.ts`), the error contract (`errors.ts`), formatting (`format.ts`),
  tones (`status-tones.ts`) and the monitor form builders (`monitor-form/`).
  Unit tests sit next to the code as `*.test.ts`.

## Docker

```bash
docker build -t probara-frontend .
docker run -p 3000:3000 \
  -e NEXT_PUBLIC_API_URL=/api \
  -e NEXT_PUBLIC_STATUS_PAGE_URL=http://localhost:8082 \
  probara-frontend
```

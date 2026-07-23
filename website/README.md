# Probara website

Standalone marketing and product-documentation site for Probara. This project is
deliberately isolated from the authenticated dashboard in `../web` and from all
Go services.

The Next.js build uses static export. No Probara API, PostgreSQL database, NATS
server, or Node.js runtime is required after the build completes.

## Develop

```bash
cd website
npm install
npm run dev
```

Open `http://localhost:3000`.

## Verify and build

```bash
npm run typecheck
npm run lint
npm run build
```

The deployable site is written to `out/`. Routes use trailing slashes so static
hosts can serve nested documentation pages without a rewrite fallback.

## Site configuration

Copy `.env.example` to `.env.local` when a deployment needs overrides.

| Variable | Required | Description |
| --- | --- | --- |
| `NEXT_PUBLIC_SITE_URL` | No | Canonical public origin used for absolute metadata. |
| `NEXT_PUBLIC_BASE_PATH` | No | Path prefix such as `/probara` for project-based hosting. Omit for a root-domain deployment. |
| `NEXT_PUBLIC_APP_URL` | No | URL of a separately hosted product dashboard. When set, the header displays an “Open dashboard” link. The static site never contacts the app. |

`NEXT_PUBLIC_*` values are compiled into the static output. Set them during the
build, not after the `out/` directory has been generated.

## Host the static output

Any static host can publish `website/out`.

- Project directory: `website`
- Install command: `npm ci`
- Build command: `npm run build`
- Publish directory: `out`

For a container deployment:

```bash
docker build -t probara-website ./website
docker run --rm -p 8080:8080 probara-website
```

The included Nginx configuration serves prerendered route directories, applies
long-lived caching only to fingerprinted Next.js assets, and returns a real 404
for unknown paths.

## Content model

Documentation navigation lives in `lib/docs/navigation.ts`. Each article exports
a structured `DocPage` from `lib/docs/pages/`; the dynamic documentation route
turns those records into fully static pages.

The product repository is the documentation source of truth. When functionality,
configuration defaults, Compose wiring, or Helm values change, update the
matching article and keep limitations explicit.

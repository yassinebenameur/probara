# Repository Guidelines

## Project Structure & Module Organization
`probara` is a Go monorepo with service packages at the repo root: `api/`, `scheduler/`, `worker/`, `alerter/`, `status-page/`, and `agent/`. Shared code lives in `shared/` and common entrypoints live under service-specific `cmd/` folders plus top-level utilities in `cmd/`. The web frontend is in `web/` (`app/`, `components/`, `lib/`). Deployment assets live in `helm/` and `infra/`; architecture notes are in `docs/architecture.md`; helper scripts are in `scripts/`.

## Build, Test, and Development Commands
Use the `Makefile` for normal development workflows:

- `make start-all-local` runs the project locally without building service Docker images: it starts PostgreSQL and NATS in Docker, bootstraps local DB access, validates the Go toolchain, runs migrations with `go run ./cmd/migrate`, starts Go services locally, and launches the UI.
- `make start-all` runs the project with Docker-built application services: it starts PostgreSQL and NATS in Docker, bootstraps local DB access, runs migrations, starts backend services via Docker Compose, and launches the UI.
- `make stop-all-local` stops locally running Go services, the UI, and Docker infrastructure.
- `make stop-all` stops the UI and Docker Compose services.
- `make restart-all-local` restarts the local stack by running `make stop-all-local` followed by `make start-all-local`.
- `make restart-all` restarts the Docker-backed stack by running `make stop-all` followed by `make start-all`.

Use the matching local or Docker-backed stop/restart command for the way the stack was started. Avoid documenting older or one-off startup commands here unless they become the preferred workflow again.

Verification commands:

- `make test` runs `go test -v -race -coverprofile=coverage.out` across the module.
- `make lint` checks formatting and `go vet`; `make fmt` and `make vet` run them separately.
- For frontend-only changes, run relevant commands from `web/`, usually `npm run lint` and `npm run build`.

## Coding Style & Naming Conventions
Format Go code with `gofmt`; do not hand-align or mix spacing styles. Keep packages lowercase, exported identifiers in `CamelCase`, and filenames descriptive (`service.go`, `handlers.go`, `registry_test.go`). Follow existing service boundaries: reusable logic belongs in `shared/`, not copied between services. In `web/`, use TypeScript, PascalCase component filenames, and keep route files under `app/**/page.tsx`.

## Architecture Notes
Keep changes aligned with the current service boundaries:

- `api/` owns CRUD, dashboard/admin flows, auth, imports, push handling, and writes the shared application data model.
- `scheduler/` is responsible for scheduling monitor execution and retention cleanup; do not move check execution or alert evaluation logic into it.
- `worker/` consumes check jobs, runs monitor checks (`http`, `ping`, `dns`, `grpc`, `sip`, `agent`, `synthetic_api`, `synthetic_browser`), writes results, and publishes live status updates.
- `alerter/` evaluates alert policies from database state, manages alert lifecycle/notification deduplication, and publishes alert events.
- `status-page/` is the public-facing Go service for rendered status pages and live updates; `web/` is the separate React/Next.js application for the main product UI.
- `agent/` is a separate nested Go module that reports agent-side metrics back to the backend; account for that when running Go commands.

Shared integration contracts matter more than internal implementation details:

- PostgreSQL is the shared source of truth; preserve tenant isolation and avoid duplicating schema-specific logic across services.
- Scheduler and worker communicate through NATS JetStream using `CHECK_JOBS` / `check.jobs` by default.
- Live status-page fan-out uses core NATS on `statuspage.updates` by default via `shared/statusupdates`.
- Alert events use the configured `ALERTS` stream and `alerts` subject by default.
- Operational endpoints `/healthz`, `/readyz`, and `/metrics` are part of the standard service shape; keep them intact when touching service startup/server wiring.

## Testing Guidelines
Place Go tests next to the code they cover using `*_test.go`. Use `*_integration_test.go` for database or queue-backed tests and keep unit tests fast by default. Add coverage for each touched package, especially validators, services, and handlers. The top-level `make test` covers the main Go module; when touching the nested `agent/` module, run its tests from `agent/` as well. For frontend changes, run relevant checks from `web/` and note manual verification steps when no automated UI test exists.

## Agent Instructions Maintenance
After completing a feature or bug fix, consider whether `AGENTS.md` should be updated. Only add or change guidance when the work introduces durable repo knowledge that future agents or contributors need, such as new commands, service boundaries, architecture contracts, testing expectations, required workflows, or non-obvious operational constraints. Do not add one-off implementation notes, temporary workarounds, obvious code details, or anything already documented clearly elsewhere unless linking or summarizing it here would prevent repeated mistakes. Keep updates concise, general, and aligned with best practices.

## Commit & Pull Request Guidelines
Git history follows Conventional Commits, for example `fix(worker): recover consumer loop` or `feat(status-page): add theme settings`. Use `type(scope): summary`; keep subjects imperative and under one line. PRs should describe behavior changes, call out schema or env var updates, link related issues, and include screenshots for `web/` or status-page UI changes. Before opening a PR, run the checks that match the touched areas; include `helm lint ./helm/monitoring-platform` only when Helm or Kubernetes deployment assets change.

## Security & Configuration Tips
Start from `.env.example` and avoid committing secrets. Review `docker-compose.yml` and Helm values together when changing ports, queue subjects, or credentials so local and Kubernetes environments stay aligned.
If local startup fails with PostgreSQL auth errors after old experiments, remove the repo-scoped Docker volumes with `docker compose down -v` so the database can reinitialize with the expected `probara` credentials.

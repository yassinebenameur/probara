# Contributing to Probara

Thanks for your interest in the project. This guide covers how to get a local
environment running, what the code expects of a change, and how to get it
merged.

By contributing you agree that your contributions are licensed under the
project's [LICENSE](LICENSE) (AGPL-3.0).

## Prerequisites

- **Go 1.23+** — CI builds against 1.23
- **Node.js LTS** — see `.nvmrc`
- **Docker + Docker Compose** — provides Postgres and NATS for local dev
- **Helm 3** — only if you touch `helm/monitoring-platform/`

## Getting a local environment

Infrastructure (Postgres, NATS, and an optional dex IdP for OIDC) runs in
Compose; the application services run as local processes:

```sh
make start-all-local # run api, scheduler, worker, and the web UI locally
```

The operator UI comes up on <http://localhost:3000>. Default credentials are
`admin` / `change-me`, and the default tenant ID is
`00000000-0000-0000-0000-000000000001`. `make help` lists the rest of the
targets. [RUNNING.md](RUNNING.md) has the longer walkthrough, including the
fully containerized variant.

Note that locally running binaries are stale after a code change — restart the
affected process, or verify through tests.

## Repository layout

| Path | What lives there |
|---|---|
| `api/` | REST API (chi), validation registry, monitor/import/auth services |
| `scheduler/` | Cron-style dispatch, JetStream provisioning, results ingest |
| `worker/` | Check execution for every monitor type; `dial_guard.go` SSRF policy |
| `alerter/` | Alert evaluation and notification delivery |
| `status-page/` | Public status pages and their template engine |
| `shared/` | Cross-service models, config, secrets, queue, AI provider |
| `web/` | Next.js operator UI |
| `website/` | Landing page and documentation site |
| `helm/monitoring-platform/` | Deployment chart |

## Building and testing

Most Go services share the root module. The downloadable agent under `agent/`
is a separate nested module, so verify both when a change crosses that boundary:

```sh
make lint
go test ./...
(cd agent && go test ./...)
```

The full `api/` suite takes over two minutes.

Use the repository's Node.js LTS selection and the committed lockfiles for both
frontends. From `web/`, then `website/`, run:

```sh
nvm use --lts
npm ci
npm run lint
npm exec -- tsc --noEmit
npm run build
```

Chart changes:

```sh
helm lint helm/monitoring-platform
helm template helm/monitoring-platform
```

Compose changes: `docker compose config`.

## What a change is expected to include

**Documentation follows code, in the same commit.** `website/lib/docs/pages/*.ts`
documents real behavior, including known gaps. If your change fixes a
documented limitation, adds a config field, changes a default, or adds a
capability, update those pages in the same commit. Fixing a documented gap
without updating the doc is an incomplete change.

**Respect the single sources of truth.** Several lists exist exactly once, and
duplicating them is the most common way to break things:

- Monitor types — `api/internal/validation/registry.go` (`DefaultRegistry`).
  Import, preview, and anything type-driven must delegate to it rather than
  keeping a parallel hardcoded list.
- Active-check types — `validation.IsActiveCheckType`.
- Monitor config secrets — `shared/secrets/monitor_config.go`. One entry there
  gives you encryption at rest, `***` masking on reads, write-only merge on
  updates, and correct scheduler/worker handling. No per-type code needed.

**Adding a monitor type** touches several layers in a fixed order; the checklist
lives in [CLAUDE.md](CLAUDE.md) under "Adding a monitor type". Anything that
dials a network target must go through `dialGuard` and accept
`(blockPrivateIPs, allowedCIDRs)` so the SSRF policy holds.

**Cross-service contracts** — queue subject names, `PROBARA_SECRETS_KEY`, and
the `SMTP_*` variables must stay consistent across API, scheduler, worker, and
alerter. Changing one service alone breaks the others silently. CLAUDE.md
documents each contract.

## Commits and pull requests

This project uses [Conventional Commits](https://www.conventionalcommits.org/);
releases are generated from commit history, so the prefix matters:

```
feat(worker): add Redis monitor type
fix(alerter): stop double-sending resolved notifications
chore(repo): ...
```

Write a body that explains the root cause and how you verified the fix, not
just what changed.

For pull requests:

1. Branch off `dev` (not `main`).
2. Keep the change focused — one concern per PR.
3. Include tests for behavior changes, and say in the PR description what you
   ran to verify.
4. Make sure builds and type checks pass for every module you touched.

## Reporting bugs and requesting features

Open an issue with the version or commit, what you expected, what happened, and
enough to reproduce it. For anything security-related, do **not** open a public
issue — see [SECURITY.md](SECURITY.md).

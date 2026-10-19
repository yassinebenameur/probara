# Test suites and CI

Run Go commands from the repository root. `make test` keeps the complete suite
with race detection and coverage. `make test-unit` (or `go test -race -tags=unit
./...`) runs the fast suite without Docker, Chrome, or a collector binary.
The fast suite includes local HTTP servers and in-process protocol peers; it
does not require a running application stack.

External-service tests belong in `*_integration_test.go` files with
`//go:build !unit` before the package declaration. This excludes them only
when the `unit` tag is selected, preserving ordinary `go test ./...` behavior.
Keep shared assertions and pure fixture builders in ordinary `*_test.go`
files so both modes compile. CI checks the file convention and runs both
modes. A test's security, regression or contract purpose does not change its
suite: classify it by the dependencies it needs.

## Go jobs

`build-test.yml` runs formatting and full vetting, the fast suite, and three
complete package groups in parallel. The complete groups intentionally also
run their cheap tests; this avoids changing the default full-suite behavior.
`.github/workflows/scripts/ci.py packages` enumerates `go list ./...` and
assigns every package exactly once:

| Group | Packages |
|---|---|
| `api` | API packages except dashboard and SLA services |
| `alerting-worker` | Alerter and worker packages, including SIPp and browser checks |
| `platform` | Scheduler, shared code, status pages, utilities, dashboard/SLA services and newly added roots |

Each full job uses `-race -count=1 -p 2` and a job-local PostgreSQL 16 service.
`PROBARA_TEST_POSTGRES_DSN` selects that service; `shared/testutil` creates,
migrates and drops a unique database per fixture. Test databases are isolated
even when packages execute concurrently. Local runs without the variable keep
using testcontainers. The database client integration test retains its own
container to exercise client startup independently. NATS and SIPp tests also
own their containers. Chrome must be installed for the worker group.

Go output is recorded as JSON in seven-day workflow artifacts, with failures
and package timings repeated in the step summary. Missing Docker or Chrome
must fail the full suite rather than silently reduce coverage. The report
allows only these documented skips:

- `TestDumpPreviews`: opt-in email preview generation.
- `TestWritePreviewHTML`: opt-in status-page preview generation.
- `TestGeneratedConfigValidatesWithCollector`: run after
  `make build-collector-static` when changing collector configuration or wiring;
  CI does not build the collector as part of every application test run.

The fast job excludes the collector integration file entirely. Additional
dependency-related skips must be fixed or explicitly reviewed, not added to
the allow-list just to make CI pass.

Each full group uploads a separate atomic coverage profile. The aggregate
requires all three profiles, sums matching block counts, and uploads one
complete report with the existing Codecov `main` flag. Fast-job coverage is
not uploaded a second time. `Go test (main module)` remains the stable required
Go check and verifies all Go groups without waiting for AI reproduction.
The existing frontend check names also remain unchanged. An unexpectedly
skipped, failed or cancelled dependency fails its aggregate check.

## Python and checkpoint validation

Install the existing hash-locked CPU test dependencies and pinned laya package
before running Python tests; synthetic model fixtures still need Torch and
laya. Generate the controlled dataset as described in `CLAUDE.md`.

`python -m pytest ai-service/tests .github/workflows/scripts -m 'not checkpoint'
--strict-markers -q -rs` runs the fast Python lane. The `checkpoint` marker in
`pytest.ini` identifies the committed LFS-head integrity test, real-base
composition test and bundled CPU inference test. Running pytest without `-m`
still selects both groups, with the existing real-model environment-variable
requirements. The checkpoint job pulls and verifies the actual heads/base,
runs `-m checkpoint`, and reproduces the held-out CPU release gate unchanged.

The full checkpoint job runs on every protected-branch push and on PR changes
to AI source, dependencies, artifacts, shared AI contracts, Helm, workflow helpers,
or relevant packaging/startup and pytest configuration files. Unknown or unavailable diffs trigger it
conservatively. Renames consider both old and new paths. Other PRs still run
the fast Python suite but can complete without repeating the unchanged model
evaluation. Selection happens inside an always-triggered workflow; the final
`Bundled CPU AI` check permits a skipped checkpoint job only when selection
explicitly says it is unnecessary. Release approval checks are not relaxed.

Superseded PR runs are cancelled. Protected-branch runs retain full validation.
The two frontend jobs retain lint, type checking and production builds; the
operator UI also runs its existing TypeScript tests. Extended fuzzing and Go
benchmarks remain explicit local runs, separate from ordinary PR validation.

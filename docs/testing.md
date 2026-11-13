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
files so both modes compile: a plain test calling a helper that lives in an
integration file builds under plain `go test` but not under `-tags=unit`.
`make lint` vets both modes and checks that every `*_integration_test.go`
carries the tag; CI runs the same checks. The fast job runs without Docker,
so a Docker-backed test in a plain file fails there. Nothing detects other
misclassified dependencies (Chrome, binaries, slow in-process peers), so
classify each test by the dependencies it needs. A test's security,
regression or contract purpose does not change its suite.

## Go jobs

`build-test.yml` runs formatting and full vetting, the fast suite, and three
complete package groups in parallel. The complete groups intentionally also
run their cheap tests; this avoids changing the default full-suite behavior.
`.github/workflows/scripts/ci.py packages` enumerates `go list ./...` and
assigns every package exactly once through a static path-prefix map
(`shard_for`); new top-level directories land in `platform`. Groups are not
balanced by timing, so move packages when one group's wall time drifts and
update this table with it:

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

The SIPp test's `media/*` subtests (`TestSIPInvite_SIPpUAS_Integration`) place
test calls that exchange RTP with SIPp's `-rtp_echo` (echo with PCMU and PCMA,
TCP signalling, `audio`, `two_way`, and SIPp without the echo for the no-echo,
no-audio and measure-only outcomes). Each publishes an even UDP port P from
the host's `127.0.0.1` to the same port in the container, so the templated
answer SDP (`testdata/sipp/uas_echo.xml.tmpl`) can name `127.0.0.1:P`, and the
echo comes back through the daemon's loopback forwarder: Docker Desktop's on
macOS, `docker-proxy` (or DNAT and conntrack with `userland-proxy: false`) on
Linux. That needs a daemon publishing on the test host's own loopback, so a
remote `DOCKER_HOST` fails these subtests with a message naming the cause;
rootless Docker and Podman are not supported. Readiness also waits for SIPp's
media socket on a non-loopback container address and for a build that has
`-rtp_echo`. No subtest skips: the report's skip check covers subtests too.
One-way streams, loss and jitter are covered in process by `fakeMediaPeer`.

Go output is recorded as JSON in seven-day workflow artifacts, with failures
and package timings repeated in the step summary (failure detail is capped
at 512 KiB; the rest stays in the artifact). Missing Docker or Chrome
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
sums matching block counts and uploads one report with the existing Codecov
`main` flag. It requires all three profiles when every group passed; when one
failed it uploads whatever profiles exist, with a warning, before failing the
check. Fast-job coverage is
not uploaded a second time. `Go test (main module)` remains the stable required
Go check and verifies all Go groups without waiting for AI reproduction.
The existing frontend check names also remain unchanged. An unexpectedly
skipped, failed or cancelled dependency fails its aggregate check.

The aggregate also uploads the merged profile as `merged-coverage`, which the
`SonarQube Cloud` job scans with the rest of the repository once every Go
group has passed. `sonar-project.properties` sets the organization, project
key, source/test split and exclusions; coverage is Go-only, so the other
languages are kept out of the coverage metric. The organization is on the
Free plan, which analyzes only the project's main branch and PRs targeting it,
so `dev` is the main branch in SonarQube Cloud and the job runs only for
pushes to `dev` and PRs into it (`staging`/`main` are promotions of `dev`).
Automatic Analysis is off for the project; with it on, the CI scan fails.
Without the `SONAR_TOKEN` repository secret (fork and Dependabot PRs never
receive it) the job's steps skip and it passes. It is not a required check.

## Python and checkpoint validation

Install the existing hash-locked CPU test dependencies and pinned laya package
before running Python tests; synthetic model fixtures still need Torch and
laya. Generate the controlled dataset as described in `CLAUDE.md`.

`python -m pytest ai-service/tests
.github/workflows/scripts/test_local_ai_artifact.py -m 'not checkpoint'
--strict-markers -q -rs` runs the fast Python lane (`test_ci.py` runs in the
Go formatting job). The `checkpoint` marker in
`pytest.ini` identifies the committed LFS-head integrity test, real-base
composition test and bundled CPU inference test. Running pytest without `-m`
still selects both groups, with the existing real-model environment-variable
requirements. The checkpoint job pulls and verifies the actual heads/base,
reproduces the held-out CPU release gate unchanged, then runs
`-m checkpoint`; the gate runs first so a test failure cannot hide it.

The full checkpoint job runs on every protected-branch push and on PR changes
to AI source, dependencies, artifacts, shared AI contracts, Helm, workflow helpers,
or relevant packaging/startup and pytest configuration files. Unknown or unavailable diffs trigger it
conservatively. Renames consider both old and new paths. Other PRs still run
the fast Python suite but can complete without repeating the unchanged model
evaluation. Selection happens inside an always-triggered workflow; the final
`Bundled CPU AI` check permits a skipped checkpoint job only when selection
explicitly says it is unnecessary. Release approval checks are not relaxed.

Superseded PR runs are cancelled. Every push runs in its own concurrency
group, so protected-branch runs are never queued behind or cancelled by a
later push.
The two frontend jobs retain lint, type checking and production builds; the
operator UI also runs its existing TypeScript tests. Extended fuzzing and Go
benchmarks remain explicit local runs, separate from ordinary PR validation.

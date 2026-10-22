# Bundled CPU AI

Dependency suggestions, incident analysis and automatic alert triage use one
Probara-tuned decision head on the [Laya multilingual](https://huggingface.co/convaiinnovations/laya)
encoder, trained through the
[pinned training interface](https://github.com/NandhaKishorM/laya/blob/68804629e8ccd9d616d48a40e87de9fabbeae069/laya/train.py).
Inference runs in `ai-service/`, a CPU-only Python service that needs no
network at runtime. There is no external Chat Completions path. Current
validation results are in [local-ai-validation.md](local-ai-validation.md).

## Deployment and settings

Local AI is on by default. Only the API (dependency suggestions, readiness)
and the database-connected worker (incident analysis, automatic alert triage)
call the service, over `POST /v1/decisions` with a bearer token. The alerter
never calls it: it reads ready triages for the opt-in suspected-cause
follow-up, and `LOCAL_AI_DISABLED=true` or `AI_TRIAGE_ENABLED=false` stops
those follow-ups too. SLA reports are the other reader: the API's suspected-cause
breakdown (`api/internal/services/slas/causes.go`) reads ready triages and
their operator labels read-only through `triageview.EffectiveCause` (a label
wins over the model's hypothesis), never calling the service and never
affecting paging. The service receives only that
token: no database, NATS or platform credentials. Private-location workers
never receive its URL or token and run checks without it.

| Setting | Default | Read by | Meaning |
|---|---|---|---|
| `LOCAL_AI_DISABLED` | `false` | API, worker, alerter, service | Installation override; disables analysis, triage, suggestions and suspected-cause follow-ups for every workspace |
| `LOCAL_AI_URL` | `http://127.0.0.1:8090` | API, worker | `http(s)://host[:port]`, no path, query or credentials |
| `LOCAL_AI_TOKEN` | none | API, worker, service | Shared bearer token, at least 16 characters; every side trims surrounding whitespace (Go `strings.TrimSpace`, Python `str.strip`) |
| `LOCAL_AI_TIMEOUT_SECONDS` | `90` | API, worker | Request timeout, 1–300 seconds |
| `LOCAL_AI_THREADS` | `4` | service | CPU inference threads, 1–4 |
| `LOCAL_AI_QUEUE_CAPACITY` | `4` | service | Batches waiting behind the executing one, 1–32 |
| `LOCAL_AI_QUEUE_TIMEOUT_SECONDS` | `60` | service | Wait for the executing slot, above 0 and at most 300; keep below `LOCAL_AI_TIMEOUT_SECONDS` (Helm enforces it) |
| `LOCAL_AI_MAX_INFERENCE_SECONDS` | `120` | service | A batch running longer is wedged: `/healthz` and `/readyz` answer 503; above 0 and at most 600 |

- **Compose**: service `local-ai`, published on `127.0.0.1:8090`, wired to
  `api` and `worker` as `http://local-ai:8090`. Without an exported
  `LOCAL_AI_TOKEN` all three use a fixed development token that is public in
  this repository; it is tolerable only because the port is loopback-bound.
  The image is pulled (`LOCAL_AI_IMAGE`) and built from `ai-service/` only
  when the pull fails. The Make targets start the platform first and
  `local-ai` separately, so a failed build leaves AI unavailable instead of
  aborting the stack. `LOCAL_AI_CPUS` (Compose CPU limit) and the thread count
  default to min(host CPUs, 4) in the Make targets.
- **Local processes** (`make start-all-local`): the same container, plus a
  per-checkout token in the gitignored `.dev-local-ai-token`, exported to the
  container and to the api/worker processes. The container starts after the
  Go services, in the background (progress in `/tmp/probara-local-start.log`);
  with no local image and the head weights still a Git LFS pointer it only
  tries a pull, never a build.
- **Rebuilds**: a local build is tagged as `LOCAL_AI_IMAGE` and reused as is,
  so after an `ai-service/` or `MODEL_VERSION` change run
  `docker compose build local-ai` (or `docker compose up -d --build local-ai`).
- **Helm**: `localAI` values deploy one `Recreate` replica (1 CPU/2 GiB
  request, 4 CPU/4 GiB limit), a ClusterIP Service and a NetworkPolicy that
  admits only the API and worker pods and allows no egress; it is enforced
  only by a CNI that implements NetworkPolicy. `localAI.externalUrl` points
  API and worker at an operator-run service instead (with `enabled=false` and
  an explicit token). The token is the chart credential `local_ai_token`
  (`secrets.localAiToken`, a per-field or chart-wide existing Secret); left
  empty and chart-managed it is derived from `secrets.adminJwtSecret`. The
  website's Deployment page (`/docs/deployment/#helm-values-local-ai`) has
  the full contract, including the upgrade case of an existing Secret
  without the key.

Workspace Settings → AI shows enablement and readiness. Explicit workspace
switches are preserved and absent settings default enabled; the installation
disable overrides them. Settings updates accept `enabled` and the automatic
alert triage fields, including `triage_notifications_enabled` (the opt-in
suspected-cause follow-up; triage itself never delays, routes, suppresses or
re-prioritises a page). The test
endpoint accepts `{}` and probes the configured service.

The service exposes `/healthz` (unauthenticated), `/readyz` (bearer token;
503 until the model is loaded, when disabled, or while shutting down) and
`/metrics` (`probara_ai_*`). It runs one batch at a time; a batch beyond the
queue capacity, or one that waits past the queue timeout, gets HTTP 429 with
`Retry-After: 5`. A full 50-decision batch takes about 15-20 s on 4 CPU
threads, so the 60 s default queue timeout lets a queued request outlast a
batch or two, and it stays below the 90 s client timeout so callers see a
retryable 429 (worker jobs are redelivered; dependency suggestions answer 503
`unavailable` with `Retry-After`) rather than their own timeout. A batch still
running after `LOCAL_AI_MAX_INFERENCE_SECONDS` is wedged: an executor thread
cannot be cancelled, so `/healthz` and `/readyz` answer 503 and
`probara_ai_wedged` reads 1 until it finishes, and the Helm liveness probe
(every 15 s, three failures) restarts the pod. Compose only marks the
container unhealthy. Bodies over 512 KiB get 413, and evidence outside the
allowlist or duplicate decision IDs get 422.

## Model artifact

A committed artifact is `ai-service/artifacts/<version>/`, where `<version>`
matches `probara-laya-*`. Training freezes the encoder, so the artifact holds
only what training changes:

| File | Content |
|---|---|
| `head.safetensors` | The trained decision-head tensors (fp16, about 30 MB); the only Git LFS file |
| `rl_agent_config.json` | Head configuration; `model_name` equals the version |
| `calibration.json` | Per-task temperature and threshold from CPU validation |
| `cpu-evaluation.json` | CPU held-out report, written by `validate_cpu.py`; carries `release_approved` |
| `evaluation.json`, `training-config.json`, `provenance.json`, `upstream.json` | Training-time report, settings, corpus provenance, upstream pins |
| `LICENSE`, `base-model-card.md` | Upstream license and model card |
| `manifest.json` | Integrity and identity record (below) |

`manifest.json` records `version`, `catalogue` (must equal
`shared/ai/catalogue.json`'s version), `state_format`, `release_approved`,
`cpu_format` (int8 dynamic encoder linear layers, float32 head, QNNPACK,
torch 2.8.0), `base` (Hugging Face repo, revision and weights sha256),
`upstream_commit`, `head_sha256`, `files` (sha256 of every committed file)
and `composed` (sha256 of every file composition generates). `version`,
`catalogue`, `state_format`, `base`, `upstream_commit` and `head_sha256` are
fixed when a candidate is written; changing any of them makes a new version.
`python ai-service/training/compose_artifact.py verify --artifact <dir>`
checks committed integrity without the base.

Install Git LFS and run
`git lfs pull --include 'ai-service/artifacts/*/head.safetensors'` before
building the image or running artifact tests. Tokenizer and encoder are never
committed; they come from the pinned base.

### Evidence state format

The service renders evidence to text before tokenizing it, and a head only
works on the rendering it was trained on. `probara_ai.contract.STATE_FORMAT`
versions that rendering (current: 2, whose wording follows the catalogue
descriptions). Each artifact pins its own in the manifest's `state_format`
(absent means 1, the first artifact's generic labels), the corpus used to
validate it must be rendered in the same format, and the runtime renders
requests in the artifact's format. Changing the rendering is a new model,
never a refactor: add a format, do not edit an existing one.
`training/history/generate_v1.py` regenerates the format-1 corpus for v1.

## Image build

`ai-service/Dockerfile` (context: repository root) builds offline-capable
images in stages:

1. `laya-wheel`: fetches the laya commit from `training/base-lock.json`
   (`upstream_commit`), checks it out by full commit id and builds a wheel.
2. `base-model`: `training/upstream.py` downloads the pinned base by Hugging
   Face revision and verifies the sha256 of every locked file (weights,
   tokenizer, encoder config, model card) before anything uses them.
   `base-lock.json` also records the `normalized` hash of
   `tokenizer/tokenizer_config.json`, which laya rewrites in place on load;
   `fetch` applies that rewrite itself so the base is never modified later.
3. `dependencies`: the hash-locked CPU runtime (`requirements.lock`, torch
   from the PyTorch CPU index) plus the laya wheel.
4. `artifact`: `compose_artifact.py compose` loads the base, overlays
   `head.safetensors`, saves through laya's `save_checkpoint` and copies the
   committed metadata; it refuses any result whose files differ from the
   manifest's `composed` hashes. Then the release gate,
   `probara_ai.runtime.verify_artifact`, must accept the composed checkpoint
   or the build fails.
5. `runtime`: no git, compiler or pip; uid/gid 1000; root-owned code and
   weights; writes only `/tmp`; `HF_HUB_OFFLINE=1` and `TRANSFORMERS_OFFLINE=1`.

`ARG MODEL_VERSION` (default `probara-laya-v4`) names the artifact directory
to ship. CI reads the shipped version from that line, so shipping a new
artifact means committing its directory and changing that default. The
service also runs `verify_artifact` at startup.

Two stages fetch from third parties, and both locations are build args so a
mirror can stand in if upstream disappears: `LAYA_GIT_URL` (default
`https://github.com/NandhaKishorM/laya.git`) for the laya commit, and
`HF_ENDPOINT` plus optional `BASE_MODEL_REPO`/`BASE_MODEL_REVISION` (empty =
`base-lock.json`) for the base. The pins stay the authority: the checkout must
be the locked `upstream_commit`, and every base file must match its locked
sha256, so a mirror only has to serve the same bytes. CI passes the
repository variables `vars.LAYA_GIT_URL` and `vars.HF_ENDPOINT` when set.

## Release gate

`release_approved` is written only by `training/validate_cpu.py`, after it
recalibrates on the calibration partition and scores the held-out test
partition on CPU with the deployed preparation. `probara_ai/gate.py` holds
the predicate. For each of `rca` and `dependency`, the held-out result must
reach:

- emitted precision of at least 95% and at least the conservative rules'
  precision,
- at least 90% abstention on unsupported cases,
- at least 50% supported coverage, and more coverage than the rules,
- decision accuracy no lower than the rules'.

`verify_artifact` recomputes the gate from the report's metrics, so editing
the boolean does not approve anything. It also requires the report's version,
composed weights sha256, CPU format, state format and calibration to match
the artifact. An approved artifact is immutable.

Enforcement: the image build fails for an unapproved artifact; `build-test`
composes every committed artifact and requires the gate to accept exactly the
approved ones; on an approved shipped artifact it also re-scores the held-out
partition on linux/amd64, where the QNNPACK kernels differ from the
developer machine, and fails unless the gate still holds there. `docker-build`
builds and pushes the `-local-ai` image only on release tags and only for an
approved artifact (branch pushes leave it to `local-ai-pr.yml` and
`build-test`), pulling just the shipped checkpoint's head from Git LFS, and
`release.yml` refuses to tag or publish the chart while the shipped artifact
is unapproved, because the chart deploys local AI by default. Never set
approval by hand.

`release.yml` also runs `local_ai_artifact.py sources` before tagging: a
shallow fetch of the pinned laya commit and a Hugging Face check that the
pinned revision still lists every locked file. Either source vanishing would
otherwise fail only the tag's local-ai image build, after the chart is
published. If that happens anyway, point the `LAYA_GIT_URL` / `HF_ENDPOINT`
repository variables at a mirror and re-run only the failed `local-ai` leg
of `docker-build`; until it succeeds, installs can pin `localAI.image.tag` to
the last good release.

### Image visibility (one-time)

Every tagged image must be pullable anonymously, because the chart deploys
them without pull secrets by default. A new GHCR package in a personal
account starts **private**, so the first release that pushes
`probara-local-ai` creates it private, and `docker-build`'s "Check the image
is publicly pullable" step then fails that leg. Once:

1. Open github.com/users/yassinebenameur/packages/container/probara-local-ai/settings.
2. Set visibility to **Public** and link the package to the probara
   repository, like the other `probara-*` packages.
3. Verify anonymously: `docker logout ghcr.io && docker manifest inspect
   ghcr.io/yassinebenameur/probara-local-ai:<tag>`, then re-run the failed
   leg (its image is already pushed; the check passes).

Ideally do it before the first tag that ships local AI: push one approved
image under the account by hand, make the package public, and the release is
pullable as soon as it is published.

## Training and validation

Run from the repository root in an isolated Python 3.13.3 environment.
Paths under `ai-service/.cache/` are gitignored.

```sh
export PYTHONPATH=ai-service:ai-service/training PYTHONDONTWRITEBYTECODE=1
python -m pip install --require-hashes --only-binary=:all: \
  --extra-index-url https://download.pytorch.org/whl/cpu -r ai-service/requirements-dev.lock
python -m pip install --no-deps --no-build-isolation -r ai-service/requirements-laya.txt

# Pinned base, verified file by file (the only networked step)
python ai-service/training/upstream.py --output ai-service/.cache/base

# Controlled corpus (current state format) from real loopback checker output
PROBARA_AI_FIXTURES=ai-service/training/checker-fixtures.json \
  go test ./worker/internal/worker -run TestAIFaultFixtures -count=1
python ai-service/training/generate.py --output ai-service/artifacts/dataset

# Train a head from the base on the deployed CPU features (training rows only)
python ai-service/training/train_head_cpu.py --version probara-laya-v3 \
  --base ai-service/.cache/base --data ai-service/artifacts/dataset \
  --output ai-service/artifacts/probara-laya-v3 \
  --work ai-service/.cache/composed --epochs 3

# Optional: training-only dependency augmentation (deterministic, seeded) and
# a warm start from a previous head, as probara-laya-v4 was trained
python ai-service/training/augment.py --output ai-service/.cache/augment
python ai-service/training/train_head_cpu.py --version probara-laya-v4 \
  --base ai-service/.cache/base --data ai-service/artifacts/dataset \
  --extra-train ai-service/.cache/augment/dependency-train-augment.jsonl \
  --init-head ai-service/.cache/composed/probara-laya-v3.epochs/head-epoch-3.safetensors \
  --output ai-service/artifacts/probara-laya-v4 \
  --work ai-service/.cache/composed --epochs 3

# Select on the calibration partition only (no report, no approval) ...
python ai-service/training/validate_cpu.py --calibration-only \
  --artifact ai-service/artifacts/probara-laya-v4 \
  --base ai-service/.cache/base --data ai-service/artifacts/dataset \
  --work ai-service/.cache/composed

# ... then calibrate, score held-out on CPU once, write the report and any approval
python ai-service/training/validate_cpu.py \
  --artifact ai-service/artifacts/probara-laya-v4 \
  --base ai-service/.cache/base --data ai-service/artifacts/dataset \
  --work ai-service/.cache/composed

python -m pytest ai-service/tests -q
```

Alternatives write the same head-only layout: `training/train.py --version
<v> ... --device auto` trains on an accelerator, and `training/train_cpu.py
--version <v> --resume ai-service/artifacts/<predecessor> ...` adapts an
existing head to the CPU features (a corrected corpus must keep the
predecessor's scenario group roles; pass `--predecessor-data`).
`tools/convert_candidate.py` converts a full-checkpoint candidate from an
earlier script into this layout, verifying that every encoder tensor equals
the base. No training script writes approval. Per-epoch recovery heads go to
`<work>/<version>.epochs/`, never the artifact. `--extra-train` refuses rows
whose scenario groups appear in the calibration or test partitions, and
`--init-head` accepts head tensors only; both are recorded in the artifact's
`training-config.json` with their sha256.

Scenarios use reproducible local checker faults, configured topologies and
generated telemetry: 5,000 examples per task in 1,000 independent groups,
split 60/20/20 into train, calibration and test before variations are
expanded, with option order, names and timestamps randomized from a fixed
seed. Hidden causes without visible evidence are labelled insufficient
evidence. The conservative baseline requires three repeated direct symptoms
or five co-firing windows; the stronger construction-aligned guards are a
disclosed corpus upper bound, not a claim the model beats. Results measure
this controlled corpus, not real-world incident accuracy.

### Operator labels

Operator labels are the route to real-world accuracy. A tenant admin exports
them from Settings → AI → Export labels (`POST /api/v1/ai-labels/export`,
optionally windowed by `since`/`until` and filtered by subject type), which
downloads one `probara-ai-labels-YYYYMMDD.jsonl` per workspace. Each row is a
training row (`task`, `group`, `evidence`, `state`, `questions`, `expected`,
plus `provenance`) whose `state` is rendered in evidence state format 1,
frozen on the Go side (`shared/ai/labelexport.go`, pinned by
`shared/ai/testdata/label-export-v1.jsonl`) whatever the current
`STATE_FORMAT` is. Pass the file as `--extra-train`
only, never as calibration or test data:

```sh
python ai-service/training/train_head_cpu.py --version <next> \
  --base ai-service/.cache/base --data ai-service/artifacts/dataset \
  --extra-train ai-service/.cache/labels/probara-ai-labels-20261016.jsonl \
  --output ai-service/artifacts/<next> --work ai-service/.cache/composed --epochs 3
```

`read_extra_train` re-renders each `op-<task>-` row from its evidence in the
current format (`probara_ai.contract.from_label_export`) and refuses a row
whose exported state is not exactly the format-1 text (an edited file, or a Go
and Python contract that drifted), as well as any other extra row not in the
current format. Export groups are `op-<task>-<hmac>`, disjoint from corpus
groups (and `--extra-train` refuses any overlap with calibration or test), so
held-out numbers stay corpus-only, as for v4. The file's sha256 is recorded
in `training-config.json`. An export carries evidence counters and answers
with no monitor names or IDs, but it still describes a workspace's
incidents: share it only with the people who retrain the model (docs site,
Security → "Notifications, webhooks, SMTP, and AI").

## Evidence and outcomes

RCA uses the earliest linked alert as its anchor, falling back to incident
creation, and covers 15 minutes before through 60 minutes after it, capped at
request time. Monitor and upstream selection is bounded to 100 and detailed
checks to 20,000 per monitor; truncated detail prevents a confident monitor
diagnosis. Large incidents use batches of at most 50 decisions.

The inference allowlist holds counts, numeric protocol timings, certificate
validity/change flags, fresh host CPU/memory pressure flags, measured
upstream ordering and maintenance overlap. It excludes configuration,
credentials, addresses, names, error text, bodies and screenshots. Only
opaque decision IDs leave the caller. The model selects one supplied
hypothesis; all prose and actions come from the versioned catalogue
(`shared/ai/catalogue.json`, shared with the service, so a change is a
catalogue version bump on both sides and a model whose catalogue differs is
refused). A supported diagnosis is a **suspected cause**; unsupported
incidents finish with insufficient evidence, and infrastructure failures
(including repeated 429s) finish failed with a retry action.

Suggestions examine up to 50 pairs, prioritizing 30-day co-firing history in
ten-minute buckets before service/group and naming signals. An emitted edge
needs repeated failures, consistent timing direction, service/group
corroboration, no known common upstream and the calibrated threshold; names
alone never establish dependency. Existing, duplicate, self-referencing and
cycle-producing edges are rejected, and acceptance uses the existing
dependency write endpoint.

## Acceptance

`training/benchmark.py` runs inside a 4-CPU/4-GiB container with network
`none` and measures cold start, warm 20-monitor RCA p95, warm 50-pair
latency, peak RSS and a 500-request soak. With Docker and a disposable test
cluster, build the evaluated image and run the isolated acceptance scripts:

```sh
docker build -f ai-service/Dockerfile -t probara-local-ai:validation .
python scripts/local-ai-acceptance.py --start   # then --run, then --stop
python scripts/local-ai-kubernetes-acceptance.py --context "$TEST_CONTEXT" \
  --image probara-local-ai:validation --output ai-service/artifacts/kubernetes-acceptance.json
```

The API fixture defaults to `http://127.0.0.1:18090` and token
`isolated-local-ai-acceptance-token` (`--model-url`, `--token`). Kubernetes
acceptance creates its own token and namespace; it does not establish that
the cluster's CNI enforces NetworkPolicy.

## Migration and rollback

Migration 000092 adds structured metadata, preserves historical analysis
prose and labels it `legacy-llm`, and adds the partial index on pending
analyses that the worker's stale sweep scans. Obsolete provider columns and encrypted
keys remain unused for rollback compatibility, and retired `LLM_*` variables
are ignored. To roll back, restore the previous application images and
configuration; dropping the metadata removes new provenance but leaves the
existing analysis fields intact.

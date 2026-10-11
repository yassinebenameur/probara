# Local AI validation

Status as of 2026-10-11: **no artifact is release-approved.** The shipped
artifact (`ai-service/Dockerfile` `ARG MODEL_VERSION`) is `probara-laya-v1`,
which failed its CPU release gates. Until an artifact passes, the local AI
image cannot be built, `docker-build` pushes no `-local-ai` image, and
`release.yml` refuses to tag or publish the chart. Default-on Compose and
Helm installs from source therefore run with AI analysis reported
unavailable unless an approved image is supplied or local AI is disabled.

The gate and the commands are in [local-ai.md](local-ai.md). All numbers
below come from the artifact's own reports and measure a controlled
corpus, not real-world incident accuracy.

| Candidate | State format | How it was trained | Result |
|---|---|---|---|
| `probara-laya-v1` | 1 | Head tuned on an accelerator, encoder frozen, 2 epochs | Failed CPU gates; kept as a predecessor and reproducibility record, never deployable |
| v2 | 1 | CPU-feature adaptation of v1 | Lost before validation; no report exists and nothing is claimed for it |
| `probara-laya-v3` | 2 (catalogue-aligned wording) | Head trained from the base's own head on the deployed CPU-format features, training rows only, corpus `probara-controlled-v3` | Training in progress; calibration and held-out CPU evaluation pending |

## probara-laya-v1

Each task has 5,000 controlled examples (1,000 held-out test cases: 610
supported for RCA, 255 for dependency). The conservative rules baseline is
predeclared.

Accelerator evaluation (`evaluation.json`), not a release result:

| Task | Coverage | Precision | Unsupported abstention | Accuracy | Rules coverage / accuracy | Outcome |
|---|---|---|---|---|---|---|
| RCA | 94.1% | 100% | 100% | 96.4% | 75.9% / 85.3% | Passes |
| Dependency | 48.6% | 100% | 100% | 86.9% | 73.7% / 93.3% | Fails: below 50% and the rules |

Deployment-format CPU evaluation (`cpu-evaluation.json`, int8 dynamic
encoder, float32 head, QNNPACK), the gate that decides release:

| Task | Coverage | Precision | Unsupported abstention | Accuracy | Rules coverage / accuracy | Outcome |
|---|---|---|---|---|---|---|
| RCA | 60.7% | 100% | 100% | 76.0% | 75.9% / 85.3% | Fails: coverage and accuracy below the rules |
| Dependency | 5.5% | 100% | 100% | 75.9% | 73.7% / 93.3% | Fails |

That run used a fixed 0.5 probability threshold, which is not a gate
requirement. A later calibration-only audit
(`cpu-calibration-audit-initial.json`, calibration partition only) searched
thresholds 0.01–0.99: the best RCA coverage was 63.1% against the rules'
74.3%, and dependency 20.0% against 72.5%, so no threshold can qualify v1.
That audit is disclosed because the held-out results had already been seen;
no gate was weakened. The untuned base, calibrated to the same precision,
covered 43.8% (RCA) and 1.2% (dependency).

The artifact has since been re-laid out head-only.
`compose_artifact.py record --expect-model-sha256` reproduced the evaluated
full checkpoint (`b8ca791b…`) bit for bit from the head and the pinned base,
so the reports above still describe the committed bytes.

## probara-laya-v3

v3 changes two things against v1: the evidence is rendered in state format 2,
whose wording follows the catalogue descriptions, and the head is trained
directly on the features the deployed CPU preparation produces instead of
being adapted after accelerator training. It is trained from the untuned
base, not from v1. It is unapproved until `validate_cpu.py` writes a passing
`cpu-evaluation.json`; if it passes, shipping it means committing
`ai-service/artifacts/probara-laya-v3/` and changing the Dockerfile's
`MODEL_VERSION`, after which CI re-scores the held-out partition on
linux/amd64.

## Still to verify with an approved artifact

- Network-disabled container inference with the real checkpoint.
- API → JetStream → worker → persisted RCA, dependency suggestions and
  acceptance end to end.
- The 4-CPU/4-GiB benchmark: cold start, warm 20-monitor RCA p95, warm
  50-pair latency, peak RSS, 500-request soak.
- Disposable Kubernetes namespace acceptance. A rendered NetworkPolicy is not
  evidence of CNI enforcement; that must be checked per cluster.

Packaging is checked without a model: `ai-service/tests` (contract, queue
and cancellation, artifact composition and gate, packaging pins),
`.github/workflows/scripts/local_ai_helm_cases.py` and
`scripts/check-local-ai-packaging.py` (chart and Compose wiring), all run in
CI.

## Operational limits

RCA selects at most 100 affected or upstream monitors, traverses groups to
depth eight, summarizes at most 20,000 detailed checks per monitor and
samples 24 timeline transitions. Bounds and missing evidence are recorded;
unseen detailed rows prevent a monitor diagnosis. Suggestions inspect at most
50 candidate pairs against the full configured graph.

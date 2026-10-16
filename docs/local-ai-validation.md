# Local AI validation

Status as of 2026-10-11: **`probara-laya-v4` is release-approved** and is the
shipped artifact (`ai-service/Dockerfile` `ARG MODEL_VERSION`). Its CPU
report was written by `validate_cpu.py` on the developer machine (macOS
arm64, 4 threads); CI re-scores the held-out partition on linux/amd64 before
an image is published, and fails if the gate no longer holds there. Every
other committed artifact stays unapproved and is kept only as a
reproducibility record.

The gate and the commands are in [local-ai.md](local-ai.md). All numbers
below come from the artifacts' own reports and measure a controlled
corpus, not real-world incident accuracy.

| Candidate | State format | How it was trained | Result |
|---|---|---|---|
| `probara-laya-v1` | 1 | Head tuned on an accelerator, encoder frozen, 2 epochs | Failed CPU gates; kept as a predecessor and reproducibility record, never deployable |
| v2 | 1 | CPU-feature adaptation of v1 | Lost before validation; no report exists and nothing is claimed for it |
| `probara-laya-v3` | 2 (catalogue-aligned wording) | Head trained from the base's own head on the deployed CPU-format features, training rows only, corpus `probara-controlled-v3` | Failed CPU gates on dependency coverage; kept as v4's predecessor |
| `probara-laya-v4` | 2 | v3's head, 3 more epochs on the same training rows plus 2,000 training-only dependency rows (`training/augment.py`) | **Passed CPU gates; shipped** |

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
base, not from v1. Corpus `probara-controlled-v3` has the same shape as v1's
(1,000 held-out cases per task: 610 supported for RCA, 255 for dependency).

Deployment-format CPU evaluation (`cpu-evaluation.json`):

| Task | Coverage | Precision | Unsupported abstention | Accuracy | Rules coverage / accuracy | Outcome |
|---|---|---|---|---|---|---|
| RCA | 99.2% | 100% | 100% | 99.5% | 75.9% / 85.3% | Passes |
| Dependency | 65.5% | 100% | 100% | 91.2% | 74.5% / 93.5% | Fails: coverage and accuracy below the rules |

Most dependency misses were the wrong direction: the head confused "A fails
before B" with "B fails before A". On the calibration partition 174 of 255
supported pairs were answered correctly, 57 in the wrong direction and 24
otherwise (68.2% coverage).

## probara-laya-v4

v4 targets that confusion with more training data and changes nothing else:
same corpus partitions, state format, calibration, gate and threshold
search.

- **Training data:** v3's 6,000 training rows plus 2,000 rows from
  `training/augment.py` (seed 20261111, 400 new scenario groups named
  `dependency-aug-<n>`, about 70% of them the two directional kinds and the
  rest near-miss contrasts). The generator is deterministic; the committed
  script regenerates the file bit for bit (sha256 `2dc8e270…`), and
  `train_head_cpu.py --extra-train` refuses any group that also appears in
  calibration or test.
- **Initialisation:** v3's epoch-3 recovery head (float32, sha256
  `32745c89…`, recorded in `training-config.json`), 3 further epochs at the
  same learning rate, head on Apple MPS over int8 CPU-format encoder
  features. Mean training loss per epoch: 0.293, 0.128, 0.071.
- **Selection:** on the calibration partition only. `validate_cpu.py
  --calibration-only` gave dependency coverage 94.5% (rules 72.2%) and RCA
  99.9% (rules 74.3%), so the held-out partition was scored once, by the
  full `validate_cpu.py` run that wrote the report below.

Deployment-format CPU evaluation (`cpu-evaluation.json`), the gate that
decides release:

| Task | Coverage | Precision | Unsupported abstention | Accuracy | Rules coverage / accuracy | Outcome |
|---|---|---|---|---|---|---|
| RCA | 99.7% | 100% | 100% | 99.8% | 75.9% / 85.3% | Passes |
| Dependency | 92.5% | 100% | 100% | 98.1% | 74.5% / 93.5% | Passes |

Calibrated temperature / threshold: RCA 0.992 / 0.96, dependency 0.911 /
0.55. The untuned base, calibrated to the same precision, covered 81.6%
(RCA) and 13.3% (dependency).

Disclosures:

- The held-out partition is not pristine for v4. v3's held-out result had
  been seen, and the direction-confusion diagnosis that motivated the
  augmentation drew on it as well as on the calibration partition. From
  that point every choice was made on calibration only, and v4's held-out
  scores were computed exactly once.
- Precision and unsupported abstention are 100% by construction: the
  deterministic guards veto any answer the measured evidence does not
  support. What the gate actually discriminates is how often the model's
  supported answer is right.
- The labels come from the same rules that gate the answers, so these
  numbers show the model reproducing the rules more often than the
  conservative rules baseline commits to an answer; they say nothing about
  real incidents. Operator labels (`POST /ai-labels/export`) are the path
  to that; see [Operator labels](local-ai.md#operator-labels) for the
  training procedure.
- v4 was trained by a working copy of `train_head_cpu.py` that wrote the
  earlier full-checkpoint layout and was converted with
  `tools/convert_candidate.py`, which verified every encoder tensor against
  the pinned base. The committed script has the same training options and
  writes the head-only layout directly. Retraining needs v3's recovery head
  (not committed; v3's committed `head.safetensors` is its float16 export),
  and MPS training is not bit-for-bit deterministic, so a retrain is a new
  candidate that must pass the gate itself.

## Still to verify with the approved artifact

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

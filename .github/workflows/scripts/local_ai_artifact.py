"""CI helper for the bundled local AI checkpoint (run from the repo root).

The shipped checkpoint is the one ai-service/Dockerfile builds by default
(`ARG MODEL_VERSION=...`), so a new artifact version needs no workflow edit.

gate     Stdlib only, no Git LFS needed. Writes approved/version/reason for
         the shipped checkpoint to $GITHUB_OUTPUT. Approved means manifest.json
         AND cpu-evaluation.json both say release_approved: true, on device
         cpu, and the report's held-out metrics meet probara_ai.gate. The
         image's verify_artifact enforces the same rule (and more); this
         decides whether a release or an image build may go ahead.
verify   Needs the ai-service dependencies, the LFS head weights and a
         verified pinned base (--base). Composes every committed artifact
         directory exactly as the image does (compose_artifact.compose, which
         refuses any result differing from the manifest's `composed`
         checksums), then runs probara_ai.runtime.verify_artifact on the
         result: it must pass for an approved artifact and must refuse an
         unapproved one, so the release gate itself is under test. Writes
         approved/version/composed for the shipped checkpoint to $GITHUB_OUTPUT.
reproduce
         Needs the same as verify, plus the generated controlled corpus
         (--data) and the composed, approved shipped checkpoint (--composed).
         The CPU evaluation that approved it ran on the developer's machine
         (e.g. darwin/arm64), while the image runs linux/amd64, where the
         qnnpack int8 kernels are different code paths. This re-scores the
         held-out test partition on the runner (linux/amd64, the image's
         locked torch wheel) through probara_ai.runtime.Runtime, i.e. with the
         image's CPU preparation and calibration.json, and fails unless the
         CPU release gate (probara_ai.gate) still holds there. Metric drift from
         cpu-evaluation.json is reported but only the gate decides.
"""
import argparse
import json
import os
from pathlib import Path
import re
import sys

ARTIFACTS = Path("ai-service/artifacts")
DOCKERFILE = Path("ai-service/Dockerfile")
# probara_ai.gate is stdlib only, so `gate` still runs without the ai-service
# dependencies; it is the one release-gate predicate (validator, verify_artifact).
sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "ai-service"))
from probara_ai.gate import TASKS, release_gate, task_passes  # noqa: E402


def shipped_version():
    match = re.search(r"^ARG MODEL_VERSION=(\S+)\s*$", DOCKERFILE.read_text(), re.MULTILINE)
    if not match:
        sys.exit(f"::error title=Local AI::{DOCKERFILE} has no ARG MODEL_VERSION=<version> default")
    return match[1]


def approval(artifact):
    """(approved, reason) from the committed release flags alone."""
    manifest = json.loads((artifact / "manifest.json").read_text())
    report = artifact / "cpu-evaluation.json"
    if not report.is_file():
        return False, "no cpu-evaluation.json: the candidate has not been through training/validate_cpu.py"
    evaluation = json.loads(report.read_text())
    flags = (manifest.get("release_approved") is True
             and evaluation.get("release_approved") is True
             and evaluation.get("device") == "cpu")
    # As verify_artifact does, recompute the gate from the reported metrics:
    # a flag the metrics do not support would only fail later, in the image.
    meets_gate = release_gate(evaluation.get("tasks"))
    reason = evaluation.get("gate_failure") or (
        "manifest.json and cpu-evaluation.json must both say release_approved: true on device cpu" if not flags else
        "cpu-evaluation.json says release_approved, but its held-out metrics do not meet the release gate")
    return flags and meets_gate, " ".join(str(reason).split())


def write_outputs(outputs):
    with open(os.environ.get("GITHUB_OUTPUT", os.devnull), "a") as out:
        for key, value in outputs.items():
            out.write(f"{key}={value}\n")
    print(json.dumps(outputs))


def gate(args):
    version = shipped_version()
    artifact = args.artifacts / version
    if not (artifact / "manifest.json").is_file():
        sys.exit(f"::error title=Local AI::the Dockerfile ships {version} but {artifact}/manifest.json does not exist")
    approved, reason = approval(artifact)
    write_outputs({"approved": str(approved).lower(), "version": version, "reason": "" if approved else reason})


def verify(args):
    from compose_artifact import compose
    from probara_ai.runtime import verify_artifact

    shipped = shipped_version()
    artifacts = sorted(p for p in args.artifacts.glob("probara-laya-*") if p.is_dir())
    if shipped not in {p.name for p in artifacts}:
        sys.exit(f"::error title=Local AI::the Dockerfile ships {shipped} but {args.artifacts}/{shipped} does not exist")
    errors, outputs = [], {}
    for artifact in artifacts:
        version, output = artifact.name, args.work / artifact.name
        approved, reason = approval(artifact)
        try:
            composed = compose(artifact, args.base, output)
        except Exception as error:  # noqa: BLE001 - reported per artifact
            errors.append(f"{version}: composition failed: {error}")
            continue
        print(f"{version}: composed from the pinned base, model.safetensors {composed['model.safetensors']}")
        try:
            verify_artifact(output)
            accepted, refusal = True, ""
        except Exception as error:  # noqa: BLE001
            accepted, refusal = False, str(error)
        if approved and not accepted:
            errors.append(f"{version}: release-approved, but verify_artifact refuses it: {refusal}")
        elif accepted and not approved:
            errors.append(f"{version}: verify_artifact accepted a checkpoint whose release flags are not approved")
        elif not approved:
            print(f"::notice title=Local AI checkpoint not release-approved::{version}: {reason} "
                  f"verify_artifact refuses it as designed ({refusal}).")
        else:
            print(f"{version}: release-approved and verified on the composed checkpoint")
        if version == shipped:
            outputs = {"approved": str(approved and accepted).lower(), "version": version, "composed": str(output),
                       "reason": "" if approved else reason}
    for error in errors:
        print(f"::error title=Local AI artifact::{error}")
    if errors:
        sys.exit(1)
    write_outputs(outputs)


METRICS = ("emitted", "correct_emitted", "precision", "coverage", "unsupported_abstention", "decision_accuracy")


def drift(recorded, reproduced):
    """(task, metric, recorded, reproduced) for every held-out metric that moved."""
    return [(task, name, recorded[task]["held_out"][name], reproduced[task]["held_out"][name])
            for task in TASKS for name in METRICS
            if recorded[task]["held_out"][name] != reproduced[task]["held_out"][name]]


def score_held_out(runtime, data):
    """{task: {"held_out", "conservative_rules"}} for the test partition, on this CPU.

    Mirrors validate_cpu.evaluate_pair for the trained head: the corpus's
    precomputed states, chunks of 4 over the rca then dependency rows, and
    train.metrics with the runtime's calibration.
    """
    import torch
    from laya.common import collate_items
    from laya.train import _forward, encode_item, items_from_rows
    from partitions import read
    from train import metrics, rules

    rows = [row for task in TASKS for row in read(data / f"{task}-test.jsonl")]
    items, skipped = items_from_rows(runtime.tokenizer, rows, runtime.max_len, runtime.head_len)
    if skipped or len(items) != len(rows):
        raise ValueError("held-out rows exceed the model's token or option budget")
    records = []
    with torch.inference_mode():
        for start in range(0, len(items), 4):
            chunk = items[start:start + 4]
            batch = collate_items([[encode_item(runtime.tokenizer, item, runtime.max_len, runtime.head_len)
                                    for item in chunk]], runtime.tokenizer.pad_token_id)
            logits = _forward(runtime.model, batch, torch.device("cpu"), False, False).numpy()
            records.extend((item["qtype"], z[:item["k"]], item["target"], item["k"])
                           for item, z in zip(chunk, logits, strict=True))
            if start % 400 == 0:
                print(f"held-out on {platform_name()}: {min(start + 4, len(items))}/{len(items)}", flush=True)
    reports = {}
    for task in TASKS:
        selected = [(row, record) for row, record in zip(rows, records, strict=True) if row["task"] == task]
        task_rows, task_records = zip(*selected, strict=True)
        reports[task] = {"held_out": metrics(task_rows, task_records, runtime.calibration["temperatures"][task],
                                             runtime.calibration["thresholds"][task]),
                         "conservative_rules": rules(task_rows)}
    return reports


def platform_name():
    import platform
    return f"{platform.system().lower()}/{platform.machine().lower()}"


def summarize(lines):
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a") as out:
            out.write("\n".join(lines) + "\n")


def reproduce(args):
    from partitions import verify_partitions, verify_rendering
    from probara_ai.runtime import Runtime

    # Runtime runs verify_artifact (which also binds calibration.json to the
    # evaluated calibration), the image's CPU preparation (pinned torch,
    # qnnpack, dynamic int8 encoder) and loads calibration.json, exactly as the
    # server does; 4 threads as in validation and the default LOCAL_AI_THREADS.
    runtime = Runtime(args.composed, threads=4)
    version = runtime.manifest["version"]
    verify_partitions(args.data, args.composed)
    verify_rendering(args.data, runtime.state_format, parts=("test",))
    evaluation = json.loads((args.composed / "cpu-evaluation.json").read_text())
    reproduced = score_held_out(runtime, args.data)
    errors = [f"the CPU release gate fails for {task} on {platform_name()}"
              for task in TASKS if not task_passes(reproduced[task])]
    if not errors and release_gate(reproduced) is not True:
        errors.append(f"the CPU release gate fails on {platform_name()}")
    recorded_on = evaluation.get("platform") or {}
    recorded_name = "/".join(str(recorded_on.get(k, "unrecorded")).lower() for k in ("system", "machine"))
    moved = drift(evaluation["tasks"], reproduced)
    lines = [f"### Local AI held-out reproduction on {platform_name()}",
             f"`{version}`, evaluated for approval on {recorded_name}.", "",
             "| task | metric | approval run | this platform |", "|---|---|---|---|"]
    lines += [f"| {task} | {name} | {fmt(evaluation['tasks'][task]['held_out'][name])} | "
              f"{fmt(reproduced[task]['held_out'][name])} |" for task in TASKS for name in METRICS]
    lines += ["", f"{len(moved)} held-out metric(s) moved; the CPU release gate "
              f"{'fails: ' + '; '.join(errors) if errors else 'holds'}."]
    summarize(lines)
    print("\n".join(lines))
    for task, name, before, after in moved:
        print(f"::warning title=Local AI held-out drift::{version} {task} {name}: {before} on {recorded_name}, "
              f"{after} on {platform_name()}")
    for error in errors:
        print(f"::error title=Local AI not reproduced on the image platform::{version}: {error}")
    if errors:
        sys.exit(1)


def fmt(value):
    return f"{value:.4f}" if isinstance(value, float) else str(value)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--artifacts", type=Path, default=ARTIFACTS)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("gate").set_defaults(run=gate)
    verifier = commands.add_parser("verify")
    verifier.add_argument("--base", type=Path, required=True, help="verified pinned base checkpoint directory")
    verifier.add_argument("--work", type=Path, required=True, help="empty directory for the composed checkpoints")
    verifier.set_defaults(run=verify)
    reproducer = commands.add_parser("reproduce")
    reproducer.add_argument("--composed", type=Path, required=True,
                            help="composed, release-approved shipped checkpoint (verify's `composed` output)")
    reproducer.add_argument("--data", type=Path, required=True, help="controlled corpus from training/generate.py")
    reproducer.set_defaults(run=reproduce)
    parsed = parser.parse_args()
    parsed.run(parsed)

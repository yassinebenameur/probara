"""Pinned upstream training interface, group splits and separate calibration.

Writes a head-only candidate (compose_artifact.py) to artifacts/<version>.
Its manifest is never release-approved here: approval comes only from the
separate CPU validation (validate_cpu.py) of the composed checkpoint.
"""
import argparse
import json
import os
from pathlib import Path
import time
os.environ.setdefault("USE_TF", "0")
os.environ.setdefault("HF_HUB_OFFLINE", "1")
os.environ.setdefault("TRANSFORMERS_OFFLINE", "1")
import numpy as np
import torch
from laya.train import TrainConfig, load_checkpoint, items_from_rows, train_model, calibration_records
from laya.calibrate import fit_one_temperature
from probara_ai.contract import STATE_FORMAT, VERSION
from upstream import verify_base, package_notices
from compose_artifact import CONFIG, check_version, prepare_output, record_composed, save_head, save_json, write_manifest
from partitions import read, verify_partitions, verify_rendering

MAX_LEN, HEAD_LEN = 512, 256


def metrics(rows, records, temperature, threshold):
    supported = emitted = correct = abstained_unsupported = unsupported = accuracy = 0
    for row, record in zip(rows, records, strict=True):
        _, logits, target, k = record
        p = torch.softmax(torch.tensor(logits) / temperature, -1).numpy()
        idx = int(np.argmax(p)); gold = int(np.argmax(target))
        keys = list(row["questions"][row["task"]]["criteria"])
        label, prediction = keys[gold], keys[idx]
        is_supported = label != "insufficient_evidence" and (row["task"] == "rca" or label in ("a_depends_b", "b_depends_a"))
        emit = prediction != "insufficient_evidence" and p[idx] >= threshold and (row["task"] == "rca" or prediction in ("a_depends_b", "b_depends_a"))
        # Apply identical explicit evidence guards in evaluation and production.
        e = row["evidence"]
        if row["task"] == "dependency":
            emit &= (e["co_fires"] >= 2 and e["reverse_co_fires"] == 0 and (e["shared_service"] or e["shared_group"]) and e["a_leads_seconds"] != 0 and not e["common_upstream"])
            emit &= (prediction == "a_depends_b" and e["a_leads_seconds"] < 0) or (prediction == "b_depends_a" and e["a_leads_seconds"] > 0)
        else:
            eligible = {"dns_failure": e.get("dns_failures", 0) > 0, "connection_failure": e.get("connection_failures", 0) > 0,
                        "tls_problem": e.get("tls_failures", 0) > 0 or (e.get("certificate_expired", False) and e["failures"] > 0),
                        "protocol_error": e.get("protocol_failures", 0) > 0,
                        "latency_degradation": e.get("slow_checks", 0) > 0 and e.get("baseline_ms", 0) > 0,
                        "resource_pressure": e.get("resource_pressure", False) and e.get("host_metrics_available", False) and e["failures"] > 0,
                        "upstream_failure": e.get("upstream_failed_before", False) and e["failures"] > 0,
                        "maintenance_downtime": e.get("maintenance_overlap", False) and e["failures"] > 0}
            emit &= eligible.get(prediction, False) and not e.get("conflict", False) and not e.get("missing_checks", False)
        supported += is_supported; unsupported += not is_supported
        emitted += bool(emit); correct += bool(emit and idx == gold)
        abstained_unsupported += bool(not is_supported and not emit)
        accuracy += bool((emit and idx == gold) or (not emit and not is_supported))
    return {"n": len(rows), "supported": supported, "emitted": emitted, "correct_emitted": correct,
            "precision": correct / emitted if emitted else 0.0, "coverage": correct / supported if supported else 0.0,
            "unsupported_abstention": abstained_unsupported / unsupported if unsupported else 1.0,
            "decision_accuracy": accuracy / len(rows)}


def rules(rows):
    """Predeclared conservative baseline, independent of gold labels.

    Requires 3 repeated direct symptoms or 5 co-firing windows, rejects
    conflicting domains and weak naming-only evidence. The relaxed evidence
    guards are reported separately as a corpus construction upper bound.
    """
    correct = supported = emitted = accuracy = 0
    for row in rows:
        task = row["task"]; e = row["evidence"]
        label = row["expected"][task]
        prediction = "insufficient_evidence"
        if task == "dependency":
            if e["co_fires"] >= 5 and e["reverse_co_fires"] == 0 and (e["shared_service"] or e["shared_group"]) and e["a_leads_seconds"] != 0 and not e["common_upstream"]:
                prediction = "b_depends_a" if e["a_leads_seconds"] > 0 else "a_depends_b"
        elif not e.get("conflict", False) and not e.get("missing_checks", False) and e["failures"] >= 3:
            for field, hypothesis in (("dns_failures", "dns_failure"), ("connection_failures", "connection_failure"), ("tls_failures", "tls_problem"), ("protocol_failures", "protocol_error"), ("slow_checks", "latency_degradation")):
                if e.get(field, 0) >= 3:
                    prediction = hypothesis
            for field, hypothesis in (("resource_pressure", "resource_pressure"), ("upstream_failed_before", "upstream_failure"), ("maintenance_overlap", "maintenance_downtime")):
                if e.get(field, False):
                    prediction = hypothesis
        is_supported = label != "insufficient_evidence" and (task == "rca" or label in ("a_depends_b", "b_depends_a"))
        emit = prediction != "insufficient_evidence"
        supported += is_supported; emitted += emit; correct += bool(emit and prediction == label)
        accuracy += bool((emit and prediction == label) or (not emit and not is_supported))
    return {"precision": correct / emitted if emitted else 0, "coverage": correct / supported if supported else 0, "decision_accuracy": accuracy / len(rows)}


def run(args):
    check_version(args.version)
    # Every refusal before anything is deleted or trained: record_composed
    # needs a fresh <work>/<version>, and --replace-unapproved deletes the old
    # candidate only once base and corpus are known good.
    composed = args.work / args.version
    if composed.exists():
        raise ValueError(f"Compose directory {composed} already exists; remove it first")
    verify_base(args.base)
    verify_partitions(args.data, args.data, current_generator=True)
    verify_rendering(args.data, STATE_FORMAT)
    prepare_output(args.output, args.version, args.replace_unapproved)
    torch.set_num_threads(4); torch.manual_seed(20261010)
    model, tok, cfg = load_checkpoint(str(args.base))
    device = torch.device(args.device if args.device != "auto" else ("cuda" if torch.cuda.is_available() else "mps" if torch.backends.mps.is_available() else "cpu"))
    # Frozen encoder runs in fp16 on accelerators; trainable head stays fp32,
    # avoiding fp16 Adam underflow. Export and CPU validation use fp32.
    if device.type in ("mps", "cuda"):
        model.encoder.half()
    partitions = {part: sum((read(args.data / f"{task}-{part}.jsonl") for task in ("rca", "dependency")), []) for part in ("train", "calibration", "test")}
    group_sets = {part: {row["group"] for row in rows} for part, rows in partitions.items()}
    assert not (group_sets["train"] & group_sets["calibration"] or group_sets["train"] & group_sets["test"] or group_sets["calibration"] & group_sets["test"])
    prepared = {}
    for part, rows in partitions.items():
        for row in rows:
            if len(tok.encode(row["state"], add_special_tokens=False)) > MAX_LEN - HEAD_LEN:
                raise ValueError("state exceeds token budget")
        items, skipped = items_from_rows(tok, rows, MAX_LEN, HEAD_LEN)
        assert not skipped and len(items) == len(rows), skipped
        prepared[part] = items
    config = TrainConfig(epochs=args.epochs, micro_batch=8, grad_accum=4, head_lr=3e-4, freeze_encoder=True,
                         loss="soft-ce", shuffle_options=("choice",), seed=20261010, max_len=MAX_LEN,
                         head_max_len=HEAD_LEN, log_every=50, amp=False, gradient_checkpointing=False)
    def records(items):
        return calibration_records(model, tok, items, device, MAX_LEN, HEAD_LEN, batch_size=8)
    model.to(device)
    print("Evaluating untuned checkpoint on calibration and held-out data", flush=True)
    base_calibration = records(prepared["calibration"])
    base_test = records(prepared["test"])
    started = time.perf_counter()
    history = train_model(model, tok, prepared["train"], config, device, MAX_LEN, HEAD_LEN)
    print("Calibrating using calibration partition only", flush=True)
    calibration = records(prepared["calibration"])
    test_records = records(prepared["test"])
    reports, temperatures, thresholds = {}, {}, {}
    for task in ("rca", "dependency"):
        def subset(part, records_):
            return [(r, v) for r, v in zip(partitions[part], records_, strict=True) if r["task"] == task]
        cal = subset("calibration", calibration); held = subset("test", test_records)
        temperature = fit_one_temperature([(v[1], v[2]) for _, v in cal], min_n=10)
        temperatures[task] = temperature
        candidates = []
        for threshold in np.linspace(0.01, 0.99, 99):
            m = metrics([r for r, _ in cal], [v for _, v in cal], temperature, float(threshold))
            if m["precision"] >= .95 and m["unsupported_abstention"] >= .90:
                candidates.append((m["coverage"], float(threshold)))
        threshold = max(candidates)[1] if candidates else 1.0
        thresholds[task] = threshold
        held_metrics = metrics([r for r, _ in held], [v for _, v in held], temperature, threshold)
        baseline = rules([r for r, _ in held])
        base_cal = subset("calibration", base_calibration); base_held = subset("test", base_test)
        base_temperature = fit_one_temperature([(v[1], v[2]) for _, v in base_cal], min_n=10)
        reports[task] = {"held_out": held_metrics, "conservative_rules": baseline,
                         "untuned_at_release_threshold": metrics([r for r, _ in base_held], [v for _, v in base_held], base_temperature, threshold),
                         "evidence_guard_rules": {"precision": 1.0, "coverage": 1.0, "decision_accuracy": 1.0},
                         "temperature": temperature, "threshold": threshold}
    # Gate against the predeclared conservative deployed-rule baseline. The
    # construction upper bound is disclosed, never called a learned result.
    gate = all(r["held_out"]["precision"] >= .95 and r["held_out"]["unsupported_abstention"] >= .9 and
               r["held_out"]["coverage"] >= .5 and r["held_out"]["decision_accuracy"] >= r["conservative_rules"]["decision_accuracy"] and
               r["held_out"]["coverage"] > r["conservative_rules"]["coverage"] for r in reports.values())
    cfg.update(max_len=MAX_LEN, head_max_len=HEAD_LEN, model_name=args.version, catalogue=VERSION,
               state_format=STATE_FORMAT)
    model.float().cpu()
    # Only the head is trained; the encoder is the pinned base's, restored by composition.
    save_head(model, args.output)
    save_json(args.output / CONFIG, cfg)
    package_notices(args.base, args.output)
    evaluation = {"release_approved": gate, "scope": "Controlled scenarios only; no real-world accuracy claim.", "tasks": reports,
                  "loss_history": history, "training_seconds": time.perf_counter() - started,
                  "gate_failure": None if gate else "Release gates, including superiority over the predeclared conservative rules, not met."}
    (args.output / "evaluation.json").write_text(json.dumps(evaluation, indent=2) + "\n")
    (args.output / "calibration.json").write_text(json.dumps({"temperatures": temperatures, "thresholds": thresholds}, indent=2) + "\n")
    (args.output / "training-config.json").write_text(json.dumps({**vars(config), "state_format": STATE_FORMAT}, indent=2) + "\n")
    import shutil
    shutil.copy(args.data / "provenance.json", args.output / "provenance.json")
    write_manifest(args.output, args.version, STATE_FORMAT, release_approved=False)
    record_composed(args.output, args.base, composed)
    print(json.dumps(evaluation, indent=2), flush=True)
    return 0 if gate else 2


if __name__ == "__main__":
    p = argparse.ArgumentParser()
    p.add_argument("--version", required=True, help="candidate name, e.g. probara-laya-v3; also the output directory name")
    p.add_argument("--base", type=Path, required=True, help="verified pinned base, e.g. ai-service/.cache/base")
    p.add_argument("--data", type=Path, required=True, help="controlled corpus, e.g. ai-service/artifacts/dataset")
    p.add_argument("--output", type=Path, required=True, help="ai-service/artifacts/<version>")
    p.add_argument("--work", type=Path, required=True, help="compose root, e.g. ai-service/.cache/composed")
    p.add_argument("--replace-unapproved", action="store_true", help="replace a non-empty, unapproved candidate directory")
    p.add_argument("--device", default="auto"); p.add_argument("--epochs", type=int, default=2)
    raise SystemExit(run(p.parse_args()))

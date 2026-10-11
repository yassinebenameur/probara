"""Recalibrate on the calibration partition and gate the exported CPU checkpoint.

Thresholds never use test labels. The untuned checkpoint gets its own
calibration at the same precision as the predeclared deterministic baseline.
The head-only artifact is composed with the pinned base (compose_artifact.py,
checksums verified against its manifest) into <work>/<version>, evaluated
there, and the report, calibration and approval are written back to the
committed artifact directory. A release-approved artifact is immutable.
The evidence rendering is the artifact's own (manifest "state_format",
missing means 1): the corpus must be rendered in it, the report records it,
and validation never changes it.
"""
import argparse
import hashlib
import json
import os
import platform
from pathlib import Path
os.environ.update(HF_HUB_OFFLINE="1", TRANSFORMERS_OFFLINE="1", USE_TF="0", CUDA_VISIBLE_DEVICES="")
import numpy as np
import torch
from laya.train import load_checkpoint, items_from_rows, encode_item, _forward
from laya.common import DecisionModel, collate_items
from safetensors import safe_open
from laya.calibrate import fit_one_temperature
from train import read, metrics, rules, MAX_LEN, HEAD_LEN
from probara_ai.cpu import CPU_FORMAT, prepare_cpu
from upstream import verify_base
from compose_artifact import compose, load_manifest, refresh_manifest
from partitions import verify_partitions, verify_rendering
from probara_ai.contract import STATE_FORMAT, artifact_state_format
from probara_ai.gate import release_gate
from upstream import LOCK

HERE = Path(__file__).resolve().parent
# Code whose behaviour determines the cached scores: item encoding (train.py
# read/MAX_LEN, laya), CPU preparation, the evidence contract and this file.
SCORE_CODE = (HERE / "validate_cpu.py", HERE / "train.py", HERE.parent / "probara_ai/contract.py",
              HERE.parent / "probara_ai/cpu.py")


def file_sha256(path):
    with open(path, "rb") as f:
        return hashlib.file_digest(f, "sha256").hexdigest()


class BatchEncoder(torch.nn.Module):
    """Share one frozen encoder pass between trained and untuned heads only."""
    def __init__(self, encoder):
        super().__init__()
        self.encoder, self.config, self.cached = encoder, encoder.config, None

    def forward(self, **kwargs):
        if self.cached is None:
            self.cached = self.encoder(**kwargs)
        return self.cached


@torch.inference_mode()
def evaluate_pair(artifact, base, data, cache=None, calibration_only=False, state_format=1, cache_parts=None):
    import transformers
    identity = {"state_format": state_format,
                "trained": file_sha256(artifact / 'model.safetensors'),
                "base": file_sha256(base / 'model.safetensors'),
                "cpu_format": CPU_FORMAT, "platform": {"system": platform.system(), "machine": platform.machine()},
                "partitions": json.loads((artifact / 'provenance.json').read_text())['partition_sha256'],
                "laya_commit": LOCK["upstream_commit"], "transformers": transformers.__version__,
                "code": {path.name: file_sha256(path) for path in SCORE_CODE}}
    saved = json.loads(cache.read_text()) if cache and cache.exists() else {"identity": identity, "outputs": [{}, {}]}
    if saved['identity'] != identity:
        raise ValueError('CPU score cache does not match checkpoint, CPU format, dataset, laya, transformers '
                         'or validator code')
    if cache:
        cache.parent.mkdir(parents=True, exist_ok=True)
    # The training interface freezes the encoder. Verify every encoder tensor
    # before sharing its computation; both heads still execute on actual CPU.
    with safe_open(str(artifact / "model.safetensors"), framework="pt") as tuned, safe_open(str(base / "model.safetensors"), framework="pt") as original:
        keys = [k for k in original.keys() if k.startswith("encoder.")]
        if not keys or any(not torch.equal(tuned.get_tensor(k), original.get_tensor(k)) for k in keys):
            raise ValueError("Encoder changed: paired CPU evaluation requires frozen identical weights")
        original_head = {k: original.get_tensor(k).float() for k in original.keys() if not k.startswith("encoder.")}
    model, tok, cfg = load_checkpoint(str(artifact))
    model = prepare_cpu(model)
    model.encoder = BatchEncoder(model.encoder)
    base_cfg = json.loads((base / "rl_agent_config.json").read_text())
    untuned = DecisionModel(model.encoder, head_layers=base_cfg.get("head_layers", 2), n_act=model.act_head[-1].out_features, no_init=True)
    # Load only head modules: quantized encoder loaders require their own
    # packed keys even under strict=False, and must never be reloaded here.
    for name in ("head", "type_emb", "scorer", "act_head"):
        module = getattr(untuned, name)
        if module is not None:
            prefix = name + "."
            module.load_state_dict({k.removeprefix(prefix):v for k,v in original_head.items() if k.startswith(prefix)}, strict=True)
    untuned.temperature.copy_(original_head["temperature"])
    untuned.eval()
    parts = ("calibration",) if calibration_only else ("calibration", "test")
    partitions = {part: sum((read(data / f"{task}-{part}.jsonl") for task in ("rca", "dependency")), []) for part in parts}
    outputs = [{}, {}]
    for part, rows in partitions.items():
        if all(part in output for output in saved['outputs']):
            for output, existing in zip(outputs, saved['outputs'], strict=True):
                records = [(r[0], np.asarray(r[1], dtype=np.float32), np.asarray(r[2], dtype=np.float32), r[3]) for r in existing[part]]
                output[part] = list(zip(rows, records, strict=True))
            print(f'Using verified CPU score cache: {part}', flush=True)
            if cache_parts is not None:
                cache_parts.append(part)
            continue
        items, skipped = items_from_rows(tok, rows, MAX_LEN, HEAD_LEN)
        assert not skipped
        print(f"CPU paired evaluation: {part}, {len(rows)} examples", flush=True)
        records = [[], []]
        for start in range(0, len(items), 4):
            chunk = items[start:start+4]
            batch = collate_items([[encode_item(tok, it, MAX_LEN, HEAD_LEN) for it in chunk]], tok.pad_token_id)
            model.encoder.cached = None
            for index, candidate in enumerate((model, untuned)):
                logits = _forward(candidate, batch, torch.device("cpu"), False, False).numpy()
                records[index].extend((it["qtype"], z[:it["k"]], it["target"], it["k"]) for it, z in zip(chunk, logits, strict=True))
            model.encoder.cached = None
            if start % 200 == 0:
                print(f"CPU {part}: {min(start+4,len(items))}/{len(items)}", flush=True)
        for output, result in zip(outputs, records, strict=True):
            output[part] = list(zip(rows, result, strict=True))
        if cache:
            for output, result in zip(saved['outputs'], records, strict=True):
                output[part] = [[int(r[0]), np.asarray(r[1]).tolist(), np.asarray(r[2]).tolist(), int(r[3])] for r in result]
            cache.write_text(json.dumps(saved) + '\n')
    return outputs


def calibrate(cal):
    rows, records = zip(*cal, strict=True)
    temperature = fit_one_temperature([(v[1], v[2]) for v in records], min_n=10)
    target_precision = max(.95, rules(rows)["precision"])
    candidates = []
    for threshold in np.linspace(.01, .99, 99):
        m = metrics(rows, records, temperature, float(threshold))
        if m["precision"] >= target_precision and m["unsupported_abstention"] >= .9:
            candidates.append((m["coverage"], float(threshold)))
    # Among equal-coverage candidates retain the most conservative threshold.
    threshold = max(candidates)[1] if candidates else 1.
    return temperature, threshold, target_precision


def run(args):
    if load_manifest(args.artifact).get("release_approved") is True:
        raise ValueError("Release-approved artifacts are immutable; validate a new version")
    state_format = artifact_state_format(load_manifest(args.artifact))
    verify_base(args.base)
    # A current-format candidate must also come from the committed generator.
    verify_partitions(args.data, args.artifact, current_generator=state_format == STATE_FORMAT)
    verify_rendering(args.data, state_format, parts=("calibration",) if args.calibration_only else ("calibration", "test"))
    composed_dir = args.work / args.artifact.name
    composed = compose(args.artifact, args.base, composed_dir)
    torch.set_num_threads(4); torch.set_num_interop_threads(1)
    cache_parts = []
    trained, untuned = evaluate_pair(composed_dir, args.base, args.data, args.cache, args.calibration_only, state_format,
                                     cache_parts)
    if args.calibration_only:
        reports = {}
        for task in ('rca', 'dependency'):
            cal = [(row, rec) for row, rec in trained['calibration'] if row['task'] == task]
            temperature, threshold, target = calibrate(cal)
            rows, records = zip(*cal, strict=True)
            reports[task] = {'calibration': metrics(rows, records, temperature, threshold), 'rules': rules(rows),
                             'temperature': temperature, 'threshold': threshold, 'precision_target': target}
        print(json.dumps({'calibration_only': True, 'release_approved': False, 'tasks': reports}, indent=2), flush=True)
        return 0
    reports, temperatures, thresholds = {}, {}, {}
    for task in ("rca", "dependency"):
        select = lambda result, part: [(row, rec) for row, rec in result[part] if row["task"] == task]
        cal, held = select(trained, "calibration"), select(trained, "test")
        temperature, threshold, target = calibrate(cal)
        temperatures[task], thresholds[task] = temperature, threshold
        rows, records = zip(*held, strict=True)
        baseline = rules(rows)
        base_temp, base_threshold, _ = calibrate(select(untuned, "calibration"))
        base_rows, base_records = zip(*select(untuned, "test"), strict=True)
        reports[task] = {"held_out": metrics(rows, records, temperature, threshold), "conservative_rules": baseline,
                         "untuned_matched_precision": metrics(base_rows, base_records, base_temp, base_threshold),
                         "calibration_precision_target": target, "temperature": temperature, "threshold": threshold,
                         "evidence_guard_rules": {"precision": 1., "coverage": 1., "decision_accuracy": 1.}}
    gate = release_gate(reports)
    # Which held-out scores were reused from a cache, and its exact bytes, so
    # the committed report shows whether this run computed them itself.
    score_cache = {"sha256": file_sha256(args.cache), "parts": cache_parts} if cache_parts else None
    previous = json.loads((args.artifact / "evaluation.json").read_text())
    manifest = load_manifest(args.artifact)
    evaluation = {"release_approved": gate, "device": "cpu", "cpu_format": CPU_FORMAT,
                  "version": manifest["version"], "state_format": state_format,
                  "model_sha256": composed["model.safetensors"],
                  "platform": {"system": platform.system(), "machine": platform.machine(), "python": platform.python_version(), "threads": 4},
                  "scope": previous["scope"], "tasks": reports, "score_cache": score_cache,
                  "training_record": previous, "gate_failure": None if gate else "CPU held-out release gates not met."}
    (args.artifact / "cpu-evaluation.json").write_text(json.dumps(evaluation, indent=2) + "\n")
    (args.artifact / "calibration.json").write_text(json.dumps({"temperatures": temperatures, "thresholds": thresholds}, indent=2) + "\n")
    updated = refresh_manifest(args.artifact, release_approved=gate, cpu_format=CPU_FORMAT)
    if artifact_state_format(updated) != state_format:
        raise RuntimeError("Validation must not change the artifact's evidence state format")
    print(json.dumps(evaluation, indent=2), flush=True)
    return 0 if gate else 2


if __name__ == "__main__":
    p = argparse.ArgumentParser()
    p.add_argument("--artifact", type=Path, required=True, help="committed head-only artifact, e.g. ai-service/artifacts/<version>")
    p.add_argument("--base", type=Path, required=True, help="verified pinned base, e.g. ai-service/.cache/base")
    p.add_argument("--data", type=Path, required=True, help="controlled corpus, e.g. ai-service/artifacts/dataset")
    p.add_argument("--work", type=Path, required=True, help="compose root; the checkpoint goes to <work>/<version>, e.g. ai-service/.cache/composed")
    p.add_argument('--cache', type=Path, help="optional CPU score cache, e.g. ai-service/.cache/scores/<version>.json")
    p.add_argument('--calibration-only', action='store_true')
    raise SystemExit(run(p.parse_args()))

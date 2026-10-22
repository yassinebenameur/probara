"""One local Laya model, CPU only, with no downloads or arbitrary model code."""
import hashlib
import json
import os
from pathlib import Path
os.environ["HF_HUB_OFFLINE"] = "1"
os.environ["TRANSFORMERS_OFFLINE"] = "1"
os.environ["USE_TF"] = "0"
os.environ["CUDA_VISIBLE_DEVICES"] = ""
import torch
from laya.train import load_checkpoint, items_from_rows, encode_item, _forward
from laya.common import collate_items, TEMP_MIN, TEMP_MAX
from probara_ai.contract import VERSION, artifact_state_format, options, state_text
from probara_ai.cpu import CPU_FORMAT, prepare_cpu
from probara_ai.gate import TASKS, release_gate

HEAD = "head.safetensors"
# Committed head-only artifact files (artifacts/<version>/manifest.json "files").
REQUIRED_FILES = {HEAD, "rl_agent_config.json", "calibration.json", "evaluation.json", "cpu-evaluation.json",
                  "training-config.json", "provenance.json", "LICENSE", "base-model-card.md", "upstream.json",
                  "THIRD-PARTY-NOTICES"}
# Files training/compose_artifact.py generates from the head and the pinned base ("composed").
REQUIRED_COMPOSED = {"model.safetensors", "encoder/config.json", "tokenizer/tokenizer.json", "tokenizer/tokenizer_config.json"}


def _sha256(file):
    with file.open("rb") as f:
        return hashlib.file_digest(f, "sha256").hexdigest()


def verify_artifact(path):
    """Release gate for a composed checkpoint directory (image build and startup).

    The head weights are bound through the composed model.safetensors checksum,
    so a composed directory does not carry head.safetensors itself, and the
    directory must hold exactly the listed files. The CPU report's approval is
    rechecked against probara_ai.gate.release_gate over its own metrics, its
    version and evidence format must be the manifest's, and calibration.json
    must carry the validated settings. The returned manifest always carries "state_format", the evidence rendering
    the head was trained and validated on (absent in older manifests: 1).
    """
    path = Path(path)
    manifest = json.loads((path / "manifest.json").read_text())
    if manifest.get("release_approved") is not True or manifest.get("catalogue") != VERSION:
        raise ValueError("Checkpoint has not passed Probara release gates")
    files, composed = manifest.get("files", {}), manifest.get("composed", {})
    if (not REQUIRED_FILES <= set(files) or not REQUIRED_COMPOSED <= set(composed) or set(files) & set(composed)
            or manifest.get("head_sha256") != files[HEAD]):
        raise ValueError("Incomplete checkpoint manifest")
    # Exactly the listed files: laya and transformers read any extra file under
    # tokenizer/ or encoder/ (added_tokens.json, special-token overrides), so
    # an unlisted one would change tokenization without changing a checksum.
    expected_files = (set(files) - {HEAD}) | set(composed) | {"manifest.json"}
    present = set()
    for file in path.rglob("*"):
        if file.is_symlink():
            raise ValueError("Invalid artifact path")
        if file.is_file():
            present.add(file.relative_to(path).as_posix())
    if present != expected_files:
        raise ValueError("Checkpoint files differ from the manifest: " + ", ".join(sorted(present ^ expected_files)))
    for relative, expected in [*((k, v) for k, v in files.items() if k != HEAD), *composed.items()]:
        file = path / relative
        if file.is_symlink() or not file.resolve().is_relative_to(path.resolve()):
            raise ValueError("Invalid artifact path")
        if not file.is_file() or _sha256(file) != expected:
            raise ValueError(f"Checkpoint checksum mismatch: {relative}")
    config = json.loads((path / "rl_agent_config.json").read_text())
    if config.get("model_name") != manifest.get("version"):
        raise ValueError("Checkpoint configuration does not match the manifest version")
    state_format = artifact_state_format(manifest)
    if config.get("state_format", state_format) != state_format:
        raise ValueError("Checkpoint configuration and manifest disagree on the evidence state format")
    evaluation = json.loads((path / "cpu-evaluation.json").read_text())
    if evaluation.get("release_approved") is not True or evaluation.get("device") != "cpu":
        raise ValueError("Held-out validation did not pass")
    # Recompute the gate from the reported metrics: the flag alone is not trusted.
    if release_gate(evaluation.get("tasks")) is not True:
        raise ValueError("Held-out validation report does not meet the release gate")
    if evaluation.get("version") != manifest.get("version"):
        raise ValueError("Held-out validation report names a different artifact version")
    if evaluation.get("model_sha256") != composed["model.safetensors"]:
        raise ValueError("Held-out validation evaluated different weights")
    if evaluation.get("cpu_format") != CPU_FORMAT or manifest.get("cpu_format") != CPU_FORMAT:
        raise ValueError("CPU preparation does not match the validated artifact")
    # A manifest that records its format needs a report that records the same
    # one; only a legacy (format 1) manifest may pair with a report without it.
    reported = evaluation.get("state_format", None if "state_format" in manifest else 1)
    if type(reported) is not int or reported != state_format:
        raise ValueError("Held-out validation used a different evidence state format")
    calibration = json.loads((path / "calibration.json").read_text())
    for task in TASKS:
        report = evaluation["tasks"][task]
        if (calibration.get("temperatures", {}).get(task), calibration.get("thresholds", {}).get(task)) != (
                report.get("temperature"), report.get("threshold")):
            raise ValueError("Calibration settings differ from the validated ones")
    return {**manifest, "state_format": state_format}


class Runtime:
    def __init__(self, path, threads=4):
        path = Path(path)
        self.manifest = verify_artifact(path)
        # Render evidence exactly as this head was trained and validated.
        self.state_format = self.manifest["state_format"]
        torch.set_num_threads(threads)
        torch.set_num_interop_threads(1)
        self.model, self.tokenizer, self.config = load_checkpoint(str(path))
        self.model = prepare_cpu(self.model)
        self.max_len = self.config["max_len"]
        self.head_len = self.config["head_max_len"]
        self.calibration = json.loads((path / "calibration.json").read_text())
        for task in ("rca", "dependency"):
            temperature = self.calibration["temperatures"][task]
            threshold = self.calibration["thresholds"][task]
            if not (TEMP_MIN <= temperature <= TEMP_MAX and 0 < threshold <= 1):
                raise ValueError("Invalid calibration settings")
        self.identity = {"engine": "laya-cpu", "version": self.manifest["version"],
                         "sha256": self.manifest["composed"]["model.safetensors"], "catalogue": VERSION}

    def prepare(self, decisions):
        rows = []
        for d in decisions:
            state = state_text(d.task, d.evidence, self.state_format)
            if len(self.tokenizer.encode(state, add_special_tokens=False)) > self.max_len - self.head_len:
                raise ValueError("Evidence exceeds model token budget; split the decision")
            if d.choices != options(d.task):
                raise ValueError("Supplied choices do not match the versioned catalogue")
            # Caller may vary option order; expected is ignored at inference.
            rows.append({"state": state, "questions": {d.task: {"type": "choice", "instructions": "Select the hypothesis supported by the measured evidence; abstain when inconclusive.", "criteria": d.choices}}, "expected": {d.task: next(iter(d.choices))}})
        items, skipped = items_from_rows(self.tokenizer, rows, self.max_len, self.head_len)
        if skipped or len(items) != len(rows):
            raise ValueError("Choices exceed distinct option-marker budget")
        return items

    @torch.inference_mode()
    def predict(self, decisions, items):
        results = []
        for start in range(0, len(items), 4):
            chunk = items[start:start + 4]
            batch = collate_items([[encode_item(self.tokenizer, item, self.max_len, self.head_len) for item in chunk]], self.tokenizer.pad_token_id)
            logits = _forward(self.model, batch, torch.device("cpu"), False, False)
            for decision, item, z in zip(decisions[start:start + 4], chunk, logits, strict=True):
                p = torch.softmax(z[:item["k"]] / self.calibration["temperatures"][decision.task], -1)
                if not torch.isfinite(p).all():
                    raise RuntimeError("Nonfinite model probabilities")
                results.append({"id": decision.id, "probabilities": dict(zip(decision.choices, p.tolist(), strict=True))})
        return {"model": self.identity, "thresholds": self.calibration["thresholds"], "results": results}

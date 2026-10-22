"""Convert a full-checkpoint candidate into the committed head-only layout.

Input: a directory as an earlier train_head_cpu.py writes it -- laya's
save_checkpoint layout (model.safetensors, encoder/, tokenizer/,
rl_agent_config.json) plus LICENSE, base-model-card.md, upstream.json,
provenance.json, evaluation.json, training-config.json, manifest.json and
per-epoch head-epoch-N.safetensors recovery files (dropped).

Output: artifacts/<version> exactly as training/train_head_cpu.py writes it
now: head.safetensors (the candidate's own fp16 non-encoder tensors), the
metadata files, THIRD-PARTY-NOTICES (from the repository when the candidate
predates it), and manifest.json (compose_artifact.write_manifest), with the
composition recorded in <work>/<version>.

Nothing is trusted on faith. The candidate's manifest checksums must hold,
it must name the pinned base and never claim approval, every encoder tensor
must equal the pinned base bit for bit, composing the head-only artifact must
reproduce the candidate's model.safetensors byte for byte, and its encoder
config and tokenizer must equal what composition generates from the base.
Any failure removes the partial output.

    PYTHONPATH=ai-service:ai-service/training python ai-service/tools/convert_candidate.py \\
      --candidate CANDIDATE_DIR --base BASE_DIR \\
      --output ai-service/artifacts/<version> --work ai-service/.cache/composed
"""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import sys

ROOT = Path(__file__).resolve().parents[1]
for _path in (ROOT, ROOT / "training"):
    if str(_path) not in sys.path:
        sys.path.insert(0, str(_path))
os.environ.update(HF_HUB_OFFLINE="1", TRANSFORMERS_OFFLINE="1", USE_TF="0", CUDA_VISIBLE_DEVICES="")

from compose_artifact import (CONFIG, HEAD, MANIFEST, check_version, prepare_output, record_composed,  # noqa: E402
                              sha256, tree_hashes, verify_committed, load_manifest, write_manifest)
from upstream import LOCK, verify_base  # noqa: E402

WEIGHTS = "model.safetensors"
# Committed next to head.safetensors, copied unchanged.
METADATA = (CONFIG, "LICENSE", "base-model-card.md", "upstream.json", "provenance.json",
            "evaluation.json", "training-config.json")
# Regenerated from the pinned base by composition, never committed.
GENERATED_DIRS = ("encoder/", "tokenizer/")
RECOVERY = re.compile(r"^head-epoch-\d+\.safetensors$")
# Third-party attribution (mmBERT, tokenizer origin). Candidates written before
# it existed lack it, so it is taken from the repository; one that has it must
# carry the repository's text.
NOTICES = "THIRD-PARTY-NOTICES"
# Per-call settings transformers leaves on a fast tokenizer's backend after
# encoding with truncation or padding; they are not part of the vocabulary and
# every runtime call sets its own.
TOKENIZER_CALL_STATE = ("truncation", "padding")


def _inventory(candidate):
    files = tree_hashes(candidate)  # also refuses symlinks
    generated = {name for name in files if name == WEIGHTS or name.startswith(GENERATED_DIRS)}
    recovery = {name for name in files if RECOVERY.fullmatch(name)}
    unknown = set(files) - generated - recovery - set(METADATA) - {MANIFEST, NOTICES}
    if unknown:
        raise ValueError("Unexpected files in candidate: " + ", ".join(sorted(unknown)))
    missing = ({WEIGHTS, MANIFEST, *METADATA} - set(files)) | (
        set() if any(n.startswith("encoder/") for n in files) else {"encoder/config.json"}) | (
        set() if any(n.startswith("tokenizer/") for n in files) else {"tokenizer/tokenizer.json"})
    if missing:
        raise ValueError("Candidate is missing: " + ", ".join(sorted(missing)))
    return files, generated


def _check_candidate(candidate, files, version, lock):
    """Candidate metadata: integrity, identity, pinned base, never approved."""
    from probara_ai.contract import VERSION, artifact_state_format
    from probara_ai.cpu import CPU_FORMAT
    manifest = load_manifest(candidate)
    config = json.loads((candidate / CONFIG).read_text())
    version = version or config.get("model_name")
    check_version(version)
    if config.get("model_name") != version or manifest.get("version") != version:
        raise ValueError("Candidate model_name and manifest version must equal --version")
    if manifest.get("release_approved") is not False:
        raise ValueError("Candidate claims approval; approval only comes from validate_cpu.py on the head-only artifact")
    if manifest.get("catalogue") != VERSION or config.get("catalogue", VERSION) != VERSION:
        raise ValueError("Candidate was trained on a different catalogue than shared/ai/catalogue.json")
    state_format = artifact_state_format(manifest)
    if config.get("state_format", state_format) != state_format:
        raise ValueError("Candidate configuration and manifest disagree on the evidence state format")
    base = manifest.get("base", {})
    if base.get("repo") != lock["repo"] or base.get("revision") != lock["revision"] or \
            base.get("model_sha256", lock["files"][WEIGHTS]) != lock["files"][WEIGHTS] or \
            manifest.get("upstream_commit") != lock["upstream_commit"]:
        raise ValueError("Candidate was trained on a different base than training/base-lock.json")
    if manifest.get("cpu_format", CPU_FORMAT) != CPU_FORMAT:
        raise ValueError("Candidate was trained on a different CPU feature format")
    training = json.loads((candidate / "training-config.json").read_text())
    if training.get("frozen_encoder_cpu_format", CPU_FORMAT) != CPU_FORMAT or \
            training.get("state_format", state_format) != state_format:
        raise ValueError("Candidate training-config.json disagrees with its manifest")
    listed = manifest.get("files", {})
    if not {WEIGHTS, *METADATA} <= set(listed):
        raise ValueError("Candidate manifest does not cover its weights and metadata")
    for name, digest in listed.items():
        if files.get(name) != digest:
            raise ValueError(f"Candidate checksum mismatch: {name}")
    unlisted = {n for n in files if n != MANIFEST and not RECOVERY.fullmatch(n)} - set(listed)
    if unlisted:
        raise ValueError("Candidate files not covered by its manifest: " + ", ".join(sorted(unlisted)))
    # Attribution must describe the pinned base, as package_notices writes it.
    if files["LICENSE"] != sha256(ROOT / "UPSTREAM-LICENSE") or files["base-model-card.md"] != lock["files"]["README.md"]:
        raise ValueError("Candidate license or base model card is not the pinned base's")
    if NOTICES in files and files[NOTICES] != sha256(ROOT / NOTICES):
        raise ValueError("Candidate THIRD-PARTY-NOTICES differs from the repository's")
    notice = json.loads((candidate / "upstream.json").read_text())
    if (notice.get("repo"), notice.get("revision"), notice.get("weight_sha256"), notice.get("training_interface_commit")) != (
            lock["repo"], lock["revision"], lock["files"][WEIGHTS], lock["upstream_commit"]):
        raise ValueError("Candidate upstream.json does not name the pinned base")
    return version, state_format


def _split_weights(candidate, base):
    """Return the candidate's head tensors after proving its encoder is the base's."""
    import torch
    from safetensors import safe_open
    head = {}
    with safe_open(str(candidate / WEIGHTS), framework="pt") as tuned, \
            safe_open(str(base / WEIGHTS), framework="pt") as original:
        if set(tuned.keys()) != set(original.keys()):
            raise ValueError("Candidate weights do not have the base checkpoint layout")
        encoder = [key for key in original.keys() if key.startswith("encoder.")]
        if not encoder:
            raise ValueError("Base checkpoint has no encoder tensors")
        for key in sorted(original.keys()):
            value = tuned.get_tensor(key)
            if key.startswith("encoder."):
                expected = original.get_tensor(key)
                if value.dtype != expected.dtype or value.shape != expected.shape or not torch.equal(value, expected):
                    raise ValueError(f"Encoder tensor differs from the pinned base: {key}")
            else:
                if value.dtype != torch.float16:
                    raise ValueError(f"Head tensor is not stored in fp16: {key}")
                head[key] = value.contiguous()
    if not head:
        raise ValueError("Candidate has no decision-head tensors")
    return head


def _same_generated(candidate_file, composed_file):
    if sha256(candidate_file) == sha256(composed_file):
        return True
    try:
        left, right = json.loads(candidate_file.read_text()), json.loads(composed_file.read_text())
    except (UnicodeDecodeError, json.JSONDecodeError):
        return False
    if candidate_file.name == "tokenizer.json" and isinstance(left, dict) and isinstance(right, dict):
        for key in TOKENIZER_CALL_STATE:
            left.pop(key, None)
            right.pop(key, None)
    return left == right


def convert(candidate, output, base, work, version=None, lock=LOCK):
    candidate, output, base, work = Path(candidate), Path(output), Path(base), Path(work)
    files, generated = _inventory(candidate)
    version, state_format = _check_candidate(candidate, files, version, lock)
    composed = work / version
    if composed.exists():
        raise ValueError(f"Compose directory {composed} already exists; remove it first")
    if output.exists() and any(output.iterdir()):
        raise ValueError("Output directory must be new or empty")
    verify_base(base, lock)
    head = _split_weights(candidate, base)
    from probara_ai.cpu import CPU_FORMAT
    from safetensors.torch import save_file
    prepare_output(output, version)
    try:
        save_file(head, str(output / HEAD))
        (output / HEAD).chmod(0o644)
        for name in METADATA:
            shutil.copyfile(candidate / name, output / name)
            (output / name).chmod(0o644)
        shutil.copyfile(ROOT / NOTICES, output / NOTICES)
        (output / NOTICES).chmod(0o644)
        write_manifest(output, version, state_format, release_approved=False, cpu_format=CPU_FORMAT, lock=lock)
        record_composed(output, base, composed, expect_model_sha256=files[WEIGHTS], lock=lock)
        produced = set(tree_hashes(composed)) - set(tree_hashes(output))
        expected = generated - {WEIGHTS}
        if {n for n in produced if n.startswith(GENERATED_DIRS)} != expected:
            raise ValueError("Composition generates different encoder/tokenizer files than the candidate has")
        for name in sorted(expected):
            if not _same_generated(candidate / name, composed / name):
                raise ValueError(f"Candidate {name} differs from the pinned base's")
        manifest = load_manifest(output)
        verify_committed(output, manifest, lock)
        return manifest
    except BaseException:
        shutil.rmtree(output, ignore_errors=True)
        shutil.rmtree(composed, ignore_errors=True)
        raise


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--candidate", type=Path, required=True, help="full-checkpoint candidate directory (read only)")
    parser.add_argument("--base", type=Path, required=True, help="verified pinned base, e.g. ai-service/.cache/base")
    parser.add_argument("--output", type=Path, required=True, help="new, empty ai-service/artifacts/<version>")
    parser.add_argument("--work", type=Path, required=True, help="compose root, e.g. ai-service/.cache/composed")
    parser.add_argument("--version", help="expected candidate version (default: its rl_agent_config model_name)")
    args = parser.parse_args()
    result = convert(args.candidate, args.output, args.base, args.work, args.version)
    print(json.dumps({key: result[key] for key in ("version", "state_format", "head_sha256", "composed", "release_approved")},
                     indent=2))

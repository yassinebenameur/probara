"""Head-only artifacts: write, record and compose them with the pinned base.

Training freezes the encoder, so a candidate's only trained weights are the
decision head. A committed artifact directory (artifacts/<version>/) holds
head.safetensors -- exactly the fp16 tensors the full model.safetensors
contains -- plus its metadata and manifest.json. Tokenizer and encoder come
from the verified pinned base (training/base-lock.json).

compose() rebuilds the full laya checkpoint deterministically: load the base,
overlay the head, save through laya's save_checkpoint (fp16), then copy the
committed metadata over it. The manifest's `composed` map records the sha256
of every generated file; compose refuses any result that differs, so the
image, the CPU validator and the runtime all see the exact evaluated weights.

Manifest fields: version, catalogue, state_format (the evidence rendering the
head was trained on; absent in older manifests, meaning 1), release_approved
and cpu_format (approval only from validate_cpu.py), base {repo, revision,
model_sha256}, upstream_commit, head_sha256, composed {generated file: sha256},
files {committed file: sha256}. version, state_format, base, upstream_commit
and head_sha256 are fixed when a candidate is written; changing any of them is
a new artifact version.

    compose_artifact.py verify  --artifact DIR           committed integrity (no base needed)
    compose_artifact.py record  --artifact DIR --base B --output OUT
    compose_artifact.py compose --artifact DIR --base B --output OUT
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil

from upstream import LOCK, verify_base

HEAD = "head.safetensors"
MANIFEST = "manifest.json"
CONFIG = "rl_agent_config.json"
VERSION_PATTERN = re.compile(r"^probara-laya-[a-z0-9][a-z0-9.-]{0,62}$")
IMMUTABLE = ("version", "catalogue", "state_format", "base", "upstream_commit", "head_sha256")


def sha256(path):
    with open(path, "rb") as f:
        return hashlib.file_digest(f, "sha256").hexdigest()


def tree_hashes(root, exclude=()):
    root = Path(root)
    hashes = {}
    for path in sorted(root.rglob("*")):
        relative = path.relative_to(root).as_posix()
        if path.is_symlink():
            raise ValueError(f"Symlink in artifact: {relative}")
        if path.is_file() and relative not in exclude:
            hashes[relative] = sha256(path)
    return hashes


def load_manifest(artifact):
    return json.loads((Path(artifact) / MANIFEST).read_text())


def save_json(path, value):
    Path(path).write_text(json.dumps(value, indent=2) + "\n")


def check_version(version):
    if not VERSION_PATTERN.fullmatch(version or ""):
        raise ValueError("--version must look like probara-laya-<name> (lowercase, digits, '.', '-')")
    return version


def prepare_output(output, version, replace_unapproved=False):
    """Fresh candidate directory named after its version; approved ones are immutable."""
    output = Path(output)
    check_version(version)
    if output.name != version:
        raise ValueError("Artifact directory name must equal --version")
    if output.exists() and any(output.iterdir()):
        manifest = output / MANIFEST
        if manifest.exists() and json.loads(manifest.read_text()).get("release_approved") is True:
            raise ValueError("Refusing to overwrite a release-approved artifact; train a new --version")
        if not replace_unapproved:
            raise ValueError("Output directory is not empty; pass --replace-unapproved to replace an unapproved candidate")
        shutil.rmtree(output)
    output.mkdir(parents=True, exist_ok=True)
    return output


def extract_head(model):
    """Non-encoder tensors exactly as laya's save_checkpoint stores them (fp16)."""
    return {key: value.detach().half().cpu().contiguous()
            for key, value in model.state_dict().items() if not key.startswith("encoder.")}


def save_head(model, artifact):
    from safetensors.torch import save_file
    path = Path(artifact) / HEAD
    save_file(extract_head(model), str(path))
    path.chmod(0o644)
    return path


def write_manifest(artifact, version, state_format, release_approved=False, cpu_format=None, composed=None,
                   extra=None, lock=LOCK):
    """Record every committed file; never approves on its own (validate_cpu.py does).

    state_format is required: it is the rendering of the corpus the head was
    trained on (probara_ai.contract.STATE_FORMAT for a freshly generated one).
    """
    artifact = Path(artifact)
    check_version(version)
    files = tree_hashes(artifact, exclude={MANIFEST})
    if HEAD not in files or CONFIG not in files:
        raise ValueError("Artifact needs head.safetensors and rl_agent_config.json")
    config = json.loads((artifact / CONFIG).read_text())
    if config.get("model_name") != version:
        raise ValueError("rl_agent_config.json model_name must equal the artifact version")
    from probara_ai.contract import VERSION, artifact_state_format
    artifact_state_format({"state_format": state_format})
    if config.get("state_format", state_format) != state_format:
        raise ValueError("rl_agent_config.json state_format differs from the manifest")
    manifest = {"version": version, "catalogue": VERSION, "state_format": state_format,
                "release_approved": release_approved is True,
                "base": {"repo": lock["repo"], "revision": lock["revision"],
                         "model_sha256": lock["files"]["model.safetensors"]},
                "upstream_commit": lock["upstream_commit"], "head_sha256": files[HEAD]}
    if cpu_format is not None:
        manifest["cpu_format"] = cpu_format
    if composed is not None:
        manifest["composed"] = composed
    if set(extra or {}) & {*manifest, "composed", "cpu_format", "files"}:
        raise ValueError("extra manifest fields must not override recorded ones")
    manifest.update(extra or {})
    manifest["files"] = files
    save_json(artifact / MANIFEST, manifest)
    return manifest


def refresh_manifest(artifact, **changes):
    """Rehash committed files after metadata changes, keeping composed checksums."""
    artifact = Path(artifact)
    manifest = load_manifest(artifact)
    for key in IMMUTABLE:
        if key in changes and changes[key] != manifest.get(key):
            raise ValueError(f"Manifest {key} is fixed for an artifact version; write a new version instead")
    manifest.update(changes)
    manifest["files"] = tree_hashes(artifact, exclude={MANIFEST})
    if manifest["files"].get(HEAD) != manifest.get("head_sha256"):
        raise ValueError("Head weights changed; a new head needs a new artifact version")
    save_json(artifact / MANIFEST, manifest)
    return manifest


def verify_committed(artifact, manifest, lock=LOCK):
    artifact = Path(artifact)
    from probara_ai.contract import artifact_state_format
    check_version(manifest.get("version"))
    artifact_state_format(manifest)
    base = manifest.get("base", {})
    if (base.get("repo"), base.get("revision"), base.get("model_sha256")) != (
            lock["repo"], lock["revision"], lock["files"]["model.safetensors"]):
        raise ValueError("Artifact was trained on a different base than training/base-lock.json")
    files = manifest.get("files", {})
    if HEAD not in files or CONFIG not in files or files[HEAD] != manifest.get("head_sha256"):
        raise ValueError("Incomplete head-only artifact manifest")
    for relative, digest in files.items():
        path = artifact / relative
        if path.is_symlink() or not path.resolve().is_relative_to(artifact.resolve()):
            raise ValueError("Invalid artifact path")
        if not path.is_file() or sha256(path) != digest:
            raise ValueError(f"Artifact checksum mismatch: {relative}")
    present = set(tree_hashes(artifact, exclude={MANIFEST}))
    if present != set(files):
        raise ValueError("Files not covered by the manifest: " + ", ".join(sorted(present ^ set(files))))
    config = json.loads((artifact / CONFIG).read_text())
    if config.get("model_name") != manifest["version"]:
        raise ValueError("rl_agent_config.json model_name must equal the artifact version")
    if config.get("state_format", artifact_state_format(manifest)) != artifact_state_format(manifest):
        raise ValueError("rl_agent_config.json state_format differs from the manifest")


def _copy_metadata(artifact, manifest, output):
    for relative in manifest["files"]:
        if relative != HEAD:
            (output / relative).parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(artifact / relative, output / relative)
    shutil.copyfile(artifact / MANIFEST, output / MANIFEST)
    for path in [output, *output.rglob("*")]:
        path.chmod(0o755 if path.is_dir() else 0o644)


def _generated(output, manifest):
    return tree_hashes(output, exclude={MANIFEST, *manifest["files"]})


def compose(artifact, base, output, verify=True, lock=LOCK):
    """Compose artifact + base into output; returns generated file checksums.

    verify=True (always, except when first recording a new candidate) requires
    the manifest's `composed` checksums to match. An existing output is reused
    only when its generated files verify; its metadata is then refreshed.
    """
    artifact, base, output = Path(artifact), Path(base), Path(output)
    manifest = load_manifest(artifact)
    verify_committed(artifact, manifest, lock)
    recorded = manifest.get("composed")
    if verify and not recorded:
        raise ValueError("Manifest has no composed checksums; record the candidate first")
    if output.exists():
        if not verify:
            raise ValueError("Recording a candidate requires a fresh compose directory")
        if _generated(output, manifest) != recorded:
            raise ValueError(f"Stale compose directory {output}; delete it and compose again")
        _copy_metadata(artifact, manifest, output)
        return recorded
    verify_base(base, lock)
    from safetensors.torch import load_file
    from laya.train import load_checkpoint, save_checkpoint
    model, tokenizer, _ = load_checkpoint(str(base))
    head = load_file(str(artifact / HEAD))
    expected = {key for key in model.state_dict() if not key.startswith("encoder.")}
    if set(head) != expected:
        raise ValueError("Head weights do not match the base decision-head layout")
    missing, unexpected = model.load_state_dict(head, strict=False)
    if unexpected or any(not key.startswith("encoder.") for key in missing):
        raise ValueError("Head weights did not load")
    model.float().cpu().eval()
    partial = output.with_name("." + output.name + ".partial")
    shutil.rmtree(partial, ignore_errors=True)
    partial.parent.mkdir(parents=True, exist_ok=True)
    save_checkpoint(model, tokenizer, json.loads((artifact / CONFIG).read_text()), str(partial))
    _copy_metadata(artifact, manifest, partial)
    generated = _generated(partial, manifest)
    if "model.safetensors" not in generated:
        raise ValueError("Composition produced no model weights")
    if verify and generated != recorded:
        differing = sorted(set(generated) ^ set(recorded) | {k for k in generated if generated[k] != recorded.get(k)})
        shutil.rmtree(partial)
        raise ValueError("Composed checkpoint differs from the manifest: " + ", ".join(differing))
    os.replace(partial, output)
    return generated


def record_composed(artifact, base, output, expect_model_sha256=None, lock=LOCK):
    """Compose a new candidate once and record what it generates."""
    manifest = load_manifest(artifact)
    if manifest.get("release_approved") is True:
        raise ValueError("Release-approved artifacts are immutable")
    generated = compose(artifact, base, output, verify=False, lock=lock)
    if expect_model_sha256 and generated["model.safetensors"] != expect_model_sha256:
        shutil.rmtree(output)
        raise ValueError("Composed weights differ from the expected checksum")
    refresh_manifest(artifact, composed=generated)
    _copy_metadata(Path(artifact), load_manifest(artifact), Path(output))
    return generated


if __name__ == "__main__":
    os.environ.update(HF_HUB_OFFLINE="1", TRANSFORMERS_OFFLINE="1", USE_TF="0", CUDA_VISIBLE_DEVICES="")
    parser = argparse.ArgumentParser(description="Compose a head-only artifact with the pinned base.")
    parser.add_argument("command", choices=("compose", "record", "verify"),
                        help="compose: build and verify against the manifest; record: first composition of an "
                             "unapproved candidate; verify: committed files only (no base, no approval check)")
    parser.add_argument("--artifact", type=Path, required=True)
    parser.add_argument("--base", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--expect-model-sha256", help="record only: required composed model.safetensors checksum")
    args = parser.parse_args()
    if args.command == "verify":
        manifest = load_manifest(args.artifact)
        verify_committed(args.artifact, manifest)
        if manifest["version"] != args.artifact.resolve().name:
            raise SystemExit("Artifact directory name must equal the manifest version")
        if not manifest.get("composed"):
            raise SystemExit("Manifest has no composed checksums; run `record` with the pinned base")
        print(json.dumps({"version": manifest["version"], "state_format": manifest.get("state_format", 1),
                          "release_approved": manifest.get("release_approved") is True}))
        raise SystemExit(0)
    if args.base is None or args.output is None:
        parser.error("--base and --output are required for compose and record")
    if args.command == "compose":
        result = compose(args.artifact, args.base, args.output)
    else:
        result = record_composed(args.artifact, args.base, args.output, args.expect_model_sha256)
    print(json.dumps({"output": str(args.output), "composed": result}, indent=2))

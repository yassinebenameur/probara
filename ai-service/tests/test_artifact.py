"""Head-only artifacts: deterministic composition, checksum binding, release gate.

A tiny laya checkpoint stands in for the pinned base so these run in seconds;
PROBARA_LAYA_BASE (the real verified base) additionally reproduces v1.
"""
import hashlib
import json
import os
from pathlib import Path
import pytest

torch = pytest.importorskip("torch")
pytest.importorskip("laya")
from safetensors.torch import load_file, save_file  # noqa: E402
from compose_artifact import (HEAD, compose, load_manifest, prepare_output, record_composed,  # noqa: E402
                              refresh_manifest, save_head, save_json, write_manifest)
from probara_ai.contract import STATE_FORMAT  # noqa: E402
from probara_ai.cpu import CPU_FORMAT  # noqa: E402
from probara_ai.runtime import verify_artifact  # noqa: E402
from approval import write_approval  # noqa: E402

ARTIFACTS = Path(__file__).resolve().parents[1] / "artifacts"
METADATA = ("calibration.json", "evaluation.json", "training-config.json", "provenance.json", "upstream.json")


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def make_candidate(root, base, version="probara-laya-fixture", seed=11, state_format=STATE_FORMAT):
    """Mirror train.py's export: head + metadata, manifest, recorded composition."""
    from laya.train import load_checkpoint
    base_dir, lock = base
    artifact = prepare_output(root / version, version)
    model, _, cfg = load_checkpoint(str(base_dir))
    generator = torch.Generator().manual_seed(seed)
    with torch.no_grad():
        for name, parameter in model.named_parameters():
            if not name.startswith("encoder."):
                parameter.add_(torch.randn(parameter.shape, generator=generator) * 0.01)
    save_head(model, artifact)
    save_json(artifact / "rl_agent_config.json", {**cfg, "model_name": version})
    for name in METADATA:
        save_json(artifact / name, {"fixture": name})
    (artifact / "LICENSE").write_text("Apache-2.0 fixture\n")
    (artifact / "base-model-card.md").write_text("fixture card\n")
    (artifact / "THIRD-PARTY-NOTICES").write_text("fixture notices\n")
    write_manifest(artifact, version, state_format, lock=lock)
    record_composed(artifact, base_dir, root / "work" / version, lock=lock)
    return artifact


def approve(artifact):
    """Simulate a passing validate_cpu.py run (tests only; never for real artifacts)."""
    manifest = load_manifest(artifact)
    write_approval(artifact, manifest["version"], manifest["composed"]["model.safetensors"], CPU_FORMAT,
                   manifest["state_format"])
    refresh_manifest(artifact, release_approved=True, cpu_format=CPU_FORMAT)


def test_composition_is_deterministic_and_recorded(tmp_path, base):
    base_dir, lock = base
    artifact = make_candidate(tmp_path, base)
    manifest = load_manifest(artifact)
    assert set(manifest["files"]) == {HEAD, "rl_agent_config.json", "LICENSE", "base-model-card.md",
                                      "THIRD-PARTY-NOTICES", *METADATA}
    assert {"model.safetensors", "encoder/config.json", "tokenizer/tokenizer.json"} <= set(manifest["composed"])
    assert manifest["head_sha256"] == sha(artifact / HEAD) and manifest["release_approved"] is False
    first = compose(artifact, base_dir, tmp_path / "a", lock=lock)
    second = compose(artifact, base_dir, tmp_path / "elsewhere" / "b", lock=lock)
    assert first == second == manifest["composed"]
    assert (tmp_path / "a/model.safetensors").read_bytes() == (tmp_path / "elsewhere/b/model.safetensors").read_bytes()
    assert not (tmp_path / "a" / HEAD).exists()
    composed = load_file(str(tmp_path / "a/model.safetensors"))
    for key, value in load_file(str(artifact / HEAD)).items():
        assert torch.equal(composed[key], value)
    original = load_file(str(base_dir / "model.safetensors"))
    assert all(torch.equal(composed[k], v) for k, v in original.items() if k.startswith("encoder."))


def test_compose_rejects_tampering_and_foreign_bases(tmp_path, base):
    base_dir, lock = base
    artifact = make_candidate(tmp_path, base)
    with pytest.raises(ValueError, match="different base"):
        compose(artifact, base_dir, tmp_path / "x", lock={**lock, "revision": "f" * 40})
    head = load_file(str(artifact / HEAD))
    first = next(iter(head))
    head[first] = head[first] + 1
    save_file(head, str(artifact / HEAD))
    with pytest.raises(ValueError, match="checksum mismatch: head.safetensors"):
        compose(artifact, base_dir, tmp_path / "x", lock=lock)
    # Even with a consistently rewritten manifest, the recorded composition fails.
    manifest = load_manifest(artifact)
    manifest["head_sha256"] = manifest["files"][HEAD] = sha(artifact / HEAD)
    save_json(artifact / "manifest.json", manifest)
    with pytest.raises(ValueError, match="differs from the manifest: model.safetensors"):
        compose(artifact, base_dir, tmp_path / "x", lock=lock)
    assert not (tmp_path / "x").exists()


def test_stale_compose_directory_is_refused(tmp_path, base):
    base_dir, lock = base
    artifact = make_candidate(tmp_path, base)
    out = tmp_path / "out"
    compose(artifact, base_dir, out, lock=lock)
    save_json(artifact / "calibration.json", {"temperatures": {}, "thresholds": {}})
    refresh_manifest(artifact)
    compose(artifact, base_dir, out, lock=lock)  # generated files verify; metadata refreshed
    assert (out / "calibration.json").read_bytes() == (artifact / "calibration.json").read_bytes()
    (out / "tokenizer/tokenizer.json").write_text("{}")
    with pytest.raises(ValueError, match="Stale compose directory"):
        compose(artifact, base_dir, out, lock=lock)


def test_release_gate_on_composed_checkpoint(tmp_path, base):
    base_dir, lock = base
    artifact = make_candidate(tmp_path, base)
    save_json(artifact / "cpu-evaluation.json", {"release_approved": False, "device": "cpu", "cpu_format": CPU_FORMAT})
    refresh_manifest(artifact, cpu_format=CPU_FORMAT)
    compose(artifact, base_dir, tmp_path / "unapproved", lock=lock)
    with pytest.raises(ValueError, match="release gates"):
        verify_artifact(tmp_path / "unapproved")
    approve(artifact)
    out = tmp_path / "approved"
    compose(artifact, base_dir, out, lock=lock)
    manifest = verify_artifact(out)
    assert manifest["composed"]["model.safetensors"] == sha(out / "model.safetensors")
    # Approved artifacts are immutable for training and recording.
    with pytest.raises(ValueError, match="release-approved"):
        prepare_output(artifact, artifact.name, replace_unapproved=True)
    with pytest.raises(ValueError, match="immutable"):
        record_composed(artifact, base_dir, tmp_path / "again", lock=lock)
    for relative, content in (("calibration.json", "{}"), ("model.safetensors", "x"), ("tokenizer/tokenizer.json", "{}")):
        original = (out / relative).read_bytes()
        (out / relative).write_text(content)
        with pytest.raises(ValueError, match="checksum mismatch: " + relative):
            verify_artifact(out)
        (out / relative).write_bytes(original)
    verify_artifact(out)
    # A passing report must name the exact composed weights it evaluated.
    report = json.loads((out / "cpu-evaluation.json").read_text())
    report["model_sha256"] = "0" * 64
    (out / "cpu-evaluation.json").write_text(json.dumps(report))
    manifest = load_manifest(out)
    manifest["files"]["cpu-evaluation.json"] = sha(out / "cpu-evaluation.json")
    save_json(out / "manifest.json", manifest)
    with pytest.raises(ValueError, match="evaluated different weights"):
        verify_artifact(out)


def test_candidate_output_rules(tmp_path):
    with pytest.raises(ValueError, match="--version"):
        prepare_output(tmp_path / "v1", "v1")
    with pytest.raises(ValueError, match="must equal --version"):
        prepare_output(tmp_path / "probara-laya-a", "probara-laya-b")
    out = prepare_output(tmp_path / "probara-laya-a", "probara-laya-a")
    (out / "stale.json").write_text("{}")
    with pytest.raises(ValueError, match="not empty"):
        prepare_output(out, "probara-laya-a")
    prepare_output(out, "probara-laya-a", replace_unapproved=True)
    assert not (out / "stale.json").exists()


@pytest.mark.skipif(not os.environ.get("PROBARA_LAYA_BASE"), reason="set PROBARA_LAYA_BASE to the verified pinned base")
def test_v1_reproduces_its_evaluated_weights(tmp_path):
    artifact = ARTIFACTS / "probara-laya-v1"
    generated = compose(artifact, Path(os.environ["PROBARA_LAYA_BASE"]), tmp_path / "probara-laya-v1")
    # The rejected predecessor's full checkpoint, as CPU-evaluated before the head-only layout.
    assert generated["model.safetensors"] == "b8ca791b7e57671d01ee86f4cbc832493846557f43bba3a2aef0c4abab0ecb49"
    with pytest.raises(ValueError, match="release gates"):
        verify_artifact(tmp_path / "probara-laya-v1")

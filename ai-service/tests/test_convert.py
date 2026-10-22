"""tools/convert_candidate.py: full-checkpoint candidate -> committed head-only layout.

The synthetic candidate mirrors exactly what the v3 train_head_cpu.py writes
(laya save_checkpoint + notices + manifest + head-epoch recovery files).
"""
import hashlib
import json
import shutil
import sys
from pathlib import Path
import pytest

torch = pytest.importorskip("torch")
pytest.importorskip("laya")
from safetensors.torch import load_file, save_file  # noqa: E402
from compose_artifact import HEAD, compose, load_manifest, verify_committed  # noqa: E402
from probara_ai.contract import STATE_FORMAT, VERSION  # noqa: E402
from probara_ai.cpu import CPU_FORMAT  # noqa: E402

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "tools"))
from convert_candidate import convert  # noqa: E402

VERSION_NAME = "probara-laya-fixture3"


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def full_candidate(root, base, version=VERSION_NAME, seed=5):
    from laya.train import load_checkpoint, save_checkpoint
    base_dir, lock = base
    out = root / "candidate" / version
    model, tok, cfg = load_checkpoint(str(base_dir))
    generator = torch.Generator().manual_seed(seed)
    with torch.no_grad():
        for name, parameter in model.named_parameters():
            if not name.startswith("encoder."):
                parameter.add_(torch.randn(parameter.shape, generator=generator) * 0.01)
    # Leave per-call truncation state on the tokenizer backend, as training does.
    tok("w1 w2 w3", truncation=True, max_length=4)
    cfg.update(max_len=64, head_max_len=32, model_name=version, catalogue=VERSION, state_format=STATE_FORMAT,
               training={"initialised_from": "base head"})
    save_checkpoint(model, tok, cfg, str(out))
    save_file({k: v.detach().contiguous() for k, v in model.state_dict().items() if not k.startswith("encoder.")},
              str(out / "head-epoch-1.safetensors"))
    shutil.copy(ROOT / "UPSTREAM-LICENSE", out / "LICENSE")
    shutil.copy(base_dir / "README.md", out / "base-model-card.md")
    (out / "upstream.json").write_text(json.dumps({
        "repo": lock["repo"], "revision": lock["revision"], "weight_sha256": lock["files"]["model.safetensors"],
        "training_interface_commit": lock["upstream_commit"], "license": "Apache-2.0", "modified": "fixture"}, indent=2) + "\n")
    (out / "provenance.json").write_text(json.dumps({"corpus_version": "probara-controlled-v3"}) + "\n")
    (out / "evaluation.json").write_text(json.dumps({"release_approved": False, "scope": "fixture"}) + "\n")
    (out / "training-config.json").write_text(json.dumps({"frozen_encoder_cpu_format": CPU_FORMAT,
                                                          "state_format": STATE_FORMAT}) + "\n")
    write_full_manifest(out, version, lock)
    return out


def write_full_manifest(out, version, lock, **changes):
    manifest = {"version": version, "catalogue": VERSION, "state_format": STATE_FORMAT,
                "base": {"repo": lock["repo"], "revision": lock["revision"]}, "upstream_commit": lock["upstream_commit"],
                "release_approved": False, "cpu_format": CPU_FORMAT, "files": {}, **changes}
    for path in out.rglob("*"):
        if path.is_file() and path.name != "manifest.json" and not path.name.startswith("head-epoch-"):
            manifest["files"][str(path.relative_to(out))] = sha(path)
    (out / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")


def test_round_trip_reproduces_the_candidate(tmp_path, base):
    base_dir, lock = base
    candidate = full_candidate(tmp_path, base)
    before = {p: sha(p) for p in candidate.rglob("*") if p.is_file()}
    output = tmp_path / "artifacts" / VERSION_NAME
    manifest = convert(candidate, output, base_dir, tmp_path / "work", lock=lock)
    assert {p: sha(p) for p in candidate.rglob("*") if p.is_file()} == before, "candidate must stay untouched"
    present = {p.relative_to(output).as_posix() for p in output.rglob("*") if p.is_file()}
    assert present == {HEAD, "manifest.json", "rl_agent_config.json", "LICENSE", "base-model-card.md", "upstream.json",
                       "provenance.json", "evaluation.json", "training-config.json", "THIRD-PARTY-NOTICES"}
    assert (output / "THIRD-PARTY-NOTICES").read_bytes() == (ROOT / "THIRD-PARTY-NOTICES").read_bytes()
    assert manifest["version"] == VERSION_NAME and manifest["catalogue"] == VERSION
    assert manifest["state_format"] == STATE_FORMAT and manifest["release_approved"] is False
    assert manifest["cpu_format"] == CPU_FORMAT and manifest["upstream_commit"] == lock["upstream_commit"]
    assert manifest["base"] == {"repo": lock["repo"], "revision": lock["revision"],
                                "model_sha256": lock["files"]["model.safetensors"]}
    assert manifest["head_sha256"] == sha(output / HEAD) == manifest["files"][HEAD]
    # The head is the candidate's own fp16 non-encoder tensors, nothing else.
    full = load_file(str(candidate / "model.safetensors"))
    head = load_file(str(output / HEAD))
    assert set(head) == {k for k in full if not k.startswith("encoder.")}
    assert all(torch.equal(head[k], full[k]) and head[k].dtype == torch.float16 for k in head)
    # Composition reproduces the evaluated full checkpoint byte for byte.
    assert manifest["composed"]["model.safetensors"] == sha(candidate / "model.safetensors")
    verify_committed(output, manifest, lock)
    again = compose(output, base_dir, tmp_path / "elsewhere" / VERSION_NAME, lock=lock)
    assert again == manifest["composed"]


def test_refuses_a_modified_encoder(tmp_path, base):
    base_dir, lock = base
    candidate = full_candidate(tmp_path, base)
    weights = load_file(str(candidate / "model.safetensors"))
    key = next(k for k in weights if k.startswith("encoder."))
    weights[key] = weights[key] + 1
    save_file(weights, str(candidate / "model.safetensors"))
    write_full_manifest(candidate, VERSION_NAME, lock)
    output = tmp_path / "artifacts" / VERSION_NAME
    with pytest.raises(ValueError, match="Encoder tensor differs from the pinned base"):
        convert(candidate, output, base_dir, tmp_path / "work", lock=lock)
    assert not output.exists() and not (tmp_path / "work" / VERSION_NAME).exists()


@pytest.mark.parametrize("tamper, message", [
    ("approved", "claims approval"),
    ("foreign_base", "different base"),
    ("unknown_format", "unknown evidence state format"),
    ("checksum", "checksum mismatch: evaluation.json"),
    ("extra_file", "Unexpected files"),
    ("wrong_version", "must equal --version"),
    ("foreign_notices", "THIRD-PARTY-NOTICES differs"),
])
def test_refuses_untrustworthy_candidates(tmp_path, base, tamper, message):
    base_dir, lock = base
    candidate = full_candidate(tmp_path, base)
    if tamper == "approved":
        write_full_manifest(candidate, VERSION_NAME, lock, release_approved=True)
    elif tamper == "foreign_base":
        write_full_manifest(candidate, VERSION_NAME, lock, base={"repo": lock["repo"], "revision": "f" * 40})
    elif tamper == "unknown_format":
        write_full_manifest(candidate, VERSION_NAME, lock, state_format=9)
    elif tamper == "checksum":
        (candidate / "evaluation.json").write_text("{}\n")
    elif tamper == "extra_file":
        (candidate / "notes.txt").write_text("x")
    elif tamper == "foreign_notices":
        (candidate / "THIRD-PARTY-NOTICES").write_text("no attribution\n")
        write_full_manifest(candidate, VERSION_NAME, lock)
    version = "probara-laya-other" if tamper == "wrong_version" else None
    output = tmp_path / "artifacts" / VERSION_NAME
    with pytest.raises(ValueError, match=message):
        convert(candidate, output, base_dir, tmp_path / "work", version=version, lock=lock)
    assert not output.exists()


def test_refuses_non_empty_output(tmp_path, base):
    base_dir, lock = base
    candidate = full_candidate(tmp_path, base)
    output = tmp_path / "artifacts" / VERSION_NAME
    output.mkdir(parents=True)
    (output / "manifest.json").write_text("{}")
    with pytest.raises(ValueError, match="new or empty"):
        convert(candidate, output, base_dir, tmp_path / "work", lock=lock)
    assert (output / "manifest.json").exists()


def test_converted_artifact_validates_its_manifest(tmp_path, base):
    base_dir, lock = base
    output = tmp_path / "artifacts" / VERSION_NAME
    convert(full_candidate(tmp_path, base), output, base_dir, tmp_path / "work", lock=lock)
    composed = tmp_path / "work" / VERSION_NAME
    assert load_manifest(composed)["state_format"] == STATE_FORMAT
    assert json.loads((composed / "rl_agent_config.json").read_text())["state_format"] == STATE_FORMAT

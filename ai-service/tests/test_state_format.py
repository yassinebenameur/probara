"""Versioned evidence rendering: artifacts render exactly the format they were trained on."""
import hashlib
import json
from pathlib import Path
from types import SimpleNamespace
import pytest
import probara_ai.runtime as runtime_module
from compose_artifact import load_manifest, refresh_manifest, save_json, verify_committed, write_manifest
from partitions import verify_rendering
from probara_ai.contract import (STATE_FORMAT, STATE_FORMATS, VERSION, artifact_state_format, options, state_text,
                                 training_row)
from probara_ai.cpu import CPU_FORMAT
from probara_ai.runtime import HEAD, REQUIRED_COMPOSED, REQUIRED_FILES, Runtime, verify_artifact
from approval import write_approval

ROOT = Path(__file__).resolve().parents[1]
PAIR = {"co_fires": 5, "a_leads_seconds": -20, "reverse_co_fires": 0, "shared_service": True,
        "shared_group": False, "naming_signal": False, "common_upstream": False}
RCA = {"checks": 12, "failures": 4, "dns_failures": 4, "max_dns_ms": 900}


def test_current_format_is_two_and_both_formats_render():
    assert STATE_FORMAT == 2 and STATE_FORMATS == {1, 2}
    # Format 1 is frozen: probara-laya-v1 was trained and validated on this text.
    assert state_text("dependency", PAIR, 1) == ("Co-firing windows: 5. B precedes A by 20 seconds. "
                                                 "Shared service tag: observed.")
    assert state_text("rca", RCA, 1) == ("Checks: 12. Failed observations: 4. dns failures: 4. "
                                         "max dns ms: 900.")
    assert state_text("dependency", PAIR, 2) == ("Repeated B-before-A failures in 5 co-firing windows; "
                                                 "B fails first by 20 seconds. Corroborating service "
                                                 "evidence: shared service tag.")
    assert state_text("rca", RCA, 2) == ("Checks: 12. Failed observations: 4. Measured DNS resolution "
                                         "failures: 4. Slowest phases: DNS 900 ms.")
    assert state_text("rca", RCA) == state_text("rca", RCA, STATE_FORMAT)
    row = training_row("rca", RCA, "dns_failure", "rca-1")
    assert row["state"] == state_text("rca", RCA, STATE_FORMAT)


@pytest.mark.parametrize("state_format", [0, 3, "2", 2.0, True, None])
def test_unknown_formats_are_refused(state_format):
    with pytest.raises(ValueError, match="Unknown evidence state format"):
        state_text("rca", RCA, state_format)
    with pytest.raises(ValueError, match="unknown evidence state format"):
        artifact_state_format({"state_format": state_format})


@pytest.mark.parametrize("state_format", sorted(STATE_FORMATS))
def test_every_format_validates_evidence(state_format):
    with pytest.raises(ValueError, match="allowlist"):
        state_text("rca", {**RCA, "hostname": 1}, state_format)
    with pytest.raises(ValueError, match="check counts"):
        state_text("rca", {"checks": 1, "failures": 2}, state_format)


def test_missing_manifest_format_means_one():
    assert artifact_state_format({}) == 1
    assert artifact_state_format({"state_format": 2}) == 2
    assert artifact_state_format(load_manifest(ROOT / "artifacts/probara-laya-v1")) == 1


@pytest.mark.parametrize("state_format", sorted(STATE_FORMATS))
def test_runtime_renders_with_the_artifact_format(monkeypatch, state_format):
    captured = []
    monkeypatch.setattr(runtime_module, "items_from_rows", lambda tok, rows, *a: (captured.extend(rows) or rows, []))
    runtime = Runtime.__new__(Runtime)
    runtime.state_format, runtime.max_len, runtime.head_len = state_format, 512, 256
    runtime.tokenizer = SimpleNamespace(encode=lambda text, **kw: text.split())
    decisions = [SimpleNamespace(task="rca", evidence=RCA, choices=options("rca")),
                 SimpleNamespace(task="dependency", evidence=PAIR, choices=options("dependency"))]
    runtime.prepare(decisions)
    assert [row["state"] for row in captured] == [state_text("rca", RCA, state_format),
                                                  state_text("dependency", PAIR, state_format)]


def composed_fixture(path, manifest_format=None, config_format=None, report_format=None):
    """A composed checkpoint directory that passes verify_artifact apart from the formats."""
    for name in REQUIRED_FILES - {HEAD} | REQUIRED_COMPOSED:
        (path / name).parent.mkdir(parents=True, exist_ok=True)
        (path / name).write_text("{}")
    config = {"model_name": "probara-laya-fixture"}
    if config_format is not None:
        config["state_format"] = config_format
    (path / "rl_agent_config.json").write_text(json.dumps(config))
    digest = lambda name: hashlib.sha256((path / name).read_bytes()).hexdigest()  # noqa: E731
    composed = {name: digest(name) for name in REQUIRED_COMPOSED}
    write_approval(path, "probara-laya-fixture", composed["model.safetensors"], CPU_FORMAT, report_format)
    files = {name: digest(name) for name in REQUIRED_FILES - {HEAD}}
    files[HEAD] = "a" * 64
    manifest = {"version": "probara-laya-fixture", "release_approved": True, "catalogue": VERSION,
                "cpu_format": CPU_FORMAT, "head_sha256": "a" * 64, "files": files, "composed": composed}
    if manifest_format is not None:
        manifest["state_format"] = manifest_format
    (path / "manifest.json").write_text(json.dumps(manifest))
    return path


def test_verify_artifact_records_and_checks_the_format(tmp_path):
    assert verify_artifact(composed_fixture(tmp_path / "legacy"))["state_format"] == 1
    assert verify_artifact(composed_fixture(tmp_path / "v2", 2, 2, 2))["state_format"] == 2
    with pytest.raises(ValueError, match="unknown evidence state format"):
        verify_artifact(composed_fixture(tmp_path / "future", 3))
    with pytest.raises(ValueError, match="disagree on the evidence state format"):
        verify_artifact(composed_fixture(tmp_path / "config", 2, 1))
    with pytest.raises(ValueError, match="different evidence state format"):
        verify_artifact(composed_fixture(tmp_path / "report", 2, 2, 1))


def head_only_fixture(path, version="probara-laya-fixture"):
    path.mkdir(parents=True)
    (path / HEAD).write_bytes(b"head")
    save_json(path / "rl_agent_config.json", {"model_name": version, "state_format": 2})
    return path


def test_manifest_records_a_fixed_state_format(tmp_path):
    artifact = head_only_fixture(tmp_path / "probara-laya-fixture")
    with pytest.raises(ValueError, match="state_format differs"):
        write_manifest(artifact, "probara-laya-fixture", 1)
    with pytest.raises(ValueError, match="unknown evidence state format"):
        write_manifest(artifact, "probara-laya-fixture", 9)
    manifest = write_manifest(artifact, "probara-laya-fixture", 2, cpu_format=CPU_FORMAT)
    assert {key: manifest[key] for key in ("version", "catalogue", "state_format", "release_approved", "cpu_format")} == {
        "version": "probara-laya-fixture", "catalogue": VERSION, "state_format": 2, "release_approved": False,
        "cpu_format": CPU_FORMAT}
    assert set(manifest["base"]) == {"repo", "revision", "model_sha256"}
    assert manifest["head_sha256"] == manifest["files"][HEAD] and "upstream_commit" in manifest
    verify_committed(artifact, manifest)
    for key, value in (("state_format", 1), ("version", "probara-laya-other"), ("head_sha256", "0" * 64)):
        with pytest.raises(ValueError, match="fixed for an artifact version"):
            refresh_manifest(artifact, **{key: value})
    assert refresh_manifest(artifact, release_approved=False, state_format=2)["state_format"] == 2
    manifest["state_format"] = 7
    with pytest.raises(ValueError, match="unknown evidence state format"):
        verify_committed(artifact, manifest)
    (artifact / "stray.json").write_text("{}")
    with pytest.raises(ValueError, match="not covered by the manifest"):
        verify_committed(artifact, load_manifest(artifact))


def test_committed_corpus_is_rendered_in_the_current_format():
    data = ROOT / "artifacts/dataset"
    assert json.loads((data / "provenance.json").read_text())["corpus_version"] == "probara-controlled-v3"
    verify_rendering(data, STATE_FORMAT)
    with pytest.raises(ValueError, match="not rendered in evidence state format 1"):
        verify_rendering(data, 1, parts=("test",))

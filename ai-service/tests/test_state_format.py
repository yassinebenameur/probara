"""Versioned evidence rendering: artifacts render exactly the format they were trained on."""
import hashlib
import json
import os
from pathlib import Path
from types import SimpleNamespace
import pytest
import probara_ai.runtime as runtime_module
from compose_artifact import load_manifest, refresh_manifest, save_json, verify_committed, write_manifest
from partitions import verify_rendering
from probara_ai.contract import (BOOL_FIELDS, PAIR_FIELDS, STATE_FORMAT, STATE_FORMATS, VERSION, artifact_state_format,
                                 options, state_text, training_row)
from probara_ai.cpu import CPU_FORMAT
from probara_ai.runtime import HEAD, REQUIRED_COMPOSED, REQUIRED_FILES, Runtime, verify_artifact
from approval import write_approval

ROOT = Path(__file__).resolve().parents[1]
PAIR = {"co_fires": 5, "a_leads_seconds": -20, "reverse_co_fires": 0, "shared_service": True,
        "shared_group": False, "naming_signal": False, "common_upstream": False}
# Every catalogue v2 pair field set: a service and its backing resource.
FULL_PAIR = {**PAIR, "naming_signal": True, "a_windows": 9, "b_windows": 14, "window_monitors": 2,
             "shared_tag_monitors": 3, "shared_group_monitors": 40, "shared_name_prefix": 2, "a_extends_b": False,
             "b_extends_a": False, "same_type": False, "a_infra": False, "b_infra": True, "peers": False}
# The production false positive: same-role peers restarted one after another.
PEERS = {"co_fires": 2, "a_leads_seconds": 195, "reverse_co_fires": 0, "shared_service": True, "shared_group": False,
         "naming_signal": True, "common_upstream": False, "a_windows": 8, "b_windows": 9, "window_monitors": 14,
         "shared_tag_monitors": 24, "shared_group_monitors": 0, "shared_name_prefix": 1, "a_extends_b": False,
         "b_extends_a": False, "same_type": True, "a_infra": False, "b_infra": False, "peers": True}
RCA = {"checks": 12, "failures": 4, "dns_failures": 4, "max_dns_ms": 900}


def test_format_three_dependency_goldens():
    assert state_text("dependency", FULL_PAIR, 3) == (
        "Repeated B-before-A failures in 5 co-firing windows; B fails first by 20 seconds. "
        "Few monitors failing in the same windows: a median of 2 monitors alert in each co-firing window. "
        "Alert windows in the lookback: A 9, B 14. "
        "Rare corroborating service evidence: a shared tag held by 3 monitors. "
        "Large group: a shared group of 40 monitors. "
        "Shared name prefix: the names share their first 2 tokens. "
        "B is a backing resource and A a service.")
    assert state_text("dependency", PEERS, 3) == (
        "Repeated A-before-B failures in 2 co-firing windows; A fails first by 195 seconds. "
        "Many monitors failing in the same windows: a median of 14 monitors alert in each co-firing window. "
        "Alert windows in the lookback: A 8, B 9. "
        "Common tag: a shared tag held by 24 monitors. "
        "The names share no rare multi-token prefix. "
        "No rare corroborating service evidence or name prefix. "
        "A and B are same-role peers: same monitor type and identical tags or numbered names.")
    # A seven-field legacy pair renders only what it measured.
    assert state_text("dependency", PAIR, 3) == (
        "Repeated B-before-A failures in 5 co-firing windows; B fails first by 20 seconds. "
        "Shared service tag. No rare corroborating service evidence or name prefix.")
    variant = {"co_fires": 1, "a_leads_seconds": 0, "reverse_co_fires": 3, "shared_group": True,
               "common_upstream": True, "a_extends_b": True, "a_infra": True, "b_infra": True}
    assert state_text("dependency", variant, 3) == (
        "1 co-firing windows with no clear time direction. Missing history: only 1 co-firing windows. "
        "Conflicting direction: 3 reverse-direction windows. Shared group. "
        "No rare corroborating service evidence or name prefix. "
        "A is a variant of B: its name extends B's name. A and B are both backing resources. "
        "Known common upstream evidence explains both failures.")
    # Formats 1 and 2 ignore the v2 fields: their text is frozen.
    for state_format in (1, 2):
        legacy = {key: FULL_PAIR[key] for key in PAIR}
        assert state_text("dependency", FULL_PAIR, state_format) == state_text("dependency", legacy, state_format)


@pytest.mark.parametrize("evidence", [RCA, {"checks": 0, "failures": 0, "missing_checks": True},
                                      {"checks": 9, "failures": 3, "slow_checks": 3, "baseline_ms": 40,
                                       "max_latency_ms": 900, "resource_pressure": True,
                                       "host_metrics_available": True, "certificate_changed": True}])
def test_format_three_rca_is_format_two(evidence):
    # Only the dependency task changed in format 3; RCA delegates to format 2.
    assert state_text("rca", evidence, 3) == state_text("rca", evidence, 2)


def test_format_three_worst_case_fits_the_runtime_token_budget():
    """Every sentence at its longest with the largest values validation allows.

    Runtime.prepare refuses a state longer than max_len - head_len tokens
    (512 - 256 for every artifact), so the longest possible pair must fit.
    """
    import itertools
    pytest.importorskip("laya")
    # The verified pinned base (CI and docs/local-ai.md put it in these places).
    base = Path(os.environ.get("PROBARA_LAYA_BASE") or ROOT / ".cache/base")
    if not (base / "tokenizer/tokenizer.json").is_file():
        pytest.skip(f"the real base tokenizer is not at {base}/tokenizer; set PROBARA_LAYA_BASE to the verified "
                    "pinned base to check the format-3 token budget")
    tokenizer_dir = base / "tokenizer"
    from transformers import AutoTokenizer
    from train import HEAD_LEN, MAX_LEN
    tokenizer = AutoTokenizer.from_pretrained(str(tokenizer_dir))
    big = 100000000
    longest = 0
    # Mutually exclusive branches: few vs many co-fires, direction, crowd,
    # rare vs common populations, prefix length, roles.
    for co, lead, crowd, tag, group, prefix, roles in itertools.product(
            (1, big), (big, -big, 0), (3, big), (10, big), (10, big), (big, 1, 0),
            ((True, True), (True, False), (False, True))):
        evidence = {key: True for key in BOOL_FIELDS & PAIR_FIELDS} | {key: big for key in PAIR_FIELDS - BOOL_FIELDS}
        evidence.update(co_fires=co, a_leads_seconds=lead, window_monitors=crowd, shared_tag_monitors=tag,
                        shared_group_monitors=group, shared_name_prefix=prefix, a_infra=roles[0], b_infra=roles[1])
        text = state_text("dependency", evidence, 3)
        longest = max(longest, len(tokenizer.encode(text, add_special_tokens=False)))
    assert longest <= MAX_LEN - HEAD_LEN, longest


def test_current_format_is_three_and_every_format_renders():
    assert STATE_FORMAT == 3 and STATE_FORMATS == {1, 2, 3}
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


@pytest.mark.parametrize("state_format", [0, 4, "3", 3.0, True, None])
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
        verify_artifact(composed_fixture(tmp_path / "future", 4))
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


def test_generated_corpus_is_rendered_in_the_current_format():
    # artifacts/dataset is gitignored: CI generates it before pytest.
    data = ROOT / "artifacts/dataset"
    if not data.exists(): pytest.skip("Regenerate controlled dataset first")
    assert json.loads((data / "provenance.json").read_text())["corpus_version"] == "probara-controlled-v4"
    verify_rendering(data, STATE_FORMAT)
    for older in (1, 2):
        with pytest.raises(ValueError, match=f"not rendered in evidence state format {older}"):
            verify_rendering(data, older, parts=("test",))

"""Tests for the CI local AI helper (build-test.yml runs them with ai-service/tests)."""
import json
from pathlib import Path
import sys
import types

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import local_ai_artifact as ci  # noqa: E402

ROOT = Path(__file__).resolve().parents[3]
ARTIFACTS = ROOT / "ai-service" / "artifacts"
COMMITTED = sorted(p for p in ARTIFACTS.glob("probara-laya-*") if (p / "cpu-evaluation.json").is_file())


def passing():
    held = {"n": 1000, "supported": 600, "emitted": 500, "correct_emitted": 500, "precision": 1.0,
            "coverage": 0.83, "unsupported_abstention": 1.0, "decision_accuracy": 0.9}
    rules = {"precision": 1.0, "coverage": 0.75, "decision_accuracy": 0.85}
    return {task: {"held_out": dict(held), "conservative_rules": dict(rules), "temperature": 1.5, "threshold": 0.5}
            for task in ci.TASKS}


def failing():
    tasks = passing()
    tasks["dependency"]["held_out"]["coverage"] = 0.05
    return tasks


def write_artifact(root, manifest_approved, evaluation):
    root.mkdir()
    (root / "manifest.json").write_text(json.dumps({"release_approved": manifest_approved}))
    if evaluation is not None:
        (root / "cpu-evaluation.json").write_text(json.dumps(evaluation))
    return root


@pytest.mark.parametrize("manifest,evaluation,approved", [
    (True, {"release_approved": True, "device": "cpu", "tasks": passing()}, True),
    (False, {"release_approved": True, "device": "cpu", "tasks": passing()}, False),
    (True, {"release_approved": False, "device": "cpu", "tasks": failing(),
            "gate_failure": "CPU held-out release gates not met."}, False),
    (True, {"release_approved": True, "device": "cuda", "tasks": passing()}, False),
    # Truthy is not approved: only the JSON literal true counts.
    (True, {"release_approved": "true", "device": "cpu", "tasks": passing()}, False),
    # Flags the reported metrics do not support: verify_artifact would refuse
    # it in the image, so the release must not go ahead either.
    (True, {"release_approved": True, "device": "cpu", "tasks": failing()}, False),
    (True, {"release_approved": True, "device": "cpu"}, False),
    (True, None, False),
])
def test_approval_needs_both_flags_on_cpu_and_metrics_that_meet_the_gate(tmp_path, manifest, evaluation, approved):
    result, reason = ci.approval(write_artifact(tmp_path / "a", manifest, evaluation))
    assert result is approved
    assert reason


def test_approval_explains_flags_without_supporting_metrics(tmp_path):
    artifact = write_artifact(tmp_path / "a", True, {"release_approved": True, "device": "cpu", "tasks": failing()})
    assert "do not meet the release gate" in ci.approval(artifact)[1]


@pytest.mark.parametrize("artifact", COMMITTED, ids=lambda p: p.name)
def test_committed_reports_are_consistent_with_their_flags(artifact):
    evaluation = json.loads((artifact / "cpu-evaluation.json").read_text())
    manifest = json.loads((artifact / "manifest.json").read_text())
    assert ci.approval(artifact)[0] is (evaluation["release_approved"] is True and manifest["release_approved"] is True)


def test_committed_artifacts_exist():
    assert COMMITTED, "no committed artifact with a cpu-evaluation.json"


def test_gate_reports_the_shipped_checkpoint(tmp_path, monkeypatch):
    monkeypatch.chdir(ROOT)
    output = tmp_path / "out"
    monkeypatch.setenv("GITHUB_OUTPUT", str(output))
    ci.gate(types.SimpleNamespace(artifacts=ARTIFACTS))
    values = dict(line.split("=", 1) for line in output.read_text().splitlines())
    assert values["version"] == ci.shipped_version()
    assert values["approved"] == str(ci.approval(ARTIFACTS / values["version"])[0]).lower()
    assert (values["reason"] == "") is (values["approved"] == "true")


def test_drift_lists_only_moved_metrics():
    recorded, reproduced = passing(), passing()
    assert ci.drift(recorded, reproduced) == []
    reproduced["dependency"]["held_out"]["emitted"] = 499
    reproduced["dependency"]["held_out"]["precision"] = 0.998
    assert ci.drift(recorded, reproduced) == [("dependency", "emitted", 500, 499),
                                              ("dependency", "precision", 1.0, 0.998)]


def test_reproduce_imports_resolve():
    # reproduce runs only once a checkpoint is approved; catch a renamed
    # upstream or training helper before that.
    pytest.importorskip("torch")
    pytest.importorskip("laya")
    from laya.common import collate_items  # noqa: F401
    from laya.train import _forward, encode_item, items_from_rows  # noqa: F401
    from partitions import read, verify_partitions, verify_rendering  # noqa: F401
    from probara_ai.runtime import Runtime  # noqa: F401
    from train import metrics, rules  # noqa: F401


@pytest.fixture
def reproduction(tmp_path, monkeypatch):
    """reproduce() over a fake composed checkpoint; call it with the re-scored metrics."""
    composed = tmp_path / "composed"
    composed.mkdir()
    (composed / "cpu-evaluation.json").write_text(json.dumps(
        {"release_approved": True, "device": "cpu", "platform": {"system": "Darwin", "machine": "arm64"},
         "tasks": passing()}))

    class Runtime:
        def __init__(self, path, threads):
            assert path == composed and threads == 4
            self.manifest, self.state_format = {"version": "probara-laya-test"}, 2

    seen = {}
    monkeypatch.setitem(sys.modules, "probara_ai.runtime", types.SimpleNamespace(Runtime=Runtime))
    monkeypatch.setitem(sys.modules, "partitions", types.SimpleNamespace(
        verify_partitions=lambda data, artifact: seen.setdefault("partitions", (data, artifact)),
        verify_rendering=lambda data, state_format, parts: seen.setdefault("rendering", (state_format, parts))))
    monkeypatch.setenv("GITHUB_STEP_SUMMARY", str(tmp_path / "summary.md"))

    def run(reproduced):
        monkeypatch.setattr(ci, "score_held_out", lambda runtime, data: reproduced)
        ci.reproduce(types.SimpleNamespace(composed=composed, data=tmp_path / "data"))
        assert seen == {"partitions": (tmp_path / "data", composed), "rendering": (2, ("test",))}
        return (tmp_path / "summary.md").read_text()

    return run


def test_reproduce_passes_when_the_gate_holds_despite_drift(reproduction, capsys):
    reproduced = passing()
    reproduced["rca"]["held_out"]["emitted"] = 498
    summary = reproduction(reproduced)
    assert "the CPU release gate holds" in summary and "darwin/arm64" in summary
    assert "::warning title=Local AI held-out drift::probara-laya-test rca emitted: 500" in capsys.readouterr().out


@pytest.mark.parametrize("field,value", [("precision", 0.94), ("coverage", 0.75), ("decision_accuracy", 0.8)])
def test_reproduce_fails_when_the_gate_breaks_on_this_platform(reproduction, capsys, field, value):
    reproduced = passing()
    reproduced["rca"]["held_out"][field] = value
    with pytest.raises(SystemExit) as exit_info:
        reproduction(reproduced)
    assert exit_info.value.code == 1
    assert "the CPU release gate fails for rca on" in capsys.readouterr().out


LOCK = {"repo": "upstream/base", "revision": "a" * 40, "upstream_commit": "c" * 40,
        "files": {"model.safetensors": "0" * 64, "tokenizer/tokenizer.json": "1" * 64}}


@pytest.fixture
def source_check(tmp_path, monkeypatch):
    """sources() with git and the Hub faked; returns the calls each saw."""
    lock = tmp_path / "base-lock.json"
    lock.write_text(json.dumps(LOCK))
    seen = {}
    for name in ("LAYA_GIT_URL", "HF_ENDPOINT", "BASE_MODEL_REPO", "BASE_MODEL_REVISION"):
        monkeypatch.delenv(name, raising=False)
    monkeypatch.chdir(ROOT)

    def run(commit_error=None, sha="a" * 40, files=frozenset(LOCK["files"])):
        def fake_commit(url, commit):
            seen["git"] = (url, commit)
            return commit_error

        def fake_files(endpoint, repo, revision):
            seen["hub"] = (endpoint, repo, revision)
            if isinstance(files, Exception):
                raise files
            return sha, set(files)

        monkeypatch.setattr(ci, "commit_error", fake_commit)
        monkeypatch.setattr(ci, "revision_files", fake_files)
        ci.sources(types.SimpleNamespace(lock=lock))
        return seen

    return run


def test_sources_checks_the_pins_at_the_dockerfile_defaults(source_check, capsys):
    seen = source_check()
    assert seen == {"git": ("https://github.com/NandhaKishorM/laya.git", "c" * 40),
                    "hub": ("https://huggingface.co", "upstream/base", "a" * 40)}
    assert "::error" not in capsys.readouterr().out


def test_sources_follows_mirror_overrides(source_check, monkeypatch):
    monkeypatch.setenv("LAYA_GIT_URL", "https://git.example/laya.git")
    monkeypatch.setenv("HF_ENDPOINT", "https://hf.example")
    monkeypatch.setenv("BASE_MODEL_REPO", "mirror/base")
    monkeypatch.setenv("BASE_MODEL_REVISION", "b" * 40)
    seen = source_check(sha="b" * 40)
    assert seen == {"git": ("https://git.example/laya.git", "c" * 40),
                    "hub": ("https://hf.example", "mirror/base", "b" * 40)}


@pytest.mark.parametrize("kwargs,message", [
    ({"commit_error": "fatal: remote error: upload-pack: not our ref"}, "is not fetchable from"),
    ({"files": {"model.safetensors"}}, "lacks locked files: tokenizer/tokenizer.json"),
    ({"sha": "d" * 40}, "resolves to"),
    ({"files": OSError("HTTP Error 404: Not Found")}, "is not reachable: HTTP Error 404"),
])
def test_sources_fails_when_a_pin_is_no_longer_served(source_check, capsys, kwargs, message):
    with pytest.raises(SystemExit) as exit_info:
        source_check(**kwargs)
    assert exit_info.value.code == 1
    out = capsys.readouterr().out
    assert "::error title=Local AI build source unavailable::" in out and message in out

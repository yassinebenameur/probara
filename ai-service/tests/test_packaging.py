"""Static packaging invariants: hash locks, laya pin, LFS scope, image hardening."""
import json
import re
import shutil
import subprocess
from pathlib import Path
import pytest
from compose_artifact import HEAD, load_manifest, verify_committed
from upstream import LOCK
from probara_ai.contract import STATE_FORMATS

ROOT = Path(__file__).resolve().parents[1]
REPO = ROOT.parent
PIN = re.compile(r"^([A-Za-z0-9_.-]+)==(\S+)")


def pins(name):
    lines = (ROOT / name).read_text().replace("\\\n", " ").splitlines()
    found = {}
    for line in lines:
        match = PIN.match(line.strip())
        if match:
            hashes = re.findall(r"--hash=sha256:[0-9a-f]{64}", line)
            assert hashes, f"{name}: {match[1]} is not hash-pinned"
            found[match[1].lower().replace("_", "-")] = (match[2], sorted(hashes))
    return found


def test_locks_are_hash_pinned_and_consistent():
    runtime = pins("requirements.lock")
    assert "torch" in runtime and "laya" not in runtime
    assert not {"pytest", "httpx", "iniconfig", "pluggy"} & set(runtime), "test tools in the runtime image"
    for subset in ("requirements-fetch.lock", "requirements-build.lock"):
        for name, pin in pins(subset).items():
            assert runtime.get(name) == pin, f"{subset}: {name} differs from requirements.lock"
    dev = (ROOT / "requirements-dev.lock").read_text()
    assert "-r requirements.lock" in dev and "pytest" in pins("requirements-dev.lock")


def test_laya_pin_matches_training_interface():
    line = [l for l in (ROOT / "requirements-laya.txt").read_text().splitlines() if l.startswith("laya @")]
    assert line == [f"laya @ git+https://github.com/NandhaKishorM/laya.git@{LOCK['upstream_commit']}"]
    assert re.fullmatch(r"[0-9a-f]{40}", LOCK["revision"]) and re.fullmatch(r"[0-9a-f]{40}", LOCK["upstream_commit"])


def test_lfs_tracks_only_head_weights():
    rules = [l for l in (REPO / ".gitattributes").read_text().splitlines() if l and not l.startswith("#")]
    assert [l for l in rules if "filter=lfs" in l] == ["ai-service/artifacts/*/head.safetensors filter=lfs diff=lfs merge=lfs -text"]
    # Any other weights file would be committed as a plain Git blob.
    weights = {p.relative_to(ROOT).as_posix() for p in ROOT.rglob("*.safetensors") if ".cache" not in p.parts}
    assert all(re.fullmatch(r"artifacts/[^/]+/head\.safetensors", w) for w in weights), weights


def test_image_build_sources_are_overridable_but_pinned():
    """Mirrors may replace the fetch locations, never the pins they are checked against."""
    dockerfile = (ROOT / "Dockerfile").read_text()
    laya = [l for l in (ROOT / "requirements-laya.txt").read_text().splitlines() if l.startswith("laya @")][0]
    default = re.search(r"^ARG LAYA_GIT_URL=(\S+)$", dockerfile, re.MULTILINE)[1]
    assert laya.startswith(f"laya @ git+{default}@")
    stage = dockerfile[dockerfile.index("AS laya-wheel"):dockerfile.index("AS base-model")]
    assert 'fetch -q --depth 1 "$LAYA_GIT_URL" "$commit"' in stage
    assert 'test "$(git -C /build/laya rev-parse HEAD)" = "$commit"' in stage
    base = dockerfile[dockerfile.index("AS base-model"):dockerfile.index("AS dependencies")]
    assert "ARG HF_ENDPOINT=https://huggingface.co" in base
    assert '--repo "$BASE_MODEL_REPO" --revision "$BASE_MODEL_REVISION"' in base


def test_image_hardening():
    dockerfile = (ROOT / "Dockerfile").read_text()
    assert re.search(r"python:3\.13\.\d+-slim@sha256:[0-9a-f]{64}", dockerfile)
    runtime = dockerfile[dockerfile.index("AS runtime"):]
    assert "USER 1000:1000" in runtime and "HEALTHCHECK" in runtime and "/healthz" in runtime
    assert "EXPOSE 8090" in runtime and "HF_HUB_OFFLINE=1" in runtime
    assert "apt-get" not in runtime and not re.search(r"\bgit\b", runtime)
    assert dockerfile.count("--require-hashes") >= 3
    assert "verify_artifact" in dockerfile[dockerfile.index("AS artifact"):dockerfile.index("AS runtime")]


def test_training_scripts_take_explicit_versions_and_paths():
    scripts = [*(ROOT / "training").glob("*.py"), *(ROOT / "tools").glob("*.py"), *(ROOT / "probara_ai").glob("*.py")]
    for path in scripts:
        source = path.read_text()
        assert "probara-laya-v1" not in source and "/tmp" not in source, path.name
    for name in ("train.py", "train_cpu.py", "train_head_cpu.py"):
        assert '"--version", required=True' in (ROOT / "training" / name).read_text().replace("'", '"'), name


@pytest.mark.checkpoint
@pytest.mark.parametrize("artifact", sorted(p for p in (ROOT / "artifacts").glob("probara-laya-*") if p.is_dir()), ids=lambda p: p.name)
def test_committed_artifacts_are_head_only_and_intact(artifact):
    manifest = load_manifest(artifact)
    verify_committed(artifact, manifest)
    present = {p.relative_to(artifact).as_posix() for p in artifact.rglob("*") if p.is_file()}
    assert present == set(manifest["files"]) | {"manifest.json"}, "unlisted or missing artifact files"
    assert not {"model.safetensors", "tokenizer/tokenizer.json", "encoder/config.json"} & present
    # A recorded composition is required before use (compose refuses without
    # it) and always for release; recording needs the real pinned base.
    if manifest["release_approved"] is True or "composed" in manifest:
        assert {"model.safetensors", "tokenizer/tokenizer.json", "encoder/config.json"} <= set(manifest["composed"])
    assert manifest["version"] == artifact.name
    assert manifest.get("state_format", 1) in STATE_FORMATS
    assert json.loads((artifact / "rl_agent_config.json").read_text())["model_name"] == artifact.name
    if manifest["release_approved"] is True:
        report = json.loads((artifact / "cpu-evaluation.json").read_text())
        assert report["release_approved"] is True and report["model_sha256"] == manifest["composed"]["model.safetensors"]
    assert manifest["files"][HEAD] == manifest["head_sha256"]


def test_v1_stays_rejected():
    manifest = load_manifest(ROOT / "artifacts/probara-laya-v1")
    report = json.loads((ROOT / "artifacts/probara-laya-v1/cpu-evaluation.json").read_text())
    assert manifest["release_approved"] is False and report["release_approved"] is False


def test_shipped_artifact_is_approved():
    """The Dockerfile default is what CI gates and every default-on install runs."""
    version = re.search(r"^ARG MODEL_VERSION=(\S+)$", (ROOT / "Dockerfile").read_text(), re.MULTILINE)[1]
    manifest = load_manifest(ROOT / "artifacts" / version)
    report = json.loads((ROOT / "artifacts" / version / "cpu-evaluation.json").read_text())
    assert manifest["release_approved"] is True and report["release_approved"] is True and report["device"] == "cpu"


V1_EVALUATED ="b8ca791b7e57671d01ee86f4cbc832493846557f43bba3a2aef0c4abab0ecb49"


def test_v1_composition_is_recorded():
    """v1's composition reproduces the full checkpoint its CPU audit scored."""
    artifact = ROOT / "artifacts/probara-laya-v1"
    manifest = load_manifest(artifact)
    audit = json.loads((artifact / "cpu-calibration-audit-initial.json").read_text())
    assert manifest["composed"]["model.safetensors"] == audit["model_sha256"] == V1_EVALUATED


def test_notices_cover_the_redistributed_upstreams():
    notices = (ROOT / "THIRD-PARTY-NOTICES").read_text()
    assert "jhu-clsp/mmBERT-base" in notices and "MIT License" in notices and "Gemma 2" in notices
    assert "THIRD-PARTY-NOTICES" in (ROOT / "training/upstream.py").read_text()
    from probara_ai.runtime import REQUIRED_FILES
    assert "THIRD-PARTY-NOTICES" in REQUIRED_FILES


LFS_POINTER = b"version https://git-lfs.github.com/spec/v1\n"


def _git(*args):
    return subprocess.run(["git", "-C", str(REPO), *args], capture_output=True, check=False)


def test_head_weights_are_stored_as_lfs_pointers():
    """A head staged or committed without the LFS filter is a 30 MB blob in history.

    .gitattributes alone does not prevent that: without git-lfs installed and
    `git lfs install` run, `git add` stores the raw file. Committed heads must
    be pointers, and a checkout that could stage one must have the filter.
    """
    if _git("rev-parse", "--is-inside-work-tree").returncode != 0:
        pytest.skip("not a Git checkout")
    heads = sorted(p.relative_to(REPO).as_posix() for p in (ROOT / "artifacts").glob("*/head.safetensors"))
    tracked = set(_git("ls-files", "--", *heads).stdout.decode().split())
    for head in heads:
        for revision in (":", "HEAD:"):
            blob = _git("cat-file", "blob", f"{revision}{head}")
            if blob.returncode == 0:
                assert blob.stdout.startswith(LFS_POINTER), f"{revision}{head} is a raw blob, not a Git LFS pointer"
    untracked = [head for head in heads if head not in tracked]
    if untracked:
        clean = _git("config", "--get", "filter.lfs.clean").stdout.decode().strip()
        assert clean and shutil.which("git-lfs"), (
            "git-lfs is not installed or not configured (`git lfs install`) in this checkout; staging "
            + ", ".join(untracked) + " now would commit raw weights instead of LFS pointers")

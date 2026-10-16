"""Base download: mirror overrides never relax the sha256 lock."""
import hashlib
import json
import sys
import types

import pytest

import upstream

FILES = {"README.md": b"card\n", "model.safetensors": b"weights",
         "tokenizer/tokenizer_config.json": json.dumps({"model_max_length": 8}).encode()}
LOCK = {"repo": "upstream/base", "revision": "a" * 40,
        "files": {name: hashlib.sha256(data).hexdigest() for name, data in FILES.items()}}


@pytest.fixture
def hub(monkeypatch):
    """A fake huggingface_hub serving FILES (or `served`), recording each call."""
    calls, served = [], dict(FILES)

    def snapshot_download(repo, revision, local_dir, allow_patterns):
        calls.append((repo, revision, sorted(allow_patterns)))
        for name, data in served.items():
            path = local_dir / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)

    monkeypatch.setitem(sys.modules, "huggingface_hub", types.SimpleNamespace(snapshot_download=snapshot_download))
    return calls, served


def test_fetch_defaults_to_the_locked_repo_and_revision(tmp_path, hub):
    calls, _ = hub
    upstream.fetch(tmp_path / "base", lock=LOCK)
    assert calls == [("upstream/base", "a" * 40, sorted(FILES))]


def test_fetch_accepts_a_mirror_serving_the_same_bytes(tmp_path, hub):
    calls, _ = hub
    upstream.fetch(tmp_path / "base", repo="mirror/base", revision="b" * 40, lock=LOCK)
    assert calls == [("mirror/base", "b" * 40, sorted(FILES))]
    assert (tmp_path / "base/model.safetensors").read_bytes() == b"weights"


@pytest.mark.parametrize("change", ["tampered", "missing"])
def test_fetch_refuses_a_mirror_that_differs_from_the_lock(tmp_path, hub, change):
    _, served = hub
    if change == "tampered":
        served["model.safetensors"] = b"other weights"
    else:
        del served["model.safetensors"]
    with pytest.raises(ValueError, match="model.safetensors"):
        upstream.fetch(tmp_path / "base", repo="mirror/base", lock=LOCK)

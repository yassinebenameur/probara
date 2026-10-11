"""Training-only augmentation never touches evaluation groups."""
import json

import pytest

import augment
from train_head_cpu import read_extra_train, load_init_head


def _write(path, rows):
    path.write_text("".join(json.dumps(r) + "\n" for r in rows))


def test_augment_rows_are_deterministic_and_train_only(tmp_path):
    for name in ("a", "b"):
        (tmp_path / name).mkdir()
    a = augment.generate(tmp_path / "a", 20)
    b = augment.generate(tmp_path / "b", 20)
    assert a["sha256"] == b["sha256"] and a["rows"] == 100
    rows = [json.loads(l) for l in (tmp_path / "a" / "dependency-train-augment.jsonl").read_text().splitlines()]
    assert all(r["group"].startswith("dependency-aug-") and r["task"] == "dependency" for r in rows)
    labels = {r["expected"]["dependency"] for r in rows}
    assert {"a_depends_b", "b_depends_a"} <= labels


def test_extra_train_rejects_evaluation_groups(tmp_path):
    data = tmp_path / "data"
    data.mkdir()
    for task in ("rca", "dependency"):
        for part in ("calibration", "test"):
            _write(data / f"{task}-{part}.jsonl", [{"group": f"{task}-{part}-1", "task": task}])
    ok = tmp_path / "ok.jsonl"
    _write(ok, [{"group": "dependency-aug-0", "task": "dependency"}])
    assert len(read_extra_train(data, [ok])) == 1
    leak = tmp_path / "leak.jsonl"
    _write(leak, [{"group": "dependency-test-1", "task": "dependency"}])
    with pytest.raises(ValueError, match="overlap"):
        read_extra_train(data, [leak])


def test_init_head_rejects_encoder_tensors(tmp_path):
    torch = pytest.importorskip("torch")
    from safetensors.torch import save_file
    path = tmp_path / "head.safetensors"
    save_file({"encoder.layer.weight": torch.zeros(1)}, str(path))
    with pytest.raises(ValueError, match="encoder"):
        load_init_head(object(), path)

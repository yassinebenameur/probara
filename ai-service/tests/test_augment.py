"""Training-only augmentation never touches evaluation groups."""
import json
from pathlib import Path

import pytest

import augment
from probara_ai.contract import STATE_FORMAT, from_label_export, state_text
from train_head_cpu import read_extra_train, load_init_head

LABEL_EXPORT = Path(__file__).resolve().parents[2] / "shared/ai/testdata/label-export-v1.jsonl"


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


def _eval_partitions(tmp_path):
    data = tmp_path / "data"
    data.mkdir()
    for task in ("rca", "dependency"):
        for part in ("calibration", "test"):
            _write(data / f"{task}-{part}.jsonl", [{"group": f"{task}-{part}-1", "task": task}])
    return data


def _label_export_rows():
    return [json.loads(line) for line in LABEL_EXPORT.read_text().splitlines()]


def test_extra_train_rejects_evaluation_groups(tmp_path):
    data = _eval_partitions(tmp_path)
    augmented = tmp_path / "augment"
    augmented.mkdir()
    augment.generate(augmented, 1)
    ok = augmented / "dependency-train-augment.jsonl"
    assert len(read_extra_train(data, [ok])) == len(ok.read_text().splitlines())
    leak = tmp_path / "leak.jsonl"
    _write(leak, [{"group": "dependency-test-1", "task": "dependency"}])
    with pytest.raises(ValueError, match="overlap"):
        read_extra_train(data, [leak])


def test_extra_train_re_renders_label_exports(tmp_path):
    # The Go export (shared/ai/labelexport.go) is frozen at state format 1;
    # passing it straight to --extra-train must train on current-format text.
    data = _eval_partitions(tmp_path)
    exported = _label_export_rows()
    rows = read_extra_train(data, [LABEL_EXPORT])
    assert rows == [from_label_export(r) for r in exported]
    assert all(r["state"] == state_text(r["task"], r["evidence"], STATE_FORMAT) for r in rows)
    # An already converted export passes through unchanged.
    converted = tmp_path / "converted.jsonl"
    _write(converted, rows)
    assert read_extra_train(data, [converted]) == rows


def test_extra_train_refuses_stale_or_edited_state(tmp_path):
    data = _eval_partitions(tmp_path)
    row = _label_export_rows()[0]
    edited = tmp_path / "edited.jsonl"
    _write(edited, [{**row, "state": "Checks: 1."}])
    with pytest.raises(ValueError, match="format-1 rendering"):
        read_extra_train(data, [edited])
    # Outside operator-label groups, only current-format rows are accepted.
    stale = tmp_path / "stale.jsonl"
    _write(stale, [{**row, "group": "dependency-aug-x", "state": state_text(row["task"], row["evidence"], 1)}])
    if STATE_FORMAT != 1:
        with pytest.raises(ValueError, match="state format"):
            read_extra_train(data, [stale])


def test_init_head_rejects_encoder_tensors(tmp_path):
    torch = pytest.importorskip("torch")
    from safetensors.torch import save_file
    path = tmp_path / "head.safetensors"
    save_file({"encoder.layer.weight": torch.zeros(1)}, str(path))
    with pytest.raises(ValueError, match="encoder"):
        load_init_head(object(), path)

"""Operator label exports: the Go twin (shared/ai/labelexport.go) pins format 1."""
import json
from pathlib import Path
import pytest
from probara_ai.contract import LABEL_EXPORT_FORMAT, STATE_FORMAT, from_label_export, options, state_text

FIXTURE = Path(__file__).resolve().parents[2] / "shared/ai/testdata/label-export-v1.jsonl"
INSTRUCTIONS = "Select the hypothesis supported by the measured evidence; abstain when inconclusive."


def rows():
    return [json.loads(line) for line in FIXTURE.read_text().splitlines()]


def test_go_export_fixture_is_python_format_one():
    exported = rows()
    assert LABEL_EXPORT_FORMAT == 1 and {row["task"] for row in exported} == {"rca", "dependency"}
    for row in exported:
        task = row["task"]
        assert row["state"] == state_text(task, row["evidence"], 1)
        question = row["questions"][task]
        assert set(row["questions"]) == {task} and question["type"] == "choice"
        assert question["instructions"] == INSTRUCTIONS
        assert question["criteria"] == options(task)
        assert set(row["expected"]) == {task} and row["expected"][task] in question["criteria"]
        assert row["group"].startswith(f"op-{task}-")


def test_export_rows_are_re_rendered_for_training():
    for row in rows():
        converted = from_label_export(row)
        assert converted["state"] == state_text(row["task"], row["evidence"], STATE_FORMAT)
        assert {k: v for k, v in converted.items() if k != "state"} == {k: v for k, v in row.items() if k != "state"}
    edited = {**rows()[0], "state": "Checks: 1."}
    with pytest.raises(ValueError, match="format-1 rendering"):
        from_label_export(edited)

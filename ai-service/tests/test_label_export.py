"""Operator label exports: the Go twin (shared/ai/labelexport.go) pins format 1."""
import json
from pathlib import Path
import pytest
from probara_ai.contract import (LABEL_EXPORT_FORMAT, PAIR_FIELDS, STATE_FORMAT, StaleLabelExport, VERSION,
                                 from_label_export, options, state_text)

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
        # The options of the catalogue the row was labelled under: the current
        # one, or (for a row exported before a catalogue bump) an older one
        # with the same keys, which training skips (StaleLabelExport).
        assert set(question["criteria"]) == set(options(task))
        if row["provenance"].get("catalogue") == VERSION:
            assert question["criteria"] == options(task)
        assert set(row["expected"]) == {task} and row["expected"][task] in question["criteria"]
        assert row["group"].startswith(f"op-{task}-")


def current(row):
    """The row as a current-catalogue export would carry it: v2 options and every pair field."""
    task = row["task"]
    evidence = dict(row["evidence"])
    if task == "dependency":
        evidence = {key: evidence.get(key, False if key in ("a_extends_b", "b_extends_a", "same_type", "a_infra",
                                                             "b_infra", "peers") else 0) for key in sorted(PAIR_FIELDS)}
    return {**row, "evidence": evidence, "state": state_text(task, evidence, 1),
            "questions": {task: {**row["questions"][task], "criteria": options(task)}},
            "provenance": {**row["provenance"], "catalogue": VERSION}}


def test_export_rows_are_re_rendered_for_training():
    for row in map(current, rows()):
        converted = from_label_export(row)
        assert converted["state"] == state_text(row["task"], row["evidence"], STATE_FORMAT)
        assert {k: v for k, v in converted.items() if k != "state"} == {k: v for k, v in row.items() if k != "state"}
    edited = {**rows()[0], "state": "Checks: 1."}
    with pytest.raises(ValueError, match="format-1 rendering"):
        from_label_export(edited)


def test_rows_from_an_older_catalogue_are_stale_not_trained():
    exported = rows()
    raw = next(row for row in exported if row["task"] == "dependency")
    pair = current(raw)
    # The v2 pair fields do not change the frozen format-1 state the Go side exports.
    assert pair["state"] == raw["state"]
    older = {**pair, "questions": {"dependency": {**pair["questions"]["dependency"], "criteria": {
        key: "an older catalogue's description" for key in options("dependency")}}}}
    with pytest.raises(StaleLabelExport, match="another catalogue"):
        from_label_export(older)
    # The Go export writes the later fields as zero for a pair labelled under
    # v1; provenance.catalogue is what tells it apart.
    zero_filled = {**pair, "provenance": {**pair["provenance"], "catalogue": "probara-evidence-v1"}}
    with pytest.raises(StaleLabelExport, match="labelled under probara-evidence-v1"):
        from_label_export(zero_filled)
    rca = current(next(row for row in exported if row["task"] == "rca"))
    from_label_export({**rca, "provenance": {**rca["provenance"], "catalogue": "probara-evidence-v1"}})
    seven = {key: pair["evidence"][key] for key in ("co_fires", "a_leads_seconds", "reverse_co_fires", "shared_service",
                                                     "shared_group", "naming_signal", "common_upstream")}
    unmeasured = {**pair, "evidence": seven, "state": state_text("dependency", seven, 1)}
    with pytest.raises(StaleLabelExport, match="unmeasured, not absent"):
        from_label_export(unmeasured)
    # Whatever the fixture's catalogue, each of its rows either converts or is stale; never silently kept.
    for row in exported:
        try:
            from_label_export(row)
        except StaleLabelExport:
            assert row["task"] == "dependency"

"""The dependency guard against the cross-language reference cases and the production regression set."""
import json
from pathlib import Path
import pytest
from probara_ai.contract import LEGACY_PAIR_FIELDS, PAIR_FIELDS
from probara_ai.guard import ABSTAIN, DIRECTIONAL, NONE, RARE_MAX, dependency_guard

TESTDATA = Path(__file__).resolve().parents[2] / "shared/ai/testdata"
CASES = json.loads((TESTDATA / "dependency-guard-cases.json").read_text())
REGRESSION = json.loads((TESTDATA / "dependency-regression.json").read_text())["rows"]


def test_rare_bound_is_the_reference_one():
    assert RARE_MAX == CASES["rare_max"]


@pytest.mark.parametrize("case", CASES["cases"], ids=lambda case: case["name"])
def test_reference_cases(case):
    assert set(case["evidence"]) == PAIR_FIELDS
    assert dependency_guard(case["evidence"]) == (case["basis"], case["label"])


@pytest.mark.parametrize("index", range(len(REGRESSION)))
def test_production_regression_rows(index):
    row = REGRESSION[index]
    basis, label = dependency_guard(row["evidence"])
    assert (basis, label) == (row["expected_basis"], row["expected_label"])
    # Invariants: a pair the operator rejected never gets a basis, and no pair
    # gets a direction other than the operator's.
    if row["truth"] == "no_dependency":
        assert (basis, label) == (NONE, ABSTAIN)
    if label in DIRECTIONAL:
        assert label == row["truth"]


def test_regression_set_exercises_every_outcome():
    outcomes = {(row["truth"], row["expected_basis"]) for row in REGRESSION}
    assert {("no_dependency", "none"), ("b_depends_a", "variant"), ("a_depends_b", "variant"),
            ("b_depends_a", "structural"), ("a_depends_b", "structural")} <= outcomes


def test_missing_fields_count_as_zero_and_false():
    # An older caller's seven-field pair can only abstain: every basis needs a v2 field.
    legacy = {"co_fires": 12, "a_leads_seconds": 30, "reverse_co_fires": 0, "shared_service": True,
              "shared_group": True, "naming_signal": True, "common_upstream": False}
    assert set(legacy) == set(LEGACY_PAIR_FIELDS)
    assert dependency_guard(legacy) == (NONE, ABSTAIN)
    assert dependency_guard({}) == (NONE, ABSTAIN)
    assert dependency_guard({**legacy, "shared_tag_monitors": 4}) == ("timing", "b_depends_a")
    assert dependency_guard({"shared_name_prefix": 2, "b_infra": True}) == ("structural", "a_depends_b")


def test_conservative_rules_are_strictly_inside_the_guard():
    pytest.importorskip("laya")
    from train import guard_rules, rules

    def row(label, **evidence):
        return {"task": "dependency", "evidence": evidence, "expected": {"dependency": label}}

    timing5 = dict(co_fires=5, a_leads_seconds=60, window_monitors=2, shared_tag_monitors=3)
    rows = [row("b_depends_a", **timing5),
            row("b_depends_a", **{**timing5, "co_fires": 4}),                          # guard yes, rules no
            row("a_depends_b", shared_tag_monitors=3, b_infra=True),                    # structural: rules no
            row("insufficient_evidence", **{**timing5, "window_monitors": 6, "peers": True})]
    assert rules(rows) == {"precision": 1.0, "coverage": 1 / 3, "decision_accuracy": 0.5}
    assert guard_rules(rows) == {"precision": 1.0, "coverage": 1.0, "decision_accuracy": 1.0}
    # A corpus label the guard disagrees with shows up in the guard's own score.
    assert guard_rules([row("a_depends_b", **timing5)])["precision"] == 0


def test_metrics_emit_only_the_guard_label_above_threshold():
    pytest.importorskip("laya")
    import numpy as np
    from train import metrics
    keys = ["a_depends_b", "b_depends_a", "shared_upstream", "no_dependency", "insufficient_evidence"]

    def scored(label, evidence, best, score=4.0):
        row = {"task": "dependency", "evidence": evidence, "expected": {"dependency": label},
               "questions": {"dependency": {"criteria": {key: key for key in keys}}}}
        logits = np.zeros(5, dtype=np.float32)
        logits[keys.index(best)] = score
        target = np.zeros(5, dtype=np.float32)
        target[keys.index(label)] = 1
        return row, (0, logits, target, 5)

    timing = dict(co_fires=4, a_leads_seconds=60, window_monitors=2, shared_tag_monitors=3)
    cases = [scored("b_depends_a", timing, "b_depends_a"),                        # emitted, correct
             scored("b_depends_a", timing, "a_depends_b"),                        # not the guard's label
             scored("b_depends_a", timing, "b_depends_a", score=0.1),             # below threshold
             scored("insufficient_evidence", {**timing, "peers": True}, "b_depends_a")]  # no basis
    rows, records = zip(*cases)
    result = metrics(rows, records, 1.0, 0.5)
    assert (result["emitted"], result["correct_emitted"], result["supported"]) == (1, 1, 3)
    assert result["unsupported_abstention"] == 1.0

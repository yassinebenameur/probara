"""The CPU release gate, one predicate for the validator and every verifier.

training/validate_cpu.py computes it to decide release_approved;
probara_ai.runtime.verify_artifact (image build, CI, startup) recomputes it
from the committed cpu-evaluation.json "tasks" and refuses a report whose
release_approved flag disagrees, so approval cannot be asserted by editing a
boolean. Changing a threshold here is a release-policy change.
"""
import math

TASKS = ("rca", "dependency")
MIN_PRECISION = .95
MIN_UNSUPPORTED_ABSTENTION = .9
MIN_COVERAGE = .5


def _number(value):
    return type(value) in (int, float) and math.isfinite(value)


def task_passes(report):
    """Held-out metrics against the predeclared conservative-rules baseline."""
    held, baseline = report.get("held_out"), report.get("conservative_rules")
    if not isinstance(held, dict) or not isinstance(baseline, dict):
        return False
    metrics = ("precision", "unsupported_abstention", "coverage", "decision_accuracy")
    if not all(_number(held.get(key)) for key in metrics) or \
            not all(_number(baseline.get(key)) for key in ("precision", "coverage", "decision_accuracy")):
        return False
    return (held["precision"] >= max(MIN_PRECISION, baseline["precision"]) and
            held["unsupported_abstention"] >= MIN_UNSUPPORTED_ABSTENTION and
            held["coverage"] >= MIN_COVERAGE and
            held["decision_accuracy"] >= baseline["decision_accuracy"] and
            held["coverage"] > baseline["coverage"])


def release_gate(tasks):
    """True only when every task (exactly rca and dependency) passes."""
    return (isinstance(tasks, dict) and set(tasks) == set(TASKS) and
            all(isinstance(tasks[task], dict) and task_passes(tasks[task]) for task in TASKS))

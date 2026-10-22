"""Synthetic CPU reports for gate tests only; never written into a real artifact."""
import copy
import json

TASK = {"held_out": {"n": 1000, "supported": 600, "emitted": 500, "correct_emitted": 500, "precision": 1.0,
                     "coverage": 500 / 600, "unsupported_abstention": 1.0, "decision_accuracy": 0.9},
        "conservative_rules": {"precision": 1.0, "coverage": 0.75, "decision_accuracy": 0.85},
        "temperature": 1.5, "threshold": 0.5}


def passing_tasks():
    return {task: copy.deepcopy(TASK) for task in ("rca", "dependency")}


def calibration(tasks):
    return {"temperatures": {task: report["temperature"] for task, report in tasks.items()},
            "thresholds": {task: report["threshold"] for task, report in tasks.items()}}


def passing_report(version, model_sha256, cpu_format, state_format=None, tasks=None):
    report = {"release_approved": True, "device": "cpu", "cpu_format": cpu_format, "version": version,
              "model_sha256": model_sha256, "tasks": tasks or passing_tasks()}
    if state_format is not None:
        report["state_format"] = state_format
    return report


def write_approval(path, version, model_sha256, cpu_format, state_format=None, tasks=None):
    """Write a passing cpu-evaluation.json and its matching calibration.json into path."""
    report = passing_report(version, model_sha256, cpu_format, state_format, tasks)
    (path / "cpu-evaluation.json").write_text(json.dumps(report))
    (path / "calibration.json").write_text(json.dumps(calibration(report["tasks"])))
    return report

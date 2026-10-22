"""Training-only augmentation for the dependency task.

Adds fresh scenario groups (ids "dependency-aug-<n>", disjoint from every
calibration/test group) drawn from the same generator rules as generate.py,
weighted toward the two directional kinds the model confuses (a_depends_b vs
b_depends_a) plus near-miss contrasts (no direction, reverse windows, missing
corroboration, missing history). Calibration and test partitions are never
touched, so evaluation stays comparable across candidates.
"""
import argparse
import hashlib
import json
import random
from pathlib import Path
from probara_ai.contract import training_row

SEED = 20261111


def dependency_row(rng, kind, group):
    count = rng.randint(2, 12)
    lead = rng.randint(5, 240)
    corroboration = rng.randrange(3)
    e = {"co_fires": count, "a_leads_seconds": lead if kind % 2 else -lead, "reverse_co_fires": 0,
         "shared_service": corroboration in (0, 2), "shared_group": corroboration in (1, 2),
         "naming_signal": bool(rng.randrange(2)), "common_upstream": False}
    if kind <= 1:
        label = "b_depends_a" if kind else "a_depends_b"
    elif kind == 2:
        e["common_upstream"] = True
        label = "shared_upstream"
    elif kind == 3:
        e.update(co_fires=0, shared_group=False, shared_service=False, naming_signal=False)
        label = "no_dependency"
    else:
        label = "insufficient_evidence"
        if kind == 4:
            e.update(shared_service=False, shared_group=False)
        elif kind == 5:
            e["a_leads_seconds"] = 0
        elif kind == 6:
            e["reverse_co_fires"] = rng.randint(1, 8)
        else:
            e["co_fires"] = rng.randint(0, 1)
    row = training_row("dependency", e, label, group)
    opts = list(row["questions"]["dependency"]["criteria"].items()); rng.shuffle(opts)
    row["questions"]["dependency"]["criteria"] = dict(opts)
    return row


def generate(output, groups):
    rows = []
    for n in range(groups):
        rng = random.Random(SEED + n * 37)
        # 70% directional (balanced a/b), 30% contrasts across kinds 2..7.
        kind = n % 2 if n % 10 < 7 else 2 + (n % 6)
        for variant in range(5):
            row = dependency_row(rng, kind, f"dependency-aug-{n}")
            row["provenance"] = {"augmentation": True, "scenario_kind": kind, "variation": variant}
            rows.append(row)
    path = output / "dependency-train-augment.jsonl"
    path.write_text("".join(json.dumps(r) + "\n" for r in rows))
    meta = {"seed": SEED, "groups": groups, "rows": len(rows), "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
            "generator_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
            "scope": "training-only augmentation; calibration and test partitions unchanged"}
    (output / "augment-provenance.json").write_text(json.dumps(meta, indent=2) + "\n")
    return meta


if __name__ == "__main__":
    p = argparse.ArgumentParser(); p.add_argument("--output", type=Path, required=True); p.add_argument("--groups", type=int, default=400)
    a = p.parse_args(); a.output.mkdir(parents=True, exist_ok=True); print(json.dumps(generate(a.output, a.groups)))

"""Training-only augmentation for the dependency task.

Adds fresh scenario groups (ids "dependency-aug-<n>", disjoint from every
calibration/test group) drawn from generate.py's own dependency scenarios
(dependency_example, labels checked against the guard), weighted toward the
supported kinds with their direction mirrored inside each group, plus
the near-miss contrasts. Calibration and test partitions are never touched,
so evaluation stays comparable across candidates.

probara-controlled-v4 already balances directions in the base corpus (the
lesson of v4, whose head confused A-before-B with B-before-A until 2,000 rows
from this script's predecessor were added), so this is optional: use it only
when calibration-only validation shows a directional confusion the base
corpus does not fix. training/history/augment_v4.py regenerates v4's file.
"""
import argparse
import hashlib
import json
import random
from pathlib import Path
from probara_ai.contract import training_row
from generate import DEPENDENCY_KINDS, SUPPORTED, check_label, dependency_example

SEED = 20261111
SUPPORTED_KINDS = sorted(SUPPORTED)
CONTRAST_KINDS = sorted(set(DEPENDENCY_KINDS) - SUPPORTED)


def generate(output, groups):
    rows = []
    for n in range(groups):
        rng = random.Random(SEED + n * 37)
        # 70% supported (each supported kind in turn, both directions), 30% contrasts.
        kind = SUPPORTED_KINDS[n % len(SUPPORTED_KINDS)] if n % 10 < 7 else CONTRAST_KINDS[n % len(CONTRAST_KINDS)]
        start = rng.randrange(2)
        for variant in range(5):
            a_first = (start + variant) % 2 == 1 if kind in SUPPORTED else rng.random() < .5
            e, label = dependency_example(rng, kind, a_first)
            check_label(e, label)
            row = training_row("dependency", e, label, f"dependency-aug-{n}")
            opts = list(row["questions"]["dependency"]["criteria"].items()); rng.shuffle(opts)
            row["questions"]["dependency"]["criteria"] = dict(opts)
            row["provenance"] = {"augmentation": True, "scenario": kind, "variation": variant}
            rows.append(row)
    path = output / "dependency-train-augment.jsonl"
    path.write_text("".join(json.dumps(r) + "\n" for r in rows))
    meta = {"seed": SEED, "groups": groups, "rows": len(rows), "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
            "generator_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
            "scenario_generator_sha256": hashlib.sha256((Path(__file__).parent / "generate.py").read_bytes()).hexdigest(),
            "scope": "training-only augmentation; calibration and test partitions unchanged"}
    (output / "augment-provenance.json").write_text(json.dumps(meta, indent=2) + "\n")
    return meta


if __name__ == "__main__":
    p = argparse.ArgumentParser(); p.add_argument("--output", type=Path, required=True); p.add_argument("--groups", type=int, default=400)
    a = p.parse_args(); a.output.mkdir(parents=True, exist_ok=True); print(json.dumps(generate(a.output, a.groups)))

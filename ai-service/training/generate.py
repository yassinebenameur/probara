"""Controlled-scenario corpus, derived from real loopback checker observations.

Five related variations per independently seeded scenario/topology. Group split
happens before generation; every variation stays in its group's partition.
Hidden injected faults never determine labels without visible evidence.
"""
import hashlib
import json
import random
from pathlib import Path
from probara_ai.contract import training_row

SEED = 20261010
HERE = Path(__file__).parent


def generate(output):
    fixtures = json.loads((HERE / "checker-fixtures.json").read_text())
    for task in ("rca", "dependency"):
        by_split = {k: [] for k in ("train", "calibration", "test")}
        groups = list(range(1000))
        random.Random(SEED + (task == "dependency")).shuffle(groups)
        split_of = {group: "train" if i < 600 else "calibration" if i < 800 else "test" for i, group in enumerate(groups)}
        for group in groups:
            rng = random.Random(SEED + group * 31 + (task == "dependency"))
            # Random irrelevant identifiers/timestamps are recorded as provenance,
            # never used as model state or split labels.
            name = f"service-{rng.getrandbits(64):016x}"
            timestamp = rng.randrange(1500000000, 1900000000)
            kind = rng.randrange(12 if task == "rca" else 8)
            for variant in range(5):
                if task == "rca":
                    keys = ["dns_failure", "connection_failure", "tls_problem", "protocol_error", "latency_degradation", "resource_pressure", "upstream_failure", "maintenance_downtime", "insufficient_evidence", "insufficient_evidence", "insufficient_evidence", "insufficient_evidence"]
                    label = keys[kind]
                    failures = rng.randint(1, 8)
                    checks = failures + rng.randint(1, 12)
                    e = {"checks": checks, "failures": failures}
                    if kind < 4:
                        category = fixtures[label]["error_category"]
                        e[{"dns": "dns_failures", "connection": "connection_failures", "tls": "tls_failures", "protocol": "protocol_failures"}[category]] = failures
                    elif kind == 4:
                        # Slow checker produced a measured >500 ms observation.
                        measured = fixtures[label]["latency_ms"]
                        e.update(slow_checks=failures, baseline_ms=rng.randint(5, 100), max_latency_ms=max(500, measured) + rng.randint(0, 2000))
                    elif kind == 5:
                        # Controlled metric-store ratio fixture, not external
                        # telemetry: measured pressure during failed observations.
                        e.update(resource_pressure=True, host_metrics_available=True)
                    elif kind == 6:
                        # Topology's known upstream has real refused-connect outputs
                        # preceding the dependent's real failed HTTP outputs.
                        e.update(upstream_failed_before=True)
                    elif kind == 7:
                        e.update(maintenance_overlap=True)
                    elif kind == 8:
                        e = {"checks": 0, "failures": 0, "missing_checks": True}
                    elif kind == 9:
                        e.update(dns_failures=1, protocol_failures=1, conflict=True)
                    elif kind == 10:
                        # Unsupported timeout/hidden injected application fault.
                        pass
                    else:
                        # A changed but valid certificate or expired host telemetry
                        # must not become a certificate/resource diagnosis.
                        e.update(certificate_changed=True, host_metrics_available=False)
                else:
                    count = rng.randint(2, 12)
                    lead = rng.randint(5, 240)
                    # Production accepts service OR group corroboration. Do not
                    # teach the model that group membership is always required.
                    corroboration = rng.randrange(3)
                    e = {"co_fires": count, "a_leads_seconds": lead if kind % 2 else -lead, "reverse_co_fires": 0, "shared_service": corroboration in (0, 2), "shared_group": corroboration in (1, 2), "naming_signal": bool(rng.randrange(2)), "common_upstream": False}
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
                            e.update(shared_service=False, shared_group=False)  # coincidences/misleading names
                        elif kind == 5:
                            e["a_leads_seconds"] = 0  # simultaneous symptoms
                        elif kind == 6:
                            e["reverse_co_fires"] = rng.randint(1, 8)  # conflicting direction
                        else:
                            e["co_fires"] = rng.randint(0, 1)  # missing history
                row = training_row(task, e, label, f"{task}-{group}")
                # Randomise option order in all partitions and every variation.
                opts = list(row["questions"][task]["criteria"].items()); rng.shuffle(opts)
                row["questions"][task]["criteria"] = dict(opts)
                row["provenance"] = {"topology": name, "anchor_epoch": timestamp, "variation": variant, "scenario_kind": kind}
                by_split[split_of[group]].append(row)
        for split, rows in by_split.items():
            path = output / f"{task}-{split}.jsonl"
            path.write_text("".join(json.dumps(row, sort_keys=False) + "\n" for row in rows))
    provenance = {"corpus_version": "probara-controlled-v3", "generator_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                  "seed": SEED, "examples_per_task": 5000, "group_count_per_task": 1000, "variations_per_group": 5,
                  "split": [0.6, 0.2, 0.2], "source": "worker/internal/worker/ai_fault_fixture_test.go",
                  "limitations": "Controlled synthetic variations of real loopback checker outputs and metric/topology fixtures; no real-world accuracy claim.",
                  "partition_sha256": {str(p.name): hashlib.sha256(p.read_bytes()).hexdigest() for p in output.glob("*.jsonl")},
                  "checker_fixture_sha256": hashlib.sha256((HERE / "checker-fixtures.json").read_bytes()).hexdigest()}
    (output / "provenance.json").write_text(json.dumps(provenance, indent=2) + "\n")


if __name__ == "__main__":
    import argparse
    parser = argparse.ArgumentParser(); parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(); args.output.mkdir(parents=True, exist_ok=True); generate(args.output)

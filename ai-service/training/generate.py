"""Controlled-scenario corpus, derived from real loopback checker observations.

Five related variations per independently seeded scenario/topology. Group split
happens before generation; every variation stays in its group's partition.
Hidden injected faults never determine labels without visible evidence.

Corpus probara-controlled-v4 (state format 3, catalogue probara-evidence-v2).
RCA rows, their RNG stream and their bytes are those of probara-controlled-v3.
Dependency rows carry the v2 pair evidence, and their labels follow from the
visible evidence only, through the guard (probara_ai.guard): a pair is
directional exactly when the guard supports that direction, which generate()
checks for every row. DEPENDENCY_KINDS is balanced toward the three supported
bases (with backing-resource roles agreeing, or overriding contrary timing),
and every supported group mirrors its direction across variations
(A-upstream, B-upstream, ...): v4 needed 2,000 augmented directional rows
because the head confused A-before-B with B-before-A, so the base corpus now
carries that contrast itself. Near misses break one guard clause each.
training/history/ regenerates older corpora.
"""
import hashlib
import json
import random
from pathlib import Path
from probara_ai.contract import STATE_FORMAT, VERSION, training_row
from probara_ai.guard import DIRECTIONAL, NONE, PILE_UP_MIN, RARE_MAX, dependency_guard

SEED = 20261010
HERE = Path(__file__).parent
CORPUS_VERSION = "probara-controlled-v4"

# One entry per scenario draw; repeats are weights. 12 of 27 are supported
# (the guard's timing, variant and structural bases), 2 shared_upstream, 1
# no_dependency and 12 insufficient_evidence near misses, one per guard
# clause. "peers" is the production false positive (same-role peers restarted
# one after another) and is labelled insufficient_evidence: the catalogue names
# correlation among same-role peers there, while shared_upstream is kept for
# pile-ups of otherwise eligible pairs without a variant or backing-resource
# relation, and for known common upstreams.
#
# A backing resource never depends on the service that uses it: timing that
# says so ("contrary_timing", the service check alerting sooner) is no basis,
# and the pair gets the structural direction when a rare tag or a name prefix
# corroborates the roles, or insufficient_evidence when only a small group does
# ("contrary_timing_group"). Timing and variants that agree with the roles
# ("timing" with the resource failing first, "variant_service": a service
# named after its database) keep their basis.
DEPENDENCY_KINDS = (
    *("timing",) * 5, "contrary_timing", *("variant",) * 2, "variant_service", *("structural",) * 3,
    "common_upstream", "pile_up",
    "independent",
    "two_co_fires", "reverse", "no_direction", "peers", "peers", "common_tag_only", "large_group_only",
    "one_token_prefix", "small_group_structural", "both_infra", "extension_first", "contrary_timing_group",
)
SUPPORTED = {"timing", "contrary_timing", "variant", "variant_service", "structural"}
LABELS = {"common_upstream": "shared_upstream", "pile_up": "shared_upstream", "independent": "no_dependency"}
# Production reports the median lead inside 600-second buckets: one range for
# every scenario with a direction, so no magnitude is a label cue.
MAX_LEAD = 599
MAX_CO_FIRES = 40


def _pair(**values):
    """Full v2 pair evidence; the tag/group booleans follow their populations."""
    e = {"co_fires": 0, "a_leads_seconds": 0, "reverse_co_fires": 0, "shared_service": False, "shared_group": False,
         "naming_signal": False, "common_upstream": False, "a_windows": 0, "b_windows": 0, "window_monitors": 0,
         "shared_tag_monitors": 0, "shared_group_monitors": 0, "shared_name_prefix": 0, "a_extends_b": False,
         "b_extends_a": False, "same_type": False, "a_infra": False, "b_infra": False, "peers": False}
    e.update(values)
    e["shared_service"] = e["shared_tag_monitors"] > 0
    e["shared_group"] = e["shared_group_monitors"] > 0
    return e


def _rare(rng):
    return rng.randint(2, RARE_MAX)


def _common_tag(rng):
    return rng.randint(RARE_MAX + 1, 100)


def _large_group(rng):
    return rng.randint(RARE_MAX + 1, 200)


def _naming(rng, prefix):
    # A shared first token of at least three characters is the usual case; a
    # shared token elsewhere in the names is occasional.
    return rng.random() < (.9 if prefix else .15)


def _lead(rng, a_first):
    return rng.randint(1, MAX_LEAD) * (1 if a_first else -1)


def _count(rng, low, high):
    """Co-fire windows: mostly low..high, a minority up to MAX_CO_FIRES, so a large count is no cue."""
    return rng.randint(low, high) if rng.random() < .8 else rng.randint(high + 1, MAX_CO_FIRES)


def _reverse(rng, co):
    """Reverse windows are among the co-fires and, with a reported (median) lead, under half of them."""
    return rng.randint(1, (co - 1) // 2)


def _activity(rng, e, co, lead, crowd, reverse=0):
    """Co-fire counters with consistent per-monitor window totals.

    The advisor reports 0 or at least 2 co-firing windows; every co-firing
    window (reverse ones included) is an alert window of both monitors.
    """
    assert co != 1 and reverse <= co
    e.update(co_fires=co, a_leads_seconds=lead if co else 0, reverse_co_fires=reverse if co else 0,
             window_monitors=crowd if co else 0,
             a_windows=co + rng.randint(0, 15), b_windows=co + rng.randint(0, 15))
    return e


def _same_role(rng, e, infra=None):
    """Equal roles (no structural basis): neither or both backing resources."""
    infra = rng.random() < .2 if infra is None else infra
    e.update(a_infra=infra, b_infra=infra, same_type=infra or rng.random() < .5)
    return e


def _timing(rng, a_first):
    """Repeated one-way windows in a quiet window, rarely corroborated, roles neutral or agreeing."""
    mode = rng.choice(("tag", "group", "prefix", "tag+group", "tag+prefix", "group+prefix", "all"))
    tag = _rare(rng) if "tag" in mode or mode == "all" else (_common_tag(rng) if rng.random() < .25 else 0)
    group = _rare(rng) if "group" in mode or mode == "all" else (_large_group(rng) if rng.random() < .25 else 0)
    prefix = rng.randint(2, 4) if "prefix" in mode or mode == "all" else rng.choice((0, 0, 1))
    e = _pair(shared_tag_monitors=tag, shared_group_monitors=group, shared_name_prefix=prefix,
              naming_signal=_naming(rng, prefix))
    _activity(rng, e, _count(rng, 3, 12), _lead(rng, a_first), rng.randint(2, PILE_UP_MIN - 1))
    if rng.random() < .65:
        _same_role(rng, e, infra=False)
    else:
        # The monitor failing first is the backing resource: timing and roles agree.
        e.update(a_infra=a_first, b_infra=not a_first, same_type=False)
    return e


def _contrary_timing(rng, a_infra, structural):
    """Timing that makes the backing resource the dependent (the service alerts sooner).

    structural=True: a rare tag or a name prefix corroborates, and the pair
    gets the role direction; False: only a small group does (insufficient).
    """
    if structural:
        mode = rng.choice(("tag", "prefix", "both"))
        tag = _rare(rng) if mode != "prefix" else rng.choice((0, _common_tag(rng)))
        prefix = rng.randint(2, 4) if mode != "tag" else rng.choice((0, 0, 1))
        group = rng.choice((0, 0, _rare(rng), _large_group(rng)))
    else:
        tag, prefix, group = rng.choice((0, _common_tag(rng))), rng.choice((0, 1)), _rare(rng)
    e = _pair(shared_tag_monitors=tag, shared_group_monitors=group, shared_name_prefix=prefix,
              naming_signal=_naming(rng, prefix), a_infra=a_infra, b_infra=not a_infra)
    # The service (the non-infra side) fails first.
    return _activity(rng, e, _count(rng, 3, 12), _lead(rng, not a_infra), rng.randint(2, PILE_UP_MIN - 1))


def _variant(rng, a_first, service=False):
    """A name and its variant ("X" / "X DEGRADED"): the base fails first, pile-up or not.

    service=True: mixed roles agreeing with the variant direction, the base a
    backing resource and the variant a service named after it.
    """
    base_tokens = rng.randint(1, 4)
    e = _pair(shared_tag_monitors=rng.choice((0, _rare(rng), _common_tag(rng))),
              shared_group_monitors=rng.choice((0, _rare(rng), _large_group(rng))),
              shared_name_prefix=base_tokens, naming_signal=_naming(rng, base_tokens),
              a_extends_b=not a_first, b_extends_a=a_first)
    crowd = rng.randint(PILE_UP_MIN, 14) if rng.random() < .5 else rng.randint(2, PILE_UP_MIN - 1)
    co = _count(rng, 2, 6)
    _activity(rng, e, co, _lead(rng, a_first), crowd)
    # The variant alerts about as often as it co-fires; its base more often.
    variant_side = "b_windows" if a_first else "a_windows"
    e[variant_side] = co + rng.randint(0, 3)
    if service:
        e.update(a_infra=a_first, b_infra=not a_first, same_type=False)
        return e
    return _same_role(rng, e, infra=rng.random() < .5)


def _structural(rng, a_infra):
    """A service and its backing resource sharing a rare tag or a name prefix; timing ignored."""
    mode = rng.choice(("tag", "prefix", "both"))
    tag = _rare(rng) if mode != "prefix" else (_common_tag(rng) if rng.random() < .3 else 0)
    prefix = rng.randint(2, 4) if mode != "tag" else rng.choice((0, 0, 1))
    e = _pair(shared_tag_monitors=tag, shared_group_monitors=rng.choice((0, 0, _rare(rng), _large_group(rng))),
              shared_name_prefix=prefix, naming_signal=_naming(rng, prefix), a_infra=a_infra, b_infra=not a_infra)
    # No co-fires, or timing the guard cannot use (contrary or noisy).
    noise = rng.choice(("none", "none", "weak", "reverse", "pile_up", "simultaneous"))
    if noise == "weak":
        _activity(rng, e, 2, _lead(rng, rng.random() < .5), rng.randint(2, 8))
    elif noise == "reverse":
        co = _count(rng, 3, 8)
        _activity(rng, e, co, _lead(rng, rng.random() < .5), rng.randint(2, PILE_UP_MIN - 1), reverse=_reverse(rng, co))
    elif noise == "pile_up":
        _activity(rng, e, _count(rng, 3, 10), _lead(rng, rng.random() < .5), rng.randint(PILE_UP_MIN, 12))
    elif noise == "simultaneous":
        _activity(rng, e, _count(rng, 3, 8), 0, rng.randint(2, PILE_UP_MIN - 1))
    else:
        e.update(a_windows=rng.randint(0, 25), b_windows=rng.randint(0, 25))
    if e["co_fires"] == 0 and rng.random() < .2:
        # The service's name extends the resource's ("billing-db" / "billing-db-api").
        e.update({"b_extends_a" if a_infra else "a_extends_b": True},
                 shared_name_prefix=max(prefix, 2), naming_signal=True)
    return e


def _near_miss(rng, kind, a_first):
    """insufficient_evidence: each breaks exactly one clause of an otherwise supported pair."""
    if kind == "peers":
        if rng.random() < .7:
            # Rolling restart of same-role peers: a few one-way windows among
            # many failing monitors, a common tag and a one-token prefix.
            e = _pair(shared_tag_monitors=_common_tag(rng), shared_name_prefix=1, naming_signal=True, peers=True)
            _activity(rng, e, _count(rng, 2, 4), _lead(rng, a_first), rng.randint(PILE_UP_MIN, 12))
        else:
            # Quiet peers with a rare tag: peers alone blocks the timing basis.
            prefix = rng.choice((0, 1, 2))
            e = _pair(shared_tag_monitors=_rare(rng), shared_name_prefix=prefix, naming_signal=_naming(rng, prefix),
                      peers=True)
            _activity(rng, e, _count(rng, 3, 8), _lead(rng, a_first), rng.randint(2, PILE_UP_MIN - 1))
        return _same_role(rng, e, infra=rng.random() < .2) | {"same_type": True}
    if kind == "contrary_timing_group":
        return _contrary_timing(rng, a_first, structural=False)
    if kind == "small_group_structural":
        prefix = rng.choice((0, 1))
        e = _pair(shared_tag_monitors=rng.choice((0, _common_tag(rng))), shared_group_monitors=_rare(rng),
                  shared_name_prefix=prefix, naming_signal=_naming(rng, prefix), a_infra=a_first, b_infra=not a_first)
        _activity(rng, e, rng.choice((0, 2)), _lead(rng, rng.random() < .5), rng.randint(2, 8))
        return e
    if kind == "both_infra":
        prefix = rng.choice((0, 1, 2, 3))
        e = _pair(shared_tag_monitors=_rare(rng) if prefix < 2 or rng.random() < .5 else 0,
                  shared_name_prefix=prefix, naming_signal=_naming(rng, prefix))
        _activity(rng, e, rng.choice((0, 2)), _lead(rng, a_first), rng.randint(2, 8))
        return _same_role(rng, e, infra=True)
    if kind == "extension_first":
        # The longer name fails first: not a variant failing after its base.
        mode = rng.choice(("one_token", "two_co_fires", "pile_up"))
        prefix = 1 if mode == "one_token" else rng.randint(2, 4)
        e = _pair(shared_tag_monitors=rng.choice((0, _common_tag(rng))),
                  shared_group_monitors=rng.choice((0, _large_group(rng))),
                  shared_name_prefix=prefix, naming_signal=_naming(rng, prefix),
                  a_extends_b=a_first, b_extends_a=not a_first)
        co = 2 if mode == "two_co_fires" else _count(rng, 2, 6)
        crowd = rng.randint(PILE_UP_MIN, 12) if mode == "pile_up" else rng.randint(2, PILE_UP_MIN - 1)
        _activity(rng, e, co, _lead(rng, a_first), crowd)
        return _same_role(rng, e)
    e = _same_role(rng, _timing(rng, a_first), infra=False)
    if kind == "two_co_fires":
        # Missing history: 0 or 2 co-firing windows (timing needs 3).
        _activity(rng, e, 2 if rng.random() < .75 else 0, e["a_leads_seconds"], e["window_monitors"])
    elif kind == "reverse":
        e["reverse_co_fires"] = _reverse(rng, e["co_fires"])
    elif kind == "no_direction":
        e["a_leads_seconds"] = 0
    else:
        if kind == "common_tag_only":
            tag, group, prefix = (_common_tag(rng), rng.choice((0, _large_group(rng))), rng.choice((0, 1))) \
                if rng.random() < .75 else (0, 0, 0)
        elif kind == "large_group_only":
            tag, group, prefix = 0, _large_group(rng), 0
        else:  # one_token_prefix
            tag, group, prefix = 0, 0, 1
        e.update(shared_tag_monitors=tag, shared_service=tag > 0, shared_group_monitors=group, shared_group=group > 0,
                 shared_name_prefix=prefix, naming_signal=_naming(rng, prefix))
    return e


def dependency_example(rng, kind, a_first):
    """(evidence, label) for one variation of a dependency scenario kind.

    a_first: in supported kinds, A is the upstream (label b_depends_a), which
    for contrary_timing means A is the backing resource while B fails first;
    elsewhere it only sets the direction of the near miss.
    """
    if kind in SUPPORTED:
        build = {"timing": _timing, "variant": _variant, "structural": _structural,
                 "variant_service": lambda r, first: _variant(r, first, service=True),
                 "contrary_timing": lambda r, first: _contrary_timing(r, first, structural=True)}[kind]
        return build(rng, a_first), DIRECTIONAL[a_first]
    if kind == "common_upstream":
        e = (_timing if rng.random() < .6 else _structural)(rng, a_first)
        e["common_upstream"] = True
        return e, LABELS[kind]
    if kind == "pile_up":
        # Otherwise timing-eligible, but many monitors fail in the same windows.
        e = _same_role(rng, _timing(rng, a_first), infra=rng.random() < .2)
        e["window_monitors"] = rng.randint(PILE_UP_MIN, 14)
        return e, LABELS[kind]
    if kind == "independent":
        e = _pair(a_windows=rng.randint(0, 20), b_windows=rng.randint(0, 20))
        a_infra, b_infra = rng.random() < .3, rng.random() < .3
        return e | {"a_infra": a_infra, "b_infra": b_infra,
                    "same_type": a_infra == b_infra and rng.random() < .5}, LABELS[kind]
    return _near_miss(rng, kind, a_first), "insufficient_evidence"


def check_label(evidence, label):
    """The guard and the label agree: directional exactly when the guard supports that direction."""
    basis, supported = dependency_guard(evidence)
    if (label in DIRECTIONAL or basis != NONE) and supported != label:
        raise AssertionError(f"label {label} disagrees with the guard ({basis}, {supported}): {evidence}")


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
            kind = rng.randrange(12 if task == "rca" else len(DEPENDENCY_KINDS))
            if task == "dependency":
                scenario = DEPENDENCY_KINDS[kind]
                # Supported groups alternate direction across variations.
                start = rng.randrange(2)
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
                    a_first = (start + variant) % 2 == 1 if scenario in SUPPORTED else rng.random() < .5
                    e, label = dependency_example(rng, scenario, a_first)
                    check_label(e, label)
                row = training_row(task, e, label, f"{task}-{group}")
                # Randomise option order in all partitions and every variation.
                opts = list(row["questions"][task]["criteria"].items()); rng.shuffle(opts)
                row["questions"][task]["criteria"] = dict(opts)
                row["provenance"] = {"topology": name, "anchor_epoch": timestamp, "variation": variant, "scenario_kind": kind}
                if task == "dependency":
                    row["provenance"]["scenario"] = scenario
                by_split[split_of[group]].append(row)
        for split, rows in by_split.items():
            path = output / f"{task}-{split}.jsonl"
            path.write_text("".join(json.dumps(row, sort_keys=False) + "\n" for row in rows))
    provenance = {"corpus_version": CORPUS_VERSION, "state_format": STATE_FORMAT, "catalogue": VERSION,
                  "generator_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
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

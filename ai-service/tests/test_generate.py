"""Corpus probara-controlled-v4: deterministic, guard-consistent, internally consistent evidence."""
import collections
import hashlib
import json
from pathlib import Path
import runpy
import pytest
from probara_ai.contract import PAIR_FIELDS, STATE_FORMAT, VERSION, options, state_text
from probara_ai.guard import DIRECTIONAL, NONE, PILE_UP_MIN, RARE_MAX, dependency_guard

ROOT = Path(__file__).resolve().parents[1]
GENERATOR = runpy.run_path(str(ROOT / "training/generate.py"))
PARTS = ("train", "calibration", "test")


@pytest.fixture(scope="module")
def corpus(tmp_path_factory):
    out = tmp_path_factory.mktemp("corpus")
    GENERATOR["generate"](out)
    return out


def rows(corpus, task, part):
    return [json.loads(line) for line in (corpus / f"{task}-{part}.jsonl").read_text().splitlines()]


def test_generation_is_byte_for_byte_deterministic(corpus, tmp_path):
    GENERATOR["generate"](tmp_path)
    for path in sorted(corpus.iterdir()):
        assert (tmp_path / path.name).read_bytes() == path.read_bytes(), path.name


def test_rca_partitions_are_unchanged_from_probara_controlled_v3(corpus):
    # RCA generation and its RNG stream are untouched; format 3 renders RCA as format 2.
    recorded = json.loads((ROOT / "artifacts/probara-laya-v4/provenance.json").read_text())["partition_sha256"]
    for part in PARTS:
        name = f"rca-{part}.jsonl"
        assert hashlib.sha256((corpus / name).read_bytes()).hexdigest() == recorded[name]


def test_provenance_block(corpus):
    provenance = json.loads((corpus / "provenance.json").read_text())
    assert provenance["corpus_version"] == "probara-controlled-v4"
    assert (provenance["state_format"], provenance["catalogue"]) == (STATE_FORMAT, VERSION)
    assert provenance["generator_sha256"] == hashlib.sha256((ROOT / "training/generate.py").read_bytes()).hexdigest()
    assert (provenance["examples_per_task"], provenance["group_count_per_task"], provenance["variations_per_group"],
            provenance["split"]) == (5000, 1000, 5, [0.6, 0.2, 0.2])
    assert set(provenance["partition_sha256"]) == {f"{t}-{p}.jsonl" for t in ("rca", "dependency") for p in PARTS}


def test_partitions_split_by_group_with_shuffled_options(corpus):
    for task in ("rca", "dependency"):
        groups = {}
        orders = set()
        for part, count in zip(PARTS, (3000, 1000, 1000), strict=True):
            part_rows = rows(corpus, task, part)
            assert len(part_rows) == count
            assert len({row["group"] for row in part_rows}) == count // 5
            groups[part] = {row["group"] for row in part_rows}
            for row in part_rows:
                criteria = row["questions"][task]["criteria"]
                assert dict(sorted(criteria.items())) == dict(sorted(options(task).items()))
                assert row["state"] == state_text(task, row["evidence"])
                orders.add(tuple(criteria))
        assert not groups["train"] & groups["calibration"] and not groups["train"] & groups["test"]
        assert not groups["calibration"] & groups["test"]
        assert len(orders) > 50


def dependency_rows(corpus):
    return [row for part in PARTS for row in rows(corpus, "dependency", part)]


def test_labels_are_directional_exactly_when_the_guard_supports_them(corpus):
    for row in dependency_rows(corpus):
        label = row["expected"]["dependency"]
        basis, supported = dependency_guard(row["evidence"])
        for direction in DIRECTIONAL:
            assert (label == direction) == (supported == direction), (row["group"], row["provenance"], basis)
        if basis == NONE:
            assert label not in DIRECTIONAL


def test_evidence_is_internally_consistent(corpus):
    for row in dependency_rows(corpus):
        e = row["evidence"]
        where = (row["group"], row["provenance"]["variation"])
        assert set(e) == PAIR_FIELDS, where
        co, lead, reverse = e["co_fires"], e["a_leads_seconds"], e["reverse_co_fires"]
        # The advisor reports 0 or at least 2 co-firing windows (its SQL minimum).
        assert co != 1 and co <= GENERATOR["MAX_CO_FIRES"], where
        if co:
            assert e["window_monitors"] >= 2, where
        else:
            assert e["window_monitors"] == lead == reverse == 0, where
        # co_fires counts every shared window, reverse ones included, and the
        # lead is their median: reverse windows are under half when a lead is reported.
        assert reverse <= co and (lead == 0 or 2 * reverse < co), where
        assert abs(lead) <= GENERATOR["MAX_LEAD"], where
        assert e["a_windows"] >= co and e["b_windows"] >= co, where
        assert e["shared_service"] == (e["shared_tag_monitors"] > 0), where
        assert e["shared_group"] == (e["shared_group_monitors"] > 0), where
        assert e["shared_tag_monitors"] != 1 and e["shared_group_monitors"] != 1, where  # both monitors hold it
        assert not (e["a_extends_b"] and e["b_extends_a"]), where
        if e["a_extends_b"] or e["b_extends_a"]:
            assert e["shared_name_prefix"] >= 1, where
        if e["peers"]:
            assert e["same_type"] and e["shared_tag_monitors"] > 0, where
            assert not (e["a_extends_b"] or e["b_extends_a"]), where
        if e["same_type"]:
            assert e["a_infra"] == e["b_infra"], where


def test_every_scenario_kind_reaches_every_partition_with_its_label(corpus):
    seen = collections.defaultdict(set)
    for part in PARTS:
        for row in rows(corpus, "dependency", part):
            seen[(row["provenance"]["scenario"], part)].add(row["expected"]["dependency"])
    kinds = set(GENERATOR["DEPENDENCY_KINDS"])
    expected = {**{kind: set(DIRECTIONAL) for kind in GENERATOR["SUPPORTED"]},
                "common_upstream": {"shared_upstream"}, "pile_up": {"shared_upstream"}, "independent": {"no_dependency"}}
    for kind in kinds:
        for part in PARTS:
            assert seen[(kind, part)] == expected.get(kind, {"insufficient_evidence"}), (kind, part)


def test_supported_groups_mirror_their_direction(corpus):
    by_group = collections.defaultdict(list)
    for row in dependency_rows(corpus):
        if row["provenance"]["scenario"] in GENERATOR["SUPPORTED"]:
            by_group[row["group"]].append(row["expected"]["dependency"])
    assert by_group and all(set(labels) == set(DIRECTIONAL) for labels in by_group.values())
    counts = collections.Counter(label for labels in by_group.values() for label in labels)
    assert abs(counts["a_depends_b"] - counts["b_depends_a"]) < 0.1 * sum(counts.values())


def scenario_rows(corpus, kind):
    return [row["evidence"] for row in dependency_rows(corpus) if row["provenance"]["scenario"] == kind]


def rare(n):
    return 0 < n <= RARE_MAX


def test_lead_and_count_ranges_carry_no_label_cue(corpus):
    """Every directional scenario draws |lead| from 1..MAX_LEAD; large co-fire counts occur on both sides."""
    by_label = collections.defaultdict(list)
    for row in dependency_rows(corpus):
        e = row["evidence"]
        if e["a_leads_seconds"]:
            by_label[row["expected"]["dependency"] in DIRECTIONAL].append(e)
    for directional, rows_ in by_label.items():
        leads = [abs(e["a_leads_seconds"]) for e in rows_]
        assert min(leads) < 20 and max(leads) > 580, directional
        assert max(e["co_fires"] for e in rows_) > 30, directional
    peers = [abs(e["a_leads_seconds"]) for e in scenario_rows(corpus, "peers")]
    assert max(peers) > 400 and min(peers) < 30


def test_scenarios_cover_the_brief(corpus):
    timing = scenario_rows(corpus, "timing")
    counts = [e["co_fires"] for e in timing]
    assert set(range(3, 13)) <= set(counts) and max(counts) > 30 and sum(c > 12 for c in counts) < .3 * len(counts)
    # Timing roles are neutral or agree: the backing resource fails first.
    assert all(e["a_infra"] == e["b_infra"] or e["a_infra"] == (e["a_leads_seconds"] > 0) for e in timing)
    assert any(e["a_infra"] != e["b_infra"] for e in timing)
    assert all(e["window_monitors"] < PILE_UP_MIN for e in timing)
    corroboration = collections.Counter((rare(e["shared_tag_monitors"]), rare(e["shared_group_monitors"]),
                                         e["shared_name_prefix"] >= 2) for e in timing)
    assert set(corroboration) == {c for c in ((t, g, p) for t in (0, 1) for g in (0, 1) for p in (0, 1)) if any(c)}
    variant = scenario_rows(corpus, "variant")
    assert any(e["window_monitors"] >= PILE_UP_MIN for e in variant) and any(e["window_monitors"] < 4 for e in variant)
    assert max(e["co_fires"] for e in variant) > 6
    # A service named after its backing resource: the base is the resource and fails first.
    for e in scenario_rows(corpus, "variant_service"):
        base_infra = e["b_infra"] if e["a_extends_b"] else e["a_infra"]
        assert base_infra and e["a_infra"] != e["b_infra"] and dependency_guard(e)[0] == "variant"
    # Timing that makes the backing resource the dependent: structural when a
    # rare tag or prefix corroborates, otherwise no basis at all.
    for kind, basis in (("contrary_timing", "structural"), ("contrary_timing_group", NONE)):
        rows_ = scenario_rows(corpus, kind)
        assert rows_ and all(e["a_infra"] != e["b_infra"] and e["co_fires"] >= 3 and not e["reverse_co_fires"]
                             and e["a_infra"] == (e["a_leads_seconds"] < 0)
                             and dependency_guard(e)[0] == basis for e in rows_), kind
    structural = scenario_rows(corpus, "structural")
    assert any(e["co_fires"] == 0 for e in structural)
    assert any(e["reverse_co_fires"] for e in structural) and any(e["window_monitors"] >= PILE_UP_MIN for e in structural)
    assert any(e["co_fires"] and (e["a_leads_seconds"] > 0) == e["b_infra"] for e in structural)  # contrary timing
    assert any(e["window_monitors"] >= PILE_UP_MIN for e in scenario_rows(corpus, "pile_up"))
    assert all(e["common_upstream"] for e in scenario_rows(corpus, "common_upstream"))
    assert all(e["co_fires"] == 0 for e in scenario_rows(corpus, "independent"))
    # The production false positive: rolling restarts of same-role peers.
    rolling = [e for e in scenario_rows(corpus, "peers") if e["window_monitors"] >= PILE_UP_MIN]
    assert rolling and all(e["co_fires"] >= 2 and 11 <= e["shared_tag_monitors"] <= 100
                           and e["shared_name_prefix"] == 1 and e["peers"] for e in rolling)
    assert sum(e["co_fires"] <= 4 for e in rolling) > .7 * len(rolling)
    assert all(e["a_infra"] and e["b_infra"] for e in scenario_rows(corpus, "both_infra"))
    assert all((e["a_extends_b"] and e["a_leads_seconds"] > 0) or (e["b_extends_a"] and e["a_leads_seconds"] < 0)
               for e in scenario_rows(corpus, "extension_first"))
    assert {e["co_fires"] for e in scenario_rows(corpus, "two_co_fires")} == {0, 2}
    assert all(e["reverse_co_fires"] for e in scenario_rows(corpus, "reverse"))
    assert all(e["a_leads_seconds"] == 0 for e in scenario_rows(corpus, "no_direction"))
    assert all(e["shared_name_prefix"] == 1 for e in scenario_rows(corpus, "one_token_prefix"))
    assert all(e["shared_group_monitors"] > RARE_MAX for e in scenario_rows(corpus, "large_group_only"))
    assert all(rare(e["shared_group_monitors"]) and e["a_infra"] != e["b_infra"]
               for e in scenario_rows(corpus, "small_group_structural"))


def test_check_label_refuses_a_label_the_guard_contradicts():
    check = GENERATOR["check_label"]
    timing = {"co_fires": 4, "a_leads_seconds": 60, "window_monitors": 2, "shared_tag_monitors": 3}
    check(timing, "b_depends_a")
    for wrong in ("a_depends_b", "insufficient_evidence", "shared_upstream"):
        with pytest.raises(AssertionError, match="disagrees with the guard"):
            check(timing, wrong)
    with pytest.raises(AssertionError):
        check({}, "a_depends_b")

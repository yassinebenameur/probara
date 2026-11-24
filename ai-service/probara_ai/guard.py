"""The dependency evidence guard: which measured basis, if any, supports a pair.

One definition in Python, used by training metrics, the conservative rules
baseline, the corpus generator's label check and the reports. Its Go twin in
shared/ai must agree on every case of shared/ai/testdata/dependency-guard-cases.json
and every row of dependency-regression.json (tests/test_guard.py). A
suggestion counts only when this guard returns a basis, the model's best
choice equals the guard's label and the calibrated score passes the threshold.

Missing fields read as zero/false, so a seven-field pair from an older caller
can only be supported by nothing: every basis needs a v2 field.
"""

RARE_MAX = 10
# window_monitors at or above this is a pile-up: many monitors failing in the
# same windows (timing needs window_monitors <= PILE_UP_MIN - 1).
PILE_UP_MIN = 4
NONE, ABSTAIN = "none", "abstain"
DIRECTIONAL = ("a_depends_b", "b_depends_a")


def rare(population):
    """A shared tag or group held by few enough monitors to corroborate a pair."""
    return 0 < population <= RARE_MAX


def dependency_guard(evidence):
    """(basis, label): basis is timing, variant, structural or none (label "abstain")."""
    def number(key):
        return evidence.get(key, 0)

    def flag(key):
        return evidence.get(key, False) is True

    if flag("common_upstream"):
        return NONE, ABSTAIN
    co, lead, reverse = number("co_fires"), number("a_leads_seconds"), number("reverse_co_fires")
    peers = flag("peers")
    tag, group, prefix = number("shared_tag_monitors"), number("shared_group_monitors"), number("shared_name_prefix")
    a_extends_b, b_extends_a = flag("a_extends_b"), flag("b_extends_a")
    # 1. variant: "X DEGRADED" fails after "X", so the variant depends on its base.
    a_infra, b_infra = flag("a_infra"), flag("b_infra")

    def against_roles(label):
        # A backing resource never depends on the service that uses it. A
        # timing or variant direction that says so is detection latency (the
        # service check alerts sooner), so that basis does not apply and the
        # pair falls through to the structural rule or abstains.
        return a_infra != b_infra and ((label == "a_depends_b" and a_infra) or (label == "b_depends_a" and b_infra))

    if a_extends_b != b_extends_a and co >= 2 and reverse == 0 and lead != 0:
        label = "a_depends_b" if a_extends_b and lead < 0 else "b_depends_a" if b_extends_a and lead > 0 else None
        if label and not against_roles(label):
            return "variant", label
        # The extension fails first, or the roles contradict: fall through.
    # 2. timing: repeated one-way windows, not a pile-up, not peers, rarely corroborated.
    corroborated = rare(tag) or rare(group) or prefix >= 2
    if (co >= 3 and reverse == 0 and lead != 0 and number("window_monitors") < PILE_UP_MIN and not peers
            and corroborated):
        label = "b_depends_a" if lead > 0 else "a_depends_b"
        if not against_roles(label):
            return "timing", label
    # 3. structural: a service and its backing resource sharing a rare tag or a
    # name prefix; a small group alone is not enough, and timing is ignored.
    if a_infra != b_infra and not peers and (rare(tag) or prefix >= 2):
        return "structural", "a_depends_b" if b_infra else "b_depends_a"
    return NONE, ABSTAIN

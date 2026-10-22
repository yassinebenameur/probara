"""One evidence/option contract for training and deployed CPU inference."""
import json
from pathlib import Path

CATALOGUE_PATH = Path(__file__).resolve().parents[2] / "shared/ai/catalogue.json"
if not CATALOGUE_PATH.exists():
    CATALOGUE_PATH = Path(__file__).with_name("catalogue.json")
CATALOGUE = json.loads(CATALOGUE_PATH.read_text())
VERSION = CATALOGUE["version"]
RCA_FIELDS = {
    "checks", "failures", "dns_failures", "connection_failures", "tls_failures",
    "protocol_failures", "slow_checks", "baseline_ms", "max_latency_ms", "max_dns_ms",
    "max_connect_ms", "max_tls_ms", "max_ttfb_ms", "certificate_expired", "certificate_changed",
    "resource_pressure", "host_metrics_available", "upstream_failed_before", "maintenance_overlap",
    "conflict", "missing_checks",
}
PAIR_FIELDS = {"co_fires", "a_leads_seconds", "reverse_co_fires", "shared_service", "shared_group", "naming_signal", "common_upstream"}
BOOL_FIELDS = {"certificate_expired", "certificate_changed", "resource_pressure", "host_metrics_available", "upstream_failed_before", "maintenance_overlap", "conflict", "missing_checks", "shared_service", "shared_group", "naming_signal", "common_upstream"}


def options(task):
    return {key: value["description"] for key, value in CATALOGUE[task].items()}


def validate_evidence(task, evidence):
    fields = RCA_FIELDS if task == "rca" else PAIR_FIELDS
    if not isinstance(evidence, dict) or set(evidence) - fields:
        raise ValueError("evidence contains fields outside the analysis allowlist")
    for key, value in evidence.items():
        if key in BOOL_FIELDS:
            if type(value) is not bool:
                raise ValueError("boolean evidence expected")
        elif type(value) is not int or abs(value) > 100000000:
            raise ValueError("bounded integer evidence expected")
        elif key != "a_leads_seconds" and value < 0:
            raise ValueError("negative measurement")
    if task == "rca" and ("checks" not in evidence or "failures" not in evidence or evidence["failures"] > evidence["checks"]):
        raise ValueError("invalid check counts")


# STATE_FORMAT versions the evidence-to-text rendering. A checkpoint is trained
# on one format and the runtime must render the same one (manifest
# "state_format"); changing the wording below is a new model, not a refactor.
STATE_FORMAT = 2


def _state_text_v1(task, evidence):
    """Format 1 (the first artifact; controlled corpora v1 and v2): generic field labels.

    Numeric zero and false values imply absence only. Frozen: artifacts
    trained on it render exactly this text.
    """
    if task == "dependency":
        lead = evidence.get("a_leads_seconds", 0)
        text = [f"Co-firing windows: {evidence.get('co_fires', 0)}."]
        text.append(f"{'A precedes B' if lead > 0 else 'B precedes A'} by {abs(lead)} seconds." if lead else "No clear time direction.")
        labels = {"reverse_co_fires": "Reverse-direction windows", "shared_service": "Shared service tag", "shared_group": "Shared group", "naming_signal": "Shared naming token", "common_upstream": "Known common upstream"}
    else:
        text = [f"Checks: {evidence['checks']}. Failed observations: {evidence['failures']}."]
        labels = {key: key.replace("_", " ") for key in RCA_FIELDS - {"checks", "failures"}}
    for key in sorted(labels):
        value = evidence.get(key)
        if value:
            text.append(f"{labels[key]}: {'observed' if value is True else value}.")
    return " ".join(text)


def _state_text_v2(task, evidence):
    """Compact factual text in the catalogue's own vocabulary.

    Every sentence restates measured evidence only, phrased with the words the
    closed choices use (for example "Repeated A-before-B failures" for the
    direction the a/b dependency options describe), so a frozen encoder can
    match state to option. Zero and false values are omitted: absence only.
    No names, free text, endpoints, secrets or invented diagnostic prose.
    """
    e = evidence
    out = []
    if task == "dependency":
        co = e.get("co_fires", 0)
        lead = e.get("a_leads_seconds", 0)
        if lead > 0:
            out.append(f"Repeated A-before-B failures in {co} co-firing windows; A fails first by {lead} seconds.")
        elif lead < 0:
            out.append(f"Repeated B-before-A failures in {co} co-firing windows; B fails first by {-lead} seconds.")
        else:
            out.append(f"{co} co-firing windows with no clear time direction.")
        if co < 2:
            out.append(f"Missing history: only {co} co-firing windows.")
        if e.get("reverse_co_fires"):
            out.append(f"Conflicting direction: {e['reverse_co_fires']} reverse-direction windows.")
        corroboration = [label for key, label in (("shared_service", "shared service tag"), ("shared_group", "shared group")) if e.get(key)]
        if corroboration:
            out.append("Corroborating service evidence: " + " and ".join(corroboration) + ".")
        else:
            out.append("No corroborating service evidence.")
        if e.get("common_upstream"):
            out.append("Known common upstream evidence explains both failures.")
        if e.get("naming_signal"):
            out.append("Shared naming token only; names alone cannot establish a dependency.")
        return " ".join(out)
    out.append(f"Checks: {e['checks']}. Failed observations: {e['failures']}.")
    if e.get("missing_checks"):
        out.append("Missing check evidence; inconclusive.")
    if e.get("conflict"):
        out.append("Conflicting failure domains; inconclusive.")
    counts = (("dns_failures", "Measured DNS resolution failures"),
              ("connection_failures", "Measured connection refusals or unreachable routes"),
              ("tls_failures", "Measured TLS handshake or certificate validity errors"),
              ("protocol_failures", "Measured application or protocol status and assertion failures"))
    domain = False
    for key, label in counts:
        if e.get(key):
            out.append(f"{label}: {e[key]}.")
            domain = True
    if e.get("certificate_expired"):
        out.append("Certificate validity error: expired certificate.")
        domain = True
    if e.get("slow_checks"):
        latency = f"Measured latency increase against healthy checks in this window: {e['slow_checks']} slow checks"
        if e.get("baseline_ms"):
            latency += f", healthy baseline {e['baseline_ms']} ms"
        if e.get("max_latency_ms"):
            latency += f", maximum {e['max_latency_ms']} ms"
        out.append(latency + ".")
        domain = True
    phases = [f"{label} {e[key]} ms" for key, label in (("max_dns_ms", "DNS"), ("max_connect_ms", "connect"), ("max_tls_ms", "TLS"), ("max_ttfb_ms", "first byte")) if e.get(key)]
    if phases:
        out.append("Slowest phases: " + ", ".join(phases) + ".")
    if e.get("resource_pressure") and e.get("host_metrics_available"):
        out.append("Fresh monitored host metrics show high CPU or memory utilization during failures.")
        domain = True
    elif e.get("host_metrics_available"):
        out.append("Fresh host metrics available without high utilization.")
    if e.get("upstream_failed_before"):
        out.append("Configured upstream has measured failures before dependent failures.")
        domain = True
    if e.get("maintenance_overlap"):
        out.append("A maintenance window overlaps measured downtime.")
        domain = True
    if e.get("certificate_changed"):
        out.append("Certificate changed but remains valid.")
    if e["failures"] and not domain and not e.get("conflict") and not e.get("missing_checks"):
        out.append("No measured failure domain; inconclusive.")
    return " ".join(out)


_RENDERERS = {1: _state_text_v1, 2: _state_text_v2}
STATE_FORMATS = frozenset(_RENDERERS)


def state_text(task, evidence, state_format=STATE_FORMAT):
    """Render validated evidence in the given format (default: the current one).

    An artifact renders with its manifest "state_format" (missing means 1), so
    older artifacts keep the exact text they were trained and validated on.
    """
    renderer = _RENDERERS.get(state_format) if type(state_format) is int else None
    if renderer is None:
        raise ValueError(f"Unknown evidence state format: {state_format!r}")
    validate_evidence(task, evidence)
    return renderer(task, evidence)


def artifact_state_format(manifest):
    """The rendering an artifact was trained on: manifest "state_format", missing means 1."""
    state_format = manifest.get("state_format", 1)
    if type(state_format) is not int or state_format not in STATE_FORMATS:
        raise ValueError(f"Artifact uses an unknown evidence state format: {state_format!r}")
    return state_format


def training_row(task, evidence, label, group, state_format=STATE_FORMAT):
    """One corpus row; new corpora use the current STATE_FORMAT.

    state_format exists for frozen history (training/history/) only: it must
    regenerate an older artifact's corpus byte for byte.
    """
    return {"task": task, "group": group, "evidence": evidence,
            "state": state_text(task, evidence, state_format),
            "questions": {task: {"type": "choice", "instructions": "Select the hypothesis supported by the measured evidence; abstain when inconclusive.", "criteria": options(task)}},
            "expected": {task: label}}


# Operator label exports (shared/ai/labelexport.go WriteLabelExport) render
# "state" in format 1, and the Go side stays frozen there. Training corpora
# are STATE_FORMAT, so an export is re-rendered from its evidence, never used
# with its exported text (partitions.verify_rendering would refuse it).
LABEL_EXPORT_FORMAT = 1


def from_label_export(row, state_format=STATE_FORMAT):
    """An exported label row as a training row in state_format.

    Refuses a row whose exported state is not this contract's format-1 text
    for its evidence: the Go twin and Python have drifted, or the row was edited.
    """
    task = row["task"]
    if row["state"] != state_text(task, row["evidence"], LABEL_EXPORT_FORMAT):
        raise ValueError("Label export state is not the format-1 rendering of its evidence")
    question = row["questions"][task]
    if question.get("type") != "choice" or row["expected"][task] not in question["criteria"]:
        raise ValueError("Label export row has no valid choice answer")
    return {**row, "state": state_text(task, row["evidence"], state_format)}

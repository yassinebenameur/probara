"""Render the chart's local AI cases and assert the wiring (run from the repo root).

Needs helm and PyYAML. Positive cases are also linted. Negative cases must
make `helm template` fail. The contract under test (values.yaml documents it):
the bundled service is default-on; localAI.externalUrl replaces it, so it
requires localAI.enabled=false and an explicit token (secrets.localAiToken or an
existing Secret), since a derived one cannot match an operator-run service. The
token always resolves through secretEnv under the canonical key local_ai_token.
Objects are matched by name suffix so a chart rename does not break the cases.
"""
import subprocess
import sys
import yaml

CHART = "helm/monitoring-platform"
RELEASE = "ci"
TOKEN = "ci-local-ai-token-0123456789"
LOCATION = ["worker.locations[0].name=eu", "worker.locations[0].locationId=00000000-0000-0000-0000-0000000000e1",
            "worker.locations[0].existingSecret=eu-location"]
failures = []


def helm(command, sets):
    args = ["helm", command, *([RELEASE] if command == "template" else []), CHART]
    for value in sets:
        args += ["--set", value]
    return subprocess.run(args, capture_output=True, text=True)


def render(sets):
    result = helm("template", sets)
    if result.returncode:
        raise AssertionError(f"helm template failed:\n{result.stderr.strip()}")
    lint = helm("lint", sets)
    if lint.returncode:
        raise AssertionError(f"helm lint failed:\n{lint.stdout.strip()}\n{lint.stderr.strip()}")
    return [doc for doc in yaml.safe_load_all(result.stdout) if doc]


def objects(docs, kind):
    return {d["metadata"]["name"]: d for d in docs if d.get("kind") == kind}


def deployment(docs, suffix):
    matches = [d for name, d in objects(docs, "Deployment").items() if name.endswith(suffix)]
    assert len(matches) == 1, f"expected one Deployment ending in {suffix}, found {len(matches)}"
    return matches[0]


def env(workload):
    containers = workload["spec"]["template"]["spec"]["containers"]
    return {e["name"]: e for c in containers for e in c.get("env", [])}


def local_ai_objects(docs):
    return sorted(f"{d['kind']}/{d['metadata']['name']}" for d in docs
                  if d["metadata"]["name"].endswith("-local-ai"))


def secret_ref(variable, optional=None):
    """The token's secretKeyRef as {name, key}, checking its `optional` flag.

    Clients (api, worker) reference it optionally so a pre-local-AI existing
    Secret does not wedge them on upgrade; the local-ai pod's stays required
    (see localAIEnv in _helpers.tpl). optional=None skips the check.
    """
    assert "value" not in variable, f"{variable['name']} rendered as a literal value"
    ref = dict(variable["valueFrom"]["secretKeyRef"])
    flag = bool(ref.pop("optional", False))
    if optional is not None:
        assert flag == optional, f"{variable['name']} optional={flag}, expected {optional}"
    return ref


def token_ref(docs, suffix):
    return secret_ref(env(deployment(docs, suffix))["LOCAL_AI_TOKEN"], optional=suffix != "-local-ai")


def chart_secret_keys(docs):
    keys = set()
    for secret in objects(docs, "Secret").values():
        keys |= set(secret.get("stringData") or {}) | set(secret.get("data") or {})
    return keys


def assert_boundary(docs):
    # Only the API, the platform worker and the inference service carry the
    # LOCAL_AI_* client/service env; scheduler, status page and location
    # workers never. The alerter never calls the service and carries only the
    # two kill switches (alerterAIEnv), LOCAL_AI_DISABLED and AI_TRIAGE_ENABLED,
    # which stop suspected-cause follow-ups.
    allowed = ("-api", "-worker", "-local-ai")
    for name, workload in {**objects(docs, "Deployment"), **objects(docs, "StatefulSet"),
                           **objects(docs, "Job")}.items():
        variables = env(workload)
        carried = sorted(k for k in variables if k.startswith("LOCAL_AI_"))
        if name.endswith("-alerter"):
            if carried != ["LOCAL_AI_DISABLED"]:
                raise AssertionError(f"{name} must receive only LOCAL_AI_DISABLED, got {carried}")
            if "AI_TRIAGE_ENABLED" not in variables:
                raise AssertionError(f"{name} must receive AI_TRIAGE_ENABLED")
            continue
        if carried and not name.endswith(allowed):
            raise AssertionError(f"{name} must not receive {carried}")


def alerter_disabled(docs):
    """The alerter's LOCAL_AI_DISABLED, which must match the api's value."""
    return env(deployment(docs, "-alerter"))["LOCAL_AI_DISABLED"]["value"]


def case_default():
    docs = render(LOCATION)
    kinds = sorted(name.split("/")[0] for name in local_ai_objects(docs))
    assert kinds == ["Deployment", "NetworkPolicy", "Service"], local_ai_objects(docs)
    secret_name = token_ref(docs, "-local-ai")["name"]
    assert "local_ai_token" in chart_secret_keys(docs), "chart-managed Secret lacks local_ai_token"
    for suffix in ("-api", "-worker"):
        variables = env(deployment(docs, suffix))
        assert variables["LOCAL_AI_DISABLED"]["value"] == "false", suffix
        service = next(name for name in objects(docs, "Service") if name.endswith("-local-ai"))
        assert variables["LOCAL_AI_URL"]["value"] == f"http://{service}:8090", suffix
        assert token_ref(docs, suffix) == {"name": secret_name, "key": "local_ai_token"}, suffix
    policy = next(d for n, d in objects(docs, "NetworkPolicy").items() if n.endswith("-local-ai"))["spec"]
    assert policy["egress"] == [], "local AI must have no egress"
    assert alerter_disabled(docs) == "false", "alerter LOCAL_AI_DISABLED differs from the api's"
    assert_boundary(docs)


def case_disabled():
    docs = render(["localAI.enabled=false", *LOCATION])
    assert not local_ai_objects(docs), local_ai_objects(docs)
    assert "local_ai_token" not in chart_secret_keys(docs), "disabled chart still manages local_ai_token"
    for suffix in ("-api", "-worker"):
        variables = env(deployment(docs, suffix))
        assert variables["LOCAL_AI_DISABLED"]["value"] == "true", suffix
        assert "LOCAL_AI_URL" not in variables and "LOCAL_AI_TOKEN" not in variables, suffix
    assert alerter_disabled(docs) == "true", "alerter LOCAL_AI_DISABLED differs from the api's"
    assert_boundary(docs)


def case_external_url():
    docs = render(["localAI.enabled=false", "localAI.externalUrl=https://x", f"secrets.localAiToken={TOKEN}", *LOCATION])
    assert not local_ai_objects(docs), f"external endpoint must not deploy the bundled service: {local_ai_objects(docs)}"
    assert "local_ai_token" in chart_secret_keys(docs), "external token not kept in the chart-managed Secret"
    for suffix in ("-api", "-worker"):
        variables = env(deployment(docs, suffix))
        assert variables["LOCAL_AI_DISABLED"]["value"] == "false", suffix
        assert variables["LOCAL_AI_URL"]["value"] == "https://x", suffix
        assert token_ref(docs, suffix)["key"] == "local_ai_token", suffix
    assert alerter_disabled(docs) == "false", "alerter LOCAL_AI_DISABLED differs from the api's"
    assert_boundary(docs)


def case_existing_secret():
    docs = render(["secrets.existingSecret=probara-external", *LOCATION])
    assert "local_ai_token" not in chart_secret_keys(docs), "chart generated a token despite secrets.existingSecret"
    for suffix in ("-api", "-worker", "-local-ai"):
        ref = token_ref(docs, suffix)
        assert ref == {"name": "probara-external", "key": "local_ai_token"}, (suffix, ref)
    assert_boundary(docs)


def case_dedicated_token_secret():
    docs = render(["secrets.localAiTokenExistingSecret=ai-token", "secrets.localAiTokenExistingSecretKey=token", *LOCATION])
    assert "local_ai_token" not in chart_secret_keys(docs), "chart generated a token despite localAiTokenExistingSecret"
    for suffix in ("-api", "-worker", "-local-ai"):
        ref = token_ref(docs, suffix)
        assert ref == {"name": "ai-token", "key": "token"}, (suffix, ref)


def case_external_url_existing_secret():
    docs = render(["localAI.enabled=false", "localAI.externalUrl=http://probara-ai.ai.svc:8090",
                   "secrets.localAiTokenExistingSecret=ai-token"])
    assert not local_ai_objects(docs), local_ai_objects(docs)
    for suffix in ("-api", "-worker"):
        ref = token_ref(docs, suffix)
        assert ref == {"name": "ai-token", "key": "local_ai_token"}, (suffix, ref)


def expect_failure(sets, why, needle):
    # The needle proves the intended guard fired, not an unrelated render error.
    result = helm("template", sets)
    if result.returncode == 0:
        raise AssertionError(f"helm template succeeded but must fail: {why}")
    error = next((line for line in result.stderr.splitlines() if line.startswith("Error:")), result.stderr.strip())
    if needle not in result.stderr:
        raise AssertionError(f"failed for another reason than {why!r} (expected {needle!r}): {error}")
    print(f"    rejected as expected: {error}")


CASES = {
    "default (bundled, derived token)": case_default,
    "localAI.enabled=false": case_disabled,
    "localAI.externalUrl=https://x (enabled=false, literal token)": case_external_url,
    "localAI.externalUrl with secrets.localAiTokenExistingSecret": case_external_url_existing_secret,
    "secrets.existingSecret": case_existing_secret,
    "secrets.localAiTokenExistingSecret": case_dedicated_token_secret,
    "reject: enabled and externalUrl": lambda: expect_failure(
        ["localAI.externalUrl=https://x", f"secrets.localAiToken={TOKEN}"], "bundled service and external endpoint together",
        "mutually exclusive"),
    "reject: externalUrl without token": lambda: expect_failure(
        ["localAI.enabled=false", "localAI.externalUrl=https://x"], "external endpoint with no token to present",
        "required with localAI.externalUrl"),
    "reject: externalUrl with a path": lambda: expect_failure(
        ["localAI.enabled=false", "localAI.externalUrl=https://x/v1", f"secrets.localAiToken={TOKEN}"],
        "externalUrl must be scheme and host only", "localAI.externalUrl must be"),
    "reject: token shorter than 16 characters": lambda: expect_failure(
        ["secrets.localAiToken=short"], "token below the 16-character minimum", "at least 16 characters"),
    "reject: underivable token (adminJwtSecret from an existing Secret)": lambda: expect_failure(
        ["secrets.adminJwtSecretExistingSecret=jwt"], "the chart cannot read the JWT secret to derive the token",
        "derived from secrets.adminJwtSecret"),
}

for title, check in CASES.items():
    try:
        check()
        print(f"ok    {title}")
    except Exception as error:  # report every case, then fail once
        failures.append(title)
        print(f"FAIL  {title}: {error}")
        print(f"::error title=Helm local AI case failed::{title}: {' '.join(str(error).split())}")

sys.exit(1 if failures else 0)

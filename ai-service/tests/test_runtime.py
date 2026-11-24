import os
import socket
from types import SimpleNamespace
import pytest
from probara_ai.contract import options
from probara_ai.guard import dependency_guard
from probara_ai.runtime import Runtime
from probara_ai.server import create_app
from fastapi.testclient import TestClient


# A pair the v5 guard supports on its timing basis (A fails first, rare shared
# tag, quiet windows): the bundled model must agree with the guard on it.
SUPPORTED_PAIR = {"co_fires": 7, "a_leads_seconds": 30, "reverse_co_fires": 0, "shared_service": True,
                  "shared_group": False, "naming_signal": False, "common_upstream": False, "a_windows": 9,
                  "b_windows": 11, "window_monitors": 2, "shared_tag_monitors": 4, "shared_group_monitors": 0,
                  "shared_name_prefix": 0, "a_extends_b": False, "b_extends_a": False, "same_type": False,
                  "a_infra": False, "b_infra": False, "peers": False}


def test_bundled_pair_is_guard_supported():
    assert dependency_guard(SUPPORTED_PAIR) == ("timing", "b_depends_a")


def test_actual_token_budget_rejects_without_truncation():
    runtime = Runtime.__new__(Runtime)
    runtime.max_len, runtime.head_len, runtime.state_format = 260, 256, 3
    runtime.tokenizer = SimpleNamespace(encode=lambda text, **kw: list(range(5)))
    d = SimpleNamespace(task="rca", evidence={"checks": 0, "failures": 0, "missing_checks": True}, choices=options("rca"))
    with pytest.raises(ValueError, match="token budget"):
        runtime.prepare([d])


@pytest.mark.checkpoint
@pytest.mark.skipif(not os.environ.get("LOCAL_AI_ARTIFACT"), reason="set LOCAL_AI_ARTIFACT for real bundled CPU verification")
def test_bundled_cpu_inference_without_network(monkeypatch):
    def denied(*args, **kwargs):
        raise AssertionError("Runtime attempted external network access")
    monkeypatch.setattr(socket.socket, "connect", denied)
    monkeypatch.setenv("LOCAL_AI_TOKEN", "offline-inference-test-token")
    runtime = Runtime(os.environ["LOCAL_AI_ARTIFACT"])
    assert next(runtime.model.parameters()).device.type == "cpu"
    with TestClient(create_app(lambda: runtime)) as client:
        r = client.post("/v1/decisions", headers={"Authorization": "Bearer offline-inference-test-token"}, json={"decisions": [
            {"id": "offline_rca", "task": "rca", "choices": options("rca"), "evidence": {"checks": 12, "failures": 4, "dns_failures": 4}},
            {"id": "offline_pair", "task": "dependency", "choices": options("dependency"), "evidence": SUPPORTED_PAIR},
        ]})
        assert r.status_code == 200, r.text
        assert r.json()["model"]["engine"] == "laya-cpu"
        assert all(abs(sum(v["probabilities"].values()) - 1) < 1e-5 for v in r.json()["results"])
        for result, task, expected in zip(r.json()["results"], ("rca", "dependency"), ("dns_failure", "b_depends_a"), strict=True):
            probabilities = result["probabilities"]
            assert max(probabilities, key=probabilities.get) == expected
            assert probabilities[expected] >= r.json()["thresholds"][task]

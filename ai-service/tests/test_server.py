import time
import pytest
from fastapi.testclient import TestClient
from probara_ai.server import create_app
from probara_ai.contract import options

TOKEN = "test-internal-service-token"
HEADERS = {"Authorization": "Bearer " + TOKEN}


class FakeRuntime:
    identity = {"engine": "laya-cpu", "version": "fixture", "sha256": "a" * 64, "catalogue": "probara-evidence-v1"}
    def prepare(self, decisions):
        if any(d.choices != options(d.task) for d in decisions): raise ValueError("invalid choices")
        return decisions
    def predict(self, decisions, _):
        return {"model": self.identity, "thresholds": {"rca": .9, "dependency": .9}, "results": [{"id": d.id, "probabilities": {k: 1.0 if k == "insufficient_evidence" else 0.0 for k in d.choices}} for d in decisions]}


def batch(id="request1"):
    return {"decisions": [{"id": id, "task": "rca", "evidence": {"checks": 0, "failures": 0, "missing_checks": True}, "choices": options("rca")}]}


def test_auth_readiness_and_decisions(monkeypatch):
    monkeypatch.setenv("LOCAL_AI_TOKEN", TOKEN)
    with TestClient(create_app(FakeRuntime)) as client:
        assert client.get("/healthz").status_code == 200
        assert client.get("/readyz").status_code == 401
        assert client.get("/readyz", headers=HEADERS).status_code == 200
        assert client.post("/v1/decisions", json=batch()).status_code == 401
        r = client.post("/v1/decisions", json=batch(), headers=HEADERS)
        assert r.status_code == 200 and r.json()["results"][0]["probabilities"]["insufficient_evidence"] == 1
        assert "probara_ai_completed_total 1" in client.get("/metrics").text


def test_malformed_queue_and_body(monkeypatch):
    monkeypatch.setenv("LOCAL_AI_TOKEN", TOKEN)
    with TestClient(create_app(FakeRuntime)) as client:
        b = batch(); b["decisions"] *= 2
        assert client.post("/v1/decisions", json=b, headers=HEADERS).status_code == 422
        b = batch(); b["decisions"][0]["evidence"]["config"] = {"api_key": "secret"}
        assert client.post("/v1/decisions", json=b, headers=HEADERS).status_code == 422
        b = batch(); b["decisions"][0]["choices"] = {"x": "cause"}
        assert client.post("/v1/decisions", json=b, headers=HEADERS).status_code == 422
        assert client.post("/v1/decisions", content=b"x" * (512 * 1024 + 1), headers=HEADERS).status_code == 413


def test_disabled_installation(monkeypatch):
    monkeypatch.setenv("LOCAL_AI_TOKEN", TOKEN); monkeypatch.setenv("LOCAL_AI_DISABLED", "true")
    with TestClient(create_app(lambda: pytest.fail("disabled installation loaded model"))) as client:
        assert client.get("/readyz", headers=HEADERS).status_code == 503
        assert client.post("/v1/decisions", json=batch(), headers=HEADERS).status_code == 503


def test_saturation_and_queue_deadline(monkeypatch):
    import concurrent.futures
    import threading
    entered, release = threading.Event(), threading.Event()
    class SlowRuntime(FakeRuntime):
        def predict(self, decisions, items):
            entered.set()
            assert release.wait(5)
            return super().predict(decisions, items)
    monkeypatch.setenv("LOCAL_AI_TOKEN", TOKEN)
    monkeypatch.setenv("LOCAL_AI_QUEUE_CAPACITY", "1")
    monkeypatch.setenv("LOCAL_AI_QUEUE_TIMEOUT_SECONDS", "0.15")
    with TestClient(create_app(SlowRuntime)) as client, concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        first = pool.submit(client.post, "/v1/decisions", json=batch("active"), headers=HEADERS)
        assert entered.wait(3)
        second = pool.submit(client.post, "/v1/decisions", json=batch("queued"), headers=HEADERS)
        time.sleep(.03)
        assert client.post("/v1/decisions", json=batch("overflow"), headers=HEADERS).status_code == 429
        assert second.result(timeout=3).status_code == 429
        release.set()
        assert first.result(timeout=3).status_code == 200
        assert client.post("/v1/decisions", json=batch("recovered"), headers=HEADERS).status_code == 200


def test_token_budget_overflow(monkeypatch):
    class OverflowRuntime(FakeRuntime):
        def prepare(self, decisions):
            raise ValueError("Evidence exceeds model token budget; split the decision")
    monkeypatch.setenv("LOCAL_AI_TOKEN", TOKEN)
    with TestClient(create_app(OverflowRuntime)) as client:
        r = client.post("/v1/decisions", json=batch(), headers=HEADERS)
        assert r.status_code == 422 and "token budget" in r.json()["detail"]


def test_cancelled_request_retains_cpu_slot(monkeypatch):
    import asyncio
    import threading
    import httpx
    entered, release = threading.Event(), threading.Event()
    class SlowRuntime(FakeRuntime):
        def predict(self, decisions, items):
            entered.set()
            assert release.wait(5)
            return super().predict(decisions, items)
    monkeypatch.setenv("LOCAL_AI_TOKEN", TOKEN)
    monkeypatch.setenv("LOCAL_AI_QUEUE_TIMEOUT_SECONDS", "0.05")
    async def check():
        app = create_app(SlowRuntime)
        async with app.router.lifespan_context(app), httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url="http://fixture") as client:
            task = asyncio.create_task(client.post("/v1/decisions", json=batch("cancelled"), headers=HEADERS))
            assert await asyncio.to_thread(entered.wait, 3)
            task.cancel()
            await asyncio.sleep(.01)
            task.cancel()
            await asyncio.sleep(.01)
            assert "probara_ai_admitted 1" in (await client.get("/metrics")).text
            waiting = await client.post("/v1/decisions", json=batch("waiting"), headers=HEADERS)
            assert waiting.status_code == 429
            release.set()
            with pytest.raises(asyncio.CancelledError):
                await task
            assert (await client.post("/v1/decisions", json=batch("recovered"), headers=HEADERS)).status_code == 200
    asyncio.run(check())

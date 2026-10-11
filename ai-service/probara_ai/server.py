"""Authenticated bounded queue with one CPU inference process."""
import asyncio
from concurrent.futures import ThreadPoolExecutor
from contextlib import asynccontextmanager
import hmac
import os
import time
from typing import Literal
from fastapi import FastAPI, Depends, HTTPException, Request
from fastapi.responses import JSONResponse, PlainTextResponse
from pydantic import BaseModel, ConfigDict, Field
from probara_ai.contract import validate_evidence


class Decision(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    id: str = Field(min_length=1, max_length=128, pattern=r"^[A-Za-z0-9_-]+$")
    task: Literal["rca", "dependency"]
    evidence: dict
    choices: dict[str, str]


class Batch(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    decisions: list[Decision] = Field(min_length=1, max_length=50)


def create_app(runtime_factory=None):
    token = os.environ.get("LOCAL_AI_TOKEN", "")
    if len(token) < 16:
        raise ValueError("LOCAL_AI_TOKEN must have at least 16 characters")
    capacity = int(os.environ.get("LOCAL_AI_QUEUE_CAPACITY", "4"))
    threads = int(os.environ.get("LOCAL_AI_THREADS", "4"))
    if not 1 <= capacity <= 32 or not 1 <= threads <= 4:
        raise ValueError("Invalid bounded queue or CPU thread configuration")
    deadline = float(os.environ.get("LOCAL_AI_QUEUE_TIMEOUT_SECONDS", "5"))
    if not 0 < deadline <= 30:
        raise ValueError("Invalid queue timeout")
    admitted = 0
    metrics = {"completed": 0, "rejected": 0, "failed": 0, "inference_seconds": 0.0}
    runtime = None
    executor = ThreadPoolExecutor(max_workers=1, thread_name_prefix="laya-cpu")
    lock = asyncio.Lock()
    closing = False

    @asynccontextmanager
    async def lifespan(app):
        nonlocal runtime, closing
        if os.environ.get("LOCAL_AI_DISABLED", "false").lower() in ("true", "1"):
            runtime = None
        elif runtime_factory:
            runtime = runtime_factory()
        else:
            from probara_ai.runtime import Runtime
            runtime = Runtime(os.environ.get("LOCAL_AI_MODEL_DIR", "/app/model"), threads)
        try:
            yield
        finally:
            closing = True
            await asyncio.to_thread(executor.shutdown, wait=True, cancel_futures=True)
            runtime = None

    app = FastAPI(lifespan=lifespan, docs_url=None, redoc_url=None, openapi_url=None)

    async def authenticate(request: Request):
        header = request.headers.get("Authorization", "")
        if not hmac.compare_digest(header.encode(), ("Bearer " + token).encode()):
            raise HTTPException(401, "Invalid internal service token")

    @app.get("/healthz")
    async def health():
        return {"status": "ok"}

    @app.get("/readyz", dependencies=[Depends(authenticate)])
    async def ready():
        if runtime is None or closing:
            raise HTTPException(503, "Model unavailable")
        return {"model": runtime.identity}

    @app.get("/metrics")
    async def prometheus():
        lines = [f"probara_ai_{k}{'_total' if k != 'inference_seconds' else '_sum'} {v}" for k, v in metrics.items()]
        lines += [f"probara_ai_admitted {admitted}", f"probara_ai_ready {int(runtime is not None and not closing)}"]
        return PlainTextResponse("\n".join(lines) + "\n")

    @app.post("/v1/decisions", dependencies=[Depends(authenticate)])
    async def decide(batch: Batch, request: Request):
        nonlocal admitted
        if runtime is None or closing:
            raise HTTPException(503, "Model unavailable")
        ids = [d.id for d in batch.decisions]
        if len(set(ids)) != len(ids):
            raise HTTPException(422, "Duplicate request IDs")
        try:
            for d in batch.decisions:
                validate_evidence(d.task, d.evidence)
        except ValueError as exc:
            raise HTTPException(422, str(exc)) from None
        if admitted >= capacity + 1:
            metrics["rejected"] += 1
            raise HTTPException(429, "Inference queue full", headers={"Retry-After": "5"})
        admitted += 1
        acquired = False
        try:
            try:
                await asyncio.wait_for(lock.acquire(), timeout=deadline)
                acquired = True
            except TimeoutError:
                metrics["rejected"] += 1
                raise HTTPException(429, "Inference queue deadline exceeded", headers={"Retry-After": "5"}) from None
            if await request.is_disconnected():
                raise HTTPException(499, "Request cancelled")
            try:
                items = runtime.prepare(batch.decisions)
            except ValueError as exc:
                raise HTTPException(422, str(exc)) from None
            started = time.perf_counter()
            future = asyncio.get_running_loop().run_in_executor(executor, runtime.predict, batch.decisions, items)
            # Once CPU execution starts, retain the slot until it finishes even
            # when the HTTP caller cancels. Another batch must never overlap it.
            try:
                result = await asyncio.shield(future)
            except asyncio.CancelledError:
                # A second cancellation must not cancel the asyncio future and
                # release admission while its executor thread is still running.
                while not future.done():
                    try:
                        await asyncio.shield(future)
                    except asyncio.CancelledError:
                        continue
                    except Exception:
                        break
                if not future.cancelled():
                    future.exception()  # Consume failures after caller cancellation.
                raise
            except Exception:
                metrics["failed"] += 1
                raise HTTPException(503, "CPU inference failed") from None
            metrics["inference_seconds"] += time.perf_counter() - started
            metrics["completed"] += 1
            return result
        finally:
            if acquired:
                lock.release()
            admitted -= 1

    class BodyLimit:
        def __init__(self, app): self.app = app
        async def __call__(self, scope, receive, send):
            if scope["type"] != "http" or scope.get("path") != "/v1/decisions":
                return await self.app(scope, receive, send)
            chunks = []; size = 0
            while True:
                message = await receive()
                if message["type"] == "http.disconnect": return
                size += len(message.get("body", b""))
                if size > 512 * 1024:
                    return await JSONResponse({"detail": "Request exceeds evidence budget"}, status_code=413)(scope, receive, send)
                chunks.append(message)
                if not message.get("more_body", False): break
            async def replay():
                if chunks: return chunks.pop(0)
                return await receive()
            return await self.app(scope, replay, send)
    app.add_middleware(BodyLimit)
    return app

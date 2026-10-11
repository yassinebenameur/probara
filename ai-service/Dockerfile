# Local AI inference image. Build context: repository root.
#
#   laya-wheel    verified checkout of the pinned laya commit -> wheel (only stage with git)
#   base-model    pinned Hugging Face base, fetched by revision and sha256-verified
#   dependencies  hash-locked CPU runtime in /opt/venv
#   artifact      head-only artifact + base -> composed checkpoint, then the
#                 release gate: the build fails unless manifest and CPU evaluation
#                 both say release_approved (probara_ai.runtime.verify_artifact)
#   runtime       no git, compiler or pip; offline; non-root uid/gid 1000;
#                 writes nothing outside /tmp (read-only root filesystem friendly)
#
# MODEL_VERSION names the committed directory ai-service/artifacts/<version>.
ARG PYTHON_IMAGE=python:3.13.3-slim@sha256:56a11364ffe0fee3bd60af6d6d5209eba8a99c2c16dc4c7c5861dc06261503cc

FROM ${PYTHON_IMAGE} AS python-base
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 PIP_DISABLE_PIP_VERSION_CHECK=1 PIP_NO_INPUT=1

FROM python-base AS laya-wheel
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/*
COPY ai-service/requirements-build.lock /build/requirements-build.lock
COPY ai-service/training/base-lock.json /build/base-lock.json
RUN python -m pip install --require-hashes --only-binary=:all: -r /build/requirements-build.lock
# The commit comes from base-lock.json (upstream_commit), the single pin shared
# with training. A full commit id names the exact tree, so the checkout is
# content-verified; pip cannot hash a VCS requirement directly.
RUN set -eu; \
    commit="$(python -c 'import json; print(json.load(open("/build/base-lock.json"))["upstream_commit"])')"; \
    git init -q /build/laya; \
    git -C /build/laya fetch -q --depth 1 https://github.com/NandhaKishorM/laya.git "$commit"; \
    git -C /build/laya checkout -q --detach FETCH_HEAD; \
    test "$(git -C /build/laya rev-parse HEAD)" = "$commit"; \
    python -m pip wheel --no-deps --no-build-isolation --wheel-dir /wheels /build/laya

FROM python-base AS base-model
COPY ai-service/requirements-fetch.lock /build/requirements-fetch.lock
RUN python -m pip install --require-hashes --only-binary=:all: -r /build/requirements-fetch.lock
COPY ai-service/training/upstream.py ai-service/training/base-lock.json ai-service/UPSTREAM-LICENSE /build/fetch/
# snapshot_download(revision=<commit sha>), then sha256 of every locked file
# (weights, tokenizer, encoder config, model card) before anything uses them.
RUN HF_HUB_DISABLE_TELEMETRY=1 python /build/fetch/upstream.py --output /base

FROM python-base AS dependencies
COPY ai-service/requirements.lock /build/requirements.lock
COPY --from=laya-wheel /wheels /wheels
RUN python -m venv /opt/venv && \
    /opt/venv/bin/pip install --require-hashes --only-binary=:all: \
      --extra-index-url https://download.pytorch.org/whl/cpu -r /build/requirements.lock && \
    /opt/venv/bin/pip install --no-deps --no-index /wheels/laya-*.whl && \
    /opt/venv/bin/pip check && \
    /opt/venv/bin/pip uninstall -y pip

FROM python-base AS artifact
ARG MODEL_VERSION=probara-laya-v1
ENV PATH=/opt/venv/bin:$PATH HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 USE_TF=0 CUDA_VISIBLE_DEVICES=""
COPY --from=dependencies /opt/venv /opt/venv
COPY ai-service/probara_ai /app/probara_ai
COPY shared/ai/catalogue.json /app/probara_ai/catalogue.json
COPY ai-service/training/compose_artifact.py ai-service/training/upstream.py ai-service/training/base-lock.json /build/training/
COPY ai-service/artifacts/${MODEL_VERSION} /build/artifact
COPY --from=base-model /base /build/base
RUN PYTHONPATH=/app:/build/training python /build/training/compose_artifact.py compose \
      --artifact /build/artifact --base /build/base --output /app/model
# Release gate: an unapproved, tampered or CPU-unvalidated artifact fails the build.
RUN PYTHONPATH=/app python -c 'from probara_ai.runtime import verify_artifact; verify_artifact("/app/model")'

FROM python-base AS runtime
RUN python -m pip uninstall -y pip && \
    groupadd --gid 1000 probara && \
    useradd --uid 1000 --gid 1000 --no-create-home --home-dir /tmp --shell /usr/sbin/nologin probara
COPY --from=dependencies /opt/venv /opt/venv
# Root-owned and world-readable: the service user can read but never modify code or weights.
COPY --from=artifact /app /app
ENV PATH=/opt/venv/bin:$PATH PYTHONPATH=/app HOME=/tmp HF_HOME=/tmp/huggingface \
    HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 HF_HUB_DISABLE_TELEMETRY=1 USE_TF=0 \
    CUDA_VISIBLE_DEVICES="" LOCAL_AI_MODEL_DIR=/app/model LOCAL_AI_THREADS=4
WORKDIR /app
USER 1000:1000
EXPOSE 8090
HEALTHCHECK --interval=30s --timeout=5s --start-period=120s --retries=3 \
  CMD ["python", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8090/healthz', timeout=3)"]
CMD ["python", "-m", "uvicorn", "probara_ai.server:create_app", "--factory", "--host", "0.0.0.0", "--port", "8090", "--workers", "1", "--limit-concurrency", "64", "--timeout-keep-alive", "5", "--no-access-log"]

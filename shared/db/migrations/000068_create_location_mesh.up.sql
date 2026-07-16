-- 000068_create_location_mesh.up.sql
-- Directed inter-location connectivity edges. One row per (source, target)
-- pair of mesh-participating locations; the scheduler syncs the row set from
-- locations.mesh_endpoint and drives probing via next_run_at (same SKIP LOCKED
-- contract as monitors.next_run_at). State is the per-edge consecutive-failures
-- machine (shared/monitorstate.Apply) — no quorum, an edge is its own subject.
CREATE TABLE location_mesh_state (
    tenant_id            UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    source_location_id   UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    target_location_id   UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    current_state        TEXT NOT NULL DEFAULT 'unknown'
        CHECK (current_state IN ('unknown', 'up', 'suspect', 'down')),
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_latency_ms      BIGINT,
    last_error           TEXT,
    last_check_at        TIMESTAMPTZ,
    last_state_change_at TIMESTAMPTZ,
    next_run_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (source_location_id, target_location_id),
    CHECK (source_location_id <> target_location_id)
);
CREATE INDEX idx_location_mesh_state_due    ON location_mesh_state(next_run_at);
CREATE INDEX idx_location_mesh_state_tenant ON location_mesh_state(tenant_id);
CREATE INDEX idx_location_mesh_state_target ON location_mesh_state(target_location_id);

-- Raw probe samples: latency history for edge charts, and the durable
-- idempotency anchor for JetStream at-least-once delivery (UNIQUE(job_id)
-- mirrors check_results' (job_id, result_source) dedupe from 000060 —
-- without it a redelivery would double-advance consecutive_failures).
-- Pruned by the scheduler retention loop alongside check_results.
CREATE TABLE mesh_probe_results (
    id                 UUID PRIMARY KEY,
    tenant_id          UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    source_location_id UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    target_location_id UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    job_id             UUID NOT NULL,
    status             TEXT NOT NULL CHECK (status IN ('success', 'failure', 'error')),
    latency_ms         BIGINT,
    error_message      TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_mesh_probe_results_job       ON mesh_probe_results(job_id);
CREATE INDEX idx_mesh_probe_results_edge_time        ON mesh_probe_results(source_location_id, target_location_id, created_at DESC);
CREATE INDEX idx_mesh_probe_results_tenant_time      ON mesh_probe_results(tenant_id, created_at);

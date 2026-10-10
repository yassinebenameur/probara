-- 000084_create_metric_store.up.sql
-- Generic OTel metric store backing agent monitors: the OTLP ingest endpoint
-- writes every reported series/sample here (replacing the fixed-schema
-- check_results.metrics_data blob), and the alerter/UI/status pages read it.
--
-- Layout: a small per-series registry (one row per distinct
-- monitor + metric name + attribute set, deduped by attr_hash) and a
-- range-partitioned samples table keyed by the registry id. Samples carry the
-- agent's data-point clock; availability/freshness never reads this table —
-- the heartbeat check_results row (server clock) owns that.

CREATE TABLE metric_series (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    monitor_id    UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    metric_name   TEXT NOT NULL,
    unit          TEXT NOT NULL DEFAULT '',
    metric_type   TEXT NOT NULL CHECK (metric_type IN ('gauge','sum')),
    is_monotonic  BOOLEAN NOT NULL DEFAULT FALSE,
    temporality   TEXT NOT NULL DEFAULT 'unspecified'
                  CHECK (temporality IN ('cumulative','delta','unspecified')),
    -- Canonical form: object with sorted keys, values stringified per OTLP
    -- AnyValue rules (shared/metricstore/hash.go is the single writer).
    attributes    JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- sha256 over metric_name + NUL + canonical attributes JSON.
    attr_hash     BYTEA NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Refreshed on ingest (throttled in code); freshness bounds for alert
    -- evaluation and status pages key on this, NOT on a samples scan.
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (monitor_id, attr_hash)
);
CREATE INDEX idx_metric_series_monitor_name ON metric_series(monitor_id, metric_name);
CREATE INDEX idx_metric_series_tenant ON metric_series(tenant_id);

-- Raw samples. Daily partitions are created ahead by the scheduler
-- (metric_partitions.go) and on demand by ingest (EnsurePartitions fallback);
-- retention is partition DROP, so there is deliberately no DEFAULT partition.
-- No FK to metric_series: series deletion (monitor purge, tenant cascade)
-- must batch-delete samples explicitly — an FK cascade across partitions is
-- unbounded work inside someone else's transaction.
CREATE TABLE metric_samples (
    series_id BIGINT NOT NULL,
    ts        TIMESTAMPTZ NOT NULL,
    value     DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (series_id, ts)
) PARTITION BY RANGE (ts);

-- Hourly downsampling so charts survive raw-sample retention. `increase` is
-- the reset-aware sum of positive deltas within the bucket (NULL for gauges);
-- rate(bucket) = increase / 3600 without needing the raw counter rows.
CREATE TABLE metric_rollups_hourly (
    series_id    BIGINT NOT NULL REFERENCES metric_series(id) ON DELETE CASCADE,
    bucket       TIMESTAMPTZ NOT NULL,
    sample_count INT NOT NULL,
    min_value    DOUBLE PRECISION NOT NULL,
    max_value    DOUBLE PRECISION NOT NULL,
    sum_value    DOUBLE PRECISION NOT NULL,
    first_value  DOUBLE PRECISION NOT NULL,
    last_value   DOUBLE PRECISION NOT NULL,
    increase     DOUBLE PRECISION,
    PRIMARY KEY (series_id, bucket)
);

-- Dirty-bucket ledger, same contract as rollup_dirty (000083): ingest marks
-- the (monitor, hour) of every committed sample batch in the same
-- transaction; the consumer rebuilds marked monitor-hours wholesale and its
-- delete is conditional on the marked_at it read, so re-marks mid-rebuild
-- survive for the next run. Monitor granularity (not series) keeps mark
-- volume low; a rebuild covers every series of the monitor-hour.
CREATE TABLE metric_rollup_dirty (
    monitor_id  UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    bucket_hour TIMESTAMPTZ NOT NULL,
    marked_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (monitor_id, bucket_hour)
);
CREATE INDEX idx_metric_rollup_dirty_bucket ON metric_rollup_dirty(bucket_hour);

-- Seed partitions: yesterday through three days ahead, named
-- metric_samples_YYYYMMDD (UTC bounds). The scheduler keeps extending this.
DO $$
DECLARE
    d DATE;
BEGIN
    FOR d IN SELECT generate_series((NOW() AT TIME ZONE 'utc')::date - 1,
                                    (NOW() AT TIME ZONE 'utc')::date + 3,
                                    '1 day')::date
    LOOP
        -- Bounds are UTC midnights independent of the server timezone.
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS metric_samples_%s PARTITION OF metric_samples FOR VALUES FROM (%L) TO (%L)',
            to_char(d, 'YYYYMMDD'),
            d::timestamp AT TIME ZONE 'utc',
            (d + 1)::timestamp AT TIME ZONE 'utc'
        );
    END LOOP;
END $$;

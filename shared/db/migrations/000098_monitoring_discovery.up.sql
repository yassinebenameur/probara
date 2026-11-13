CREATE TABLE discovery_integrations (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    -- Active namespaces: every snapshot must cover them, and a failure in any
    -- of them aborts the scan, so a lost Role never fakes deleted inventory.
    namespaces TEXT[] NOT NULL,
    -- Where the collector Deployment, ServiceAccount and Secret live: set at
    -- create, never re-derived from list order, and always active.
    home_namespace TEXT NOT NULL,
    -- Added but not scanned successfully yet: scanned all-or-nothing, a failure
    -- only skips them. Active plus pending are at most 20.
    pending_namespaces TEXT[] NOT NULL DEFAULT '{}',
    -- Last skip per pending namespace: {"ns": {"reason": …, "checked_at": …}}.
    pending_status JSONB NOT NULL DEFAULT '{}',
    location_id UUID REFERENCES locations(id) ON DELETE SET NULL,
    token_hash TEXT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 0,
    -- Last accepted snapshot: inventory staleness.
    last_seen_at TIMESTAMPTZ,
    -- Last snapshot that reached reconciliation, applied or refused for the
    -- inventory cap: the snapshot rate limit.
    last_attempt_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (home_namespace = ANY(namespaces)),
    UNIQUE (tenant_id, name),
    UNIQUE (tenant_id, id)
);
CREATE TABLE discovered_services (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    integration_id UUID NOT NULL,
    service_key TEXT NOT NULL,
    name TEXT NOT NULL,
    namespace TEXT NOT NULL,
    -- When the service stopped having a present resource (they went missing,
    -- or a grouping change moved them away); NULL while it has one. Drives
    -- its missing grace, and the retention of a resource-less service kept
    -- only because monitors are still linked to it.
    absent_since TIMESTAMPTZ,
    FOREIGN KEY (tenant_id, integration_id) REFERENCES discovery_integrations(tenant_id, id) ON DELETE CASCADE,
    UNIQUE (integration_id, service_key),
    UNIQUE (tenant_id, id)
);
CREATE TABLE discovered_resources (
    integration_id UUID NOT NULL REFERENCES discovery_integrations(id) ON DELETE CASCADE,
    uid TEXT NOT NULL,
    service_id UUID NOT NULL REFERENCES discovered_services(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    namespace TEXT NOT NULL,
    suggestions JSONB NOT NULL DEFAULT '[]',
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    missing_since TIMESTAMPTZ,
    PRIMARY KEY (integration_id, uid)
);
CREATE INDEX discovered_resources_service ON discovered_resources(service_id);
CREATE TABLE discovery_monitor_links (
    tenant_id UUID NOT NULL,
    service_id UUID NOT NULL,
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    suggestion_key TEXT,
    FOREIGN KEY (tenant_id, service_id) REFERENCES discovered_services(tenant_id, id) ON DELETE CASCADE,
    PRIMARY KEY(service_id, monitor_id),
    UNIQUE(service_id, suggestion_key)
);
-- Monitor hard deletes cascade into the links; without this the cascade scans.
CREATE INDEX discovery_monitor_links_monitor ON discovery_monitor_links(monitor_id);

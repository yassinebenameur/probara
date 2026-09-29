CREATE TABLE discovery_integrations (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    namespaces TEXT[] NOT NULL,
    location_id UUID REFERENCES locations(id) ON DELETE SET NULL,
    token_hash TEXT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 0,
    last_seen_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
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

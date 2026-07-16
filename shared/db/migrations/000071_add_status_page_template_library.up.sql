-- 000071_add_status_page_template_library.up.sql
-- Tenant-level library of named, reusable status page templates. Library
-- entries are admin-side source storage only: applying one to a status page
-- copies its source into that page's draft (status_page_templates), so the
-- per-page draft → publish → history pipeline stays the single path to the
-- public renderer and library edits never change a live page.
CREATE TABLE status_page_template_library (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT,
    source      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT status_page_template_library_name_not_blank CHECK (btrim(name) <> '')
);

CREATE UNIQUE INDEX uq_status_page_template_library_tenant_name
    ON status_page_template_library(tenant_id, lower(name));
CREATE INDEX idx_status_page_template_library_tenant
    ON status_page_template_library(tenant_id);

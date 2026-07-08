-- 000070_add_status_page_templates.up.sql
-- Custom UI templates for public status pages. A page has at most one draft
-- and one published template; earlier published versions are kept as
-- 'archived' so a bad publish can be reverted. version is assigned at publish
-- time (drafts carry NULL) and is monotonic per page.
CREATE TABLE status_page_templates (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    status_page_id UUID NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    version        INTEGER,
    status         TEXT NOT NULL CHECK (status IN ('draft', 'published', 'archived')),
    source         TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at   TIMESTAMPTZ,
    CONSTRAINT status_page_templates_version_when_published
        CHECK (status = 'draft' OR version IS NOT NULL)
);

CREATE INDEX idx_status_page_templates_page ON status_page_templates(status_page_id);
CREATE UNIQUE INDEX uq_status_page_templates_page_version
    ON status_page_templates(status_page_id, version);
CREATE UNIQUE INDEX uq_status_page_templates_one_draft
    ON status_page_templates(status_page_id) WHERE status = 'draft';
CREATE UNIQUE INDEX uq_status_page_templates_one_published
    ON status_page_templates(status_page_id) WHERE status = 'published';

ALTER TABLE tenants
    ADD COLUMN dashboard_group_tags TEXT[] NOT NULL DEFAULT '{}'::TEXT[];

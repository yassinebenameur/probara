CREATE TABLE monitor_dependencies (
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    depends_on_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (monitor_id, depends_on_id),
    CONSTRAINT check_no_self_dependency CHECK (monitor_id != depends_on_id)
);

CREATE INDEX idx_monitor_dependencies_depends_on ON monitor_dependencies(depends_on_id);

-- Tenant scoping and acyclicity are enforced at the application level,
-- following the monitor_groups convention.

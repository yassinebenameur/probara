CREATE TABLE monitor_groups (
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    group_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (monitor_id, group_id),
    CONSTRAINT check_no_self_reference CHECK (monitor_id != group_id)
);

CREATE INDEX idx_monitor_groups_monitor_id ON monitor_groups(monitor_id);
CREATE INDEX idx_monitor_groups_group_id ON monitor_groups(group_id);

-- Add constraint to ensure group_id references a monitor of type 'group'
-- This will be validated at the application level to avoid complex triggers


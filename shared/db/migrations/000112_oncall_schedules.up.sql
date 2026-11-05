-- On-call schedules (people layer). Informational for now: they answer who
-- is on call, preview shifts, take overrides and publish an iCal feed;
-- nothing pages a schedule yet. The resolver is shared/oncall.
CREATE TABLE oncall_schedules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    timezone TEXT NOT NULL DEFAULT 'UTC',
    -- SHA-256 of the iCal feed token; NULL when the feed is off.
    ical_token_hash TEXT UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);

-- Rotation layers, in position order: a later active layer wins.
CREATE TABLE oncall_rotations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    schedule_id UUID NOT NULL REFERENCES oncall_schedules(id) ON DELETE CASCADE,
    position INT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL CHECK (kind IN ('daily', 'weekly')),
    -- Local handoff time in the schedule's zone, minutes after midnight.
    handoff_minute INT NOT NULL CHECK (handoff_minute BETWEEN 0 AND 1439),
    -- 0 = Sunday … 6 = Saturday; weekly rotations only.
    handoff_weekday INT CHECK (handoff_weekday BETWEEN 0 AND 6),
    start_at TIMESTAMPTZ NOT NULL,
    end_at TIMESTAMPTZ CHECK (end_at IS NULL OR end_at > start_at),
    UNIQUE (schedule_id, position),
    CHECK ((kind = 'weekly') = (handoff_weekday IS NOT NULL))
);

-- Members take turns in position order. Removing a user removes their turns.
CREATE TABLE oncall_rotation_members (
    rotation_id UUID NOT NULL REFERENCES oncall_rotations(id) ON DELETE CASCADE,
    position INT NOT NULL,
    user_id UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    PRIMARY KEY (rotation_id, position)
);

CREATE INDEX idx_oncall_rotation_members_user ON oncall_rotation_members(user_id);

-- Overrides win over every rotation; of overlapping ones the newest wins.
CREATE TABLE oncall_overrides (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    schedule_id UUID NOT NULL REFERENCES oncall_schedules(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL CHECK (ends_at > starts_at),
    note TEXT NOT NULL DEFAULT '',
    created_by UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_oncall_overrides_schedule ON oncall_overrides(schedule_id, ends_at);

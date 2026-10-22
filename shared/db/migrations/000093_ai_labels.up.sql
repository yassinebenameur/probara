-- Exact model exchange per analysis (shared/ai.DecisionSnapshot), written by the
-- worker in markReady. NULL for failed, legacy-llm and no-decision analyses.
ALTER TABLE incident_ai_analyses ADD COLUMN decision_snapshot JSONB;

-- Operator-confirmed/corrected answers in the model's input format. Only the
-- tenant FK cascades: monitor purges and incident deletes must not drop labels
-- (request carries its own copy of the evidence).
CREATE TABLE ai_labels (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    subject_type      TEXT NOT NULL CHECK (subject_type IN ('incident_analysis','alert_triage','dependency_pair')),
    subject_key       TEXT NOT NULL,   -- analysis UUID text, or "<uuidLo>:<uuidHi>" for pairs
    subject_id        UUID,            -- analysis or alert_triage id (no FK); NULL for pairs
    incident_id       UUID,            -- export group key (no FK): the analysis's incident, or for alert_triage the earliest incident linking the alert; NULL for pairs and unlinked triage
    verdict           TEXT NOT NULL CHECK (verdict IN ('confirmed','corrected','rejected')),
    incident_label    TEXT,            -- incident_analysis: incident-level catalogue key; alert_triage: alert-level key (hypothesis or correction); NULL for dependency_pair
    answers           JSONB NOT NULL CHECK (jsonb_typeof(answers) = 'object'),
    request           JSONB NOT NULL CHECK (jsonb_typeof(request) = 'object'),
    predicted         JSONB,
    catalogue_version TEXT NOT NULL,
    model_version     TEXT,
    model_sha256      TEXT,
    labeled_by        UUID,            -- admin_users.id, no FK (user deletion keeps the label)
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ai_labels_subject_unique UNIQUE (tenant_id, subject_type, subject_key),
    CONSTRAINT ai_labels_subject_shape CHECK (
        (subject_type IN ('incident_analysis','alert_triage')
            AND subject_id IS NOT NULL AND subject_key = subject_id::text)
     OR (subject_type = 'dependency_pair' AND subject_id IS NULL
            AND subject_key ~ '^[0-9a-f-]{36}:[0-9a-f-]{36}$'
            AND split_part(subject_key, ':', 1) < split_part(subject_key, ':', 2))
    )
);
CREATE INDEX idx_ai_labels_tenant_updated ON ai_labels (tenant_id, updated_at, id);

-- Per-workspace secret for export group/row ids (HMAC key). Never exported.
CREATE TABLE ai_label_salts (
    tenant_id  UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    salt       BYTEA NOT NULL CHECK (octet_length(salt) = 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Catalog of IdP groups presented in verified ID tokens at successful SSO
-- logins. OIDC has no API to enumerate an IdP's groups, so this is how the
-- mapping editor offers real group names instead of relying on operators
-- typing them blind (matching is case-sensitive and exact).
CREATE TABLE oidc_seen_groups (
    group_name TEXT PRIMARY KEY CHECK (group_name <> ''),
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

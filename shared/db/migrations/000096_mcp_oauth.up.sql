-- MCP OAuth 2.1 authorization server (api/internal/services/mcpoauth).
-- Clients self-register (RFC 7591, public clients with PKCE); a grant is a
-- user's consent for one client, with a default tenant and a scope that can
-- only narrow the user's role. Codes and tokens are stored as SHA-256
-- hashes; roles are never stored, they are resolved per request.

CREATE TABLE mcp_oauth_clients (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_name   TEXT NOT NULL,
    redirect_uris TEXT[] NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE mcp_oauth_grants (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id     UUID NOT NULL REFERENCES mcp_oauth_clients(id) ON DELETE CASCADE,
    admin_user_id UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    scope         TEXT NOT NULL CHECK (scope IN ('read', 'write')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at  TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ
);
CREATE INDEX idx_mcp_oauth_grants_user ON mcp_oauth_grants (admin_user_id) WHERE revoked_at IS NULL;

CREATE TABLE mcp_oauth_codes (
    code_hash      TEXT PRIMARY KEY,
    client_id      UUID NOT NULL REFERENCES mcp_oauth_clients(id) ON DELETE CASCADE,
    admin_user_id  UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    scope          TEXT NOT NULL CHECK (scope IN ('read', 'write')),
    redirect_uri   TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL,
    -- Set when the code is exchanged; a second exchange revokes this grant.
    grant_id       UUID REFERENCES mcp_oauth_grants(id) ON DELETE CASCADE
);

CREATE TABLE mcp_oauth_tokens (
    token_hash TEXT PRIMARY KEY,
    grant_id   UUID NOT NULL REFERENCES mcp_oauth_grants(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('access', 'refresh')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Set when a refresh token is rotated; presenting it again revokes the grant.
    rotated_at TIMESTAMPTZ
);
CREATE INDEX idx_mcp_oauth_tokens_grant ON mcp_oauth_tokens (grant_id);
CREATE INDEX idx_mcp_oauth_tokens_expiry ON mcp_oauth_tokens (expires_at);

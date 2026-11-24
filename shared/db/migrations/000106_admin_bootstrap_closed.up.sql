-- First-admin bootstrap (the setup-code form, and the first SSO sign-in on an
-- install without a code) closes for good once any superadmin has existed.
-- Users are hard-deleted and roles change in place, so "no active superadmin
-- right now" is not proof the install is new: this one-row table is. A
-- trigger sets it, so every writer of admin_users is covered, the break-glass
-- cmd/admin included. Read by api/internal/services/adminusers
-- (SuperadminEverExisted).
CREATE TABLE admin_bootstrap_closed (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    closed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE FUNCTION admin_bootstrap_close() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO admin_bootstrap_closed (singleton) VALUES (TRUE)
    ON CONFLICT (singleton) DO NOTHING;
    RETURN NULL;
END;
$$;

CREATE TRIGGER admin_users_close_bootstrap
    AFTER INSERT OR UPDATE OF platform_role ON admin_users
    FOR EACH ROW
    WHEN (NEW.platform_role = 'superadmin')
    EXECUTE FUNCTION admin_bootstrap_close();

-- Existing installs: any superadmin row, active or disabled, means one has
-- existed.
INSERT INTO admin_bootstrap_closed (singleton)
SELECT TRUE WHERE EXISTS (SELECT 1 FROM admin_users WHERE platform_role = 'superadmin');

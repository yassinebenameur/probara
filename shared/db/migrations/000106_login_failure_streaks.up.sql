-- Login lockout streaks for usernames with no password account (unknown,
-- SSO-only, disabled). adminauth counts them with the same SQL as
-- admin_users' lockout columns (migration 000103), so an unknown name and a
-- real account answer identically on every replica and after restarts.
-- Keyed by SHA-256 of the username: the table never stores usernames.
CREATE TABLE login_failure_streaks (
    username_hash TEXT PRIMARY KEY,
    failed_login_count INT NOT NULL,
    last_failed_login_at TIMESTAMPTZ NOT NULL,
    locked_until TIMESTAMPTZ
);

-- Pruning (adminauth.PruneGhostStreaks) deletes streaks idle past 24 hours.
CREATE INDEX idx_login_failure_streaks_last_failed
    ON login_failure_streaks(last_failed_login_at);

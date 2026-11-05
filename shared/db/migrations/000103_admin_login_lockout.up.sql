-- Login brute-force protection (adminauth.Authenticate). DB-backed so the
-- lockout holds across API replicas. failed_login_count is reserved before
-- the password is compared, so concurrent guesses cannot overshoot the
-- threshold; last_failed_login_at lets an old streak of typos expire.
ALTER TABLE admin_users
    ADD COLUMN failed_login_count INT NOT NULL DEFAULT 0,
    ADD COLUMN last_failed_login_at TIMESTAMPTZ,
    ADD COLUMN locked_until TIMESTAMPTZ,
    ADD COLUMN password_changed_at TIMESTAMPTZ;

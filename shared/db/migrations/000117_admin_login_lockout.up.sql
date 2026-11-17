-- Password sign-in lockout (api/internal/services/adminauth/lockout.go): ten
-- consecutive failed sign-ins lock the account for fifteen minutes. Kept on
-- the user row so every API replica sees the same count and lock, and both
-- survive a restart; every time is the database clock's.
-- failed_login_count is the current streak (reset by a successful sign-in,
-- by a lock and by a superadmin unlock), last_failed_login_at the streak's
-- latest failure (a failure more than 24 hours after it starts a new
-- streak), locked_until the end of the current or last lock (a past value is
-- no lock).
ALTER TABLE admin_users
    ADD COLUMN failed_login_count INT NOT NULL DEFAULT 0,
    ADD COLUMN last_failed_login_at TIMESTAMPTZ,
    ADD COLUMN locked_until TIMESTAMPTZ;

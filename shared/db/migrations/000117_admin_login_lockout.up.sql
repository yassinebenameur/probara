-- Password sign-in lockout (api/internal/services/adminauth/lockout.go): ten
-- consecutive failed sign-ins lock the account for fifteen minutes. Kept on
-- the user row so every API replica sees the same count and lock, and both
-- survive a restart. failed_login_count is the current streak (reset by a
-- successful sign-in, by a lock and by a superadmin unlock); locked_until is
-- the end of the current lock, or of the last one (a past value is no lock).
ALTER TABLE admin_users
    ADD COLUMN failed_login_count INT NOT NULL DEFAULT 0,
    ADD COLUMN locked_until TIMESTAMPTZ;

-- Case-insensitive email uniqueness (auth-8). The runner's precheck for this
-- migration (sqlx.precheckUniqueEmails) stops the upgrade with a list of user
-- ids when two accounts differ only by case or surrounding space, instead of
-- failing on a raw constraint error.
UPDATE users SET email = lower(trim(email)) WHERE email <> lower(trim(email));
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_lower ON users (lower(email));

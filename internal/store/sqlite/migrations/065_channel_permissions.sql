-- Record the notification channel permission defaults for existing orgs:
-- owners and admins manage channels, members may also send a test. Orgs
-- without these rows get the same defaults in code.
INSERT OR IGNORE INTO org_settings (org_id, key, value)
SELECT id, 'channel_manage_roles', 'owner,admin' FROM orgs;
INSERT OR IGNORE INTO org_settings (org_id, key, value)
SELECT id, 'channel_test_roles', 'owner,admin,member' FROM orgs;

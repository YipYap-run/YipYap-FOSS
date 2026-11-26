-- Users provisioned by SSO before the fix got a verified account email but
-- no contact method, so they could not be paged. Seed the account email as a
-- verified method plus an immediate rule for every verified user who has no
-- contact method at all. Safe to re-run: the first insert skips users who
-- already have a method, and the second only touches users who have exactly
-- one method (the seeded one) and no rules.
INSERT INTO user_contact_methods (id, org_id, user_id, type, label, config, verified_at, created_at, updated_at)
SELECT lower(hex(randomblob(16))), u.org_id, u.id, 'email', 'Account email',
       json_object('address', u.email), u.email_verified_at,
       strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
FROM users u
WHERE u.email_verified_at IS NOT NULL AND u.email <> ''
  AND NOT EXISTS (SELECT 1 FROM user_contact_methods m WHERE m.user_id = u.id);

INSERT INTO user_notification_rules (id, org_id, user_id, contact_method_id, delay_minutes, urgency, created_at)
SELECT lower(hex(randomblob(16))), m.org_id, m.user_id, m.id, 0, 'any', strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
FROM user_contact_methods m
WHERE m.type = 'email' AND m.label = 'Account email'
  AND NOT EXISTS (SELECT 1 FROM user_notification_rules r WHERE r.user_id = m.user_id)
  AND (SELECT COUNT(*) FROM user_contact_methods o WHERE o.user_id = m.user_id) = 1;

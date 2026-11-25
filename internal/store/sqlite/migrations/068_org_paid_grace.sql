-- Deadline for paid channel types (SMS, voice, Telegram, CloudEvents)
-- after an org drops to Free. The org store sets it on the downgrade and
-- clears it on an upgrade.
ALTER TABLE orgs ADD COLUMN paid_grace_until TEXT;

-- Free orgs that already hold paid channels were downgraded before the
-- column existed. Give them the default 30 day grace from now instead of
-- cutting delivery off at deploy.
UPDATE orgs
   SET paid_grace_until = strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '+30 days')
 WHERE plan = 'free'
   AND paid_grace_until IS NULL
   AND EXISTS (
       SELECT 1 FROM notification_channels nc
        WHERE nc.org_id = orgs.id
          AND nc.type IN ('twilio_sms', 'twilio_voice', 'telnyx_sms', 'telnyx_voice', 'telegram', 'cloudevent_http')
   );

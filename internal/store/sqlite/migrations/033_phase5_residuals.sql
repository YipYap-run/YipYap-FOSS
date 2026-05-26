-- Phase-5 residuals from the round-2 audit:
--   * Backfill org_id on alert_remediations (was alert-scoped via JOIN).
--   * Add priority column on alert_routing_rules for deterministic ordering.
--   * UNIQUE on (org_id, alert_id, reply_id) on cloudevent_reply_audit so
--     duplicate replies don't double-write side state.

ALTER TABLE alert_remediations ADD COLUMN org_id TEXT NOT NULL DEFAULT '';
UPDATE alert_remediations
   SET org_id = (SELECT alerts.org_id FROM alerts WHERE alerts.id = alert_remediations.alert_id)
 WHERE org_id = '';
CREATE INDEX IF NOT EXISTS idx_alert_remediations_org_alert
    ON alert_remediations (org_id, alert_id);

ALTER TABLE alert_routing_rules ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_alert_routing_rules_org_priority_expires
    ON alert_routing_rules (org_id, priority DESC, expires_at);

CREATE UNIQUE INDEX IF NOT EXISTS uq_cloudevent_reply_audit_dedup
    ON cloudevent_reply_audit (org_id, alert_id, reply_id);

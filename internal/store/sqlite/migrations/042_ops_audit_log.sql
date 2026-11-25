-- Audit trail for the internal ops API (/internal/v1). One row per successful
-- mutating call: who (X-Ops-Actor), what (action), on what (org, user,
-- ticket, or other target id), why (reason), the state before and after
-- (JSON), and the client IP. Rows are not removed when an org is deleted.
CREATE TABLE IF NOT EXISTS ops_audit_log (
    id TEXT PRIMARY KEY,
    actor TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    org_id TEXT NOT NULL DEFAULT '',
    user_id TEXT NOT NULL DEFAULT '',
    ticket_id TEXT NOT NULL DEFAULT '',
    target_id TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    before_state TEXT NOT NULL DEFAULT '{}',
    after_state TEXT NOT NULL DEFAULT '{}',
    ip TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ops_audit_log_created ON ops_audit_log(created_at);
CREATE INDEX IF NOT EXISTS idx_ops_audit_log_org ON ops_audit_log(org_id, created_at);
CREATE INDEX IF NOT EXISTS idx_ops_audit_log_actor ON ops_audit_log(actor, created_at);
CREATE INDEX IF NOT EXISTS idx_ops_audit_log_action ON ops_audit_log(action, created_at);

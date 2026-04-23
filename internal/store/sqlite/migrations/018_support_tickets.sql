-- Support tickets
CREATE TABLE support_tickets (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id),
    user_id TEXT NOT NULL REFERENCES users(id),
    subject TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    priority TEXT NOT NULL DEFAULT 'normal',
    context_json TEXT,
    assigned_to TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    closed_at TEXT,
    last_staff_reply_at TEXT,
    first_staff_reply_at TEXT,
    escalated_at TEXT,
    csat_rating INTEGER,
    csat_comment TEXT,
    csat_submitted_at TEXT
);
CREATE INDEX idx_support_tickets_org ON support_tickets(org_id);
CREATE INDEX idx_support_tickets_status ON support_tickets(status);
CREATE INDEX idx_support_tickets_updated ON support_tickets(updated_at DESC);

-- Support messages
CREATE TABLE support_messages (
    id TEXT PRIMARY KEY,
    ticket_id TEXT NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    sender_type TEXT NOT NULL,
    sender_id TEXT NOT NULL,
    sender_email TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_support_messages_ticket ON support_messages(ticket_id, created_at);

-- Staff notification channels for support events
CREATE TABLE support_notify_channels (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    config TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    min_priority TEXT NOT NULL DEFAULT 'normal',
    notify_on_new INTEGER NOT NULL DEFAULT 1,
    notify_on_reply INTEGER NOT NULL DEFAULT 1,
    notify_on_escalation INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- Support settings (escalation timers, etc.)
CREATE TABLE support_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT INTO support_settings VALUES ('escalate_normal_hours', '24');
INSERT INTO support_settings VALUES ('escalate_high_hours', '2');
INSERT INTO support_settings VALUES ('renotify_urgent_minutes', '30');

-- Canned responses / macros
CREATE TABLE support_macros (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    category TEXT DEFAULT '',
    sort_order INTEGER DEFAULT 0,
    created_at TEXT NOT NULL
);

-- File attachments
CREATE TABLE support_attachments (
    id TEXT PRIMARY KEY,
    message_id TEXT NOT NULL REFERENCES support_messages(id) ON DELETE CASCADE,
    filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    storage_key TEXT NOT NULL,
    created_at TEXT NOT NULL
)

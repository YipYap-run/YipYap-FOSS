-- awaiting_staff_since is the time of the oldest customer message that has no
-- staff reply after it. Set on customer messages, cleared on staff replies.
-- The escalation worker times out on it, so follow-ups escalate too.
ALTER TABLE support_tickets ADD COLUMN awaiting_staff_since TEXT;

-- related_ticket_id links a new ticket to the closed ticket it follows up on
-- (reopen window passed). Validated on create: same org, visible to the caller.
ALTER TABLE support_tickets ADD COLUMN related_ticket_id TEXT;

-- Backfill: an open or pending ticket waits on staff from its oldest customer
-- message that has no staff reply after it. Internal notes do not count.
UPDATE support_tickets
SET awaiting_staff_since = (
    SELECT MIN(m.created_at) FROM support_messages m
    WHERE m.ticket_id = support_tickets.id
      AND m.sender_type = 'user'
      AND NOT EXISTS (
          SELECT 1 FROM support_messages s
          WHERE s.ticket_id = m.ticket_id
            AND s.sender_type = 'staff'
            AND s.created_at >= m.created_at
      )
)
WHERE status IN ('open', 'pending')
  AND awaiting_staff_since IS NULL;

-- An escalation recorded before the first staff reply belongs to an earlier
-- wait. Clear it so a follow-up that is still unanswered can escalate.
UPDATE support_tickets
SET escalated_at = NULL
WHERE awaiting_staff_since IS NOT NULL
  AND first_staff_reply_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_support_tickets_awaiting
    ON support_tickets(awaiting_staff_since);

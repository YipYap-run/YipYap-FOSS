-- Impact the customer picked when opening a ticket (for example
-- alerts_not_delivered). The server maps it to priority; staff see both.
ALTER TABLE support_tickets ADD COLUMN impact TEXT;

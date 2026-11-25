-- Escalation policies get a free-text description (what the policy is for,
-- who owns it). Existing policies start with an empty description.
ALTER TABLE escalation_policies ADD COLUMN description TEXT NOT NULL DEFAULT '';

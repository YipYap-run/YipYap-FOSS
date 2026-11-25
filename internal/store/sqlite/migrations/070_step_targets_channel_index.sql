-- Channel delete and the "used by" list look up escalation step targets
-- by channel.
CREATE INDEX IF NOT EXISTS idx_step_targets_channel_id ON step_targets(channel_id);

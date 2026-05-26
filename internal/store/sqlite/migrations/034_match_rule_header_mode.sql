-- Add header_match_mode so match-rule header matching supports
-- equals (default), contains, and regex, mirroring body_match_mode.
ALTER TABLE monitor_match_rules ADD COLUMN header_match_mode TEXT NOT NULL DEFAULT '';

-- Store the check error message on the alert for display in the UI.
ALTER TABLE alerts ADD COLUMN error TEXT NOT NULL DEFAULT '';

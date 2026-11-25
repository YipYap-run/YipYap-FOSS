-- Account deletion keeps the user row as an anonymized tombstone so support
-- tickets, incidents, incident updates and schedule overrides that reference
-- it stay valid (auth-9). deleted_at marks the tombstone.
ALTER TABLE users ADD COLUMN deleted_at TEXT;

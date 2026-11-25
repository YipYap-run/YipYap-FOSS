CREATE TABLE IF NOT EXISTS cloudevent_positions (
    org_id     TEXT NOT NULL,
    key_hash   TEXT NOT NULL,
    cursor     TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (org_id, key_hash)
);

CREATE INDEX IF NOT EXISTS idx_cloudevent_positions_updated_at
    ON cloudevent_positions (updated_at);

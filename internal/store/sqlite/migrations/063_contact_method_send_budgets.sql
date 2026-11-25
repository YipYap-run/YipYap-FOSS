CREATE TABLE IF NOT EXISTS contact_method_send_budgets (
    scope    TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    day      TEXT NOT NULL,
    count    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (scope, owner_id, day)
);

CREATE INDEX IF NOT EXISTS idx_contact_method_send_budgets_day ON contact_method_send_budgets(day);

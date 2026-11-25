CREATE TABLE IF NOT EXISTS rate_budget (
    budget_key TEXT PRIMARY KEY,
    window_started_at TEXT NOT NULL,
    counter INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_rate_budget_window ON rate_budget(window_started_at);

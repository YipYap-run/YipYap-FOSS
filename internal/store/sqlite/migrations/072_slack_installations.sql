-- Platform Slack app installations (OAuth v2). One row per org and Slack
-- workspace. bot_token holds the token sealed with the notification
-- envelope key, never plaintext. Revoked rows keep their history with the
-- token cleared.
CREATE TABLE IF NOT EXISTS slack_installations (
    id           TEXT PRIMARY KEY,
    org_id       TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    team_id      TEXT NOT NULL,
    team_name    TEXT NOT NULL DEFAULT '',
    bot_user_id  TEXT NOT NULL DEFAULT '',
    bot_token    TEXT NOT NULL DEFAULT '',
    scopes       TEXT NOT NULL DEFAULT '',
    installed_by TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    revoked_at   TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_slack_installations_org_team ON slack_installations(org_id, team_id);
CREATE INDEX IF NOT EXISTS idx_slack_installations_team ON slack_installations(team_id);

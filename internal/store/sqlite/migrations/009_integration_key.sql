ALTER TABLE monitors ADD COLUMN integration_key TEXT;
CREATE UNIQUE INDEX idx_monitors_integration_key ON monitors(integration_key) WHERE integration_key IS NOT NULL;

ALTER TABLE oidc_connections ADD COLUMN authorize_url TEXT NOT NULL DEFAULT '';
ALTER TABLE oidc_connections ADD COLUMN token_url TEXT NOT NULL DEFAULT '';
ALTER TABLE oidc_connections ADD COLUMN userinfo_url TEXT NOT NULL DEFAULT '';
ALTER TABLE oidc_connections ADD COLUMN scopes TEXT NOT NULL DEFAULT 'openid,email,profile';
ALTER TABLE oidc_connections ADD COLUMN auto_provision INTEGER NOT NULL DEFAULT 1;
ALTER TABLE oidc_connections ADD COLUMN default_role TEXT NOT NULL DEFAULT 'member'

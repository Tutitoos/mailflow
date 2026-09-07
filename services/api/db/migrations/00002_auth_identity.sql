-- +goose Up
ALTER TABLE users
  ADD COLUMN name text,
  ADD COLUMN email_verified boolean NOT NULL DEFAULT false,
  ADD COLUMN image text;

ALTER TABLE users ALTER COLUMN id SET DEFAULT gen_random_uuid();

UPDATE users
SET name = split_part(email, '@', 1)
WHERE name IS NULL;

ALTER TABLE users ALTER COLUMN name SET NOT NULL;

CREATE TABLE auth_sessions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  expires_at timestamptz NOT NULL,
  token text NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL,
  ip_address text,
  user_agent text,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX auth_sessions_user_id_idx ON auth_sessions(user_id);

CREATE TABLE auth_accounts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id text NOT NULL,
  provider_id text NOT NULL,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  access_token text,
  refresh_token text,
  id_token text,
  access_token_expires_at timestamptz,
  refresh_token_expires_at timestamptz,
  scope text,
  password text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL
);

CREATE INDEX auth_accounts_user_id_idx ON auth_accounts(user_id);

CREATE TABLE auth_verifications (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  identifier text NOT NULL,
  value text NOT NULL,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX auth_verifications_identifier_idx ON auth_verifications(identifier);

CREATE TABLE auth_passkeys (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text,
  public_key text NOT NULL,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  credential_id text NOT NULL,
  counter integer NOT NULL,
  device_type text NOT NULL,
  backed_up boolean NOT NULL,
  transports text,
  created_at timestamptz,
  aaguid text
);

CREATE INDEX auth_passkeys_user_id_idx ON auth_passkeys(user_id);
CREATE INDEX auth_passkeys_credential_id_idx ON auth_passkeys(credential_id);

CREATE TABLE auth_jwks (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  public_key text NOT NULL,
  private_key text NOT NULL,
  created_at timestamptz NOT NULL,
  expires_at timestamptz,
  alg text,
  crv text
);

-- +goose Down
DROP TABLE IF EXISTS auth_jwks;
DROP TABLE IF EXISTS auth_passkeys;
DROP TABLE IF EXISTS auth_verifications;
DROP TABLE IF EXISTS auth_accounts;
DROP TABLE IF EXISTS auth_sessions;

ALTER TABLE users
  ALTER COLUMN id DROP DEFAULT,
  DROP COLUMN IF EXISTS image,
  DROP COLUMN IF EXISTS email_verified,
  DROP COLUMN IF EXISTS name;

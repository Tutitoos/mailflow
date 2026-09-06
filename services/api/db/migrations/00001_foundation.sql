-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE users (
  id uuid PRIMARY KEY,
  email text NOT NULL UNIQUE,
  locale text NOT NULL DEFAULT 'en' CHECK (locale IN ('en', 'es')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE accounts (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider text NOT NULL CHECK (provider IN ('google', 'microsoft', 'imap')),
  display_name text NOT NULL,
  encrypted_credentials bytea NOT NULL,
  credential_nonce bytea NOT NULL,
  capabilities jsonb NOT NULL DEFAULT '{}',
  sync_state text NOT NULL DEFAULT 'pending',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mailboxes (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  remote_id text NOT NULL,
  name text NOT NULL,
  role text,
  unread_count integer NOT NULL DEFAULT 0,
  UNIQUE (account_id, remote_id)
);

CREATE TABLE threads (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  remote_id text NOT NULL,
  last_message_at timestamptz NOT NULL,
  is_read boolean NOT NULL DEFAULT false,
  is_starred boolean NOT NULL DEFAULT false,
  category text NOT NULL DEFAULT 'primary',
  UNIQUE (account_id, remote_id)
);

CREATE TABLE messages (
  id uuid PRIMARY KEY,
  thread_id uuid NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
  remote_id text NOT NULL,
  message_id text,
  references_header text[] NOT NULL DEFAULT '{}',
  sender jsonb NOT NULL,
  recipients jsonb NOT NULL,
  subject text NOT NULL DEFAULT '',
  body_text text NOT NULL DEFAULT '',
  body_html_sanitized text NOT NULL DEFAULT '',
  sent_at timestamptz NOT NULL,
  search_vector tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', coalesce(subject, '') || ' ' || coalesce(body_text, ''))
  ) STORED,
  UNIQUE (thread_id, remote_id)
);

CREATE INDEX messages_search_idx ON messages USING gin(search_vector);
CREATE INDEX messages_subject_trgm_idx ON messages USING gin(subject gin_trgm_ops);
CREATE INDEX threads_account_last_message_idx ON threads(account_id, last_message_at DESC);

CREATE TABLE sync_cursors (
  account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
  kind text NOT NULL,
  cursor jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE pending_actions (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  idempotency_key text NOT NULL,
  action text NOT NULL,
  payload jsonb NOT NULL,
  status text NOT NULL DEFAULT 'pending',
  attempts integer NOT NULL DEFAULT 0,
  available_at timestamptz NOT NULL DEFAULT now(),
  last_error_code text,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (account_id, idempotency_key)
);

CREATE TABLE metric_points (
  bucket timestamptz NOT NULL,
  resolution text NOT NULL CHECK (resolution IN ('minute', 'hour', 'day')),
  name text NOT NULL,
  kind text NOT NULL CHECK (kind IN ('counter', 'gauge', 'histogram')),
  dimensions jsonb NOT NULL DEFAULT '{}',
  value double precision NOT NULL,
  count bigint NOT NULL DEFAULT 1,
  PRIMARY KEY (bucket, resolution, name, dimensions)
);

CREATE TABLE log_entries (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  occurred_at timestamptz NOT NULL DEFAULT now(),
  service text NOT NULL,
  module text NOT NULL,
  level text NOT NULL CHECK (level IN ('info', 'warning', 'error')),
  event text NOT NULL,
  request_id text,
  attributes jsonb NOT NULL DEFAULT '{}'
);

CREATE INDEX log_entries_query_idx ON log_entries(occurred_at DESC, service, module, level);

CREATE TABLE sentry_issues (
  id uuid PRIMARY KEY,
  fingerprint text NOT NULL,
  component text NOT NULL,
  environment text NOT NULL,
  title text NOT NULL,
  status text NOT NULL DEFAULT 'unresolved',
  first_seen_at timestamptz NOT NULL,
  last_seen_at timestamptz NOT NULL,
  event_count bigint NOT NULL DEFAULT 1,
  UNIQUE (fingerprint, component, environment)
);

-- +goose Down
DROP TABLE IF EXISTS sentry_issues;
DROP TABLE IF EXISTS log_entries;
DROP TABLE IF EXISTS metric_points;
DROP TABLE IF EXISTS pending_actions;
DROP TABLE IF EXISTS sync_cursors;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS threads;
DROP TABLE IF EXISTS mailboxes;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS users;

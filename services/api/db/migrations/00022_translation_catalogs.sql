-- +goose Up
CREATE TABLE translation_revisions (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  message_count integer NOT NULL CHECK (message_count BETWEEN 1 AND 1024),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE translation_messages (
  revision_id bigint NOT NULL REFERENCES translation_revisions(id) ON DELETE CASCADE,
  locale text NOT NULL CHECK (locale IN ('en', 'es')),
  key text NOT NULL CHECK (key ~ '^[A-Za-z][A-Za-z0-9_.-]{0,127}$'),
  value text NOT NULL CHECK (char_length(value) BETWEEN 1 AND 4096),
  source_hash text NOT NULL CHECK (source_hash ~ '^[0-9a-f]{64}$'),
  PRIMARY KEY (revision_id, locale, key)
);

CREATE TABLE translation_state (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  active_revision bigint NOT NULL REFERENCES translation_revisions(id),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX translation_revisions_retention_idx ON translation_revisions (created_at DESC, id DESC);

-- +goose Down
DROP TABLE IF EXISTS translation_state;
DROP TABLE IF EXISTS translation_messages;
DROP TABLE IF EXISTS translation_revisions;

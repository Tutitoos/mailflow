-- +goose Up
ALTER TABLE sentry_issues
  ADD CONSTRAINT sentry_issues_status_check CHECK (status IN ('unresolved', 'resolved', 'ignored'));

ALTER TABLE sentry_projects
  ADD COLUMN artifact_token_hash text CHECK (artifact_token_hash IS NULL OR artifact_token_hash ~ '^[0-9a-f]{64}$');

ALTER TABLE sentry_events
  ADD COLUMN issue_id uuid REFERENCES sentry_issues(id),
  ADD COLUMN grouping_key text CHECK (grouping_key IS NULL OR grouping_key ~ '^[0-9a-f]{64}$'),
  ADD COLUMN normalized_stack jsonb NOT NULL DEFAULT '[]',
  ADD COLUMN symbolication_status text NOT NULL DEFAULT 'not_required'
    CHECK (symbolication_status IN ('not_required', 'pending', 'resolved', 'failed'));

CREATE INDEX sentry_events_issue_idx ON sentry_events (issue_id, received_at DESC);

CREATE TABLE sentry_releases (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  component text NOT NULL REFERENCES sentry_projects(component),
  version text NOT NULL CHECK (version ~ '^(v?[0-9]+\.[0-9]+\.[0-9]+([-+][A-Za-z0-9.-]+)?|[0-9a-f]{7,64})$'),
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'finalized')),
  created_at timestamptz NOT NULL DEFAULT now(),
  finalized_at timestamptz,
  UNIQUE (component, version)
);

CREATE TABLE sentry_release_artifacts (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  release_id bigint NOT NULL REFERENCES sentry_releases(id) ON DELETE CASCADE,
  object_id text NOT NULL,
  object_namespace text GENERATED ALWAYS AS ('sentry') STORED,
  name text NOT NULL CHECK (btrim(name) <> '' AND octet_length(name) <= 255 AND name !~ '[/\\[:cntrl:]]'),
  kind text NOT NULL CHECK (kind IN ('sourcemap', 'dsym', 'dif')),
  checksum_sha256 text NOT NULL CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
  size_bytes bigint NOT NULL CHECK (size_bytes > 0),
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processed', 'failed')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0 AND attempts <= 20),
  error_code text,
  created_at timestamptz NOT NULL DEFAULT now(),
  processed_at timestamptz,
  FOREIGN KEY (object_id, object_namespace) REFERENCES cdn_objects(object_id, namespace),
  UNIQUE (release_id, name)
);

CREATE INDEX sentry_release_artifacts_status_idx ON sentry_release_artifacts (status, id);

-- +goose Down
DROP TABLE IF EXISTS sentry_release_artifacts;
DROP TABLE IF EXISTS sentry_releases;
DROP INDEX IF EXISTS sentry_events_issue_idx;
ALTER TABLE sentry_events
  DROP COLUMN IF EXISTS symbolication_status,
  DROP COLUMN IF EXISTS normalized_stack,
  DROP COLUMN IF EXISTS grouping_key,
  DROP COLUMN IF EXISTS issue_id;
ALTER TABLE sentry_issues DROP CONSTRAINT IF EXISTS sentry_issues_status_check;
ALTER TABLE sentry_projects DROP COLUMN IF EXISTS artifact_token_hash;

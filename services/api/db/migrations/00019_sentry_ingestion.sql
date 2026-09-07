-- +goose Up
CREATE TABLE sentry_projects (
  component text PRIMARY KEY CHECK (component IN ('api', 'web', 'desktop', 'ios')),
  public_key text NOT NULL UNIQUE CHECK (public_key ~ '^[0-9a-f]{32}$'),
  enabled boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sentry_events (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  event_id text NOT NULL CHECK (event_id ~ '^[0-9a-f]{32}$'),
  component text NOT NULL REFERENCES sentry_projects(component),
  event_type text NOT NULL,
  environment text,
  release text,
  level text,
  sdk_name text,
  received_bytes bigint NOT NULL CHECK (received_bytes >= 0),
  item_count bigint NOT NULL CHECK (item_count > 0 AND item_count <= 100),
  received_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (component, event_id)
);

CREATE INDEX sentry_events_received_idx ON sentry_events (received_at DESC, component, event_type);

CREATE TABLE sentry_event_items (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  event_id bigint NOT NULL REFERENCES sentry_events(id) ON DELETE CASCADE,
  item_type text NOT NULL,
  content_type text,
  received_bytes bigint NOT NULL CHECK (received_bytes >= 0),
  payload_sha256 text NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
  summary jsonb NOT NULL DEFAULT '{}',
  payload_object_id text,
  payload_namespace text GENERATED ALWAYS AS (
    CASE WHEN payload_object_id IS NULL THEN NULL ELSE 'sentry' END
  ) STORED,
  discarded boolean NOT NULL DEFAULT false,
  FOREIGN KEY (payload_object_id, payload_namespace)
    REFERENCES cdn_objects(object_id, namespace)
);

CREATE INDEX sentry_event_items_event_idx ON sentry_event_items (event_id, id);

-- +goose Down
DROP TABLE IF EXISTS sentry_event_items;
DROP TABLE IF EXISTS sentry_events;
DROP TABLE IF EXISTS sentry_projects;

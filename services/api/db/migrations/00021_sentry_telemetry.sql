-- +goose Up
CREATE TABLE sentry_traces (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  event_id bigint NOT NULL UNIQUE REFERENCES sentry_events(id) ON DELETE CASCADE,
  component text NOT NULL REFERENCES sentry_projects(component),
  trace_id text NOT NULL CHECK (trace_id ~ '^[0-9a-f]{32}$'),
  span_id text NOT NULL CHECK (span_id ~ '^[0-9a-f]{16}$'),
  parent_span_id text CHECK (parent_span_id IS NULL OR parent_span_id ~ '^[0-9a-f]{16}$'),
  operation text,
  status text,
  started_at timestamptz,
  duration_ms double precision CHECK (duration_ms IS NULL OR duration_ms >= 0),
  span_count integer NOT NULL CHECK (span_count >= 0 AND span_count <= 1000),
  received_at timestamptz NOT NULL
);

CREATE INDEX sentry_traces_query_idx ON sentry_traces (received_at DESC, component, trace_id);

CREATE TABLE sentry_spans (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  trace_row_id bigint NOT NULL REFERENCES sentry_traces(id) ON DELETE CASCADE,
  trace_id text NOT NULL CHECK (trace_id ~ '^[0-9a-f]{32}$'),
  span_id text NOT NULL CHECK (span_id ~ '^[0-9a-f]{16}$'),
  parent_span_id text CHECK (parent_span_id IS NULL OR parent_span_id ~ '^[0-9a-f]{16}$'),
  operation text,
  status text,
  started_at timestamptz,
  duration_ms double precision CHECK (duration_ms IS NULL OR duration_ms >= 0),
  UNIQUE (trace_row_id, span_id)
);

CREATE INDEX sentry_spans_trace_idx ON sentry_spans (trace_row_id, id);

CREATE TABLE sentry_profiles (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  event_id bigint NOT NULL UNIQUE REFERENCES sentry_events(id) ON DELETE CASCADE,
  component text NOT NULL REFERENCES sentry_projects(component),
  platform text,
  sample_count integer NOT NULL CHECK (sample_count >= 0 AND sample_count <= 1000000),
  frame_count integer NOT NULL CHECK (frame_count >= 0 AND frame_count <= 1000000),
  received_at timestamptz NOT NULL
);

CREATE INDEX sentry_profiles_query_idx ON sentry_profiles (received_at DESC, component);

CREATE TABLE sentry_replays (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  component text NOT NULL REFERENCES sentry_projects(component),
  replay_id text NOT NULL CHECK (replay_id ~ '^[0-9a-f]{32}$'),
  environment text NOT NULL,
  first_seen_at timestamptz NOT NULL,
  last_seen_at timestamptz NOT NULL,
  segment_count integer NOT NULL DEFAULT 0 CHECK (segment_count >= 0),
  UNIQUE (component, replay_id)
);

CREATE TABLE sentry_replay_segments (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  replay_row_id bigint NOT NULL REFERENCES sentry_replays(id) ON DELETE CASCADE,
  event_id bigint NOT NULL REFERENCES sentry_events(id) ON DELETE CASCADE,
  sequence integer NOT NULL CHECK (sequence >= 0 AND sequence <= 1000000),
  object_id text NOT NULL,
  object_namespace text GENERATED ALWAYS AS ('sentry') STORED,
  size_bytes bigint NOT NULL CHECK (size_bytes > 0),
  checksum_sha256 text NOT NULL CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
  received_at timestamptz NOT NULL,
  FOREIGN KEY (object_id, object_namespace) REFERENCES cdn_objects(object_id, namespace),
  UNIQUE (replay_row_id, sequence)
);

CREATE INDEX sentry_replays_query_idx ON sentry_replays (last_seen_at DESC, component);
CREATE INDEX sentry_replay_segments_retention_idx ON sentry_replay_segments (received_at, object_id);

-- +goose Down
DROP TABLE IF EXISTS sentry_replay_segments;
DROP TABLE IF EXISTS sentry_replays;
DROP TABLE IF EXISTS sentry_profiles;
DROP TABLE IF EXISTS sentry_spans;
DROP TABLE IF EXISTS sentry_traces;

-- +goose Up
ALTER TABLE log_entries DROP CONSTRAINT log_entries_level_check;
ALTER TABLE log_entries ADD CONSTRAINT log_entries_level_check CHECK (level IN ('debug', 'info', 'warning', 'error'));

DROP INDEX IF EXISTS log_entries_query_idx;
CREATE INDEX log_entries_query_idx ON log_entries (occurred_at DESC, service, module, level, event, request_id);

CREATE TABLE log_debug_lease (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  enabled_until timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO log_debug_lease (singleton) VALUES (true);

-- +goose Down
DELETE FROM log_entries WHERE level = 'debug';
DROP TABLE IF EXISTS log_debug_lease;
DROP INDEX IF EXISTS log_entries_query_idx;
CREATE INDEX log_entries_query_idx ON log_entries (occurred_at DESC, service, module, level);
ALTER TABLE log_entries DROP CONSTRAINT log_entries_level_check;
ALTER TABLE log_entries ADD CONSTRAINT log_entries_level_check CHECK (level IN ('info', 'warning', 'error'));

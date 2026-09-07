-- +goose Up
CREATE TABLE cdn_objects (
  object_id text PRIMARY KEY CHECK (object_id ~ '^[0-9a-f]{32}$'),
  namespace text NOT NULL CHECK (namespace IN ('attachments', 'sentry')),
  account_id uuid REFERENCES accounts(id) ON DELETE CASCADE,
  recovery_reference text,
  filename text,
  media_type text NOT NULL,
  size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
  etag text NOT NULL CHECK (etag ~ '^[0-9a-f]{64}$'),
  storage_status text NOT NULL DEFAULT 'cached' CHECK (storage_status IN ('cached', 'missing')),
  expires_at timestamptz,
  stored_at timestamptz NOT NULL DEFAULT now(),
  last_accessed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (object_id, namespace),
  CHECK ((namespace = 'attachments') = (account_id IS NOT NULL)),
  CHECK (recovery_reference IS NULL OR (btrim(recovery_reference) <> '' AND octet_length(recovery_reference) <= 2048)),
  CHECK (filename IS NULL OR (btrim(filename) <> '' AND octet_length(filename) <= 1024 AND filename !~ '[[:cntrl:]]')),
  CHECK (btrim(media_type) <> '' AND octet_length(media_type) <= 255),
  CHECK (expires_at IS NULL OR expires_at >= stored_at)
);

CREATE INDEX cdn_objects_expiry_idx
  ON cdn_objects (expires_at) WHERE namespace = 'attachments' AND storage_status = 'cached';
CREATE INDEX cdn_objects_account_idx ON cdn_objects (account_id, object_id);

ALTER TABLE message_attachments
  ADD COLUMN cached_object_id text,
  ADD COLUMN cached_object_namespace text GENERATED ALWAYS AS (
    CASE WHEN cached_object_id IS NULL THEN NULL ELSE 'attachments' END
  ) STORED,
  ADD CONSTRAINT message_attachments_cached_object_fkey
    FOREIGN KEY (cached_object_id, cached_object_namespace)
    REFERENCES cdn_objects(object_id, namespace);

CREATE INDEX message_attachments_cached_object_idx
  ON message_attachments (cached_object_id) WHERE cached_object_id IS NOT NULL;

-- +goose Down
ALTER TABLE message_attachments
  DROP COLUMN IF EXISTS cached_object_namespace,
  DROP COLUMN IF EXISTS cached_object_id;
DROP TABLE IF EXISTS cdn_objects;

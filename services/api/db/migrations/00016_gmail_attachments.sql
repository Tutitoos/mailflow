-- +goose Up
ALTER TABLE draft_attachments
  ADD COLUMN object_namespace text GENERATED ALWAYS AS ('attachments') STORED,
  ADD CONSTRAINT draft_attachments_object_fkey
    FOREIGN KEY (object_id, object_namespace)
    REFERENCES cdn_objects(object_id, namespace) ON DELETE RESTRICT;

CREATE INDEX draft_attachments_object_idx ON draft_attachments (object_id);

-- +goose Down
ALTER TABLE draft_attachments
  DROP COLUMN IF EXISTS object_namespace;

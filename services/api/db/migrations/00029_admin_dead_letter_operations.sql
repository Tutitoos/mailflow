-- +goose Up
ALTER TABLE admin_operations
  DROP CONSTRAINT admin_operations_action_check,
  ADD CONSTRAINT admin_operations_action_check
    CHECK (action IN ('queue.retry_sync', 'queue.resolve_dead_letter'));

ALTER TABLE admin_operations
  DROP CONSTRAINT admin_operations_result_check,
  ADD CONSTRAINT admin_operations_result_check
    CHECK (result IN ('requested', 'queued', 'already_running', 'resolved', 'already_resolved', 'failed'));

-- +goose Down
DELETE FROM admin_operations WHERE action = 'queue.resolve_dead_letter';

ALTER TABLE admin_operations
  DROP CONSTRAINT admin_operations_action_check,
  ADD CONSTRAINT admin_operations_action_check
    CHECK (action IN ('queue.retry_sync'));

ALTER TABLE admin_operations
  DROP CONSTRAINT admin_operations_result_check,
  ADD CONSTRAINT admin_operations_result_check
    CHECK (result IN ('requested', 'queued', 'already_running', 'failed'));

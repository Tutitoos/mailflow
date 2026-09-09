-- +goose Up
ALTER TABLE sync_runs DROP CONSTRAINT sync_runs_state_check;
ALTER TABLE sync_runs
  ADD COLUMN failure_code text,
  ADD CONSTRAINT sync_runs_state_check
    CHECK (state IN ('queued', 'running', 'completed', 'cancelled', 'failed')),
  ADD CONSTRAINT sync_runs_failure_code_check
    CHECK (
      (state = 'failed' AND failure_code ~ '^sync_[a-z0-9_]{1,96}$') OR
      (state <> 'failed' AND failure_code IS NULL)
    );

-- +goose Down
ALTER TABLE sync_runs DROP CONSTRAINT sync_runs_failure_code_check;
ALTER TABLE sync_runs DROP CONSTRAINT sync_runs_state_check;
ALTER TABLE sync_runs DROP COLUMN failure_code;
ALTER TABLE sync_runs
  ADD CONSTRAINT sync_runs_state_check
    CHECK (state IN ('queued', 'running', 'completed', 'cancelled'));

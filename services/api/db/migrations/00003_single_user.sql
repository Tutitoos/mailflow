-- +goose Up
CREATE UNIQUE INDEX users_single_user_idx ON users ((true));

-- +goose Down
DROP INDEX IF EXISTS users_single_user_idx;

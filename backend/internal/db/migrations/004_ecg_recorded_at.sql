-- +goose Up
-- +goose StatementBegin
ALTER TABLE ecgs ADD COLUMN recorded_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE ecgs DROP COLUMN recorded_at;
-- +goose StatementEnd

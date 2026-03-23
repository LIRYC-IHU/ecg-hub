-- +goose Up
-- +goose StatementBegin
ALTER TABLE ecgs ADD COLUMN hl7_retry_count INT NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE ecgs DROP COLUMN hl7_retry_count;
-- +goose StatementEnd

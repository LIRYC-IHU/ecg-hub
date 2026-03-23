-- +goose Up
-- +goose StatementBegin

-- Migration 009: ECG metadata — extra JSONB column + ecg.read / ecg.write permissions

ALTER TABLE ecgs ADD COLUMN IF NOT EXISTS extra JSONB NOT NULL DEFAULT '{}';

-- New granular permissions for ECG metadata access
INSERT INTO role_permissions (role_id, permission)
SELECT id, perm FROM roles, unnest(ARRAY['ecg.read']) AS perm
WHERE name IN ('reader', 'writer');

INSERT INTO role_permissions (role_id, permission)
SELECT id, perm FROM roles, unnest(ARRAY['ecg.write']) AS perm
WHERE name = 'writer';

-- +goose StatementEnd

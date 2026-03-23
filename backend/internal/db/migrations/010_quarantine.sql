-- +goose Up
-- +goose StatementBegin

-- Migration 010: Quarantine entries table + quarantine.read / quarantine.delete permissions

CREATE TABLE IF NOT EXISTS quarantine_entries (
    id          SERIAL PRIMARY KEY,
    filename    TEXT        NOT NULL,
    file_path   TEXT        NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    error_reason TEXT       NOT NULL
);

-- New permissions for quarantine access
INSERT INTO role_permissions (role_id, permission)
SELECT id, perm FROM roles, unnest(ARRAY['quarantine.read', 'quarantine.delete']) AS perm
WHERE name = 'writer';

-- +goose StatementEnd

-- +goose Up
-- +goose StatementBegin

-- Migration 006: dynamic roles and permissions table
-- Roles are created by admins in the Rights page.
-- The admin role (configured via OIDC.AdminRoleName) bypasses all permission checks.

CREATE TABLE roles (
    id          SERIAL PRIMARY KEY,
    name        TEXT UNIQUE NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE role_permissions (
    role_id    INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission TEXT    NOT NULL,
    PRIMARY KEY (role_id, permission)
);

-- Seed legacy roles so existing deployments keep working.
INSERT INTO roles (name, description) VALUES
    ('reader', 'Lecture seule — consultation et téléchargement ECG'),
    ('writer', 'Lecture + suppression et force HL7 sur ECG');

INSERT INTO role_permissions (role_id, permission)
SELECT id, perm FROM roles, unnest(ARRAY['patient.read','ecg.download']) AS perm
WHERE name = 'reader';

INSERT INTO role_permissions (role_id, permission)
SELECT id, perm FROM roles, unnest(ARRAY['patient.read','ecg.download','ecg.delete','ecg.force_hl7']) AS perm
WHERE name = 'writer';

-- +goose StatementEnd

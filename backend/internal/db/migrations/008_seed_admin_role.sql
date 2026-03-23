-- +goose Up
-- +goose StatementBegin

-- Seed the admin role so it appears in the DB for user linkage.
-- PermissionChecker bypasses DB for this role (all permissions always granted).
INSERT INTO roles (name, description)
VALUES ('admin', 'Accès complet — bypass toutes les permissions')
ON CONFLICT (name) DO NOTHING;

-- +goose StatementEnd

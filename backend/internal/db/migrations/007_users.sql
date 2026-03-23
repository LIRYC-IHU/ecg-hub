-- +goose Up
-- +goose StatementBegin

-- Migration 007: ECG Hub user registry
-- Stores every user who has authenticated, with their assigned role.
-- This is the source of truth for LDAP users and the fallback for OIDC users
-- when no Keycloak realm role is present.

CREATE TABLE ecg_hub_users (
    id          SERIAL PRIMARY KEY,
    external_id TEXT    UNIQUE NOT NULL,  -- preferred_username (OIDC) or username (LDAP)
    provider    TEXT    NOT NULL DEFAULT 'oidc',
    role_id     INTEGER REFERENCES roles(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose StatementEnd

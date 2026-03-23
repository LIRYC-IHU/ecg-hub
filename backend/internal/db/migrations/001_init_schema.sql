-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS patients (
    id            BIGSERIAL    PRIMARY KEY,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    patient_id    TEXT         NOT NULL UNIQUE,
    first_name    TEXT         NOT NULL DEFAULT '',
    last_name     TEXT         NOT NULL DEFAULT '',
    date_of_birth DATE,
    gender        TEXT         NOT NULL DEFAULT '',
    hl7_source    TEXT         NOT NULL DEFAULT '',
    extra         JSONB        NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS ecgs (
    id                BIGSERIAL    PRIMARY KEY,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    -- NO updated_at: ECGs are immutable after insertion (FR12)
    patient_id        TEXT         NOT NULL,
    vendor            TEXT         NOT NULL,
    file_path         TEXT         NOT NULL,
    original_filename TEXT         NOT NULL,
    ingested_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    hl7_status        TEXT         NOT NULL DEFAULT 'pending',
    immutable         BOOLEAN      NOT NULL DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id          BIGSERIAL    PRIMARY KEY,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    -- NO updated_at: audit_logs are append-only (NFR-S6, RGPD)
    user_id     TEXT         NOT NULL,
    action      TEXT         NOT NULL,
    resource_id TEXT         NOT NULL,
    details     TEXT         NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS ecg_buffer (
    id          BIGSERIAL    PRIMARY KEY,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    received_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    raw_data    JSONB        NOT NULL,
    status      TEXT         NOT NULL DEFAULT 'pending'
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS ecg_buffer;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS ecgs;
DROP TABLE IF EXISTS patients;
-- +goose StatementEnd

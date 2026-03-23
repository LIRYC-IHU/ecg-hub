-- 011_export_jobs.sql
-- Batch export jobs for story 5.1 (FR19, NFR-SC3)
-- +goose Up

CREATE TABLE IF NOT EXISTS export_jobs (
    id              VARCHAR(36)  PRIMARY KEY,
    user_id         VARCHAR(255) NOT NULL,
    status          VARCHAR(20)  NOT NULL DEFAULT 'queued',
    ecg_count       INT          NOT NULL,
    processed_count INT          NOT NULL DEFAULT 0,
    format          VARCHAR(20)  NOT NULL DEFAULT 'original',
    file_path       TEXT,
    error           TEXT,
    created_at      TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at      TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_export_jobs_user_id    ON export_jobs(user_id);
CREATE INDEX IF NOT EXISTS idx_export_jobs_status     ON export_jobs(status);
CREATE INDEX IF NOT EXISTS idx_export_jobs_created_at ON export_jobs(created_at);

CREATE TABLE IF NOT EXISTS export_job_ecgs (
    export_job_id VARCHAR(36) NOT NULL REFERENCES export_jobs(id) ON DELETE CASCADE,
    ecg_id        BIGINT      NOT NULL,
    PRIMARY KEY (export_job_id, ecg_id)
);

-- +goose Down

DROP TABLE IF EXISTS export_job_ecgs;
DROP TABLE IF EXISTS export_jobs;

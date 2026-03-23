-- +goose Up
-- +goose StatementBegin

-- 1. Drop ecg_buffer — never used in production pipeline (in-memory channels handle buffering).
DROP TABLE IF EXISTS ecg_buffer;

-- 2. Drop ecgs.immutable — always TRUE, enforced at application layer, not meaningful in DB.
ALTER TABLE ecgs DROP COLUMN immutable;

-- 3. Drop ecgs.created_at — duplicate of ingested_at (GORM auto-set and persister both write NOW()).
--    ingested_at is the canonical ingestion timestamp.
ALTER TABLE ecgs DROP COLUMN created_at;

-- 4. Add FK: ecgs.patient_id → patients.patient_id.
--    Safe: persister always UpsertByPatientID before inserting ECG.
ALTER TABLE ecgs
    ADD CONSTRAINT fk_ecgs_patient_id
    FOREIGN KEY (patient_id) REFERENCES patients(patient_id);

-- 5. audit_logs.details: TEXT → JSONB.
--    Drop the TEXT default first (PostgreSQL cannot cast '' to jsonb automatically),
--    then change the type, then set the new JSONB default.
ALTER TABLE audit_logs ALTER COLUMN details DROP DEFAULT;

ALTER TABLE audit_logs
    ALTER COLUMN details TYPE JSONB
    USING CASE WHEN details = '' OR details IS NULL THEN '{}' ELSE details::jsonb END;

ALTER TABLE audit_logs ALTER COLUMN details SET DEFAULT '{}';

-- 6. CHECK constraint on hl7_status — enforce valid values at DB level.
ALTER TABLE ecgs
    ADD CONSTRAINT chk_ecgs_hl7_status
    CHECK (hl7_status IN ('pending', 'success', 'hl7_exhausted'));

-- 7. Index on recorded_at — should have been in 004 alongside the column.
CREATE INDEX IF NOT EXISTS idx_ecgs_recorded_at ON ecgs (recorded_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_ecgs_recorded_at;
ALTER TABLE ecgs DROP CONSTRAINT IF EXISTS chk_ecgs_hl7_status;
ALTER TABLE audit_logs ALTER COLUMN details TYPE TEXT USING details::text;
ALTER TABLE audit_logs ALTER COLUMN details SET DEFAULT '';
ALTER TABLE ecgs DROP CONSTRAINT IF EXISTS fk_ecgs_patient_id;
ALTER TABLE ecgs ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE ecgs ADD COLUMN immutable BOOLEAN NOT NULL DEFAULT TRUE;

CREATE TABLE IF NOT EXISTS ecg_buffer (
    id          BIGSERIAL    PRIMARY KEY,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    received_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    raw_data    JSONB        NOT NULL,
    status      TEXT         NOT NULL DEFAULT 'pending'
);

-- +goose StatementEnd

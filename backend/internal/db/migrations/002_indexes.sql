-- +goose Up
-- +goose StatementBegin
-- Patient search (FR13, NFR-P2 <2s for name search)
CREATE INDEX IF NOT EXISTS idx_patients_patient_id ON patients (patient_id);
CREATE INDEX IF NOT EXISTS idx_patients_last_name   ON patients (last_name);

-- ECG queries
CREATE INDEX IF NOT EXISTS idx_ecgs_patient_id  ON ecgs (patient_id);
CREATE INDEX IF NOT EXISTS idx_ecgs_hl7_status  ON ecgs (hl7_status);
CREATE INDEX IF NOT EXISTS idx_ecgs_ingested_at ON ecgs (ingested_at);

-- Audit log queries
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id     ON audit_logs (user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_resource_id ON audit_logs (resource_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at  ON audit_logs (created_at);

-- ECG buffer drain
CREATE INDEX IF NOT EXISTS idx_ecg_buffer_status ON ecg_buffer (status);

-- Enforce audit_logs immutability at DB privilege level (NFR-S6, RGPD, AC#5).
-- This revokes UPDATE/DELETE from the current DB user (ecghub).
-- In production, run migrations as the DB owner for full enforcement.
REVOKE UPDATE, DELETE ON audit_logs FROM CURRENT_USER;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
GRANT UPDATE, DELETE ON audit_logs TO CURRENT_USER;
DROP INDEX IF EXISTS idx_ecg_buffer_status;
DROP INDEX IF EXISTS idx_audit_logs_created_at;
DROP INDEX IF EXISTS idx_audit_logs_resource_id;
DROP INDEX IF EXISTS idx_audit_logs_user_id;
DROP INDEX IF EXISTS idx_ecgs_ingested_at;
DROP INDEX IF EXISTS idx_ecgs_hl7_status;
DROP INDEX IF EXISTS idx_ecgs_patient_id;
DROP INDEX IF EXISTS idx_patients_last_name;
DROP INDEX IF EXISTS idx_patients_patient_id;
-- +goose StatementEnd

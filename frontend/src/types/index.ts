// Shared TypeScript types for ECG Hub frontend.
// All API response fields use snake_case (Go convention).

export interface Patient {
  id: number
  patient_id: string
  first_name: string
  last_name: string
  date_of_birth: string | null
  gender: string
  nip?: string
  ecg_count: number
  last_activity: string | null // ISO 8601 UTC
}

export interface ECG {
  id: number
  patient_id: string
  vendor: string
  file_path: string
  original_filename: string
  recorded_at: string | null  // acquisition timestamp from device; null for legacy records
  ingested_at: string
  hl7_status: 'pending' | 'success' | 'hl7_exhausted'
  immutable: boolean
  extra: Record<string, unknown>  // editable metadata (JSONB)
}

export interface ECGFieldDef {
  key: string
  label: string
  type: 'text' | 'number' | 'datetime' | 'select'
  options?: string[]
}

export interface ECGMetaResponse {
  fields: ECGFieldDef[]
  values: Record<string, unknown>
}

export interface ExportJob {
  id: string
  status: 'pending' | 'running' | 'done' | 'error'
  ecg_ids: number[]
  progress: number
  error?: string
  created_at: string
}

export interface AuditLog {
  id: number
  user_id: string
  action: 'view' | 'download' | 'delete' | 'quarantine_decision' | 'hl7_force'
  resource_id: string
  details: Record<string, unknown> | null
  created_at: string
}

export interface QuarantineEntry {
  id: number
  filename: string
  received_at: string
  error_reason: string
  file_path: string
}

export interface SystemState {
  status: 'ok' | 'degraded'
  database: 'ok' | 'error'
  volume: 'ok' | 'unavailable'
  hl7: 'ok' | 'degraded'
  ftp: 'ok' | 'error'
  buffer_count: number
  quarantine_count: number
  hl7_exhausted_count: number
}

// ECGWithPatient extends ECG with patient demographics for the timeline view (GET /api/v1/ecgs).
export interface ECGWithPatient extends ECG {
  patient_first_name: string
  patient_last_name: string
  patient_gender: string
  patient_dob: string | null
}

// API response wrappers
export interface ListResponse<T> {
  data: T[]
  total: number
  page: number
  per_page: number
}

export interface ItemResponse<T> {
  data: T
}

export interface ErrorResponse {
  code: string
  message: string
}

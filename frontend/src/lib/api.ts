import type { ECG, ECGMetaResponse, ErrorResponse, ListResponse, Patient } from '../types'

const BASE_URL = (import.meta.env as Record<string, string>).VITE_API_URL ?? ''

export interface MeResponse {
  user_id: string
  role: string
  permissions: string[]
}

export async function fetchAuthProviders(): Promise<string[]> {
  const res = await fetch(`${BASE_URL}/api/v1/auth/provider`)
  const data: { providers: string[] } = await res.json()
  return data.providers ?? []
}

export async function loginWithLDAP(username: string, password: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
}

export async function fetchMe(): Promise<MeResponse> {
  const res = await fetch(`${BASE_URL}/api/v1/auth/me`)
  if (!res.ok) throw new Error('unauthenticated')
  return res.json()
}

export interface ECGFilters {
  from?: string
  to?: string
  vendor?: string
  hl7_status?: 'pending' | 'success' | 'hl7_exhausted'
  page?: number
  per_page?: number
}

export async function fetchECGs(
  patientId: number,
  filters: ECGFilters = {},
): Promise<ListResponse<ECG>> {
  const params = new URLSearchParams()
  if (filters.from) params.set('from', filters.from)
  if (filters.to) params.set('to', filters.to)
  if (filters.vendor) params.set('vendor', filters.vendor)
  if (filters.hl7_status) params.set('hl7_status', filters.hl7_status)
  params.set('page', String(filters.page ?? 1))
  params.set('per_page', String(filters.per_page ?? 20))

  const res = await fetch(`${BASE_URL}/api/v1/patients/${patientId}/ecgs?${params}`)
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  return res.json()
}

// downloadECG triggers a browser file download for the original ECG format.
// window.location.href sends the JWT cookie automatically; Content-Disposition: attachment
// causes the browser to save the file rather than navigate.
export function downloadECG(id: number): void {
  window.location.href = `${BASE_URL}/api/v1/ecgs/${id}/download`
}

// downloadECGXMLFDA triggers a browser file download of the ECG converted to FDA HL7 v3 aECG XML.
// Only call this for vendors listed in XMLFDA_SUPPORTED_VENDORS (currently: philips).
export function downloadECGXMLFDA(id: number): void {
  window.location.href = `${BASE_URL}/api/v1/ecgs/${id}/download?format=xmlfda`
}

export interface AdminStats {
  total_ecgs: number
  total_patients: number
  hl7_pending: number
  hl7_success: number
  hl7_exhausted: number
  quarantine_count: number
}

export async function fetchAdminStats(): Promise<AdminStats> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/stats`)
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  return res.json()
}

export async function fetchHealth(): Promise<{
  status: string
  database: string
  dicom_enabled?: boolean
  dicom_port?: number
  ftp_enabled?: boolean
  ftp_port?: number
}> {
  const res = await fetch(`${BASE_URL}/healthz`)
  return res.json()
}

export interface WebhookStatus {
  enabled: boolean
  url: string
  secret_configured: boolean
}

export interface KeycloakUser {
  id: string
  username: string
  email?: string
  enabled: boolean
  ecg_hub_role: string
}

export async function fetchUsers(): Promise<KeycloakUser[]> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/users`)
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  const data: { data: KeycloakUser[] } = await res.json()
  return data.data ?? []
}

export async function setUserRole(userId: string, role: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/users/${userId}/role`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ role }),
  })
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
}

export async function deleteECG(id: number): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/ecgs/${id}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) {
    const err: ErrorResponse = await res.json()
    throw err
  }
}

export async function fetchWebhookStatus(): Promise<WebhookStatus> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/webhook`)
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  return res.json()
}

export interface ModuleStatus {
  name: string
  extensions: string[]
  status: string // "ok" or error message
}

export async function fetchModules(): Promise<ModuleStatus[]> {
  const res = await fetch(`${BASE_URL}/api/v1/modules`)
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  return res.json()
}

export async function testWebhook(): Promise<{ success: boolean; status_code?: number; error?: string }> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/webhook/test`, { method: 'POST' })
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  return res.json()
}

export async function forceHL7(ecgId: number): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/ecgs/${ecgId}/hl7/force`, { method: 'POST' })
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
}

export interface AuditLogFilters {
  user_id?: string
  action?: string
  from?: string
  to?: string
  page?: number
  per_page?: number
}

export async function fetchAuditLogs(
  filters: AuditLogFilters = {},
): Promise<ListResponse<import('../types').AuditLog>> {
  const params = new URLSearchParams()
  if (filters.user_id) params.set('user_id', filters.user_id)
  if (filters.action) params.set('action', filters.action)
  if (filters.from) params.set('from', filters.from)
  if (filters.to) params.set('to', filters.to)
  params.set('page', String(filters.page ?? 1))
  params.set('per_page', String(filters.per_page ?? 50))
  const res = await fetch(`${BASE_URL}/api/v1/audit-logs?${params}`)
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  return res.json()
}

export interface PatientFilters {
  q?: string
  sort_by?: 'patient_id' | 'last_name' | 'created_at'
  sort_order?: 'asc' | 'desc'
  page?: number
  per_page?: number
}

export async function fetchPatients(
  filters: PatientFilters = {},
): Promise<ListResponse<Patient>> {
  const params = new URLSearchParams()
  if (filters.q) params.set('q', filters.q)
  if (filters.sort_by) params.set('sort_by', filters.sort_by)
  if (filters.sort_order) params.set('sort_order', filters.sort_order)
  params.set('page', String(filters.page ?? 1))
  params.set('per_page', String(filters.per_page ?? 50))
  const res = await fetch(`${BASE_URL}/api/v1/patients?${params}`)
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  return res.json()
}

export interface AppRole {
  id: number
  name: string
  description: string
  permissions: string[]
}

export const ALL_PERMISSIONS = [
  'patient.read',
  'ecg.read',
  'ecg.write',
  'ecg.download',
  'ecg.delete',
  'ecg.force_hl7',
  'quarantine.read',
  'quarantine.delete',
  'admin.users',
  'admin.audit',
  'admin.system',
] as const

export type Permission = typeof ALL_PERMISSIONS[number]

export async function fetchRoles(): Promise<AppRole[]> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/roles`)
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  const data: { data: AppRole[] } = await res.json()
  return data.data ?? []
}

export async function createRole(name: string, description: string, permissions: string[]): Promise<AppRole> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/roles`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, description, permissions }),
  })
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  return res.json()
}

export async function updateRole(id: number, description: string, permissions: string[]): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/roles/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ description, permissions }),
  })
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
}

export async function deleteRole(id: number): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/roles/${id}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) {
    const err: ErrorResponse = await res.json()
    throw err
  }
}

export interface AppUser {
  id: number
  external_id: string
  provider: string
  role_name: string
  last_login: string
}

export async function fetchAppUsers(): Promise<AppUser[]> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/app-users`)
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  const data: { data: AppUser[] } = await res.json()
  return data.data ?? []
}

export interface QuarantineEntry {
  id: number
  filename: string
  file_path: string
  received_at: string
  error_reason: string
}

export async function fetchQuarantine(page = 1, perPage = 50): Promise<ListResponse<QuarantineEntry>> {
  const params = new URLSearchParams({ page: String(page), per_page: String(perPage) })
  const res = await fetch(`${BASE_URL}/api/v1/admin/quarantine?${params}`)
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  return res.json()
}

export async function deleteQuarantineEntry(id: number): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/quarantine/${id}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) {
    const err: ErrorResponse = await res.json()
    throw err
  }
}

export async function fetchECGMeta(ecgId: number): Promise<ECGMetaResponse> {
  const res = await fetch(`${BASE_URL}/api/v1/ecgs/${ecgId}/metadata`)
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  return res.json()
}

export async function patchECGMetadata(
  ecgId: number,
  values: Record<string, unknown>,
): Promise<Record<string, unknown>> {
  const res = await fetch(`${BASE_URL}/api/v1/ecgs/${ecgId}/metadata`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(values),
  })
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  return res.json()
}

// --- Batch Export (FR19, Story 5.1) ---

export interface ExportJobRequest {
  ecg_ids: number[]
  format?: 'original' | 'xmlfda'
}

export interface ExportJobResponse {
  id: string
  status: 'queued' | 'processing' | 'complete' | 'failed'
  ecg_count: number
  processed_count?: number
  format?: string
  created_at: string
  download_url: string
  error?: string
}

export async function createExportJob(req: ExportJobRequest): Promise<ExportJobResponse> {
  const res = await fetch(`${BASE_URL}/api/v1/exports`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  return res.json()
}

export async function getExportJob(jobId: string): Promise<ExportJobResponse> {
  const res = await fetch(`${BASE_URL}/api/v1/exports/${jobId}`)
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
  return res.json()
}

export async function setAppUserRole(userId: number, role: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/app-users/${userId}/role`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ role }),
  })
  if (!res.ok) {
    const err: ErrorResponse = await res.json()
    throw err
  }
}

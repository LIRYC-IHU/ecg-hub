import type {
  ECG,
  ECGMetaResponse,
  ErrorResponse,
  ListResponse,
  Patient,
} from "../types";

const BASE_URL = (import.meta.env as Record<string, string>).VITE_API_URL ?? "";

export interface MeResponse {
  user_id: string;
  role: string;
  permissions: string[];
}

export async function fetchSetupStatus(): Promise<{ initialized: boolean }> {
  const res = await fetch(`${BASE_URL}/api/v1/setup/status`);
  return res.json();
}

export async function setupAdmin(
  username: string,
  password: string,
): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/setup`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function fetchAuthProviders(): Promise<string[]> {
  const res = await fetch(`${BASE_URL}/api/v1/auth/provider`);
  const data: { providers: string[] } = await res.json();
  return data.providers ?? [];
}

export async function loginWithLDAP(
  username: string,
  password: string,
): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function loginWithLocal(
  username: string,
  password: string,
): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password, provider: "local" }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function fetchMe(): Promise<MeResponse> {
  const res = await fetch(`${BASE_URL}/api/v1/auth/me`);
  if (!res.ok) throw new Error("unauthenticated");
  return res.json();
}

export interface AllECGFilters {
  q?: string;
  hl7_status?: "pending" | "success" | "hl7_exhausted";
  vendor?: string;
  device_model?: string;
  file_format?: string;
  from?: string;
  to?: string;
  page?: number;
  per_page?: number;
}

export interface ECGFilterFacets {
  vendors: string[];
  device_models: string[];
  file_formats: string[];
}

export async function fetchECGFilterFacets(): Promise<ECGFilterFacets> {
  const res = await fetch(`${BASE_URL}/api/v1/ecgs/filters`);
  if (!res.ok) return { vendors: [], device_models: [] };
  return res.json();
}

export async function fetchAllECGs(
  filters: AllECGFilters = {},
): Promise<ListResponse<import("../types").ECGWithPatient>> {
  const params = new URLSearchParams();
  if (filters.q) params.set("q", filters.q);
  if (filters.hl7_status) params.set("hl7_status", filters.hl7_status);
  if (filters.vendor) params.set("vendor", filters.vendor);
  if (filters.device_model) params.set("device_model", filters.device_model);
  if (filters.file_format) params.set("file_format", filters.file_format);
  if (filters.from) params.set("from", filters.from);
  if (filters.to) params.set("to", filters.to);
  params.set("page", String(filters.page ?? 1));
  params.set("per_page", String(filters.per_page ?? 50));
  const res = await fetch(`${BASE_URL}/api/v1/ecgs?${params}`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export interface ECGFilters {
  from?: string;
  to?: string;
  vendor?: string;
  hl7_status?: "pending" | "success" | "hl7_exhausted";
  page?: number;
  per_page?: number;
}

export async function fetchECGs(
  patientId: number,
  filters: ECGFilters = {},
): Promise<ListResponse<ECG>> {
  const params = new URLSearchParams();
  if (filters.from) params.set("from", filters.from);
  if (filters.to) params.set("to", filters.to);
  if (filters.vendor) params.set("vendor", filters.vendor);
  if (filters.hl7_status) params.set("hl7_status", filters.hl7_status);
  params.set("page", String(filters.page ?? 1));
  params.set("per_page", String(filters.per_page ?? 20));

  const res = await fetch(
    `${BASE_URL}/api/v1/patients/${patientId}/ecgs?${params}`,
  );
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

// triggerBlobDownload fetches a URL and triggers a browser download from a blob.
// Unlike window.location.href, this allows catching JSON error responses.
async function triggerBlobDownload(url: string): Promise<void> {
  const res = await fetch(url, { cache: "no-store" });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  const blob = await res.blob();
  const disposition = res.headers.get("Content-Disposition") ?? "";
  const match = disposition.match(/filename[^;=\n]*=((['"]).*?\2|[^;\n]*)/);
  const filename = match ? match[1].replace(/['"]/g, "") : "download";
  const objectUrl = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = objectUrl;
  a.download = filename;
  a.style.display = "none";
  document.body.appendChild(a);
  a.click();
  setTimeout(() => {
    document.body.removeChild(a);
    URL.revokeObjectURL(objectUrl);
  }, 100);
}

// downloadECGFormat triggers a browser download for a specific export format.
// format = "original" | "xmlfda" | "dicom" | ...
export async function downloadECGFormat(
  id: number,
  format: string,
): Promise<void> {
  if (format === "original") {
    await triggerBlobDownload(`${BASE_URL}/api/v1/ecgs/${id}/download`);
  } else {
    await triggerBlobDownload(
      `${BASE_URL}/api/v1/ecgs/${id}/download?format=${encodeURIComponent(format)}`,
    );
  }
}

export interface AdminStats {
  total_ecgs: number;
  total_patients: number;
  hl7_pending: number;
  hl7_success: number;
  hl7_exhausted: number;
  quarantine_count: number;
}

export async function fetchAdminStats(): Promise<AdminStats> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/stats`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export interface ConnectorHealthEntry {
  name: string;
  protocol?: string;
  status: string; // "ok" or error message
  host?: string;
  port?: number;
  ae_title?: string;
}

export async function fetchConnectors(): Promise<ConnectorHealthEntry[]> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/connectors`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export async function fetchHealth(): Promise<{
  status: string;
  database: string;
  dicom_enabled?: boolean;
  dicom_port?: number;
  ftp_enabled?: boolean;
  ftp_port?: number;
  ectp_enabled?: boolean;
  ectp_port?: number;
  connectors?: ConnectorHealthEntry[];
}> {
  const res = await fetch(`${BASE_URL}/healthz`);
  return res.json();
}

export interface WebhookStatus {
  enabled: boolean;
  url: string;
  secret_configured: boolean;
}

export interface KeycloakUser {
  id: string;
  username: string;
  email?: string;
  enabled: boolean;
  ecg_hub_role: string;
}

export async function fetchUsers(): Promise<KeycloakUser[]> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/users`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  const data: { data: KeycloakUser[] } = await res.json();
  return data.data ?? [];
}

export async function setUserRole(userId: string, role: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/users/${userId}/role`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ role }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function deleteECG(id: number): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/ecgs/${id}`, {
    method: "DELETE",
  });
  if (!res.ok && res.status !== 404) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function fetchWebhookStatus(): Promise<WebhookStatus> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/webhook`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export interface ExportFormat {
  id: string; // "original" | "xmlfda" | "dicom"
  label: string; // human-readable label
  extension: string;
}

export interface ModuleStatus {
  name: string;
  extensions: string[];
  status: string; // "ok" or error message
  formats: ExportFormat[];
}

export async function fetchModules(): Promise<ModuleStatus[]> {
  const res = await fetch(`${BASE_URL}/api/v1/modules`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export async function testWebhook(): Promise<{
  success: boolean;
  status_code?: number;
  error?: string;
}> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/webhook/test`, {
    method: "POST",
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export async function forceHL7(ecgId: number): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/ecgs/${ecgId}/hl7/force`, {
    method: "POST",
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export interface AuditLogFilters {
  user_id?: string;
  action?: string;
  from?: string;
  to?: string;
  page?: number;
  per_page?: number;
}

export async function fetchAuditLogs(
  filters: AuditLogFilters = {},
): Promise<ListResponse<import("../types").AuditLog>> {
  const params = new URLSearchParams();
  if (filters.user_id) params.set("user_id", filters.user_id);
  if (filters.action) params.set("action", filters.action);
  if (filters.from) params.set("from", filters.from);
  if (filters.to) params.set("to", filters.to);
  params.set("page", String(filters.page ?? 1));
  params.set("per_page", String(filters.per_page ?? 50));
  const res = await fetch(`${BASE_URL}/api/v1/audit-logs?${params}`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export interface PatientFilters {
  q?: string;
  tags?: string[];
  sort_by?: "patient_id" | "last_name" | "created_at" | "last_activity";
  sort_order?: "asc" | "desc";
  page?: number;
  per_page?: number;
}

export async function fetchPatients(
  filters: PatientFilters = {},
): Promise<ListResponse<Patient>> {
  const params = new URLSearchParams();
  if (filters.q) params.set("q", filters.q);
  if (filters.tags && filters.tags.length > 0) params.set("tags", filters.tags.join(","));
  if (filters.sort_by) params.set("sort_by", filters.sort_by);
  if (filters.sort_order) params.set("sort_order", filters.sort_order);
  params.set("page", String(filters.page ?? 1));
  params.set("per_page", String(filters.per_page ?? 50));
  const res = await fetch(`${BASE_URL}/api/v1/patients?${params}`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export interface AppRole {
  id: number;
  name: string;
  description: string;
  permissions: string[];
}

export const ALL_PERMISSIONS = [
  "patient.read",
  "ecg.read",
  "ecg.write",
  "ecg.download",
  "ecg.delete",
  "ecg.force_hl7",
  "quarantine.read",
  "quarantine.delete",
  "admin.users",
  "admin.audit",
  "admin.system",
  "admin.auth_config",
] as const;

export type Permission = (typeof ALL_PERMISSIONS)[number];

export async function fetchRoles(): Promise<AppRole[]> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/roles`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  const data: { data: AppRole[] } = await res.json();
  return data.data ?? [];
}

export async function createRole(
  name: string,
  description: string,
  permissions: string[],
): Promise<AppRole> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/roles`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, description, permissions }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export async function updateRole(
  id: number,
  description: string,
  permissions: string[],
): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/roles/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ description, permissions }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function deleteRole(id: number): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/roles/${id}`, {
    method: "DELETE",
  });
  if (!res.ok && res.status !== 404) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export interface AppUser {
  id: number;
  external_id: string;
  provider: string;
  role_name: string;
  last_login: string;
}

export async function fetchAppUsers(): Promise<AppUser[]> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/app-users`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  const data: { data: AppUser[] } = await res.json();
  return data.data ?? [];
}

export interface QuarantineEntry {
  id: number;
  filename: string;
  file_path: string;
  received_at: string;
  error_reason: string;
}

export async function fetchQuarantine(
  page = 1,
  perPage = 50,
): Promise<ListResponse<QuarantineEntry>> {
  const params = new URLSearchParams({
    page: String(page),
    per_page: String(perPage),
  });
  const res = await fetch(`${BASE_URL}/api/v1/admin/quarantine?${params}`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export async function deleteQuarantineEntry(id: number): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/quarantine/${id}`, {
    method: "DELETE",
  });
  if (!res.ok && res.status !== 404) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function fetchECGMeta(ecgId: number): Promise<ECGMetaResponse> {
  const res = await fetch(`${BASE_URL}/api/v1/ecgs/${ecgId}/metadata`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export async function patchECGMetadata(
  ecgId: number,
  values: Record<string, unknown>,
): Promise<Record<string, unknown>> {
  const res = await fetch(`${BASE_URL}/api/v1/ecgs/${ecgId}/metadata`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(values),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

// --- Batch Export (FR19, Story 5.1) ---

export interface ExportJobRequest {
  ecg_ids: number[];
  formats: string[];
}

export interface ExportJobResponse {
  id: string;
  status: "queued" | "processing" | "complete" | "failed";
  ecg_count: number;
  processed_count?: number;
  formats: string[];
  created_at: string;
  download_url: string;
  error?: string;
}

export async function createExportJob(
  req: ExportJobRequest,
): Promise<ExportJobResponse> {
  const res = await fetch(`${BASE_URL}/api/v1/exports`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export async function getExportJob(jobId: string): Promise<ExportJobResponse> {
  const res = await fetch(`${BASE_URL}/api/v1/exports/${jobId}`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export async function setAppUserRole(
  userId: number,
  role: string,
): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/app-users/${userId}/role`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ role }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

// ─── Pins (favourites) ──────────────────────────────────────────────────────

export async function fetchPins(): Promise<string[]> {
  const res = await fetch(`${BASE_URL}/api/v1/pins`);
  if (!res.ok) return [];
  const json = await res.json();
  return json.data ?? [];
}

export async function pinPatient(patientId: string): Promise<void> {
  await fetch(`${BASE_URL}/api/v1/pins`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ patient_id: patientId }),
  });
}

export async function unpinPatient(patientId: string): Promise<void> {
  await fetch(`${BASE_URL}/api/v1/pins/${patientId}`, {
    method: "DELETE",
  });
}

// ─── HL7 Test ────────────────────────────────────────────────────────────────

export interface HL7CompNode {
  path: string;
  value: string;
}

export interface HL7FieldNode {
  path: string;
  value: string;
  components?: HL7CompNode[];
}

export interface HL7SegmentNode {
  name: string;
  fields: HL7FieldNode[];
}

export interface HL7TestResult {
  success: boolean;
  patient_id: string;
  duration: string;
  error?: string;
  raw?: string;
  tree?: HL7SegmentNode[];
  demographics?: {
    last_name: string;
    first_name: string;
    date_of_birth: string;
    gender: string;
    source: string;
  };
}

export interface HL7Mapping {
  id: string;
  preset_id: string;
  source_path: string;
  target_field: string;
}

export interface HL7Preset {
  id: string;
  name: string;
  active: boolean;
  mappings: HL7Mapping[];
  created_at: string;
}

export async function testHL7Query(patientId: string): Promise<HL7TestResult> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/hl7/test`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ patient_id: patientId }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export async function fetchHL7Presets(): Promise<HL7Preset[]> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/hl7/presets`);
  if (!res.ok) return [];
  const json = await res.json();
  return json.data ?? [];
}

export async function createHL7Preset(name: string, mappings: { source_path: string; target_field: string }[]): Promise<HL7Preset> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/hl7/presets`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, mappings }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  const json = await res.json();
  return json.data;
}

export async function activateHL7Preset(id: string): Promise<void> {
  await fetch(`${BASE_URL}/api/v1/admin/hl7/presets/${id}/activate`, { method: "POST" });
}

export async function deleteHL7Preset(id: string): Promise<void> {
  await fetch(`${BASE_URL}/api/v1/admin/hl7/presets/${id}`, { method: "DELETE" });
}

export async function saveHL7PresetMappings(presetId: string, mappings: { source_path: string; target_field: string }[]): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/hl7/presets/${presetId}/mappings`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ mappings }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function fetchActiveHL7Mappings(): Promise<{ data: HL7Mapping[]; active: boolean }> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/hl7/active-mappings`);
  if (!res.ok) return { data: [], active: false };
  return res.json();
}

// ─── HL7 Settings ────────────────────────────────────────────────────────────

export interface HL7Settings {
  id: string;
  trigger_mode: "immediate" | "scheduled";
  cron_expression: string;
  max_retries: number;
  timeout: string;
  enabled: boolean;
  updated_at: string;
  last_run?: string;
  next_run?: string;
}

export async function fetchHL7Settings(): Promise<HL7Settings> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/hl7/settings`);
  if (!res.ok) throw new Error("Failed to fetch HL7 settings");
  const json = await res.json();
  return json.data;
}

export async function updateHL7Settings(settings: Partial<Pick<HL7Settings, "trigger_mode" | "cron_expression" | "max_retries" | "enabled">>): Promise<HL7Settings> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/hl7/settings`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(settings),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  const json = await res.json();
  return json.data;
}

export async function triggerHL7Run(): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/hl7/run`, { method: "POST" });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export interface HL7PingResult {
  success: boolean;
  host: string;
  latency: string;
  error?: string;
}

export async function bulkRetryHL7(): Promise<{ count: number; message: string }> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/hl7/bulk-retry`, { method: "POST" });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export async function pingHL7(): Promise<HL7PingResult> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/hl7/ping`, { method: "POST" });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

// ─── HL7 History ─────────────────────────────────────────────────────────────

export interface HL7Attempt {
  id: string;
  ecg_id: string;
  patient_id: string;
  status: "success" | "failed" | "exhausted" | "rejected";
  msa_code?: string;
  msa_message?: string;
  error?: string;
  response_ms: number;
  created_at: string;
}

export async function fetchHL7History(patientId: string): Promise<HL7Attempt[]> {
  const res = await fetch(`${BASE_URL}/api/v1/patients/${patientId}/hl7-history`);
  if (!res.ok) return [];
  const json = await res.json();
  return json.data ?? [];
}

// ─── Tags ───────────────────────────────────────────────────────────────────

export interface TagDTO {
  id: string;
  name: string;
  color: string;
  created_by: string;
  created_at: string;
}

export async function fetchTags(): Promise<TagDTO[]> {
  const res = await fetch(`${BASE_URL}/api/v1/tags`);
  if (!res.ok) return [];
  const json = await res.json();
  return json.data ?? [];
}

export async function createTag(name: string, color?: string): Promise<TagDTO> {
  const res = await fetch(`${BASE_URL}/api/v1/tags`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, color }),
  });
  const json = await res.json();
  return json.data;
}

export async function updateTag(id: string, name: string, color: string): Promise<TagDTO> {
  const res = await fetch(`${BASE_URL}/api/v1/tags/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, color }),
  });
  const json = await res.json();
  return json.data;
}

export async function deleteTag(id: string): Promise<void> {
  await fetch(`${BASE_URL}/api/v1/tags/${id}`, { method: "DELETE" });
}

export async function fetchPatientTags(patientId: string): Promise<TagDTO[]> {
  const res = await fetch(`${BASE_URL}/api/v1/patients/${patientId}/tags`);
  if (!res.ok) return [];
  const json = await res.json();
  return json.data ?? [];
}

export async function tagPatient(patientId: string, tagId: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/patients/${patientId}/tags`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ tag_id: tagId }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function untagPatient(patientId: string, tagId: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/patients/${patientId}/tags/${tagId}`, {
    method: "DELETE",
  });
  if (!res.ok && res.status !== 404) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function fetchECGTags(ecgId: string): Promise<TagDTO[]> {
  const res = await fetch(`${BASE_URL}/api/v1/ecgs/${ecgId}/tags`);
  if (!res.ok) return [];
  const json = await res.json();
  return json.data ?? [];
}

export async function tagECG(ecgId: string, tagId: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/ecgs/${ecgId}/tags`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ tag_id: tagId }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function untagECG(ecgId: string, tagId: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/ecgs/${ecgId}/tags/${tagId}`, {
    method: "DELETE",
  });
  if (!res.ok && res.status !== 404) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export interface VolumeMetric {
  name: string;
  total: number; // bytes (0 = unlimited / rotation disabled)
  available: number; // bytes (may be negative when the soft cap is exceeded)
  max_size?: string; // raw k8s resource quantity configured in storage.max_size (e.g. "50Gi")
}

export interface StorageMetricsResp {
  volumes: VolumeMetric[];
  error?: string;
}
// Api for get Metric volume storage place
export async function fetchStorageMetrics(): Promise<StorageMetricsResp> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/storage-metrics`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export interface RecentError {
  timestamp: string;
  method: string;
  route: string;
  status: number;
  error?: string;
  request_uri: string;
  user_id?: string;
  duration_ms: number;
}

export async function fetchRecentErrors(
  limit = 20,
): Promise<RecentError[]> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/errors?limit=${limit}`);
  if (!res.ok) return [];
  return res.json();
}

// ─── Auth Providers (admin) ─────────────────────────────────────────────────

export interface AuthProviderDTO {
  id: string;
  provider_type: "oidc" | "ldap";
  active: boolean;
  config: Record<string, unknown>;
}

export async function fetchAdminAuthProviders(): Promise<AuthProviderDTO[]> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/auth/providers`);
  if (!res.ok) return [];
  const json = await res.json();
  return json.data ?? [];
}

export async function saveOIDCConfig(config: Record<string, unknown>): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/auth/oidc`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(config),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function saveLDAPConfig(config: Record<string, unknown>): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/auth/ldap`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(config),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function deleteAuthProvider(id: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/auth/providers/${id}`, {
    method: "DELETE",
  });
  if (!res.ok && res.status !== 404) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function testOIDCConnection(config: Record<string, unknown>): Promise<{ success: boolean; error?: string }> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/auth/oidc/test`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(config),
  });
  return res.json();
}

export async function testLDAPConnection(config: Record<string, unknown>): Promise<{ success: boolean; error?: string }> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/auth/ldap/test`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(config),
  });
  return res.json();
}

import type {
  ECG,
  ECGMetaResponse,
  ErrorResponse,
  ListResponse,
  Patient,
} from "../types";

const BASE_URL = (import.meta.env as Record<string, string>).VITE_API_URL ?? "";

export interface MeResponse {
  user_id: string; // stable internal uuid
  username?: string; // human-readable login (display)
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
  if (!res.ok) return { vendors: [], device_models: [], file_formats: [] };
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
  device_model?: string;
  file_format?: string;
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
  if (filters.device_model) params.set("device_model", filters.device_model);
  if (filters.file_format) params.set("file_format", filters.file_format);
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

// ─── Manual ECG upload (offline/isolated devices) ──────────────────────────
export interface UploadFileResult {
  filename: string;
  size: number;
  status: "queued" | "rejected";
  error?: string;
}

export interface UploadResponse {
  files: UploadFileResult[];
  queued: number;
}

// uploadECGs sends one or more ECG files to the manual ingestion endpoint. Each
// accepted file is processed by the same pipeline as FTP/DICOM; live per-file
// status arrives over the /events/ws WebSocket (correlated by filename).
export async function uploadECGs(files: File[]): Promise<UploadResponse> {
  const fd = new FormData();
  for (const f of files) fd.append("files", f);
  // No explicit Content-Type: the browser sets the multipart boundary.
  const res = await fetch(`${BASE_URL}/api/v1/uploads`, {
    method: "POST",
    body: fd,
  });
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

// PatientDataMode controls patient data in converted downloads:
// "file" keeps the values from the source file, "inject" overwrites them with
// the HL7-enriched demographics, "anonymize" strips identifying fields.
export type PatientDataMode = "file" | "inject" | "anonymize";

function patientDataQuery(mode?: PatientDataMode): string {
  if (mode === "inject") return "&inject=1";
  if (mode === "anonymize") return "&anonymize=1";
  return "";
}

// downloadECGFormat triggers a browser download for a specific export format.
// format = "original" | "xmlfda" | "dicom" | ...
export async function downloadECGFormat(
  id: number,
  format: string,
  mode?: PatientDataMode,
): Promise<void> {
  if (format === "original") {
    await triggerBlobDownload(`${BASE_URL}/api/v1/ecgs/${id}/download`);
  } else {
    await triggerBlobDownload(
      `${BASE_URL}/api/v1/ecgs/${id}/download?format=${encodeURIComponent(format)}${patientDataQuery(mode)}`,
    );
  }
}

// downloadECGFormats downloads one or more export formats for a single ECG.
// A single format streams the file directly; multiple formats are bundled by the
// backend into one ZIP archive (a single browser download instead of N).
export async function downloadECGFormats(
  id: number,
  formats: string[],
  mode?: PatientDataMode,
): Promise<void> {
  if (formats.length === 0) return;
  if (formats.length === 1) {
    await downloadECGFormat(id, formats[0], mode);
    return;
  }
  const query = formats.map((f) => `format=${encodeURIComponent(f)}`).join("&");
  await triggerBlobDownload(`${BASE_URL}/api/v1/ecgs/${id}/download?${query}${patientDataQuery(mode)}`);
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

// fetchExportFormats returns the export formats actually available for the given
// ECGs — the union of formats the converter can produce for those ECGs' vendors
// (always includes "original"). Used by the download dialog so it never offers a
// format the backend would reject.
export async function fetchExportFormats(
  ecgIds: number[],
): Promise<ExportFormat[]> {
  const res = await fetch(`${BASE_URL}/api/v1/exports/formats`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ecg_ids: ecgIds }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  const json: { formats: ExportFormat[] } = await res.json();
  return json.formats ?? [];
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

// ORUAttempt mirrors the backend models.HL7ORUAttempt — the outcome of an outbound
// HL7 ORU result-send (ECG result pushed to the HIS/DPI).
export interface ORUAttempt {
  id: string;
  ecg_id: string;
  patient_id: string;
  status: "success" | "rejected" | "failed";
  msa_code?: string;
  msa_message?: string;
  error?: string;
  included_pdf: boolean;
  triggered_by?: string;
  response_ms: number;
  created_at: string;
}

// sendECGResult manually triggers the outbound ORU result-send for an ECG.
// Requires the ecg.send_result permission. Returns the recorded attempt on success;
// throws the ErrorResponse (HIS rejection / send failure / disabled) otherwise.
export async function sendECGResult(ecgId: number): Promise<ORUAttempt> {
  const res = await fetch(`${BASE_URL}/api/v1/ecgs/${ecgId}/send-result`, {
    method: "POST",
  });
  const json = await res.json();
  if (!res.ok) {
    // On a send failure the body is { error, attempt }; guards return { code, message }.
    throw (json.error ?? json) as ErrorResponse;
  }
  return json.attempt as ORUAttempt;
}

// fetchECGORUStatus returns the most recent outbound ORU attempt for an ECG, or null.
export async function fetchECGORUStatus(
  ecgId: number,
): Promise<ORUAttempt | null> {
  const res = await fetch(`${BASE_URL}/api/v1/ecgs/${ecgId}/oru-status`);
  if (!res.ok) throw new Error("Failed to fetch ORU status");
  const json = await res.json();
  return (json.attempt as ORUAttempt | null) ?? null;
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

// API caps per_page at 200; paginate to gather more for exports.
const AUDIT_MAX_PER_PAGE = 200;

// fetchAuditLogsForExport returns the most recent audit logs (DESC order).
// When `all` is true it walks every page until exhausted; otherwise it stops
// once `limit` rows have been collected. Optional `user_id` narrows the export.
export async function fetchAuditLogsForExport(opts: {
  all: boolean;
  limit: number;
  user_id?: string;
}): Promise<import("../types").AuditLog[]> {
  const out: import("../types").AuditLog[] = [];
  let page = 1;
  // Hard ceiling so a runaway "all" export can't loop forever.
  const maxPages = 1000;
  while (page <= maxPages) {
    const target = opts.all
      ? AUDIT_MAX_PER_PAGE
      : Math.min(AUDIT_MAX_PER_PAGE, opts.limit - out.length);
    if (!opts.all && target <= 0) break;
    const res = await fetchAuditLogs({
      page,
      per_page: target,
      user_id: opts.user_id,
    });
    out.push(...res.data);
    if (res.data.length === 0 || out.length >= res.total) break;
    if (!opts.all && out.length >= opts.limit) break;
    page++;
  }
  return opts.all ? out : out.slice(0, opts.limit);
}

export interface PatientFilters {
  q?: string;
  tags?: string[];
  sort_by?: "patient_id" | "last_name" | "created_at" | "last_activity";
  sort_order?: "asc" | "desc";
  page?: number;
  per_page?: number;
  // ECG-level filters: keep only patients owning at least one matching ECG.
  vendor?: string;
  device_model?: string;
  file_format?: string;
  hl7_status?: "pending" | "success" | "hl7_exhausted";
  from?: string;
  to?: string;
}

export async function fetchPatients(
  filters: PatientFilters = {},
): Promise<ListResponse<Patient>> {
  const params = new URLSearchParams();
  if (filters.q) params.set("q", filters.q);
  if (filters.tags && filters.tags.length > 0)
    params.set("tags", filters.tags.join(","));
  if (filters.sort_by) params.set("sort_by", filters.sort_by);
  if (filters.sort_order) params.set("sort_order", filters.sort_order);
  if (filters.vendor) params.set("vendor", filters.vendor);
  if (filters.device_model) params.set("device_model", filters.device_model);
  if (filters.file_format) params.set("file_format", filters.file_format);
  if (filters.hl7_status) params.set("hl7_status", filters.hl7_status);
  if (filters.from) params.set("from", filters.from);
  if (filters.to) params.set("to", filters.to);
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
  "ecg.upload",
  "ecg.send_result",
  "quarantine.read",
  "quarantine.delete",
  "quarantine.assign",
  "admin.users",
  "admin.roles",
  "admin.branding",
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
  id: string; // internal uuid (ecg_hub_users.id)
  external_id: string;
  provider: string;
  role_name: string;
  role_manually_set: boolean; // true = role pinned via UX; false = driven by IdP groups
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
  id: string;
  filename: string;
  file_path: string;
  received_at: string;
  error_reason: string;
  category: "error" | "unidentified";
  vendor?: string;
  recorded_at?: string;
  metadata?: Record<string, unknown>;
}

export async function fetchQuarantine(
  page = 1,
  perPage = 50,
  category?: string,
): Promise<ListResponse<QuarantineEntry>> {
  const params = new URLSearchParams({
    page: String(page),
    per_page: String(perPage),
  });
  if (category) params.set("category", category);
  const res = await fetch(`${BASE_URL}/api/v1/admin/quarantine?${params}`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export async function deleteQuarantineEntry(id: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/quarantine/${id}`, {
    method: "DELETE",
  });
  if (!res.ok && res.status !== 404) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

// assignQuarantineEntry assigns a patient ID to an unidentified quarantine entry.
// The backend re-ingests the file through the normal pipeline (patient upsert +
// ECG insert + HL7 enrichment) and removes the quarantine entry on success.
export async function assignQuarantineEntry(
  id: string,
  patientId: string,
  createNew = false,
): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/quarantine/${id}/assign`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ patient_id: patientId, create_new: createNew }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

// markEcgViewed stamps a single ECG as viewed (clears its "new" indicator).
export async function markEcgViewed(ecgId: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/ecgs/${ecgId}/view`, {
    method: "POST",
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

// markPatientEcgsViewed marks all of a patient's ECGs as viewed ("mark all as seen").
export async function markPatientEcgsViewed(patientId: string): Promise<void> {
  const res = await fetch(
    `${BASE_URL}/api/v1/patients/${encodeURIComponent(patientId)}/ecgs/view`,
    { method: "POST" },
  );
  if (!res.ok) {
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
  anonymize?: boolean; // strip identifying fields in converted outputs
  inject?: boolean; // overwrite patient fields with HL7-enriched demographics
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
  userId: string,
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

// ─── User defaults ──────────────────────────────────────────────────────────

export async function deleteAppUser(id: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/app-users/${id}`, {
    method: "DELETE",
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function fetchUserDefaults(): Promise<{ default_role: string }> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/settings/user-defaults`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  const json: { data: { default_role: string } } = await res.json();
  return json.data;
}

export async function saveUserDefaults(defaultRole: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/settings/user-defaults`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ default_role: defaultRole }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

// ─── Branding ────────────────────────────────────────────────────────────────

export interface Branding {
  center_name: string;
  logo_base64: string;
}

export async function fetchBranding(): Promise<Branding> {
  const res = await fetch(`${BASE_URL}/api/v1/branding`);
  if (!res.ok) return { center_name: "", logo_base64: "" };
  const json: { data: Branding } = await res.json();
  return json.data ?? { center_name: "", logo_base64: "" };
}

export async function saveBranding(
  centerName: string,
  logoBase64?: string,
): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/settings/branding`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      center_name: centerName,
      logo_base64: logoBase64 ?? "",
    }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function uploadLogo(file: File): Promise<string> {
  const form = new FormData();
  form.append("logo", file);
  const res = await fetch(`${BASE_URL}/api/v1/admin/settings/branding/logo`, {
    method: "POST",
    body: form,
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  const json: { data: { logo_base64: string } } = await res.json();
  return json.data.logo_base64;
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

export async function createHL7Preset(
  name: string,
  mappings: { source_path: string; target_field: string }[],
): Promise<HL7Preset> {
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
  await fetch(`${BASE_URL}/api/v1/admin/hl7/presets/${id}/activate`, {
    method: "POST",
  });
}

export async function deleteHL7Preset(id: string): Promise<void> {
  await fetch(`${BASE_URL}/api/v1/admin/hl7/presets/${id}`, {
    method: "DELETE",
  });
}

export async function saveHL7PresetMappings(
  presetId: string,
  mappings: { source_path: string; target_field: string }[],
): Promise<void> {
  const res = await fetch(
    `${BASE_URL}/api/v1/admin/hl7/presets/${presetId}/mappings`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ mappings }),
    },
  );
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function fetchActiveHL7Mappings(): Promise<{
  data: HL7Mapping[];
  active: boolean;
}> {
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
  hl7_enabled: boolean; // global master switch for the HL7 integration at this site
  updated_at: string;
  last_run?: string;
  next_run?: string;
  // Connection settings
  host: string;
  port: number;
  sending_application: string;
  sending_facility: string;
  receiving_application: string;
  receiving_facility: string;
  version: string;
  processing_id: string;
  // Outbound ORU (result-sending) settings
  oru_enabled: boolean;
  oru_trigger_mode: "auto" | "manual";
  oru_host: string;
  oru_port: number;
  oru_include_pdf: boolean;
}

export async function fetchHL7Settings(): Promise<HL7Settings> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/hl7/settings`);
  if (!res.ok) throw new Error("Failed to fetch HL7 settings");
  const json = await res.json();
  return json.data;
}

export async function updateHL7Settings(
  settings: Partial<
    Pick<
      HL7Settings,
      | "trigger_mode"
      | "cron_expression"
      | "max_retries"
      | "enabled"
      | "hl7_enabled"
      | "timeout"
      | "host"
      | "port"
      | "sending_application"
      | "sending_facility"
      | "receiving_application"
      | "receiving_facility"
      | "version"
      | "processing_id"
      | "oru_enabled"
      | "oru_trigger_mode"
      | "oru_host"
      | "oru_port"
      | "oru_include_pdf"
    >
  >,
): Promise<HL7Settings> {
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
  const res = await fetch(`${BASE_URL}/api/v1/admin/hl7/run`, {
    method: "POST",
  });
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

export async function bulkRetryHL7(): Promise<{
  count: number;
  message: string;
}> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/hl7/bulk-retry`, {
    method: "POST",
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export async function pingHL7(): Promise<HL7PingResult> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/hl7/ping`, {
    method: "POST",
  });
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

export async function fetchHL7History(
  patientId: string,
): Promise<HL7Attempt[]> {
  const res = await fetch(
    `${BASE_URL}/api/v1/patients/${patientId}/hl7-history`,
  );
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

export async function updateTag(
  id: string,
  name: string,
  color: string,
): Promise<TagDTO> {
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

export async function tagPatient(
  patientId: string,
  tagId: string,
): Promise<void> {
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

export async function untagPatient(
  patientId: string,
  tagId: string,
): Promise<void> {
  const res = await fetch(
    `${BASE_URL}/api/v1/patients/${patientId}/tags/${tagId}`,
    {
      method: "DELETE",
    },
  );
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

export async function fetchRecentErrors(limit = 20): Promise<RecentError[]> {
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

export async function saveOIDCConfig(
  config: Record<string, unknown>,
): Promise<void> {
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

export async function saveLDAPConfig(
  config: Record<string, unknown>,
): Promise<void> {
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

export async function testOIDCConnection(
  config: Record<string, unknown>,
): Promise<{ success: boolean; error?: string }> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/auth/oidc/test`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(config),
  });
  return res.json();
}

export async function testLDAPConnection(
  config: Record<string, unknown>,
): Promise<{ success: boolean; error?: string }> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/auth/ldap/test`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(config),
  });
  return res.json();
}

// ─── Module Control ──────────────────────────────────────────────────────────

export interface FTPModuleConfig {
  port: number;
  passive_port_range: string;
  public_host: string;
  tls: boolean;
  username: string;
  password: string;
  enabled: boolean;
}

export interface ModuleControlStatus {
  name: string;
  status: "running" | "stopped" | "error";
}

export async function fetchModuleStatuses(): Promise<ModuleControlStatus[]> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/modules/status`);
  if (!res.ok) return [];
  const json = await res.json();
  return json.data ?? [];
}

export async function stopModule(name: string): Promise<void> {
  const res = await fetch(
    `${BASE_URL}/api/v1/admin/modules/${encodeURIComponent(name)}/stop`,
    {
      method: "POST",
    },
  );
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function startModule(name: string): Promise<void> {
  const res = await fetch(
    `${BASE_URL}/api/v1/admin/modules/${encodeURIComponent(name)}/start`,
    {
      method: "POST",
    },
  );
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function fetchFTPConfig(): Promise<FTPModuleConfig> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/modules/ftp/config`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  const json = await res.json();
  return json.data;
}

export async function saveFTPConfig(
  config: Partial<FTPModuleConfig>,
): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/modules/ftp/config`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(config),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export interface DICOMModuleConfig {
  port: number;
  ae_title: string;
  echo_enabled: boolean;
  tls: boolean;
  enabled: boolean;
}

export async function fetchDICOMConfig(): Promise<DICOMModuleConfig> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/modules/dicom/config`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  const json = await res.json();
  return json.data;
}

export async function saveDICOMConfig(
  config: Partial<DICOMModuleConfig>,
): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/modules/dicom/config`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(config),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

// ─── Connector Config (Story 7.6) ────────────────────────────────────────────

// ConnectorType represents the logical UI type of a connector.
// "polaris" maps to protocol "ectp_ftp" (Nihon-Kohden ECTP + FTP forwarding).
// "pacs_dicom" maps to protocol "dicom_cstore" (any DICOM C-STORE target).
export type ConnectorType = "polaris" | "pacs_dicom";

export interface ConnectorConfig {
  name: string;
  protocol: "ectp_ftp" | "dicom_cstore";
  // connector_type is derived from protocol on the client side for display purposes.
  // "polaris" = ectp_ftp, "pacs_dicom" = dicom_cstore
  connector_type?: ConnectorType;
  enabled: boolean;
  extensions: string[];
  vendors: string[];
  max_attempts: number;
  interval: string;
  ectp_host?: string;
  ectp_port?: number;
  ftp_host?: string;
  ftp_port?: number;
  ftp_username?: string;
  ftp_password?: string;
  dicom_host?: string;
  dicom_port?: number;
  calling_ae?: string;
  called_ae?: string;
  dicom_timeout?: string;
}

// connectorTypeFromProtocol maps a backend protocol string to a ConnectorType.
export function connectorTypeFromProtocol(protocol: string): ConnectorType {
  return protocol === "dicom_cstore" ? "pacs_dicom" : "polaris";
}

// protocolFromConnectorType maps a ConnectorType back to the backend protocol string.
export function protocolFromConnectorType(
  ct: ConnectorType,
): ConnectorConfig["protocol"] {
  return ct === "pacs_dicom" ? "dicom_cstore" : "ectp_ftp";
}

export async function fetchConnectorConfigs(): Promise<
  { module_type: string; enabled: boolean; config: ConnectorConfig }[]
> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/connectors/config`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  const json = await res.json();
  return json.data ?? [];
}

export async function saveConnectorConfig(
  name: string,
  config: ConnectorConfig & { enabled: boolean },
): Promise<void> {
  const res = await fetch(
    `${BASE_URL}/api/v1/admin/connectors/${encodeURIComponent(name)}/config`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(config),
    },
  );
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function deleteConnectorConfig(name: string): Promise<void> {
  const res = await fetch(
    `${BASE_URL}/api/v1/admin/connectors/${encodeURIComponent(name)}`,
    { method: "DELETE" },
  );
  if (!res.ok && res.status !== 404) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function testConnector(
  name: string,
): Promise<{ success: boolean; latency: string; error?: string }> {
  const res = await fetch(
    `${BASE_URL}/api/v1/admin/connectors/${encodeURIComponent(name)}/test`,
    { method: "POST" },
  );
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

// ─── Module Settings ─────────────────────────────────────────────────────────

export interface ModuleSettingsData {
  active: string[]; // currently active in DB (empty = all)
  available: string[]; // all compiled-in module names
}

export async function fetchModuleSettings(): Promise<ModuleSettingsData> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/settings/modules`);
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  const body: { data: ModuleSettingsData } = await res.json();
  return body.data;
}

export async function saveModuleSettings(active: string[]): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/admin/settings/modules`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ active }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

// ─── API keys (per-user) ─────────────────────────────────────────────────────

export interface ApiKey {
  id: string;
  name: string;
  prefix: string;
  last_used_at?: string | null;
  created_at: string;
}

// CreatedApiKey extends ApiKey with the plaintext `key`, returned only once at
// creation time and never retrievable again.
export interface CreatedApiKey extends ApiKey {
  key: string;
}

export async function fetchApiKeys(): Promise<ApiKey[]> {
  const res = await fetch(`${BASE_URL}/api/v1/api-keys`);
  if (!res.ok) return [];
  const json = await res.json();
  return json.data ?? [];
}

export async function createApiKey(name: string): Promise<CreatedApiKey> {
  const res = await fetch(`${BASE_URL}/api/v1/api-keys`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  const json = await res.json();
  return json.data;
}

export async function deleteApiKey(id: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/api-keys/${id}`, {
    method: "DELETE",
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

// ───────────────────────── User webhooks ─────────────────────────

// UserWebhook mirrors the backend webhookResponse. Secrets are never returned;
// has_secret / has_auth_header indicate whether values are configured.
export interface UserWebhook {
  id: string;
  name: string;
  url: string;
  enabled: boolean;
  insecure_skip_verify: boolean;
  events: string[];
  vendors: string[];
  has_secret: boolean;
  has_auth_header: boolean;
  last_status_code: number;
  last_error: string;
  last_delivered_at?: string | null;
  created_at: string;
  updated_at: string;
}

// WebhookInput is the create/update body. secret and auth_header use
// tri-state semantics on update: undefined = keep, "" = clear, value = replace.
export interface WebhookInput {
  name: string;
  url: string;
  enabled: boolean;
  insecure_skip_verify: boolean;
  secret?: string;
  auth_header?: string;
  events: string[];
  vendors: string[];
}

export interface WebhookVendorOption {
  name: string;
  extensions: string[];
}

export interface WebhookOptions {
  events: string[];
  vendors: WebhookVendorOption[];
}

export interface WebhookTestResult {
  ok: boolean;
  status_code: number;
  error: string;
}

export async function fetchWebhooks(): Promise<UserWebhook[]> {
  const res = await fetch(`${BASE_URL}/api/v1/webhooks`);
  if (!res.ok) return [];
  return res.json();
}

export async function fetchWebhookOptions(): Promise<WebhookOptions> {
  const res = await fetch(`${BASE_URL}/api/v1/webhooks/options`);
  if (!res.ok) return { events: [], vendors: [] };
  return res.json();
}

export async function createWebhook(input: WebhookInput): Promise<UserWebhook> {
  const res = await fetch(`${BASE_URL}/api/v1/webhooks`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export async function updateWebhook(
  id: string,
  input: WebhookInput,
): Promise<UserWebhook> {
  const res = await fetch(`${BASE_URL}/api/v1/webhooks/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

export async function deleteWebhook(id: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/api/v1/webhooks/${id}`, {
    method: "DELETE",
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
}

export async function testUserWebhook(id: string): Promise<WebhookTestResult> {
  const res = await fetch(`${BASE_URL}/api/v1/webhooks/${id}/test`, {
    method: "POST",
  });
  if (!res.ok) {
    const err: ErrorResponse = await res.json();
    throw err;
  }
  return res.json();
}

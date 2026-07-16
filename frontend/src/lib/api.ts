import type {
  ECG,
  ECGMetaResponse,
  ECGWithPatient,
  ErrorResponse,
  ListResponse,
  Patient,
} from "../types";
import {
  adminClient,
  authClient,
  apiKeyClient,
  brandingClient,
  ecgClient,
  exportClient,
  healthClient,
  hl7Client,
  moduleClient,
  patientClient,
  pinClient,
  sessionClient,
  setupClient,
  tagClient,
  webhookClient,
} from "./grpc";
import type {
  Ecg as EcgProto,
  EcgWithPatient as EcgWithPatientProto,
} from "../gen/v1/ecg_pb";
import type { OruAttempt as OruAttemptProto } from "../gen/v1/hl7_pb";
import type { Patient as PatientProto } from "../gen/v1/patient_pb";
import type { Tag as TagProto } from "../gen/v1/tag_pb";

const BASE_URL = (import.meta.env as Record<string, string>).VITE_API_URL ?? "";

export interface MeResponse {
  user_id: string; // stable internal uuid
  username?: string; // human-readable login (display)
  role: string;
  permissions: string[];
}

export async function fetchSetupStatus(): Promise<{ initialized: boolean }> {
  const res = await setupClient.getStatus({});
  return { initialized: res.initialized };
}

export async function setupAdmin(
  username: string,
  password: string,
): Promise<void> {
  await setupClient.initialize({ username, password });
}

export async function fetchAuthProviders(): Promise<string[]> {
  const res = await authClient.getProviders({});
  return res.providers ?? [];
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
  // gRPC/Connect; the ConnectRequireAuth interceptor throws a ConnectError with
  // code=unauthenticated when the session is missing/invalid (callers catch it
  // exactly as they did the previous throw).
  const res = await sessionClient.getCurrentUser({});
  return {
    user_id: res.userId,
    username: res.username,
    role: res.role,
    permissions: res.permissions,
  };
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
  try {
    const res = await ecgClient.getFilters({});
    return {
      vendors: res.vendors,
      device_models: res.deviceModels,
      file_formats: res.fileFormats,
    };
  } catch {
    return { vendors: [], device_models: [], file_formats: [] };
  }
}

// ecgWithPatientFromProto flattens the nested gRPC EcgWithPatient (ecg + joined
// demographics) into the frontend's flat ECGWithPatient type.
function ecgWithPatientFromProto(r: EcgWithPatientProto): ECGWithPatient {
  return {
    ...ecgFromProto(r.ecg ?? ({} as EcgProto)),
    patient_first_name: r.patientFirstName,
    patient_last_name: r.patientLastName,
    patient_gender: r.patientGender,
    patient_dob: r.patientDob || null,
  };
}

export async function fetchAllECGs(
  filters: AllECGFilters = {},
): Promise<ListResponse<ECGWithPatient>> {
  // gRPC: ECGService.ListAll — cross-patient timeline with joined demographics.
  const res = await ecgClient.listAll({
    q: filters.q ?? "",
    hl7Status: filters.hl7_status ?? "",
    vendor: filters.vendor ?? "",
    deviceModel: filters.device_model ?? "",
    fileFormat: filters.file_format ?? "",
    from: filters.from ?? "",
    to: filters.to ?? "",
    page: filters.page ?? 1,
    perPage: filters.per_page ?? 50,
  });
  return {
    data: res.data.map(ecgWithPatientFromProto),
    total: Number(res.total),
    page: res.page,
    per_page: res.perPage,
  };
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

// ecgFromProto maps the gRPC Ecg message to the frontend ECG type. Mirrors the
// old EcgDTO JSON shape: file_path/immutable are not sent by the API (list view),
// recorded_at is null when empty, extra is the parsed JSON object.
function ecgFromProto(e: EcgProto): ECG {
  return {
    id: e.id as unknown as number, // API id is a string; typing is historical
    patient_id: e.patientId,
    vendor: e.vendor,
    file_path: "",
    original_filename: e.originalFilename,
    recorded_at: e.recordedAt || null,
    ingested_at: e.ingestedAt,
    hl7_status: e.hl7Status as ECG["hl7_status"],
    viewed: e.viewed,
    immutable: false,
    extra: e.extraJson ? (JSON.parse(e.extraJson) as Record<string, unknown>) : {},
  };
}

export async function fetchECGs(
  patientId: number,
  filters: ECGFilters = {},
): Promise<ListResponse<ECG>> {
  // gRPC: PatientService.ListECGs. patientId is really the patient UUID (string)
  // at runtime — the numeric typing is historical; ListECGs resolves UUID or
  // device id server-side.
  const res = await patientClient.listECGs({
    patientId: String(patientId),
    from: filters.from ?? "",
    to: filters.to ?? "",
    vendor: filters.vendor ?? "",
    deviceModel: filters.device_model ?? "",
    fileFormat: filters.file_format ?? "",
    hl7Status: filters.hl7_status ?? "",
    page: filters.page ?? 1,
    perPage: filters.per_page ?? 20,
  });
  return {
    data: res.data.map(ecgFromProto),
    total: Number(res.total),
    page: res.page,
    per_page: res.perPage,
  };
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

// uploadECGs sends one or more ECG files to the manual ingestion endpoint (REST
// multipart — binary stays REST). Each accepted file is processed by the same
// pipeline as FTP/DICOM; live per-file status arrives over the
// EventService.Subscribe gRPC stream (correlated by filename).
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
  const res = await adminClient.getStats({});
  return {
    total_ecgs: Number(res.totalEcgs),
    total_patients: Number(res.totalPatients),
    hl7_pending: Number(res.hl7Pending),
    hl7_success: Number(res.hl7Success),
    hl7_exhausted: Number(res.hl7Exhausted),
    quarantine_count: Number(res.quarantineCount),
  };
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
  const res = await moduleClient.listConnectors({});
  return res.connectors.map((c) => ({
    name: c.name,
    protocol: c.protocol || undefined,
    status: c.status,
    host: c.host || undefined,
    port: c.port || undefined,
    ae_title: c.aeTitle || undefined,
  }));
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
  // gRPC/Connect call via the generated client. The wire type is camelCase
  // (Connect's default JSON), mapped here to the app's snake_case shape so
  // consumers (AdminSystemPage) stay unchanged. The JWT cookie is carried by
  // the transport, so an authenticated caller gets the full payload.
  const res = await healthClient.checkHealth({});
  return {
    status: res.status,
    database: res.database,
    dicom_enabled: res.dicomEnabled,
    dicom_port: res.dicomPort,
    ftp_enabled: res.ftpEnabled,
    ftp_port: res.ftpPort,
    ectp_enabled: res.ectpEnabled,
    ectp_port: res.ectpPort,
    connectors: res.connectors.map((c) => ({
      name: c.name,
      protocol: c.protocol,
      status: c.status,
      host: c.host,
      port: c.port,
      ae_title: c.aeTitle,
    })),
  };
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
  version?: string; // converter binary version, when available
  formats: ExportFormat[];
}

export async function fetchModules(): Promise<ModuleStatus[]> {
  const res = await moduleClient.listModules({});
  return res.modules.map((m) => ({
    name: m.name,
    extensions: m.extensions,
    status: m.status,
    version: m.version || undefined,
    formats: m.formats.map((f) => ({
      id: f.id,
      label: f.label,
      extension: f.extension,
    })),
  }));
}

// fetchExportFormats returns the export formats actually available for the given
// ECGs — the union of formats the converter can produce for those ECGs' vendors
// (always includes "original"). Used by the download dialog so it never offers a
// format the backend would reject.
export async function fetchExportFormats(
  ecgIds: number[],
): Promise<ExportFormat[]> {
  const res = await exportClient.formats({ ecgIds: ecgIds.map(String) });
  return res.formats.map((f) => ({
    id: f.id,
    label: f.label,
    extension: f.extension,
  }));
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
  // gRPC: HL7Service.Force.
  await hl7Client.force({ ecgId: String(ecgId) });
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

// oruAttemptFromProto maps the gRPC OruAttempt to the frontend ORUAttempt.
function oruAttemptFromProto(a: OruAttemptProto): ORUAttempt {
  return {
    id: a.id,
    ecg_id: a.ecgId,
    patient_id: a.patientId,
    status: a.status as ORUAttempt["status"],
    msa_code: a.msaCode || undefined,
    msa_message: a.msaMessage || undefined,
    error: a.error || undefined,
    included_pdf: a.includedPdf,
    triggered_by: a.triggeredBy || undefined,
    response_ms: a.responseMs,
    created_at: a.createdAt,
  };
}

// sendECGResult manually triggers the outbound ORU result-send for an ECG.
// Requires the ecg.send_result permission. Returns the recorded attempt on success;
// throws the ConnectError (HIS rejection / send failure / disabled) otherwise.
export async function sendECGResult(ecgId: number): Promise<ORUAttempt> {
  // gRPC: HL7Service.SendResult. On failure a ConnectError propagates (the UI
  // shows its message; the latest attempt is re-read via fetchECGORUStatus).
  const res = await hl7Client.sendResult({ ecgId: String(ecgId) });
  return oruAttemptFromProto(res.attempt!);
}

// fetchECGORUStatus returns the most recent outbound ORU attempt for an ECG, or null.
export async function fetchECGORUStatus(
  ecgId: number,
): Promise<ORUAttempt | null> {
  // gRPC: HL7Service.GetOruStatus. attempt is unset when the ECG has none.
  const res = await hl7Client.getOruStatus({ ecgId: String(ecgId) });
  return res.attempt ? oruAttemptFromProto(res.attempt) : null;
}

export interface AuditLogFilters {
  user_id?: string;
  action?: string;
  from?: string;
  to?: string;
  page?: number;
  per_page?: number;
}

// auditLogFromProto maps the gRPC AuditLog message to the frontend type. The
// backend passes details as a JSON string (details_json) to avoid re-encoding
// its JSONB column; parse it back to an object here.
function auditLogFromProto(
  a: import("../gen/v1/admin_pb").AuditLog,
): import("../types").AuditLog {
  let details: Record<string, unknown> | null = null;
  if (a.detailsJson) {
    try {
      details = JSON.parse(a.detailsJson) as Record<string, unknown>;
    } catch {
      details = null;
    }
  }
  return {
    id: a.id as unknown as number, // API id is a uuid string; typing is historical
    user_id: a.userId,
    username: a.username || undefined,
    action: a.action as import("../types").AuditLog["action"],
    resource_id: a.resourceId,
    details,
    created_at: a.createdAt,
  };
}

export async function fetchAuditLogs(
  filters: AuditLogFilters = {},
): Promise<ListResponse<import("../types").AuditLog>> {
  const res = await adminClient.listAuditLogs({
    userId: filters.user_id ?? "",
    action: filters.action ?? "",
    from: filters.from ?? "",
    to: filters.to ?? "",
    page: filters.page ?? 1,
    perPage: filters.per_page ?? 50,
  });
  return {
    data: res.data.map(auditLogFromProto),
    total: Number(res.total),
    page: res.page,
    per_page: res.perPage,
  };
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

// patientFromProto maps the gRPC Patient message to the frontend Patient type
// (mirrors dto.PatientWithStatsToDTO). Empty date strings become null.
function patientFromProto(p: PatientProto): Patient {
  return {
    id: p.id as unknown as number, // API id is a string; typing is historical
    patient_id: p.patientId,
    first_name: p.firstName,
    last_name: p.lastName,
    date_of_birth: p.dateOfBirth || null,
    gender: p.gender,
    nda: p.nda || undefined,
    ecg_count: p.ecgCount,
    unviewed_count: p.unviewedCount,
    last_activity: p.lastActivity || null,
  };
}

export async function fetchPatients(
  filters: PatientFilters = {},
): Promise<ListResponse<Patient>> {
  // gRPC: PatientService.Search.
  const res = await patientClient.search({
    q: filters.q ?? "",
    tags: filters.tags?.length ? filters.tags.join(",") : "",
    sortBy: filters.sort_by ?? "",
    sortOrder: filters.sort_order ?? "",
    vendor: filters.vendor ?? "",
    deviceModel: filters.device_model ?? "",
    fileFormat: filters.file_format ?? "",
    hl7Status: filters.hl7_status ?? "",
    from: filters.from ?? "",
    to: filters.to ?? "",
    page: filters.page ?? 1,
    perPage: filters.per_page ?? 50,
  });
  return {
    data: res.data.map(patientFromProto),
    total: Number(res.total),
    page: res.page,
    per_page: res.perPage,
  };
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

// roleFromProto maps the gRPC Role message to the frontend AppRole. The API id
// is a uuid string; AppRole.id is historically typed number (used only as a key
// and echoed back into the update/delete calls), so cast through unknown.
function roleFromProto(r: import("../gen/v1/admin_pb").Role): AppRole {
  return {
    id: r.id as unknown as number,
    name: r.name,
    description: r.description,
    permissions: r.permissions,
  };
}

export async function fetchRoles(): Promise<AppRole[]> {
  const res = await adminClient.listRoles({});
  return res.roles.map(roleFromProto);
}

export async function createRole(
  name: string,
  description: string,
  permissions: string[],
): Promise<AppRole> {
  const res = await adminClient.createRole({ name, description, permissions });
  return res.role ? roleFromProto(res.role) : { id: 0, name, description, permissions };
}

export async function updateRole(
  id: number,
  description: string,
  permissions: string[],
): Promise<void> {
  // name is carried for the audit trail only; the backend keeps it immutable.
  await adminClient.updateRole({ id: String(id), name: "", description, permissions });
}

export async function deleteRole(id: number): Promise<void> {
  await adminClient.deleteRole({ id: String(id) });
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
  const res = await adminClient.listAppUsers({});
  return res.users.map((u) => ({
    id: u.id,
    external_id: u.externalId,
    provider: u.provider,
    role_name: u.roleName,
    role_manually_set: u.roleManuallySet,
    last_login: u.lastLogin,
  }));
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

// quarantineFromProto maps the gRPC QuarantineEntry to the frontend type. The
// backend passes extracted demographics as a JSON string (metadata_json).
function quarantineFromProto(
  e: import("../gen/v1/admin_pb").QuarantineEntry,
): QuarantineEntry {
  let metadata: Record<string, unknown> | undefined;
  if (e.metadataJson) {
    try {
      metadata = JSON.parse(e.metadataJson) as Record<string, unknown>;
    } catch {
      metadata = undefined;
    }
  }
  return {
    id: e.id,
    filename: e.filename,
    file_path: e.filePath,
    received_at: e.receivedAt,
    error_reason: e.errorReason,
    category: (e.category as QuarantineEntry["category"]) || "error",
    vendor: e.vendor || undefined,
    recorded_at: e.recordedAt || undefined,
    metadata,
  };
}

export async function fetchQuarantine(
  page = 1,
  perPage = 50,
  category?: string,
): Promise<ListResponse<QuarantineEntry>> {
  const res = await adminClient.listQuarantine({
    page,
    perPage,
    category: category ?? "",
  });
  return {
    data: res.data.map(quarantineFromProto),
    total: Number(res.total),
    page: res.page,
    per_page: res.perPage,
  };
}

export async function deleteQuarantineEntry(id: string): Promise<void> {
  await adminClient.deleteQuarantine({ id });
}

// assignQuarantineEntry assigns a patient ID to an unidentified quarantine entry.
// The backend re-ingests the file through the normal pipeline (patient upsert +
// ECG insert + HL7 enrichment) and removes the quarantine entry on success.
export async function assignQuarantineEntry(
  id: string,
  patientId: string,
  createNew = false,
): Promise<void> {
  await adminClient.assignQuarantine({ id, patientId, createNew });
}

// markEcgViewed stamps a single ECG as viewed (clears its "new" indicator).
export async function markEcgViewed(ecgId: string): Promise<void> {
  await ecgClient.markViewed({ id: ecgId });
}

// markPatientEcgsViewed marks all of a patient's ECGs as viewed ("mark all as seen").
export async function markPatientEcgsViewed(patientId: string): Promise<void> {
  await patientClient.markECGsViewed({ patientId });
}

export async function fetchECGMeta(ecgId: number): Promise<ECGMetaResponse> {
  // gRPC: ECGService.GetMetadata. values arrive as a JSON object string.
  const res = await ecgClient.getMetadata({ id: String(ecgId) });
  return {
    fields: res.fields.map((f) => ({
      key: f.key,
      label: f.label,
      type: f.type as ECGMetaResponse["fields"][number]["type"],
      options: f.options.length ? f.options : undefined,
    })),
    values: res.valuesJson
      ? (JSON.parse(res.valuesJson) as Record<string, unknown>)
      : {},
  };
}

export async function patchECGMetadata(
  ecgId: number,
  values: Record<string, unknown>,
): Promise<Record<string, unknown>> {
  // gRPC: ECGService.UpdateMetadata. Only editable keys are applied server-side.
  const res = await ecgClient.updateMetadata({
    id: String(ecgId),
    valuesJson: JSON.stringify(values),
  });
  return res.valuesJson
    ? (JSON.parse(res.valuesJson) as Record<string, unknown>)
    : {};
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

// exportJobFromProto maps the gRPC ExportJob (camelCase) to the snake_case
// ExportJobResponse the UI consumes. error is "" on the wire → undefined.
function exportJobFromProto(j: {
  id: string;
  status: string;
  ecgCount: number;
  processedCount: number;
  formats: string[];
  createdAt: string;
  downloadUrl: string;
  error: string;
}): ExportJobResponse {
  return {
    id: j.id,
    status: j.status as ExportJobResponse["status"],
    ecg_count: j.ecgCount,
    processed_count: j.processedCount,
    formats: j.formats,
    created_at: j.createdAt,
    download_url: j.downloadUrl,
    error: j.error || undefined,
  };
}

export async function createExportJob(
  req: ExportJobRequest,
): Promise<ExportJobResponse> {
  const res = await exportClient.create({
    ecgIds: req.ecg_ids.map(String),
    formats: req.formats,
    anonymize: req.anonymize ?? false,
    inject: req.inject ?? false,
  });
  return exportJobFromProto(res.job!);
}

export async function getExportJob(jobId: string): Promise<ExportJobResponse> {
  const res = await exportClient.get({ id: jobId });
  return exportJobFromProto(res.job!);
}

export async function setAppUserRole(
  userId: string,
  role: string,
): Promise<void> {
  await adminClient.setAppUserRole({ id: userId, role });
}

// ─── User defaults ──────────────────────────────────────────────────────────

export async function deleteAppUser(id: string): Promise<void> {
  await adminClient.deleteAppUser({ id });
}

export async function fetchUserDefaults(): Promise<{ default_role: string }> {
  const res = await adminClient.getUserDefaults({});
  return { default_role: res.defaultRole };
}

export async function saveUserDefaults(defaultRole: string): Promise<void> {
  await adminClient.setUserDefaults({ defaultRole });
}

// ─── Branding ────────────────────────────────────────────────────────────────

export interface Branding {
  center_name: string;
  logo_base64: string;
}

export async function fetchBranding(): Promise<Branding> {
  // gRPC/Connect call; branding is non-critical (pre-auth login/setup pages),
  // so any transport error degrades to empty branding rather than throwing.
  try {
    const res = await brandingClient.getBranding({});
    return { center_name: res.centerName, logo_base64: res.logoBase64 };
  } catch {
    return { center_name: "", logo_base64: "" };
  }
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
  try {
    const res = await pinClient.listPins({});
    return res.patientIds;
  } catch {
    return [];
  }
}

export async function pinPatient(patientId: string): Promise<void> {
  await pinClient.pinPatient({ patientId });
}

export async function unpinPatient(patientId: string): Promise<void> {
  await pinClient.unpinPatient({ patientId });
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
  // gRPC: HL7Service.ListAttempts (most recent first, default limit 20).
  const res = await hl7Client.listAttempts({ patientId });
  return res.data.map((a) => ({
    id: a.id,
    ecg_id: a.ecgId,
    patient_id: a.patientId,
    status: a.status as HL7Attempt["status"],
    msa_code: a.msaCode || undefined,
    msa_message: a.msaMessage || undefined,
    error: a.error || undefined,
    response_ms: a.responseMs,
    created_at: a.createdAt,
  }));
}

// ─── Tags ───────────────────────────────────────────────────────────────────

export interface TagDTO {
  id: string;
  name: string;
  color: string;
  created_by: string;
  created_at: string;
}

// tagFromProto maps the gRPC Tag message to the frontend TagDTO.
function tagFromProto(t: TagProto): TagDTO {
  return {
    id: t.id,
    name: t.name,
    color: t.color,
    created_by: t.createdBy,
    created_at: t.createdAt,
  };
}

export async function fetchTags(): Promise<TagDTO[]> {
  const res = await tagClient.listTags({});
  return res.data.map(tagFromProto);
}

export async function createTag(name: string, color?: string): Promise<TagDTO> {
  const res = await tagClient.createTag({ name, color: color ?? "" });
  return tagFromProto(res.tag!);
}

export async function updateTag(
  id: string,
  name: string,
  color: string,
): Promise<TagDTO> {
  const res = await tagClient.updateTag({ id, name, color });
  return tagFromProto(res.tag!);
}

export async function deleteTag(id: string): Promise<void> {
  await tagClient.deleteTag({ id });
}

export async function fetchPatientTags(patientId: string): Promise<TagDTO[]> {
  const res = await tagClient.listPatientTags({ patientId });
  return res.data.map(tagFromProto);
}

export async function tagPatient(
  patientId: string,
  tagId: string,
): Promise<void> {
  await tagClient.tagPatient({ patientId, tagId });
}

export async function untagPatient(
  patientId: string,
  tagId: string,
): Promise<void> {
  await tagClient.untagPatient({ patientId, tagId });
}

export async function fetchECGTags(ecgId: string): Promise<TagDTO[]> {
  const res = await tagClient.listEcgTags({ ecgId });
  return res.data.map(tagFromProto);
}

export async function tagECG(ecgId: string, tagId: string): Promise<void> {
  await tagClient.tagEcg({ ecgId, tagId });
}

export async function untagECG(ecgId: string, tagId: string): Promise<void> {
  await tagClient.untagEcg({ ecgId, tagId });
}

// ── Batch tag reads (kill the per-row N+1) ───────────────────────────────────
// One request resolves tags for a whole list page. Returns a map keyed by the
// entity id; ids with no tags are absent (callers default to []).

export async function fetchECGTagsBatch(
  ecgIds: string[],
): Promise<Record<string, TagDTO[]>> {
  if (ecgIds.length === 0) return {};
  const res = await tagClient.batchGetEcgTags({ ecgIds });
  const out: Record<string, TagDTO[]> = {};
  for (const [id, list] of Object.entries(res.tags)) {
    out[id] = list.tags.map(tagFromProto);
  }
  return out;
}

export async function fetchPatientTagsBatch(
  patientIds: string[],
): Promise<Record<string, TagDTO[]>> {
  if (patientIds.length === 0) return {};
  const res = await tagClient.batchGetPatientTags({ patientIds });
  const out: Record<string, TagDTO[]> = {};
  for (const [id, list] of Object.entries(res.tags)) {
    out[id] = list.tags.map(tagFromProto);
  }
  return out;
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
  const res = await adminClient.getStorageMetrics({});
  return {
    volumes: res.volumes.map((v) => ({
      name: v.name,
      total: Number(v.total),
      available: Number(v.available),
      max_size: v.maxSize || undefined,
    })),
    error: res.error || undefined,
  };
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
  try {
    const res = await adminClient.getRecentErrors({ limit });
    return res.errors.map((e) => ({
      timestamp: e.timestamp,
      method: e.method,
      route: e.route,
      status: e.status,
      error: e.error || undefined,
      request_uri: e.requestUri,
      user_id: e.userId || undefined,
      duration_ms: e.durationMs,
    }));
  } catch {
    return [];
  }
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
  try {
    const res = await moduleClient.listModuleStatus({});
    return res.data.map((s) => ({
      name: s.name,
      status: s.status as ModuleControlStatus["status"],
    }));
  } catch {
    return [];
  }
}

export async function stopModule(name: string): Promise<void> {
  await moduleClient.stopModule({ name });
}

export async function startModule(name: string): Promise<void> {
  await moduleClient.startModule({ name });
}

export async function fetchFTPConfig(): Promise<FTPModuleConfig> {
  const c = await moduleClient.getFTPConfig({});
  return {
    port: c.port,
    passive_port_range: c.passivePortRange,
    public_host: c.publicHost,
    tls: c.tls,
    username: c.username,
    password: c.password,
    enabled: c.enabled,
  };
}

export async function saveFTPConfig(
  config: Partial<FTPModuleConfig>,
): Promise<void> {
  await moduleClient.saveFTPConfig({
    config: {
      port: config.port ?? 0,
      passivePortRange: config.passive_port_range ?? "",
      publicHost: config.public_host ?? "",
      tls: config.tls ?? false,
      username: config.username ?? "",
      password: config.password ?? "",
      enabled: config.enabled ?? false,
    },
  });
}

export interface DICOMModuleConfig {
  port: number;
  ae_title: string;
  echo_enabled: boolean;
  tls: boolean;
  enabled: boolean;
}

export async function fetchDICOMConfig(): Promise<DICOMModuleConfig> {
  const c = await moduleClient.getDICOMConfig({});
  return {
    port: c.port,
    ae_title: c.aeTitle,
    echo_enabled: c.echoEnabled,
    tls: c.tls,
    enabled: c.enabled,
  };
}

export async function saveDICOMConfig(
  config: Partial<DICOMModuleConfig>,
): Promise<void> {
  await moduleClient.saveDICOMConfig({
    config: {
      port: config.port ?? 0,
      aeTitle: config.ae_title ?? "",
      echoEnabled: config.echo_enabled ?? false,
      tls: config.tls ?? false,
      enabled: config.enabled ?? false,
    },
  });
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

// connectorConfigFromProto maps a gRPC ConnectorConfig to the frontend type.
function connectorConfigFromProto(
  c: import("../gen/v1/module_pb").ConnectorConfig,
): ConnectorConfig {
  return {
    name: c.name,
    protocol: c.protocol as ConnectorConfig["protocol"],
    enabled: false, // set by the caller from the entry-level flag
    extensions: c.extensions,
    vendors: c.vendors,
    max_attempts: c.maxAttempts,
    interval: c.interval,
    ectp_host: c.ectpHost || undefined,
    ectp_port: c.ectpPort || undefined,
    ftp_host: c.ftpHost || undefined,
    ftp_port: c.ftpPort || undefined,
    ftp_username: c.ftpUsername || undefined,
    ftp_password: c.ftpPassword || undefined,
    dicom_host: c.dicomHost || undefined,
    dicom_port: c.dicomPort || undefined,
    calling_ae: c.callingAe || undefined,
    called_ae: c.calledAe || undefined,
    dicom_timeout: c.dicomTimeout || undefined,
  };
}

export async function fetchConnectorConfigs(): Promise<
  { module_type: string; enabled: boolean; config: ConnectorConfig }[]
> {
  const res = await moduleClient.listConnectorConfigs({});
  return res.data.map((e) => ({
    module_type: e.moduleType,
    enabled: e.enabled,
    config: {
      ...connectorConfigFromProto(
        e.config ?? ({} as import("../gen/v1/module_pb").ConnectorConfig),
      ),
      enabled: e.enabled,
    },
  }));
}

export async function saveConnectorConfig(
  name: string,
  config: ConnectorConfig & { enabled: boolean },
): Promise<void> {
  await moduleClient.saveConnectorConfig({
    name,
    enabled: config.enabled,
    config: {
      name,
      protocol: config.protocol,
      extensions: config.extensions ?? [],
      vendors: config.vendors ?? [],
      maxAttempts: config.max_attempts ?? 0,
      interval: config.interval ?? "",
      ectpHost: config.ectp_host ?? "",
      ectpPort: config.ectp_port ?? 0,
      ftpHost: config.ftp_host ?? "",
      ftpPort: config.ftp_port ?? 0,
      ftpUsername: config.ftp_username ?? "",
      ftpPassword: config.ftp_password ?? "",
      dicomHost: config.dicom_host ?? "",
      dicomPort: config.dicom_port ?? 0,
      callingAe: config.calling_ae ?? "",
      calledAe: config.called_ae ?? "",
      dicomTimeout: config.dicom_timeout ?? "",
    },
  });
}

export async function deleteConnectorConfig(name: string): Promise<void> {
  await moduleClient.deleteConnector({ name });
}

export async function testConnector(
  name: string,
): Promise<{ success: boolean; latency: string; error?: string }> {
  const res = await moduleClient.testConnector({ name });
  return {
    success: res.success,
    latency: res.latency,
    error: res.error || undefined,
  };
}

// ─── Module Settings ─────────────────────────────────────────────────────────

export interface ModuleSettingsData {
  active: string[]; // currently active in DB (empty = all)
  available: string[]; // all compiled-in module names
}

export async function fetchModuleSettings(): Promise<ModuleSettingsData> {
  const res = await moduleClient.getModuleSettings({});
  return { active: res.active, available: res.available };
}

export async function saveModuleSettings(active: string[]): Promise<void> {
  await moduleClient.saveModuleSettings({ active });
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

// apiKeyFromProto maps the gRPC ApiKey (camelCase) to the snake_case ApiKey the
// UI uses. lastUsedAt is "" on the wire when never used → null.
function apiKeyFromProto(k: {
  id: string;
  name: string;
  prefix: string;
  createdAt: string;
  lastUsedAt: string;
}): ApiKey {
  return {
    id: k.id,
    name: k.name,
    prefix: k.prefix,
    created_at: k.createdAt,
    last_used_at: k.lastUsedAt || null,
  };
}

export async function fetchApiKeys(): Promise<ApiKey[]> {
  try {
    const res = await apiKeyClient.listApiKeys({});
    return res.keys.map(apiKeyFromProto);
  } catch {
    return [];
  }
}

export async function createApiKey(name: string): Promise<CreatedApiKey> {
  const res = await apiKeyClient.createApiKey({ name });
  return { ...apiKeyFromProto(res.key!), key: res.plaintext };
}

export async function deleteApiKey(id: string): Promise<void> {
  await apiKeyClient.deleteApiKey({ id });
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

// webhookFromProto maps the gRPC Webhook (camelCase) to the snake_case
// UserWebhook the UI uses. lastDeliveredAt is "" on the wire → null.
function webhookFromProto(w: {
  id: string;
  name: string;
  url: string;
  enabled: boolean;
  insecureSkipVerify: boolean;
  events: string[];
  vendors: string[];
  hasSecret: boolean;
  hasAuthHeader: boolean;
  lastStatusCode: number;
  lastError: string;
  lastDeliveredAt: string;
  createdAt: string;
  updatedAt: string;
}): UserWebhook {
  return {
    id: w.id,
    name: w.name,
    url: w.url,
    enabled: w.enabled,
    insecure_skip_verify: w.insecureSkipVerify,
    events: w.events,
    vendors: w.vendors,
    has_secret: w.hasSecret,
    has_auth_header: w.hasAuthHeader,
    last_status_code: w.lastStatusCode,
    last_error: w.lastError,
    last_delivered_at: w.lastDeliveredAt || null,
    created_at: w.createdAt,
    updated_at: w.updatedAt,
  };
}

// webhookInputToProto maps the UI input to the proto WebhookInput. secret /
// auth_header stay undefined when not provided (tri-state: leave unchanged).
function webhookInputToProto(input: WebhookInput) {
  return {
    name: input.name,
    url: input.url,
    enabled: input.enabled,
    insecureSkipVerify: input.insecure_skip_verify,
    secret: input.secret,
    authHeader: input.auth_header,
    events: input.events,
    vendors: input.vendors,
  };
}

export async function fetchWebhooks(): Promise<UserWebhook[]> {
  try {
    const res = await webhookClient.listWebhooks({});
    return res.webhooks.map(webhookFromProto);
  } catch {
    return [];
  }
}

export async function fetchWebhookOptions(): Promise<WebhookOptions> {
  try {
    const res = await webhookClient.getOptions({});
    return {
      events: res.events,
      vendors: res.vendors.map((v) => ({
        name: v.name,
        extensions: v.extensions,
      })),
    };
  } catch {
    return { events: [], vendors: [] };
  }
}

export async function createWebhook(input: WebhookInput): Promise<UserWebhook> {
  const res = await webhookClient.createWebhook({
    input: webhookInputToProto(input),
  });
  return webhookFromProto(res.webhook!);
}

export async function updateWebhook(
  id: string,
  input: WebhookInput,
): Promise<UserWebhook> {
  const res = await webhookClient.updateWebhook({
    id,
    input: webhookInputToProto(input),
  });
  return webhookFromProto(res.webhook!);
}

export async function deleteWebhook(id: string): Promise<void> {
  await webhookClient.deleteWebhook({ id });
}

export async function testUserWebhook(id: string): Promise<WebhookTestResult> {
  const res = await webhookClient.testWebhook({ id });
  return { ok: res.ok, status_code: res.statusCode, error: res.error };
}

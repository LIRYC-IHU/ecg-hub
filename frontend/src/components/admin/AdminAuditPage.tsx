import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ChevronLeft,
  ChevronRight,
  Eye,
  Download,
  RefreshCw,
  Trash2,
  Shield,
  Copy,
  ScrollText,
  LogIn,
  KeyRound,
  Webhook,
  Plug,
  Settings,
  Power,
  UserCog,
  FileEdit,
  Search,
  FileUp,
  FileJson,
  ChevronDown,
} from "lucide-react";
import { useAuditLogs } from "../../hooks/useAuditLogs";
import type { AuditLogFilters } from "../../lib/api";
import { fetchAuditLogsForExport } from "../../lib/api";
import { Spinner } from "../ui/Spinner";

const PAGE_SIZE = 50;

type ActionKey =
  | "view"
  | "download"
  | "ecg_download"
  | "ecg_search"
  | "ecg_metadata_update"
  | "patient_ecg_list"
  | "hl7_force"
  | "delete"
  | "quarantine_decision"
  | "ecg_duplicate_skipped"
  | "export_create"
  | "login_success"
  | "login_failed"
  | "role_change"
  | "role_created"
  | "role_updated"
  | "role_deleted"
  | "user_created"
  | "user_deleted"
  | "api_key_created"
  | "api_key_deleted"
  | "webhook_created"
  | "webhook_updated"
  | "webhook_deleted"
  | "auth_config_saved"
  | "auth_config_deleted"
  | "connector_config_saved"
  | "connector_config_deleted"
  | "module_started"
  | "module_stopped"
  | "module_settings_saved"
  | "branding_updated"
  | "hl7_settings_saved"
  | "hl7_bulk_retry"
  | "system_initialized";

const primary = "bg-primary/10 text-primary";
const success = "bg-success/10 text-success";
const warning = "bg-warning/10 text-warning";
const destructive = "bg-destructive/10 text-destructive";

const actionConfig: Record<
  ActionKey,
  { icon: React.ElementType; label: string; cls: string }
> = {
  // Data access
  view: { icon: Eye, label: "view", cls: primary },
  download: { icon: Download, label: "download", cls: success },
  ecg_download: { icon: Download, label: "ecg_download", cls: success },
  ecg_search: { icon: Search, label: "ecg_search", cls: primary },
  ecg_metadata_update: { icon: FileEdit, label: "ecg_metadata_update", cls: warning },
  patient_ecg_list: { icon: Eye, label: "patient_ecg_list", cls: primary },
  hl7_force: { icon: RefreshCw, label: "hl7_force", cls: warning },
  delete: { icon: Trash2, label: "delete", cls: destructive },
  quarantine_decision: { icon: Shield, label: "quarantine_decision", cls: "bg-quarantine/10 text-quarantine" },
  ecg_duplicate_skipped: { icon: Copy, label: "ecg_duplicate_skipped", cls: warning },
  export_create: { icon: FileUp, label: "export_create", cls: success },
  // Auth / session
  login_success: { icon: LogIn, label: "login_success", cls: success },
  login_failed: { icon: LogIn, label: "login_failed", cls: destructive },
  // Users / roles
  role_change: { icon: UserCog, label: "role_change", cls: warning },
  role_created: { icon: Shield, label: "role_created", cls: warning },
  role_updated: { icon: Shield, label: "role_updated", cls: warning },
  role_deleted: { icon: Shield, label: "role_deleted", cls: destructive },
  user_created: { icon: UserCog, label: "user_created", cls: success },
  user_deleted: { icon: UserCog, label: "user_deleted", cls: destructive },
  // Credentials
  api_key_created: { icon: KeyRound, label: "api_key_created", cls: warning },
  api_key_deleted: { icon: KeyRound, label: "api_key_deleted", cls: destructive },
  webhook_created: { icon: Webhook, label: "webhook_created", cls: warning },
  webhook_updated: { icon: Webhook, label: "webhook_updated", cls: warning },
  webhook_deleted: { icon: Webhook, label: "webhook_deleted", cls: destructive },
  // System config
  auth_config_saved: { icon: Settings, label: "auth_config_saved", cls: warning },
  auth_config_deleted: { icon: Settings, label: "auth_config_deleted", cls: destructive },
  connector_config_saved: { icon: Plug, label: "connector_config_saved", cls: warning },
  connector_config_deleted: { icon: Plug, label: "connector_config_deleted", cls: destructive },
  module_started: { icon: Power, label: "module_started", cls: success },
  module_stopped: { icon: Power, label: "module_stopped", cls: destructive },
  module_settings_saved: { icon: Settings, label: "module_settings_saved", cls: warning },
  branding_updated: { icon: Settings, label: "branding_updated", cls: primary },
  hl7_settings_saved: { icon: Settings, label: "hl7_settings_saved", cls: warning },
  hl7_bulk_retry: { icon: RefreshCw, label: "hl7_bulk_retry", cls: warning },
  system_initialized: { icon: Power, label: "system_initialized", cls: success },
};

// Export scope tiers — group actions by sensitivity for filtered JSON exports.
// "all" includes everything (and any future/unknown action). Keep in sync with
// the audit action taxonomy in backend models/audit_log.go.
type ExportScope = "all" | "critical" | "sensitive" | "routine";

const SCOPE_ACTIONS: Record<Exclude<ExportScope, "all">, Set<string>> = {
  // Security / access-critical
  critical: new Set([
    "login_success",
    "login_failed",
    "role_change",
    "role_created",
    "role_updated",
    "role_deleted",
    "user_created",
    "user_deleted",
    "api_key_created",
    "api_key_deleted",
    "auth_config_saved",
    "auth_config_deleted",
    "delete",
    "system_initialized",
  ]),
  // System config changes + data egress
  sensitive: new Set([
    "connector_config_saved",
    "connector_config_deleted",
    "module_started",
    "module_stopped",
    "module_settings_saved",
    "hl7_settings_saved",
    "hl7_bulk_retry",
    "hl7_force",
    "webhook_created",
    "webhook_updated",
    "webhook_deleted",
    "branding_updated",
    "export_create",
    "quarantine_decision",
    "ecg_metadata_update",
  ]),
  // Read / data access + pipeline
  routine: new Set([
    "view",
    "download",
    "ecg_download",
    "ecg_search",
    "patient_ecg_list",
    "ecg_ingested",
    "ecg_duplicate_skipped",
    "hl7_exhausted",
  ]),
};

function actionInScope(actionName: string, scope: ExportScope): boolean {
  if (scope === "all") return true;
  return SCOPE_ACTIONS[scope].has(actionName);
}

export function AdminAuditPage() {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const [userId, setUserId] = useState("");
  const [action, setAction] = useState("");
  const [expandedRow, setExpandedRow] = useState<number | null>(null);

  // JSON export options.
  const [showExport, setShowExport] = useState(false);
  const [expPretty, setExpPretty] = useState(true);
  const [expScope, setExpScope] = useState<ExportScope>("all");
  const [expAll, setExpAll] = useState(false);
  const [expCount, setExpCount] = useState(100);
  const [exporting, setExporting] = useState(false);

  const filters: AuditLogFilters = {
    page,
    per_page: PAGE_SIZE,
    ...(userId ? { user_id: userId } : {}),
    ...(action ? { action } : {}),
  };

  const { logs, total, isLoading } = useAuditLogs(filters);
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  async function exportJSON() {
    setExporting(true);
    try {
      // Scope filtering is client-side, so when a tier is selected we fetch
      // everything and keep the last N matching rows; otherwise honour the
      // "last N / all" choice directly. The on-screen user filter is respected.
      const fetched = await fetchAuditLogsForExport({
        all: expAll || expScope !== "all",
        limit: expCount,
        user_id: userId || undefined,
      });
      const scoped = fetched.filter((l) => actionInScope(l.action, expScope));
      const rows = expAll ? scoped : scoped.slice(0, expCount);

      const payload = {
        exported_at: new Date().toISOString(),
        scope: expScope,
        count: rows.length,
        ...(userId ? { user_id: userId } : {}),
        logs: rows,
      };
      const json = expPretty
        ? JSON.stringify(payload, null, 2)
        : JSON.stringify(payload);
      const blob = new Blob([json], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `audit-${expScope}-${new Date().toISOString().slice(0, 10)}.json`;
      a.click();
      URL.revokeObjectURL(url);
      setShowExport(false);
    } finally {
      setExporting(false);
    }
  }

  return (
    <div className="p-6">
      {/* Header */}
      <div className="mb-6 flex items-center gap-3">
        <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-primary/10 text-primary ring-1 ring-primary/20">
          <ScrollText className="h-5 w-5" />
        </div>
        <div>
          <h1 className="font-display text-2xl font-semibold tracking-tight text-foreground">
            {t("admin.audit.title")}
          </h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {t("admin.audit.subtitle")}
          </p>
        </div>
      </div>

      {/* Filter bar */}
      <div className="flex gap-3 flex-wrap items-center mb-4">
        <input
          type="text"
          value={userId}
          onChange={(e) => {
            setUserId(e.target.value);
            setPage(1);
          }}
          placeholder={t("admin.audit.filterUser")}
          className="border border-border rounded-lg px-3 py-2 text-sm bg-card focus:outline-none focus:ring-2 focus:ring-ring/20 placeholder:text-muted-foreground/50 w-48"
        />
        <select
          value={action}
          onChange={(e) => {
            setAction(e.target.value);
            setPage(1);
          }}
          className="border border-border rounded-lg px-2 py-2 text-sm bg-card focus:outline-none focus:ring-2 focus:ring-ring/20 text-muted-foreground"
        >
          <option value="">{t("admin.audit.allActions")}</option>
          {(Object.keys(actionConfig) as ActionKey[]).map((a) => (
            <option key={a} value={a}>
              {a}
            </option>
          ))}
        </select>

        <div className="ml-auto flex items-center gap-3">
          {isLoading && <Spinner size={14} className="text-muted-foreground" />}
          {!isLoading && (
            <span className="text-xs text-muted-foreground">
              {total} {t("admin.audit.entries")}
            </span>
          )}
          <div className="relative">
            <button
              onClick={() => setShowExport((v) => !v)}
              className="flex items-center gap-1.5 text-xs px-3 py-1.5 border border-border rounded-lg hover:bg-muted/40 transition-colors text-muted-foreground"
            >
              <FileJson size={13} />
              {t("admin.audit.exportJSON")}
              <ChevronDown
                size={13}
                className={`transition-transform ${showExport ? "rotate-180" : ""}`}
              />
            </button>

            {showExport && (
              <>
                {/* Click-away backdrop */}
                <div
                  className="fixed inset-0 z-10"
                  onClick={() => setShowExport(false)}
                />
                <div className="absolute right-0 mt-2 z-20 w-72 bg-card border border-border rounded-xl shadow-lg p-4 space-y-4">
                  <p className="text-xs font-semibold text-foreground">
                    {t("admin.audit.exportTitle")}
                  </p>

                  {/* Format */}
                  <div className="space-y-1.5">
                    <span className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider">
                      {t("admin.audit.exportFormat")}
                    </span>
                    <div className="flex gap-1.5">
                      {[
                        { v: true, label: t("admin.audit.exportPretty") },
                        { v: false, label: t("admin.audit.exportCompact") },
                      ].map((o) => (
                        <button
                          key={String(o.v)}
                          onClick={() => setExpPretty(o.v)}
                          className={`flex-1 text-xs px-2 py-1.5 rounded-md border transition-colors ${
                            expPretty === o.v
                              ? "border-primary bg-primary/10 text-primary"
                              : "border-border text-muted-foreground hover:bg-muted/40"
                          }`}
                        >
                          {o.label}
                        </button>
                      ))}
                    </div>
                  </div>

                  {/* Scope */}
                  <div className="space-y-1.5">
                    <span className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider">
                      {t("admin.audit.exportScope")}
                    </span>
                    <select
                      value={expScope}
                      onChange={(e) =>
                        setExpScope(e.target.value as ExportScope)
                      }
                      className="w-full text-xs border border-border rounded-md px-2 py-1.5 bg-background text-foreground focus:outline-none focus:ring-1 focus:ring-ring/20"
                    >
                      <option value="all">
                        {t("admin.audit.exportScopeAll")}
                      </option>
                      <option value="critical">
                        {t("admin.audit.exportScopeCritical")}
                      </option>
                      <option value="sensitive">
                        {t("admin.audit.exportScopeSensitive")}
                      </option>
                      <option value="routine">
                        {t("admin.audit.exportScopeRoutine")}
                      </option>
                    </select>
                  </div>

                  {/* Count (tail N / all) */}
                  <div className="space-y-1.5">
                    <span className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider">
                      {t("admin.audit.exportCount")}
                    </span>
                    <div className="flex items-center gap-2">
                      <span className="text-xs text-muted-foreground">
                        {t("admin.audit.exportLastN")}
                      </span>
                      <input
                        type="number"
                        min={1}
                        value={expCount}
                        disabled={expAll}
                        onChange={(e) =>
                          setExpCount(Math.max(1, Number(e.target.value) || 1))
                        }
                        className="w-20 text-xs border border-border rounded-md px-2 py-1.5 bg-background text-foreground focus:outline-none focus:ring-1 focus:ring-ring/20 disabled:opacity-40"
                      />
                      <label className="ml-auto flex items-center gap-1.5 text-xs text-muted-foreground cursor-pointer">
                        <input
                          type="checkbox"
                          checked={expAll}
                          onChange={(e) => setExpAll(e.target.checked)}
                          className="accent-primary"
                        />
                        {t("admin.audit.exportAll")}
                      </label>
                    </div>
                  </div>

                  <button
                    onClick={exportJSON}
                    disabled={exporting}
                    className="w-full flex items-center justify-center gap-1.5 text-xs px-3 py-2 rounded-lg bg-primary text-primary-foreground hover:bg-primary/90 transition-colors disabled:opacity-50"
                  >
                    {exporting ? (
                      <Spinner size={13} />
                    ) : (
                      <Download size={13} />
                    )}
                    {exporting
                      ? t("admin.audit.exporting")
                      : t("admin.audit.exportDownload")}
                  </button>
                </div>
              </>
            )}
          </div>
        </div>
      </div>

      {/* Table */}
      <div className="bg-card rounded-lg border border-border overflow-hidden">
        {/* Header row */}
        <div className="grid grid-cols-[140px_1fr_180px_120px_10px] gap-4 px-4 py-2.5 bg-muted/50 border-b border-border">
          <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
            {t("admin.audit.colDate")}
          </span>
          <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
            {t("admin.audit.colUser")}
          </span>
          <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
            {t("admin.audit.colAction")}
          </span>
          <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
            {t("admin.audit.colResource")}
          </span>
          <span />
        </div>

        {/* Loading */}
        {isLoading && (
          <div className="flex items-center justify-center py-12">
            <Spinner size={18} className="text-muted-foreground" />
          </div>
        )}

        {/* Empty */}
        {!isLoading && logs.length === 0 && (
          <div className="px-4 py-12 text-center text-sm text-muted-foreground">
            {t("admin.audit.noEntries")}
          </div>
        )}

        {/* Rows */}
        {logs.map((log) => {
          const cfg = actionConfig[log.action as ActionKey];
          const Icon = cfg?.icon;
          const isExpanded = expandedRow === log.id;

          return (
            <div key={log.id} className="border-b border-border last:border-0">
              <div className="grid grid-cols-[140px_1fr_110px_120px_80px] gap-4 px-4 py-3 items-center hover:bg-muted/20 transition-colors">
                {/* Date */}
                <span className="text-xs text-muted-foreground whitespace-nowrap">
                  {new Date(log.created_at).toLocaleString("fr-FR")}
                </span>

                {/* User — display name, raw UUID on hover for traceability */}
                <span
                  className="text-xs text-foreground truncate"
                  title={log.user_id}
                >
                  {log.username || log.user_id}
                </span>

                {/* Action badge */}
                <span
                  className={`inline-flex items-center gap-1 text-[11px] font-medium px-2 py-0.5 rounded-full w-fit ${cfg?.cls ?? "bg-muted text-muted-foreground"}`}
                >
                  {Icon && <Icon size={10} />}
                  {log.action}
                </span>

                {/* Details toggle */}
                {log.details ? (
                  <button
                    onClick={() => setExpandedRow(isExpanded ? null : log.id)}
                    className="text-xs text-primary hover:text-primary/80 transition-colors text-right"
                  >
                    {isExpanded ? t("admin.audit.hide") : t("admin.audit.show")}
                  </button>
                ) : (
                  <span className="text-xs text-muted-foreground/40">—</span>
                )}
              </div>

              {/* Expanded details */}
              {isExpanded && log.details && (
                <div className="px-4 pb-3">
                  <pre className="text-[11px] font-mono bg-muted/40 rounded-lg px-3 py-2.5 overflow-x-auto text-muted-foreground leading-relaxed">
                    {JSON.stringify(log.details, null, 2)}
                  </pre>
                </div>
              )}
            </div>
          );
        })}
      </div>

      {/* Pagination */}
      <div className="flex items-center gap-3 justify-end mt-4">
        <span className="text-xs text-muted-foreground">
          {page} / {totalPages}
        </span>
        <button
          onClick={() => setPage((p) => Math.max(1, p - 1))}
          disabled={page === 1}
          className="p-1.5 rounded-md border border-border hover:bg-muted/40 disabled:opacity-40 transition-colors"
        >
          <ChevronLeft size={14} />
        </button>
        <button
          onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
          disabled={page === totalPages}
          className="p-1.5 rounded-md border border-border hover:bg-muted/40 disabled:opacity-40 transition-colors"
        >
          <ChevronRight size={14} />
        </button>
      </div>
    </div>
  );
}

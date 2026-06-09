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
} from "lucide-react";
import { useAuditLogs } from "../../hooks/useAuditLogs";
import type { AuditLogFilters } from "../../lib/api";
import { Spinner } from "../ui/Spinner";

const PAGE_SIZE = 50;

type ActionKey =
  | "view"
  | "download"
  | "hl7_force"
  | "delete"
  | "quarantine_decision"
  | "ecg_duplicate_skipped";

const actionConfig: Record<
  ActionKey,
  { icon: React.ElementType; label: string; cls: string }
> = {
  view: { icon: Eye, label: "view", cls: "bg-primary/10 text-primary" },
  download: {
    icon: Download,
    label: "download",
    cls: "bg-success/10 text-success",
  },
  hl7_force: {
    icon: RefreshCw,
    label: "hl7_force",
    cls: "bg-warning/10 text-warning",
  },
  delete: {
    icon: Trash2,
    label: "delete",
    cls: "bg-destructive/10 text-destructive",
  },
  quarantine_decision: {
    icon: Shield,
    label: "quarantine_decision",
    cls: "bg-quarantine/10 text-quarantine",
  },
  ecg_duplicate_skipped: {
    icon: Copy,
    label: "ecg_duplicate_skipped",
    cls: "bg-warning/10 text-warning",
  },
};

export function AdminAuditPage() {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const [userId, setUserId] = useState("");
  const [action, setAction] = useState("");
  const [expandedRow, setExpandedRow] = useState<number | null>(null);

  const filters: AuditLogFilters = {
    page,
    per_page: PAGE_SIZE,
    ...(userId ? { user_id: userId } : {}),
    ...(action ? { action } : {}),
  };

  const { logs, total, isLoading } = useAuditLogs(filters);
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  function exportCSV() {
    const header = "date,user,action,resource_id";
    const rows = logs.map((l) =>
      [
        new Date(l.created_at).toISOString(),
        l.user_id,
        l.action,
        l.resource_id,
      ].join(","),
    );
    const blob = new Blob([[header, ...rows].join("\n")], { type: "text/csv" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `audit-${new Date().toISOString().slice(0, 10)}.csv`;
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <div className="p-6">
      {/* Header */}
      <div className="mb-6">
        <h1 className="text-lg font-semibold text-foreground">
          {t("admin.audit.title")}
        </h1>
        <p className="text-sm text-muted-foreground mt-1">
          {t("admin.audit.subtitle")}
        </p>
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
          <button
            onClick={exportCSV}
            className="text-xs px-3 py-1.5 border border-border rounded-lg hover:bg-muted/40 transition-colors text-muted-foreground"
          >
            {t("admin.audit.exportCSV")}
          </button>
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

                {/* User */}
                <span className="text-xs font-mono text-foreground truncate">
                  {log.user_id}
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

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  AlertTriangle,
  Trash2,
  FileWarning,
  ChevronLeft,
  ChevronRight,
} from "lucide-react";
import { fetchQuarantine, deleteQuarantineEntry } from "../../lib/api";
import { Spinner } from "../ui/Spinner";
import { EmptyState } from "../ui/EmptyState";
import { useNotification } from "../../context/NotificationContext";

interface Props {
  canDelete?: boolean;
}

function timeAgo(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime();
  const hours = Math.floor(diff / (1000 * 60 * 60));
  const days = Math.floor(hours / 24);
  if (days > 0) return `Il y a ${days}j`;
  if (hours > 0) return `Il y a ${hours}h`;
  return "Récemment";
}

export function AdminQuarantinePage({ canDelete }: Props) {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [page, setPage] = useState(1);
  const [confirmDelete, setConfirmDelete] = useState<number | null>(null);
  const [expandedError, setExpandedError] = useState<number | null>(null);
  const [perPage, setPerPage] = useState<number>(25);
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false);
  const { data, isLoading } = useQuery({
    queryKey: ["admin", "quarantine", page],
    queryFn: () => fetchQuarantine(page, perPage),
    staleTime: 30_000,
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => deleteQuarantineEntry(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "quarantine"] });
      setConfirmDelete(null);
      notify("success", t("admin.quarantine.deleted"));
    },
    onError: () => notify("error", t("admin.quarantine.deleteError")),
  });

  const toggleSelect = (id: number) => {
    setSelected((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  };

  function selectAllOnPage() {
    if (selected.size === entries.length) {
      setSelected(new Set());
    } else {
      setSelected(new Set(entries.map((e) => e.id)));
    }
  }

  const deleteManyMutation = useMutation({
    // add deleted confirmation
    mutationFn: async (ids: number[]) => {
      await Promise.all(ids.map((id) => deleteQuarantineEntry(id)));
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "quarantine"] });
      setSelected(new Set());
      notify("success", t("admin.quarantine.deletedMany"));
    },
    onError: () => notify("error", t("admin.quarantine.deleteError")),
  });

  const entries = data?.data ?? [];
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / perPage));

  return (
    <div className="p-6">
      {/* Header */}
      <div className="flex items-center justify-between gap-3 mb-2">
        <div className="flex items-center gap-3 mb-2">
          {canDelete && (
            <input
              type="checkbox"
              checked={selected.size === entries.length && entries.length > 0}
              onChange={selectAllOnPage}
              className="w-4 h-4 rounded border-border accent-primary cursor-pointer"
            />
          )}
          <AlertTriangle className="w-5 h-5 text-quarantine" />
          <h1 className="text-lg font-semibold text-foreground">
            {t("admin.quarantine.title")}
          </h1>
          {total > 0 && (
            <span className="text-[10px] font-semibold bg-quarantine text-quarantine-foreground px-2 py-0.5 rounded-full">
              {total}
            </span>
          )}
        </div>
        <div>
          <select
            value={perPage}
            onChange={(e) => setPerPage(Number(e.target.value))}
            className="text-sm bg-card border border-border rounded-lg px-3 py-2 text-foreground focus:outline-none focus:ring-2 focus:ring-ring/20"
          >
            <option value={25}>25 / page</option>
            <option value={50}>50 / page</option>
            <option value={100}>100 / page</option>
            <option value={200}>200 / page</option>
          </select>
        </div>
      </div>
      <p className="text-sm text-muted-foreground mb-4">
        {t("quarantine.ingested")}
      </p>

      {/* Warning banner */}
      {total > 0 && (
        <div className="bg-quarantine/5 border border-quarantine/20 rounded-lg px-4 py-3 mb-4">
          <p className="text-xs text-quarantine">{t("quarantine.verify")}</p>
        </div>
      )}

      {/* Loading */}
      {isLoading && (
        <div className="flex justify-center py-12">
          <Spinner size={20} className="text-muted-foreground" />
        </div>
      )}

      {/* Empty */}
      {!isLoading && entries.length === 0 && <EmptyState type="quarantine" />}

      {/* Table */}
      {!isLoading && entries.length > 0 && (
        <div className="bg-card rounded-lg border border-border overflow-hidden">
          <div className="grid grid-cols-[1fr_100px_1fr_80px] gap-4 px-4 py-2.5 bg-muted/50 border-b border-border">
            <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
              {t("admin.quarantine.colFilename")}
            </span>
            <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
              {t("admin.quarantine.colReceivedAt")}
            </span>
            <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
              {t("admin.quarantine.colReason")}
            </span>
            <span />
          </div>

          {entries.map((entry) => (
            <div
              key={entry.id}
              className="grid grid-cols-[30px_1fr_100px_1fr_80px] gap-4 px-4 py-3 items-center border-b border-border last:border-0 hover:bg-muted/20 transition-colors"
            >
              {canDelete && (
                <input
                  type="checkbox"
                  checked={selected.has(entry.id)}
                  onChange={() => toggleSelect(entry.id)}
                  className="w-4 h-4 rounded border-border accent-primary cursor-pointer"
                />
              )}
              {/* Filename */}
              <div className="flex items-center gap-2 min-w-0">
                <FileWarning className="w-4 h-4 text-quarantine shrink-0" />
                <span className="text-xs font-mono font-medium text-foreground truncate">
                  {entry.filename}
                </span>
              </div>

              {/* Received */}
              <span
                className="text-xs text-muted-foreground"
                title={new Date(entry.received_at).toLocaleString("fr-FR")}
              >
                {timeAgo(entry.received_at)}
              </span>

              {/* Error — expandable */}
              <button
                onClick={() =>
                  setExpandedError(expandedError === entry.id ? null : entry.id)
                }
                className="text-xs text-quarantine text-left hover:text-quarantine/80 transition-colors min-w-0"
                title={entry.error_reason}
              >
                {expandedError === entry.id
                  ? entry.error_reason
                  : entry.error_reason.length > 50
                    ? entry.error_reason.slice(0, 50) + "…"
                    : entry.error_reason}
              </button>

              {/* Delete */}
              {canDelete && (
                <div className="flex items-center gap-1 justify-end">
                  {confirmDelete === entry.id ? (
                    <>
                      <button
                        onClick={() => deleteMutation.mutate(entry.id)}
                        disabled={deleteMutation.isPending}
                        className="text-[10px] font-medium text-destructive hover:underline disabled:opacity-50 flex items-center gap-1"
                      >
                        {deleteMutation.isPending &&
                        deleteMutation.variables === entry.id ? (
                          <Spinner size={10} />
                        ) : null}
                        {t("common.confirm")}
                      </button>
                      <button
                        onClick={() => setConfirmDelete(null)}
                        className="text-[10px] text-muted-foreground hover:underline"
                      >
                        {t("common.cancel")}
                      </button>
                    </>
                  ) : (
                    <button
                      onClick={() => setConfirmDelete(entry.id)}
                      className="p-1.5 rounded hover:bg-destructive/10 transition-colors"
                      title={t("admin.quarantine.delete")}
                    >
                      <Trash2 className="w-3.5 h-3.5 text-destructive/60" />
                    </button>
                  )}
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Pagination */}
      {!isLoading && entries.length > 0 && (
        <div className="flex items-center justify-between mt-4">
          <span className="text-xs text-muted-foreground">
            {page == 0 ? (
              <span className="text-xs text-muted-foreground">
                {total} {t("quarantine.file")}
              </span>
            ) : (
              <span className="text-xs text-muted-foreground">
                {total} {t("quarantine.files")}
              </span>
            )}
          </span>
          <div className="flex items-center gap-2">
            <button
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page === 1}
              className="p-1.5 rounded border border-border hover:bg-muted/40 disabled:opacity-50 transition-colors"
            >
              <ChevronLeft className="w-4 h-4 text-muted-foreground" />
            </button>
            {page == 0 ? (
              <span className="text-xs text-muted-foreground">
                {t("quarantine.file")} / {totalPages}
              </span>
            ) : (
              <span className="text-xs text-muted-foreground">
                {page} / {totalPages}
              </span>
            )}
            <button
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page === totalPages}
              className="p-1.5 rounded border border-border hover:bg-muted/40 disabled:opacity-50 transition-colors"
            >
              <ChevronRight className="w-4 h-4 text-muted-foreground" />
            </button>
          </div>
        </div>
      )}
      {selected.size > 0 && (
        <div className="fixed bottom-0 left-52 right-0 z-20 border-t border-border bg-card/90 backdrop-blur-sm px-6 py-3 flex items-center justify-between shrink-0 ">
          <div className="flex items-center gap-2 text-sm text-foreground"></div>
          <div className="flex items-center gap-3">
            <button
              onClick={() => setConfirmBulkDelete(true)}
              className="flex items-center hover:bg-destructive/10 transition-colors gap-2 bg-red-600 text-foreground text-sm font-medium px-4 py-2 rounded-lg"
            >
              <Trash2 className="w-3.5 h-3.5 text-foreground" />
              {t("ecg.delete")}
            </button>{" "}
          </div>
        </div>
      )}
      {confirmBulkDelete && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
          <div className="bg-card border border-border rounded-lg p-6 w-full max-w-md shadow-xl">
            <div className="flex items-center gap-2 mb-3">
              <AlertTriangle className="w-5 h-5 text-destructive" />
              <h2 className="text-sm font-semibold">
                {t("admin.quarantine.deleteConfirmTitle") ??
                  "Confirmer la suppression"}
              </h2>
            </div>

            <p className="text-xs text-muted-foreground mb-5">
              {t("admin.quarantine.deleteConfirmMany") ??
                `Supprimer ${selected.size} fichier(s) définitivement ?`}
            </p>

            <div className="flex justify-end gap-2">
              <button
                onClick={() => setConfirmBulkDelete(false)}
                className="px-3 py-1.5 text-xs rounded border border-border hover:bg-muted"
              >
                {t("common.cancel")}
              </button>

              <button
                onClick={() => {
                  deleteManyMutation.mutate(Array.from(selected));
                  setConfirmBulkDelete(false);
                }}
                disabled={deleteManyMutation.isPending}
                className="px-3 py-1.5 text-xs rounded bg-destructive text-white hover:opacity-90 disabled:opacity-50"
              >
                {deleteManyMutation.isPending ? "..." : t("common.confirm")}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

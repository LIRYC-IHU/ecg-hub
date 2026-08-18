import { useState, useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  AlertTriangle,
  Trash2,
  FileWarning,
  ChevronLeft,
  ChevronRight,
  UserPlus,
  HelpCircle,
} from "lucide-react";
import type { QuarantineEntry } from "../../lib/api";
import type { Patient } from "../../types";
import {
  fetchQuarantine,
  deleteQuarantineEntry,
  assignQuarantineEntry,
  fetchPatients,
} from "../../lib/api";
import { formatPatientName } from "../../lib/patient";
import { Spinner } from "../ui/Spinner";
import { EmptyState } from "../ui/EmptyState";
import { useNotification } from "../../context/NotificationContext";

interface Props {
  canDelete?: boolean;
  canAssign?: boolean;
}

// extraField reads a demographic value from the serialized ECGMetadata.Extra map.
function extraField(entry: QuarantineEntry, key: string): string {
  const extra = (entry.metadata?.Extra ?? {}) as Record<string, unknown>;
  const v = extra[key];
  return v == null ? "" : String(v);
}

// demographicsSummary builds a short human-readable identity line for review.
function demographicsSummary(entry: QuarantineEntry): string {
  const parts = [
    [extraField(entry, "last_name"), extraField(entry, "first_name")]
      .filter(Boolean)
      .join(" "),
    extraField(entry, "birth_date"),
    extraField(entry, "sex"),
  ].filter(Boolean);
  return parts.join(" · ");
}

function timeAgo(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime();
  const hours = Math.floor(diff / (1000 * 60 * 60));
  const days = Math.floor(hours / 24);
  if (days > 0) return `Il y a ${days}j`;
  if (hours > 0) return `Il y a ${hours}h`;
  return "Récemment";
}

export function AdminQuarantinePage({ canDelete, canAssign }: Props) {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [page, setPage] = useState(1);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [expandedError, setExpandedError] = useState<string | null>(null);
  const [perPage, setPerPage] = useState<number>(25);
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false);
  const [assigningId, setAssigningId] = useState<string | null>(null);
  const { data, isLoading } = useQuery({
    queryKey: ["admin", "quarantine", page],
    queryFn: () => fetchQuarantine(page, perPage),
    staleTime: 30_000,
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteQuarantineEntry(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "quarantine"] });
      setConfirmDelete(null);
      notify("success", t("admin.quarantine.deleted"));
    },
    onError: () => notify("error", t("admin.quarantine.deleteError")),
  });

  const assignMutation = useMutation({
    mutationFn: ({
      id,
      patientId,
      createNew,
    }: {
      id: string;
      patientId: string;
      createNew: boolean;
    }) => assignQuarantineEntry(id, patientId, createNew),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "quarantine"] });
      setAssigningId(null);
      notify("success", t("admin.quarantine.assigned"));
    },
    onError: () => notify("error", t("admin.quarantine.assignError")),
  });

  const toggleSelect = (id: string) => {
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
    mutationFn: async (ids: string[]) => {
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
          <AlertTriangle className="w-6 h-6 text-quarantine" />
          <h1 className="font-display text-2xl font-semibold tracking-tight text-foreground">
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

          {entries.map((entry) => {
            const isUnidentified = entry.category === "unidentified";
            const demographics = isUnidentified
              ? demographicsSummary(entry)
              : "";
            return (
              <div key={entry.id}>
                <div className="grid grid-cols-[30px_1fr_100px_1fr_110px] gap-4 px-4 py-3 items-center border-b border-border hover:bg-muted/20 transition-colors">
                  {canDelete && (
                    <input
                      type="checkbox"
                      checked={selected.has(entry.id)}
                      onChange={() => toggleSelect(entry.id)}
                      className="w-4 h-4 rounded border-border accent-primary cursor-pointer"
                    />
                  )}
                  {/* Filename + category */}
                  <div className="flex flex-col gap-1 min-w-0">
                    <div className="flex items-center gap-2 min-w-0">
                      {isUnidentified ? (
                        <HelpCircle className="w-4 h-4 text-amber-500 shrink-0" />
                      ) : (
                        <FileWarning className="w-4 h-4 text-quarantine shrink-0" />
                      )}
                      <span className="text-xs font-mono font-medium text-foreground truncate">
                        {entry.filename}
                      </span>
                      <span
                        className={`text-[9px] font-semibold px-1.5 py-0.5 rounded-full shrink-0 ${
                          isUnidentified
                            ? "bg-amber-500/15 text-amber-600"
                            : "bg-quarantine/15 text-quarantine"
                        }`}
                      >
                        {isUnidentified
                          ? t("admin.quarantine.categoryUnidentified")
                          : t("admin.quarantine.categoryError")}
                      </span>
                    </div>
                    {isUnidentified && demographics && (
                      <span className="text-[11px] text-muted-foreground truncate pl-6">
                        {demographics}
                        {entry.vendor ? ` — ${entry.vendor}` : ""}
                      </span>
                    )}
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
                      setExpandedError(
                        expandedError === entry.id ? null : entry.id,
                      )
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

                  {/* Actions */}
                  <div className="flex items-center gap-1 justify-end">
                    {isUnidentified && canAssign && (
                      <button
                        onClick={() =>
                          setAssigningId(
                            assigningId === entry.id ? null : entry.id,
                          )
                        }
                        className="flex items-center gap-1 text-[10px] font-medium text-primary hover:underline px-1.5 py-1 rounded hover:bg-primary/10 transition-colors"
                        title={t("admin.quarantine.assign")}
                      >
                        <UserPlus className="w-3.5 h-3.5" />
                        {t("admin.quarantine.assign")}
                      </button>
                    )}
                    {canDelete &&
                      (confirmDelete === entry.id ? (
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
                      ))}
                  </div>
                </div>

                {/* Assign panel — search + confirm (anti wrong-patient) */}
                {isUnidentified && assigningId === entry.id && (
                  <AssignPanel
                    isPending={assignMutation.isPending}
                    onConfirm={(patientId, createNew) =>
                      assignMutation.mutate({
                        id: entry.id,
                        patientId,
                        createNew,
                      })
                    }
                    onCancel={() => setAssigningId(null)}
                  />
                )}
              </div>
            );
          })}
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

// AssignPanel drives the "assign an unidentified ECG to a patient" flow as a
// search → select → confirm sequence. Two outcomes:
//   • existing patient → pick from search, confirm name + DOB (anti wrong-patient);
//   • unknown ID → explicit "new patient" path (create_new), where demographics
//     are filled later by HL7 enrichment from the HIS. This preserves the original
//     unidentified-ECG workflow, which assigns to patient IDs not yet in ECG Hub.
function AssignPanel({
  isPending,
  onConfirm,
  onCancel,
}: {
  isPending: boolean;
  onConfirm: (patientId: string, createNew: boolean) => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  const [q, setQ] = useState("");
  const [debounced, setDebounced] = useState("");
  const [selected, setSelected] = useState<Patient | null>(null);
  const [newMode, setNewMode] = useState(false);

  useEffect(() => {
    const h = setTimeout(() => setDebounced(q.trim()), 250);
    return () => clearTimeout(h);
  }, [q]);

  const { data, isFetching } = useQuery({
    queryKey: ["assign-patient-search", debounced],
    queryFn: () => fetchPatients({ q: debounced, per_page: 8 }),
    enabled: debounced.length >= 2 && !selected && !newMode,
    staleTime: 10_000,
  });
  const results = data?.data ?? [];

  const fmtDob = (dob: string | null) =>
    dob ? new Date(dob).toLocaleDateString("fr-FR") : "—";

  // A new-patient ID candidate only makes sense when the query looks like an ID
  // (no spaces) and nothing matched — nudging the nurse to type the HIS ID.
  const canCreateNew =
    !isFetching &&
    debounced.length >= 2 &&
    results.length === 0 &&
    !debounced.includes(" ");

  return (
    <div className="px-4 py-3 bg-primary/5 border-b border-border">
      <p className="text-[11px] text-muted-foreground mb-2">
        {t("admin.quarantine.assignHint")}
      </p>

      {selected ? (
        // ── Existing patient confirmation ──
        <>
          <div className="text-xs bg-card border border-warning/40 rounded-lg px-3 py-2.5">
            <p className="text-[11px] text-muted-foreground mb-1">
              {t("admin.quarantine.assignConfirmQuestion")}
            </p>
            <p className="font-semibold text-foreground">
              {formatPatientName(selected)}
            </p>
            <p className="text-muted-foreground">
              {t("admin.quarantine.bornOn")} {fmtDob(selected.date_of_birth)}
              {" · "}
              {selected.nda || selected.patient_id}
            </p>
          </div>
          <div className="flex items-center gap-2 mt-2">
            <button
              onClick={() => onConfirm(selected.patient_id, false)}
              disabled={isPending}
              className="flex items-center gap-1 text-xs font-medium bg-primary text-primary-foreground px-3 py-2 rounded-lg hover:opacity-90 disabled:opacity-50"
            >
              {isPending ? (
                <Spinner size={12} />
              ) : (
                <UserPlus className="w-3.5 h-3.5" />
              )}
              {t("admin.quarantine.assignConfirm")}
            </button>
            <button
              onClick={() => setSelected(null)}
              className="text-xs text-muted-foreground hover:underline px-2"
            >
              {t("admin.quarantine.assignChange")}
            </button>
            <button
              onClick={onCancel}
              className="text-xs text-muted-foreground hover:underline px-2"
            >
              {t("common.cancel")}
            </button>
          </div>
        </>
      ) : newMode ? (
        // ── New patient confirmation (demographics via HL7) ──
        <>
          <div className="text-xs bg-card border border-warning/40 rounded-lg px-3 py-2.5">
            <p className="text-[11px] text-muted-foreground mb-1">
              {t("admin.quarantine.assignNewConfirmQuestion")}
            </p>
            <p className="font-semibold text-foreground">
              {t("admin.quarantine.newPatientId")} {debounced}
            </p>
            <p className="text-[11px] text-muted-foreground mt-1">
              {t("admin.quarantine.assignNewHint")}
            </p>
          </div>
          <div className="flex items-center gap-2 mt-2">
            <button
              onClick={() => onConfirm(debounced, true)}
              disabled={isPending}
              className="flex items-center gap-1 text-xs font-medium bg-primary text-primary-foreground px-3 py-2 rounded-lg hover:opacity-90 disabled:opacity-50"
            >
              {isPending ? (
                <Spinner size={12} />
              ) : (
                <UserPlus className="w-3.5 h-3.5" />
              )}
              {t("admin.quarantine.assignNewConfirm")}
            </button>
            <button
              onClick={() => setNewMode(false)}
              className="text-xs text-muted-foreground hover:underline px-2"
            >
              {t("admin.quarantine.assignChange")}
            </button>
            <button
              onClick={onCancel}
              className="text-xs text-muted-foreground hover:underline px-2"
            >
              {t("common.cancel")}
            </button>
          </div>
        </>
      ) : (
        // ── Search ──
        <>
          <input
            autoFocus
            type="text"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={t("admin.quarantine.assignSearchPlaceholder")}
            className="w-full text-xs bg-card border border-border rounded-lg px-3 py-2 text-foreground focus:outline-none focus:ring-2 focus:ring-ring/20"
          />
          <div className="mt-2 space-y-1">
            {isFetching && (
              <Spinner size={12} className="text-muted-foreground" />
            )}
            {results.map((p) => (
              <button
                key={p.patient_id}
                onClick={() => setSelected(p)}
                className="w-full text-left text-xs bg-card border border-border rounded-lg px-3 py-2 hover:bg-muted/40 transition-colors"
              >
                <span className="font-medium text-foreground">
                  {formatPatientName(p)}
                </span>
                <span className="text-muted-foreground">
                  {" — "}
                  {t("admin.quarantine.bornOn")} {fmtDob(p.date_of_birth)}
                  {" · "}
                  {p.nda || p.patient_id}
                </span>
              </button>
            ))}
            {canCreateNew && (
              <div className="pt-1">
                <p className="text-[11px] text-muted-foreground mb-1">
                  {t("admin.quarantine.assignNoMatch")}
                </p>
                <button
                  onClick={() => setNewMode(true)}
                  className="w-full flex items-center gap-1.5 text-xs font-medium text-primary border border-dashed border-primary/40 rounded-lg px-3 py-2 hover:bg-primary/10 transition-colors"
                >
                  <UserPlus className="w-3.5 h-3.5" />
                  {t("admin.quarantine.assignNewWithId")} «&nbsp;{debounced}&nbsp;»
                </button>
              </div>
            )}
          </div>
          <button
            onClick={onCancel}
            className="mt-2 text-xs text-muted-foreground hover:underline"
          >
            {t("common.cancel")}
          </button>
        </>
      )}
    </div>
  );
}

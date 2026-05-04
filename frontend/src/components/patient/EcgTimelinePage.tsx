import { useState, useCallback, useEffect, useRef } from "react";
import {
  Search,
  Download,
  ChevronLeft,
  ChevronRight,
  X,
  Clock,
  AlertTriangle,
} from "lucide-react";
import { useAllECGs } from "../../hooks/useAllECGs";
import { Spinner } from "../ui/Spinner";
import { ExportFooter } from "../export/ExportFooter";
import { DownloadFormatPopup } from "../ecg/DownloadFormatPopup";
import { downloadECGFormat } from "../../lib/api";
import { useNotification } from "../../context/NotificationContext";
import type { AllECGFilters } from "../../lib/api";
import type { ECGWithPatient } from "../../types";

type ExternalFilters = Pick<AllECGFilters, "vendor" | "device_model" | "file_format" | "hl7_status" | "from" | "to">;

type QuickFilter = "all" | "today" | "pending";

function PatientAvatar({
  firstName,
  lastName,
  gender,
}: {
  firstName: string;
  lastName: string;
  gender: string;
}) {
  const initials = [lastName?.[0], firstName?.[0]]
    .filter(Boolean)
    .join("")
    .toUpperCase();
  const isF = gender?.toLowerCase() === "f";
  return (
    <div
      className={`w-7 h-7 rounded-full flex items-center justify-center text-[10px] font-semibold flex-shrink-0 ${
        isF
          ? "bg-pink-500/10 text-pink-400 ring-1 ring-pink-500/20"
          : "bg-blue-500/10 text-blue-400 ring-1 ring-blue-500/20"
      }`}
    >
      {initials || "?"}
    </div>
  );
}

function HL7StatusBadge({ status }: { status: string }) {
  const map: Record<string, { cls: string; label: string }> = {
    success: { cls: "bg-green-500/10 text-green-400", label: "Enrichi" },
    pending: { cls: "bg-amber-500/10 text-amber-400", label: "En attente" },
    hl7_exhausted: {
      cls: "bg-red-500/10 text-red-400",
      label: "HL7 épuisé",
    },
  };
  const s = map[status] ?? map.pending;
  return (
    <span
      className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-medium ${s.cls}`}
    >
      <span className="w-1.5 h-1.5 rounded-full bg-current opacity-70" />
      {s.label}
    </span>
  );
}

function VendorBadge({ vendor }: { vendor: string }) {
  const colors: Record<string, string> = {
    philips: "text-blue-400 bg-blue-500/10",
    mortara: "text-violet-400 bg-violet-500/10",
    "nihon-kohden": "text-pink-400 bg-pink-500/10",
    ge: "text-emerald-400 bg-emerald-500/10",
    dicom: "text-amber-400 bg-amber-500/10",
  };
  const cls =
    colors[vendor?.toLowerCase()] ??
    "text-muted-foreground bg-muted/50";
  return (
    <span
      className={`font-mono text-[11px] font-medium px-2 py-0.5 rounded ${cls}`}
    >
      {vendor || "—"}
    </span>
  );
}

function formatDateTime(ecg: ECGWithPatient): { date: string; time: string } {
  const d = new Date((ecg.recorded_at ?? ecg.ingested_at) as string);
  return {
    date: d.toLocaleDateString("fr-FR", {
      day: "2-digit",
      month: "2-digit",
      year: "numeric",
    }),
    time: d.toLocaleTimeString("fr-FR", {
      hour: "2-digit",
      minute: "2-digit",
    }),
  };
}

function ageFromDOB(dob: string | null): number | null {
  if (!dob) return null;
  const d = new Date(dob);
  const now = new Date();
  let age = now.getFullYear() - d.getFullYear();
  if (now < new Date(now.getFullYear(), d.getMonth(), d.getDate())) age--;
  return age;
}

interface Props {
  canDelete: boolean;
  canRead: boolean;
  canWrite: boolean;
  canForceHL7: boolean;
  search: string;
  onSearchChange: (value: string) => void;
  filters: ExternalFilters;
}

export function EcgTimelinePage({
  canDelete,
  canRead,
  search,
  onSearchChange,
  filters,
}: Props) {
  const { notify } = useNotification();
  const [downloadOpenId, setDownloadOpenId] = useState<string | null>(null);
  const [downloading, setDownloading] = useState(false);
  const [debouncedSearch, setDebouncedSearch] = useState(search);
  const [quickFilter, setQuickFilter] = useState<QuickFilter>("all");
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState<number>(() => {
    return Number(localStorage.getItem("ecghub.timelinePerPage") ?? 50);
  });
  const [selectedECGs, setSelectedECGs] = useState<Set<string>>(new Set());
  const checkAllRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const id = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(id);
  }, [search]);

  const clearSearch = () => onSearchChange("");

  useEffect(() => {
    setPage(1);
  }, [debouncedSearch, quickFilter, perPage, filters]);

  const handlePerPageChange = (value: number) => {
    setPerPage(value);
    localStorage.setItem("ecghub.timelinePerPage", String(value));
  };

  const today = new Date().toISOString().slice(0, 10);
  const apiFilters: AllECGFilters = {
    ...(debouncedSearch ? { q: debouncedSearch } : {}),
    ...(quickFilter === "today" ? { from: today, to: today } : {}),
    ...(quickFilter === "pending" ? { hl7_status: "pending" } : {}),
    ...filters,
    page,
    per_page: perPage,
  };

  const { ecgs, total, isLoading } = useAllECGs(apiFilters);

  const allChecked =
    ecgs.length > 0 && ecgs.every((e) => selectedECGs.has(e.id as unknown as string));
  const someChecked =
    selectedECGs.size > 0 && !allChecked;

  useEffect(() => {
    if (checkAllRef.current) {
      checkAllRef.current.indeterminate = someChecked;
    }
  }, [someChecked]);

  const toggleAll = () => {
    if (allChecked || someChecked) {
      setSelectedECGs(new Set());
    } else {
      setSelectedECGs(new Set(ecgs.map((e) => e.id as unknown as string)));
    }
  };

  const toggle = (id: string) => {
    setSelectedECGs((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  };

  const handleClearSelection = useCallback(() => setSelectedECGs(new Set()), []);

  const totalPages = Math.ceil(total / perPage);

  const chipConfig: {
    key: QuickFilter;
    label: string;
    Icon?: typeof Clock;
  }[] = [
    { key: "all", label: "Tous" },
    { key: "today", label: "Aujourd'hui", Icon: Clock },
    { key: "pending", label: "En attente HL7", Icon: AlertTriangle },
  ];

  return (
    <div className="relative flex flex-col h-full overflow-hidden">
      {/* Top bar */}
      <div className="sticky top-0 z-10 bg-card border-b border-border px-6 py-3 shrink-0">
        <div className="flex items-center gap-3 mb-3">
          <div className="relative flex-1 max-w-lg">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
            <input
              autoFocus
              type="text"
              value={search}
              onChange={(e) => onSearchChange(e.target.value)}
              placeholder="Rechercher par patient, identifiant, nom de fichier…"
              className={`w-full pl-10 ${search ? "pr-9" : "pr-4"} py-2 text-sm bg-muted/50 border-0 rounded-full focus:outline-none focus:ring-2 focus:ring-ring/20 placeholder:text-muted-foreground/60`}
            />
            {search && (
              <button
                onClick={clearSearch}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground transition-colors"
              >
                <X className="w-4 h-4" />
              </button>
            )}
          </div>
          {isLoading ? (
            <Spinner size={15} className="text-muted-foreground shrink-0" />
          ) : (
            <span className="text-xs text-muted-foreground bg-muted px-3 py-1.5 rounded-full shrink-0">
              {total} ECGs
            </span>
          )}
        </div>

        {/* Filter chips */}
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-[10px] font-semibold text-muted-foreground uppercase tracking-wider mr-1">
            Vue
          </span>
          {chipConfig.map(({ key, label, Icon }) => {
            const active = quickFilter === key;
            return (
              <button
                key={key}
                onClick={() => setQuickFilter(key)}
                className={`inline-flex items-center gap-1.5 px-3 py-1 rounded-md text-xs font-medium transition-colors border ${
                  active
                    ? "bg-primary/10 text-primary border-primary/30"
                    : "text-muted-foreground border-border hover:text-foreground hover:bg-muted/50"
                }`}
              >
                {Icon && <Icon className="w-3 h-3" />}
                {label}
              </button>
            );
          })}
        </div>
      </div>

      <main className="flex-1 overflow-auto pb-20">
        {/* Selection banner */}
        {selectedECGs.size > 0 && (
          <div className="mx-6 mt-4 flex items-center gap-3 bg-primary/5 border border-primary/20 rounded-lg px-4 py-2.5">
            <span className="text-sm font-medium text-foreground">
              {selectedECGs.size} ECG
              {selectedECGs.size > 1 ? "s" : ""} sélectionné
              {selectedECGs.size > 1 ? "s" : ""}
            </span>
            <div className="flex-1" />
            <button
              onClick={handleClearSelection}
              className="text-muted-foreground hover:text-foreground transition-colors"
            >
              <X className="w-4 h-4" />
            </button>
          </div>
        )}

        <div className="px-6 py-4">
          {isLoading ? (
            <div className="flex justify-center py-16">
              <Spinner size={24} className="text-muted-foreground" />
            </div>
          ) : ecgs.length === 0 ? (
            <div className="text-center py-16 text-muted-foreground text-sm">
              Aucun ECG trouvé
            </div>
          ) : (
            <div className="border border-border rounded-lg overflow-hidden">
              {/* Header row */}
              <div className="grid grid-cols-[40px_140px_1fr_140px_110px_130px_1fr_72px] gap-0 px-4 py-2 bg-muted/50 border-b border-border text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                <div>
                  <input
                    ref={checkAllRef}
                    type="checkbox"
                    checked={allChecked}
                    onChange={toggleAll}
                    className="w-4 h-4 rounded border-border accent-primary cursor-pointer"
                  />
                </div>
                <div>Date &amp; heure</div>
                <div>Patient</div>
                <div>Identifiant</div>
                <div>Appareil</div>
                <div>Statut HL7</div>
                <div>Fichier</div>
                <div className="text-right">Actions</div>
              </div>

              {/* Data rows */}
              {ecgs.map((ecg, i) => {
                const { date, time } = formatDateTime(ecg);
                const age = ageFromDOB(ecg.patient_dob);
                const id = ecg.id as unknown as string;
                const isSelected = selectedECGs.has(id);

                return (
                  <div
                    key={ecg.id}
                    onClick={() => toggle(id)}
                    className={`grid grid-cols-[40px_140px_1fr_140px_110px_130px_1fr_72px] gap-0 px-4 py-2.5 items-center text-sm border-b border-border/40 transition-colors cursor-pointer last:border-b-0 ${
                      isSelected
                        ? "bg-primary/5"
                        : i % 2 === 0
                          ? ""
                          : "bg-muted/10"
                    } hover:bg-muted/30`}
                  >
                    <div
                      onClick={(e) => e.stopPropagation()}
                      className="flex items-center"
                    >
                      <input
                        type="checkbox"
                        checked={isSelected}
                        onChange={() => toggle(id)}
                        className="w-4 h-4 rounded border-border accent-primary cursor-pointer"
                      />
                    </div>

                    <div className="font-mono text-xs text-muted-foreground leading-tight">
                      <div>{date}</div>
                      <div className="text-muted-foreground/50">{time}</div>
                    </div>

                    <div className="flex items-center gap-2.5 min-w-0">
                      <PatientAvatar
                        firstName={ecg.patient_first_name}
                        lastName={ecg.patient_last_name}
                        gender={ecg.patient_gender}
                      />
                      <div className="min-w-0">
                        <div className="font-medium text-foreground truncate">
                          {ecg.patient_last_name || "—"}
                          {ecg.patient_first_name
                            ? `, ${ecg.patient_first_name}`
                            : ""}
                        </div>
                        <div className="text-[11px] text-muted-foreground">
                          {ecg.patient_gender || "—"}
                          {age !== null ? ` · ${age} ans` : ""}
                        </div>
                      </div>
                    </div>

                    <div className="font-mono text-xs text-muted-foreground truncate">
                      {ecg.patient_id}
                    </div>

                    <div>
                      <VendorBadge vendor={ecg.vendor} />
                    </div>

                    <div>
                      <HL7StatusBadge status={ecg.hl7_status} />
                    </div>

                    <div
                      className="font-mono text-[11px] text-muted-foreground/60 truncate"
                      title={ecg.original_filename}
                    >
                      {ecg.original_filename}
                    </div>

                    <div
                      className="flex items-center gap-1 justify-end"
                      onClick={(e) => e.stopPropagation()}
                    >
                      {canRead && (
                        <>
                          <button
                            title="Télécharger"
                            onClick={() => setDownloadOpenId(id)}
                            disabled={downloading && downloadOpenId === id}
                            className="w-7 h-7 rounded flex items-center justify-center text-muted-foreground hover:text-foreground hover:bg-muted transition-colors disabled:opacity-50"
                          >
                            {downloading && downloadOpenId === id
                              ? <Spinner size={13} className="text-muted-foreground" />
                              : <Download className="w-3.5 h-3.5" />
                            }
                          </button>
                          <DownloadFormatPopup
                            open={downloadOpenId === id}
                            onClose={() => setDownloadOpenId(null)}
                            vendor={ecg.vendor}
                            busy={downloading}
                            onConfirm={async (formats) => {
                              setDownloadOpenId(null);
                              setDownloading(true);
                              try {
                                for (const fmt of formats) {
                                  await downloadECGFormat(ecg.id as unknown as number, fmt).catch(() => {
                                    notify("error", "Erreur de téléchargement");
                                  });
                                }
                              } finally {
                                setDownloading(false);
                              }
                            }}
                          />
                        </>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          )}

          {/* Pagination */}
          {!isLoading && total > 0 && (
            <div className="flex items-center justify-between pt-4">
              <div className="flex items-center gap-3">
                <span className="text-xs text-muted-foreground">
                  {(page - 1) * perPage + 1}–{Math.min(page * perPage, total)}{" "}
                  sur {total}
                </span>
                <div className="flex items-center gap-1.5">
                  <span className="text-xs text-muted-foreground">Afficher</span>
                  {[25, 50, 100].map((n) => (
                    <button
                      key={n}
                      onClick={() => handlePerPageChange(n)}
                      className={`min-w-[32px] h-6 rounded text-xs font-medium transition-colors ${
                        perPage === n
                          ? "bg-primary text-primary-foreground"
                          : "border border-border hover:bg-muted text-muted-foreground"
                      }`}
                    >
                      {n}
                    </button>
                  ))}
                </div>
              </div>
              <div className="flex items-center gap-1">
                <button
                  onClick={() => setPage((p) => Math.max(1, p - 1))}
                  disabled={page === 1}
                  className="p-1.5 rounded border border-border hover:bg-muted transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
                >
                  <ChevronLeft className="w-4 h-4" />
                </button>
                {Array.from(
                  { length: Math.min(totalPages, 7) },
                  (_, i) => i + 1,
                ).map((p) => (
                  <button
                    key={p}
                    onClick={() => setPage(p)}
                    className={`min-w-[32px] h-8 rounded text-sm font-medium transition-colors ${
                      p === page
                        ? "bg-primary text-primary-foreground"
                        : "border border-border hover:bg-muted"
                    }`}
                  >
                    {p}
                  </button>
                ))}
                <button
                  onClick={() =>
                    setPage((p) => Math.min(totalPages, p + 1))
                  }
                  disabled={page === totalPages}
                  className="p-1.5 rounded border border-border hover:bg-muted transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
                >
                  <ChevronRight className="w-4 h-4" />
                </button>
              </div>
            </div>
          )}
        </div>
      </main>

      {selectedECGs.size > 0 && (
        <ExportFooter
          count={selectedECGs.size}
          ecgIds={Array.from(selectedECGs) as unknown as number[]}
          onClear={handleClearSelection}
        />
      )}
    </div>
  );
}

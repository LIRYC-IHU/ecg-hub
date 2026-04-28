import { useState, useCallback, useEffect } from "react";
import {
  Search,
  ChevronRight,
  Eye,
  Download,
  Trash2,
  Send,
  X,
  ChevronLeft,
  ChevronRight as ChevronRightIcon,
  RefreshCw,
} from "lucide-react";
import { usePatients } from "../../hooks/usePatients";
import { useECGs } from "../../hooks/useECGs";
import { Spinner } from "../ui/Spinner";
import { ExportFooter } from "../export/ExportFooter";
import { downloadECGFormat } from "../../lib/api";
import type { Patient, ECG } from "../../types";

// ─── Helpers ─────────────────────────────────────────────────────────────────

function formatDate(iso: string | null): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleDateString("fr-FR", {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
  });
}

function formatDateTime(ecg: ECG): string {
  const d = new Date((ecg.recorded_at ?? ecg.ingested_at) as string);
  return d.toLocaleString("fr-FR", {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function ageFromDOB(dob: string | null): number | null {
  if (!dob) return null;
  const d = new Date(dob);
  const now = new Date();
  let age = now.getFullYear() - d.getFullYear();
  if (now < new Date(now.getFullYear(), d.getMonth(), d.getDate())) age--;
  return age;
}

function PatientAvatar({
  patient,
  size = 28,
}: {
  patient: Patient;
  size?: number;
}) {
  const isF = patient.gender?.toLowerCase() === "f";
  const initials = [patient.last_name?.[0], patient.first_name?.[0]]
    .filter(Boolean)
    .join("")
    .toUpperCase();
  const sz = `${size}px`;
  return (
    <div
      className={`rounded-full flex items-center justify-center font-semibold flex-shrink-0 ${
        isF
          ? "bg-pink-500/10 text-pink-400 ring-1 ring-pink-500/20"
          : "bg-blue-500/10 text-blue-400 ring-1 ring-blue-500/20"
      }`}
      style={{ width: sz, height: sz, fontSize: size >= 40 ? 14 : 10 }}
    >
      {initials || "?"}
    </div>
  );
}

function HL7Badge({ status }: { status: string }) {
  const cfg: Record<string, { cls: string; label: string }> = {
    success: { cls: "bg-green-500/10 text-green-400", label: "Envoyé" },
    pending: { cls: "bg-amber-500/10 text-amber-400", label: "En attente" },
    hl7_exhausted: {
      cls: "bg-red-500/10 text-red-400",
      label: "HL7 épuisé",
    },
  };
  const s = cfg[status] ?? cfg.pending;
  return (
    <span
      className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded-full text-[10px] font-medium ${s.cls}`}
    >
      <span className="w-1 h-1 rounded-full bg-current" />
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
  return (
    <span
      className={`font-mono text-[10px] font-medium px-1.5 py-0.5 rounded ${
        colors[vendor?.toLowerCase()] ?? "text-muted-foreground bg-muted/50"
      }`}
    >
      {vendor || "—"}
    </span>
  );
}

// ─── Inline ECG sub-rows (loaded lazily when patient is expanded) ─────────────

function EcgSubRows({
  patient,
  selectedECGs,
  onToggle,
  canDelete,
  canRead,
  onLoaded,
}: {
  patient: Patient;
  selectedECGs: Set<string>;
  onToggle: (id: string) => void;
  canDelete: boolean;
  canRead: boolean;
  onLoaded: (ids: string[]) => void;
}) {
  const { ecgs, isLoading } = useECGs(patient.id as unknown as number, {
    per_page: 50,
  });

  useEffect(() => {
    if (!isLoading && ecgs.length > 0) {
      onLoaded(ecgs.map((e) => e.id as unknown as string));
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isLoading, ecgs.length]);

  if (isLoading) {
    return (
      <div className="flex justify-center py-3 bg-muted/5 border-b border-border/40">
        <Spinner size={14} className="text-muted-foreground" />
      </div>
    );
  }

  return (
    <>
      {ecgs.map((ecg) => {
        const id = ecg.id as unknown as string;
        const isSelected = selectedECGs.has(id);
        return (
          <div
            key={ecg.id}
            onClick={() => onToggle(id)}
            className={`grid gap-0 px-4 py-2 items-center text-xs border-b border-border/30 cursor-pointer transition-colors ${
              isSelected ? "bg-primary/5" : "bg-muted/5 hover:bg-muted/20"
            }`}
            style={{ gridTemplateColumns: "40px 32px 1fr 40px 40px" }}
          >
            <div onClick={(e) => e.stopPropagation()}>
              <input
                type="checkbox"
                checked={isSelected}
                onChange={() => onToggle(id)}
                className="w-3.5 h-3.5 rounded border-border accent-primary cursor-pointer"
              />
            </div>
            <div />
            <div className="flex items-center gap-2.5 min-w-0">
              <span className="font-mono text-muted-foreground">
                {formatDateTime(ecg)}
              </span>
              <VendorBadge vendor={ecg.vendor} />
              <HL7Badge status={ecg.hl7_status} />
              <span
                className="font-mono text-[10px] text-muted-foreground/50 truncate"
                title={ecg.original_filename}
              >
                {ecg.original_filename}
              </span>
            </div>
            <div />
            <div
              className="flex items-center gap-0.5 justify-end"
              onClick={(e) => e.stopPropagation()}
            >
              {canRead && (
                <button
                  title="Télécharger"
                  onClick={() =>
                    void downloadECGFormat(ecg.id as unknown as number, "original")
                  }
                  className="w-6 h-6 rounded flex items-center justify-center text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
                >
                  <Download className="w-3 h-3" />
                </button>
              )}
              {canDelete && (
                <button
                  title="Supprimer"
                  className="w-6 h-6 rounded flex items-center justify-center text-muted-foreground hover:text-red-400 hover:bg-red-500/10 transition-colors"
                >
                  <Trash2 className="w-3 h-3" />
                </button>
              )}
            </div>
          </div>
        );
      })}
    </>
  );
}

// ─── Drawer: patient detail panel ────────────────────────────────────────────

function Drawer({
  patient,
  canRead,
  onClose,
}: {
  patient: Patient;
  canRead: boolean;
  onClose: () => void;
}) {
  const { ecgs, total, isLoading } = useECGs(
    patient.id as unknown as number,
    { per_page: 50 },
  );
  const age = ageFromDOB(patient.date_of_birth);

  return (
    <>
      {/* Backdrop */}
      <div
        className="absolute inset-0 bg-black/50 z-20"
        onClick={onClose}
      />
      {/* Panel */}
      <div className="absolute right-0 top-0 bottom-0 w-[440px] bg-card border-l border-border z-30 flex flex-col shadow-2xl">
        {/* Header */}
        <div className="p-5 border-b border-border flex items-start gap-4 shrink-0">
          <PatientAvatar patient={patient} size={48} />
          <div className="flex-1 min-w-0">
            <div className="text-base font-semibold text-foreground">
              {patient.last_name}, {patient.first_name}
            </div>
            <div className="text-xs text-muted-foreground mt-1">
              <span className="font-mono">{patient.patient_id}</span>
              {" · "}
              {patient.gender === "F" ? "Femme" : patient.gender === "M" ? "Homme" : patient.gender || "—"}
              {age !== null ? `, ${age} ans` : ""}
            </div>
          </div>
          <button
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground transition-colors"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Info grid */}
        <div className="grid grid-cols-2 gap-2 p-4 border-b border-border shrink-0">
          {[
            {
              label: "Date de naissance",
              value: patient.date_of_birth
                ? new Date(patient.date_of_birth).toLocaleDateString("fr-FR")
                : "—",
            },
            { label: "Total ECGs", value: String(total) },
            {
              label: "Dernier examen",
              value: formatDate(patient.last_activity),
            },
            {
              label: "Statut",
              value: patient.ecg_count ? `${patient.ecg_count} examen(s)` : "—",
            },
          ].map((item) => (
            <div
              key={item.label}
              className="p-2.5 rounded-md bg-muted/20 border border-border/50"
            >
              <div className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                {item.label}
              </div>
              <div className="text-sm font-medium text-foreground mt-0.5">
                {item.value}
              </div>
            </div>
          ))}
        </div>

        {/* Actions */}
        {canRead && (
          <div className="px-4 py-3 border-b border-border shrink-0 flex gap-2">
            <button className="flex-1 inline-flex items-center justify-center gap-1.5 px-3 py-1.5 rounded-md text-sm bg-primary text-primary-foreground hover:opacity-90 transition-opacity">
              <Download className="w-3.5 h-3.5" />
              Tout télécharger
            </button>
            <button className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm border border-border text-foreground hover:bg-muted transition-colors">
              <Send className="w-3.5 h-3.5" />
              Renvoyer HL7
            </button>
          </div>
        )}

        {/* ECG list */}
        <div className="flex-1 overflow-y-auto p-4 space-y-2">
          <div className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground mb-3">
            Examens
          </div>
          {isLoading ? (
            <div className="flex justify-center py-6">
              <Spinner size={16} className="text-muted-foreground" />
            </div>
          ) : ecgs.length === 0 ? (
            <p className="text-sm text-muted-foreground text-center py-6">
              Aucun ECG
            </p>
          ) : (
            ecgs.map((ecg) => (
              <div
                key={ecg.id}
                className="flex items-center gap-3 p-3 rounded-md bg-muted/10 border border-border/50"
              >
                <div className="flex-1 min-w-0">
                  <div className="font-mono text-xs text-foreground">
                    {formatDateTime(ecg)}
                  </div>
                  <div className="flex items-center gap-1.5 mt-1.5">
                    <VendorBadge vendor={ecg.vendor} />
                    <HL7Badge status={ecg.hl7_status} />
                  </div>
                </div>
                {canRead && (
                  <button
                    title="Télécharger"
                    onClick={() =>
                      void downloadECGFormat(ecg.id as unknown as number, "original")
                    }
                    className="w-7 h-7 rounded flex items-center justify-center text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
                  >
                    <Download className="w-3.5 h-3.5" />
                  </button>
                )}
              </div>
            ))
          )}
        </div>
      </div>
    </>
  );
}

// ─── Patient row ──────────────────────────────────────────────────────────────

function PatientRow({
  patient,
  isExpanded,
  selectedECGs,
  loadedEcgIds,
  onToggleExpand,
  onToggleEcg,
  onToggleAllEcgs,
  onOpenDrawer,
  canDelete,
  canRead,
}: {
  patient: Patient;
  isExpanded: boolean;
  selectedECGs: Set<string>;
  loadedEcgIds: string[];
  onToggleExpand: () => void;
  onToggleEcg: (id: string) => void;
  onToggleAllEcgs: (ids: string[], allSelected: boolean) => void;
  onOpenDrawer: () => void;
  canDelete: boolean;
  canRead: boolean;
}) {
  const selectedCount = loadedEcgIds.filter((id) => selectedECGs.has(id)).length;
  const allSelected = loadedEcgIds.length > 0 && selectedCount === loadedEcgIds.length;
  const someSelected = selectedCount > 0 && !allSelected;

  return (
    <>
      {/* Patient header row */}
      <div
        onClick={onToggleExpand}
        className={`grid gap-0 px-4 py-3 items-center text-sm border-b border-border/60 cursor-pointer transition-colors hover:bg-muted/20 ${
          isExpanded ? "bg-muted/20" : ""
        }`}
        style={{ gridTemplateColumns: "40px 32px 1fr 150px 70px 140px 40px" }}
      >
        {/* Patient checkbox */}
        <div onClick={(e) => e.stopPropagation()}>
          <input
            type="checkbox"
            checked={allSelected}
            ref={(el) => {
              if (el) el.indeterminate = someSelected;
            }}
            onChange={() => onToggleAllEcgs(loadedEcgIds, allSelected)}
            className="w-4 h-4 rounded border-border accent-primary cursor-pointer"
          />
        </div>

        {/* Chevron */}
        <div>
          <ChevronRight
            className={`w-3.5 h-3.5 text-muted-foreground transition-transform duration-150 ${
              isExpanded ? "rotate-90" : ""
            }`}
          />
        </div>

        {/* Name + demographics */}
        <div className="flex items-center gap-2.5 min-w-0">
          <PatientAvatar patient={patient} size={28} />
          <div className="min-w-0">
            <div className="font-medium text-foreground truncate">
              {patient.last_name}, {patient.first_name}
            </div>
            <div className="text-[11px] text-muted-foreground">
              {patient.gender || "—"}
              {ageFromDOB(patient.date_of_birth) !== null
                ? ` · ${ageFromDOB(patient.date_of_birth)} ans`
                : ""}
            </div>
          </div>
        </div>

        {/* Patient ID */}
        <div className="font-mono text-xs text-muted-foreground truncate">
          {patient.patient_id}
        </div>

        {/* ECG count */}
        <div className="text-sm font-medium text-foreground">
          {patient.ecg_count ?? 0}
        </div>

        {/* Last activity */}
        <div className="font-mono text-xs text-muted-foreground">
          {formatDate(patient.last_activity)}
        </div>

        {/* Open drawer */}
        <div
          onClick={(e) => {
            e.stopPropagation();
            onOpenDrawer();
          }}
        >
          <button
            title="Ouvrir le dossier"
            className="w-7 h-7 rounded flex items-center justify-center text-primary/60 hover:text-primary hover:bg-primary/10 transition-colors"
          >
            <Eye className="w-3.5 h-3.5" />
          </button>
        </div>
      </div>

      {/* Inline ECG sub-rows */}
      {isExpanded && (
        <EcgSubRows
          patient={patient}
          selectedECGs={selectedECGs}
          onToggle={onToggleEcg}
          canDelete={canDelete}
          canRead={canRead}
          onLoaded={(ids) => {
            /* ids are available from loadedEcgIds via parent state */
            void ids;
          }}
        />
      )}
    </>
  );
}

// ─── Main ─────────────────────────────────────────────────────────────────────

interface Props {
  canDelete: boolean;
  canRead: boolean;
  canWrite: boolean;
  canForceHL7: boolean;
  search: string;
  onSearchChange: (value: string) => void;
  filters: Record<string, string | undefined>;
}

export function PatientGroupedPage({ canDelete, canRead, search, onSearchChange }: Props) {
  const [debouncedSearch, setDebouncedSearch] = useState(search);
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState<number>(() => {
    return Number(localStorage.getItem("ecghub.groupedPerPage") ?? 25);
  });

  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [loadedEcgIds, setLoadedEcgIds] = useState<
    Record<string, string[]>
  >({});
  const [selectedECGs, setSelectedECGs] = useState<Set<string>>(new Set());
  const [drawerPatient, setDrawerPatient] = useState<Patient | null>(null);

  useEffect(() => {
    const id = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(id);
  }, [search]);

  useEffect(() => {
    setPage(1);
  }, [debouncedSearch, perPage]);

  const handlePerPageChange = (value: number) => {
    setPerPage(value);
    localStorage.setItem("ecghub.groupedPerPage", String(value));
  };

  const { patients, total, isLoading } = usePatients({
    ...(debouncedSearch ? { q: debouncedSearch } : {}),
    sort_by: "last_name",
    sort_order: "asc",
    per_page: perPage,
    page,
  });

  const totalPages = Math.ceil(total / perPage);

  const toggleExpand = (patientKey: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      next.has(patientKey) ? next.delete(patientKey) : next.add(patientKey);
      return next;
    });
  };

  const toggleEcg = useCallback((id: string) => {
    setSelectedECGs((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }, []);

  const toggleAllEcgs = useCallback(
    (ids: string[], allSelected: boolean) => {
      setSelectedECGs((prev) => {
        const next = new Set(prev);
        if (allSelected) ids.forEach((id) => next.delete(id));
        else ids.forEach((id) => next.add(id));
        return next;
      });
    },
    [],
  );

  const registerLoadedIds = useCallback(
    (patientKey: string, ids: string[]) => {
      setLoadedEcgIds((prev) =>
        prev[patientKey]?.join() === ids.join() ? prev : { ...prev, [patientKey]: ids },
      );
    },
    [],
  );

  const handleClearSelection = useCallback(() => setSelectedECGs(new Set()), []);

  return (
    <div className="relative flex flex-col h-full overflow-hidden">
      {/* Top bar */}
      <div className="sticky top-0 z-10 bg-card border-b border-border px-6 py-3 shrink-0 flex items-center gap-3">
        <div className="relative flex-1 max-w-md">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
          <input
            autoFocus
            type="text"
            value={search}
            onChange={(e) => onSearchChange(e.target.value)}
            placeholder="Rechercher patient, identifiant…"
            className={`w-full pl-10 ${search ? "pr-9" : "pr-4"} py-2 text-sm bg-muted/50 border-0 rounded-full focus:outline-none focus:ring-2 focus:ring-ring/20 placeholder:text-muted-foreground/60`}
          />
          {search && (
            <button
              onClick={() => onSearchChange("")}
              className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground transition-colors"
            >
              <X className="w-4 h-4" />
            </button>
          )}
        </div>
        <button className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm border border-border text-muted-foreground hover:text-foreground hover:bg-muted transition-colors">
          <RefreshCw className="w-3.5 h-3.5" />
          Actualiser
        </button>
        {isLoading ? (
          <Spinner size={15} className="text-muted-foreground shrink-0" />
        ) : (
          <span className="text-xs text-muted-foreground bg-muted px-3 py-1.5 rounded-full shrink-0">
            {total} patients
          </span>
        )}
      </div>

      {/* Table */}
      <div className="flex-1 overflow-auto px-6 py-4 pb-24 relative">
        <div className="border border-border rounded-lg overflow-hidden relative">
          {/* Table header */}
          <div
            className="grid gap-0 px-4 py-2.5 bg-muted/50 border-b border-border text-[10px] font-semibold uppercase tracking-wider text-muted-foreground"
            style={{
              gridTemplateColumns: "40px 32px 1fr 150px 70px 140px 40px",
            }}
          >
            <div />
            <div />
            <div>Patient</div>
            <div>Identifiant</div>
            <div>ECGs</div>
            <div>Dernière activité</div>
            <div />
          </div>

          {/* Rows */}
          {isLoading ? (
            <div className="flex justify-center py-16">
              <Spinner size={22} className="text-muted-foreground" />
            </div>
          ) : patients.length === 0 ? (
            <div className="text-center py-16 text-sm text-muted-foreground">
              Aucun patient trouvé
            </div>
          ) : (
            patients.map((patient) => {
              const key = patient.id as unknown as string;
              return (
                <PatientRow
                  key={key}
                  patient={patient}
                  isExpanded={expanded.has(key)}
                  selectedECGs={selectedECGs}
                  loadedEcgIds={loadedEcgIds[key] ?? []}
                  onToggleExpand={() => toggleExpand(key)}
                  onToggleEcg={toggleEcg}
                  onToggleAllEcgs={toggleAllEcgs}
                  onOpenDrawer={() => setDrawerPatient(patient)}
                  canDelete={canDelete}
                  canRead={canRead}
                />
              );
            })
          )}

          {/* Drawer (positioned inside the relative container) */}
          {drawerPatient && (
            <Drawer
              patient={drawerPatient}
              canRead={canRead}
              onClose={() => setDrawerPatient(null)}
            />
          )}
        </div>

        {/* Pagination */}
        {!isLoading && total > 0 && (
          <div className="flex items-center justify-between pt-4">
            <div className="flex items-center gap-3">
              <span className="text-xs text-muted-foreground">
                Page {page} sur {totalPages} · {total} patients
              </span>
              <div className="flex items-center gap-1.5">
                <span className="text-xs text-muted-foreground">Afficher</span>
                {[10, 25, 50].map((n) => (
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
              {Array.from({ length: Math.min(totalPages, 7) }, (_, i) => i + 1).map(
                (p) => (
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
                ),
              )}
              <button
                onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                disabled={page === totalPages}
                className="p-1.5 rounded border border-border hover:bg-muted transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
              >
                <ChevronRightIcon className="w-4 h-4" />
              </button>
            </div>
          </div>
        )}
      </div>

      {/* Floating bulk action bar */}
      {selectedECGs.size > 0 && (
        <div className="absolute bottom-4 left-1/2 -translate-x-1/2 z-40 flex items-center gap-2.5 px-4 py-2.5 rounded-xl bg-card/95 backdrop-blur-sm border border-primary/40 shadow-2xl">
          <span className="text-sm font-medium text-foreground">
            {selectedECGs.size} ECG{selectedECGs.size > 1 ? "s" : ""}{" "}
            sélectionné{selectedECGs.size > 1 ? "s" : ""}
          </span>
          <div className="w-px h-4 bg-border" />
          <button className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-md text-xs font-medium border border-border text-foreground hover:bg-muted transition-colors">
            <Download className="w-3.5 h-3.5" />
            Télécharger
          </button>
          <button className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-md text-xs font-medium border border-border text-foreground hover:bg-muted transition-colors">
            <Send className="w-3.5 h-3.5" />
            Renvoyer HL7
          </button>
          {canDelete && (
            <button className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-md text-xs font-medium border border-red-500/30 text-red-400 hover:bg-red-500/10 transition-colors">
              <Trash2 className="w-3.5 h-3.5" />
              Supprimer
            </button>
          )}
          <div className="w-px h-4 bg-border" />
          <button
            onClick={handleClearSelection}
            className="text-muted-foreground hover:text-foreground transition-colors"
          >
            <X className="w-3.5 h-3.5" />
          </button>
        </div>
      )}

      {/* Export footer (for ZIP export) */}
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

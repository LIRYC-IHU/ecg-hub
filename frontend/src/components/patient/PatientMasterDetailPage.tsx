import { useState, useCallback, useEffect, useRef } from "react";
import {
  Search,
  Star,
  Download,
  Send,
  Eye,
  Calendar,
  X,
  ChevronRight,
  Trash2,
} from "lucide-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { usePatients } from "../../hooks/usePatients";
import { useECGs } from "../../hooks/useECGs";
import { Spinner } from "../ui/Spinner";
import { ExportFooter } from "../export/ExportFooter";
import { downloadECGFormat, deleteECG } from "../../lib/api";
import { useNotification } from "../../context/NotificationContext";
import { DownloadFormatPopup } from "../ecg/DownloadFormatPopup";
import type { Patient, ECG } from "../../types";

// ─── Small helpers ───────────────────────────────────────────────────────────

function PatientAvatar({
  patient,
  size = 32,
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
      style={{ width: sz, height: sz, fontSize: size >= 48 ? 16 : 11 }}
    >
      {initials || "?"}
    </div>
  );
}

function HL7Badge({ status }: { status: string }) {
  const cfg: Record<string, { dot: string; label: string }> = {
    success: { dot: "bg-green-400", label: "Envoyé" },
    pending: { dot: "bg-amber-400", label: "En attente" },
    hl7_exhausted: { dot: "bg-red-400", label: "HL7 épuisé" },
  };
  const s = cfg[status] ?? cfg.pending;
  const textCls =
    status === "success"
      ? "text-green-400"
      : status === "pending"
        ? "text-amber-400"
        : "text-red-400";
  return (
    <span
      className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-medium bg-current/10 ${textCls}`}
    >
      <span className={`w-1.5 h-1.5 rounded-full ${s.dot}`} />
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
      className={`font-mono text-[11px] font-medium px-2 py-0.5 rounded ${
        colors[vendor?.toLowerCase()] ?? "text-muted-foreground bg-muted/50"
      }`}
    >
      {vendor || "—"}
    </span>
  );
}

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

// ─── Patient row in the left panel ──────────────────────────────────────────

function PatientRow({
  patient,
  isSelected,
  isPinned,
  onSelect,
  onTogglePin,
}: {
  patient: Patient;
  isSelected: boolean;
  isPinned: boolean;
  onSelect: () => void;
  onTogglePin: (e: React.MouseEvent) => void;
}) {
  return (
    <div
      onClick={onSelect}
      className={`group mx-2 px-3 py-2.5 rounded-md flex items-center gap-2.5 cursor-pointer transition-colors ${
        isSelected
          ? "bg-primary/10 border-l-2 border-primary"
          : "border-l-2 border-transparent hover:bg-muted/40"
      }`}
    >
      <PatientAvatar patient={patient} size={34} />
      <div className="flex-1 min-w-0">
        <div className="text-sm font-medium text-foreground truncate">
          {patient.last_name}, {patient.first_name}
        </div>
        <div className="text-[11px] text-muted-foreground flex gap-1.5 mt-0.5">
          <span className="font-mono">{patient.patient_id}</span>
          <span>·</span>
          <span>
            {patient.ecg_count ?? 0} ECG
            {(patient.ecg_count ?? 0) > 1 ? "s" : ""}
          </span>
        </div>
      </div>
      <button
        onClick={onTogglePin}
        className={`p-1 rounded transition-colors ${
          isPinned
            ? "text-amber-400"
            : "text-muted-foreground/30 group-hover:text-muted-foreground/60"
        }`}
        title={isPinned ? "Désépingler" : "Épingler"}
      >
        <Star
          className="w-3.5 h-3.5"
          fill={isPinned ? "currentColor" : "none"}
        />
      </button>
    </div>
  );
}

// ─── Right panel: empty state ────────────────────────────────────────────────

function NoPatientSelected() {
  return (
    <div className="flex flex-col items-center justify-center h-full gap-3 text-muted-foreground">
      <ChevronRight className="w-10 h-10 opacity-20" />
      <p className="text-sm">Sélectionner un patient dans la liste</p>
    </div>
  );
}

// ─── Right panel: patient detail ─────────────────────────────────────────────

function PatientDetail({
  patient,
  canRead,
  canDelete,
  canForceHL7,
}: {
  patient: Patient;
  canRead: boolean;
  canDelete: boolean;
  canForceHL7: boolean;
}) {
  const { notify } = useNotification();
  const queryClient = useQueryClient();
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);
  const [downloadOpenId, setDownloadOpenId] = useState<string | null>(null);
  const [downloading, setDownloading] = useState(false);
  const deleteMutation = useMutation({
    mutationFn: (id: number) => deleteECG(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["ecgs", patient.id] });
      notify("success", "ECG supprimé");
      setConfirmDeleteId(null);
    },
    onError: () => {
      notify("error", "Erreur lors de la suppression");
      setConfirmDeleteId(null);
    },
  });
  const [selectedECGs, setSelectedECGs] = useState<Set<string>>(new Set());
  const { ecgs, total, isLoading } = useECGs(patient.id as unknown as number, {
    per_page: 50,
  });

  const pendingCount = ecgs.filter((e) => e.hl7_status === "pending").length;
  const sentCount = ecgs.filter((e) => e.hl7_status === "success").length;
  const age = ageFromDOB(patient.date_of_birth);

  const toggle = (id: string) => {
    setSelectedECGs((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  };

  const checkAllRef = useRef<HTMLInputElement>(null);
  const allChecked =
    ecgs.length > 0 &&
    ecgs.every((e) => selectedECGs.has(e.id as unknown as string));
  const someChecked = selectedECGs.size > 0 && !allChecked;
  useEffect(() => {
    if (checkAllRef.current) checkAllRef.current.indeterminate = someChecked;
  }, [someChecked]);

  const handleClearSelection = useCallback(
    () => setSelectedECGs(new Set()),
    [],
  );

  // reset selection when patient changes
  useEffect(() => {
    setSelectedECGs(new Set());
  }, [patient.id]);

  return (
    <div className="flex flex-col h-full min-h-0 overflow-hidden relative">
      {/* Patient header */}
      <div className="px-8 py-6 border-b border-border flex items-start gap-5 shrink-0">
        <PatientAvatar patient={patient} size={56} />
        <div className="flex-1 min-w-0">
          <h2 className="text-xl font-semibold tracking-tight text-foreground">
            {patient.last_name}, {patient.first_name}
          </h2>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 mt-1.5 text-sm text-muted-foreground">
            <span className="font-mono text-foreground text-xs">
              {patient.patient_id}
            </span>
            <span className="text-border">·</span>
            <span>
              {patient.gender === "F"
                ? "Femme"
                : patient.gender === "M"
                  ? "Homme"
                  : patient.gender || "—"}
              {age !== null ? `, ${age} ans` : ""}
            </span>
            {patient.date_of_birth && (
              <>
                <span className="text-border">·</span>
                <span>
                  né le{" "}
                  {new Date(patient.date_of_birth).toLocaleDateString("fr-FR")}
                </span>
              </>
            )}
          </div>
        </div>
        <div className="flex gap-2 shrink-0">
          <button className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm border border-border text-foreground hover:bg-muted transition-colors">
            <Calendar className="w-3.5 h-3.5" />
            Historique
          </button>
          {canRead && (
            <button className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm bg-primary text-primary-foreground hover:opacity-90 transition-opacity">
              <Download className="w-3.5 h-3.5" />
              Tout télécharger
            </button>
          )}
        </div>
      </div>

      {/* Stats strip */}
      <div className="grid grid-cols-4 gap-3 px-8 py-4 border-b border-border shrink-0">
        {[
          { label: "Total ECGs", value: total, cls: "text-primary" },
          {
            label: "Dernier examen",
            value: formatDate(patient.last_activity),
            cls: "text-foreground font-mono text-sm",
          },
          { label: "Envoyés HL7", value: sentCount, cls: "text-green-400" },
          { label: "En attente", value: pendingCount, cls: "text-amber-400" },
        ].map((s) => (
          <div
            key={s.label}
            className="p-3 rounded-lg border border-border bg-muted/20"
          >
            <div className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
              {s.label}
            </div>
            <div className={`text-lg font-semibold mt-1 ${s.cls}`}>
              {s.value}
            </div>
          </div>
        ))}
      </div>

      {/* ECG list header */}
      <div className="px-8 py-3 border-b border-border shrink-0 flex items-center justify-between">
        <div className="flex items-center gap-2.5">
          <input
            ref={checkAllRef}
            type="checkbox"
            checked={allChecked}
            onChange={() => {
              if (allChecked || someChecked) setSelectedECGs(new Set());
              else
                setSelectedECGs(
                  new Set(ecgs.map((e) => e.id as unknown as string)),
                );
            }}
            className="w-4 h-4 rounded border-border accent-primary cursor-pointer"
          />
          <span className="text-sm font-semibold text-foreground">
            Examens ECG
          </span>
        </div>
        {selectedECGs.size > 0 && (
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <span>
              {selectedECGs.size} sélectionné
              {selectedECGs.size > 1 ? "s" : ""}
            </span>
            <button className="inline-flex items-center gap-1 px-2 py-1 rounded border border-border bg-card hover:bg-muted transition-colors">
              <Send className="w-3 h-3" />
              Renvoyer HL7
            </button>
            <button
              onClick={handleClearSelection}
              className="text-muted-foreground hover:text-foreground"
            >
              <X className="w-3.5 h-3.5" />
            </button>
          </div>
        )}
      </div>

      {/* ECG list body */}
      <div className="flex-1 overflow-auto px-8 py-4 pb-20 space-y-2">
        {isLoading ? (
          <div className="flex justify-center py-10">
            <Spinner size={20} className="text-muted-foreground" />
          </div>
        ) : ecgs.length === 0 ? (
          <p className="text-sm text-muted-foreground text-center py-10">
            Aucun ECG pour ce patient
          </p>
        ) : (
          ecgs.map((ecg) => {
            const id = ecg.id as unknown as string;
            const isSelected = selectedECGs.has(id);
            return (
              <div
                key={ecg.id}
                onClick={() => toggle(id)}
                className={`flex items-center gap-3 p-3.5 rounded-lg border cursor-pointer transition-colors ${
                  isSelected
                    ? "border-primary/40 bg-primary/5"
                    : "border-border bg-muted/10 hover:bg-muted/30"
                }`}
              >
                <div onClick={(e) => e.stopPropagation()}>
                  <input
                    type="checkbox"
                    checked={isSelected}
                    onChange={() => toggle(id)}
                    className="w-4 h-4 rounded border-border accent-primary cursor-pointer"
                  />
                </div>

                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="font-mono text-xs text-foreground">
                      {formatDateTime(ecg)}
                    </span>
                    <VendorBadge vendor={ecg.vendor} />
                    <HL7Badge status={ecg.hl7_status} />
                  </div>
                  <div
                    className="text-[11px] text-muted-foreground font-mono mt-1 truncate"
                    title={ecg.original_filename}
                  >
                    {ecg.original_filename}
                  </div>
                </div>

                <div
                  className="flex items-center gap-1.5 shrink-0"
                  onClick={(e) => e.stopPropagation()}
                >
                  <button className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md text-xs font-medium bg-primary/10 text-primary border border-primary/20 hover:bg-primary/20 transition-colors">
                    <Eye className="w-3 h-3" />
                    Ouvrir
                  </button>
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
                  {canDelete && (
                    confirmDeleteId === id ? (
                      <>
                        <button
                          onClick={() => deleteMutation.mutate(ecg.id as unknown as number)}
                          disabled={deleteMutation.isPending}
                          className="text-[10px] font-medium text-destructive hover:underline disabled:opacity-50 flex items-center gap-0.5 px-1"
                        >
                          {deleteMutation.isPending && <Spinner size={10} className="text-destructive" />}
                          Confirmer
                        </button>
                        <button
                          onClick={() => setConfirmDeleteId(null)}
                          className="text-[10px] text-muted-foreground hover:underline px-1"
                        >
                          Annuler
                        </button>
                      </>
                    ) : (
                      <button
                        onClick={() => setConfirmDeleteId(id)}
                        className="w-7 h-7 rounded flex items-center justify-center text-destructive/60 hover:text-destructive hover:bg-destructive/10 transition-colors"
                        title="Supprimer"
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    )
                  )}
                </div>
              </div>
            );
          })
        )}
      </div>

      {/* Export footer */}
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

// ─── Main component ──────────────────────────────────────────────────────────

interface Props {
  canDelete: boolean;
  canRead: boolean;
  canWrite: boolean;
  canForceHL7: boolean;
  search: string;
  onSearchChange: (value: string) => void;
  filters: Record<string, string | undefined>;
}

export function PatientMasterDetailPage({
  canDelete,
  canRead,
  canForceHL7,
  search,
  onSearchChange,
}: Props) {
  const [debouncedSearch, setDebouncedSearch] = useState(search);
  const [selectedPatient, setSelectedPatient] = useState<Patient | null>(null);
  const [pinned, setPinned] = useState<Set<string>>(() => {
    try {
      const saved = localStorage.getItem("ecghub.pinnedPatients");
      return new Set(JSON.parse(saved ?? "[]") as string[]);
    } catch {
      return new Set<string>();
    }
  });

  useEffect(() => {
    const id = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(id);
  }, [search]);

  const { patients, isLoading } = usePatients({
    ...(debouncedSearch ? { q: debouncedSearch } : {}),
    sort_by: "last_name",
    sort_order: "asc",
    per_page: 200,
  });

  // Pinned first, then alphabetical
  const sortedPatients = [...patients].sort((a, b) => {
    const aP = pinned.has(a.patient_id) ? 0 : 1;
    const bP = pinned.has(b.patient_id) ? 0 : 1;
    return aP - bP;
  });

  const firstNonPinned = sortedPatients.findIndex(
    (p) => !pinned.has(p.patient_id),
  );
  const hasPinnedSection = pinned.size > 0 && firstNonPinned > 0;

  const togglePin = useCallback((patientId: string, e: React.MouseEvent) => {
    e.stopPropagation();
    setPinned((prev) => {
      const next = new Set(prev);
      next.has(patientId) ? next.delete(patientId) : next.add(patientId);
      localStorage.setItem(
        "ecghub.pinnedPatients",
        JSON.stringify(Array.from(next)),
      );
      return next;
    });
  }, []);

  return (
    <div className="flex h-full min-h-0 overflow-hidden">
      {/* ── Left panel: patient list ── */}
      <div className="w-80 shrink-0 border-r border-border flex flex-col min-h-0 bg-card/40">
        {/* Search */}
        <div className="p-3 border-b border-border">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
            <input
              autoFocus
              type="text"
              value={search}
              onChange={(e) => onSearchChange(e.target.value)}
              placeholder="Rechercher un patient…"
              className={`w-full pl-9 ${search ? "pr-9" : "pr-3"} py-2 text-sm bg-muted/50 border-0 rounded-full focus:outline-none focus:ring-2 focus:ring-ring/20 placeholder:text-muted-foreground/60`}
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
        </div>

        {/* Patient list */}
        <div className="flex-1 min-h-0 overflow-y-auto py-2">
          {isLoading ? (
            <div className="flex justify-center py-8">
              <Spinner size={18} className="text-muted-foreground" />
            </div>
          ) : patients.length === 0 ? (
            <p className="text-sm text-muted-foreground text-center py-8">
              Aucun patient trouvé
            </p>
          ) : (
            <>
              {hasPinnedSection && (
                <div className="px-4 py-1.5 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground flex items-center gap-1.5">
                  <Star className="w-2.5 h-2.5" />
                  Épinglés
                </div>
              )}
              {sortedPatients.map((patient, i) => {
                const showDivider = hasPinnedSection && i === firstNonPinned;
                return (
                  <div key={patient.id as unknown as string}>
                    {showDivider && (
                      <div className="mx-4 my-2 border-t border-border" />
                    )}
                    <PatientRow
                      patient={patient}
                      isSelected={selectedPatient?.id === patient.id}
                      isPinned={pinned.has(patient.patient_id)}
                      onSelect={() => setSelectedPatient(patient)}
                      onTogglePin={(e) => togglePin(patient.patient_id, e)}
                    />
                  </div>
                );
              })}
            </>
          )}
        </div>
      </div>

      {/* ── Right panel: detail ── */}
      <div className="flex-1 min-h-0 overflow-hidden">
        {selectedPatient ? (
          <PatientDetail
            key={selectedPatient.id as unknown as string}
            patient={selectedPatient}
            canRead={canRead}
            canDelete={canDelete}
            canForceHL7={canForceHL7}
          />
        ) : (
          <NoPatientSelected />
        )}
      </div>
    </div>
  );
}

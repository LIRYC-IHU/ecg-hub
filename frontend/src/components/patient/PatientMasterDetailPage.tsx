import { useState, useCallback, useEffect, useRef, useMemo } from "react";
import {
  Search,
  Star,
  Download,
  Eye,
  X,
  Trash2,
  ChevronLeft,
  ChevronRight,
  ArrowDownAZ,
  ArrowUpAZ,
  Clock,
  Tag,
  Copy,
  RefreshCw,
  Send,
  Activity,
  CalendarClock,
  CheckCircle2,
  Hourglass,
} from "lucide-react";
import { gsap } from "gsap";
import { useGSAP } from "@gsap/react";
import { useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { usePatients } from "../../hooks/usePatients";
import { useECGs } from "../../hooks/useECGs";
import { Spinner } from "../ui/Spinner";
import {
  downloadECGFormats,
  createExportJob,
  deleteECG,
  markEcgViewed,
  fetchPins,
  pinPatient,
  unpinPatient,
  fetchPatientTags,
  fetchTags,
  untagPatient,
  fetchECGTags,
  untagECG,
  forceHL7,
  sendECGResult,
  fetchECGORUStatus,
  fetchActiveHL7Mappings,
  fetchHL7Settings,
  fetchHL7History,
  type TagDTO,
  type ECGFilters,
} from "../../lib/api";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../hooks/useAuth";
import { useNotification } from "../../context/NotificationContext";
import { DownloadFormatPopup } from "../ecg/DownloadFormatPopup";
import { TagBadge } from "../ui/TagBadge";
import { TagManager } from "../tags/TagManager";
import { ECGViewerModal } from "../ecg/ECGViewerModal";
import type { Patient, ECG } from "../../types";

gsap.registerPlugin(useGSAP);

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
      className={`rounded-full flex items-center justify-center font-semibold shrink-0 ${
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
  const { t } = useTranslation();
  const dotCls: Record<string, string> = {
    success: "bg-green-400",
    pending: "bg-amber-400",
    hl7_exhausted: "bg-red-400",
  };
  const label = t(`ecg.status.${status}`) || status;
  const s = { dot: dotCls[status] ?? dotCls.pending, label };
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
    muse: "text-teal-400 bg-teal-500/10",
    mindray: "text-cyan-400 bg-cyan-500/10",
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

function PatientHL7Status({
  pending,
  sent,
  total,
}: {
  pending: number;
  sent: number;
  total: number;
}) {
  if (total === 0) return null;
  const exhausted = total - pending - sent;
  if (sent === total) {
    return (
      <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium bg-green-500/10 text-green-400">
        <span className="w-1.5 h-1.5 rounded-full bg-green-400" />
        HL7
      </span>
    );
  }
  if (exhausted > 0) {
    return (
      <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium bg-red-500/10 text-red-400">
        <span className="w-1.5 h-1.5 rounded-full bg-red-400" />
        HL7
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium bg-amber-500/10 text-amber-400">
      <span className="w-1.5 h-1.5 rounded-full bg-amber-400" />
      HL7
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

// ─── Full-page patient grid (no patient selected) ───────────────────────────

function PatientGrid({
  patients,
  pinned,
  checked,
  selectedEcgCounts,
  onSelect,
  onTogglePin,
  onToggleCheck,
  isLoading,
}: {
  patients: Patient[];
  pinned: Set<string>;
  checked: Set<number>;
  selectedEcgCounts: Map<number, number>;
  onSelect: (p: Patient) => void;
  onTogglePin: (patientId: string, e: React.MouseEvent) => void;
  onToggleCheck: (patientId: number) => void;
  isLoading: boolean;
}) {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const gridRef = useRef<HTMLDivElement>(null);

  if (isLoading) {
    return (
      <div className="flex justify-center py-16">
        <Spinner size={24} className="text-muted-foreground" />
      </div>
    );
  }

  if (patients.length === 0) {
    return (
      <p className="text-sm text-muted-foreground text-center py-16">
        {t("search.noResults")}
      </p>
    );
  }

  return (
    <div ref={gridRef} className="flex flex-col gap-1.5 p-4">
      {patients.map((patient) => {
        const isPinned = pinned.has(patient.patient_id);
        const isChecked = checked.has(patient.id);
        const age = ageFromDOB(patient.date_of_birth);
        return (
          <div
            key={patient.id}
            onClick={() => onSelect(patient)}
            className={`pg-row group flex items-center gap-4 px-4 py-3 rounded-xl border cursor-pointer transition-all duration-200 ease-out hover:-translate-y-0.5 ${
              isChecked
                ? "border-primary/40 bg-primary/5 shadow-sm shadow-primary/5"
                : "border-border bg-card hover:bg-muted/40 hover:border-primary/30 hover:shadow-md hover:shadow-black/5"
            }`}
          >
            <div
              onClick={(e) => {
                e.stopPropagation();
                onToggleCheck(patient.id);
              }}
            >
              <input
                type="checkbox"
                checked={isChecked}
                readOnly
                className="w-4 h-4 rounded border-border accent-primary cursor-pointer pointer-events-none"
              />
            </div>
            <PatientAvatar patient={patient} size={36} />
            <div className="min-w-0 w-48">
              <div className="text-sm font-medium text-foreground truncate">
                {patient.last_name}, {patient.first_name}
              </div>
              <div className="text-[11px] text-muted-foreground font-mono mt-0.5 inline-flex items-center gap-1">
                {patient.patient_id}
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    void navigator.clipboard.writeText(patient.patient_id);
                    notify("success", t("patient.idCopied"));
                  }}
                  className="p-0.5 rounded text-muted-foreground/40 hover:text-foreground transition-colors"
                >
                  <Copy className="w-2.5 h-2.5" />
                </button>
              </div>
            </div>
            <div className="flex items-center gap-4 flex-1 text-[11px] text-muted-foreground">
              <span>
                {patient.ecg_count ?? 0} ECG
                {(patient.ecg_count ?? 0) > 1 ? "s" : ""}
              </span>
              {age !== null && (
                <span>
                  {age} {t("common.years", "ans")}
                </span>
              )}
              {patient.last_activity && (
                <span>{formatDate(patient.last_activity)}</span>
              )}
              <PatientTagDots patientId={patient.patient_id} />
            </div>
            {(selectedEcgCounts.get(patient.id) ?? 0) > 0 && (
              <span className="text-[10px] font-medium text-primary tabular-nums">
                {selectedEcgCounts.get(patient.id)}/{patient.ecg_count ?? 0}
              </span>
            )}
            <button
              onClick={(e) => onTogglePin(patient.patient_id, e)}
              className={`p-1.5 rounded transition-colors hover:cursor-pointer ${
                isPinned
                  ? "text-amber-400"
                  : "text-muted-foreground/20 group-hover:text-muted-foreground/50"
              }`}
            >
              <Star
                className="w-3.5 h-3.5"
                fill={isPinned ? "currentColor" : "none"}
              />
            </button>
          </div>
        );
      })}
    </div>
  );
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
  const { t } = useTranslation();
  return (
    <div
      onClick={onSelect}
      className={`pl-row group mx-2 px-3 py-2.5 rounded-md flex items-center gap-2.5 cursor-pointer transition-all duration-200 ease-out ${
        isSelected
          ? "bg-primary/10 border-l-2 border-primary"
          : "border-l-2 border-transparent hover:bg-muted/40 hover:translate-x-0.5"
      }`}
    >
      <PatientAvatar patient={patient} size={34} />
      <div className="flex-1 min-w-0">
        <div className="text-sm font-medium text-foreground truncate flex items-center gap-1.5">
          <span className="truncate">
            {patient.last_name}, {patient.first_name}
          </span>
          {(patient.unviewed_count ?? 0) > 0 && (
            <span
              title={t("patient.newEcgs", { count: patient.unviewed_count })}
              className="shrink-0 inline-flex items-center justify-center min-w-4 h-4 px-1 rounded-full bg-primary text-primary-foreground text-[10px] font-semibold"
            >
              {patient.unviewed_count}
            </span>
          )}
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
        title={isPinned ? t("patient.unpin") : t("patient.pin")}
      >
        <Star
          className="w-3.5 h-3.5"
          fill={isPinned ? "currentColor" : "none"}
        />
      </button>
    </div>
  );
}

// ─── Patient tags row ────────────────────────────────────────────────────────

function PatientTagsRow({ patientId }: { patientId: string }) {
  const queryClient = useQueryClient();
  const { hasPermission } = useAuth();
  const canCreate = hasPermission("tag.create");
  const canDeleteTag = hasPermission("tag.delete");
  const canApply = hasPermission("tag.apply");

  const { data: tags = [] } = useQuery({
    queryKey: ["patient-tags", patientId],
    queryFn: () => fetchPatientTags(patientId),
    staleTime: 30_000,
    enabled: !!patientId,
  });

  const handleRemove = async (tagId: string) => {
    queryClient.setQueryData<TagDTO[]>(["patient-tags", patientId], (old) =>
      (old ?? []).filter((t) => t.id !== tagId),
    );
    await untagPatient(patientId, tagId);
    void queryClient.invalidateQueries({
      queryKey: ["patient-tags", patientId],
    });
  };

  return (
    <div className="flex flex-wrap items-center gap-1.5 mt-2">
      {tags.map((tag) => (
        <TagBadge
          key={tag.id}
          name={tag.name}
          color={tag.color}
          onRemove={canApply ? () => handleRemove(tag.id) : undefined}
        />
      ))}
      {(canCreate || canApply) && (
        <TagManager
          patientId={patientId}
          canCreate={canCreate}
          canDelete={canDeleteTag}
          canApply={canApply}
        />
      )}
    </div>
  );
}

// ─── HL7 History timeline ────────────────────────────────────────────────────

function PatientHL7History({ patientId }: { patientId: string }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const { data: attempts = [] } = useQuery({
    queryKey: ["hl7-history", patientId],
    queryFn: () => fetchHL7History(patientId),
    staleTime: 30_000,
    enabled: open,
  });

  if (!open) {
    return (
      <button
        onClick={() => setOpen(true)}
        className="inline-flex items-center gap-1.5 mt-2 px-2.5 py-1 rounded-md text-[11px] font-medium border border-border text-muted-foreground hover:text-foreground hover:bg-muted/50 transition-colors"
      >
        <Clock className="w-3 h-3" />
        {t("patient.hl7History")}
      </button>
    );
  }

  return (
    <div className="mt-2">
      <button
        onClick={() => setOpen(false)}
        className="text-[11px] font-medium text-muted-foreground hover:text-foreground transition-colors mb-2"
      >
        {t("patient.hl7HistoryHide")}
      </button>
      {attempts.length === 0 ? (
        <p className="text-[11px] text-muted-foreground/60 italic">
          {t("patient.hl7NoHistory")}
        </p>
      ) : (
        <div className="space-y-1 max-h-40 overflow-y-auto">
          {attempts.map((a) => (
            <div key={a.id} className="flex items-center gap-2 text-[11px]">
              <span
                className={`w-1.5 h-1.5 rounded-full shrink-0 ${
                  a.status === "success"
                    ? "bg-green-400"
                    : a.status === "rejected"
                      ? "bg-amber-400"
                      : "bg-red-400"
                }`}
              />
              <span className="text-muted-foreground font-mono w-32 shrink-0">
                {new Date(a.created_at).toLocaleString("fr-FR", {
                  day: "2-digit",
                  month: "2-digit",
                  hour: "2-digit",
                  minute: "2-digit",
                })}
              </span>
              <span className="text-muted-foreground font-mono w-12 shrink-0 text-right">
                {a.response_ms}ms
              </span>
              {a.status === "success" && (
                <span className="text-green-400 font-medium">OK</span>
              )}
              {a.status === "rejected" && (
                <span className="text-amber-400 truncate" title={a.msa_message}>
                  MSA {a.msa_code}: {a.msa_message || "rejected"}
                </span>
              )}
              {(a.status === "failed" || a.status === "exhausted") && (
                <span className="text-red-400 truncate" title={a.error}>
                  {a.error || "failed"}
                </span>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// ─── ECG tags (inline dots on ECG rows) ─────────────────────────────────────

function ECGTagDots({ ecgId, canApply }: { ecgId: string; canApply: boolean }) {
  const queryClient = useQueryClient();
  const { data: tags = [] } = useQuery({
    queryKey: ["ecg-tags", ecgId],
    queryFn: () => fetchECGTags(ecgId),
    staleTime: 30_000,
  });

  if (tags.length === 0 && !canApply) return null;

  return (
    <div className="flex items-center gap-1 mt-1">
      {tags.map((tag) => (
        <TagBadge
          key={tag.id}
          name={tag.name}
          color={tag.color}
          onRemove={
            canApply
              ? () => {
                  void untagECG(ecgId, tag.id);
                  void queryClient.invalidateQueries({
                    queryKey: ["ecg-tags", ecgId],
                  });
                }
              : undefined
          }
        />
      ))}
      {canApply && (
        <TagManager
          ecgId={ecgId}
          canCreate={false}
          canDelete={false}
          canApply={canApply}
        />
      )}
    </div>
  );
}

// ─── Patient tags dots (compact, for grid rows) ─────────────────────────────

function PatientTagDots({ patientId }: { patientId: string }) {
  const { data: tags = [] } = useQuery({
    queryKey: ["patient-tags", patientId],
    queryFn: () => fetchPatientTags(patientId),
    staleTime: 30_000,
    enabled: !!patientId,
  });

  if (tags.length === 0) return null;

  return (
    <div className="flex items-center gap-1">
      {tags.slice(0, 4).map((tag) => (
        <span
          key={tag.id}
          className="w-2 h-2 rounded-full"
          style={{ backgroundColor: tag.color }}
          title={tag.name}
        />
      ))}
      {tags.length > 4 && (
        <span className="text-[9px] text-muted-foreground">
          +{tags.length - 4}
        </span>
      )}
    </div>
  );
}

// ─── Right panel: patient detail ─────────────────────────────────────────────

// OruSendButton renders the per-ECG "send result to DPI" action with a status dot
// reflecting the latest outbound ORU attempt (green=sent, red=rejected, amber=failed).
function OruSendButton({ ecgId }: { ecgId: number }) {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const queryClient = useQueryClient();

  const { data: status } = useQuery({
    queryKey: ["oru-status", ecgId],
    queryFn: () => fetchECGORUStatus(ecgId),
    staleTime: 30_000,
  });

  const mutation = useMutation({
    mutationFn: () => sendECGResult(ecgId),
    onSuccess: () => {
      notify("success", t("ecg.oruSent"));
      void queryClient.invalidateQueries({ queryKey: ["oru-status", ecgId] });
    },
    onError: (err: { message?: string }) =>
      notify("error", err?.message ?? t("ecg.oruError")),
  });

  const dot =
    status?.status === "success"
      ? "bg-success"
      : status?.status === "rejected"
        ? "bg-destructive"
        : status?.status === "failed"
          ? "bg-warning"
          : "";

  return (
    <button
      title={t("ecg.sendResult")}
      onClick={() => mutation.mutate()}
      disabled={mutation.isPending}
      className="relative w-7 h-7 rounded flex items-center justify-center text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors disabled:opacity-50"
    >
      {mutation.isPending ? (
        <Spinner size={13} />
      ) : (
        <Send className="w-3.5 h-3.5" />
      )}
      {dot && !mutation.isPending && (
        <span
          className={`absolute top-0.5 right-0.5 w-1.5 h-1.5 rounded-full ${dot}`}
        />
      )}
    </button>
  );
}

function PatientDetail({
  patient,
  canRead,
  canDelete,
  canForceHL7,
  canSendResult,
  filters,
  selectedECGs,
  onToggleECG,
  onSetAllECGs,
  onClearECGs,
  onPatientCheckedChange,
}: {
  patient: Patient;
  canRead: boolean;
  canDelete: boolean;
  canForceHL7: boolean;
  canSendResult: boolean;
  filters: ECGFilters;
  selectedECGs: Set<number>;
  onToggleECG: (ecgId: number) => void;
  onSetAllECGs: (ecgIds: number[]) => void;
  onClearECGs: () => void;
  onPatientCheckedChange: (
    patientId: number,
    allSelected: boolean,
    selectedCount: number,
  ) => void;
}) {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const queryClient = useQueryClient();
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);
  const [viewerEcgId, setViewerEcgId] = useState<string | null>(null);
  const [downloadOpenId, setDownloadOpenId] = useState<string | null>(null);
  const [downloading, setDownloading] = useState(false);
  const detailRef = useRef<HTMLDivElement>(null);
  const deleteMutation = useMutation({
    mutationFn: (id: number) => deleteECG(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["ecgs", patient.id] });
      void queryClient.invalidateQueries({ queryKey: ["patients"] });
      notify("success", t("ecg.deleted"));
      setConfirmDeleteId(null);
    },
    onError: () => {
      notify("error", t("ecg.deleteError"));
      setConfirmDeleteId(null);
    },
  });
  // Mark an ECG as viewed (first open) so its "new" indicator and the patient's
  // unviewed badge clear. Fire-and-forget; refresh the ECG list and patient list.
  const markViewedMutation = useMutation({
    mutationFn: (id: string) => markEcgViewed(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["ecgs", patient.id] });
      void queryClient.invalidateQueries({ queryKey: ["patients"] });
    },
  });

  const openViewer = (ecg: ECG) => {
    setViewerEcgId(String(ecg.id));
    if (!ecg.viewed) markViewedMutation.mutate(String(ecg.id));
  };

  const { ecgs, total, isLoading } = useECGs(patient.id as unknown as number, {
    ...filters,
    per_page: 50,
  });

  const pendingCount = ecgs.filter((e) => e.hl7_status === "pending").length;
  const sentCount = ecgs.filter((e) => e.hl7_status === "success").length;
  const age = ageFromDOB(patient.date_of_birth);

  const ecgIdsOnPage = ecgs.map((e) => e.id);
  const selectedOnPage = ecgIdsOnPage.filter((id) => selectedECGs.has(id));
  const allChecked = ecgs.length > 0 && selectedOnPage.length === ecgs.length;
  const someChecked = selectedOnPage.length > 0 && !allChecked;
  const checkAllRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (checkAllRef.current) checkAllRef.current.indeterminate = someChecked;
  }, [someChecked]);

  useEffect(() => {
    if (ecgs.length > 0) {
      onPatientCheckedChange(patient.id, allChecked, selectedOnPage.length);
    }
  }, [allChecked, selectedOnPage.length, ecgs.length, patient.id]);

  // Entrance choreography: header band + bento rise in, exam rows stagger up.
  // Keyed to patient.id so switching patients re-plays it (not on poll refetch).
  useGSAP(
    () => {
      gsap.from(".detail-band", {
        y: 10,
        duration: 0.4,
        ease: "",
        stagger: 0.08,
      });
      gsap.from(".ecg-row", {
        y: 12,
        duration: 0.45,
        ease: "",
        stagger: 0.03,
        delay: 0.1,
      });
    },
    { scope: detailRef, dependencies: [patient.id] },
  );

  return (
    <div
      ref={detailRef}
      className="flex flex-col h-full min-h-0 overflow-hidden relative"
    >
      {/* Patient header */}
      <div className="detail-band px-8 py-7 border-b border-border flex items-start gap-5 shrink-0">
        <PatientAvatar patient={patient} size={56} />
        <div className="flex-1 min-w-0">
          <h2 className="font-display text-2xl font-semibold tracking-tight text-foreground">
            {patient.last_name}, {patient.first_name}
          </h2>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 mt-1.5 text-sm text-muted-foreground">
            <span className="inline-flex items-center gap-1 font-mono text-foreground text-xs">
              {patient.patient_id}
              <button
                onClick={() => {
                  void navigator.clipboard.writeText(patient.patient_id);
                  notify("success", t("patient.idCopied"));
                }}
                className="p-0.5 rounded text-muted-foreground hover:text-foreground transition-colors"
                title={t("patient.copyId")}
              >
                <Copy className="w-3 h-3" />
              </button>
            </span>
            <PatientHL7Status
              pending={pendingCount}
              sent={sentCount}
              total={ecgs.length}
            />
            <span className="text-border">·</span>
            <span>
              {patient.gender === "F"
                ? t("patient.genderF")
                : patient.gender === "M"
                  ? t("patient.genderM")
                  : patient.gender || "—"}
              {age !== null ? `, ${age} ${t("common.years", "ans")}` : ""}
            </span>
            {patient.date_of_birth && (
              <>
                <span className="text-border">·</span>
                <span>
                  {t("patient.bornOn")}{" "}
                  {new Date(patient.date_of_birth).toLocaleDateString("fr-FR")}
                </span>
              </>
            )}
            {patient.nda && (
              <>
                <span className="text-border">·</span>
                <span className="inline-flex items-center gap-1 font-mono text-xs">
                  {t("patient.ndaLabel")} {patient.nda}
                  <button
                    onClick={() => {
                      void navigator.clipboard.writeText(patient.nda!);
                      notify("success", t("patient.ndaCopied"));
                    }}
                    className="p-0.5 rounded text-muted-foreground hover:text-foreground transition-colors"
                  >
                    <Copy className="w-3 h-3" />
                  </button>
                </span>
              </>
            )}
          </div>
          <PatientTagsRow patientId={patient.patient_id} />
          <PatientHL7History patientId={patient.patient_id} />
        </div>
        <div className="flex gap-2 shrink-0">
          {canForceHL7 && (
            <button
              onClick={async () => {
                if (ecgs.length === 0) {
                  notify("info", t("patient.hl7NoPending"));
                  return;
                }
                try {
                  for (const ecg of ecgs) {
                    await forceHL7(ecg.id);
                  }
                  notify("success", t("patient.hl7Forced"));
                  void queryClient.invalidateQueries({
                    queryKey: ["ecgs", patient.id],
                  });
                  void queryClient.invalidateQueries({
                    queryKey: ["patients"],
                  });
                } catch {
                  notify("error", t("patient.hl7ForceError"));
                }
              }}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm border border-border text-foreground hover:bg-muted transition-colors"
            >
              <RefreshCw className="w-3.5 h-3.5" />
              {t("patient.forceHL7")}
            </button>
          )}
        </div>
      </div>

      {/* Metric bento */}
      <div className="detail-band grid grid-cols-4 grid-flow-dense gap-3 px-8 py-5 border-b border-border shrink-0">
        {[
          {
            label: t("patient.totalEcgs"),
            value: total,
            cls: "text-primary",
            Icon: Activity,
            accent: "bg-primary/10 text-primary ring-primary/20",
          },
          {
            label: t("patient.lastExam"),
            value: formatDate(patient.last_activity),
            cls: "text-foreground font-mono text-base",
            Icon: CalendarClock,
            accent: "bg-muted text-muted-foreground ring-border",
          },
          {
            label: t("patient.hl7Sent"),
            value: sentCount,
            cls: "text-green-500",
            Icon: CheckCircle2,
            accent: "bg-green-500/10 text-green-500 ring-green-500/20",
          },
          {
            label: t("patient.hl7Pending"),
            value: pendingCount,
            cls: "text-amber-500",
            Icon: Hourglass,
            accent: "bg-amber-500/10 text-amber-500 ring-amber-500/20",
          },
        ].map((s) => (
          <div
            key={s.label}
            className="group rounded-xl border border-border bg-card p-4 transition-all duration-200 ease-out hover:-translate-y-0.5 hover:border-primary/25 hover:shadow-md hover:shadow-black/5"
          >
            <div className="flex items-center justify-between">
              <span className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                {s.label}
              </span>
              <span
                className={`flex h-7 w-7 items-center justify-center rounded-lg ring-1 ${s.accent}`}
              >
                <s.Icon className="h-3.5 w-3.5" />
              </span>
            </div>
            <div
              className={`font-display mt-2 text-2xl font-semibold leading-none tabular-nums ${s.cls}`}
            >
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
              if (allChecked || someChecked) onClearECGs();
              else onSetAllECGs(ecgIdsOnPage);
            }}
            className="w-4 h-4 rounded border-border accent-primary cursor-pointer"
          />
          <span className="text-sm font-semibold text-foreground">
            {t("patient.ecgExams")}
          </span>
          {selectedOnPage.length > 0 && (
            <span className="text-[11px] text-muted-foreground">
              ({t("common.selected", { count: selectedOnPage.length })})
            </span>
          )}
        </div>
      </div>

      {/* ECG list body */}
      <div className="flex-1 overflow-auto px-8 py-4 pb-20 space-y-2">
        {isLoading ? (
          <div className="flex justify-center py-10">
            <Spinner size={20} className="text-muted-foreground" />
          </div>
        ) : ecgs.length === 0 ? (
          <p className="text-sm text-muted-foreground text-center py-10">
            {t("patient.noEcg")}
          </p>
        ) : (
          ecgs.map((ecg) => {
            const isSelected = selectedECGs.has(ecg.id);
            return (
              <div
                key={ecg.id}
                onClick={() => {
                  onToggleECG(ecg.id);
                  if (!ecg.viewed) markViewedMutation.mutate(String(ecg.id));
                }}
                className={`ecg-row flex items-center gap-3 p-3.5 rounded-xl border cursor-pointer transition-all duration-200 ease-out hover:-translate-y-0.5 hover:shadow-md hover:shadow-black/5 ${
                  isSelected
                    ? "border-primary/40 bg-primary/5"
                    : ecg.viewed
                      ? "border-border bg-muted/10 hover:bg-muted/30"
                      : "border-primary/30 bg-primary/4 hover:bg-primary/10"
                }`}
              >
                <div onClick={(e) => e.stopPropagation()}>
                  <input
                    type="checkbox"
                    checked={isSelected}
                    onChange={() => onToggleECG(ecg.id)}
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
                    {!ecg.viewed && (
                      <span className="inline-flex items-center gap-1 text-[10px] font-semibold px-1.5 py-0.5 rounded-full bg-primary/15 text-primary">
                        <span className="w-1.5 h-1.5 rounded-full bg-primary" />
                        {t("ecg.new")}
                      </span>
                    )}
                  </div>
                  <div
                    className="text-[11px] text-muted-foreground font-mono mt-1 truncate"
                    title={ecg.original_filename}
                  >
                    {ecg.original_filename}
                  </div>
                  <ECGTagDots ecgId={String(ecg.id)} canApply={canForceHL7} />
                </div>

                <div
                  className="flex items-center gap-1.5 shrink-0"
                  onClick={(e) => e.stopPropagation()}
                >
                  <button
                    onClick={() => openViewer(ecg)}
                    className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md text-xs font-medium bg-primary/10 text-primary border border-primary/20 hover:bg-primary/20 transition-colors"
                  >
                    <Eye className="w-3 h-3" />
                    {t("ecg.open")}
                  </button>
                  {canRead && (
                    <>
                      <button
                        title={t("ecg.download")}
                        onClick={() => setDownloadOpenId(String(ecg.id))}
                        disabled={
                          downloading && downloadOpenId === String(ecg.id)
                        }
                        className="w-7 h-7 rounded flex items-center justify-center text-muted-foreground hover:text-foreground hover:bg-muted transition-colors disabled:opacity-50"
                      >
                        {downloading && downloadOpenId === String(ecg.id) ? (
                          <Spinner
                            size={13}
                            className="text-muted-foreground"
                          />
                        ) : (
                          <Download className="w-3.5 h-3.5" />
                        )}
                      </button>
                      <DownloadFormatPopup
                        open={downloadOpenId === String(ecg.id)}
                        onClose={() => setDownloadOpenId(null)}
                        ecgIds={[ecg.id]}
                        busy={downloading}
                        onConfirm={async (formats, mode) => {
                          setDownloadOpenId(null);
                          setDownloading(true);
                          try {
                            // One request: a single format streams the file,
                            // multiple formats come back as one ZIP.
                            await downloadECGFormats(ecg.id, formats, mode);
                          } catch {
                            notify("error", t("ecg.downloadError"));
                          } finally {
                            setDownloading(false);
                          }
                        }}
                      />
                    </>
                  )}
                  {canSendResult && <OruSendButton ecgId={ecg.id} />}
                  {canDelete &&
                    (confirmDeleteId === String(ecg.id) ? (
                      <>
                        <button
                          onClick={() => deleteMutation.mutate(ecg.id)}
                          disabled={deleteMutation.isPending}
                          className="text-[10px] font-medium text-destructive hover:underline disabled:opacity-50 flex items-center gap-0.5 px-1"
                        >
                          {deleteMutation.isPending && (
                            <Spinner size={10} className="text-destructive" />
                          )}
                          {t("common.confirm")}
                        </button>
                        <button
                          onClick={() => setConfirmDeleteId(null)}
                          className="text-[10px] text-muted-foreground hover:underline px-1"
                        >
                          {t("common.cancel")}
                        </button>
                      </>
                    ) : (
                      <button
                        onClick={() => setConfirmDeleteId(String(ecg.id))}
                        className="w-7 h-7 rounded flex items-center justify-center text-destructive/60 hover:text-destructive hover:bg-destructive/10 transition-colors"
                        title={t("common.delete")}
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    ))}
                </div>
              </div>
            );
          })
        )}
      </div>

      {/* ECG Viewer modal */}
      {viewerEcgId && (
        <ECGViewerModal
          ecgId={viewerEcgId}
          filename={
            ecgs.find((e) => String(e.id) === viewerEcgId)?.original_filename
          }
          onClose={() => setViewerEcgId(null)}
        />
      )}
    </div>
  );
}

// ─── Shared bulk ECG action footer ──────────────────────────────────────────

function BulkECGFooter({
  count,
  ecgIds,
  canRead,
  canDelete,
  canSendResult,
  onClear,
}: {
  count: number;
  ecgIds: Set<number>;
  canRead: boolean;
  canDelete: boolean;
  canSendResult: boolean;
  onClear: () => void;
}) {
  const { t } = useTranslation();
  const { notify, notifyProgress } = useNotification();
  const queryClient = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);

  return (
    <div className="absolute bottom-0 left-0 right-0 z-20 border-t border-border bg-card/95 backdrop-blur-sm px-6 py-3 flex items-center justify-between">
      <div className="flex items-center gap-2 text-sm text-foreground">
        <span className="font-medium">
          {count} ECG{count > 1 ? "s" : ""} {t("common.selected", { count })}
        </span>
      </div>
      <div className="flex items-center gap-2">
        <button
          onClick={onClear}
          className="text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          {t("common.deselect")}
        </button>
        {canRead && (
          <>
            <button
              disabled={busy}
              onClick={() => setExportOpen(true)}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-primary text-primary-foreground hover:opacity-90 transition-opacity disabled:opacity-50"
            >
              <Download className="w-3.5 h-3.5" />
              {busy ? t("export.exporting") : t("ecg.download")}
            </button>
            <DownloadFormatPopup
              open={exportOpen}
              onClose={() => setExportOpen(false)}
              ecgIds={Array.from(ecgIds)}
              busy={busy}
              onConfirm={async (formats, mode) => {
                setExportOpen(false);
                setBusy(true);
                try {
                  const job = await createExportJob({
                    ecg_ids: Array.from(ecgIds),
                    formats,
                    anonymize: mode === "anonymize",
                    inject: mode === "inject",
                  });
                  // Open the progress toast (WebSocket) which surfaces the ZIP
                  // download link when the job completes.
                  notifyProgress(job.id, t("export.overlayTitle"));
                  // Defer clear so the notification renders before this footer unmounts.
                  setTimeout(onClear, 100);
                } catch {
                  notify("error", t("common.exportError"));
                } finally {
                  setBusy(false);
                }
              }}
            />
          </>
        )}
        {canSendResult && (
          <button
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              let ok = 0;
              let failed = 0;
              try {
                for (const id of ecgIds) {
                  try {
                    await sendECGResult(id);
                    ok++;
                  } catch {
                    failed++;
                  }
                }
                if (failed === 0) {
                  notify("success", t("ecg.oruSentBulk", { count: ok }));
                } else {
                  notify("warn", t("ecg.oruSentBulkPartial", { ok, failed }));
                }
                void queryClient.invalidateQueries({
                  queryKey: ["oru-status"],
                });
                onClear();
              } finally {
                setBusy(false);
              }
            }}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-primary/10 text-primary border border-primary/20 hover:bg-primary/20 transition-colors disabled:opacity-50"
          >
            <Send className="w-3.5 h-3.5" />
            {t("ecg.sendResultBulk")}
          </button>
        )}
        {canDelete && (
          <button
            disabled={busy}
            onClick={async () => {
              if (!window.confirm(t("ecg.deleteConfirmBulk", { count })))
                return;
              setBusy(true);
              try {
                for (const id of ecgIds) {
                  await deleteECG(id);
                }
                notify("success", t("ecg.deletedBulk", { count }));
                onClear();
                void queryClient.invalidateQueries({ queryKey: ["patients"] });
                void queryClient.invalidateQueries({ queryKey: ["ecgs"] });
              } catch {
                notify("error", t("ecg.deleteError"));
              } finally {
                setBusy(false);
              }
            }}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-destructive/10 text-destructive border border-destructive/20 hover:bg-destructive/20 transition-colors disabled:opacity-50"
          >
            <Trash2 className="w-3.5 h-3.5" />
            {t("common.delete")}
          </button>
        )}
      </div>
    </div>
  );
}

// ─── Main component ──────────────────────────────────────────────────────────

interface Props {
  canDelete: boolean;
  canRead: boolean;
  canWrite: boolean;
  canForceHL7: boolean;
  canSendResult: boolean;
  search: string;
  onSearchChange: (value: string) => void;
  filters: ECGFilters;
}

export function PatientMasterDetailPage({
  canDelete,
  canRead,
  canForceHL7,
  canSendResult,
  search,
  onSearchChange,
  filters,
}: Props) {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const queryClient = useQueryClient();
  const [debouncedSearch, setDebouncedSearch] = useState(search);
  const [selectedPatient, setSelectedPatient] = useState<Patient | null>(null);
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState<number>(() => {
    const saved = localStorage.getItem("ecghub.patientsPerPage");
    return saved ? Number(saved) : 25;
  });
  const [checkedPatients, setCheckedPatients] = useState<Set<number>>(
    new Set(),
  );
  const [selectedECGs, setSelectedECGs] = useState<Set<number>>(new Set());
  const [pinnedOnly, setPinnedOnly] = useState(false);
  const [selectedTags, setSelectedTags] = useState<string[]>([]);
  const [tagDropdownOpen, setTagDropdownOpen] = useState(false);
  const [sortBy, setSortBy] = useState<"last_name" | "last_activity">(
    "last_name",
  );
  const [sortOrder, setSortOrder] = useState<"asc" | "desc">("asc");
  const [ecgCountByPatient, setEcgCountByPatient] = useState<
    Map<number, number>
  >(new Map());
  const { data: pinnedData } = useQuery({
    queryKey: ["pins"],
    queryFn: fetchPins,
    staleTime: 60_000,
  });
  const pinned = new Set(pinnedData ?? []);

  const { data: allTags = [] } = useQuery({
    queryKey: ["tags"],
    queryFn: fetchTags,
    staleTime: 60_000,
  });

  // Check HL7 mapping on mount — notify once if not configured (only for users with hl7.config permission)
  const { hasPermission } = useAuth();
  const canConfigHL7 = hasPermission("hl7.config");
  const hl7Notified = useRef(false);
  useEffect(() => {
    if (hl7Notified.current || !canConfigHL7) return;
    hl7Notified.current = true;
    // Only warn about a missing mapping when the HL7 integration is globally
    // enabled — if the site turned HL7 off, there's nothing to configure.
    Promise.all([fetchHL7Settings(), fetchActiveHL7Mappings()])
      .then(([settings, { active }]) => {
        if (settings.hl7_enabled && !active) {
          notify("warn", t("patient.hl7NotConfigured"), {
            label: t("patient.hl7Configure"),
            href: "/hl7",
          });
        }
      })
      .catch(() => {});
  }, [canConfigHL7]); // eslint-disable-line react-hooks/exhaustive-deps

  const tagDropdownRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!tagDropdownOpen) return;
    function handleClick(e: MouseEvent) {
      if (
        tagDropdownRef.current &&
        !tagDropdownRef.current.contains(e.target as Node)
      ) {
        setTagDropdownOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, [tagDropdownOpen]);

  useEffect(() => {
    const id = setTimeout(() => {
      setDebouncedSearch(search);
      setPage(1);
    }, 300);
    return () => clearTimeout(id);
  }, [search]);

  const { patients, total, isLoading } = usePatients({
    ...(debouncedSearch ? { q: debouncedSearch } : {}),
    ...(selectedTags.length > 0 ? { tags: selectedTags } : {}),
    ...filters,
    sort_by: sortBy,
    sort_order: sortOrder,
    page,
    per_page: perPage,
  });

  // Reset to page 1 whenever the ECG filters change so we never land on a now-empty page.
  useEffect(() => {
    setPage(1);
  }, [filters]);

  const totalPages = Math.max(1, Math.ceil(total / perPage));

  // Deep-link selection: when arriving via ?patient=<patient_id> (e.g. from a
  // realtime ingestion notification), select that patient once it is in the loaded
  // list, then strip the params so later refetches don't re-trigger selection.
  const [searchParams, setSearchParams] = useSearchParams();
  useEffect(() => {
    const pid = searchParams.get("patient");
    if (!pid) return;
    const match = patients.find((p) => p.patient_id === pid);
    if (match) {
      setSelectedPatient(match);
      const next = new URLSearchParams(searchParams);
      next.delete("patient");
      next.delete("ecg");
      setSearchParams(next, { replace: true });
    }
  }, [searchParams, patients, setSearchParams]);

  // Sync selectedPatient with refetched data
  useEffect(() => {
    if (selectedPatient) {
      const updated = patients.find((p) => p.id === selectedPatient.id);
      if (
        updated &&
        (updated.last_name !== selectedPatient.last_name ||
          updated.first_name !== selectedPatient.first_name ||
          updated.date_of_birth !== selectedPatient.date_of_birth ||
          updated.gender !== selectedPatient.gender)
      ) {
        setSelectedPatient(updated);
      }
    }
  }, [patients]); // eslint-disable-line react-hooks/exhaustive-deps

  const pinnedSnapshot = useRef<Set<string>>(pinned);
  useEffect(() => {
    pinnedSnapshot.current = pinned;
  }, [patients]);

  const sortedPatients = useMemo(() => {
    const snap = pinnedSnapshot.current;
    return [...patients].sort((a, b) => {
      const aP = snap.has(a.patient_id) ? 0 : 1;
      const bP = snap.has(b.patient_id) ? 0 : 1;
      return aP - bP;
    });
  }, [patients]);

  const displayedPatients = pinnedOnly
    ? sortedPatients.filter((p) => pinned.has(p.patient_id))
    : sortedPatients;

  const firstNonPinned = displayedPatients.findIndex(
    (p) => !pinned.has(p.patient_id),
  );
  const hasPinnedSection = !pinnedOnly && pinned.size > 0 && firstNonPinned > 0;

  const togglePin = useCallback(
    (patientId: string, e: React.MouseEvent) => {
      e.stopPropagation();
      const wasPinned = (
        queryClient.getQueryData<string[]>(["pins"]) ?? []
      ).includes(patientId);
      queryClient.setQueryData<string[]>(["pins"], (old) => {
        if (!old) return wasPinned ? [] : [patientId];
        return wasPinned
          ? old.filter((id) => id !== patientId)
          : [...old, patientId];
      });
      if (wasPinned) {
        void unpinPatient(patientId);
      } else {
        void pinPatient(patientId);
      }
    },
    [queryClient],
  );

  const handleSelectPatient = async (patient: Patient) => {
    if (selectedPatient?.id === patient.id) {
      setSelectedPatient(null);
    } else {
      setSelectedPatient(patient);
      if (checkedPatients.has(patient.id)) {
        try {
          const res = await fetch(
            `${import.meta.env.VITE_API_URL || ""}/api/v1/patients/${patient.id}/ecgs?per_page=500`,
          );
          if (res.ok) {
            const json = await res.json();
            const ids = (json.data ?? []).map((e: { id: number }) => e.id);
            setSelectedECGs((prev) => {
              const next = new Set(prev);
              for (const id of ids) next.add(id);
              return next;
            });
          }
        } catch {
          /* ignore */
        }
      }
    }
  };

  const fetchAndAddECGs = async (patientId: number) => {
    try {
      const res = await fetch(
        `${import.meta.env.VITE_API_URL || ""}/api/v1/patients/${patientId}/ecgs?per_page=500`,
      );
      if (res.ok) {
        const json = await res.json();
        const ids: number[] = (json.data ?? []).map(
          (e: { id: number }) => e.id,
        );
        setSelectedECGs((prev) => {
          const next = new Set(prev);
          for (const id of ids) next.add(id);
          return next;
        });
        setEcgCountByPatient((prev) =>
          new Map(prev).set(patientId, ids.length),
        );
      }
    } catch {
      /* ignore */
    }
  };

  const fetchAndRemoveECGs = async (patientId: number) => {
    try {
      const res = await fetch(
        `${import.meta.env.VITE_API_URL || ""}/api/v1/patients/${patientId}/ecgs?per_page=500`,
      );
      if (res.ok) {
        const json = await res.json();
        const ids = new Set<number>(
          (json.data ?? []).map((e: { id: number }) => e.id),
        );
        setSelectedECGs((prev) => {
          const next = new Set(prev);
          for (const id of ids) next.delete(id);
          return next;
        });
        setEcgCountByPatient((prev) => {
          const next = new Map(prev);
          next.delete(patientId);
          return next;
        });
      }
    } catch {
      /* ignore */
    }
  };

  const handleToggleCheck = (patientId: number) => {
    const wasChecked = checkedPatients.has(patientId);
    setCheckedPatients((prev) => {
      const next = new Set(prev);
      wasChecked ? next.delete(patientId) : next.add(patientId);
      return next;
    });
    if (wasChecked) {
      void fetchAndRemoveECGs(patientId);
    } else {
      void fetchAndAddECGs(patientId);
    }
  };

  const handleCheckAll = () => {
    if (checkedPatients.size === patients.length) {
      setCheckedPatients(new Set());
      setSelectedECGs(new Set());
    } else {
      setCheckedPatients(new Set(patients.map((p) => p.id)));
      for (const p of patients) {
        if (!checkedPatients.has(p.id)) {
          void fetchAndAddECGs(p.id);
        }
      }
    }
  };

  const handleToggleECG = useCallback((ecgId: number) => {
    setSelectedECGs((prev) => {
      const next = new Set(prev);
      next.has(ecgId) ? next.delete(ecgId) : next.add(ecgId);
      return next;
    });
  }, []);

  const handleSetAllECGs = useCallback((ecgIds: number[]) => {
    setSelectedECGs((prev) => {
      const next = new Set(prev);
      for (const id of ecgIds) next.add(id);
      return next;
    });
  }, []);

  const handleClearAllECGs = useCallback(() => {
    setSelectedECGs(new Set());
  }, []);

  const handlePatientCheckedChange = useCallback(
    (patientId: number, allSelected: boolean, selectedCount: number) => {
      setCheckedPatients((prev) => {
        const next = new Set(prev);
        if (allSelected) {
          next.add(patientId);
        } else {
          next.delete(patientId);
        }
        return next;
      });
      setEcgCountByPatient((prev) => {
        const next = new Map(prev);
        if (selectedCount > 0) {
          next.set(patientId, selectedCount);
        } else {
          next.delete(patientId);
        }
        return next;
      });
    },
    [],
  );

  // ─── Full-page mode (no patient selected) ─────────────────────────────────
  if (!selectedPatient) {
    const allCheckedOnPage =
      patients.length > 0 && patients.every((p) => checkedPatients.has(p.id));
    const someCheckedOnPage = checkedPatients.size > 0 && !allCheckedOnPage;

    return (
      <div className="flex flex-col h-full min-h-0 overflow-hidden animate-in fade-in duration-200">
        {/* Search bar + per page selector */}
        <div className="p-4 border-b border-border shrink-0 flex items-center gap-4">
          <input
            type="checkbox"
            checked={allCheckedOnPage}
            ref={(el) => {
              if (el) el.indeterminate = someCheckedOnPage;
            }}
            onChange={handleCheckAll}
            className="w-4 h-4 rounded border-border accent-primary cursor-pointer shrink-0"
            title={t("patient.selectAll")}
          />
          <div className="relative flex-1 max-w-md">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
            <input
              autoFocus
              type="text"
              value={search}
              onChange={(e) => onSearchChange(e.target.value)}
              placeholder={t("search.placeholder")}
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
          <button
            onClick={() => setPinnedOnly((v) => !v)}
            className={`inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-md text-xs font-medium border transition-colors shrink-0 ${
              pinnedOnly
                ? "bg-amber-400/10 text-amber-500 border-amber-400/30"
                : "text-muted-foreground border-border hover:text-foreground hover:bg-muted/50"
            }`}
          >
            <Star
              className="w-3 h-3"
              fill={pinnedOnly ? "currentColor" : "none"}
            />
            {t("patient.favorites")}
          </button>
          {/* Tag multi-select filter */}
          <div ref={tagDropdownRef} className="relative shrink-0">
            <button
              onClick={() => setTagDropdownOpen((v) => !v)}
              className={`inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-md text-xs font-medium border transition-colors ${
                selectedTags.length > 0
                  ? "bg-primary/10 text-primary border-primary/30"
                  : "text-muted-foreground border-border hover:text-foreground hover:bg-muted/50"
              }`}
            >
              <Tag className="w-3 h-3" />
              {t("patient.filterTags")}
              {selectedTags.length > 0 && (
                <span className="ml-1 px-1.5 py-0.5 rounded-full bg-primary text-primary-foreground text-[10px] leading-none">
                  {selectedTags.length}
                </span>
              )}
            </button>
            {tagDropdownOpen && (
              <div className="absolute top-full left-0 mt-1 z-50 w-56 bg-card border border-border rounded-lg shadow-lg overflow-hidden">
                <div className="max-h-60 overflow-y-auto p-1">
                  {allTags.length === 0 ? (
                    <p className="text-xs text-muted-foreground text-center py-3">
                      {t("patient.noTags")}
                    </p>
                  ) : (
                    allTags.map((tag) => {
                      const isActive = selectedTags.includes(tag.id);
                      return (
                        <label
                          key={tag.id}
                          className="flex items-center gap-2.5 px-3 py-2 rounded-md hover:bg-muted/50 cursor-pointer transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked={isActive}
                            onChange={() => {
                              setSelectedTags((prev) =>
                                isActive
                                  ? prev.filter((id) => id !== tag.id)
                                  : [...prev, tag.id],
                              );
                              setPage(1);
                            }}
                            className="w-3.5 h-3.5 rounded border-border accent-primary cursor-pointer"
                          />
                          <span
                            className="w-2.5 h-2.5 rounded-full shrink-0"
                            style={{ backgroundColor: tag.color }}
                          />
                          <span className="text-xs text-foreground truncate">
                            {tag.name}
                          </span>
                        </label>
                      );
                    })
                  )}
                </div>
                {selectedTags.length > 0 && (
                  <div className="border-t border-border p-2">
                    <button
                      onClick={() => {
                        setSelectedTags([]);
                        setPage(1);
                      }}
                      className="w-full text-xs text-muted-foreground hover:text-foreground text-center py-1 transition-colors"
                    >
                      {t("common.clearAll")}
                    </button>
                  </div>
                )}
              </div>
            )}
          </div>
          <button
            onClick={() => {
              if (sortBy === "last_name") {
                setSortOrder((o) => (o === "asc" ? "desc" : "asc"));
              } else {
                setSortBy("last_name");
                setSortOrder("asc");
              }
              setPage(1);
            }}
            className={`inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md text-xs font-medium border transition-colors shrink-0 ${
              sortBy === "last_name"
                ? "bg-primary/10 text-primary border-primary/30"
                : "text-muted-foreground border-border hover:text-foreground hover:bg-muted/50"
            }`}
          >
            {sortBy === "last_name" && sortOrder === "desc" ? (
              <ArrowUpAZ className="w-3.5 h-3.5" />
            ) : (
              <ArrowDownAZ className="w-3.5 h-3.5" />
            )}
            {t("patient.name")}
          </button>
          <button
            onClick={() => {
              if (sortBy === "last_activity") {
                setSortOrder((o) => (o === "desc" ? "asc" : "desc"));
              } else {
                setSortBy("last_activity");
                setSortOrder("desc");
              }
              setPage(1);
            }}
            className={`inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md text-xs font-medium border transition-colors shrink-0 ${
              sortBy === "last_activity"
                ? "bg-primary/10 text-primary border-primary/30"
                : "text-muted-foreground border-border hover:text-foreground hover:bg-muted/50"
            }`}
          >
            <Clock className="w-3 h-3" />
            {sortBy === "last_activity" && sortOrder === "asc"
              ? t("patient.sortOldest")
              : t("patient.sortRecent")}
          </button>
          <div className="flex items-center gap-2 text-xs text-muted-foreground shrink-0">
            <span>
              {total} patient{total > 1 ? "s" : ""}
            </span>
            <select
              value={perPage}
              onChange={(e) => {
                const v = Number(e.target.value);
                setPerPage(v);
                setPage(1);
                localStorage.setItem("ecghub.patientsPerPage", String(v));
              }}
              className="text-xs border border-border rounded-md px-2 py-1 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
            >
              {[10, 25, 50, 100].map((n) => (
                <option key={n} value={n}>
                  {n} / page
                </option>
              ))}
            </select>
          </div>
        </div>

        {/* Patient list */}
        <div className="flex-1 min-h-0 overflow-y-auto">
          <PatientGrid
            patients={displayedPatients}
            pinned={pinned}
            checked={checkedPatients}
            selectedEcgCounts={ecgCountByPatient}
            onSelect={handleSelectPatient}
            onTogglePin={togglePin}
            onToggleCheck={handleToggleCheck}
            isLoading={isLoading}
          />
        </div>

        {/* Pagination */}
        {totalPages > 1 && (
          <div className="shrink-0 border-t border-border px-4 py-2 flex items-center justify-between">
            <span className="text-xs text-muted-foreground">
              Page {page} / {totalPages}
            </span>
            <div className="flex items-center gap-1">
              <button
                onClick={() => setPage((p) => Math.max(1, p - 1))}
                disabled={page <= 1}
                className="inline-flex items-center justify-center w-7 h-7 rounded border border-border text-muted-foreground hover:text-foreground hover:bg-muted disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
              >
                <ChevronLeft className="w-3.5 h-3.5" />
              </button>
              <button
                onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                disabled={page >= totalPages}
                className="inline-flex items-center justify-center w-7 h-7 rounded border border-border text-muted-foreground hover:text-foreground hover:bg-muted disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
              >
                <ChevronRight className="w-3.5 h-3.5" />
              </button>
            </div>
          </div>
        )}

        {/* Bulk ECG footer (shared) */}
        {selectedECGs.size > 0 && (
          <BulkECGFooter
            count={selectedECGs.size}
            ecgIds={selectedECGs}
            canRead={canRead}
            canDelete={canDelete}
            canSendResult={canSendResult}
            onClear={handleClearAllECGs}
          />
        )}
      </div>
    );
  }

  // ─── Master-detail mode (patient selected) ────────────────────────────────
  return (
    <div className="flex h-full min-h-0 overflow-hidden animate-in slide-in-from-left-2 duration-200">
      {/* ── Left panel: patient list ── */}
      <div className="w-80 shrink-0 border-r border-border flex flex-col min-h-0 bg-card/40">
        {/* Search */}
        <div className="p-3 border-b border-border">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
            <input
              type="text"
              value={search}
              onChange={(e) => onSearchChange(e.target.value)}
              placeholder={t("search.placeholder")}
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
              {t("search.noResults")}
            </p>
          ) : (
            <>
              {hasPinnedSection && (
                <div className="px-4 py-1.5 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground flex items-center gap-1.5">
                  <Star className="w-2.5 h-2.5" />
                  {t("patient.pinned")}
                </div>
              )}
              {displayedPatients.map((patient, i) => {
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
                      onSelect={() => handleSelectPatient(patient)}
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
      <div className="flex-1 min-h-0 overflow-hidden animate-in fade-in duration-150">
        <PatientDetail
          patient={selectedPatient}
          canRead={canRead}
          canDelete={canDelete}
          canForceHL7={canForceHL7}
          canSendResult={canSendResult}
          filters={filters}
          selectedECGs={selectedECGs}
          onToggleECG={handleToggleECG}
          onSetAllECGs={handleSetAllECGs}
          onClearECGs={handleClearAllECGs}
          onPatientCheckedChange={handlePatientCheckedChange}
        />
      </div>

      {/* ── Common footer ── */}
      {selectedECGs.size > 0 && (
        <BulkECGFooter
          count={selectedECGs.size}
          ecgIds={selectedECGs}
          canRead={canRead}
          canDelete={canDelete}
          canSendResult={canSendResult}
          onClear={handleClearAllECGs}
        />
      )}
    </div>
  );
}

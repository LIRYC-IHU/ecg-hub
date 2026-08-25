import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import {
  Upload,
  FileUp,
  CheckCircle2,
  AlertTriangle,
  XCircle,
  Copy,
  Loader2,
  Send,
  UserPlus,
  RotateCcw,
} from "lucide-react";
import { uploadECGs, type UploadFileResult } from "../../lib/api";
import { eventClient } from "../../lib/grpc";
import { useNotification } from "../../context/NotificationContext";

// RowStatus mirrors the lifecycle of one uploaded file: queued on the server,
// then a terminal outcome pushed over the events WebSocket (or rejected upfront).
type RowStatus =
  | "queued"
  | "ingested"
  | "unidentified"
  | "quarantined"
  | "duplicate"
  | "rejected"
  // Handed to the pipeline, outcome unknown: the live stream was unavailable,
  // so no terminal event can reach this page.
  | "accepted";

interface UploadRow {
  key: string;
  filename: string;
  size: number;
  status: RowStatus;
  patientId?: string;
  quarantineId?: string;
  reason?: string;
}

interface IngestionEvent {
  type: "ecg.ingested" | "ecg.unidentified" | "ecg.quarantined" | "ecg.duplicate";
  patient_id?: string;
  quarantine_id?: string;
  filename?: string;
  reason?: string;
}

function statusFromEvent(type: IngestionEvent["type"]): RowStatus {
  switch (type) {
    case "ecg.ingested":
      return "ingested";
    case "ecg.unidentified":
      return "unidentified";
    case "ecg.quarantined":
      return "quarantined";
    case "ecg.duplicate":
      return "duplicate";
  }
}

function StatusBadge({ status }: { status: RowStatus }) {
  const { t } = useTranslation();
  const map: Record<RowStatus, { cls: string; icon: React.ReactNode }> = {
    accepted: {
      cls: "text-muted-foreground bg-muted/50",
      icon: <Send className="w-3.5 h-3.5" />,
    },
    queued: {
      cls: "text-muted-foreground bg-muted/50",
      icon: <Loader2 className="w-3.5 h-3.5 animate-spin" />,
    },
    ingested: {
      cls: "text-green-400 bg-green-500/10",
      icon: <CheckCircle2 className="w-3.5 h-3.5" />,
    },
    unidentified: {
      cls: "text-amber-400 bg-amber-500/10",
      icon: <AlertTriangle className="w-3.5 h-3.5" />,
    },
    quarantined: {
      cls: "text-red-400 bg-red-500/10",
      icon: <XCircle className="w-3.5 h-3.5" />,
    },
    duplicate: {
      cls: "text-blue-400 bg-blue-500/10",
      icon: <Copy className="w-3.5 h-3.5" />,
    },
    rejected: {
      cls: "text-red-400 bg-red-500/10",
      icon: <XCircle className="w-3.5 h-3.5" />,
    },
  };
  const s = map[status];
  return (
    <span
      className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-medium ${s.cls}`}
    >
      {s.icon}
      {t(`uploads.status.${status}`)}
    </span>
  );
}

function humanSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function UploadPage() {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const [files, setFiles] = useState<File[]>([]);
  const [phase, setPhase] = useState<"select" | "processing">("select");
  const [rows, setRows] = useState<UploadRow[]>([]);
  const [uploading, setUploading] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const abortRef = useRef<AbortController | null>(null);

  const pending = rows.filter((r) => r.status === "queued").length;

  // Abort the live stream on unmount.
  useEffect(() => () => abortRef.current?.abort(), []);

  // openEventStream subscribes to EventService.Subscribe and resolves once the
  // stream is live — the server sends an immediate keepalive as its first frame,
  // which guarantees its hub subscription is active. We open BEFORE enqueuing
  // files, otherwise a fast file could finish (and broadcast its terminal event)
  // before we subscribe, leaving its row stuck on "processing". Later events
  // update the matching queued row by filename.
  function openEventStream(): Promise<void> {
    const abort = new AbortController();
    abortRef.current = abort;
    return new Promise((resolve, reject) => {
      let ready = false;
      void (async () => {
        try {
          for await (const ev of eventClient.subscribe(
            {},
            { signal: abort.signal },
          )) {
            if (!ready) {
              ready = true;
              resolve(); // first frame (keepalive) → stream + hub subscription live
            }
            if (!ev.filename) continue; // skip keepalive frames
            // Refresh patient/quarantine lists so other open views stay in sync.
            void queryClient.invalidateQueries({ queryKey: ["patients"] });
            void queryClient.invalidateQueries({
              queryKey: ["admin", "quarantine"],
            });
            const type = ev.type as IngestionEvent["type"];
            setRows((prev) => {
              const idx = prev.findIndex(
                (r) => r.status === "queued" && r.filename === ev.filename,
              );
              if (idx === -1) return prev;
              const next = [...prev];
              next[idx] = {
                ...next[idx],
                status: statusFromEvent(type),
                patientId: ev.patientId || undefined,
                quarantineId: ev.quarantineId || undefined,
                reason: ev.reason || undefined,
              };
              return next;
            });
          }
        } catch {
          if (!ready) reject(new Error("stream error"));
        }
      })();
      // Fallback so a stalled stream never blocks the upload.
      setTimeout(() => {
        if (!ready) reject(new Error("stream timeout"));
      }, 5000);
    });
  }

  function onSelectFiles(e: React.ChangeEvent<HTMLInputElement>) {
    setFiles(Array.from(e.target.files ?? []));
  }

  async function handleUpload() {
    if (files.length === 0) return;
    setUploading(true);

    // Pre-populate rows from the local selection so no event can be missed once
    // the socket is open (the server-side outcome arrives via WebSocket).
    const initial: UploadRow[] = files.map((f, i) => ({
      key: `${f.name}-${i}`,
      filename: f.name,
      size: f.size,
      status: "queued",
    }));

    // Open the live stream first (best-effort — proceed even if it fails).
    let liveUpdates = true;
    try {
      await openEventStream();
    } catch {
      // No stream: nothing will ever resolve these rows, so say so instead of
      // spinning forever. Ingestion itself is unaffected — the files are
      // processed server-side either way.
      liveUpdates = false;
    }

    setRows(initial);
    setPhase("processing");

    try {
      const res = await uploadECGs(files);
      // Reconcile server-side rejections (these never reach the pipeline, so they
      // emit no event). Response order matches the submitted file order.
      setRows((prev) =>
        prev.map((r, i) => {
          const sr: UploadFileResult | undefined = res.files[i];
          if (sr && sr.status === "rejected") {
            return { ...r, status: "rejected", reason: sr.error };
          }
          // Without the stream the row has no terminal state to wait for.
          return liveUpdates ? r : { ...r, status: "accepted" };
        }),
      );
      if (!liveUpdates) {
        notify("warn", t("uploads.noLiveUpdates"));
        void queryClient.invalidateQueries({ queryKey: ["patients"] });
      }
    } catch {
      notify("error", t("uploads.uploadError"));
      abortRef.current?.abort();
      setPhase("select");
    } finally {
      setUploading(false);
    }
  }

  function reset() {
    abortRef.current?.abort();
    abortRef.current = null;
    setFiles([]);
    setRows([]);
    setPhase("select");
    if (fileInputRef.current) fileInputRef.current.value = "";
  }

  const done = rows.length > 0 && pending === 0;

  return (
    <div className="p-6 max-w-3xl mx-auto w-full">
      <div className="mb-6">
        <h1 className="font-display text-2xl font-semibold tracking-tight text-foreground flex items-center gap-2">
          <Upload className="w-6 h-6 text-primary" />
          {t("uploads.title")}
        </h1>
        <p className="text-sm text-muted-foreground mt-1">
          {t("uploads.subtitle")}
        </p>
      </div>

      {phase === "select" && (
        <div className="border border-border rounded-xl bg-card p-6 flex flex-col gap-4">
          <label
            htmlFor="ecg-upload-input"
            className="flex flex-col items-center justify-center gap-3 border-2 border-dashed border-border rounded-lg py-12 cursor-pointer hover:border-primary/40 hover:bg-muted/30 transition-colors"
          >
            <FileUp className="w-8 h-8 text-muted-foreground" />
            <span className="text-sm text-muted-foreground">
              {files.length > 0
                ? t("uploads.filesSelected", { count: files.length })
                : t("uploads.selectFiles")}
            </span>
            <input
              id="ecg-upload-input"
              ref={fileInputRef}
              type="file"
              multiple
              onChange={onSelectFiles}
              className="hidden"
            />
          </label>

          {files.length > 0 && (
            <ul className="text-xs text-muted-foreground max-h-40 overflow-auto divide-y divide-border/50">
              {files.map((f, i) => (
                <li key={`${f.name}-${i}`} className="flex justify-between py-1.5">
                  <span className="truncate">{f.name}</span>
                  <span className="shrink-0 ml-3">{humanSize(f.size)}</span>
                </li>
              ))}
            </ul>
          )}

          <button
            onClick={handleUpload}
            disabled={files.length === 0 || uploading}
            className="inline-flex items-center justify-center gap-2 px-4 py-2 rounded-md text-sm font-medium bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
          >
            {uploading ? (
              <Loader2 className="w-4 h-4 animate-spin" />
            ) : (
              <Upload className="w-4 h-4" />
            )}
            {t("uploads.upload")}
          </button>
        </div>
      )}

      {phase === "processing" && (
        <div className="border border-border rounded-xl bg-card overflow-hidden">
          <div className="flex items-center justify-between px-4 py-3 border-b border-border bg-muted/30">
            <span className="text-sm font-medium text-foreground">
              {done
                ? t("uploads.done", { count: rows.length })
                : t("uploads.processing", {
                    done: rows.length - pending,
                    total: rows.length,
                  })}
            </span>
            <button
              onClick={reset}
              className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
            >
              <RotateCcw className="w-3.5 h-3.5" />
              {t("uploads.newUpload")}
            </button>
          </div>
          <ul className="divide-y divide-border">
            {rows.map((r) => (
              <li
                key={r.key}
                className="flex items-center gap-3 px-4 py-3 text-sm"
              >
                <span className="flex-1 min-w-0 truncate text-foreground">
                  {r.filename}
                  <span className="text-muted-foreground ml-2 text-xs">
                    {humanSize(r.size)}
                  </span>
                  {r.reason && (
                    <span className="block text-[11px] text-muted-foreground truncate">
                      {r.reason}
                    </span>
                  )}
                </span>
                <StatusBadge status={r.status} />
                {r.status === "unidentified" && (
                  <button
                    onClick={() => navigate("/quarantine")}
                    className="inline-flex items-center gap-1 px-2 py-1 rounded-md text-xs font-medium border border-amber-500/30 text-amber-400 hover:bg-amber-500/10 transition-colors"
                    title={t("uploads.assign")}
                  >
                    <UserPlus className="w-3.5 h-3.5" />
                    {t("uploads.assign")}
                  </button>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

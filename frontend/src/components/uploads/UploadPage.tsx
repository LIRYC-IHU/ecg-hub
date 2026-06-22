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
  UserPlus,
  RotateCcw,
} from "lucide-react";
import { uploadECGs, type UploadFileResult } from "../../lib/api";
import { useNotification } from "../../context/NotificationContext";

// RowStatus mirrors the lifecycle of one uploaded file: queued on the server,
// then a terminal outcome pushed over the events WebSocket (or rejected upfront).
type RowStatus =
  | "queued"
  | "ingested"
  | "unidentified"
  | "quarantined"
  | "duplicate"
  | "rejected";

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
  const wsRef = useRef<WebSocket | null>(null);

  const pending = rows.filter((r) => r.status === "queued").length;

  // Close the live socket on unmount.
  useEffect(() => () => wsRef.current?.close(1000, "unmount"), []);

  // openEventsSocket connects to the shared ingestion events stream and resolves
  // once the socket is OPEN. We connect BEFORE enqueuing files — otherwise a fast
  // file can finish (and broadcast its terminal event) before we subscribe,
  // leaving its row stuck on "processing".
  function openEventsSocket(): Promise<void> {
    return new Promise((resolve, reject) => {
      const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
      const ws = new WebSocket(
        `${protocol}//${window.location.host}/api/v1/events/ws`,
      );
      wsRef.current = ws;
      ws.onmessage = (e) => {
        let ev: IngestionEvent;
        try {
          ev = JSON.parse(e.data as string) as IngestionEvent;
        } catch {
          return;
        }
        if (!ev.filename) return;
        // Refresh patient/quarantine lists so other open views stay in sync.
        void queryClient.invalidateQueries({ queryKey: ["patients"] });
        void queryClient.invalidateQueries({ queryKey: ["admin", "quarantine"] });
        setRows((prev) => {
          const idx = prev.findIndex(
            (r) => r.status === "queued" && r.filename === ev.filename,
          );
          if (idx === -1) return prev;
          const next = [...prev];
          next[idx] = {
            ...next[idx],
            status: statusFromEvent(ev.type),
            patientId: ev.patient_id,
            quarantineId: ev.quarantine_id,
            reason: ev.reason,
          };
          return next;
        });
      };
      ws.onopen = () => resolve();
      ws.onerror = () => reject(new Error("ws error"));
      // Fallback so a stalled handshake never blocks the upload.
      setTimeout(() => {
        if (ws.readyState !== WebSocket.OPEN) reject(new Error("ws timeout"));
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

    // Connect the live socket first (best-effort — proceed even if it fails).
    try {
      await openEventsSocket();
    } catch {
      /* no live updates; rows may stay "processing" until refresh */
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
          return sr && sr.status === "rejected"
            ? { ...r, status: "rejected", reason: sr.error }
            : r;
        }),
      );
    } catch {
      notify("error", t("uploads.uploadError"));
      wsRef.current?.close(1000, "error");
      setPhase("select");
    } finally {
      setUploading(false);
    }
  }

  function reset() {
    wsRef.current?.close(1000, "reset");
    wsRef.current = null;
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

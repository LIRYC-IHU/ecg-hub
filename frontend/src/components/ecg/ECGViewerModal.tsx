import { useRef, useState, useEffect } from "react";
import { X, Download, Maximize2, Settings } from "lucide-react";
import { ECGViewer } from "../../ecg-viewer/ECGViewer";
import type { ECGViewerHandle } from "../../ecg-viewer/ECGViewer";
import type { EcgRecord } from "../../ecg-viewer/ecgTypes";
import "../../ecg-viewer/ECGViewer.css";

const BASE_URL = (import.meta.env as Record<string, string>).VITE_API_URL ?? "";
const PREFS_KEY = "ecghub.ecgviewer.prefs";

// ─── Theme presets ────────────────────────────────────────────────────────────

type Theme = "red" | "green" | "bw" | "dark";

const THEMES: { id: Theme; label: string; className: string }[] = [
  { id: "red",   label: "Rouge",   className: "" },
  { id: "green", label: "Vert",    className: "ecg-theme-green" },
  { id: "bw",    label: "N&B",     className: "ecg-theme-bw" },
  { id: "dark",  label: "Sombre",  className: "ecg-theme-dark" },
];

interface ViewerPrefs {
  theme: Theme;
  showGrid: boolean;
  traceThickness: number;
}

const DEFAULT_PREFS: ViewerPrefs = { theme: "red", showGrid: true, traceThickness: 1 };

function loadPrefs(): ViewerPrefs {
  try {
    const s = localStorage.getItem(PREFS_KEY);
    if (s) return { ...DEFAULT_PREFS, ...JSON.parse(s) };
  } catch { /* ignore */ }
  return DEFAULT_PREFS;
}

function savePrefs(p: ViewerPrefs) {
  localStorage.setItem(PREFS_KEY, JSON.stringify(p));
}

interface Props {
  ecgId: string;
  filename?: string;
  onClose: () => void;
}

export function ECGViewerModal({ ecgId, filename, onClose }: Props) {
  const viewerRef = useRef<ECGViewerHandle>(null);
  const [record, setRecord] = useState<EcgRecord | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [showOptions, setShowOptions] = useState(false);
  const [prefs, setPrefs] = useState<ViewerPrefs>(loadPrefs);

  // Persist prefs and refresh WebGL on any pref change
  useEffect(() => {
    savePrefs(prefs);
    // Small delay so the DOM updates the CSS variables before WebGL re-reads them
    const id = setTimeout(() => { viewerRef.current?.refresh(); }, 20);
    return () => clearTimeout(id);
  }, [prefs]);

  // Close on Escape — same pattern as ConfirmDialog. In fullscreen the browser
  // already consumes Escape to exit it, so don't also close the viewer.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || document.fullscreenElement) return;
      if (showOptions) { setShowOptions(false); return; }
      onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, showOptions]);

  useEffect(() => {
    setLoading(true);
    setError(null);
    fetch(`${BASE_URL}/api/v1/ecgs/${ecgId}/waveform`)
      .then((res) => {
        if (!res.ok) {
          return res.json().then((e) => { throw new Error(e.message ?? "Failed to load waveform"); });
        }
        return res.arrayBuffer();
      })
      .then((buf) => setRecord(decodeWireFormat(buf)))
      .catch((e) => setError(e.message ?? "Failed to load ECG"))
      .finally(() => setLoading(false));
  }, [ecgId]);

  // Refresh WebGL after record loads
  useEffect(() => {
    if (!record) return;
    const id = setTimeout(() => { viewerRef.current?.refresh(); }, 50);
    return () => clearTimeout(id);
  }, [record]);

  const handleExport = () => {
    const url = viewerRef.current?.exportPng();
    if (!url) return;
    const a = document.createElement("a");
    a.href = url;
    a.download = `${filename ?? ecgId}.png`;
    a.click();
  };

  // Build CSS vars for grid visibility and trace thickness
  const themeClass = THEMES.find((t) => t.id === prefs.theme)?.className ?? "";
  const inlineStyle: React.CSSProperties = {
    "--ecg-trace-thickness": `${prefs.traceThickness}px`,
    ...(prefs.showGrid ? {} : {
      "--ecg-grid-minor": "transparent",
      "--ecg-grid-major": "transparent",
    }),
  } as React.CSSProperties;

  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-background">
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-border shrink-0 bg-card">
        <span className="text-sm font-medium text-foreground truncate max-w-xs">
          {filename ?? ecgId}
        </span>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setShowOptions((v) => !v)}
            className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs border transition-colors ${
              showOptions ? "bg-primary/10 text-primary border-primary/30" : "border-border text-muted-foreground hover:text-foreground hover:bg-muted"
            }`}
          >
            <Settings className="w-3.5 h-3.5" />
            Options
          </button>
          <button
            onClick={handleExport}
            disabled={!record}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs border border-border text-muted-foreground hover:text-foreground hover:bg-muted transition-colors disabled:opacity-30"
          >
            <Download className="w-3.5 h-3.5" />
            PNG
          </button>
          <button
            onClick={() => viewerRef.current?.toggleFullscreen()}
            disabled={!record}
            className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-muted transition-colors disabled:opacity-30"
          >
            <Maximize2 className="w-3.5 h-3.5" />
          </button>
          <button
            onClick={onClose}
            className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
          >
            <X className="w-4 h-4" />
          </button>
        </div>
      </div>

      {/* Options panel */}
      {showOptions && (
        <div className="shrink-0 flex items-center gap-6 px-4 py-2.5 border-b border-border bg-muted/30">
          {/* Theme */}
          <div className="flex items-center gap-2">
            <span className="text-[11px] font-medium text-muted-foreground uppercase tracking-wide">Thème</span>
            <div className="flex gap-1">
              {THEMES.map((t) => (
                <button
                  key={t.id}
                  onClick={() => setPrefs((p) => ({ ...p, theme: t.id }))}
                  className={`px-2.5 py-1 rounded text-xs font-medium border transition-colors ${
                    prefs.theme === t.id ? "bg-primary/10 text-primary border-primary/30" : "border-border text-muted-foreground hover:border-primary/20"
                  }`}
                >
                  {t.label}
                </button>
              ))}
            </div>
          </div>

          {/* Grid */}
          <div className="flex items-center gap-2">
            <span className="text-[11px] font-medium text-muted-foreground uppercase tracking-wide">Grille</span>
            <button
              onClick={() => setPrefs((p) => ({ ...p, showGrid: !p.showGrid }))}
              className={`relative w-9 h-5 rounded-full transition-colors ${prefs.showGrid ? "bg-primary" : "bg-muted-foreground/30"}`}
            >
              <span className={`absolute left-0.5 top-0.5 w-4 h-4 rounded-full bg-white shadow transition-transform ${prefs.showGrid ? "translate-x-4" : "translate-x-0"}`} />
            </button>
          </div>

          {/* Trace thickness */}
          <div className="flex items-center gap-2">
            <span className="text-[11px] font-medium text-muted-foreground uppercase tracking-wide">Épaisseur</span>
            <div className="flex gap-1">
              {[0.75, 1, 1.5, 2].map((v) => (
                <button
                  key={v}
                  onClick={() => setPrefs((p) => ({ ...p, traceThickness: v }))}
                  className={`px-2 py-1 rounded text-xs border transition-colors ${
                    prefs.traceThickness === v ? "bg-primary/10 text-primary border-primary/30" : "border-border text-muted-foreground hover:border-primary/20"
                  }`}
                >
                  {v}px
                </button>
              ))}
            </div>
          </div>
        </div>
      )}

      {/* Content */}
      <div className="flex-1 min-h-0" style={{ display: "flex", flexDirection: "column" }}>
        {loading && (
          <div className="flex-1 flex items-center justify-center">
            <div className="text-sm text-muted-foreground">Chargement de la trace ECG…</div>
          </div>
        )}
        {error && (
          <div className="flex-1 flex items-center justify-center p-8">
            <div className="text-sm text-destructive text-center max-w-md">{error}</div>
          </div>
        )}
        {record && (
          <div style={{ flex: 1, minHeight: 0, display: "flex" }}>
            <ECGViewer
              ref={viewerRef}
              record={record}
              initialOptions={{ layout: "3x4", timeScale: 25, amplitudeScale: 10 }}
              className={themeClass}
              style={inlineStyle}
            />
          </div>
        )}
      </div>
    </div>
  );
}

// ─── Binary wire format decoder ───────────────────────────────────────────────

function decodeWireFormat(buf: ArrayBuffer): EcgRecord {
  const view = new DataView(buf);
  const jsonLen = view.getUint32(0, true);
  const jsonBytes = new Uint8Array(buf, 4, jsonLen);
  const meta = JSON.parse(new TextDecoder().decode(jsonBytes)) as {
    numChannels: number;
    numSamples: number;
    samplingFrequency: number;
    durationSec: number;
    patientName?: string;
    acquisitionDate?: string;
    channelLabels: string[];
  };

  const samplesOffset = 4 + jsonLen;
  const samplesBuffer = buf.slice(samplesOffset);
  const allSamples = new Float32Array(samplesBuffer);

  const channels = meta.channelLabels.map((label, c) => {
    const samples = new Float32Array(meta.numSamples);
    const offset = c * meta.numSamples;
    for (let s = 0; s < meta.numSamples; s++) {
      samples[s] = allSamples[offset + s];
    }
    return { label, samples };
  });

  return {
    channels,
    samplingFrequency: meta.samplingFrequency,
    durationSec: meta.durationSec,
    patientName: meta.patientName,
    acquisitionDate: meta.acquisitionDate,
  };
}

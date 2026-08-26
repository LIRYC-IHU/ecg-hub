import {
  type StorageBackendInfo,
  type VolumeMetric,
  fetchStorageMetrics,
} from "@/lib/api";
import { Cloud, HardDrive } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Spinner } from "../ui/Spinner";

// The API sends display names for the volumes; map the known ones to a
// translation key and fall back to the raw name for anything new.
const VOLUME_LABEL_KEYS: Record<string, string> = {
  "ECG Storage": "admin.system.storage.ecg",
  Quarantine: "admin.system.storage.quarantine",
};

// With object storage the same volumes hold only what has not been uploaded
// yet. Labelling them "ECG storage" would misreport both what is stored and
// what the remaining space means.
const SPOOL_LABEL_KEYS: Record<string, string> = {
  "ECG Storage": "admin.system.storage.ecgSpool",
  Quarantine: "admin.system.storage.quarantineSpool",
};

function formatBytes(bytes: number) {
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  if (bytes === 0) return "0 B";
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(1) + " " + sizes[i];
}

// formatAge keeps the backlog age readable without pulling in a date library
// for one string.
function formatAge(seconds: number) {
  if (seconds < 60) return `${Math.round(seconds)}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}

function MetricCard() {
  const { t } = useTranslation();
  const [volumes, setVolumes] = useState<VolumeMetric[]>([]);
  const [backend, setBackend] = useState<StorageBackendInfo | null>(null);
  const volumeLabel = (name: string) => {
    const keys = backend?.kind === "s3" ? SPOOL_LABEL_KEYS : VOLUME_LABEL_KEYS;
    return keys[name] ? t(keys[name]) : name;
  };
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    loadMetrics();
  }, []);

  async function loadMetrics() {
    setError("");
    setLoading(true);
    try {
      const data = await fetchStorageMetrics();
      if (!data) {
        setError("Failed to load metrics");
      } else {
        if (data.error) {
          setError("Failed to load metrics:" + data.error);
        } else {
          setVolumes(data.volumes);
          setBackend(data.backend);
        }
      }
    } catch (err) {
      setError("Failed to load metrics:" + err);
    } finally {
      setLoading(false);
    }
  }

  if (loading) {
    return (
      <div className="bg-card rounded-lg border border-border p-5">
        <div className="flex items-center gap-3 mb-4">
          <Spinner size={15} className="text-muted-foreground shrink-0" />
        </div>
      </div>
    );
  }

  return (
    <div className="bg-card rounded-lg border border-border p-5 space-y-6">
      <div className="flex items-center gap-3">
        <p className="font-semibold">{t("admin.system.storage.title")}</p>
      </div>

      {backend?.kind === "s3" && (
        <div className="rounded-lg border border-border bg-muted/30 px-3 py-2 space-y-1">
          <div className="flex items-center gap-2 text-sm font-medium">
            <Cloud className="w-4 h-4 text-primary" />
            <span>{t("admin.system.storage.objectStorage")}</span>
          </div>
          <p className="text-xs text-muted-foreground break-all">
            {backend.bucket}
            {backend.endpoint ? ` — ${backend.endpoint}` : ""}
          </p>
          <p className="text-xs flex items-center gap-1.5">
            <HardDrive className="w-3 h-3 text-muted-foreground shrink-0" />
            {backend.pendingUploads === 0 ? (
              <span className="text-muted-foreground">
                {t("admin.system.storage.spoolEmpty")}
              </span>
            ) : (
              <span
                className={
                  backend.oldestPendingSeconds > 3600
                    ? "text-destructive font-medium"
                    : "text-muted-foreground"
                }
              >
                {t("admin.system.storage.spoolPending", {
                  count: backend.pendingUploads,
                  age: formatAge(backend.oldestPendingSeconds),
                })}
              </span>
            )}
          </p>
        </div>
      )}

      {error ? (
        <div className="bg-destructive/5 border border-destructive/20 rounded-lg px-3 py-2">
          <p className="text-xs text-destructive">{error}</p>
        </div>
      ) : (
        volumes &&
        volumes.map((v) => {
          const used = v.total - v.available;
          const unlimited = v.total <= 0;
          const percent = unlimited ? 0 : Math.round((used / v.total) * 100);
          // The cap is a soft cap — usage can legitimately exceed it.
          // Clamp the bar width but keep the real percentage in the label.
          const barWidth = Math.min(100, Math.max(0, percent));
          const capLabel = unlimited
            ? "Unlimited"
            : (v.max_size ?? formatBytes(v.total));

          return (
            <div key={v.name} className="space-y-2">
              <div className="flex justify-between text-sm">
                <span className="font-medium">{volumeLabel(v.name)}</span>
                <span>
                  {formatBytes(used)} / {capLabel}
                  {!unlimited && ` (${percent}%)`}
                </span>
              </div>

              {!unlimited && (
                <div
                  role="progressbar"
                  aria-label={volumeLabel(v.name)}
                  aria-valuenow={percent}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  className="w-full h-3 bg-muted rounded-full overflow-hidden border-2 border-border"
                >
                  <div
                    className={`h-full transition-all duration-500 ${
                      percent > 85
                        ? "bg-red-500"
                        : percent > 60
                          ? "bg-yellow-500"
                          : "bg-green-500"
                    }`}
                    style={{ width: `${barWidth}%` }}
                  />
                </div>
              )}
            </div>
          );
        })
      )}
    </div>
  );
}
export default MetricCard;

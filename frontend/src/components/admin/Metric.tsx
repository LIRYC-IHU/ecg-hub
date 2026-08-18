import { type VolumeMetric, fetchStorageMetrics } from "@/lib/api";
import { useEffect, useState } from "react";
import { Spinner } from "../ui/Spinner";

function formatBytes(bytes: number) {
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  if (bytes === 0) return "0 B";
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(1) + " " + sizes[i];
}

function MetricCard() {
  const [volumes, setVolumes] = useState<VolumeMetric[]>([]);
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
        <p className="font-semibold">Storage Metrics</p>
      </div>

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
                <span className="font-medium">{v.name}</span>
                <span>
                  {formatBytes(used)} / {capLabel}
                  {!unlimited && ` (${percent}%)`}
                </span>
              </div>

              {!unlimited && (
                <div
                  role="progressbar"
                  aria-label={v.name}
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

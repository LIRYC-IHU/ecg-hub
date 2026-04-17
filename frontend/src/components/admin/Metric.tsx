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
        setVolumes(data.volumes);
      }
    } catch (err) {
      setError("Failed to load metrics");
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
          const percent = Math.round((used / v.total) * 100);

          return (
            <div key={v.name} className="space-y-2">
              <div className="flex justify-between text-sm">
                <span className="font-medium">{v.name}</span>
                <span>
                  {formatBytes(used)} / {formatBytes(v.total)} ({percent}%)
                </span>
              </div>

              {/* Barre */}
              <div className="w-full h-3 bg-blue-500 rounded-full overflow-hidden border-border border-2 ">
                <div
                  className={`h-full transition-all duration-500 ${
                    percent > 85
                      ? "bg-red-500"
                      : percent > 60
                        ? "bg-yellow-500"
                        : "bg-green-500"
                  }`}
                  style={{ width: `${percent}%` }}
                />
              </div>
            </div>
          );
        })
      )}
    </div>
  );
}
export default MetricCard;

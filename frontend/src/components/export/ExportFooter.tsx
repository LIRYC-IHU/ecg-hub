import { useState } from "react";
import { Archive } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useMutation, useQuery } from "@tanstack/react-query";
import { createExportJob, fetchModules } from "../../lib/api";
import type { ExportFormat } from "../../lib/api";
import { useNotification } from "../../context/NotificationContext";

interface Props {
  count: number;
  ecgIds: number[];
  onClear: () => void;
}

// ExportFooter appears contextually at the bottom when ≥1 ECG is selected.
// Clicking "Export ZIP" calls POST /api/v1/exports and enqueues a background ZIP job.
export function ExportFooter({ count, ecgIds, onClear }: Props) {
  const { t } = useTranslation();
  const { notify, notifyProgress } = useNotification();
  const [selectedFormat, setSelectedFormat] = useState("original");

  // Fetch modules to build the format list. Deduplicate by format ID across all modules.
  const { data: modules } = useQuery({
    queryKey: ["modules"],
    queryFn: fetchModules,
    staleTime: 5 * 60_000,
  });

  // Collect unique formats across all modules (original always first).
  const availableFormats: ExportFormat[] = [];
  const seen = new Set<string>();
  for (const mod of modules ?? []) {
    for (const fmt of mod.formats ?? []) {
      if (!seen.has(fmt.id)) {
        seen.add(fmt.id);
        availableFormats.push(fmt);
      }
    }
  }
  // Fallback when modules haven't loaded yet.
  if (availableFormats.length === 0) {
    availableFormats.push({ id: "original", label: "Original", extension: "" });
  }

  const exportMutation = useMutation({
    mutationFn: () =>
      createExportJob({
        ecg_ids: ecgIds,
        format: selectedFormat === "original" ? undefined : selectedFormat,
      }),
    onSuccess: (response) => {
      notifyProgress(response.id, t("export.overlayTitle"));
      // Defer clear so React flushes the notification render before unmounting
      // this component (both updates would otherwise batch into the same pass).
      setTimeout(onClear, 100);
    },
    onError: () => {
      notify("error", t("export.jobError", "Échec du lancement de l'export"));
    },
  });

  return (
    <div className="fixed bottom-0 left-52 right-0 z-20 border-t border-border bg-card/90 backdrop-blur-sm px-6 py-3 flex items-center justify-between shrink-0 ">
      <div className="flex items-center gap-2 text-sm text-foreground">
        <Archive className="w-4 h-4 text-primary" />
        <span className="font-medium">{t("export.selected", { count })}</span>
      </div>
      <div className="flex items-center gap-3">
        <button
          onClick={onClear}
          className="text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          {t("export.clearSelection", "Tout désélectionner")}
        </button>
        {availableFormats.length > 1 && (
          <select
            value={selectedFormat}
            onChange={(e) => setSelectedFormat(e.target.value)}
            disabled={exportMutation.isPending}
            className="text-sm bg-card border border-border rounded-lg px-3 py-2 text-foreground focus:outline-none focus:ring-2 focus:ring-ring/20 disabled:opacity-60"
          >
            {availableFormats.map((fmt) => (
              <option key={fmt.id} value={fmt.id}>
                {fmt.label}
              </option>
            ))}
          </select>
        )}
        <button
          onClick={() => exportMutation.mutate()}
          disabled={exportMutation.isPending}
          className="flex items-center gap-2 bg-primary text-primary-foreground text-sm font-medium px-4 py-2 rounded-lg hover:bg-primary/90 transition-colors disabled:opacity-60 disabled:cursor-not-allowed"
        >
          <Archive className="w-4 h-4" />
          {exportMutation.isPending
            ? t("export.exporting", "Export…")
            : t("export.exportZip")}
        </button>
      </div>
    </div>
  );
}

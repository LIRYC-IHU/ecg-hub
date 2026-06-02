import { useState } from "react";
import { Archive } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useMutation } from "@tanstack/react-query";
import { createExportJob } from "../../lib/api";
import { useNotification } from "../../context/NotificationContext";
import { DownloadFormatPopup } from "../ecg/DownloadFormatPopup";

interface Props {
  count: number;
  ecgIds: number[];
  onClear: () => void;
}

// ExportFooter appears contextually at the bottom when ≥1 ECG is selected.
// Clicking "Export ZIP" opens a format picker (same popup as per-ECG download)
// and POSTs to /api/v1/exports with the chosen formats.
export function ExportFooter({ count, ecgIds, onClear }: Props) {
  const { t } = useTranslation();
  const { notify, notifyProgress } = useNotification();
  const [popupOpen, setPopupOpen] = useState(false);

  const exportMutation = useMutation({
    mutationFn: (formats: string[]) =>
      createExportJob({ ecg_ids: ecgIds, formats }),
    onSuccess: (response) => {
      notifyProgress(response.id, t("export.overlayTitle"));
      setPopupOpen(false);
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
        <button
          onClick={() => setPopupOpen(true)}
          disabled={exportMutation.isPending}
          className="flex items-center gap-2 bg-primary text-primary-foreground text-sm font-medium px-4 py-2 rounded-lg hover:bg-primary/90 transition-colors disabled:opacity-60 disabled:cursor-not-allowed"
        >
          <Archive className="w-4 h-4" />
          {exportMutation.isPending
            ? t("export.exporting", "Export…")
            : t("export.exportZip")}
        </button>
      </div>

      <DownloadFormatPopup
        open={popupOpen}
        onClose={() => setPopupOpen(false)}
        ecgIds={ecgIds}
        busy={exportMutation.isPending}
        onConfirm={(formats) => exportMutation.mutate(formats)}
      />
    </div>
  );
}

import { useEffect } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { AlertTriangle } from "lucide-react";
import { Spinner } from "./Spinner";

interface Props {
  open: boolean;
  title: string;
  message: React.ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  // danger renders the confirm button in the destructive colour (delete flows).
  danger?: boolean;
  // busy disables both buttons and shows a spinner on confirm (async actions).
  busy?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}

// ConfirmDialog is the shared, in-app replacement for window.confirm — a centered
// modal matching the app's design tokens. Backdrop click and Escape dismiss it;
// pass danger for destructive actions to get the red confirm button.
export function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel,
  cancelLabel,
  danger,
  busy,
  onConfirm,
  onClose,
}: Props) {
  const { t } = useTranslation();

  // Close on Escape — matches the backdrop dismiss and keeps focus predictable.
  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape" && !busy) onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, busy, onClose]);

  if (!open) return null;

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      onClick={() => !busy && onClose()}
      role="dialog"
      aria-modal="true"
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="bg-card border border-border rounded-lg p-5 w-full max-w-sm shadow-xl"
      >
        <div className="flex items-start gap-3 mb-4">
          {danger && (
            <span className="mt-0.5 shrink-0 text-destructive">
              <AlertTriangle size={18} />
            </span>
          )}
          <div className="space-y-1">
            <h2 className="text-sm font-semibold text-foreground">{title}</h2>
            <div className="text-xs text-muted-foreground">{message}</div>
          </div>
        </div>

        <div className="flex justify-end gap-2">
          <button
            onClick={onClose}
            disabled={busy}
            className="px-3 py-1.5 text-xs rounded border border-border hover:bg-muted transition-colors disabled:opacity-50"
          >
            {cancelLabel ?? t("common.cancel")}
          </button>
          <button
            onClick={onConfirm}
            disabled={busy}
            className={
              danger
                ? "flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-destructive text-destructive-foreground hover:bg-destructive/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                : "flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-primary text-primary-foreground hover:bg-primary/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
            }
          >
            {busy && <Spinner size={11} className="text-destructive-foreground" />}
            {confirmLabel ?? t("common.confirm")}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}

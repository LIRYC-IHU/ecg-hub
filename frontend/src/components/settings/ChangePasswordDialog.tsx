import { useState } from "react";
import { useTranslation } from "react-i18next";
import { KeyRound, X } from "lucide-react";
import { useMutation } from "@tanstack/react-query";
import { changeOwnPassword } from "../../lib/api";
import { useNotification } from "../../context/NotificationContext";
import { Spinner } from "../ui/Spinner";

interface Props {
  onClose: () => void;
}

/**
 * Self-service password change for local accounts.
 *
 * The current password is required: it is what makes the change safe to expose
 * on a session alone, so a borrowed session cannot lock the owner out.
 */
export function ChangePasswordDialog({ onClose }: Props) {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirmation, setConfirmation] = useState("");

  const mismatch = confirmation !== "" && next !== confirmation;

  const mutation = useMutation({
    mutationFn: () => changeOwnPassword(current, next),
    onSuccess: () => {
      notify("success", t("auth.passwordChanged"));
      onClose();
    },
    onError: (err: unknown) =>
      notify("error", (err as { message?: string })?.message ?? t("common.error")),
  });

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center bg-background/70 px-4">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!mismatch) mutation.mutate();
        }}
        className="w-full max-w-sm bg-card border border-border rounded-xl p-5 shadow-lg"
      >
        <div className="flex items-center gap-2 mb-1">
          <KeyRound className="w-4 h-4 text-primary" />
          <h2 className="text-sm font-semibold text-foreground flex-1">
            {t("auth.changePassword")}
          </h2>
          <button
            type="button"
            onClick={onClose}
            aria-label={t("common.close")}
            className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted"
          >
            <X className="w-4 h-4" />
          </button>
        </div>
        <p className="text-xs text-muted-foreground mb-4">
          {t("auth.changePasswordHint")}
        </p>

        <div className="space-y-2">
          <input
            type="password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            placeholder={t("auth.currentPassword")}
            autoComplete="current-password"
            className="w-full text-xs bg-background border border-border rounded px-2 py-2 focus:outline-none focus:ring-1 focus:ring-ring/20"
          />
          <input
            type="password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
            placeholder={t("auth.newPassword")}
            autoComplete="new-password"
            className="w-full text-xs bg-background border border-border rounded px-2 py-2 focus:outline-none focus:ring-1 focus:ring-ring/20"
          />
          <input
            type="password"
            value={confirmation}
            onChange={(e) => setConfirmation(e.target.value)}
            placeholder={t("auth.confirmPassword")}
            autoComplete="new-password"
            className={`w-full text-xs bg-background border rounded px-2 py-2 focus:outline-none focus:ring-1 focus:ring-ring/20 ${
              mismatch ? "border-destructive" : "border-border"
            }`}
          />
          {mismatch && (
            <p className="text-[11px] text-destructive">{t("auth.passwordMismatch")}</p>
          )}
        </div>

        <div className="flex justify-end gap-2 mt-4">
          <button
            type="button"
            onClick={onClose}
            className="text-xs text-muted-foreground hover:underline px-2"
          >
            {t("common.cancel")}
          </button>
          <button
            type="submit"
            disabled={
              mutation.isPending || current === "" || next === "" || mismatch
            }
            className="text-xs bg-primary text-primary-foreground px-3 py-1.5 rounded font-medium hover:bg-primary/90 disabled:opacity-50 flex items-center gap-1.5"
          >
            {mutation.isPending && <Spinner size={11} className="text-primary-foreground" />}
            {t("common.save")}
          </button>
        </div>
      </form>
    </div>
  );
}

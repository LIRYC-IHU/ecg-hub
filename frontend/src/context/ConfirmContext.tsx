import { createContext, useCallback, useContext, useRef, useState } from "react";
import { ConfirmDialog } from "../components/ui/ConfirmDialog";

// ConfirmOptions describe a single confirmation prompt. Everything but `message`
// is optional; ConfirmDialog falls back to common.confirm / common.cancel.
export interface ConfirmOptions {
  title: string;
  message: React.ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
}

type ConfirmFn = (opts: ConfirmOptions) => Promise<boolean>;

const ConfirmContext = createContext<ConfirmFn | null>(null);

// ConfirmProvider renders a single shared ConfirmDialog and exposes an
// imperative `confirm()` that resolves to the user's choice — an in-app,
// promise-based replacement for window.confirm(). Usage:
//   const confirm = useConfirm();
//   if (await confirm({ title, message, danger: true })) { … }
export function ConfirmProvider({ children }: { children: React.ReactNode }) {
  const [opts, setOpts] = useState<ConfirmOptions | null>(null);
  // Holds the resolver of the in-flight confirm() promise.
  const resolverRef = useRef<((ok: boolean) => void) | null>(null);

  const confirm = useCallback<ConfirmFn>((options) => {
    setOpts(options);
    return new Promise<boolean>((resolve) => {
      resolverRef.current = resolve;
    });
  }, []);

  const settle = useCallback((ok: boolean) => {
    resolverRef.current?.(ok);
    resolverRef.current = null;
    setOpts(null);
  }, []);

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      <ConfirmDialog
        open={opts !== null}
        title={opts?.title ?? ""}
        message={opts?.message ?? ""}
        confirmLabel={opts?.confirmLabel}
        cancelLabel={opts?.cancelLabel}
        danger={opts?.danger}
        onConfirm={() => settle(true)}
        onClose={() => settle(false)}
      />
    </ConfirmContext.Provider>
  );
}

// useConfirm returns the imperative confirm() function. Must be used within
// ConfirmProvider.
export function useConfirm(): ConfirmFn {
  const ctx = useContext(ConfirmContext);
  if (!ctx) throw new Error("useConfirm must be used inside ConfirmProvider");
  return ctx;
}

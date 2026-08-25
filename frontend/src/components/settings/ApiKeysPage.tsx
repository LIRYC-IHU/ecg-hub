import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  KeyRound,
  Plus,
  Trash2,
  Copy,
  Check,
  AlertTriangle,
  ExternalLink,
} from "lucide-react";
import {
  fetchApiKeys,
  createApiKey,
  deleteApiKey,
  type CreatedApiKey,
} from "../../lib/api";
import { Spinner } from "../ui/Spinner";
import { ConfirmDialog } from "../ui/ConfirmDialog";
import { useNotification } from "../../context/NotificationContext";

export function ApiKeysPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { notify } = useNotification();

  const [newName, setNewName] = useState("");
  // Holds the freshly created key with its one-time plaintext secret.
  const [createdKey, setCreatedKey] = useState<CreatedApiKey | null>(null);
  const [copied, setCopied] = useState(false);
  // Id of the key pending delete confirmation (null = dialog closed).
  const [deletingId, setDeletingId] = useState<string | null>(null);

  const { data: keys = [], isLoading } = useQuery({
    queryKey: ["api-keys"],
    queryFn: fetchApiKeys,
    staleTime: 30_000,
  });

  const createMutation = useMutation({
    mutationFn: (name: string) => createApiKey(name),
    onSuccess: (key) => {
      setCreatedKey(key);
      setNewName("");
      setCopied(false);
      void queryClient.invalidateQueries({ queryKey: ["api-keys"] });
      notify("success", t("apiKeys.created", { name: key.name }));
    },
    onError: () => notify("error", t("apiKeys.createError")),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteApiKey(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["api-keys"] });
      notify("success", t("apiKeys.deleted"));
    },
    onError: () => notify("error", t("apiKeys.deleteError")),
    onSettled: () => setDeletingId(null),
  });

  function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    const name = newName.trim();
    if (!name) return;
    createMutation.mutate(name);
  }

  const deletingKey = keys.find((k) => k.id === deletingId) ?? null;

  function copyKey() {
    if (!createdKey) return;
    void navigator.clipboard.writeText(createdKey.key);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }

  return (
    <div className="max-w-3xl mx-auto p-6 space-y-6">
      <div className="flex items-center gap-3">
        <div className="w-9 h-9 rounded-lg bg-primary/10 flex items-center justify-center">
          <KeyRound className="w-5 h-5 text-primary" />
        </div>
        <div className="flex-1">
          <h1 className="text-lg font-semibold text-foreground">
            {t("apiKeys.title")}
          </h1>
          <p className="text-xs text-muted-foreground">
            {t("apiKeys.subtitle")}
          </p>
        </div>
        <a
          href="/api-docs"
          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium border border-border text-muted-foreground hover:text-foreground hover:bg-muted/50 transition-colors shrink-0"
        >
          <ExternalLink className="w-3.5 h-3.5" />
          {t("apiKeys.docsLink")}
        </a>
      </div>

      {/* One-time plaintext key display */}
      {createdKey && (
        <div className="rounded-lg border border-primary/30 bg-primary/5 p-4 space-y-3">
          <div className="flex items-start gap-2 text-xs text-amber-500">
            <AlertTriangle className="w-4 h-4 shrink-0 mt-0.5" />
            <span>{t("apiKeys.copyWarning")}</span>
          </div>
          <div className="flex items-center gap-2">
            <code className="flex-1 font-mono text-xs bg-card border border-border rounded px-3 py-2 break-all">
              {createdKey.key}
            </code>
            <button
              onClick={copyKey}
              className="inline-flex items-center gap-1.5 px-3 py-2 rounded-md text-xs font-medium bg-primary text-primary-foreground hover:bg-primary/90 transition-colors shrink-0"
            >
              {copied ? (
                <Check className="w-3.5 h-3.5" />
              ) : (
                <Copy className="w-3.5 h-3.5" />
              )}
              {copied ? t("apiKeys.copied") : t("apiKeys.copy")}
            </button>
          </div>
          <button
            onClick={() => setCreatedKey(null)}
            className="text-xs text-muted-foreground hover:text-foreground transition-colors"
          >
            {t("apiKeys.dismiss")}
          </button>
        </div>
      )}

      {/* Create form */}
      <form
        onSubmit={handleCreate}
        className="flex items-end gap-2 rounded-lg border border-border bg-card p-4"
      >
        <div className="flex-1">
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("apiKeys.nameLabel")}
          </label>
          <input
            type="text"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            maxLength={100}
            placeholder={t("apiKeys.namePlaceholder")}
            className="mt-1 w-full text-sm bg-background border border-border rounded px-3 py-2 focus:outline-none focus:ring-1 focus:ring-ring/20"
          />
        </div>
        <button
          type="submit"
          disabled={!newName.trim() || createMutation.isPending}
          className="inline-flex items-center gap-1.5 px-4 py-2 rounded-md text-sm font-medium bg-primary text-primary-foreground hover:bg-primary/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <Plus className="w-4 h-4" />
          {t("apiKeys.generate")}
        </button>
      </form>

      {/* Keys list */}
      <div className="rounded-lg border border-border bg-card divide-y divide-border">
        {isLoading ? (
          <div className="p-8 flex justify-center">
            <Spinner size={20} className="text-primary" />
          </div>
        ) : keys.length === 0 ? (
          <div className="p-8 text-center text-sm text-muted-foreground">
            {t("apiKeys.empty")}
          </div>
        ) : (
          keys.map((key) => (
            <div
              key={key.id}
              className="flex items-center gap-3 px-4 py-3"
            >
              <KeyRound className="w-4 h-4 text-muted-foreground shrink-0" />
              <div className="flex-1 min-w-0">
                <p className="text-sm font-medium text-foreground truncate">
                  {key.name}
                </p>
                <p className="text-xs text-muted-foreground font-mono">
                  {key.prefix}…
                </p>
              </div>
              <div className="text-right text-[11px] text-muted-foreground shrink-0">
                <p>
                  {t("apiKeys.createdOn")}{" "}
                  {new Date(key.created_at).toLocaleDateString()}
                </p>
                <p>
                  {key.last_used_at
                    ? t("apiKeys.lastUsed", {
                        date: new Date(key.last_used_at).toLocaleDateString(),
                      })
                    : t("apiKeys.neverUsed")}
                </p>
              </div>
              <button
                onClick={() => setDeletingId(key.id)}
                disabled={deleteMutation.isPending}
                className="p-2 rounded-md text-destructive hover:bg-destructive/5 transition-colors shrink-0"
                aria-label={t("apiKeys.delete")}
              >
                <Trash2 className="w-4 h-4" />
              </button>
            </div>
          ))
        )}
      </div>

      <ConfirmDialog
        open={deletingId !== null}
        danger
        busy={deleteMutation.isPending}
        title={t("apiKeys.delete")}
        message={
          deletingKey
            ? t("apiKeys.confirmDeleteNamed", { name: deletingKey.name })
            : t("apiKeys.confirmDelete")
        }
        confirmLabel={t("apiKeys.delete")}
        onConfirm={() => deletingId && deleteMutation.mutate(deletingId)}
        onClose={() => setDeletingId(null)}
      />
    </div>
  );
}

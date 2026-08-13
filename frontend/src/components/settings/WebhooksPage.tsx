import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  Webhook as WebhookIcon,
  Plus,
  Trash2,
  Pencil,
  Send,
  ShieldAlert,
  CheckCircle2,
  XCircle,
  X,
  History,
  RotateCcw,
} from "lucide-react";
import {
  fetchWebhooks,
  fetchWebhookOptions,
  createWebhook,
  updateWebhook,
  deleteWebhook,
  testUserWebhook,
  fetchWebhookDeliveries,
  resendWebhookDelivery,
  type UserWebhook,
  type WebhookInput,
} from "../../lib/api";
import { Spinner } from "../ui/Spinner";
import { ConfirmDialog } from "../ui/ConfirmDialog";
import { useNotification } from "../../context/NotificationContext";

function WebhookDeliveries({ webhookId }: { webhookId: string }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { notify } = useNotification();

  const { data: deliveries = [], isLoading } = useQuery({
    queryKey: ["webhook-deliveries", webhookId],
    queryFn: () => fetchWebhookDeliveries(webhookId),
    staleTime: 10_000,
  });

  const resendMutation = useMutation({
    mutationFn: (deliveryId: string) =>
      resendWebhookDelivery(webhookId, deliveryId),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({
        queryKey: ["webhook-deliveries", webhookId],
      });
      if (result.ok) {
        notify("success", t("webhooks.resendOk", { status: result.status_code }));
      } else {
        notify("error", t("webhooks.resendFailed", { error: result.error }));
      }
    },
    onError: () => notify("error", t("webhooks.resendError")),
  });

  if (isLoading) {
    return (
      <div className="px-4 py-3 flex justify-center">
        <Spinner size={16} className="text-primary" />
      </div>
    );
  }

  if (deliveries.length === 0) {
    return (
      <div className="px-4 py-3 text-xs text-muted-foreground">
        {t("webhooks.deliveriesEmpty")}
      </div>
    );
  }

  return (
    <div className="divide-y divide-border/60">
      {deliveries.map((d) => (
        <div key={d.id} className="px-4 py-2 flex items-center gap-2 text-xs">
          {d.error === "" ? (
            <CheckCircle2 className="w-3.5 h-3.5 text-emerald-500 shrink-0" />
          ) : (
            <XCircle className="w-3.5 h-3.5 text-destructive shrink-0" />
          )}
          <span className="font-mono text-foreground">
            {t(`webhooks.eventTypes.${d.event}`, d.event)}
          </span>
          <span className="text-muted-foreground">
            {new Date(d.delivered_at).toLocaleString()}
          </span>
          {d.status_code > 0 && (
            <span className="text-muted-foreground">HTTP {d.status_code}</span>
          )}
          {d.error !== "" && (
            <span className="text-destructive truncate flex-1" title={d.error}>
              {d.error}
            </span>
          )}
          <button
            onClick={() => resendMutation.mutate(d.id)}
            disabled={resendMutation.isPending}
            className="ml-auto p-1.5 rounded text-muted-foreground hover:text-foreground hover:bg-muted transition-colors shrink-0"
            title={t("webhooks.resend")}
            aria-label={t("webhooks.resend")}
          >
            <RotateCcw className="w-3.5 h-3.5" />
          </button>
        </div>
      ))}
    </div>
  );
}

// Form state: secret/authHeader empty string means "unchanged" while editing
// (the API receives undefined); the explicit clear action sends "".
interface FormState {
  name: string;
  url: string;
  enabled: boolean;
  insecureSkipVerify: boolean;
  secret: string;
  authHeader: string;
  clearSecret: boolean;
  clearAuthHeader: boolean;
  events: string[];
  vendors: string[];
}

const emptyForm: FormState = {
  name: "",
  url: "",
  enabled: true,
  insecureSkipVerify: false,
  secret: "",
  authHeader: "",
  clearSecret: false,
  clearAuthHeader: false,
  events: [],
  vendors: [],
};

function toInput(form: FormState, editing: boolean): WebhookInput {
  const input: WebhookInput = {
    name: form.name.trim(),
    url: form.url.trim(),
    enabled: form.enabled,
    insecure_skip_verify: form.insecureSkipVerify,
    events: form.events,
    vendors: form.vendors,
  };
  // Create: always send what was typed. Update: only send when changed/cleared.
  if (!editing || form.secret !== "" || form.clearSecret) {
    input.secret = form.clearSecret ? "" : form.secret;
  }
  if (!editing || form.authHeader !== "" || form.clearAuthHeader) {
    input.auth_header = form.clearAuthHeader ? "" : form.authHeader;
  }
  return input;
}

export function WebhooksPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { notify } = useNotification();

  const [form, setForm] = useState<FormState>(emptyForm);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [showForm, setShowForm] = useState(false);

  // Id of the webhook pending delete confirmation (null = dialog closed).
  const [deletingId, setDeletingId] = useState<string | null>(null);

  const [historyId, setHistoryId] = useState<string | null>(null);


  const { data: hooks = [], isLoading } = useQuery({
    queryKey: ["webhooks"],
    queryFn: fetchWebhooks,
    staleTime: 30_000,
  });

  const { data: options } = useQuery({
    queryKey: ["webhook-options"],
    queryFn: fetchWebhookOptions,
    staleTime: 5 * 60_000,
  });

  const invalidate = () =>
    void queryClient.invalidateQueries({ queryKey: ["webhooks"] });

  const saveMutation = useMutation({
    mutationFn: (input: WebhookInput) =>
      editingId ? updateWebhook(editingId, input) : createWebhook(input),
    onSuccess: () => {
      invalidate();
      notify("success", t(editingId ? "webhooks.updated" : "webhooks.created"));
      closeForm();
    },
    onError: () => notify("error", t("webhooks.saveError")),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteWebhook(id),
    onSuccess: () => {
      invalidate();
      notify("success", t("webhooks.deleted"));
    },
    onError: () => notify("error", t("webhooks.deleteError")),
    onSettled: () => setDeletingId(null),
  });

  const testMutation = useMutation({
    mutationFn: (id: string) => testUserWebhook(id),
    onSuccess: (result) => {
      invalidate();
      if (result.ok) {
        notify("success", t("webhooks.testOk", { status: result.status_code }));
      } else {
        notify("error", t("webhooks.testFailed", { error: result.error }));
      }
    },
    onError: () => notify("error", t("webhooks.testError")),
  });

  function openCreate() {
    setEditingId(null);
    setForm(emptyForm);
    setShowForm(true);
  }

  function openEdit(hook: UserWebhook) {
    setEditingId(hook.id);
    setForm({
      name: hook.name,
      url: hook.url,
      enabled: hook.enabled,
      insecureSkipVerify: hook.insecure_skip_verify,
      secret: "",
      authHeader: "",
      clearSecret: false,
      clearAuthHeader: false,
      events: hook.events ?? [],
      vendors: hook.vendors ?? [],
    });
    setShowForm(true);
  }

  function closeForm() {
    setShowForm(false);
    setEditingId(null);
    setForm(emptyForm);
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!form.name.trim() || !form.url.trim()) return;
    saveMutation.mutate(toInput(form, editingId !== null));
  }

  const deletingHook = hooks.find((h) => h.id === deletingId) ?? null;

  function toggleList(list: string[], value: string): string[] {
    return list.includes(value)
      ? list.filter((v) => v !== value)
      : [...list, value];
  }

  const editingHook = editingId
    ? hooks.find((h) => h.id === editingId)
    : undefined;
  const isHTTPS = form.url.trim().toLowerCase().startsWith("https://");

  return (
    <div className="max-w-4xl mx-auto p-6 space-y-6">
      {/* Page header */}
      <div className="flex items-center gap-3">
        <div className="w-9 h-9 rounded-lg bg-primary/10 flex items-center justify-center">
          <WebhookIcon className="w-5 h-5 text-primary" />
        </div>
        <div className="flex-1">
          <h1 className="text-lg font-semibold text-foreground">
            {t("webhooks.title")}
          </h1>
          <p className="text-xs text-muted-foreground">
            {t("webhooks.subtitle")}
          </p>
        </div>
        {!showForm && (
          <button
            onClick={openCreate}
            className="inline-flex items-center gap-1.5 px-4 py-2 rounded-md text-sm font-medium bg-primary text-primary-foreground hover:bg-primary/90 transition-colors"
          >
            <Plus className="w-4 h-4" />
            {t("webhooks.add")}
          </button>
        )}
      </div>

      {/* Create / edit form */}
      {showForm && (
        <form
          onSubmit={handleSubmit}
          className="rounded-lg border border-border bg-card p-4 space-y-4"
        >
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-semibold text-foreground">
              {editingId ? t("webhooks.editTitle") : t("webhooks.createTitle")}
            </h2>
            <button
              type="button"
              onClick={closeForm}
              className="p-1 rounded text-muted-foreground hover:text-foreground"
              aria-label={t("webhooks.cancel")}
            >
              <X className="w-4 h-4" />
            </button>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("webhooks.nameLabel")}
              </label>
              <input
                type="text"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                maxLength={100}
                placeholder={t("webhooks.namePlaceholder")}
                className="mt-1 w-full text-sm bg-background border border-border rounded px-3 py-2 focus:outline-none focus:ring-1 focus:ring-ring/20"
              />
            </div>
            <div>
              <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("webhooks.urlLabel")}
              </label>
              <input
                type="url"
                value={form.url}
                onChange={(e) => setForm({ ...form, url: e.target.value })}
                placeholder="https://receiver.example.com/hook"
                className="mt-1 w-full text-sm bg-background border border-border rounded px-3 py-2 font-mono focus:outline-none focus:ring-1 focus:ring-ring/20"
              />
            </div>
            <div>
              <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("webhooks.secretLabel")}
              </label>
              <input
                type="password"
                value={form.secret}
                disabled={form.clearSecret}
                onChange={(e) => setForm({ ...form, secret: e.target.value })}
                placeholder={
                  editingHook?.has_secret
                    ? t("webhooks.secretUnchanged")
                    : t("webhooks.secretPlaceholder")
                }
                className="mt-1 w-full text-sm bg-background border border-border rounded px-3 py-2 font-mono focus:outline-none focus:ring-1 focus:ring-ring/20 disabled:opacity-50"
              />
              {editingHook?.has_secret && (
                <label className="mt-1 flex items-center gap-1.5 text-[11px] text-muted-foreground">
                  <input
                    type="checkbox"
                    checked={form.clearSecret}
                    onChange={(e) =>
                      setForm({ ...form, clearSecret: e.target.checked })
                    }
                  />
                  {t("webhooks.clearSecret")}
                </label>
              )}
              <p className="mt-1 text-[11px] text-muted-foreground">
                {t("webhooks.secretHint")}
              </p>
            </div>
            <div>
              <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("webhooks.authHeaderLabel")}
              </label>
              <input
                type="password"
                value={form.authHeader}
                disabled={form.clearAuthHeader}
                onChange={(e) =>
                  setForm({ ...form, authHeader: e.target.value })
                }
                placeholder={
                  editingHook?.has_auth_header
                    ? t("webhooks.secretUnchanged")
                    : "Bearer eyJhbGci…"
                }
                className="mt-1 w-full text-sm bg-background border border-border rounded px-3 py-2 font-mono focus:outline-none focus:ring-1 focus:ring-ring/20 disabled:opacity-50"
              />
              {editingHook?.has_auth_header && (
                <label className="mt-1 flex items-center gap-1.5 text-[11px] text-muted-foreground">
                  <input
                    type="checkbox"
                    checked={form.clearAuthHeader}
                    onChange={(e) =>
                      setForm({ ...form, clearAuthHeader: e.target.checked })
                    }
                  />
                  {t("webhooks.clearAuthHeader")}
                </label>
              )}
              <p className="mt-1 text-[11px] text-muted-foreground">
                {t("webhooks.authHeaderHint")}
              </p>
            </div>
          </div>

          {/* Event filter */}
          <div>
            <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
              {t("webhooks.eventsLabel")}
            </label>
            <p className="text-[11px] text-muted-foreground mb-1.5">
              {t("webhooks.emptyMeansAll")}
            </p>
            <div className="flex flex-wrap gap-2">
              {(options?.events ?? []).map((event) => (
                <label
                  key={event}
                  className={`inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-md border text-xs cursor-pointer transition-colors ${
                    form.events.includes(event)
                      ? "border-primary bg-primary/10 text-foreground"
                      : "border-border bg-background text-muted-foreground hover:border-primary/40"
                  }`}
                >
                  <input
                    type="checkbox"
                    className="sr-only"
                    checked={form.events.includes(event)}
                    onChange={() =>
                      setForm({ ...form, events: toggleList(form.events, event) })
                    }
                  />
                  {t(`webhooks.eventTypes.${event}`, event)}
                </label>
              ))}
            </div>
          </div>

          {/* Vendor filter */}
          <div>
            <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
              {t("webhooks.vendorsLabel")}
            </label>
            <p className="text-[11px] text-muted-foreground mb-1.5">
              {t("webhooks.emptyMeansAll")}
            </p>
            <div className="flex flex-wrap gap-2">
              {(options?.vendors ?? []).map((vendor) => (
                <label
                  key={vendor.name}
                  className={`inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-md border text-xs cursor-pointer transition-colors ${
                    form.vendors.includes(vendor.name)
                      ? "border-primary bg-primary/10 text-foreground"
                      : "border-border bg-background text-muted-foreground hover:border-primary/40"
                  }`}
                >
                  <input
                    type="checkbox"
                    className="sr-only"
                    checked={form.vendors.includes(vendor.name)}
                    onChange={() =>
                      setForm({
                        ...form,
                        vendors: toggleList(form.vendors, vendor.name),
                      })
                    }
                  />
                  {vendor.name}
                  {vendor.extensions.length > 0 && (
                    <span className="text-[10px] opacity-60 font-mono">
                      {vendor.extensions.join(" ")}
                    </span>
                  )}
                </label>
              ))}
            </div>
          </div>

          {/* Toggles */}
          <div className="flex flex-wrap items-center gap-6">
            <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
              <input
                type="checkbox"
                checked={form.enabled}
                onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
              />
              {t("webhooks.enabledLabel")}
            </label>
            {isHTTPS && (
              <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
                <input
                  type="checkbox"
                  checked={form.insecureSkipVerify}
                  onChange={(e) =>
                    setForm({ ...form, insecureSkipVerify: e.target.checked })
                  }
                />
                <span className="inline-flex items-center gap-1">
                  <ShieldAlert className="w-3.5 h-3.5 text-amber-500" />
                  {t("webhooks.skipVerifyLabel")}
                </span>
              </label>
            )}
          </div>

          <div className="flex justify-end gap-2 pt-1">
            <button
              type="button"
              onClick={closeForm}
              className="px-4 py-2 rounded-md text-sm text-muted-foreground hover:text-foreground transition-colors"
            >
              {t("webhooks.cancel")}
            </button>
            <button
              type="submit"
              disabled={
                !form.name.trim() || !form.url.trim() || saveMutation.isPending
              }
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-md text-sm font-medium bg-primary text-primary-foreground hover:bg-primary/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {saveMutation.isPending && <Spinner size={14} />}
              {editingId ? t("webhooks.save") : t("webhooks.create")}
            </button>
          </div>
        </form>
      )}

      {/* Webhook list */}
      <div className="rounded-lg border border-border bg-card divide-y divide-border">
        {isLoading ? (
          <div className="p-8 flex justify-center">
            <Spinner size={20} className="text-primary" />
          </div>
        ) : hooks.length === 0 ? (
          <div className="p-8 text-center text-sm text-muted-foreground">
            {t("webhooks.empty")}
          </div>
        ) : (
          hooks.map((hook) => (
            <div key={hook.id}>
              <div className="px-4 py-3 flex items-center gap-3">
                <span
                  className={`w-2 h-2 rounded-full shrink-0 ${
                    hook.enabled ? "bg-emerald-500" : "bg-muted-foreground/40"
                  }`}
                  title={
                    hook.enabled
                      ? t("webhooks.enabledLabel")
                      : t("webhooks.disabled")
                  }
                />
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <p className="text-sm font-medium text-foreground truncate">
                      {hook.name}
                    </p>
                    {(hook.events ?? []).length > 0 && (
                      <span className="text-[10px] px-1.5 py-0.5 rounded bg-muted text-muted-foreground">
                        {(hook.events ?? []).length} {t("webhooks.eventsBadge")}
                      </span>
                    )}
                    {(hook.vendors ?? []).length > 0 && (
                      <span className="text-[10px] px-1.5 py-0.5 rounded bg-muted text-muted-foreground">
                        {(hook.vendors ?? []).join(", ")}
                      </span>
                    )}
                  </div>
                  <p className="text-xs text-muted-foreground font-mono truncate">
                    {hook.url}
                  </p>
                  {hook.last_delivered_at && (
                    <p className="text-[11px] text-muted-foreground inline-flex items-center gap-1 mt-0.5">
                      {hook.last_error === "" ? (
                        <CheckCircle2 className="w-3 h-3 text-emerald-500" />
                      ) : (
                        <XCircle className="w-3 h-3 text-destructive" />
                      )}
                      {t("webhooks.lastDelivery", {
                        date: new Date(hook.last_delivered_at).toLocaleString(),
                      })}
                      {hook.last_status_code > 0 && ` — HTTP ${hook.last_status_code}`}
                      {hook.last_error !== "" && ` — ${hook.last_error}`}
                    </p>
                  )}
                </div>
                <button
                  onClick={() =>
                    setHistoryId(historyId === hook.id ? null : hook.id)
                  }
                  className={`p-2 rounded-md hover:text-foreground hover:bg-muted transition-colors shrink-0 ${
                    historyId === hook.id
                      ? "text-foreground bg-muted"
                      : "text-muted-foreground"
                  }`}
                  title={t("webhooks.history")}
                  aria-label={t("webhooks.history")}
                >
                  <History className="w-4 h-4" />
                </button>
                <button
                  onClick={() => testMutation.mutate(hook.id)}
                  disabled={testMutation.isPending}
                  className="p-2 rounded-md text-muted-foreground hover:text-foreground hover:bg-muted transition-colors shrink-0"
                  title={t("webhooks.test")}
                  aria-label={t("webhooks.test")}
                >
                  <Send className="w-4 h-4" />
                </button>
                <button
                  onClick={() => openEdit(hook)}
                  className="p-2 rounded-md text-muted-foreground hover:text-foreground hover:bg-muted transition-colors shrink-0"
                  title={t("webhooks.edit")}
                  aria-label={t("webhooks.edit")}
                >
                  <Pencil className="w-4 h-4" />
                </button>
                <button
                  onClick={() => handleDelete(hook.id)}
                  disabled={deleteMutation.isPending}
                  className="p-2 rounded-md text-destructive hover:bg-destructive/5 transition-colors shrink-0"
                  title={t("webhooks.delete")}
                  aria-label={t("webhooks.delete")}
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>

              <button
                onClick={() => testMutation.mutate(hook.id)}
                disabled={testMutation.isPending}
                className="p-2 rounded-md text-muted-foreground hover:text-foreground hover:bg-muted transition-colors shrink-0"
                title={t("webhooks.test")}
                aria-label={t("webhooks.test")}
              >
                <Send className="w-4 h-4" />
              </button>
              <button
                onClick={() => openEdit(hook)}
                className="p-2 rounded-md text-muted-foreground hover:text-foreground hover:bg-muted transition-colors shrink-0"
                title={t("webhooks.edit")}
                aria-label={t("webhooks.edit")}
              >
                <Pencil className="w-4 h-4" />
              </button>
              <button
                onClick={() => setDeletingId(hook.id)}
                disabled={deleteMutation.isPending}
                className="p-2 rounded-md text-destructive hover:bg-destructive/5 transition-colors shrink-0"
                title={t("webhooks.delete")}
                aria-label={t("webhooks.delete")}
              >
                <Trash2 className="w-4 h-4" />
              </button>

              {historyId === hook.id && (
                <div className="bg-background/50 border-t border-border">
                  <WebhookDeliveries webhookId={hook.id} />
                </div>
              )}

            </div>
          ))
        )}
      </div>

      {/* Payload documentation hint */}
      <div className="rounded-lg border border-border bg-card p-4 space-y-2">
        <h2 className="text-sm font-semibold text-foreground">
          {t("webhooks.docTitle")}
        </h2>
        <p className="text-xs text-muted-foreground">{t("webhooks.docBody")}</p>
        <pre className="text-[11px] font-mono bg-background border border-border rounded p-3 overflow-x-auto">
{`POST <url>  ·  X-ECG-Hub-Event / X-ECG-Hub-Signature: sha256=<hmac> / X-ECG-Hub-Timestamp
{
  "event": "ecg.ingested",
  "webhook_id": "…",
  "timestamp": "2026-06-11T08:00:00Z",
  "data": { "ecg_id": "…", "patient_id": "…", "vendor": "mindray", "filename": "…" },
  "links": {
    "ecg_metadata": "…/api/v1/ecgs/<id>/metadata",
    "ecg_download": "…/api/v1/ecgs/<id>/download",
    "ecg_waveform": "…/api/v1/ecgs/<id>/waveform",
    "patient_ecgs": "…/api/v1/patients/<pid>/ecgs"
  }
}`}
        </pre>
        <p className="text-xs text-muted-foreground">{t("webhooks.docAuth")}</p>
      </div>

      <ConfirmDialog
        open={deletingId !== null}
        danger
        busy={deleteMutation.isPending}
        title={t("webhooks.delete")}
        message={
          deletingHook
            ? t("webhooks.confirmDeleteNamed", { name: deletingHook.name })
            : t("webhooks.confirmDelete")
        }
        confirmLabel={t("webhooks.delete")}
        onConfirm={() => deletingId && deleteMutation.mutate(deletingId)}
        onClose={() => setDeletingId(null)}
      />
    </div>
  );
}

import { useState, useCallback, useRef, useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  Activity,
  Clock,
  ChevronRight,
  ChevronDown,
  GripVertical,
  Trash2,
  Save,
  Search,
  Send,
  Server,
  Power,
  AlertTriangle,
} from "lucide-react";
import {
  fetchHL7Settings,
  updateHL7Settings,
  triggerHL7Run,
  pingHL7,
  bulkRetryHL7,
  testHL7Query,
  fetchHL7Presets,
  createHL7Preset,
  activateHL7Preset,
  deleteHL7Preset,
  saveHL7PresetMappings,
  type HL7TestResult,
  type HL7SegmentNode,
  type HL7Preset,
  type HL7Settings,
} from "../../lib/api";
import { Spinner } from "../ui/Spinner";
import { useNotification } from "../../context/NotificationContext";
import { useConfirm } from "../../context/ConfirmContext";

// ─── HL7 Connection Settings Form ───────────────────────────────────────────

function HL7ConnectionForm({
  settings,
  onSave,
  saving,
}: {
  settings: HL7Settings;
  onSave: (
    data: Partial<
      Pick<
        HL7Settings,
        | "host"
        | "port"
        | "sending_application"
        | "sending_facility"
        | "receiving_application"
        | "receiving_facility"
        | "version"
        | "processing_id"
      >
    >,
  ) => void;
  saving: boolean;
}) {
  const { t } = useTranslation();
  const [host, setHost] = useState(settings.host ?? "");
  const [port, setPort] = useState(settings.port ?? 2575);
  const [sendingApp, setSendingApp] = useState(
    settings.sending_application ?? "",
  );
  const [sendingFacility, setSendingFacility] = useState(
    settings.sending_facility ?? "",
  );
  const [receivingApp, setReceivingApp] = useState(
    settings.receiving_application ?? "",
  );
  const [receivingFacility, setReceivingFacility] = useState(
    settings.receiving_facility ?? "",
  );
  const [version, setVersion] = useState(settings.version ?? "2.5");
  const [processingId, setProcessingId] = useState(
    settings.processing_id ?? "P",
  );
  const [portError, setPortError] = useState("");

  const isDirty =
    host !== (settings.host ?? "") ||
    port !== (settings.port ?? 2575) ||
    sendingApp !== (settings.sending_application ?? "") ||
    sendingFacility !== (settings.sending_facility ?? "") ||
    receivingApp !== (settings.receiving_application ?? "") ||
    receivingFacility !== (settings.receiving_facility ?? "") ||
    version !== (settings.version ?? "2.5") ||
    processingId !== (settings.processing_id ?? "P");

  const handlePortChange = (v: string) => {
    const n = parseInt(v, 10);
    setPort(isNaN(n) ? 0 : n);
    if (isNaN(n) || n < 1 || n > 65535) {
      setPortError(
        t("admin.system.hl7.portError", "Port must be between 1 and 65535"),
      );
    } else {
      setPortError("");
    }
  };

  const handleSave = () => {
    if (portError) return;
    onSave({
      host,
      port,
      sending_application: sendingApp,
      sending_facility: sendingFacility,
      receiving_application: receivingApp,
      receiving_facility: receivingFacility,
      version,
      processing_id: processingId,
    });
  };

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        {/* Host */}
        <div className="lg:col-span-2">
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.host")}
          </label>
          <input
            type="text"
            value={host}
            onChange={(e) => setHost(e.target.value)}
            placeholder="his.hospital.local"
            className="mt-1 w-full text-xs font-mono border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
          />
        </div>

        {/* Port */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.port")}
          </label>
          <input
            type="number"
            min={1}
            max={65535}
            value={port}
            onChange={(e) => handlePortChange(e.target.value)}
            className={`mt-1 w-full text-xs border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20 ${portError ? "border-destructive" : "border-border"}`}
          />
          {portError && (
            <p className="text-[10px] text-destructive mt-0.5">{portError}</p>
          )}
        </div>

        {/* Version */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.version")}
          </label>
          <input
            type="text"
            value={version}
            onChange={(e) => setVersion(e.target.value)}
            placeholder="2.5"
            className="mt-1 w-full text-xs font-mono border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
          />
        </div>
      </div>

      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        {/* Sending Application */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.sendingApp")}
          </label>
          <input
            type="text"
            value={sendingApp}
            onChange={(e) => setSendingApp(e.target.value)}
            placeholder="ECG-HUB"
            className="mt-1 w-full text-xs font-mono border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
          />
        </div>

        {/* Sending Facility */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.sendingFacility")}
          </label>
          <input
            type="text"
            value={sendingFacility}
            onChange={(e) => setSendingFacility(e.target.value)}
            placeholder="CARDIO"
            className="mt-1 w-full text-xs font-mono border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
          />
        </div>

        {/* Receiving Application */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.receivingApp")}
          </label>
          <input
            type="text"
            value={receivingApp}
            onChange={(e) => setReceivingApp(e.target.value)}
            placeholder="HIS"
            className="mt-1 w-full text-xs font-mono border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
          />
        </div>

        {/* Receiving Facility */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.receivingFacility")}
          </label>
          <input
            type="text"
            value={receivingFacility}
            onChange={(e) => setReceivingFacility(e.target.value)}
            className="mt-1 w-full text-xs font-mono border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
          />
        </div>
      </div>

      {/* Processing ID */}
      <div className="flex items-start gap-4">
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.processingId")}
          </label>
          <select
            value={processingId}
            onChange={(e) => setProcessingId(e.target.value)}
            className="mt-1 text-xs border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
          >
            <option value="P">P — Production</option>
            <option value="T">T — Training</option>
            <option value="D">D — Debug</option>
          </select>
        </div>
      </div>

      {isDirty && (
        <div className="flex items-center gap-3">
          <button
            onClick={handleSave}
            disabled={saving || !!portError}
            className="inline-flex items-center gap-1.5 px-4 py-1.5 rounded-lg text-xs font-medium bg-primary text-primary-foreground hover:opacity-90 disabled:opacity-50 transition-opacity"
          >
            {saving && (
              <Spinner size={11} className="text-primary-foreground" />
            )}
            <Save className="w-3 h-3" />
            {t("admin.system.hl7.saveSettings")}
          </button>
          <span className="text-[11px] text-warning">
            {t("admin.system.hl7.restartNotice")}
          </span>
        </div>
      )}
    </div>
  );
}

// ─── HL7 Outbound ORU Form (result-sending) ─────────────────────────────────

function HL7ORUForm({
  settings,
  onSave,
  saving,
}: {
  settings: HL7Settings;
  onSave: (
    data: Partial<
      Pick<
        HL7Settings,
        | "oru_enabled"
        | "oru_trigger_mode"
        | "oru_host"
        | "oru_port"
        | "oru_include_pdf"
      >
    >,
  ) => void;
  saving: boolean;
}) {
  const { t } = useTranslation();
  const [enabled, setEnabled] = useState(settings.oru_enabled ?? false);
  const [triggerMode, setTriggerMode] = useState(
    settings.oru_trigger_mode ?? "manual",
  );
  const [host, setHost] = useState(settings.oru_host ?? "");
  const [port, setPort] = useState(settings.oru_port ?? 2575);
  const [includePdf, setIncludePdf] = useState(
    settings.oru_include_pdf ?? true,
  );
  const [portError, setPortError] = useState("");

  const isDirty =
    enabled !== (settings.oru_enabled ?? false) ||
    triggerMode !== (settings.oru_trigger_mode ?? "manual") ||
    host !== (settings.oru_host ?? "") ||
    port !== (settings.oru_port ?? 2575) ||
    includePdf !== (settings.oru_include_pdf ?? true);

  const handlePortChange = (v: string) => {
    const n = parseInt(v, 10);
    setPort(isNaN(n) ? 0 : n);
    if (isNaN(n) || n < 1 || n > 65535) {
      setPortError(
        t("admin.system.hl7.portError", "Port must be between 1 and 65535"),
      );
    } else {
      setPortError("");
    }
  };

  const handleSave = () => {
    if (portError) return;
    onSave({
      oru_enabled: enabled,
      oru_trigger_mode: triggerMode,
      oru_host: host,
      oru_port: port,
      oru_include_pdf: includePdf,
    });
  };

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        {/* Enabled toggle */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.oruEnabled", "Outbound ORU")}
          </label>
          <div className="mt-2">
            <button
              onClick={() => setEnabled((v) => !v)}
              className={`relative w-10 h-5 rounded-full transition-colors ${enabled ? "bg-primary" : "bg-muted-foreground/30"}`}
            >
              <span
                className={`absolute left-0.5 top-0.5 w-4 h-4 rounded-full bg-white shadow transition-transform ${enabled ? "translate-x-5" : "translate-x-0"}`}
              />
            </button>
          </div>
        </div>

        {/* Trigger mode */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.oruTriggerMode", "Trigger")}
          </label>
          <select
            value={triggerMode}
            onChange={(e) =>
              setTriggerMode(e.target.value as "auto" | "manual")
            }
            className="mt-1 w-full text-xs border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
          >
            <option value="manual">
              {t("admin.system.hl7.oruModeManual", "Manual (button)")}
            </option>
            <option value="auto">
              {t("admin.system.hl7.oruModeAuto", "Auto (on ingest)")}
            </option>
          </select>
        </div>

        {/* Include PDF toggle */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.oruIncludePdf", "Embed PDF (OBX/ED)")}
          </label>
          <div className="mt-2">
            <button
              onClick={() => setIncludePdf((v) => !v)}
              className={`relative w-10 h-5 rounded-full transition-colors ${includePdf ? "bg-primary" : "bg-muted-foreground/30"}`}
            >
              <span
                className={`absolute left-0.5 top-0.5 w-4 h-4 rounded-full bg-white shadow transition-transform ${includePdf ? "translate-x-5" : "translate-x-0"}`}
              />
            </button>
          </div>
        </div>
      </div>

      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        {/* ORU Host */}
        <div className="lg:col-span-2">
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.oruHost", "Result destination host")}
          </label>
          <input
            type="text"
            value={host}
            onChange={(e) => setHost(e.target.value)}
            placeholder="mirth.hospital.local"
            className="mt-1 w-full text-xs font-mono border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
          />
        </div>

        {/* ORU Port */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.oruPort", "Result destination port")}
          </label>
          <input
            type="number"
            min={1}
            max={65535}
            value={port}
            onChange={(e) => handlePortChange(e.target.value)}
            className={`mt-1 w-full text-xs border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20 ${portError ? "border-destructive" : "border-border"}`}
          />
          {portError && (
            <p className="text-[10px] text-destructive mt-0.5">{portError}</p>
          )}
        </div>
      </div>

      {isDirty && (
        <button
          onClick={handleSave}
          disabled={saving || !!portError}
          className="inline-flex items-center gap-1.5 px-4 py-1.5 rounded-lg text-xs font-medium bg-primary text-primary-foreground hover:opacity-90 disabled:opacity-50 transition-opacity"
        >
          {saving && <Spinner size={11} className="text-primary-foreground" />}
          <Save className="w-3 h-3" />
          {t("admin.system.hl7.saveSettings")}
        </button>
      )}
    </div>
  );
}

// ─── HL7 Scheduler Form (local state + save button) ─────────────────────────

function HL7SchedulerForm({
  settings,
  onSave,
  saving,
}: {
  settings: HL7Settings;
  onSave: (
    data: Partial<
      Pick<
        HL7Settings,
        | "trigger_mode"
        | "cron_expression"
        | "max_retries"
        | "timeout"
        | "enabled"
      >
    >,
  ) => void;
  saving: boolean;
}) {
  const { t } = useTranslation();
  const [triggerMode, setTriggerMode] = useState(settings.trigger_mode);
  const [cronExpr, setCronExpr] = useState(settings.cron_expression);
  const [maxRetries, setMaxRetries] = useState(settings.max_retries);
  const [timeout, setTimeout] = useState(settings.timeout);
  const [enabled, setEnabled] = useState(settings.enabled);

  const isDirty =
    triggerMode !== settings.trigger_mode ||
    cronExpr !== settings.cron_expression ||
    maxRetries !== settings.max_retries ||
    timeout !== settings.timeout ||
    enabled !== settings.enabled;

  return (
    <div className="space-y-4 mb-4">
      <div className="grid grid-cols-2 lg:grid-cols-5 gap-4">
        {/* Trigger mode */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.triggerMode")}
          </label>
          <select
            value={triggerMode}
            onChange={(e) =>
              setTriggerMode(e.target.value as "immediate" | "scheduled")
            }
            className="mt-1 w-full text-xs border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
          >
            <option value="immediate">
              {t("admin.system.hl7.modeImmediate")}
            </option>
            <option value="scheduled">
              {t("admin.system.hl7.modeScheduled")}
            </option>
          </select>
        </div>

        {/* Cron expression */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.cronExpr")}
          </label>
          <input
            type="text"
            value={cronExpr}
            onChange={(e) => setCronExpr(e.target.value)}
            className="mt-1 w-full text-xs font-mono border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
          />
        </div>

        {/* Max retries */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.maxRetries")}
          </label>
          <input
            type="number"
            min={1}
            value={maxRetries}
            onChange={(e) => setMaxRetries(parseInt(e.target.value) || 1)}
            className="mt-1 w-full text-xs border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
          />
        </div>

        {/* Timeout */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.timeout")}
          </label>
          <input
            type="text"
            value={timeout}
            onChange={(e) => setTimeout(e.target.value)}
            placeholder="10s"
            className="mt-1 w-full text-xs font-mono border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
          />
        </div>

        {/* Enabled toggle */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.enabledLabel")}
          </label>
          <div className="mt-2">
            <button
              onClick={() => setEnabled((v) => !v)}
              className={`relative w-10 h-5 rounded-full transition-colors ${enabled ? "bg-primary" : "bg-muted-foreground/30"}`}
            >
              <span
                className={`absolute left-0.5 top-0.5 w-4 h-4 rounded-full bg-white shadow transition-transform ${enabled ? "translate-x-5" : "translate-x-0"}`}
              />
            </button>
          </div>
        </div>
      </div>

      {isDirty && (
        <button
          onClick={() =>
            onSave({
              trigger_mode: triggerMode,
              cron_expression: cronExpr,
              max_retries: maxRetries,
              timeout,
              enabled,
            })
          }
          disabled={saving}
          className="inline-flex items-center gap-1.5 px-4 py-1.5 rounded-lg text-xs font-medium bg-primary text-primary-foreground hover:opacity-90 disabled:opacity-50 transition-opacity"
        >
          {saving && <Spinner size={11} className="text-primary-foreground" />}
          <Save className="w-3 h-3" />
          {t("admin.system.hl7.saveSettings")}
        </button>
      )}
    </div>
  );
}

// ─── HL7 Scheduler Settings Section ─────────────────────────────────────────

function HL7SchedulerSection() {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const confirm = useConfirm();
  const queryClient = useQueryClient();

  const { data: settings, isLoading } = useQuery({
    queryKey: ["admin", "hl7-settings"],
    queryFn: fetchHL7Settings,
    staleTime: 30_000,
    refetchInterval: 30_000,
  });

  const updateMutation = useMutation({
    mutationFn: (data: Parameters<typeof updateHL7Settings>[0]) =>
      updateHL7Settings(data),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["admin", "hl7-settings"],
      });
      notify("success", t("admin.system.hl7.settingsSaved"));
    },
    onError: () => notify("error", t("admin.system.hl7.settingsError")),
  });

  const runMutation = useMutation({
    mutationFn: triggerHL7Run,
    onSuccess: () => {
      notify("success", t("admin.system.hl7.runTriggered"));
      void queryClient.invalidateQueries({
        queryKey: ["admin", "hl7-settings"],
      });
    },
    onError: () => notify("error", t("admin.system.hl7.runError")),
  });

  const pingMutation = useMutation({
    mutationFn: pingHL7,
    onSuccess: (data) => {
      if (data.success) {
        notify(
          "success",
          t("admin.system.hl7.pingOk", { latency: data.latency }),
        );
      } else {
        notify("error", t("admin.system.hl7.pingFail", { error: data.error }));
      }
    },
    onError: () =>
      notify(
        "error",
        t("admin.system.hl7.pingFail", { error: "request failed" }),
      ),
  });

  const bulkRetryMutation = useMutation({
    mutationFn: bulkRetryHL7,
    onSuccess: (data) => {
      notify(
        "success",
        t("admin.system.hl7.bulkRetryOk", { count: data.count }),
      );
    },
    onError: () => notify("error", t("admin.system.hl7.bulkRetryError")),
  });

  if (isLoading || !settings) return null;

  return (
    <div className="bg-card rounded-lg border border-border p-5">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-3">
          <Clock className="w-5 h-5 text-muted-foreground" />
          <h2 className="text-sm font-semibold text-foreground">
            {t("admin.system.hl7.schedulerTitle")}
          </h2>
          <span
            className={`text-[10px] font-medium px-2 py-0.5 rounded-full ${
              settings.enabled
                ? "bg-success/10 text-success"
                : "bg-muted text-muted-foreground"
            }`}
          >
            {settings.enabled
              ? t("admin.system.hl7.schedulerEnabled")
              : t("admin.system.hl7.schedulerDisabled")}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => pingMutation.mutate()}
            disabled={pingMutation.isPending}
            className="inline-flex items-center gap-1.5 text-xs border border-border px-3 py-1.5 rounded-lg hover:bg-muted transition-colors disabled:opacity-50"
          >
            {pingMutation.isPending && <Spinner size={11} />}
            {t("admin.system.hl7.ping")}
          </button>
          <button
            onClick={() => runMutation.mutate()}
            disabled={runMutation.isPending || !settings.enabled}
            className="inline-flex items-center gap-1.5 text-xs border border-border px-3 py-1.5 rounded-lg hover:bg-muted transition-colors disabled:opacity-50"
          >
            {runMutation.isPending && <Spinner size={11} />}
            {t("admin.system.hl7.runNow")}
          </button>
          <button
            onClick={async () => {
              if (await confirm({
                title: t("admin.system.hl7.bulkRetry"),
                message: t("admin.system.hl7.bulkRetryConfirm"),
              }))
                bulkRetryMutation.mutate();
            }}
            disabled={bulkRetryMutation.isPending}
            className="inline-flex items-center gap-1.5 text-xs border border-destructive/30 text-destructive px-3 py-1.5 rounded-lg hover:bg-destructive/5 transition-colors disabled:opacity-50"
          >
            {bulkRetryMutation.isPending && <Spinner size={11} />}
            {t("admin.system.hl7.bulkRetry")}
          </button>
        </div>
      </div>

      <HL7SchedulerForm
        settings={settings}
        onSave={(data) => updateMutation.mutate(data)}
        saving={updateMutation.isPending}
      />

      {/* Status row */}
      <div className="flex items-center gap-6 text-[11px] text-muted-foreground border-t border-border pt-3">
        {settings.last_run && (
          <span>
            {t("admin.system.hl7.lastRunLabel")}:{" "}
            <span className="font-mono text-foreground">
              {new Date(settings.last_run).toLocaleString("fr-FR")}
            </span>
          </span>
        )}
        {settings.next_run && (
          <span>
            {t("admin.system.hl7.nextRunLabel")}:{" "}
            <span className="font-mono text-foreground">
              {new Date(settings.next_run).toLocaleString("fr-FR")}
            </span>
          </span>
        )}
      </div>
    </div>
  );
}

// ─── HL7 Tree Node (collapsible) ────────────────────────────────────────────

function HL7TreeNode({
  segment,
  onDrag,
}: {
  segment: HL7SegmentNode;
  onDrag: (path: string, value: string) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="select-none">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1.5 py-1 px-1 w-full text-left hover:bg-muted/40 rounded transition-colors"
      >
        {open ? (
          <ChevronDown className="w-3 h-3 text-muted-foreground" />
        ) : (
          <ChevronRight className="w-3 h-3 text-muted-foreground" />
        )}
        <span className="text-xs font-semibold text-primary">
          {segment.name}
        </span>
      </button>
      {open && (
        <div className="ml-4 border-l border-border pl-2 space-y-0.5">
          {segment.fields.map((field) => (
            <HL7FieldItem key={field.path} field={field} onDrag={onDrag} />
          ))}
        </div>
      )}
    </div>
  );
}

function HL7FieldItem({
  field,
  onDrag,
}: {
  field: {
    path: string;
    value: string;
    components?: { path: string; value: string }[];
  };
  onDrag: (path: string, value: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const hasComponents = field.components && field.components.length > 0;

  return (
    <div>
      <div className="flex items-center gap-1 group">
        {hasComponents ? (
          <button onClick={() => setOpen((v) => !v)} className="p-0.5">
            {open ? (
              <ChevronDown className="w-2.5 h-2.5 text-muted-foreground" />
            ) : (
              <ChevronRight className="w-2.5 h-2.5 text-muted-foreground" />
            )}
          </button>
        ) : (
          <span className="w-3.5" />
        )}
        <div
          draggable
          onDragStart={(e) => {
            e.dataTransfer.setData(
              "text/plain",
              JSON.stringify({ path: field.path, value: field.value }),
            );
            onDrag(field.path, field.value);
          }}
          className="flex items-center gap-2 flex-1 px-1.5 py-0.5 rounded cursor-grab hover:bg-primary/5 active:cursor-grabbing transition-colors"
        >
          <GripVertical className="w-2.5 h-2.5 text-muted-foreground/50 opacity-0 group-hover:opacity-100 transition-opacity" />
          <span className="text-[10px] font-mono text-muted-foreground w-16 shrink-0">
            {field.path}
          </span>
          <span className="text-xs font-mono text-foreground truncate">
            {field.value || "—"}
          </span>
        </div>
      </div>
      {open && hasComponents && (
        <div className="ml-6 border-l border-border/50 pl-2 space-y-0.5">
          {field.components!.map((comp) => (
            <div
              key={comp.path}
              draggable
              onDragStart={(e) => {
                e.dataTransfer.setData(
                  "text/plain",
                  JSON.stringify({ path: comp.path, value: comp.value }),
                );
                onDrag(comp.path, comp.value);
              }}
              className="flex items-center gap-2 px-1.5 py-0.5 rounded cursor-grab hover:bg-primary/5 active:cursor-grabbing group transition-colors"
            >
              <GripVertical className="w-2.5 h-2.5 text-muted-foreground/50 opacity-0 group-hover:opacity-100 transition-opacity" />
              <span className="text-[10px] font-mono text-muted-foreground w-16 shrink-0">
                {comp.path}
              </span>
              <span className="text-xs font-mono text-foreground truncate">
                {comp.value || "—"}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// ─── HL7 Mapping Drop Zone ──────────────────────────────────────────────────

const TARGET_FIELDS = [
  "last_name",
  "first_name",
  "date_of_birth",
  "gender",
  "nip",
  "address",
  "phone",
  "error_code",
  "error_message",
] as const;

function HL7MappingZone({
  mappings,
  onDrop,
  onRemove,
  onSave,
  onUpdate,
  saving,
  presets,
  activePresetId,
  onSelectPreset,
  onDeletePreset,
}: {
  mappings: { source_path: string; target_field: string }[];
  onDrop: (targetField: string, sourcePath: string) => void;
  onRemove: (targetField: string) => void;
  onSave: () => void;
  onUpdate: () => void;
  saving: boolean;
  presets: HL7Preset[];
  activePresetId: string | null;
  onSelectPreset: (id: string) => void;
  onDeletePreset: (id: string) => void;
}) {
  const { t } = useTranslation();
  const [dragOver, setDragOver] = useState<string | null>(null);

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between mb-2">
        <span className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          {t("admin.system.hl7.mappingTitle")}
        </span>
        <div className="flex items-center gap-2">
          {activePresetId && (
            <button
              onClick={onUpdate}
              disabled={saving}
              className="inline-flex items-center gap-1 text-[11px] font-medium text-primary hover:text-primary/80 disabled:opacity-50 transition-colors"
            >
              {saving ? <Spinner size={10} /> : <Save className="w-3 h-3" />}
              {t("admin.system.hl7.updateMapping")}
            </button>
          )}
          <button
            onClick={onSave}
            disabled={saving}
            className="inline-flex items-center gap-1 text-[11px] font-medium text-muted-foreground hover:text-foreground disabled:opacity-50 transition-colors"
          >
            + {t("admin.system.hl7.saveAsNew")}
          </button>
        </div>
      </div>

      {/* Preset selector */}
      {presets.length > 0 && (
        <div className="flex flex-wrap gap-1.5 mb-2">
          {presets.map((p) => (
            <div key={p.id} className="inline-flex items-center gap-1">
              <button
                onClick={() => onSelectPreset(p.id)}
                className={`text-[10px] font-medium px-2 py-1 rounded-md border transition-colors ${
                  activePresetId === p.id
                    ? "bg-primary/10 text-primary border-primary/30"
                    : "text-muted-foreground border-border hover:border-primary/20"
                }`}
              >
                {p.name}
              </button>
              <button
                onClick={() => onDeletePreset(p.id)}
                className="p-0.5 text-muted-foreground/40 hover:text-destructive transition-colors"
              >
                <Trash2 className="w-2.5 h-2.5" />
              </button>
            </div>
          ))}
        </div>
      )}

      {TARGET_FIELDS.map((field) => {
        const mapping = mappings.find((m) => m.target_field === field);
        const isOver = dragOver === field;
        return (
          <div
            key={field}
            onDragOver={(e) => {
              e.preventDefault();
              setDragOver(field);
            }}
            onDragLeave={() => setDragOver(null)}
            onDrop={(e) => {
              e.preventDefault();
              setDragOver(null);
              try {
                const data = JSON.parse(e.dataTransfer.getData("text/plain"));
                onDrop(field, data.path);
              } catch {
                /* ignore */
              }
            }}
            className={`flex items-center gap-2 px-3 py-2 rounded-lg border transition-colors ${
              isOver
                ? "border-primary bg-primary/5"
                : "border-border bg-muted/20"
            }`}
          >
            <span className="text-[11px] font-medium text-muted-foreground w-48">
              {t(`admin.system.hl7.field.${field}`)}
            </span>
            {mapping ? (
              <div className="flex items-center gap-2 flex-1">
                <span className="text-[10px] font-mono bg-primary/10 text-primary px-1.5 py-0.5 rounded">
                  {mapping.source_path}
                </span>
                <button
                  onClick={() => onRemove(field)}
                  className="ml-auto p-0.5 text-muted-foreground hover:text-destructive transition-colors"
                >
                  <Trash2 className="w-3 h-3" />
                </button>
              </div>
            ) : (
              <span className="text-[10px] text-muted-foreground/50 italic">
                {t("admin.system.hl7.dropHere")}
              </span>
            )}
          </div>
        );
      })}
    </div>
  );
}

// ─── HL7 Test Section ────────────────────────────────────────────────────────

function HL7TestSection() {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const [patientId, setPatientId] = useState("");
  const [result, setResult] = useState<HL7TestResult | null>(null);
  const [tab, setTab] = useState<"response" | "tree">("response");
  const [localMappings, setLocalMappings] = useState<
    { source_path: string; target_field: string }[]
  >([]);
  const [showNamePopup, setShowNamePopup] = useState(false);
  const [presetName, setPresetName] = useState("");
  const [activePresetId, setActivePresetId] = useState<string | null>(null);

  const { data: presets = [] } = useQuery({
    queryKey: ["admin", "hl7-presets"],
    queryFn: fetchHL7Presets,
    staleTime: 60_000,
  });

  // Load active preset into local state on first load
  const initRef = useRef(false);
  useEffect(() => {
    if (initRef.current || presets.length === 0) return;
    const active = presets.find((p) => p.active);
    if (active) {
      initRef.current = true;
      setActivePresetId(active.id);
      setLocalMappings(
        active.mappings?.map((m) => ({
          source_path: m.source_path,
          target_field: m.target_field,
        })) ?? [],
      );
    }
  }, [presets]);

  const mutation = useMutation({
    mutationFn: (pid: string) => testHL7Query(pid),
    onSuccess: (data) => {
      setResult(data);
      if (data.tree) setTab("tree");
    },
    onError: () =>
      setResult({
        success: false,
        patient_id: patientId,
        duration: "—",
        error: "Request failed",
      }),
  });

  const createPresetMutation = useMutation({
    mutationFn: (name: string) => createHL7Preset(name, localMappings),
    onSuccess: (preset) => {
      void queryClient.invalidateQueries({
        queryKey: ["admin", "hl7-presets"],
      });
      setActivePresetId(preset.id);
      setShowNamePopup(false);
      setPresetName("");
      void activateHL7Preset(preset.id);
      notify("success", t("admin.system.hl7.mappingSaved"));
    },
    onError: () => notify("error", t("admin.system.hl7.mappingError")),
  });

  const updatePresetMutation = useMutation({
    mutationFn: (presetId: string) =>
      saveHL7PresetMappings(presetId, localMappings),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["admin", "hl7-presets"],
      });
      notify("success", t("admin.system.hl7.mappingSaved"));
    },
    onError: () => notify("error", t("admin.system.hl7.mappingError")),
  });

  const deletePresetMutation = useMutation({
    mutationFn: (id: string) => deleteHL7Preset(id),
    onSuccess: (_, id) => {
      void queryClient.invalidateQueries({
        queryKey: ["admin", "hl7-presets"],
      });
      if (activePresetId === id) {
        setActivePresetId(null);
        setLocalMappings([]);
      }
      notify("success", t("admin.system.hl7.presetDeleted"));
    },
  });

  const handleSave = useCallback(() => {
    setShowNamePopup(true);
  }, []);

  const handleUpdate = useCallback(() => {
    if (activePresetId) {
      updatePresetMutation.mutate(activePresetId);
    }
  }, [activePresetId, updatePresetMutation]);

  const handleSelectPreset = useCallback(
    (id: string) => {
      const preset = presets.find((p) => p.id === id);
      if (preset) {
        setActivePresetId(id);
        setLocalMappings(
          preset.mappings?.map((m) => ({
            source_path: m.source_path,
            target_field: m.target_field,
          })) ?? [],
        );
        void activateHL7Preset(id);
      }
    },
    [presets],
  );

  const handleDeletePreset = useCallback(
    async (id: string) => {
      if (
        await confirm({
          title: t("common.delete"),
          message: t("admin.system.hl7.deletePresetConfirm"),
          danger: true,
        })
      ) {
        deletePresetMutation.mutate(id);
      }
    },
    [deletePresetMutation, t, confirm],
  );

  const handleDrop = useCallback((targetField: string, sourcePath: string) => {
    setLocalMappings((prev) => {
      const filtered = prev.filter((m) => m.target_field !== targetField);
      return [
        ...filtered,
        { source_path: sourcePath, target_field: targetField },
      ];
    });
  }, []);

  const handleRemove = useCallback((targetField: string) => {
    setLocalMappings((prev) =>
      prev.filter((m) => m.target_field !== targetField),
    );
  }, []);

  const handleDrag = useCallback((_path: string, _value: string) => {}, []);

  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
      {/* Left: input + mapping zone */}
      <div className="bg-card rounded-lg border border-border p-5 space-y-5">
        <div>
          <div className="flex items-center gap-3 mb-4">
            <Activity className="w-5 h-5 text-muted-foreground" />
            <h2 className="text-sm font-semibold text-foreground">
              {t("admin.system.hl7.title")}
            </h2>
          </div>
          <p className="text-xs text-muted-foreground mb-4">
            {t("admin.system.hl7.description")}
          </p>
          <div className="flex gap-2">
            <div className="relative flex-1">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
              <input
                type="text"
                value={patientId}
                onChange={(e) => setPatientId(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && patientId.trim())
                    mutation.mutate(patientId.trim());
                }}
                placeholder={t("admin.system.hl7.placeholder")}
                className="w-full pl-9 pr-3 py-2 text-sm bg-muted/50 border border-border rounded-lg focus:outline-none focus:ring-2 focus:ring-ring/20 font-mono"
              />
            </div>
            <button
              onClick={() => mutation.mutate(patientId.trim())}
              disabled={!patientId.trim() || mutation.isPending}
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-lg text-sm font-medium bg-primary text-primary-foreground hover:opacity-90 disabled:opacity-50 transition-opacity"
            >
              {mutation.isPending ? (
                <Spinner size={12} className="text-primary-foreground" />
              ) : (
                <Send className="w-3.5 h-3.5" />
              )}
              {t("admin.system.hl7.send")}
            </button>
          </div>
        </div>

        {/* Mapping drop zone */}
        <HL7MappingZone
          mappings={localMappings}
          onDrop={handleDrop}
          onRemove={handleRemove}
          onSave={handleSave}
          onUpdate={handleUpdate}
          saving={
            createPresetMutation.isPending || updatePresetMutation.isPending
          }
          presets={presets}
          activePresetId={activePresetId}
          onSelectPreset={handleSelectPreset}
          onDeletePreset={handleDeletePreset}
        />

        {/* Save name popup */}
        {showNamePopup && (
          <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
            <div className="bg-card rounded-lg border border-border p-5 w-80 shadow-xl">
              <h3 className="text-sm font-semibold text-foreground mb-3">
                {t("admin.system.hl7.presetNameTitle")}
              </h3>
              <input
                autoFocus
                value={presetName}
                onChange={(e) => setPresetName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && presetName.trim())
                    createPresetMutation.mutate(presetName.trim());
                }}
                placeholder={t("admin.system.hl7.presetNamePlaceholder")}
                className="w-full px-3 py-2 text-sm border border-border rounded-lg bg-background focus:outline-none focus:ring-2 focus:ring-ring/20 mb-3"
              />
              <div className="flex gap-2 justify-end">
                <button
                  onClick={() => {
                    setShowNamePopup(false);
                    setPresetName("");
                  }}
                  className="text-xs text-muted-foreground hover:text-foreground px-3 py-1.5 transition-colors"
                >
                  {t("common.cancel")}
                </button>
                <button
                  onClick={() => createPresetMutation.mutate(presetName.trim())}
                  disabled={
                    !presetName.trim() || createPresetMutation.isPending
                  }
                  className="text-xs font-medium bg-primary text-primary-foreground px-3 py-1.5 rounded-lg hover:opacity-90 disabled:opacity-50 transition-opacity"
                >
                  {createPresetMutation.isPending ? (
                    <Spinner size={10} className="text-primary-foreground" />
                  ) : (
                    t("common.confirm")
                  )}
                </button>
              </div>
            </div>
          </div>
        )}
      </div>

      {/* Right: response with tabs */}
      <div className="bg-card rounded-lg border border-border p-5 flex flex-col">
        {/* Tab header */}
        <div className="flex items-center gap-1 mb-4 border-b border-border pb-2">
          <button
            onClick={() => setTab("response")}
            className={`px-3 py-1.5 text-xs font-medium rounded-md transition-colors ${
              tab === "response"
                ? "bg-primary/10 text-primary"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t("admin.system.hl7.response")}
          </button>
          <button
            onClick={() => setTab("tree")}
            disabled={!result?.tree}
            className={`px-3 py-1.5 text-xs font-medium rounded-md transition-colors disabled:opacity-30 ${
              tab === "tree"
                ? "bg-primary/10 text-primary"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t("admin.system.hl7.treeTab")}
          </button>
          {result && (
            <>
              <span
                className={`ml-2 text-[10px] font-medium px-2 py-0.5 rounded-full ${
                  result.success
                    ? "bg-success/10 text-success"
                    : "bg-destructive/10 text-destructive"
                }`}
              >
                {result.success ? "OK" : "ERROR"}
              </span>
              <span className="text-[10px] font-mono text-muted-foreground ml-auto">
                {result.duration}
              </span>
            </>
          )}
        </div>

        {/* Tab content */}
        <div className="flex-1 min-h-0 overflow-y-auto">
          {!result && !mutation.isPending && (
            <p className="text-xs text-muted-foreground text-center py-6">
              {t("admin.system.hl7.noResult")}
            </p>
          )}

          {mutation.isPending && (
            <div className="flex justify-center py-6">
              <Spinner size={18} className="text-muted-foreground" />
            </div>
          )}

          {result && !mutation.isPending && tab === "response" && (
            <div className="space-y-3">
              {result.error && (
                <div className="bg-destructive/5 border border-destructive/20 rounded-lg p-3">
                  <p className="text-xs font-mono text-destructive break-all">
                    {result.error}
                  </p>
                </div>
              )}
              {result.demographics && (
                <div className="space-y-1.5">
                  {(
                    [
                      ["last_name", result.demographics.last_name],
                      ["first_name", result.demographics.first_name],
                      ["date_of_birth", result.demographics.date_of_birth],
                      ["gender", result.demographics.gender],
                      ["source", result.demographics.source],
                    ] as const
                  ).map(([key, value]) => (
                    <div
                      key={key}
                      className="flex items-center gap-3 px-3 py-2 rounded-lg bg-muted/30"
                    >
                      <span className="text-[11px] font-medium text-muted-foreground w-24 capitalize">
                        {t(`admin.system.hl7.field.${key}`)}
                      </span>
                      <span className="text-sm font-mono text-foreground">
                        {value || "—"}
                      </span>
                    </div>
                  ))}
                </div>
              )}
              {result.raw && (
                <div className="mt-3">
                  <span className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                    {t("admin.system.hl7.rawData")}
                  </span>
                  <pre className="mt-1.5 p-3 rounded-lg bg-muted/40 border border-border text-[11px] font-mono text-foreground overflow-x-auto whitespace-pre-wrap break-all leading-relaxed">
                    {result.raw.replace(/\r/g, "\n")}
                  </pre>
                </div>
              )}
            </div>
          )}

          {result && !mutation.isPending && tab === "tree" && result.tree && (
            <div className="space-y-1">
              {result.tree.map((seg) => (
                <HL7TreeNode key={seg.name} segment={seg} onDrag={handleDrag} />
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// ─── HL7 Connection Settings Section ─────────────────────────────────────────

function HL7ConnectionSection() {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const queryClient = useQueryClient();

  const { data: settings, isLoading } = useQuery({
    queryKey: ["admin", "hl7-settings"],
    queryFn: fetchHL7Settings,
    staleTime: 30_000,
  });

  const updateMutation = useMutation({
    mutationFn: (data: Parameters<typeof updateHL7Settings>[0]) =>
      updateHL7Settings(data),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["admin", "hl7-settings"],
      });
      notify("success", t("admin.system.hl7.settingsSaved"));
    },
    onError: () => notify("error", t("admin.system.hl7.settingsError")),
  });

  if (isLoading || !settings) return null;

  return (
    <div className="bg-card rounded-lg border border-border p-5">
      <div className="flex items-center gap-3 mb-4">
        <Server className="w-5 h-5 text-muted-foreground" />
        <h2 className="text-sm font-semibold text-foreground">
          {t("admin.system.hl7.connectionTitle")}
        </h2>
      </div>
      <HL7ConnectionForm
        settings={settings}
        onSave={(data) => updateMutation.mutate(data)}
        saving={updateMutation.isPending}
      />
    </div>
  );
}

// ─── HL7 Outbound ORU Settings Section ──────────────────────────────────────

function HL7ORUSection() {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const queryClient = useQueryClient();

  const { data: settings, isLoading } = useQuery({
    queryKey: ["admin", "hl7-settings"],
    queryFn: fetchHL7Settings,
    staleTime: 30_000,
  });

  const updateMutation = useMutation({
    mutationFn: (data: Parameters<typeof updateHL7Settings>[0]) =>
      updateHL7Settings(data),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["admin", "hl7-settings"],
      });
      notify("success", t("admin.system.hl7.settingsSaved"));
    },
    onError: () => notify("error", t("admin.system.hl7.settingsError")),
  });

  if (isLoading || !settings) return null;

  return (
    <div className="bg-card rounded-lg border border-border p-5">
      <div className="flex items-center gap-3 mb-1">
        <Send className="w-5 h-5 text-muted-foreground" />
        <h2 className="text-sm font-semibold text-foreground">
          {t("admin.system.hl7.oruTitle", "Outbound results (ORU)")}
        </h2>
      </div>
      <p className="text-xs text-muted-foreground mb-4">
        {t(
          "admin.system.hl7.oruSubtitle",
          "Push the ECG result (optionally with the PDF report) to the HIS/DPI. Manual sends require the ecg.send_result permission.",
        )}
      </p>
      <HL7ORUForm
        settings={settings}
        onSave={(data) => updateMutation.mutate(data)}
        saving={updateMutation.isPending}
      />
    </div>
  );
}

// ─── HL7 Master Switch Section ──────────────────────────────────────────────
// Global on/off for the HL7 integration at this site. Enabled by default so the
// "not configured" reminder surfaces for the DSI; a facility without HL7 can turn
// it off to silence the reminder and disable every HL7 flow.

function HL7MasterSwitchSection() {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const queryClient = useQueryClient();

  const { data: settings, isLoading } = useQuery({
    queryKey: ["admin", "hl7-settings"],
    queryFn: fetchHL7Settings,
    staleTime: 30_000,
  });

  const updateMutation = useMutation({
    mutationFn: (data: Parameters<typeof updateHL7Settings>[0]) =>
      updateHL7Settings(data),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["admin", "hl7-settings"],
      });
      notify("success", t("admin.system.hl7.settingsSaved"));
    },
    onError: () => notify("error", t("admin.system.hl7.settingsError")),
  });

  const hl7Enabled = settings?.hl7_enabled ?? true;
  const notConfigured = !!settings && !settings.host?.trim();

  // Fire the reminder popup once per mount when HL7 is enabled but not configured,
  // so the DSI is prompted to set it up (or disable HL7 if the site has none).
  const warnedRef = useRef(false);
  useEffect(() => {
    if (!settings || warnedRef.current) return;
    if (hl7Enabled && notConfigured) {
      warnedRef.current = true;
      notify("warn", t("admin.system.hl7.notConfiguredPopup"));
    }
  }, [settings, hl7Enabled, notConfigured, notify, t]);

  if (isLoading || !settings) return null;

  return (
    <div className="bg-card rounded-lg border border-border p-5">
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-start gap-3">
          <Power
            className={`w-5 h-5 mt-0.5 ${hl7Enabled ? "text-success" : "text-muted-foreground"}`}
          />
          <div>
            <h2 className="text-sm font-semibold text-foreground">
              {t("admin.system.hl7.masterTitle")}
            </h2>
            <p className="text-xs text-muted-foreground mt-0.5 max-w-2xl">
              {t("admin.system.hl7.masterSubtitle")}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <span className="text-[11px] font-medium text-muted-foreground">
            {hl7Enabled
              ? t("admin.system.hl7.masterEnabled")
              : t("admin.system.hl7.masterDisabled")}
          </span>
          <button
            onClick={() => updateMutation.mutate({ hl7_enabled: !hl7Enabled })}
            disabled={updateMutation.isPending}
            className={`relative w-10 h-5 rounded-full transition-colors disabled:opacity-50 ${hl7Enabled ? "bg-primary" : "bg-muted-foreground/30"}`}
            aria-label={t("admin.system.hl7.masterTitle")}
          >
            <span
              className={`absolute left-0.5 top-0.5 w-4 h-4 rounded-full bg-white shadow transition-transform ${hl7Enabled ? "translate-x-5" : "translate-x-0"}`}
            />
          </button>
        </div>
      </div>

      {/* Reminder banner: enabled but no connection configured yet */}
      {hl7Enabled && notConfigured && (
        <div className="mt-4 flex items-start gap-2.5 rounded-lg border border-warning/30 bg-warning/5 px-3 py-2.5">
          <AlertTriangle className="w-4 h-4 text-warning mt-0.5 shrink-0" />
          <p className="text-xs text-warning-foreground/90">
            {t("admin.system.hl7.notConfiguredBanner")}
          </p>
        </div>
      )}
    </div>
  );
}

// ─── AdminHL7Page ─────────────────────────────────────────────────────────────

export function AdminHL7Page() {
  const { t } = useTranslation();

  return (
    <div className="p-6 space-y-6">
      {/* Header */}
      <div className="flex items-center gap-3">
        <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-primary/10 text-primary ring-1 ring-primary/20">
          <Send className="h-5 w-5" />
        </div>
        <div>
          <h1 className="font-display text-2xl font-semibold tracking-tight text-foreground">
            {t("nav.hl7")}
          </h1>
        </div>
      </div>

      {/* 0. Global HL7 master switch */}
      <HL7MasterSwitchSection />

      {/* 1. Connection Settings */}
      <HL7ConnectionSection />

      {/* 2. Outbound results (ORU) */}
      <HL7ORUSection />

      {/* 3. Scheduler */}
      <HL7SchedulerSection />

      {/* 4. Query Test + Field Mapping */}
      <HL7TestSection />
    </div>
  );
}

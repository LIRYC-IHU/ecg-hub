import { useState, useCallback, useRef, useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  CheckCircle,
  AlertCircle,
  Webhook,
  Copy,
  Lock,
  Database,
  Server,
  Activity,
  Clock,
  AlertTriangle,
  FileType,
  Radio,
  HardDrive,
  Send,
  Search,
  ChevronRight,
  ChevronDown,
  GripVertical,
  Trash2,
  Save,
} from "lucide-react";
import { useAdminStats } from "../../hooks/useAdminStats";
import {
  fetchWebhookStatus,
  fetchModules,
  fetchConnectors,
  testWebhook,
  testHL7Query,
  fetchHL7Presets,
  createHL7Preset,
  activateHL7Preset,
  deleteHL7Preset,
  saveHL7PresetMappings,
  fetchHL7Settings,
  updateHL7Settings,
  triggerHL7Run,
  type HL7TestResult,
  type HL7SegmentNode,
  type HL7Preset,
  type HL7Settings,
} from "../../lib/api";
import { Spinner } from "../ui/Spinner";
import { useNotification } from "../../context/NotificationContext";
import { useAuth } from "../../hooks/useAuth";
import MetricCard from "./Metric";

// ─── HL7 Scheduler Form (local state + save button) ─────────────────────────

function HL7SchedulerForm({ settings, onSave, saving }: {
  settings: HL7Settings;
  onSave: (data: Partial<Pick<HL7Settings, "trigger_mode" | "cron_expression" | "max_retries" | "enabled">>) => void;
  saving: boolean;
}) {
  const { t } = useTranslation();
  const [triggerMode, setTriggerMode] = useState(settings.trigger_mode);
  const [cronExpr, setCronExpr] = useState(settings.cron_expression);
  const [maxRetries, setMaxRetries] = useState(settings.max_retries);
  const [enabled, setEnabled] = useState(settings.enabled);

  const isDirty = triggerMode !== settings.trigger_mode || cronExpr !== settings.cron_expression || maxRetries !== settings.max_retries || enabled !== settings.enabled;

  return (
    <div className="space-y-4 mb-4">
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        {/* Trigger mode */}
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.system.hl7.triggerMode")}
          </label>
          <select
            value={triggerMode}
            onChange={(e) => setTriggerMode(e.target.value as "immediate" | "scheduled")}
            className="mt-1 w-full text-xs border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
          >
            <option value="immediate">{t("admin.system.hl7.modeImmediate")}</option>
            <option value="scheduled">{t("admin.system.hl7.modeScheduled")}</option>
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
              <span className={`absolute left-0.5 top-0.5 w-4 h-4 rounded-full bg-white shadow transition-transform ${enabled ? "translate-x-5" : "translate-x-0"}`} />
            </button>
          </div>
        </div>
      </div>

      {isDirty && (
        <button
          onClick={() => onSave({ trigger_mode: triggerMode, cron_expression: cronExpr, max_retries: maxRetries, enabled })}
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

// ─── HL7 Scheduler Settings ─────────────────────────────────────────────────

function HL7SchedulerSection() {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const queryClient = useQueryClient();

  const { data: settings, isLoading } = useQuery({
    queryKey: ["admin", "hl7-settings"],
    queryFn: fetchHL7Settings,
    staleTime: 30_000,
    refetchInterval: 30_000,
  });

  const updateMutation = useMutation({
    mutationFn: (data: Parameters<typeof updateHL7Settings>[0]) => updateHL7Settings(data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "hl7-settings"] });
      notify("success", t("admin.system.hl7.settingsSaved"));
    },
    onError: () => notify("error", t("admin.system.hl7.settingsError")),
  });

  const runMutation = useMutation({
    mutationFn: triggerHL7Run,
    onSuccess: () => {
      notify("success", t("admin.system.hl7.runTriggered"));
      void queryClient.invalidateQueries({ queryKey: ["admin", "hl7-settings"] });
    },
    onError: () => notify("error", t("admin.system.hl7.runError")),
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
          <span className={`text-[10px] font-medium px-2 py-0.5 rounded-full ${
            settings.enabled ? "bg-success/10 text-success" : "bg-muted text-muted-foreground"
          }`}>
            {settings.enabled ? t("admin.system.hl7.schedulerEnabled") : t("admin.system.hl7.schedulerDisabled")}
          </span>
        </div>
        <button
          onClick={() => runMutation.mutate()}
          disabled={runMutation.isPending || !settings.enabled}
          className="inline-flex items-center gap-1.5 text-xs border border-border px-3 py-1.5 rounded-lg hover:bg-muted transition-colors disabled:opacity-50"
        >
          {runMutation.isPending && <Spinner size={11} />}
          {t("admin.system.hl7.runNow")}
        </button>
      </div>

      <HL7SchedulerForm settings={settings} onSave={(data) => updateMutation.mutate(data)} saving={updateMutation.isPending} />

      {/* Status row */}
      <div className="flex items-center gap-6 text-[11px] text-muted-foreground border-t border-border pt-3">
        {settings.last_run && (
          <span>
            {t("admin.system.hl7.lastRunLabel")}: <span className="font-mono text-foreground">{new Date(settings.last_run).toLocaleString("fr-FR")}</span>
          </span>
        )}
        {settings.next_run && (
          <span>
            {t("admin.system.hl7.nextRunLabel")}: <span className="font-mono text-foreground">{new Date(settings.next_run).toLocaleString("fr-FR")}</span>
          </span>
        )}
      </div>
    </div>
  );
}

// ─── HL7 Tree Node (collapsible) ────────────────────────────────────────────

function HL7TreeNode({ segment, onDrag }: { segment: HL7SegmentNode; onDrag: (path: string, value: string) => void }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="select-none">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1.5 py-1 px-1 w-full text-left hover:bg-muted/40 rounded transition-colors"
      >
        {open ? <ChevronDown className="w-3 h-3 text-muted-foreground" /> : <ChevronRight className="w-3 h-3 text-muted-foreground" />}
        <span className="text-xs font-semibold text-primary">{segment.name}</span>
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

function HL7FieldItem({ field, onDrag }: { field: { path: string; value: string; components?: { path: string; value: string }[] }; onDrag: (path: string, value: string) => void }) {
  const [open, setOpen] = useState(false);
  const hasComponents = field.components && field.components.length > 0;

  return (
    <div>
      <div className="flex items-center gap-1 group">
        {hasComponents ? (
          <button onClick={() => setOpen((v) => !v)} className="p-0.5">
            {open ? <ChevronDown className="w-2.5 h-2.5 text-muted-foreground" /> : <ChevronRight className="w-2.5 h-2.5 text-muted-foreground" />}
          </button>
        ) : (
          <span className="w-3.5" />
        )}
        <div
          draggable
          onDragStart={(e) => {
            e.dataTransfer.setData("text/plain", JSON.stringify({ path: field.path, value: field.value }));
            onDrag(field.path, field.value);
          }}
          className="flex items-center gap-2 flex-1 px-1.5 py-0.5 rounded cursor-grab hover:bg-primary/5 active:cursor-grabbing transition-colors"
        >
          <GripVertical className="w-2.5 h-2.5 text-muted-foreground/50 opacity-0 group-hover:opacity-100 transition-opacity" />
          <span className="text-[10px] font-mono text-muted-foreground w-16 shrink-0">{field.path}</span>
          <span className="text-xs font-mono text-foreground truncate">{field.value || "—"}</span>
        </div>
      </div>
      {open && hasComponents && (
        <div className="ml-6 border-l border-border/50 pl-2 space-y-0.5">
          {field.components!.map((comp) => (
            <div
              key={comp.path}
              draggable
              onDragStart={(e) => {
                e.dataTransfer.setData("text/plain", JSON.stringify({ path: comp.path, value: comp.value }));
                onDrag(comp.path, comp.value);
              }}
              className="flex items-center gap-2 px-1.5 py-0.5 rounded cursor-grab hover:bg-primary/5 active:cursor-grabbing group transition-colors"
            >
              <GripVertical className="w-2.5 h-2.5 text-muted-foreground/50 opacity-0 group-hover:opacity-100 transition-opacity" />
              <span className="text-[10px] font-mono text-muted-foreground w-16 shrink-0">{comp.path}</span>
              <span className="text-xs font-mono text-foreground truncate">{comp.value || "—"}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// ─── HL7 Mapping Drop Zone ──────────────────────────────────────────────────

const TARGET_FIELDS = ["last_name", "first_name", "date_of_birth", "gender", "address", "phone"] as const;

function HL7MappingZone({ mappings, onDrop, onRemove, onSave, onUpdate, saving, presets, activePresetId, onSelectPreset, onDeletePreset }: {
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
            onDragOver={(e) => { e.preventDefault(); setDragOver(field); }}
            onDragLeave={() => setDragOver(null)}
            onDrop={(e) => {
              e.preventDefault();
              setDragOver(null);
              try {
                const data = JSON.parse(e.dataTransfer.getData("text/plain"));
                onDrop(field, data.path);
              } catch { /* ignore */ }
            }}
            className={`flex items-center gap-2 px-3 py-2 rounded-lg border transition-colors ${
              isOver ? "border-primary bg-primary/5" : "border-border bg-muted/20"
            }`}
          >
            <span className="text-[11px] font-medium text-muted-foreground w-24">
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

// ─── HL7 Test Section (main) ────────────────────────────────────────────────

function HL7TestSection() {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const queryClient = useQueryClient();
  const [patientId, setPatientId] = useState("");
  const [result, setResult] = useState<HL7TestResult | null>(null);
  const [tab, setTab] = useState<"response" | "tree">("response");
  const [localMappings, setLocalMappings] = useState<{ source_path: string; target_field: string }[]>([]);
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
      setLocalMappings(active.mappings?.map((m) => ({ source_path: m.source_path, target_field: m.target_field })) ?? []);
    }
  }, [presets]);

  const mutation = useMutation({
    mutationFn: (pid: string) => testHL7Query(pid),
    onSuccess: (data) => { setResult(data); if (data.tree) setTab("tree"); },
    onError: () => setResult({ success: false, patient_id: patientId, duration: "—", error: "Request failed" }),
  });

  const createPresetMutation = useMutation({
    mutationFn: (name: string) => createHL7Preset(name, localMappings),
    onSuccess: (preset) => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "hl7-presets"] });
      setActivePresetId(preset.id);
      setShowNamePopup(false);
      setPresetName("");
      void activateHL7Preset(preset.id);
      notify("success", t("admin.system.hl7.mappingSaved"));
    },
    onError: () => notify("error", t("admin.system.hl7.mappingError")),
  });

  const updatePresetMutation = useMutation({
    mutationFn: (presetId: string) => saveHL7PresetMappings(presetId, localMappings),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "hl7-presets"] });
      notify("success", t("admin.system.hl7.mappingSaved"));
    },
    onError: () => notify("error", t("admin.system.hl7.mappingError")),
  });

  const deletePresetMutation = useMutation({
    mutationFn: (id: string) => deleteHL7Preset(id),
    onSuccess: (_, id) => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "hl7-presets"] });
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

  const handleSelectPreset = useCallback((id: string) => {
    const preset = presets.find((p) => p.id === id);
    if (preset) {
      setActivePresetId(id);
      setLocalMappings(preset.mappings?.map((m) => ({ source_path: m.source_path, target_field: m.target_field })) ?? []);
      void activateHL7Preset(id);
    }
  }, [presets]);

  const handleDeletePreset = useCallback((id: string) => {
    if (confirm(t("admin.system.hl7.deletePresetConfirm"))) {
      deletePresetMutation.mutate(id);
    }
  }, [deletePresetMutation, t]);

  const handleDrop = useCallback((targetField: string, sourcePath: string) => {
    setLocalMappings((prev) => {
      const filtered = prev.filter((m) => m.target_field !== targetField);
      return [...filtered, { source_path: sourcePath, target_field: targetField }];
    });
  }, []);

  const handleRemove = useCallback((targetField: string) => {
    setLocalMappings((prev) => prev.filter((m) => m.target_field !== targetField));
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
                  if (e.key === "Enter" && patientId.trim()) mutation.mutate(patientId.trim());
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
              {mutation.isPending ? <Spinner size={12} className="text-primary-foreground" /> : <Send className="w-3.5 h-3.5" />}
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
          saving={createPresetMutation.isPending || updatePresetMutation.isPending}
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
                  if (e.key === "Enter" && presetName.trim()) createPresetMutation.mutate(presetName.trim());
                }}
                placeholder={t("admin.system.hl7.presetNamePlaceholder")}
                className="w-full px-3 py-2 text-sm border border-border rounded-lg bg-background focus:outline-none focus:ring-2 focus:ring-ring/20 mb-3"
              />
              <div className="flex gap-2 justify-end">
                <button
                  onClick={() => { setShowNamePopup(false); setPresetName(""); }}
                  className="text-xs text-muted-foreground hover:text-foreground px-3 py-1.5 transition-colors"
                >
                  {t("common.cancel")}
                </button>
                <button
                  onClick={() => createPresetMutation.mutate(presetName.trim())}
                  disabled={!presetName.trim() || createPresetMutation.isPending}
                  className="text-xs font-medium bg-primary text-primary-foreground px-3 py-1.5 rounded-lg hover:opacity-90 disabled:opacity-50 transition-opacity"
                >
                  {createPresetMutation.isPending ? <Spinner size={10} className="text-primary-foreground" /> : t("common.confirm")}
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
              tab === "response" ? "bg-primary/10 text-primary" : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t("admin.system.hl7.response")}
          </button>
          <button
            onClick={() => setTab("tree")}
            disabled={!result?.tree}
            className={`px-3 py-1.5 text-xs font-medium rounded-md transition-colors disabled:opacity-30 ${
              tab === "tree" ? "bg-primary/10 text-primary" : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t("admin.system.hl7.treeTab")}
          </button>
          {result && (
            <>
              <span className={`ml-2 text-[10px] font-medium px-2 py-0.5 rounded-full ${
                result.success ? "bg-success/10 text-success" : "bg-destructive/10 text-destructive"
              }`}>
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
                  <p className="text-xs font-mono text-destructive break-all">{result.error}</p>
                </div>
              )}
              {result.demographics && (
                <div className="space-y-1.5">
                  {([
                    ["last_name", result.demographics.last_name],
                    ["first_name", result.demographics.first_name],
                    ["date_of_birth", result.demographics.date_of_birth],
                    ["gender", result.demographics.gender],
                    ["source", result.demographics.source],
                  ] as const).map(([key, value]) => (
                    <div key={key} className="flex items-center gap-3 px-3 py-2 rounded-lg bg-muted/30">
                      <span className="text-[11px] font-medium text-muted-foreground w-24 capitalize">
                        {t(`admin.system.hl7.field.${key}`)}
                      </span>
                      <span className="text-sm font-mono text-foreground">{value || "—"}</span>
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

export function AdminSystemPage() {
  const { t } = useTranslation();
  const { stats, health } = useAdminStats();
  const { notify } = useNotification();
  const { hasPermission } = useAuth();

  const webhookQuery = useQuery({
    queryKey: ["admin", "webhook"],
    queryFn: fetchWebhookStatus,
    staleTime: 60_000,
  });

  const modulesQuery = useQuery({
    queryKey: ["admin", "modules"],
    queryFn: fetchModules,
    staleTime: 60_000,
  });

  const connectorsQuery = useQuery({
    queryKey: ["admin", "connectors"],
    queryFn: fetchConnectors,
    staleTime: 30_000,
    refetchInterval: 30_000,
  });

  const testMutation = useMutation({
    mutationFn: testWebhook,
    onSuccess: (data) => {
      if (data.success)
        notify(
          "success",
          t("admin.system.webhook.testOk", { code: data.status_code }),
        );
      else
        notify(
          "warn",
          t("admin.system.webhook.testWarn", { code: data.status_code ?? "?" }),
        );
    },
    onError: () => notify("error", t("admin.system.webhook.testError")),
  });

  const isOperational =
    health.data?.status === "ok" && health.data?.database === "ok";

  const dicomEnabled = health.data?.dicom_enabled ?? false;
  const dicomPort = health.data?.dicom_port ?? 0;
  const ftpEnabled = health.data?.ftp_enabled ?? false;
  const ftpPort = health.data?.ftp_port ?? 0;
  const ectpEnabled = health.data?.ectp_enabled ?? false;
  const ectpPort = health.data?.ectp_port ?? 0;

  const disabledLabel = t("admin.system.disabled");

  const services = [
    {
      name: t("admin.system.service.database"),
      icon: Database,
      ok: health.data?.database === "ok",
      label: undefined,
    },
    {
      name: t("admin.system.service.api"),
      icon: Server,
      ok: health.data?.status === "ok",
      label: undefined,
    },
    {
      name: t("admin.system.service.ftp"),
      icon: HardDrive,
      ok: ftpEnabled,
      label: ftpEnabled ? `:${ftpPort}` : disabledLabel,
    },
    {
      name: t("admin.system.service.dicom"),
      icon: Radio,
      ok: dicomEnabled,
      label: dicomEnabled ? `:${dicomPort}` : disabledLabel,
    },
    {
      name: t("admin.system.service.ectp"),
      icon: Activity,
      ok: ectpEnabled,
      label: ectpEnabled ? `:${ectpPort}` : disabledLabel,
    },
  ];

  function copyToClipboard(text: string) {
    void navigator.clipboard.writeText(text);
    notify("success", t("admin.system.webhook.urlCopied"));
  }

  const kpis = stats.data
    ? [
        {
          label: t("admin.system.kpi.totalEcgs"),
          value: stats.data.total_ecgs,
          icon: Activity,
          color: "text-foreground",
        },
        {
          label: t("admin.system.kpi.hl7Sent"),
          value: stats.data.hl7_success,
          icon: CheckCircle,
          color: "text-success",
        },
        {
          label: t("admin.system.kpi.hl7Pending"),
          value: stats.data.hl7_pending,
          icon: Clock,
          color: "text-warning",
        },
        {
          label: t("admin.system.kpi.hl7Exhausted"),
          value: stats.data.hl7_exhausted,
          icon: AlertTriangle,
          color:
            stats.data.hl7_exhausted > 0 ? "text-warning" : "text-foreground",
        },
        {
          label: t("admin.system.kpi.quarantine"),
          value: stats.data.quarantine_count ?? 0,
          icon: AlertTriangle,
          color:
            (stats.data.quarantine_count ?? 0) > 0
              ? "text-quarantine"
              : "text-foreground",
        },
      ]
    : [];

  return (
    <div className="p-6 space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-lg font-semibold text-foreground">
          {t("admin.system.title")}
        </h1>
        <p className="text-sm text-muted-foreground mt-1">
          {t("admin.system.subtitle")}
        </p>
      </div>
      {/* Global status banner */}
      <div className="bg-card rounded-lg border border-border p-4 flex items-center gap-3">
        <div
          className={`w-8 h-8 rounded-full flex items-center justify-center shrink-0 ${isOperational ? "bg-success/10" : "bg-destructive/10"}`}
        >
          {isOperational ? (
            <CheckCircle className="w-5 h-5 text-success" />
          ) : (
            <AlertCircle className="w-5 h-5 text-destructive" />
          )}
        </div>
        <div>
          <h2 className="text-sm font-semibold text-foreground">
            {isOperational
              ? t("admin.system.operational")
              : t("admin.system.degraded")}
          </h2>
          {health.isLoading && (
            <p className="text-[11px] text-muted-foreground">
              {t("common.loading")}
            </p>
          )}
        </div>
      </div>

      {/* KPI cards */}
      {stats.isLoading && (
        <div className="flex justify-center py-4">
          <Spinner size={18} className="text-muted-foreground" />
        </div>
      )}
      {kpis.length > 0 && (
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-4">
          {kpis.map((kpi) => (
            <div
              key={kpi.label}
              className="bg-card rounded-lg border border-border p-4 flex items-center gap-3"
            >
              <kpi.icon
                className={`w-5 h-5 shrink-0 translate-y-2 ${kpi.color}`}
              />
              <div className="min-w-0">
                <p className="text-[11px] text-muted-foreground truncate -translate-x-8">
                  {kpi.label}
                </p>
                <p className={`text-xl font-bold tabular-nums ${kpi.color}`}>
                  {kpi.value.toLocaleString()}
                </p>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Infrastructure (70%) + Formats ECG (30%) — same height row */}
      <div className="grid grid-cols-1 lg:grid-cols-[7fr_3fr] gap-6 lg:items-stretch">
        {/* Infrastructure — left */}
        {health.data && (
          <div className="flex flex-col">
            <div className="bg-card rounded-lg border border-border p-5 flex-1">
              <p className="text-[14px] font-semibold uppercase tracking-wide text-foreground mb-3 px-1">
                {t("admin.system.infrastructure")}
              </p>
              <p className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground mb-3 px-1">
                {t("admin.system.servicesMonitored", {
                  count: services.length,
                })}
              </p>
              <div className="space-y-1">
                {services.map((svc) => (
                  <div
                    key={svc.name}
                    className="flex items-center gap-3 px-3 py-2.5 rounded-lg hover:bg-muted/30 transition-colors"
                  >
                    <svc.icon className="w-4 h-4 text-muted-foreground shrink-0" />
                    <span className="text-sm font-medium text-foreground flex-1">
                      {svc.name}
                    </span>
                    {svc.label && (
                      <span className="text-[10px] font-mono text-muted-foreground">
                        {svc.label}
                      </span>
                    )}
                    <span
                      className={`w-2 h-2 rounded-full shrink-0 ${svc.ok ? "bg-success" : "bg-muted-foreground"}`}
                    />
                    <span
                      className={`text-[11px] font-medium w-6 ${svc.ok ? "text-success" : "text-muted-foreground"}`}
                    >
                      {svc.ok ? "OK" : svc.label === disabledLabel ? "—" : "KO"}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        )}

        {/* Formats ECG — right, same height, scroll if overflow */}
        {!modulesQuery.isLoading &&
          modulesQuery.data &&
          modulesQuery.data.length > 0 && (
            <div className="flex flex-col lg:h-full">
              <div className="bg-card rounded-lg border border-border p-5 flex-1 lg:overflow-y-auto">
                <p className="text-[14px] font-semibold uppercase tracking-wide text-foreground mb-3 px-1">
                  {t("admin.system.formatsTitle")}
                </p>
                <p className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground mb-3 px-1">
                  {t("admin.system.formatsSubtitle")}
                </p>
                <div className="space-y-1">
                  {modulesQuery.data.map((m) => (
                    <div
                      key={m.name}
                      className="flex items-center gap-3 px-3 py-2.5 rounded-lg hover:bg-muted/30 transition-colors"
                    >
                      <FileType className="w-4 h-4 text-muted-foreground shrink-0" />
                      <span className="text-sm font-medium text-foreground capitalize flex-1">
                        {m.name}
                      </span>
                      <div className="flex flex-wrap gap-1">
                        {m.extensions.map((ext) => (
                          <span
                            key={ext}
                            className="text-[10px] font-mono bg-muted px-1.5 py-0.5 rounded text-muted-foreground"
                          >
                            {ext}
                          </span>
                        ))}
                      </div>
                      <span
                        className={`w-2 h-2 rounded-full shrink-0 ${m.status === "ok" ? "bg-success" : "bg-warning"}`}
                      />
                      <span
                        className={`text-[11px] font-medium w-6 ${m.status === "ok" ? "text-success" : "text-warning"}`}
                      >
                        {m.status === "ok" ? "OK" : "ERR"}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          )}
      </div>

      {/* Connecteurs Proxy */}
      {connectorsQuery.data && connectorsQuery.data.length > 0 && (
        <div>
          <p className="text-[14px] font-semibold uppercase tracking-wide text-foreground mb-3 px-1">
            {t("admin.system.connectors.title")}
          </p>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {connectorsQuery.data.map((conn) => {
              const ok = conn.status === "ok";
              const isDicom = conn.protocol === "dicom_cstore";
              return (
                <div
                  key={conn.name}
                  className="bg-card rounded-lg border border-border p-4 space-y-2"
                >
                  <div className="flex items-center gap-2">
                    <Send className="w-4 h-4 text-muted-foreground" />
                    <span className="text-sm font-medium text-foreground capitalize flex-1">
                      {conn.name}
                    </span>
                    {conn.protocol && (
                      <span
                        className={`text-[10px] font-medium px-2 py-0.5 rounded-full ${
                          isDicom
                            ? "bg-dicom/10 text-dicom"
                            : "bg-accent/10 text-accent"
                        }`}
                      >
                        {isDicom
                          ? t("admin.system.connectors.protocolDicom")
                          : t("admin.system.connectors.protocolEctp")}
                      </span>
                    )}
                  </div>
                  <div className="flex items-center gap-4 text-xs">
                    {conn.host && (
                      <span className="font-mono text-muted-foreground">
                        {conn.host}:{conn.port}
                      </span>
                    )}
                    {conn.ae_title && (
                      <span className="font-mono text-muted-foreground">
                        AE: {conn.ae_title}
                      </span>
                    )}
                    <span
                      className={`ml-auto flex items-center gap-1.5 text-[11px] font-medium ${ok ? "text-success" : "text-destructive"}`}
                    >
                      <span
                        className={`w-2 h-2 rounded-full ${ok ? "bg-success" : "bg-destructive"}`}
                      />
                      {ok ? "OK" : "KO"}
                    </span>
                  </div>
                  {!ok && (
                    <p
                      className="text-[10px] font-mono text-destructive truncate"
                      title={conn.status}
                    >
                      {conn.status}
                    </p>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* HL7 Scheduler + Test — requires hl7.config */}
      {hasPermission("hl7.config") && <HL7SchedulerSection />}
      {hasPermission("hl7.config") && <HL7TestSection />}

      {/* Storage */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <MetricCard />

        {/* Webhook HL7 */}
        <div className="bg-card rounded-lg border border-border p-5">
          <div className="flex items-center justify-between mb-4">
            <div className="flex items-center gap-3">
              <Webhook className="w-5 h-5 text-muted-foreground" />
              <h2 className="text-sm font-semibold text-foreground">
                {t("admin.system.webhook.title")}
              </h2>
              {webhookQuery.data && (
                <span
                  className={`text-[10px] font-medium px-2 py-0.5 rounded-full ${
                    webhookQuery.data.enabled
                      ? "bg-success/10 text-success"
                      : "bg-muted text-muted-foreground"
                  }`}
                >
                  {webhookQuery.data.enabled
                    ? t("admin.system.webhook.enabled")
                    : t("admin.system.webhook.disabled")}
                </span>
              )}
            </div>
            {webhookQuery.data?.enabled && (
              <button
                onClick={() => testMutation.mutate()}
                disabled={testMutation.isPending}
                className="text-xs border border-border px-3 py-1.5 rounded-lg hover:bg-muted transition-colors disabled:opacity-50 flex items-center gap-1.5"
              >
                {testMutation.isPending && <Spinner size={11} />}
                {testMutation.isPending
                  ? t("admin.system.webhook.testing")
                  : t("admin.system.webhook.test")}
              </button>
            )}
          </div>

          {webhookQuery.isLoading && (
            <p className="text-sm text-muted-foreground">
              {t("common.loading")}
            </p>
          )}

          {webhookQuery.data && (
            <div className="space-y-3">
              {webhookQuery.data.url && (
                <div className="flex items-center gap-2">
                  <span className="text-[11px] text-muted-foreground w-12">
                    URL
                  </span>
                  <code className="text-xs font-mono bg-muted px-2 py-1 rounded flex-1 truncate">
                    {webhookQuery.data.url}
                  </code>
                  <button
                    onClick={() => copyToClipboard(webhookQuery.data!.url)}
                    className="p-1 hover:bg-muted rounded transition-colors"
                  >
                    <Copy className="w-3.5 h-3.5 text-muted-foreground" />
                  </button>
                </div>
              )}
              <div className="flex items-center gap-2">
                <span className="text-[11px] text-muted-foreground w-12">
                  {t("admin.system.webhook.secret")}
                </span>
                <div className="flex items-center gap-1.5">
                  <Lock
                    className={`w-3 h-3 ${webhookQuery.data.secret_configured ? "text-success" : "text-warning"}`}
                  />
                  <span
                    className={`text-xs ${webhookQuery.data.secret_configured ? "text-success" : "text-warning"}`}
                  >
                    {webhookQuery.data.secret_configured
                      ? t("admin.system.webhook.secretConfigured")
                      : t("admin.system.webhook.secretMissing")}
                  </span>
                </div>
              </div>
              {testMutation.data && (
                <div className="flex items-center gap-2 mt-2">
                  <span className="text-[11px] text-muted-foreground">
                    {t("admin.system.webhook.lastTest")}
                  </span>
                  <span
                    className={`text-xs font-mono font-medium ${testMutation.data.success ? "text-success" : "text-destructive"}`}
                  >
                    {testMutation.data.success
                      ? `HTTP ${testMutation.data.status_code}`
                      : `✗ ${testMutation.data.error ?? `HTTP ${testMutation.data.status_code}`}`}
                  </span>
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

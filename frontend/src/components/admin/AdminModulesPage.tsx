import { useState, useEffect, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Eye, EyeOff, Settings2, Play, Square, AlertCircle, Plus, Trash2, Wifi, Radio, Database } from "lucide-react";
import {
  fetchFTPConfig,
  saveFTPConfig,
  fetchDICOMConfig,
  saveDICOMConfig,
  fetchModuleStatuses,
  startModule,
  stopModule,
  fetchConnectorConfigs,
  saveConnectorConfig,
  deleteConnectorConfig,
  testConnector,
  connectorTypeFromProtocol,
  fetchModuleSettings,
  saveModuleSettings,
  type DICOMModuleConfig,
  type ConnectorConfig,
  type ConnectorType,
} from "../../lib/api";
import { Spinner } from "../ui/Spinner";
import { useNotification } from "../../context/NotificationContext";
import { useAuth } from "../../hooks/useAuth";
import { errorMessage } from "../../lib/errors";

const MASKED_PASSWORD = "••••••";

// ─── Password Input with toggle ─────────────────────────────────────────────

function PasswordInput({
  value,
  onChange,
  placeholder,
  className,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  className?: string;
}) {
  const [visible, setVisible] = useState(false);
  return (
    <div className="relative">
      <input
        type={visible ? "text" : "password"}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className={className}
      />
      <button
        type="button"
        onClick={() => setVisible((v) => !v)}
        className="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-muted-foreground hover:text-foreground transition-colors"
      >
        {visible ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
      </button>
    </div>
  );
}

// ─── Toggle Switch ───────────────────────────────────────────────────────────

function Toggle({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      type="button"
      onClick={() => onChange(!checked)}
      className={`relative w-10 h-5 rounded-full transition-colors ${checked ? "bg-primary" : "bg-muted-foreground/30"}`}
    >
      <span
        className={`absolute left-0.5 top-0.5 w-4 h-4 rounded-full bg-white shadow transition-transform ${checked ? "translate-x-5" : "translate-x-0"}`}
      />
    </button>
  );
}

// ─── Status Badge ────────────────────────────────────────────────────────────

function StatusBadge({ status }: { status: "running" | "stopped" | "error" | undefined }) {
  const { t } = useTranslation();

  if (!status) {
    return (
      <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-medium bg-muted text-muted-foreground">
        {t("modules.status.stopped")}
      </span>
    );
  }

  const styles = {
    running: "bg-green-500/10 text-green-600 border border-green-500/20",
    stopped: "bg-muted text-muted-foreground",
    error: "bg-orange-500/10 text-orange-600 border border-orange-500/20",
  };

  const labels = {
    running: t("modules.status.running"),
    stopped: t("modules.status.stopped"),
    error: t("modules.status.error"),
  };

  return (
    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-medium ${styles[status]}`}>
      <span className={`w-1.5 h-1.5 rounded-full ${status === "running" ? "bg-green-500" : status === "error" ? "bg-orange-500" : "bg-muted-foreground"}`} />
      {labels[status]}
    </span>
  );
}

// ─── FTP Card ────────────────────────────────────────────────────────────────

function FTPCard({ ftpStatus }: { ftpStatus: "running" | "stopped" | "error" | undefined }) {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const queryClient = useQueryClient();

  const [port, setPort] = useState(2121);
  const [passiveLow, setPassiveLow] = useState(30000);
  const [passiveHigh, setPassiveHigh] = useState(30010);
  const [publicHost, setPublicHost] = useState("");
  const [tls, setTls] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState(MASKED_PASSWORD);
  const [enabled, setEnabled] = useState(false);
  const [portModified, setPortModified] = useState(false);
  const initialPort = useRef<number | null>(null);

  const { isLoading, data: ftpConfig } = useQuery({
    queryKey: ["admin", "modules", "ftp", "config"],
    queryFn: fetchFTPConfig,
    staleTime: 30_000,
  });

  useEffect(() => {
    if (!ftpConfig) return;
    setPort(ftpConfig.port);
    const parts = (ftpConfig.passive_port_range || "30000-30010").split("-");
    setPassiveLow(parseInt(parts[0]) || 30000);
    setPassiveHigh(parseInt(parts[1]) || 30010);
    setPublicHost(ftpConfig.public_host);
    setTls(ftpConfig.tls);
    setUsername(ftpConfig.username);
    setPassword(ftpConfig.password);
    setEnabled(ftpConfig.enabled);
    if (initialPort.current === null) {
      initialPort.current = ftpConfig.port;
    }
  }, [ftpConfig]);

  useEffect(() => {
    if (initialPort.current !== null && port !== initialPort.current) {
      setPortModified(true);
    } else {
      setPortModified(false);
    }
  }, [port]);

  const saveMutation = useMutation({
    mutationFn: (overrideEnabled?: boolean) =>
      saveFTPConfig({
        port,
        passive_port_range: `${passiveLow}-${passiveHigh}`.trim(),
        public_host: publicHost.trim(),
        tls,
        username: username.trim(),
        password,
        enabled: overrideEnabled !== undefined ? overrideEnabled : enabled,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "modules", "ftp", "config"] });
      initialPort.current = port;
      setPortModified(false);
      notify("success", t("modules.ftp.saveOk"));
    },
    onError: (err: unknown) => notify("error", errorMessage(err, t("modules.ftp.saveError"))),
  });

  const startMutation = useMutation({
    mutationFn: () => startModule("ftp"),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "modules", "status"] });
      void queryClient.refetchQueries({ queryKey: ["admin", "modules", "status"] });
      notify("success", t("modules.ftp.startOk"));
    },
    onError: (err: unknown) => notify("error", errorMessage(err, t("modules.ftp.saveError"))),
  });

  const stopMutation = useMutation({
    mutationFn: () => stopModule("ftp"),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "modules", "status"] });
      void queryClient.refetchQueries({ queryKey: ["admin", "modules", "status"] });
      notify("success", t("modules.ftp.stopOk"));
    },
    onError: (err: unknown) => notify("error", errorMessage(err, t("modules.ftp.saveError"))),
  });

  const inputClass =
    "w-full text-xs border border-border rounded-md px-2.5 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20";

  const isRunning = ftpStatus === "running";
  const actionPending = startMutation.isPending || stopMutation.isPending;

  return (
    <div className="bg-card rounded-lg border border-border p-5">
      {/* Header */}
      <div className="flex items-center justify-between mb-5">
        <div className="flex items-center gap-3">
          <Settings2 className="w-5 h-5 text-muted-foreground" />
          <h2 className="text-sm font-semibold text-foreground">
            {t("modules.ftp.title")}
          </h2>
          <StatusBadge status={ftpStatus} />
        </div>
      </div>

      {isLoading && (
        <div className="flex justify-center py-4">
          <Spinner size={16} className="text-muted-foreground" />
        </div>
      )}

      {!isLoading && (
        <>
          {/* Fields */}
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 mb-5">
            <div>
              <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("modules.ftp.port")}
              </label>
              <input
                type="number"
                value={port}
                min={1} max={65535}
                onChange={(e) => setPort(Math.min(65535, Math.max(1, parseInt(e.target.value) || 2121)))}
                className={`mt-1 ${inputClass}`}
              />
            </div>
            <div>
              <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("modules.ftp.passiveRange")}
              </label>
              <div className="flex items-center gap-2 mt-1">
                <input
                  type="number"
                  value={passiveLow}
                  min={1} max={65535}
                  onChange={(e) => setPassiveLow(Math.min(65535, Math.max(1, parseInt(e.target.value) || 30000)))}
                  className={inputClass}
                  placeholder="30000"
                />
                <span className="text-muted-foreground text-sm shrink-0">–</span>
                <input
                  type="number"
                  value={passiveHigh}
                  min={1} max={65535}
                  onChange={(e) => setPassiveHigh(Math.min(65535, Math.max(1, parseInt(e.target.value) || 30010)))}
                  className={inputClass}
                  placeholder="30010"
                />
              </div>
            </div>
            <div>
              <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("modules.ftp.publicHost")}
              </label>
              <input
                type="text"
                value={publicHost}
                onChange={(e) => setPublicHost(e.target.value)}
                className={`mt-1 ${inputClass}`}
                placeholder="10.33.70.32"
              />
            </div>
            <div>
              <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("modules.ftp.username")}
              </label>
              <input
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className={`mt-1 ${inputClass}`}
              />
            </div>
            <div>
              <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("modules.ftp.password")}
              </label>
              <PasswordInput
                value={password}
                onChange={setPassword}
                className={`mt-1 ${inputClass} pr-8`}
              />
            </div>
            <div>
              <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("modules.ftp.tls")}
              </label>
              <div className="mt-2">
                <Toggle checked={tls} onChange={setTls} />
              </div>
            </div>
          </div>

          {/* Restart notice */}
          {portModified && (
            <p className="flex items-center gap-1.5 text-[10px] text-orange-600 bg-orange-500/5 border border-orange-500/20 rounded-md px-3 py-2 mb-4">
              <AlertCircle className="w-3 h-3 shrink-0" />
              {t("modules.ftp.restartNotice")}
            </p>
          )}

          {/* Action buttons */}
          <div className="flex items-center gap-2">
            <button
              onClick={() => saveMutation.mutate(undefined)}
              disabled={saveMutation.isPending}
              className="inline-flex items-center gap-1.5 px-4 py-1.5 rounded-lg text-xs font-medium bg-primary text-primary-foreground hover:opacity-90 disabled:opacity-50 transition-opacity"
            >
              {saveMutation.isPending && <Spinner size={11} className="text-primary-foreground" />}
              {t("modules.save")}
            </button>
            {isRunning ? (
              <button
                onClick={() => stopMutation.mutate()}
                disabled={actionPending}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border border-destructive/30 text-destructive hover:bg-destructive/5 transition-colors disabled:opacity-50"
              >
                {stopMutation.isPending ? (
                  <Spinner size={11} />
                ) : (
                  <Square className="w-3 h-3" />
                )}
                {t("modules.stop")}
              </button>
            ) : (
              <button
                onClick={() => startMutation.mutate()}
                disabled={actionPending}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border border-border hover:bg-muted transition-colors disabled:opacity-50"
              >
                {startMutation.isPending ? (
                  <Spinner size={11} />
                ) : (
                  <Play className="w-3 h-3" />
                )}
                {t("modules.start")}
              </button>
            )}
          </div>
        </>
      )}
    </div>
  );
}

// ─── DICOM Card ──────────────────────────────────────────────────────────────

function DICOMCard({ dicomStatus }: { dicomStatus: "running" | "stopped" | "error" | undefined }) {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const queryClient = useQueryClient();

  const [port, setPort] = useState(11112);
  const [aeTitle, setAeTitle] = useState("ECG-HUB");
  const [echoEnabled, setEchoEnabled] = useState(true);
  const [tls, setTls] = useState(false);
  const [enabled, setEnabled] = useState(false);

  const { isLoading, data: dicomConfig } = useQuery({
    queryKey: ["admin", "modules", "dicom", "config"],
    queryFn: fetchDICOMConfig,
    staleTime: 30_000,
  });

  useEffect(() => {
    if (!dicomConfig) return;
    setPort(dicomConfig.port);
    setAeTitle(dicomConfig.ae_title);
    setEchoEnabled(dicomConfig.echo_enabled);
    setTls(dicomConfig.tls);
    setEnabled(dicomConfig.enabled);
  }, [dicomConfig]);

  const saveMutation = useMutation({
    mutationFn: (overrideEnabled?: boolean) =>
      saveDICOMConfig({
        port,
        ae_title: aeTitle,
        echo_enabled: echoEnabled,
        tls,
        enabled: overrideEnabled !== undefined ? overrideEnabled : enabled,
      } as DICOMModuleConfig),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "modules", "dicom", "config"] });
      notify("success", t("modules.dicom.saveOk"));
    },
    onError: (err: unknown) => notify("error", errorMessage(err, t("modules.dicom.saveError"))),
  });

  const startMutation = useMutation({
    mutationFn: () => startModule("dicom"),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "modules", "status"] });
      void queryClient.refetchQueries({ queryKey: ["admin", "modules", "status"] });
      notify("success", t("modules.dicom.startOk"));
    },
    onError: (err: unknown) => notify("error", errorMessage(err, t("modules.dicom.saveError"))),
  });

  const stopMutation = useMutation({
    mutationFn: () => stopModule("dicom"),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "modules", "status"] });
      void queryClient.refetchQueries({ queryKey: ["admin", "modules", "status"] });
      notify("success", t("modules.dicom.stopOk"));
    },
    onError: (err: unknown) => notify("error", errorMessage(err, t("modules.dicom.saveError"))),
  });

  const inputClass =
    "w-full text-xs border border-border rounded-md px-2.5 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20";

  const isRunning = dicomStatus === "running";
  const actionPending = startMutation.isPending || stopMutation.isPending;

  return (
    <div className="bg-card rounded-lg border border-border p-5">
      {/* Header */}
      <div className="flex items-center justify-between mb-5">
        <div className="flex items-center gap-3">
          <Settings2 className="w-5 h-5 text-muted-foreground" />
          <h2 className="text-sm font-semibold text-foreground">
            {t("modules.dicom.title")}
          </h2>
          <StatusBadge status={dicomStatus} />
        </div>
      </div>

      {isLoading && (
        <div className="flex justify-center py-4">
          <Spinner size={16} className="text-muted-foreground" />
        </div>
      )}

      {!isLoading && (
        <>
          {/* Fields */}
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 mb-5">
            <div>
              <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("modules.dicom.port")}
              </label>
              <input
                type="number"
                value={port}
                min={1} max={65535}
                onChange={(e) => setPort(Math.min(65535, Math.max(1, parseInt(e.target.value) || 11112)))}
                className={`mt-1 ${inputClass}`}
              />
            </div>
            <div>
              <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("modules.dicom.aeTitle")}
              </label>
              <input
                type="text"
                value={aeTitle}
                onChange={(e) => setAeTitle(e.target.value)}
                className={`mt-1 ${inputClass}`}
                placeholder="ECG-HUB"
              />
            </div>
            <div>
              <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("modules.dicom.echoEnabled")}
              </label>
              <div className="mt-2">
                <Toggle checked={echoEnabled} onChange={setEchoEnabled} />
              </div>
            </div>
            <div>
              <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("modules.dicom.tls")}
              </label>
              <div className="mt-2">
                <Toggle checked={tls} onChange={setTls} />
              </div>
            </div>
          </div>

          {/* Action buttons */}
          <div className="flex items-center gap-2">
            <button
              onClick={() => saveMutation.mutate(undefined)}
              disabled={saveMutation.isPending}
              className="inline-flex items-center gap-1.5 px-4 py-1.5 rounded-lg text-xs font-medium bg-primary text-primary-foreground hover:opacity-90 disabled:opacity-50 transition-opacity"
            >
              {saveMutation.isPending && <Spinner size={11} className="text-primary-foreground" />}
              {t("modules.save")}
            </button>
            {isRunning ? (
              <button
                onClick={() => stopMutation.mutate()}
                disabled={actionPending}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border border-destructive/30 text-destructive hover:bg-destructive/5 transition-colors disabled:opacity-50"
              >
                {stopMutation.isPending ? (
                  <Spinner size={11} />
                ) : (
                  <Square className="w-3 h-3" />
                )}
                {t("modules.stop")}
              </button>
            ) : (
              <button
                onClick={() => startMutation.mutate()}
                disabled={actionPending}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border border-border hover:bg-muted transition-colors disabled:opacity-50"
              >
                {startMutation.isPending ? (
                  <Spinner size={11} />
                ) : (
                  <Play className="w-3 h-3" />
                )}
                {t("modules.start")}
              </button>
            )}
          </div>
        </>
      )}
    </div>
  );
}

// ─── Connector Card (shared logic) ──────────────────────────────────────────

// ConnectorCardState is the full mutable state for any connector card.
type ConnectorCardState = ConnectorConfig & { enabled: boolean };

function emptyPolaris(): ConnectorCardState {
  return {
    name: "",
    protocol: "ectp_ftp",
    enabled: false,
    extensions: [],
    vendors: [],
    max_attempts: 3,
    interval: "5m",
    ectp_host: "",
    ectp_port: 0,
    ftp_host: "",
    ftp_port: 21,
    ftp_username: "",
    ftp_password: "",
  };
}

function emptyPacs(): ConnectorCardState {
  return {
    name: "",
    protocol: "dicom_cstore",
    enabled: false,
    extensions: [],
    vendors: [],
    max_attempts: 3,
    interval: "5m",
    dicom_host: "",
    dicom_port: 104,
    calling_ae: "",
    called_ae: "",
    dicom_timeout: "30s",
  };
}

// useConnectorCardLogic encapsulates the shared save / delete / test mutations
// used by both PolarisCard and PacsCard.
function useConnectorCardLogic({
  cfg,
  isNew,
  onSaved,
  onDeleted,
}: {
  cfg: ConnectorCardState;
  isNew: boolean;
  onSaved: () => void;
  onDeleted: () => void;
}) {
  const { t } = useTranslation();
  const { notify } = useNotification();

  const [confirmDelete, setConfirmDelete] = useState(false);
  const [testResult, setTestResult] = useState<{
    success: boolean;
    latency: string;
    error?: string;
  } | null>(null);

  const saveMutation = useMutation({
    mutationFn: () => saveConnectorConfig(cfg.name, cfg),
    onSuccess: () => {
      notify("success", t("modules.connectors.saveOk"));
      setTestResult(null);
      onSaved();
    },
    onError: (err: unknown) => notify("error", errorMessage(err, t("modules.connectors.saveError"))),
  });

  const deleteMutation = useMutation({
    mutationFn: () => deleteConnectorConfig(cfg.name),
    onSuccess: () => {
      notify("success", t("modules.connectors.deleteOk"));
      onDeleted();
    },
    onError: (err: unknown) => notify("error", errorMessage(err, t("modules.connectors.saveError"))),
  });

  const testMutation = useMutation({
    mutationFn: () => testConnector(cfg.name),
    onSuccess: (res) => {
      setTestResult(res);
      if (res.success) {
        notify("success", t("modules.connectors.testOk", { latency: res.latency }));
      } else {
        notify("error", t("modules.connectors.testFail", { error: res.error ?? "unknown" }));
      }
    },
    onError: (err: unknown) =>
      notify(
        "error",
        errorMessage(err, t("modules.connectors.testFail", { error: "unknown" })),
      ),
  });

  return {
    t,
    confirmDelete,
    setConfirmDelete,
    testResult,
    saveMutation,
    deleteMutation,
    testMutation,
    isNew,
  };
}

// ConnectorCardActions renders the Save / Test / Delete button row shared by both card types.
function ConnectorCardActions({
  logic,
  canSave,
}: {
  logic: ReturnType<typeof useConnectorCardLogic>;
  canSave: boolean;
}) {
  const { t, confirmDelete, setConfirmDelete, testResult, saveMutation, deleteMutation, testMutation, isNew } =
    logic;

  return (
    <>
      {testResult && (
        <p
          className={`flex items-center gap-1.5 text-[10px] rounded-md px-3 py-2 mb-3 ${
            testResult.success
              ? "text-green-600 bg-green-500/5 border border-green-500/20"
              : "text-destructive bg-destructive/5 border border-destructive/20"
          }`}
        >
          <Wifi className="w-3 h-3 shrink-0" />
          {testResult.success
            ? t("modules.connectors.testOk", { latency: testResult.latency })
            : t("modules.connectors.testFail", { error: testResult.error ?? "unknown" })}
        </p>
      )}

      <div className="flex items-center gap-2 flex-wrap">
        <button
          onClick={() => saveMutation.mutate()}
          disabled={saveMutation.isPending || !canSave}
          className="inline-flex items-center gap-1.5 px-4 py-1.5 rounded-lg text-xs font-medium bg-primary text-primary-foreground hover:opacity-90 disabled:opacity-50 transition-opacity"
        >
          {saveMutation.isPending && (
            <Spinner size={11} className="text-primary-foreground" />
          )}
          {t("modules.save")}
        </button>

        {!isNew && (
          <button
            onClick={() => testMutation.mutate()}
            disabled={testMutation.isPending}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border border-border hover:bg-muted transition-colors disabled:opacity-50"
          >
            {testMutation.isPending ? <Spinner size={11} /> : <Wifi className="w-3 h-3" />}
            Test
          </button>
        )}

        {!isNew && (
          <>
            {confirmDelete ? (
              <>
                <button
                  onClick={() => deleteMutation.mutate()}
                  disabled={deleteMutation.isPending}
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-destructive text-destructive-foreground hover:opacity-90 disabled:opacity-50 transition-opacity"
                >
                  {deleteMutation.isPending ? <Spinner size={11} /> : null}
                  {t("common.confirm")}
                </button>
                <button
                  onClick={() => setConfirmDelete(false)}
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border border-border hover:bg-muted transition-colors"
                >
                  {t("common.cancel")}
                </button>
              </>
            ) : (
              <button
                onClick={() => setConfirmDelete(true)}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border border-destructive/30 text-destructive hover:bg-destructive/5 transition-colors"
              >
                <Trash2 className="w-3 h-3" />
                {t("common.delete")}
              </button>
            )}
          </>
        )}
      </div>
    </>
  );
}

// ─── Polaris Card (protocol: ectp_ftp) ───────────────────────────────────────

function PolarisCard({
  initial,
  isNew,
  onSaved,
  onDeleted,
}: {
  initial: ConnectorCardState | null;
  isNew: boolean;
  onSaved: () => void;
  onDeleted: () => void;
}) {
  const { t } = useTranslation();
  const [cfg, setCfg] = useState<ConnectorCardState>(initial ?? emptyPolaris());

  useEffect(() => {
    if (initial) setCfg(initial);
  }, [initial]);

  const set = <K extends keyof ConnectorCardState>(key: K, value: ConnectorCardState[K]) =>
    setCfg((prev) => ({ ...prev, [key]: value }));

  const logic = useConnectorCardLogic({ cfg, isNew, onSaved, onDeleted });

  const inputClass =
    "w-full text-xs border border-border rounded-md px-2.5 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20";

  const displayName = isNew
    ? t("modules.connectors.polaris") + " — " + t("common.new", { defaultValue: "New" })
    : cfg.name;

  return (
    <div className="bg-card rounded-lg border border-border p-5">
      {/* Header */}
      <div className="flex items-center justify-between mb-5">
        <div className="flex items-center gap-3">
          <Radio className="w-5 h-5 text-blue-500" />
          <h2 className="text-sm font-semibold text-foreground">{displayName}</h2>
          <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium bg-blue-500/10 text-blue-600 border border-blue-500/20">
            {t("modules.connectors.polaris")} — ECTP/FTP
          </span>
        </div>
        <Toggle checked={cfg.enabled} onChange={(v) => set("enabled", v)} />
      </div>

      {/* Common identity fields */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 mb-4">
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            Name
          </label>
          <input
            type="text"
            value={cfg.name}
            readOnly={!isNew}
            onChange={(e) => set("name", e.target.value)}
            className={`mt-1 ${inputClass} ${!isNew ? "opacity-60 cursor-not-allowed" : ""}`}
            placeholder="polaris"
          />
        </div>
      </div>

      {/* ECTP section */}
      <p className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground mb-2 mt-1">
        ECTP
      </p>
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 mb-4 pb-4 border-b border-border">
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.ectpHost")}
          </label>
          <input
            type="text"
            value={cfg.ectp_host ?? ""}
            onChange={(e) => set("ectp_host", e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="polaris.hospital.local"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.ectpPort")}
          </label>
          <input
            type="number"
            value={cfg.ectp_port ?? 0}
            min={1} max={65535}
            onChange={(e) => set("ectp_port", Math.min(65535, Math.max(1, parseInt(e.target.value) || 0)))}
            className={`mt-1 ${inputClass}`}
          />
        </div>
      </div>

      {/* FTP section */}
      <p className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground mb-2">
        FTP
      </p>
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 mb-4 pb-4 border-b border-border">
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.ftpHost")}
          </label>
          <input
            type="text"
            value={cfg.ftp_host ?? ""}
            onChange={(e) => set("ftp_host", e.target.value)}
            className={`mt-1 ${inputClass}`}
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.ftpPort")}
          </label>
          <input
            type="number"
            value={cfg.ftp_port ?? 21}
            min={1} max={65535}
            onChange={(e) => set("ftp_port", Math.min(65535, Math.max(1, parseInt(e.target.value) || 21)))}
            className={`mt-1 ${inputClass}`}
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.ftpUsername")}
          </label>
          <input
            type="text"
            value={cfg.ftp_username ?? ""}
            onChange={(e) => set("ftp_username", e.target.value)}
            className={`mt-1 ${inputClass}`}
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.ftpPassword")}
          </label>
          <PasswordInput
            value={cfg.ftp_password ?? ""}
            onChange={(v) => set("ftp_password", v)}
            className={`mt-1 ${inputClass} pr-8`}
          />
        </div>
      </div>

      {/* Filters + Retry */}
      <p className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground mb-2">
        Filters &amp; Retry
      </p>
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 mb-4">
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.extensions")}
          </label>
          <input
            type="text"
            defaultValue={cfg.extensions.join(", ")}
            onBlur={(e) =>
              set(
                "extensions",
                e.target.value
                  .split(",")
                  .map((s) => s.trim())
                  .filter(Boolean),
              )
            }
            className={`mt-1 ${inputClass}`}
            placeholder=".dat, .xml"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.vendors")}
          </label>
          <input
            type="text"
            defaultValue={cfg.vendors.join(", ")}
            onBlur={(e) =>
              set(
                "vendors",
                e.target.value
                  .split(",")
                  .map((s) => s.trim())
                  .filter(Boolean),
              )
            }
            className={`mt-1 ${inputClass}`}
            placeholder="nihon-kohden"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.maxAttempts")}
          </label>
          <input
            type="number"
            value={cfg.max_attempts}
            onChange={(e) => set("max_attempts", parseInt(e.target.value) || 3)}
            className={`mt-1 ${inputClass}`}
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.interval")}
          </label>
          <input
            type="text"
            value={cfg.interval}
            onChange={(e) => set("interval", e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="5m"
          />
        </div>
      </div>

      <ConnectorCardActions logic={logic} canSave={!!cfg.name} />
    </div>
  );
}

// ─── PACS DICOM Card (protocol: dicom_cstore) ────────────────────────────────

function PacsCard({
  initial,
  isNew,
  onSaved,
  onDeleted,
}: {
  initial: ConnectorCardState | null;
  isNew: boolean;
  onSaved: () => void;
  onDeleted: () => void;
}) {
  const { t } = useTranslation();
  const [cfg, setCfg] = useState<ConnectorCardState>(initial ?? emptyPacs());

  useEffect(() => {
    if (initial) setCfg(initial);
  }, [initial]);

  const set = <K extends keyof ConnectorCardState>(key: K, value: ConnectorCardState[K]) =>
    setCfg((prev) => ({ ...prev, [key]: value }));

  const logic = useConnectorCardLogic({ cfg, isNew, onSaved, onDeleted });

  const inputClass =
    "w-full text-xs border border-border rounded-md px-2.5 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20";

  const displayName = isNew
    ? t("modules.connectors.pacsDicom") + " — " + t("common.new", { defaultValue: "New" })
    : cfg.name;

  return (
    <div className="bg-card rounded-lg border border-border p-5">
      {/* Header */}
      <div className="flex items-center justify-between mb-5">
        <div className="flex items-center gap-3">
          <Database className="w-5 h-5 text-purple-500" />
          <h2 className="text-sm font-semibold text-foreground">{displayName}</h2>
          <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium bg-purple-500/10 text-purple-600 border border-purple-500/20">
            {t("modules.connectors.pacsDicom")} — DICOM C-STORE
          </span>
        </div>
        <Toggle checked={cfg.enabled} onChange={(v) => set("enabled", v)} />
      </div>

      {/* Common identity fields.
          One field on purpose: ConnectorConfig has a single `name`, which is
          also what logs and metrics carry ("connector":"orthanc"). It used to be
          rendered twice — as "Name" and "Name / label" — with both inputs bound
          to cfg.name, so typing in either updated the other. */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 mb-4">
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.label")}
          </label>
          <input
            type="text"
            value={cfg.name}
            readOnly={!isNew}
            onChange={(e) => set("name", e.target.value)}
            className={`mt-1 ${inputClass} ${!isNew ? "opacity-60 cursor-not-allowed" : ""}`}
            placeholder="orthanc"
          />
        </div>
      </div>

      {/* DICOM section */}
      <p className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground mb-2 mt-1">
        DICOM
      </p>
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 mb-4 pb-4 border-b border-border">
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.dicomHost")}
          </label>
          <input
            type="text"
            value={cfg.dicom_host ?? ""}
            onChange={(e) => set("dicom_host", e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="orthanc.hospital.local"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.dicomPort")}
          </label>
          <input
            type="number"
            value={cfg.dicom_port ?? 104}
            min={1} max={65535}
            onChange={(e) => set("dicom_port", Math.min(65535, Math.max(1, parseInt(e.target.value) || 104)))}
            className={`mt-1 ${inputClass}`}
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.callingAe")}
          </label>
          <input
            type="text"
            value={cfg.calling_ae ?? ""}
            onChange={(e) => set("calling_ae", e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="ECG-HUB"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.calledAe")}
          </label>
          <input
            type="text"
            value={cfg.called_ae ?? ""}
            onChange={(e) => set("called_ae", e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="ORTHANC"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.dicomTimeout")}
          </label>
          <input
            type="text"
            value={cfg.dicom_timeout ?? "30s"}
            onChange={(e) => set("dicom_timeout", e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="30s"
          />
        </div>
      </div>

      {/* Filters + Retry */}
      <p className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground mb-2">
        Filters &amp; Retry
      </p>
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 mb-4">
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.extensions")}
          </label>
          <input
            type="text"
            defaultValue={cfg.extensions.join(", ")}
            onBlur={(e) =>
              set(
                "extensions",
                e.target.value
                  .split(",")
                  .map((s) => s.trim())
                  .filter(Boolean),
              )
            }
            className={`mt-1 ${inputClass}`}
            placeholder=".dcm"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.vendors")}
          </label>
          <input
            type="text"
            defaultValue={cfg.vendors.join(", ")}
            onBlur={(e) =>
              set(
                "vendors",
                e.target.value
                  .split(",")
                  .map((s) => s.trim())
                  .filter(Boolean),
              )
            }
            className={`mt-1 ${inputClass}`}
            placeholder="dicom"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.maxAttempts")}
          </label>
          <input
            type="number"
            value={cfg.max_attempts}
            onChange={(e) => set("max_attempts", parseInt(e.target.value) || 3)}
            className={`mt-1 ${inputClass}`}
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("modules.connectors.interval")}
          </label>
          <input
            type="text"
            value={cfg.interval}
            onChange={(e) => set("interval", e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="5m"
          />
        </div>
      </div>

      <ConnectorCardActions logic={logic} canSave={!!cfg.name} />
    </div>
  );
}

// ─── Connector Section ───────────────────────────────────────────────────────

// NewCardEntry tracks an unsaved connector being created, with its intended type.
interface NewCardEntry {
  id: number;
  connectorType: ConnectorType;
}

function ConnectorSection() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();

  const { data: storedConfigs = [], isLoading } = useQuery({
    queryKey: ["admin", "connectors", "config"],
    queryFn: fetchConnectorConfigs,
    staleTime: 30_000,
  });

  const [newCards, setNewCards] = useState<NewCardEntry[]>([]);
  const nextId = useRef(0);

  const addNew = (connectorType: ConnectorType) => {
    setNewCards((prev) => [...prev, { id: nextId.current++, connectorType }]);
  };

  const removeNew = (id: number) => {
    setNewCards((prev) => prev.filter((n) => n.id !== id));
  };

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ["admin", "connectors", "config"] });
  };

  // Partition stored configs by type
  const polarisConfigs = storedConfigs.filter(
    (e) => connectorTypeFromProtocol(e.config.protocol) === "polaris",
  );
  const pacsConfigs = storedConfigs.filter(
    (e) => connectorTypeFromProtocol(e.config.protocol) === "pacs_dicom",
  );
  const newPolaris = newCards.filter((n) => n.connectorType === "polaris");
  const newPacs = newCards.filter((n) => n.connectorType === "pacs_dicom");

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-base font-semibold text-foreground">
          {t("modules.connectors.title")}
        </h2>
        <div className="flex items-center gap-2">
          <button
            onClick={() => addNew("polaris")}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border border-dashed border-blue-400/50 hover:bg-blue-500/5 transition-colors text-blue-600"
          >
            <Plus className="w-3 h-3" />
            {t("modules.connectors.addPolaris")}
          </button>
          <button
            onClick={() => addNew("pacs_dicom")}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border border-dashed border-purple-400/50 hover:bg-purple-500/5 transition-colors text-purple-600"
          >
            <Plus className="w-3 h-3" />
            {t("modules.connectors.addPacsDicom")}
          </button>
        </div>
      </div>

      {isLoading && (
        <div className="flex justify-center py-4">
          <Spinner size={16} className="text-muted-foreground" />
        </div>
      )}

      {!isLoading && (
        <>
          {/* Polaris connectors */}
          {(polarisConfigs.length > 0 || newPolaris.length > 0) && (
            <div className="space-y-3">
              {polarisConfigs.map((entry) => (
                <PolarisCard
                  key={entry.module_type}
                  initial={{ ...entry.config, enabled: entry.enabled }}
                  isNew={false}
                  onSaved={invalidate}
                  onDeleted={invalidate}
                />
              ))}
              {newPolaris.map((entry) => (
                <PolarisCard
                  key={`new-polaris-${entry.id}`}
                  initial={null}
                  isNew={true}
                  onSaved={() => {
                    removeNew(entry.id);
                    invalidate();
                  }}
                  onDeleted={() => removeNew(entry.id)}
                />
              ))}
            </div>
          )}

          {/* PACS DICOM connectors */}
          {(pacsConfigs.length > 0 || newPacs.length > 0) && (
            <div className="space-y-3">
              {pacsConfigs.map((entry) => (
                <PacsCard
                  key={entry.module_type}
                  initial={{ ...entry.config, enabled: entry.enabled }}
                  isNew={false}
                  onSaved={invalidate}
                  onDeleted={invalidate}
                />
              ))}
              {newPacs.map((entry) => (
                <PacsCard
                  key={`new-pacs-${entry.id}`}
                  initial={null}
                  isNew={true}
                  onSaved={() => {
                    removeNew(entry.id);
                    invalidate();
                  }}
                  onDeleted={() => removeNew(entry.id)}
                />
              ))}
            </div>
          )}

          {storedConfigs.length === 0 && newCards.length === 0 && (
            <p className="text-xs text-muted-foreground py-2">
              No connectors configured. Use the buttons above to add one.
            </p>
          )}
        </>
      )}
    </div>
  );
}

// ─── Vendor Module Activation Section ────────────────────────────────────────

function ModuleActivationSection() {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const queryClient = useQueryClient();
  const [restartNeeded, setRestartNeeded] = useState(false);
  // Local active set — drives the UI immediately without waiting for refetch
  const [localActive, setLocalActive] = useState<string[] | null>(null);
  const [available, setAvailable] = useState<string[]>([]);

  const { data, isLoading } = useQuery({
    queryKey: ["admin", "settings", "modules"],
    queryFn: fetchModuleSettings,
    staleTime: 30_000,
  });

  // Sync from server on first load (only if we haven't made local changes)
  useEffect(() => {
    if (!data) return;
    setAvailable([...data.available].sort());
    if (localActive === null) {
      // Empty from server = all active → represent as full set locally
      setLocalActive(data.active.length === 0 ? data.available : data.active);
    }
  }, [data]); // eslint-disable-line react-hooks/exhaustive-deps

  const saveMutation = useMutation({
    mutationFn: (active: string[]) => saveModuleSettings(active),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "settings", "modules"] });
      setRestartNeeded(true);
    },
    onError: (err: unknown) =>
      notify("error", errorMessage(err, "Error saving module settings")),
  });

  const handleToggle = (moduleName: string, checked: boolean) => {
    if (localActive === null) return;
    let next: string[];
    if (checked) {
      next = localActive.includes(moduleName) ? localActive : [...localActive, moduleName];
    } else {
      next = localActive.filter((n) => n !== moduleName);
    }
    setLocalActive(next);
    // Store as empty if all modules are active (empty = all)
    const allActive = next.length === available.length &&
      [...next].sort().every((n, i) => n === [...available].sort()[i]);
    saveMutation.mutate(allActive ? [] : next);
  };

  const moduleDescription = (name: string): string => {
    const key = `modules.vendorModules.descriptions.${name}`;
    const translated = t(key);
    return translated === key ? name : translated;
  };

  const isModuleActive = (name: string): boolean => {
    if (localActive === null) return true;
    return localActive.includes(name);
  };

  return (
    <div className="bg-card rounded-lg border border-border p-5">
      <div className="flex items-center justify-between mb-5">
        <div>
          <h2 className="text-sm font-semibold text-foreground">
            {t("modules.vendorModules.title")}
          </h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            {t("modules.vendorModules.subtitle")}
          </p>
        </div>
      </div>

      {isLoading && (
        <div className="flex justify-center py-4">
          <Spinner size={16} className="text-muted-foreground" />
        </div>
      )}

      {!isLoading && data && (
        <>
          <div className="space-y-3 mb-4">
            {[...data.available].sort().map((name) => (
              <div key={name} className="flex items-center justify-between py-2 border-b border-border last:border-0">
                <div>
                  <p className="text-xs font-medium text-foreground capitalize">{name}</p>
                  <p className="text-[10px] text-muted-foreground mt-0.5">{moduleDescription(name)}</p>
                </div>
                <Toggle
                  checked={isModuleActive(name)}
                  onChange={(v) => handleToggle(name, v)}
                />
              </div>
            ))}
            {data.available.length === 0 && (
              <p className="text-xs text-muted-foreground py-2">
                {t("modules.vendorModules.all")}
              </p>
            )}
          </div>

          {restartNeeded && (
            <p className="flex items-center gap-1.5 text-[10px] text-success bg-success/5 border border-success/20 rounded-md px-3 py-2">
              <AlertCircle className="w-3 h-3 shrink-0" />
              {t("modules.vendorModules.appliedNotice")}
            </p>
          )}
        </>
      )}
    </div>
  );
}

// ─── Main Page ───────────────────────────────────────────────────────────────

export function AdminModulesPage() {
  const { t } = useTranslation();
  const { hasPermission } = useAuth();

  const { data: statuses = [] } = useQuery({
    queryKey: ["admin", "modules", "status"],
    queryFn: fetchModuleStatuses,
    staleTime: 5_000,
    refetchInterval: 5_000,
  });

  if (!hasPermission("admin.system")) return null;

  const ftpStatus = statuses.find((s) => s.name === "ftp")?.status;
  const dicomStatus = statuses.find((s) => s.name === "dicom")?.status;

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center gap-3">
        <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-primary/10 text-primary ring-1 ring-primary/20">
          <Settings2 className="h-5 w-5" />
        </div>
        <div>
          <h1 className="font-display text-2xl font-semibold tracking-tight text-foreground">
            {t("modules.title")}
          </h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {t("modules.subtitle")}
          </p>
        </div>
      </div>

      <ModuleActivationSection />
      <FTPCard ftpStatus={ftpStatus} />
      <DICOMCard dicomStatus={dicomStatus} />
      <ConnectorSection />
    </div>
  );
}

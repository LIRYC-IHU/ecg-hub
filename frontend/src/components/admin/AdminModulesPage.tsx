import { useState, useEffect, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Eye, EyeOff, Settings2, Play, Square, AlertCircle } from "lucide-react";
import {
  fetchFTPConfig,
  saveFTPConfig,
  fetchDICOMConfig,
  saveDICOMConfig,
  fetchModuleStatuses,
  startModule,
  stopModule,
  type FTPModuleConfig,
  type DICOMModuleConfig,
} from "../../lib/api";
import { Spinner } from "../ui/Spinner";
import { useNotification } from "../../context/NotificationContext";
import { useAuth } from "../../hooks/useAuth";

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
  const [passiveRange, setPassiveRange] = useState("30000-30010");
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
    setPassiveRange(ftpConfig.passive_port_range);
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
        passive_port_range: passiveRange,
        public_host: publicHost,
        tls,
        username,
        password,
        enabled: overrideEnabled !== undefined ? overrideEnabled : enabled,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "modules", "ftp", "config"] });
      initialPort.current = port;
      setPortModified(false);
      notify("success", t("modules.ftp.saveOk"));
    },
    onError: (err: { message?: string; error?: string }) =>
      notify("error", err.error ?? err.message ?? t("modules.ftp.saveError")),
  });

  const startMutation = useMutation({
    mutationFn: () => startModule("ftp"),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "modules", "status"] });
      void queryClient.refetchQueries({ queryKey: ["admin", "modules", "status"] });
      notify("success", t("modules.ftp.startOk"));
    },
    onError: (err: { message?: string }) =>
      notify("error", err.message ?? t("modules.ftp.saveError")),
  });

  const stopMutation = useMutation({
    mutationFn: () => stopModule("ftp"),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "modules", "status"] });
      void queryClient.refetchQueries({ queryKey: ["admin", "modules", "status"] });
      notify("success", t("modules.ftp.stopOk"));
    },
    onError: (err: { message?: string }) =>
      notify("error", err.message ?? t("modules.ftp.saveError")),
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
                onChange={(e) => setPort(parseInt(e.target.value) || 2121)}
                className={`mt-1 ${inputClass}`}
              />
            </div>
            <div>
              <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("modules.ftp.passiveRange")}
              </label>
              <input
                type="text"
                value={passiveRange}
                onChange={(e) => setPassiveRange(e.target.value)}
                className={`mt-1 ${inputClass}`}
                placeholder="30000-30010"
              />
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
    onError: (err: { message?: string; error?: string }) =>
      notify("error", err.error ?? err.message ?? t("modules.dicom.saveError")),
  });

  const startMutation = useMutation({
    mutationFn: () => startModule("dicom"),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "modules", "status"] });
      void queryClient.refetchQueries({ queryKey: ["admin", "modules", "status"] });
      notify("success", t("modules.dicom.startOk"));
    },
    onError: (err: { message?: string; error?: string }) =>
      notify("error", err.error ?? err.message ?? t("modules.dicom.saveError")),
  });

  const stopMutation = useMutation({
    mutationFn: () => stopModule("dicom"),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "modules", "status"] });
      void queryClient.refetchQueries({ queryKey: ["admin", "modules", "status"] });
      notify("success", t("modules.dicom.stopOk"));
    },
    onError: (err: { message?: string }) =>
      notify("error", err.message ?? t("modules.dicom.saveError")),
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
                onChange={(e) => setPort(parseInt(e.target.value) || 11112)}
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
      <div>
        <h1 className="text-lg font-semibold text-foreground">
          {t("modules.title")}
        </h1>
        <p className="text-sm text-muted-foreground mt-1">
          {t("modules.subtitle")}
        </p>
      </div>

      <FTPCard ftpStatus={ftpStatus} />
      <DICOMCard dicomStatus={dicomStatus} />
    </div>
  );
}

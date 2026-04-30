import { useMutation, useQuery } from "@tanstack/react-query";
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
} from "lucide-react";
import { useAdminStats } from "../../hooks/useAdminStats";
import {
  fetchWebhookStatus,
  fetchModules,
  fetchConnectors,
  testWebhook,
} from "../../lib/api";
import { Spinner } from "../ui/Spinner";
import { useNotification } from "../../context/NotificationContext";
import MetricCard from "./Metric";

export function AdminSystemPage() {
  const { t } = useTranslation();
  const { stats, health } = useAdminStats();
  const { notify } = useNotification();

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

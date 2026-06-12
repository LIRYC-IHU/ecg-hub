import { useState } from "react";
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
} from "lucide-react";
import { useAdminStats } from "../../hooks/useAdminStats";
import {
  fetchModules,
  fetchConnectors,
  fetchConnectorConfigs,
  testConnector,
} from "../../lib/api";
import { Spinner } from "../ui/Spinner";
import { useNotification } from "../../context/NotificationContext";
import MetricCard from "./Metric";

// ─── Connector Status Section ────────────────────────────────────────────────

function ConnectorStatusSection({
  configs,
  healthMap,
}: {
  configs: { module_type: string; enabled: boolean; config: import("../../lib/api").ConnectorConfig }[];
  healthMap: Record<string, boolean>;
}) {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const queryClient = useQueryClient();

  return (
    <div>
      <p className="text-[14px] font-semibold uppercase tracking-wide text-foreground mb-3 px-1">
        {t("admin.system.connectors.title")}
      </p>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {configs.map(({ module_type, enabled, config }) => {
          const name = module_type.replace("connector.", "");
          const isDicom = config.protocol === "dicom_cstore";
          const healthOk = healthMap[name];
          const host = isDicom ? config.dicom_host : (config.ectp_host || config.ftp_host);
          const port = isDicom ? config.dicom_port : (config.ectp_port || config.ftp_port);

          return (
            <div key={module_type} className="bg-card rounded-lg border border-border p-4 space-y-2">
              <div className="flex items-center gap-2">
                <Send className="w-4 h-4 text-muted-foreground" />
                <span className="text-sm font-medium text-foreground capitalize flex-1">{name}</span>
                <span className={`text-[10px] font-medium px-2 py-0.5 rounded-full ${
                  isDicom ? "bg-purple-500/10 text-purple-400" : "bg-blue-500/10 text-blue-400"
                }`}>
                  {isDicom ? t("admin.system.connectors.protocolDicom") : t("admin.system.connectors.protocolEctp")}
                </span>
                {!enabled && (
                  <span className="text-[10px] text-muted-foreground bg-muted px-2 py-0.5 rounded-full">
                    {t("admin.system.disabled")}
                  </span>
                )}
              </div>
              <div className="flex items-center gap-4 text-xs">
                {host && port && (
                  <span className="font-mono text-muted-foreground">{host}:{port}</span>
                )}
                {isDicom && config.called_ae && (
                  <span className="font-mono text-muted-foreground">AE: {config.called_ae}</span>
                )}
                {healthMap[name] !== undefined && (
                  <span className={`ml-auto flex items-center gap-1.5 text-[11px] font-medium ${healthOk ? "text-success" : "text-destructive"}`}>
                    <span className={`w-2 h-2 rounded-full ${healthOk ? "bg-success" : "bg-destructive"}`} />
                    {healthOk ? "OK" : "KO"}
                  </span>
                )}
                <button
                  onClick={async () => {
                    try {
                      const res = await testConnector(name);
                      if (res.success) notify("success", `${name}: reachable (${res.latency})`);
                      else notify("error", `${name}: ${res.error ?? "unreachable"}`);
                    } catch { notify("error", `${name}: test failed`); }
                    void queryClient.invalidateQueries({ queryKey: ["admin", "connectors"] });
                  }}
                  className="ml-auto text-[10px] border border-border px-2 py-0.5 rounded hover:bg-muted transition-colors"
                >
                  Ping
                </button>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

export function AdminSystemPage() {
  const { t } = useTranslation();
  const { stats, health } = useAdminStats();
  const { notify } = useNotification();

  const modulesQuery = useQuery({
    queryKey: ["admin", "modules"],
    queryFn: fetchModules,
    staleTime: 60_000,
  });

  // Health check from the running process (legacy config.yaml connectors)
  const connectorsQuery = useQuery({
    queryKey: ["admin", "connectors"],
    queryFn: fetchConnectors,
    staleTime: 30_000,
    refetchInterval: 30_000,
  });

  // Full connector list from DB (includes all UI-configured connectors)
  const connectorConfigsQuery = useQuery({
    queryKey: ["admin", "connectors", "config"],
    queryFn: fetchConnectorConfigs,
    staleTime: 30_000,
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

      {/* Connecteurs Proxy — from DB (all UI-configured connectors) */}
      {connectorConfigsQuery.data && connectorConfigsQuery.data.length > 0 && (
        <ConnectorStatusSection
          configs={connectorConfigsQuery.data}
          healthMap={Object.fromEntries(
            (connectorsQuery.data ?? []).map((c) => [c.name, c.status === "ok"])
          )}
        />
      )}

      {/* Storage */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <MetricCard />

        {/* Webhooks — now per-user, configured from the user menu */}
        <div className="bg-card rounded-lg border border-border p-5">
          <div className="flex items-center gap-3 mb-3">
            <Webhook className="w-5 h-5 text-muted-foreground" />
            <h2 className="text-sm font-semibold text-foreground">
              {t("admin.system.webhook.title")}
            </h2>
          </div>
          <p className="text-xs text-muted-foreground mb-3">
            {t("admin.system.webhook.moved")}
          </p>
          <a
            href="/webhooks"
            className="inline-flex items-center gap-1.5 text-xs border border-border px-3 py-1.5 rounded-lg hover:bg-muted transition-colors"
          >
            {t("admin.system.webhook.open")}
          </a>
        </div>
      </div>
    </div>
  );
}


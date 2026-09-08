import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  AlertTriangle,
  Check,
  HardDrive,
  Radio,
  ShieldOff,
  Trash2,
} from "lucide-react";

import type { Device } from "../../gen/v1/device_pb";
import { deviceClient } from "../../lib/grpc";
import { useDeviceEvents } from "../../hooks/useDeviceEvents";
import { useNotification } from "../../context/NotificationContext";
import { useAuth } from "../../hooks/useAuth";
import { Spinner } from "../ui/Spinner";
import { ConfirmDialog } from "../ui/ConfirmDialog";

type Tab = "pairing" | "devices";

const STATUS_STYLE: Record<string, string> = {
  pending: "bg-warning/10 text-warning",
  approved: "bg-success/10 text-success",
  revoked: "bg-destructive/10 text-destructive",
};

function formatDate(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "—" : d.toLocaleString();
}

// deviceName is what an operator recognises: their own label first, then what
// the vendor module read out of the file the device sent, then the
// manufacturer prefix. The bare MAC is always shown next to it.
function deviceName(d: Device): string {
  return d.label || d.deviceModel || d.vendor || d.oui || d.mac;
}

export function AdminDevicesPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { notify } = useNotification();
  const { hasPermission } = useAuth();
  const canManage = hasPermission("device.manage");

  const [tab, setTab] = useState<Tab>("pairing");
  const [approving, setApproving] = useState<Device | null>(null);
  const [label, setLabel] = useState("");
  const [description, setDescription] = useState("");
  const [revoking, setRevoking] = useState<Device | null>(null);
  const [revokeReason, setRevokeReason] = useState("");
  const [deleting, setDeleting] = useState<Device | null>(null);

  useDeviceEvents(true);

  const { data: settings, isLoading: settingsLoading } = useQuery({
    queryKey: ["devices", "settings"],
    queryFn: () => deviceClient.getSettings({}),
  });

  const { data: list, isLoading } = useQuery({
    queryKey: ["devices", "list"],
    queryFn: () => deviceClient.listDevices({ status: "" }),
  });

  const devices = list?.devices ?? [];
  const pending = devices.filter((d) => d.status === "pending");

  const refresh = () => queryClient.invalidateQueries({ queryKey: ["devices"] });
  const failed = () => notify("error", t("common.error"));

  const saveSettings = useMutation({
    mutationFn: (next: { enabled: boolean; pairingOpen: boolean }) =>
      deviceClient.updateSettings({
        settings: {
          $typeName: "grpc.api.v1.DeviceSettings",
          enabled: next.enabled,
          pairingOpen: next.pairingOpen,
          pairingUntil: "",
        },
      }),
    onSuccess: () => {
      void refresh();
      notify("success", t("admin.devices.settingsSaved"));
    },
    onError: failed,
  });

  const approve = useMutation({
    mutationFn: (mac: string) =>
      deviceClient.approveDevice({ mac, label, description }),
    onSuccess: (res) => {
      void refresh();
      setApproving(null);
      setLabel("");
      setDescription("");
      notify(
        "success",
        res.reingested
          ? t("admin.devices.approvedAndIngested")
          : t("admin.devices.approved"),
      );
    },
    onError: failed,
  });

  const revoke = useMutation({
    mutationFn: (mac: string) =>
      deviceClient.revokeDevice({ mac, reason: revokeReason }),
    onSuccess: () => {
      void refresh();
      setRevoking(null);
      setRevokeReason("");
      notify("success", t("admin.devices.revoked"));
    },
    onError: failed,
  });

  const remove = useMutation({
    mutationFn: (mac: string) => deviceClient.deleteDevice({ mac }),
    onSuccess: () => {
      void refresh();
      setDeleting(null);
      notify("success", t("admin.devices.deleted"));
    },
    onError: failed,
  });

  const enabled = settings?.settings?.enabled ?? false;
  const pairingOpen = settings?.settings?.pairingOpen ?? false;

  return (
    <div className="p-6 space-y-4 max-w-6xl">
      <div>
        <h1 className="text-lg font-semibold flex items-center gap-2">
          <HardDrive className="w-5 h-5" />
          {t("admin.devices.title")}
        </h1>
        <p className="text-xs text-muted-foreground mt-1">
          {t("admin.devices.subtitle")}
        </p>
      </div>

      {/* The whitelist cannot identify hardware on every deployment. Saying so
          here is the point: an administrator who enables it on a routed network
          would otherwise believe they were protected while everything passes. */}
      {settings?.degraded && (
        <div className="flex gap-2 items-start rounded-lg border border-warning/40 bg-warning/10 p-3">
          <AlertTriangle className="w-4 h-4 text-warning shrink-0 mt-0.5" />
          <p className="text-xs text-warning">
            {t("admin.devices.degraded")}
          </p>
        </div>
      )}

      <div className="flex items-center gap-1 border-b border-border pb-2">
        <button
          onClick={() => setTab("pairing")}
          className={`px-3 py-1.5 text-xs font-medium rounded-md transition-colors ${
            tab === "pairing"
              ? "bg-primary/10 text-primary"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          {t("admin.devices.tab.pairing")}
          {pending.length > 0 && (
            <span className="ml-1.5 rounded-full bg-warning/20 text-warning px-1.5 py-0.5 text-[10px]">
              {pending.length}
            </span>
          )}
        </button>
        <button
          onClick={() => setTab("devices")}
          className={`px-3 py-1.5 text-xs font-medium rounded-md transition-colors ${
            tab === "devices"
              ? "bg-primary/10 text-primary"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          {t("admin.devices.tab.devices")}
        </button>
      </div>

      {tab === "pairing" && (
        <div className="space-y-4">
          <div className="bg-card rounded-lg border border-border p-5 space-y-4">
            {settingsLoading ? (
              <Spinner />
            ) : (
              <>
                <Toggle
                  checked={enabled}
                  disabled={!canManage || saveSettings.isPending}
                  onChange={(v) =>
                    saveSettings.mutate({ enabled: v, pairingOpen })
                  }
                  title={t("admin.devices.enable")}
                  hint={t("admin.devices.enableHint")}
                />
                <Toggle
                  checked={pairingOpen}
                  disabled={!canManage || !enabled || saveSettings.isPending}
                  onChange={(v) =>
                    saveSettings.mutate({ enabled, pairingOpen: v })
                  }
                  title={t("admin.devices.pairing")}
                  hint={t("admin.devices.pairingHint")}
                />
              </>
            )}
          </div>

          <div className="bg-card rounded-lg border border-border">
            <div className="px-5 py-3 border-b border-border flex items-center gap-2">
              <Radio className="w-4 h-4 text-muted-foreground" />
              <span className="text-xs font-medium">
                {t("admin.devices.waiting")}
              </span>
            </div>
            {isLoading ? (
              <div className="p-5">
                <Spinner />
              </div>
            ) : pending.length === 0 ? (
              <p className="px-5 py-8 text-center text-xs text-muted-foreground">
                {t("admin.devices.noPending")}
              </p>
            ) : (
              <ul className="divide-y divide-border">
                {pending.map((d) => (
                  <li
                    key={d.mac}
                    className="px-5 py-3 flex items-center gap-4 justify-between"
                  >
                    <div className="min-w-0">
                      <p className="text-sm font-medium truncate">
                        {deviceName(d)}
                      </p>
                      <p className="text-[11px] font-mono text-muted-foreground">
                        {d.mac}
                        {d.serialNumber ? ` · SN ${d.serialNumber}` : ""}
                        {` · ${d.firstSource}`}
                        {d.lastIp ? ` · ${d.lastIp}` : ""}
                      </p>
                      <p className="text-[11px] text-muted-foreground">
                        {t("admin.devices.seen", {
                          count: Number(d.seenCount),
                          at: formatDate(d.lastSeenAt),
                        })}
                        {d.hasHeldFile
                          ? ` · ${t("admin.devices.fileHeld")}`
                          : ""}
                      </p>
                    </div>
                    {canManage && (
                      <div className="flex gap-2 shrink-0">
                        <button
                          onClick={() => {
                            setApproving(d);
                            setLabel(d.label);
                            setDescription(d.description);
                          }}
                          className="inline-flex items-center gap-1 rounded-md bg-success/10 text-success px-2.5 py-1.5 text-xs font-medium hover:bg-success/20"
                        >
                          <Check className="w-3.5 h-3.5" />
                          {t("admin.devices.approve")}
                        </button>
                        <button
                          onClick={() => setDeleting(d)}
                          className="inline-flex items-center gap-1 rounded-md text-muted-foreground px-2.5 py-1.5 text-xs font-medium hover:text-destructive"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                          {t("admin.devices.dismiss")}
                        </button>
                      </div>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}

      {tab === "devices" && (
        <div className="bg-card rounded-lg border border-border overflow-x-auto">
          {isLoading ? (
            <div className="p-5">
              <Spinner />
            </div>
          ) : devices.length === 0 ? (
            <p className="px-5 py-8 text-center text-xs text-muted-foreground">
              {t("admin.devices.none")}
            </p>
          ) : (
            <table className="w-full text-xs">
              <thead className="text-muted-foreground border-b border-border">
                <tr>
                  <Th>{t("admin.devices.col.device")}</Th>
                  <Th>{t("admin.devices.col.mac")}</Th>
                  <Th>{t("admin.devices.col.status")}</Th>
                  <Th>{t("admin.devices.col.source")}</Th>
                  <Th>{t("admin.devices.col.lastSeen")}</Th>
                  <Th>{t("admin.devices.col.approvedBy")}</Th>
                  {canManage && <Th> </Th>}
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {devices.map((d) => (
                  <tr key={d.mac}>
                    <Td>
                      <span className="font-medium">{deviceName(d)}</span>
                      {d.description && (
                        <span className="block text-[11px] text-muted-foreground">
                          {d.description}
                        </span>
                      )}
                    </Td>
                    <Td className="font-mono">{d.mac}</Td>
                    <Td>
                      <span
                        className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${
                          STATUS_STYLE[d.status] ?? "bg-muted"
                        }`}
                      >
                        {t(`admin.devices.status.${d.status}`)}
                      </span>
                      {d.revokedReason && (
                        <span className="block text-[11px] text-muted-foreground">
                          {d.revokedReason}
                        </span>
                      )}
                    </Td>
                    <Td>{d.firstSource || "—"}</Td>
                    <Td>{formatDate(d.lastSeenAt)}</Td>
                    <Td>{d.approvedBy || "—"}</Td>
                    {canManage && (
                      <Td>
                        <div className="flex gap-2 justify-end">
                          {d.status === "approved" && (
                            <button
                              onClick={() => setRevoking(d)}
                              className="inline-flex items-center gap-1 text-muted-foreground hover:text-destructive"
                            >
                              <ShieldOff className="w-3.5 h-3.5" />
                              {t("admin.devices.revoke")}
                            </button>
                          )}
                          <button
                            onClick={() => setDeleting(d)}
                            className="inline-flex items-center gap-1 text-muted-foreground hover:text-destructive"
                          >
                            <Trash2 className="w-3.5 h-3.5" />
                            {t("common.delete")}
                          </button>
                        </div>
                      </Td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}

      {/* Approving is where the operator names the device, so the list stays
          readable once a dozen of them are enrolled. */}
      {approving && (
        <Modal
          title={t("admin.devices.approveTitle", { mac: approving.mac })}
          onClose={() => setApproving(null)}
        >
          <Field
            label={t("admin.devices.label")}
            value={label}
            onChange={setLabel}
            placeholder={t("admin.devices.labelPlaceholder")}
            maxLength={255}
          />
          <Field
            label={t("admin.devices.description")}
            value={description}
            onChange={setDescription}
            maxLength={255}
          />
          {approving.hasHeldFile && (
            <p className="text-[11px] text-muted-foreground">
              {t("admin.devices.willReingest")}
            </p>
          )}
          <div className="flex justify-end gap-2 pt-2">
            <button
              onClick={() => setApproving(null)}
              className="px-3 py-1.5 text-xs rounded-md text-muted-foreground hover:text-foreground"
            >
              {t("common.cancel")}
            </button>
            <button
              onClick={() => approve.mutate(approving.mac)}
              disabled={approve.isPending}
              className="px-3 py-1.5 text-xs rounded-md bg-primary text-primary-foreground disabled:opacity-50"
            >
              {t("admin.devices.approve")}
            </button>
          </div>
        </Modal>
      )}

      {revoking && (
        <Modal
          title={t("admin.devices.revokeTitle", { mac: revoking.mac })}
          onClose={() => setRevoking(null)}
        >
          <Field
            label={t("admin.devices.reason")}
            value={revokeReason}
            onChange={setRevokeReason}
            maxLength={255}
          />
          <div className="flex justify-end gap-2 pt-2">
            <button
              onClick={() => setRevoking(null)}
              className="px-3 py-1.5 text-xs rounded-md text-muted-foreground hover:text-foreground"
            >
              {t("common.cancel")}
            </button>
            <button
              onClick={() => revoke.mutate(revoking.mac)}
              disabled={revoke.isPending}
              className="px-3 py-1.5 text-xs rounded-md bg-destructive text-destructive-foreground disabled:opacity-50"
            >
              {t("admin.devices.revoke")}
            </button>
          </div>
        </Modal>
      )}

      {deleting && (
        <ConfirmDialog
          open
          title={t("admin.devices.deleteTitle")}
          message={t("admin.devices.deleteMessage", { mac: deleting.mac })}
          confirmLabel={t("common.delete")}
          danger
          busy={remove.isPending}
          onConfirm={() => remove.mutate(deleting.mac)}
          onClose={() => setDeleting(null)}
        />
      )}
    </div>
  );
}

function Th({ children }: { children: React.ReactNode }) {
  return <th className="text-left font-medium px-4 py-2">{children}</th>;
}

function Td({
  children,
  className = "",
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return <td className={`px-4 py-2 align-top ${className}`}>{children}</td>;
}

function Toggle({
  checked,
  disabled,
  onChange,
  title,
  hint,
}: {
  checked: boolean;
  disabled?: boolean;
  onChange: (v: boolean) => void;
  title: string;
  hint: string;
}) {
  return (
    <label className="flex items-start gap-3 cursor-pointer">
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
        className="mt-0.5 accent-primary disabled:opacity-40"
      />
      <span>
        <span className="block text-xs font-medium">{title}</span>
        <span className="block text-[11px] text-muted-foreground">{hint}</span>
      </span>
    </label>
  );
}

function Field({
  label,
  value,
  onChange,
  placeholder,
  maxLength,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  maxLength?: number;
}) {
  return (
    <label className="block">
      <span className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        maxLength={maxLength}
        className="mt-1 w-full text-xs border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
      />
    </label>
  );
}

function Modal({
  title,
  children,
  onClose,
}: {
  title: string;
  children: React.ReactNode;
  onClose: () => void;
}) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onClick={onClose}
    >
      <div
        className="bg-card rounded-lg border border-border p-5 w-full max-w-md space-y-3"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="text-sm font-semibold">{title}</h2>
        {children}
      </div>
    </div>
  );
}

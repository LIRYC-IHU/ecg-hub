import { useState, useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Eye, EyeOff, Shield, Trash2 } from "lucide-react";
import {
  fetchAdminAuthProviders,
  saveOIDCConfig,
  saveLDAPConfig,
  deleteAuthProvider,
  testOIDCConnection,
  testLDAPConnection,
  type AuthProviderDTO,
} from "../../lib/api";
import { Spinner } from "../ui/Spinner";
import { useNotification } from "../../context/NotificationContext";
import { useConfirm } from "../../context/ConfirmContext";
import { useAuth } from "../../hooks/useAuth";

// ─── Password Input with toggle ────────────────────────────────────────────

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

// ─── Toggle Switch ──────────────────────────────────────────────────────────

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

// ─── OIDC Card ──────────────────────────────────────────────────────────────

function OIDCCard({ provider }: { provider: AuthProviderDTO | undefined }) {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const confirm = useConfirm();
  const queryClient = useQueryClient();

  const [issuerUrl, setIssuerUrl] = useState("");
  const [internalUrl, setInternalUrl] = useState("");
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [redirectUrl, setRedirectUrl] = useState("");
  const [logoutUrl, setLogoutUrl] = useState("");
  const [tls, setTls] = useState(true);
  const [scopes, setScopes] = useState("profile, email");
  const [usernameClaim, setUsernameClaim] = useState("default");
  const [groupsClaim, setGroupsClaim] = useState("");
  const [active, setActive] = useState(false);

  useEffect(() => {
    if (provider) {
      const cfg = provider.config;
      setIssuerUrl((cfg.issuer_url as string) ?? "");
      setInternalUrl((cfg.internal_url as string) ?? "");
      setClientId((cfg.client_id as string) ?? "");
      setClientSecret((cfg.client_secret as string) ?? "");
      setRedirectUrl((cfg.redirect_url as string) ?? "");
      setLogoutUrl((cfg.logout_url as string) ?? "");
      setTls((cfg.tls as boolean) ?? true);
      setScopes(
        Array.isArray(cfg.scopes) && cfg.scopes.length
          ? (cfg.scopes as string[]).join(", ")
          : "profile, email",
      );
      setUsernameClaim((cfg.username_claim as string) || "default");
      setGroupsClaim((cfg.groups_claim as string) ?? "");
      setActive(provider.active);
    }
  }, [provider]);

  const getConfig = () => ({
    issuer_url: issuerUrl,
    internal_url: internalUrl,
    client_id: clientId,
    client_secret: clientSecret,
    redirect_url: redirectUrl,
    logout_url: logoutUrl,
    tls,
    scopes: scopes.split(",").map((s) => s.trim()).filter(Boolean),
    username_claim: usernameClaim,
    groups_claim: groupsClaim.trim(),
    active,
  });

  const saveMutation = useMutation({
    mutationFn: () => saveOIDCConfig(getConfig()),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "auth-providers"] });
      notify("success", t("admin.authProviders.saved"));
    },
    onError: () => notify("error", t("admin.authProviders.saveError")),
  });

  const testMutation = useMutation({
    mutationFn: () => testOIDCConnection(getConfig()),
    onSuccess: (data) => {
      if (data.success) {
        notify("success", t("admin.authProviders.oidc.testOk"));
      } else {
        notify("error", t("admin.authProviders.oidc.testFail", { error: data.error }));
      }
    },
    onError: () => notify("error", t("admin.authProviders.oidc.testFail", { error: "request failed" })),
  });

  const deleteMutation = useMutation({
    mutationFn: () => deleteAuthProvider(provider!.id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "auth-providers"] });
      notify("success", t("admin.authProviders.deleted"));
    },
  });

  const inputClass =
    "w-full text-xs border border-border rounded-md px-2.5 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20";

  return (
    <div className="bg-card rounded-lg border border-border p-5">
      <div className="flex items-center justify-between mb-5">
        <div className="flex items-center gap-3">
          <Shield className="w-5 h-5 text-muted-foreground" />
          <h2 className="text-sm font-semibold text-foreground">
            {t("admin.authProviders.oidc.title")}
          </h2>
        </div>
        <div className="flex items-center gap-3">
          <span className="text-[10px] text-muted-foreground">
            {active ? t("admin.authProviders.active") : t("admin.authProviders.inactive")}
          </span>
          <Toggle checked={active} onChange={setActive} />
        </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-5">
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.oidc.issuerUrl")}
          </label>
          <input
            type="text"
            value={issuerUrl}
            onChange={(e) => setIssuerUrl(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="https://keycloak.example.com/realms/ecg"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.oidc.internalUrl")}
          </label>
          <input
            type="text"
            value={internalUrl}
            onChange={(e) => setInternalUrl(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="http://keycloak:8080/realms/ecg"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.oidc.clientId")}
          </label>
          <input
            type="text"
            value={clientId}
            onChange={(e) => setClientId(e.target.value)}
            className={`mt-1 ${inputClass}`}
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.oidc.clientSecret")}
          </label>
          <PasswordInput
            value={clientSecret}
            onChange={setClientSecret}
            className={`mt-1 ${inputClass} pr-8`}
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.oidc.redirectUrl")}
          </label>
          <input
            type="text"
            value={redirectUrl}
            onChange={(e) => setRedirectUrl(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="https://ecg-hub.example.com/api/v1/auth/oidc/callback"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.oidc.logoutUrl")}
          </label>
          <input
            type="text"
            value={logoutUrl}
            onChange={(e) => setLogoutUrl(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="https://keycloak.example.com/realms/ecg/protocol/openid-connect/logout"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.oidc.scopes")}
          </label>
          <input
            type="text"
            value={scopes}
            onChange={(e) => setScopes(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="profile, email"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.oidc.usernameClaim")}
          </label>
          <select
            value={usernameClaim}
            onChange={(e) => setUsernameClaim(e.target.value)}
            className={`mt-1 ${inputClass}`}
          >
            <option value="default">{t("admin.authProviders.oidc.usernameClaimDefault")}</option>
            <option value="subject">subject (sub)</option>
            <option value="email">email</option>
            <option value="username">username (preferred_username)</option>
          </select>
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.oidc.groupsClaim")}
          </label>
          <input
            type="text"
            value={groupsClaim}
            onChange={(e) => setGroupsClaim(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="groups"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.oidc.tls")}
          </label>
          <div className="mt-2">
            <Toggle checked={tls} onChange={setTls} />
          </div>
        </div>
      </div>

      <div className="flex items-center gap-2">
        <button
          onClick={() => testMutation.mutate()}
          disabled={testMutation.isPending}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border border-border hover:bg-muted transition-colors disabled:opacity-50"
        >
          {testMutation.isPending && <Spinner size={11} />}
          {testMutation.isPending ? t("admin.authProviders.testing") : t("admin.authProviders.test")}
        </button>
        <button
          onClick={() => saveMutation.mutate()}
          disabled={saveMutation.isPending}
          className="inline-flex items-center gap-1.5 px-4 py-1.5 rounded-lg text-xs font-medium bg-primary text-primary-foreground hover:opacity-90 disabled:opacity-50 transition-opacity"
        >
          {saveMutation.isPending && <Spinner size={11} className="text-primary-foreground" />}
          {t("admin.authProviders.save")}
        </button>
        {provider && (
          <button
            onClick={async () => {
              if (
                await confirm({
                  title: t("common.delete"),
                  message: t("admin.authProviders.deleteConfirm"),
                  danger: true,
                })
              ) {
                deleteMutation.mutate();
              }
            }}
            disabled={deleteMutation.isPending}
            className="ml-auto inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border border-destructive/30 text-destructive hover:bg-destructive/5 transition-colors disabled:opacity-50"
          >
            <Trash2 className="w-3 h-3" />
            {t("admin.authProviders.delete")}
          </button>
        )}
      </div>
    </div>
  );
}

// ─── LDAP Card ──────────────────────────────────────────────────────────────

function LDAPCard({ provider }: { provider: AuthProviderDTO | undefined }) {
  const { t } = useTranslation();
  const { notify } = useNotification();
  const confirm = useConfirm();
  const queryClient = useQueryClient();

  const [host, setHost] = useState("");
  const [port, setPort] = useState(389);
  const [tls, setTls] = useState(false);
  const [baseDn, setBaseDn] = useState("");
  const [userSearchDn, setUserSearchDn] = useState("");
  const [userFilter, setUserFilter] = useState("");
  const [bindDn, setBindDn] = useState("");
  const [bindPassword, setBindPassword] = useState("");
  const [adminGroupDn, setAdminGroupDn] = useState("");
  const [writerGroupDn, setWriterGroupDn] = useState("");
  const [adminUsers, setAdminUsers] = useState("");
  const [usernameAttribute, setUsernameAttribute] = useState("");
  const [uuidAttribute, setUuidAttribute] = useState("objectGUID");
  const [active, setActive] = useState(false);

  useEffect(() => {
    if (provider) {
      const cfg = provider.config;
      setHost((cfg.host as string) ?? "");
      setPort((cfg.port as number) ?? 389);
      setTls((cfg.tls as boolean) ?? false);
      setBaseDn((cfg.base_dn as string) ?? "");
      setUserSearchDn((cfg.user_search_dn as string) ?? "");
      setUserFilter((cfg.user_filter as string) ?? "");
      setBindDn((cfg.bind_dn as string) ?? "");
      setBindPassword((cfg.bind_password as string) ?? "");
      setAdminGroupDn((cfg.admin_group_dn as string) ?? "");
      setWriterGroupDn((cfg.writer_group_dn as string) ?? "");
      setAdminUsers(Array.isArray(cfg.admin_users) ? (cfg.admin_users as string[]).join(", ") : "");
      setUsernameAttribute((cfg.username_attribute as string) ?? "");
      setUuidAttribute((cfg.uuid_attribute as string) || "objectGUID");
      setActive(provider.active);
    }
  }, [provider]);

  const getConfig = () => ({
    host,
    port,
    tls,
    base_dn: baseDn,
    user_search_dn: userSearchDn,
    user_filter: userFilter,
    bind_dn: bindDn,
    bind_password: bindPassword,
    admin_group_dn: adminGroupDn,
    writer_group_dn: writerGroupDn,
    admin_users: adminUsers.split(",").map((s) => s.trim()).filter(Boolean),
    username_attribute: usernameAttribute.trim(),
    uuid_attribute: uuidAttribute.trim(),
    active,
  });

  const saveMutation = useMutation({
    mutationFn: () => saveLDAPConfig(getConfig()),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "auth-providers"] });
      notify("success", t("admin.authProviders.saved"));
    },
    onError: () => notify("error", t("admin.authProviders.saveError")),
  });

  const testMutation = useMutation({
    mutationFn: () => testLDAPConnection(getConfig()),
    onSuccess: (data) => {
      if (data.success) {
        notify("success", t("admin.authProviders.ldap.testOk"));
      } else {
        notify("error", t("admin.authProviders.ldap.testFail", { error: data.error }));
      }
    },
    onError: () => notify("error", t("admin.authProviders.ldap.testFail", { error: "request failed" })),
  });

  const deleteMutation = useMutation({
    mutationFn: () => deleteAuthProvider(provider!.id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "auth-providers"] });
      notify("success", t("admin.authProviders.deleted"));
    },
  });

  const inputClass =
    "w-full text-xs border border-border rounded-md px-2.5 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20";

  return (
    <div className="bg-card rounded-lg border border-border p-5">
      <div className="flex items-center justify-between mb-5">
        <div className="flex items-center gap-3">
          <Shield className="w-5 h-5 text-muted-foreground" />
          <h2 className="text-sm font-semibold text-foreground">
            {t("admin.authProviders.ldap.title")}
          </h2>
        </div>
        <div className="flex items-center gap-3">
          <span className="text-[10px] text-muted-foreground">
            {active ? t("admin.authProviders.active") : t("admin.authProviders.inactive")}
          </span>
          <Toggle checked={active} onChange={setActive} />
        </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 mb-5">
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.ldap.host")}
          </label>
          <input
            type="text"
            value={host}
            onChange={(e) => setHost(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="ldap.example.com"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.ldap.port")}
          </label>
          <input
            type="number"
            value={port}
            onChange={(e) => setPort(parseInt(e.target.value) || 389)}
            className={`mt-1 ${inputClass}`}
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.ldap.tls")}
          </label>
          <div className="mt-2">
            <Toggle checked={tls} onChange={setTls} />
          </div>
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.ldap.baseDn")}
          </label>
          <input
            type="text"
            value={baseDn}
            onChange={(e) => setBaseDn(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="dc=example,dc=com"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.ldap.userSearchDn")}
          </label>
          <input
            type="text"
            value={userSearchDn}
            onChange={(e) => setUserSearchDn(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="ou=users,dc=example,dc=com"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.ldap.userFilter")}
          </label>
          <input
            type="text"
            value={userFilter}
            onChange={(e) => setUserFilter(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="optional — e.g. (sAMAccountName=%s)"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.ldap.usernameAttribute")}
          </label>
          <input
            type="text"
            value={usernameAttribute}
            onChange={(e) => setUsernameAttribute(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="uid"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.ldap.uuidAttribute")}
          </label>
          <input
            type="text"
            value={uuidAttribute}
            onChange={(e) => setUuidAttribute(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="objectGUID"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.ldap.bindDn")}
          </label>
          <input
            type="text"
            value={bindDn}
            onChange={(e) => setBindDn(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="cn=admin,dc=example,dc=com"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.ldap.bindPassword")}
          </label>
          <PasswordInput
            value={bindPassword}
            onChange={setBindPassword}
            className={`mt-1 ${inputClass} pr-8`}
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.ldap.adminGroupDn")}
          </label>
          <input
            type="text"
            value={adminGroupDn}
            onChange={(e) => setAdminGroupDn(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="cn=admins,ou=groups,dc=example,dc=com"
          />
        </div>
        <div>
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.ldap.writerGroupDn")}
          </label>
          <input
            type="text"
            value={writerGroupDn}
            onChange={(e) => setWriterGroupDn(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="cn=writers,ou=groups,dc=example,dc=com"
          />
        </div>
        <div className="md:col-span-2 lg:col-span-1">
          <label className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t("admin.authProviders.ldap.adminUsers")}
          </label>
          <input
            type="text"
            value={adminUsers}
            onChange={(e) => setAdminUsers(e.target.value)}
            className={`mt-1 ${inputClass}`}
            placeholder="admin1, admin2"
          />
        </div>
      </div>

      <div className="flex items-center gap-2">
        <button
          onClick={() => testMutation.mutate()}
          disabled={testMutation.isPending}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border border-border hover:bg-muted transition-colors disabled:opacity-50"
        >
          {testMutation.isPending && <Spinner size={11} />}
          {testMutation.isPending ? t("admin.authProviders.testing") : t("admin.authProviders.test")}
        </button>
        <button
          onClick={() => saveMutation.mutate()}
          disabled={saveMutation.isPending}
          className="inline-flex items-center gap-1.5 px-4 py-1.5 rounded-lg text-xs font-medium bg-primary text-primary-foreground hover:opacity-90 disabled:opacity-50 transition-opacity"
        >
          {saveMutation.isPending && <Spinner size={11} className="text-primary-foreground" />}
          {t("admin.authProviders.save")}
        </button>
        {provider && (
          <button
            onClick={async () => {
              if (
                await confirm({
                  title: t("common.delete"),
                  message: t("admin.authProviders.deleteConfirm"),
                  danger: true,
                })
              ) {
                deleteMutation.mutate();
              }
            }}
            disabled={deleteMutation.isPending}
            className="ml-auto inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border border-destructive/30 text-destructive hover:bg-destructive/5 transition-colors disabled:opacity-50"
          >
            <Trash2 className="w-3 h-3" />
            {t("admin.authProviders.delete")}
          </button>
        )}
      </div>
    </div>
  );
}

// ─── Main Page ──────────────────────────────────────────────────────────────

export function AdminAuthPage() {
  const { t } = useTranslation();
  const { hasPermission } = useAuth();

  const { data: providers = [], isLoading } = useQuery({
    queryKey: ["admin", "auth-providers"],
    queryFn: fetchAdminAuthProviders,
    staleTime: 30_000,
  });

  if (!hasPermission("admin.auth_config")) return null;

  const oidcProvider = providers.find((p) => p.provider_type === "oidc");
  const ldapProvider = providers.find((p) => p.provider_type === "ldap");

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center gap-3">
        <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-primary/10 text-primary ring-1 ring-primary/20">
          <Shield className="h-5 w-5" />
        </div>
        <div>
          <h1 className="font-display text-2xl font-semibold tracking-tight text-foreground">
            {t("admin.authProviders.title")}
          </h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {t("admin.authProviders.subtitle")}
          </p>
        </div>
      </div>

      <p className="text-xs text-muted-foreground bg-muted/50 border border-border rounded-lg px-4 py-2.5">
        {t("admin.authProviders.restartNotice")}
      </p>

      {isLoading && (
        <div className="flex justify-center py-8">
          <Spinner size={18} className="text-muted-foreground" />
        </div>
      )}

      {!isLoading && (
        <div className="space-y-6">
          <OIDCCard provider={oidcProvider} />
          <LDAPCard provider={ldapProvider} />
        </div>
      )}
    </div>
  );
}

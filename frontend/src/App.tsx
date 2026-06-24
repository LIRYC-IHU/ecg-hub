import { useState } from "react";
import { Routes, Route, Navigate, useLocation } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Users,
  Shield,
  FileText,
  Server,
  AlertTriangle,
  Heart,
  Filter,
  KeyRound,
  Settings2,
  Activity,
  Palette,
  UploadCloud,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Spinner } from "./components/ui/Spinner";
import { useAuth } from "./hooks/useAuth";
import { useIngestionEvents } from "./hooks/useIngestionEvents";
import { PatientMasterDetailPage } from "./components/patient/PatientMasterDetailPage";
import { LoginPage } from "./components/LoginPage";
import { SetupPage } from "./components/SetupPage";
import { Header } from "./components/layout/Header";
import { Sidebar } from "./components/layout/Sidebar";
import type { SidebarNavItem } from "./components/layout/Sidebar";
import { AdminUsersPage } from "./components/admin/AdminUsersPage";
import { AdminAuditPage } from "./components/admin/AdminAuditPage";
import { AdminSystemPage } from "./components/admin/AdminSystemPage";
import { AdminRolesPage } from "./components/admin/AdminRolesPage";
import { AdminAppUsersPage } from "./components/admin/AdminAppUsersPage";
import { AdminQuarantinePage } from "./components/admin/AdminQuarantinePage";
import { AdminAuthPage } from "./components/admin/AdminAuthPage";
import { AdminModulesPage } from "./components/admin/AdminModulesPage";
import { AdminHL7Page } from "./components/admin/AdminHL7Page";
import { AdminBrandingPage } from "./components/admin/AdminBrandingPage";
import { ApiKeysPage } from "./components/settings/ApiKeysPage";
import { WebhooksPage } from "./components/settings/WebhooksPage";
import { UploadPage } from "./components/uploads/UploadPage";
import {
  fetchAdminStats,
  fetchECGFilterFacets,
  fetchSetupStatus,
} from "./lib/api";
import type { AllECGFilters } from "./lib/api";
import i18n from "./lib/i18n";

function App() {
  const { status, user, logout, hasPermission } = useAuth();
  const { t, i18n: i18next } = useTranslation();
  const location = useLocation();
  const [globalSearch, setGlobalSearch] = useState("");
  const [filters, setFilters] = useState<
    Pick<
      AllECGFilters,
      "vendor" | "device_model" | "file_format" | "hl7_status" | "from" | "to"
    >
  >({});
  const [filtersOpen, setFiltersOpen] = useState(false);

  const { data: setupStatus, isLoading: setupLoading } = useQuery({
    queryKey: ["setup-status"],
    queryFn: fetchSetupStatus,
    staleTime: 30_000,
    retry: 1,
  });

  const canPatientRead =
    status === "authenticated" && hasPermission("patient.read");

  // Realtime ingestion notifications + cache refresh (no polling) while signed in.
  useIngestionEvents(canPatientRead);
  const { data: facets } = useQuery({
    queryKey: ["ecg-filter-facets"],
    queryFn: fetchECGFilterFacets,
    staleTime: 5 * 60_000,
    enabled: canPatientRead,
  });

  const activeFilterCount = Object.values(filters).filter(Boolean).length;

  const clearFilters = () => setFilters({});

  const canDelete = status === "authenticated" && hasPermission("ecg.delete");
  const canForceHL7 =
    status === "authenticated" && hasPermission("ecg.force_hl7");
  const canSendResult =
    status === "authenticated" && hasPermission("ecg.send_result");
  const canRead = status === "authenticated" && hasPermission("ecg.read");
  const canWrite = status === "authenticated" && hasPermission("ecg.write");
  const canUpload =
    status === "authenticated" && hasPermission("ecg.upload");
  const canViewUsers =
    status === "authenticated" && hasPermission("admin.users");
  const canViewAudit =
    status === "authenticated" && hasPermission("admin.audit");
  const canViewSystem =
    status === "authenticated" && hasPermission("admin.system");
  const canViewRoles =
    status === "authenticated" && hasPermission("admin.roles");
  const canViewBranding =
    status === "authenticated" && hasPermission("admin.branding");
  const canViewQuarantine =
    status === "authenticated" && hasPermission("quarantine.read");
  const canDeleteQuarantine =
    status === "authenticated" && hasPermission("quarantine.delete");
  const canAssignQuarantine =
    status === "authenticated" && hasPermission("quarantine.assign");
  const canViewAuthConfig =
    status === "authenticated" && hasPermission("admin.auth_config");
  const canManageWebhooks =
    status === "authenticated" && hasPermission("webhook.manage");
  const canManageApiKeys =
    status === "authenticated" && hasPermission("apikey.manage");
  const canViewHL7 =
    status === "authenticated" &&
    (hasPermission("hl7.config") || canViewSystem);
  const isAdmin =
    canViewUsers ||
    canViewRoles ||
    canViewBranding ||
    canViewAudit ||
    canViewSystem ||
    canViewQuarantine ||
    canViewAuthConfig ||
    canViewHL7;

  const { data: adminStats } = useQuery({
    queryKey: ["admin", "stats"],
    queryFn: fetchAdminStats,
    staleTime: 60_000,
    enabled: isAdmin,
  });

  const toggleLang = (lang: "fr" | "en") => {
    i18n.changeLanguage(lang);
    localStorage.setItem("lang", lang);
  };

  if (status === "loading" || setupLoading)
    return (
      <div className="min-h-screen bg-background flex flex-col items-center justify-center gap-4">
        <span className="font-semibold text-xl tracking-tight text-foreground">
          ECG Hub
        </span>
        <Spinner size={24} className="text-primary" />
      </div>
    );

  // If not yet initialized, show setup page (or redirect to it)
  if (setupStatus && !setupStatus.initialized) {
    if (location.pathname !== "/setup") {
      return <Navigate to="/setup" replace />;
    }
    return <SetupPage />;
  }

  // If already initialized and user navigates to /setup, redirect to /login
  if (setupStatus?.initialized && location.pathname === "/setup") {
    return <Navigate to="/login" replace />;
  }

  if (status === "unauthenticated") {
    if (location.pathname === "/login") {
      return <LoginPage />;
    }
    return <LoginPage />;
  }

  const sidebarNavItems: SidebarNavItem[] = [
    { to: "/", icon: Heart, labelKey: "nav.patients" },
    canUpload && { to: "/uploads", icon: UploadCloud, labelKey: "nav.uploads" },
    canViewUsers && { to: "/app-users", icon: Users, labelKey: "nav.appUsers" },
    canViewRoles && { to: "/roles", icon: Shield, labelKey: "nav.roles" },
    canViewBranding && { to: "/branding", icon: Palette, labelKey: "nav.branding" },
    canViewAudit && { to: "/audit", icon: FileText, labelKey: "nav.audit" },
    canViewSystem && { to: "/system", icon: Server, labelKey: "nav.system" },
    canViewSystem && {
      to: "/modules-config",
      icon: Settings2,
      labelKey: "nav.modulesConfig",
    },
    canViewHL7 && { to: "/hl7", icon: Activity, labelKey: "nav.hl7" },
    canViewAuthConfig && {
      to: "/auth-config",
      icon: KeyRound,
      labelKey: "nav.authConfig",
    },
    canViewQuarantine && {
      to: "/quarantine",
      icon: AlertTriangle,
      labelKey: "nav.quarantine",
      badge: adminStats?.quarantine_count ?? 0,
    },
  ].filter(Boolean) as SidebarNavItem[];

  return (
    <div className="h-screen overflow-hidden bg-background flex flex-col">
      <Header
        userId={user?.username ?? user?.user_id ?? ""}
        onLogout={logout}
        language={i18next.language}
        onToggleLang={toggleLang}
        canManageWebhooks={canManageWebhooks}
        canManageApiKeys={canManageApiKeys}
      />

      <div className="flex-1 min-h-0 flex overflow-hidden">
        {(isAdmin || canUpload) && <Sidebar navItems={sidebarNavItems} />}

        <div className="flex-1 min-h-0 flex flex-col overflow-hidden">
          <Routes>
            <Route
              path="/"
              element={
                <div className="flex flex-col flex-1 min-h-0 overflow-hidden">
                  {/* Filter bar */}
                  <div className="shrink-0 border-b border-border bg-card/50">
                    <div className="flex items-center gap-3 px-6 py-2">
                      <div className="flex-1" />
                      <button
                        onClick={() => setFiltersOpen((v) => !v)}
                        className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium border transition-colors ${
                          filtersOpen || activeFilterCount > 0
                            ? "bg-primary/10 text-primary border-primary/30"
                            : "text-muted-foreground border-border hover:text-foreground hover:bg-muted/50"
                        }`}
                      >
                        <Filter className="w-3.5 h-3.5" />
                        {t("filters.label")}
                        {activeFilterCount > 0 && (
                          <span className="ml-1 w-4 h-4 rounded-full bg-primary text-primary-foreground text-[10px] flex items-center justify-center">
                            {activeFilterCount}
                          </span>
                        )}
                      </button>
                      {activeFilterCount > 0 && (
                        <button
                          onClick={clearFilters}
                          className="text-xs text-muted-foreground hover:text-foreground transition-colors"
                        >
                          {t("filters.clear")}
                        </button>
                      )}
                    </div>
                    {filtersOpen && (
                      <div className="flex items-center gap-3 px-6 py-2 border-t border-border/50 bg-muted/20 flex-wrap">
                        <select
                          value={filters.vendor ?? ""}
                          onChange={(e) =>
                            setFilters((f) => ({
                              ...f,
                              vendor: e.target.value || undefined,
                            }))
                          }
                          className="text-xs border border-border rounded-md px-2.5 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
                        >
                          <option value="">{t("filters.allVendors")}</option>
                          {facets?.vendors.map((v) => (
                            <option key={v} value={v}>
                              {v}
                            </option>
                          ))}
                        </select>
                        <select
                          value={filters.device_model ?? ""}
                          onChange={(e) =>
                            setFilters((f) => ({
                              ...f,
                              device_model: e.target.value || undefined,
                            }))
                          }
                          className="text-xs border border-border rounded-md px-2.5 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
                        >
                          <option value="">{t("filters.allModels")}</option>
                          {facets?.device_models.map((m) => (
                            <option key={m} value={m}>
                              {m}
                            </option>
                          ))}
                        </select>
                        <select
                          value={filters.file_format ?? ""}
                          onChange={(e) =>
                            setFilters((f) => ({
                              ...f,
                              file_format: e.target.value || undefined,
                            }))
                          }
                          className="text-xs border border-border rounded-md px-2.5 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
                        >
                          <option value="">{t("filters.allFormats")}</option>
                          {facets?.file_formats?.map((fmt) => (
                            <option key={fmt} value={fmt}>
                              .{fmt}
                            </option>
                          ))}
                        </select>
                        <select
                          value={filters.hl7_status ?? ""}
                          onChange={(e) =>
                            setFilters((f) => ({
                              ...f,
                              hl7_status: (e.target.value ||
                                undefined) as AllECGFilters["hl7_status"],
                            }))
                          }
                          className="text-xs border border-border rounded-md px-2.5 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
                        >
                          <option value="">{t("filters.allHL7")}</option>
                          <option value="pending">
                            {t("ecg.status.pending")}
                          </option>
                          <option value="success">
                            {t("ecg.status.success")}
                          </option>
                          <option value="hl7_exhausted">
                            {t("ecg.status.hl7_exhausted")}
                          </option>
                        </select>
                        <div className="flex items-center gap-1.5">
                          <span className="text-[10px] text-muted-foreground">
                            {t("filters.from")}
                          </span>
                          <input
                            type="date"
                            value={filters.from ?? ""}
                            onChange={(e) =>
                              setFilters((f) => ({
                                ...f,
                                from: e.target.value || undefined,
                              }))
                            }
                            className="text-xs border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
                          />
                          <span className="text-[10px] text-muted-foreground">
                            {t("filters.to")}
                          </span>
                          <input
                            type="date"
                            value={filters.to ?? ""}
                            onChange={(e) =>
                              setFilters((f) => ({
                                ...f,
                                to: e.target.value || undefined,
                              }))
                            }
                            className="text-xs border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
                          />
                        </div>
                      </div>
                    )}
                  </div>
                  <div className="flex-1 min-h-0 overflow-hidden">
                    <PatientMasterDetailPage
                      canDelete={canDelete}
                      canForceHL7={canForceHL7}
                      canSendResult={canSendResult}
                      canRead={canRead}
                      canWrite={canWrite}
                      search={globalSearch}
                      onSearchChange={setGlobalSearch}
                      filters={filters}
                    />
                  </div>
                </div>
              }
            />
            {canUpload && (
              <Route
                path="/uploads"
                element={
                  <div className="flex-1 min-h-0 overflow-auto">
                    <UploadPage />
                  </div>
                }
              />
            )}
            {canViewUsers && (
              <>
                <Route
                  path="/app-users"
                  element={
                    <div className="overflow-auto">
                      <AdminAppUsersPage />
                    </div>
                  }
                />
                <Route
                  path="/users"
                  element={
                    <div className="p-6 overflow-auto">
                      <AdminUsersPage />
                    </div>
                  }
                />
              </>
            )}
            {canViewRoles && (
              <Route
                path="/roles"
                element={
                  <div className="overflow-auto">
                    <AdminRolesPage />
                  </div>
                }
              />
            )}
            {canViewBranding && (
              <Route
                path="/branding"
                element={
                  <div className="overflow-auto">
                    <AdminBrandingPage />
                  </div>
                }
              />
            )}
            {canViewAudit && (
              <Route
                path="/audit"
                element={
                  <div className="overflow-auto">
                    <AdminAuditPage />
                  </div>
                }
              />
            )}
            {canViewSystem && (
              <Route
                path="/system"
                element={
                  <div className="p-6 overflow-auto">
                    <AdminSystemPage />
                  </div>
                }
              />
            )}
            {canViewSystem && (
              <Route
                path="/modules-config"
                element={
                  <div className="overflow-auto">
                    <AdminModulesPage />
                  </div>
                }
              />
            )}
            {canViewHL7 && (
              <Route
                path="/hl7"
                element={
                  <div className="overflow-auto">
                    <AdminHL7Page />
                  </div>
                }
              />
            )}
            {canViewAuthConfig && (
              <Route
                path="/auth-config"
                element={
                  <div className="overflow-auto">
                    <AdminAuthPage />
                  </div>
                }
              />
            )}
            {canViewQuarantine && (
              <Route
                path="/quarantine"
                element={
                  <div className="overflow-auto">
                    <AdminQuarantinePage
                      canDelete={canDeleteQuarantine}
                      canAssign={canAssignQuarantine}
                    />
                  </div>
                }
              />
            )}
            {canManageApiKeys && (
              <Route
                path="/api-keys"
                element={
                  <div className="overflow-auto flex-1">
                    <ApiKeysPage />
                  </div>
                }
              />
            )}
            {canManageWebhooks && (
              <Route
                path="/webhooks"
                element={
                  <div className="overflow-auto flex-1">
                    <WebhooksPage />
                  </div>
                }
              />
            )}
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </div>
      </div>
    </div>
  );
}

export default App;

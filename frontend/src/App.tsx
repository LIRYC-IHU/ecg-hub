import { useState } from "react";
import { Routes, Route, Navigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Users,
  Shield,
  FileText,
  Server,
  AlertTriangle,
  Heart,
  List,
  PanelLeftOpen,
  Layers,
  Filter,
  X,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Spinner } from "./components/ui/Spinner";
import { useAuth } from "./hooks/useAuth";
import { EcgTimelinePage } from "./components/patient/EcgTimelinePage";
import { PatientMasterDetailPage } from "./components/patient/PatientMasterDetailPage";
import { PatientGroupedPage } from "./components/patient/PatientGroupedPage";
import { LoginPage } from "./components/LoginPage";
import { Header } from "./components/layout/Header";
import { Sidebar } from "./components/layout/Sidebar";
import type { SidebarNavItem } from "./components/layout/Sidebar";
import { AdminUsersPage } from "./components/admin/AdminUsersPage";
import { AdminAuditPage } from "./components/admin/AdminAuditPage";
import { AdminSystemPage } from "./components/admin/AdminSystemPage";
import { AdminRolesPage } from "./components/admin/AdminRolesPage";
import { AdminAppUsersPage } from "./components/admin/AdminAppUsersPage";
import { AdminQuarantinePage } from "./components/admin/AdminQuarantinePage";
import { fetchAdminStats, fetchECGFilterFacets } from "./lib/api";
import type { AllECGFilters } from "./lib/api";
import i18n from "./lib/i18n";

type PatientView = "timeline" | "master-detail" | "grouped";

function PatientsViewToggle({
  view,
  onChange,
}: {
  view: PatientView;
  onChange: (v: PatientView) => void;
}) {
  return (
    <div className="flex items-center gap-0.5 p-0.5 bg-muted rounded-lg border border-border">
      {(
        [
          { key: "timeline", Icon: List, label: "A · Timeline" },
          { key: "master-detail", Icon: PanelLeftOpen, label: "B · Dossier" },
          { key: "grouped", Icon: Layers, label: "C · Groupé" },
        ] as const
      ).map(({ key, Icon, label }) => (
        <button
          key={key}
          onClick={() => onChange(key)}
          className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs font-medium transition-colors ${
            view === key
              ? "bg-card text-foreground shadow-sm"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          <Icon className="w-3.5 h-3.5" />
          {label}
        </button>
      ))}
    </div>
  );
}

function App() {
  const { status, user, logout, hasPermission } = useAuth();
  const { i18n: i18next } = useTranslation();
  const [patientView, setPatientView] = useState<PatientView>(() => {
    return (localStorage.getItem("ecghub.patientView") as PatientView) ?? "timeline";
  });

  const [globalSearch, setGlobalSearch] = useState("");
  const [filters, setFilters] = useState<Pick<AllECGFilters, "vendor" | "device_model" | "hl7_status" | "from" | "to">>({});
  const [filtersOpen, setFiltersOpen] = useState(false);

  const { data: facets } = useQuery({
    queryKey: ["ecg-filter-facets"],
    queryFn: fetchECGFilterFacets,
    staleTime: 5 * 60_000,
    enabled: status === "authenticated",
  });

  const activeFilterCount = Object.values(filters).filter(Boolean).length;

  const clearFilters = () => setFilters({});

  const handleViewChange = (v: PatientView) => {
    setPatientView(v);
    localStorage.setItem("ecghub.patientView", v);
  };

  const canDelete = status === "authenticated" && hasPermission("ecg.delete");
  const canForceHL7 =
    status === "authenticated" && hasPermission("ecg.force_hl7");
  const canRead = status === "authenticated" && hasPermission("ecg.read");
  const canWrite = status === "authenticated" && hasPermission("ecg.write");
  const canViewUsers =
    status === "authenticated" && hasPermission("admin.users");
  const canViewAudit =
    status === "authenticated" && hasPermission("admin.audit");
  const canViewSystem =
    status === "authenticated" && hasPermission("admin.system");
  const canViewQuarantine =
    status === "authenticated" && hasPermission("quarantine.read");
  const canDeleteQuarantine =
    status === "authenticated" && hasPermission("quarantine.delete");
  const isAdmin =
    canViewUsers || canViewAudit || canViewSystem || canViewQuarantine;

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

  if (status === "loading")
    return (
      <div className="min-h-screen bg-background flex flex-col items-center justify-center gap-4">
        <span className="font-semibold text-xl tracking-tight text-foreground">
          ECG Hub
        </span>
        <Spinner size={24} className="text-primary" />
      </div>
    );
  if (status === "unauthenticated") return <LoginPage />;

  const sidebarNavItems: SidebarNavItem[] = [
    { to: "/", icon: Heart, labelKey: "nav.patients" },
    canViewUsers && { to: "/app-users", icon: Users, labelKey: "nav.appUsers" },
    canViewUsers && { to: "/roles", icon: Shield, labelKey: "nav.roles" },
    canViewAudit && { to: "/audit", icon: FileText, labelKey: "nav.audit" },
    canViewSystem && { to: "/system", icon: Server, labelKey: "nav.system" },
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
        userId={user?.user_id ?? ""}
        onLogout={logout}
        language={i18next.language}
        onToggleLang={toggleLang}
      />

      <div className="flex-1 min-h-0 flex overflow-hidden">
        {isAdmin && <Sidebar navItems={sidebarNavItems} />}

        <div className="flex-1 min-h-0 flex flex-col overflow-hidden">
          <Routes>
            <Route
              path="/"
              element={
                <div className="flex flex-col flex-1 min-h-0 overflow-hidden">
                  {/* View toggle bar */}
                  <div className="shrink-0 border-b border-border bg-card/50">
                    <div className="flex items-center gap-3 px-6 py-2">
                      <PatientsViewToggle
                        view={patientView}
                        onChange={handleViewChange}
                      />
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
                        Filtres
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
                          Effacer
                        </button>
                      )}
                    </div>
                    {filtersOpen && (
                      <div className="flex items-center gap-3 px-6 py-2 border-t border-border/50 bg-muted/20 flex-wrap">
                        <select
                          value={filters.vendor ?? ""}
                          onChange={(e) => setFilters((f) => ({ ...f, vendor: e.target.value || undefined }))}
                          className="text-xs border border-border rounded-md px-2.5 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
                        >
                          <option value="">Tous les appareils</option>
                          {facets?.vendors.map((v) => (
                            <option key={v} value={v}>{v}</option>
                          ))}
                        </select>
                        <select
                          value={filters.device_model ?? ""}
                          onChange={(e) => setFilters((f) => ({ ...f, device_model: e.target.value || undefined }))}
                          className="text-xs border border-border rounded-md px-2.5 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
                        >
                          <option value="">Tous les modèles</option>
                          {facets?.device_models.map((m) => (
                            <option key={m} value={m}>{m}</option>
                          ))}
                        </select>
                        <select
                          value={filters.hl7_status ?? ""}
                          onChange={(e) => setFilters((f) => ({ ...f, hl7_status: (e.target.value || undefined) as AllECGFilters["hl7_status"] }))}
                          className="text-xs border border-border rounded-md px-2.5 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
                        >
                          <option value="">Tous les statuts HL7</option>
                          <option value="pending">En attente</option>
                          <option value="success">Envoyé</option>
                          <option value="hl7_exhausted">Épuisé</option>
                        </select>
                        <div className="flex items-center gap-1.5">
                          <span className="text-[10px] text-muted-foreground">Du</span>
                          <input
                            type="date"
                            value={filters.from ?? ""}
                            onChange={(e) => setFilters((f) => ({ ...f, from: e.target.value || undefined }))}
                            className="text-xs border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
                          />
                          <span className="text-[10px] text-muted-foreground">au</span>
                          <input
                            type="date"
                            value={filters.to ?? ""}
                            onChange={(e) => setFilters((f) => ({ ...f, to: e.target.value || undefined }))}
                            className="text-xs border border-border rounded-md px-2 py-1.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
                          />
                        </div>
                      </div>
                    )}
                  </div>
                  <div className="flex-1 min-h-0 overflow-hidden">
                    {patientView === "timeline" ? (
                      <EcgTimelinePage
                        canDelete={canDelete}
                        canForceHL7={canForceHL7}
                        canRead={canRead}
                        canWrite={canWrite}
                        search={globalSearch}
                        onSearchChange={setGlobalSearch}
                        filters={filters}
                      />
                    ) : patientView === "master-detail" ? (
                      <PatientMasterDetailPage
                        canDelete={canDelete}
                        canForceHL7={canForceHL7}
                        canRead={canRead}
                        canWrite={canWrite}
                        search={globalSearch}
                        onSearchChange={setGlobalSearch}
                        filters={filters}
                      />
                    ) : (
                      <PatientGroupedPage
                        canDelete={canDelete}
                        canForceHL7={canForceHL7}
                        canRead={canRead}
                        canWrite={canWrite}
                        search={globalSearch}
                        onSearchChange={setGlobalSearch}
                        filters={filters}
                      />
                    )}
                  </div>
                </div>
              }
            />
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
                  path="/roles"
                  element={
                    <div className="overflow-auto">
                      <AdminRolesPage />
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
            {canViewQuarantine && (
              <Route
                path="/quarantine"
                element={
                  <div className="overflow-auto">
                    <AdminQuarantinePage canDelete={canDeleteQuarantine} />
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

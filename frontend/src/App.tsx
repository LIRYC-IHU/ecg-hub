import { useState, useEffect, useCallback } from "react";
import { Routes, Route, Navigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Users,
  Shield,
  FileText,
  Server,
  AlertTriangle,
  Heart,
  Search,
  ChevronLeft,
  ChevronRight,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Spinner } from "./components/ui/Spinner";
import { usePatients } from "./hooks/usePatients";
import { useAuth } from "./hooks/useAuth";
import { PatientTable } from "./components/patient/PatientTable";
import { LoginPage } from "./components/LoginPage";
import { ExportFooter } from "./components/export/ExportFooter";
import { Header } from "./components/layout/Header";
import { Sidebar } from "./components/layout/Sidebar";
import type { SidebarNavItem } from "./components/layout/Sidebar";
import { AdminUsersPage } from "./components/admin/AdminUsersPage";
import { AdminAuditPage } from "./components/admin/AdminAuditPage";
import { AdminSystemPage } from "./components/admin/AdminSystemPage";
import { AdminRolesPage } from "./components/admin/AdminRolesPage";
import { AdminAppUsersPage } from "./components/admin/AdminAppUsersPage";
import { AdminQuarantinePage } from "./components/admin/AdminQuarantinePage";
import { fetchAdminStats } from "./lib/api";
import i18n from "./lib/i18n";
import type { PatientFilters } from "./lib/api";

type SortOption =
  | "created_at|desc"
  | "created_at|asc"
  | "last_name|asc"
  | "last_name|desc"
  | "patient_id|asc"
  | "patient_id|desc";

function PatientsPage({
  canDelete,
  canForceHL7,
  canRead,
  canWrite,
}: {
  canDelete: boolean;
  canForceHL7: boolean;
  canRead: boolean;
  canWrite: boolean;
}) {
  const { t } = useTranslation();
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [sort, setSort] = useState<SortOption>("created_at|desc");
  const [selectedECGs, setSelectedECGs] = useState<Set<number>>(new Set());
  const [perPage, setPerPage] = useState<number>(25);
  const [page, setPage] = useState(1);

  useEffect(() => {
    const id = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(id);
  }, [search]);

  // Reset to page 1 when filters change
  useEffect(() => {
    setPage(1);
  }, [debouncedSearch, sort, perPage]);

  const [sortBy, sortOrder] = sort.split("|") as [
    PatientFilters["sort_by"],
    PatientFilters["sort_order"],
  ];
  const filters: PatientFilters = {
    ...(debouncedSearch ? { q: debouncedSearch } : {}),
    sort_by: sortBy,
    sort_order: sortOrder,
    per_page: perPage,
    page,
  };

  const { patients, total, isLoading } = usePatients(filters);

  const handleToggleECG = useCallback((ecgId: number) => {
    setSelectedECGs((prev) => {
      const next = new Set(prev);
      if (next.has(ecgId)) next.delete(ecgId);
      else next.add(ecgId);
      return next;
    });
  }, []);

  const handleToggleMultipleECGs = useCallback(
    (ecgIds: number[], selected: boolean) => {
      setSelectedECGs((prev) => {
        const next = new Set(prev);
        for (const id of ecgIds) {
          if (selected) next.add(id);
          else next.delete(id);
        }
        return next;
      });
    },
    [],
  );

  const handleClearSelection = useCallback(
    () => setSelectedECGs(new Set()),
    [],
  );

  return (
    <div className="relative flex flex-col h-full overflow-hidden">
      <div className="sticky top-0 z-10 bg-card border-b border-border px-6 py-3 shrink-0 flex items-center gap-4">
        <div className="relative flex-1 max-w-md">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
          <input
            autoFocus
            type="search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t("search.placeholder")}
            className="w-full pl-10 pr-4 py-2 text-sm bg-muted/50 border-0 rounded-full focus:outline-none focus:ring-2 focus:ring-ring/20 placeholder:text-muted-foreground/60"
          />
        </div>
        <select
          value={sort}
          onChange={(e) => setSort(e.target.value as SortOption)}
          className="text-sm bg-card border border-border rounded-lg px-3 py-2 text-foreground focus:outline-none focus:ring-2 focus:ring-ring/20"
        >
          <option value="created_at|desc">{t("sort.newest")}</option>
          <option value="created_at|asc">{t("sort.oldest")}</option>
          <option value="last_name|asc">{t("sort.nameAZ")}</option>
          <option value="last_name|desc">{t("sort.nameZA")}</option>
          <option value="patient_id|asc">{t("sort.idAsc")}</option>
          <option value="patient_id|desc">{t("sort.idDesc")}</option>
        </select>
        <select
          value={perPage}
          onChange={(e) => setPerPage(Number(e.target.value))}
          className="text-sm bg-card border border-border rounded-lg px-3 py-2 text-foreground focus:outline-none focus:ring-2 focus:ring-ring/20"
        >
          <option value={25}>25 / page</option>
          <option value={50}>50 / page</option>
          <option value={100}>100 / page</option>
          <option value={200}>200 / page</option>
        </select>
        {isLoading ? (
          <Spinner size={15} className="text-muted-foreground shrink-0" />
        ) : (
          <span className="text-xs text-muted-foreground bg-muted px-3 py-1.5 rounded-full shrink-0">
            {total} patients
          </span>
        )}
      </div>

      <main className="flex-1 overflow-auto px-6 py-4 pb-20">
        <PatientTable
          patients={patients}
          isLoading={isLoading}
          selectedECGs={selectedECGs}
          onToggleECG={handleToggleECG}
          onToggleMultipleECGs={handleToggleMultipleECGs}
          canDelete={canDelete}
          canForceHL7={canForceHL7}
          canRead={canRead}
          canWrite={canWrite}
        />
        {/* Pagination */}
        {!isLoading &&
          total > perPage &&
          (() => {
            const totalPages = Math.ceil(total / perPage);
            return (
              <div className="flex items-center justify-center gap-2 py-4">
                <button
                  onClick={() => setPage((p) => Math.max(1, p - 1))}
                  disabled={page === 1}
                  className="p-2 rounded-lg border border-border hover:bg-muted transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
                >
                  <ChevronLeft className="w-4 h-4" />
                </button>
                {Array.from({ length: totalPages }, (_, i) => i + 1).map(
                  (p) => (
                    <button
                      key={p}
                      onClick={() => setPage(p)}
                      className={`min-w-[32px] h-8 rounded-lg text-sm font-medium transition-colors ${
                        p === page
                          ? "bg-primary text-primary-foreground"
                          : "border border-border hover:bg-muted"
                      }`}
                    >
                      {p}
                    </button>
                  ),
                )}
                <button
                  onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                  disabled={page === totalPages}
                  className="p-2 rounded-lg border border-border hover:bg-muted transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
                >
                  <ChevronRight className="w-4 h-4" />
                </button>
              </div>
            );
          })()}
      </main>

      {selectedECGs.size > 0 && (
        <ExportFooter
          count={selectedECGs.size}
          ecgIds={Array.from(selectedECGs)}
          onClear={handleClearSelection}
        />
      )}
    </div>
  );
}

function App() {
  const { status, user, logout, hasPermission } = useAuth();
  const { i18n: i18next } = useTranslation();

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
    <div className="min-h-screen bg-background flex flex-col">
      <Header
        userId={user?.user_id ?? ""}
        onLogout={logout}
        language={i18next.language}
        onToggleLang={toggleLang}
      />

      <div className="flex-1 flex overflow-hidden">
        {isAdmin && <Sidebar navItems={sidebarNavItems} />}

        <div className="flex-1 flex flex-col overflow-hidden">
          <Routes>
            <Route
              path="/"
              element={
                <PatientsPage
                  canDelete={canDelete}
                  canForceHL7={canForceHL7}
                  canRead={canRead}
                  canWrite={canWrite}
                />
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

import { useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import {
  ChevronDown,
  LogOut,
  Activity,
  Sun,
  Monitor,
  Moon,
  KeyRound,
  Webhook,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { ChangePasswordDialog } from "../settings/ChangePasswordDialog";
import { useTheme } from "../../hooks/useTheme";
import { useBranding } from "../../hooks/useBranding";

interface HeaderProps {
  userId: string;
  onLogout: () => void;
  language: string;
  onToggleLang: (lang: "fr" | "en") => void;
  canManageWebhooks?: boolean;
  canManageApiKeys?: boolean;
  /** Only local accounts own a password here; IdP-backed ones change it there. */
  canChangePassword?: boolean;
}

const BREADCRUMBS: Record<string, string> = {
  "/": "nav.patients",
  "/app-users": "nav.appUsers",
  "/roles": "nav.roles",
  "/audit": "nav.audit",
  "/system": "nav.system",
  "/modules-config": "nav.modulesConfig",
  "/branding": "nav.branding",
  "/hl7": "nav.hl7",
  "/auth-config": "nav.authConfig",
  "/quarantine": "nav.quarantine",
  "/api-keys": "nav.apiKeys",
  "/webhooks": "nav.webhooks",
};

function initials(userId: string): string {
  const parts = userId.replace(/[._@]/g, " ").trim().split(/\s+/);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return userId.slice(0, 2).toUpperCase();
}

export function Header({
  userId,
  onLogout,
  language,
  onToggleLang,
  canManageWebhooks = false,
  canManageApiKeys = false,
  canChangePassword = false,
}: HeaderProps) {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [passwordOpen, setPasswordOpen] = useState(false);
  const { theme, setTheme } = useTheme();
  const { logoBase64, hasLogo } = useBranding();

  const breadcrumb = t(BREADCRUMBS[location.pathname] ?? "");

  return (
    <header className="h-14 bg-card border-b border-border shadow-sm flex items-center px-4 relative z-40 shrink-0">
      {/* Left: Logo */}
      <div className="flex items-center gap-3">
        <div className="w-8 h-8 rounded flex items-center justify-center overflow-hidden shrink-0">
          {hasLogo ? (
            <img
              src={logoBase64}
              alt="logo"
              className="w-full h-full object-contain"
            />
          ) : (
            <Activity className="w-5 h-5 text-primary" />
          )}
        </div>
        <span className="font-semibold text-base text-foreground tracking-tight">
          ECG Hub
        </span>
        {userId === "admin" && (
          <span className="text-[10px] font-medium bg-primary/10 text-primary px-2 py-0.5 rounded-full">
            Admin
          </span>
        )}
      </div>

      {/* Center: Breadcrumb */}
      <div className="flex-1 flex justify-center">
        <span className="text-sm text-muted-foreground">{breadcrumb}</span>
      </div>

      {/* Right: Lang + User */}
      <div className="flex items-center gap-3">
        {/* Language toggle */}
        <div className="flex bg-muted rounded-full p-0.5">
          {(["fr", "en"] as const).map((l) => (
            <button
              key={l}
              onClick={() => {
                if (language !== l) onToggleLang(l);
              }}
              className={`text-xs font-medium px-3 py-1 rounded-full transition-all cursor-pointer ${
                language === l
                  ? "bg-card text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground"
              }`}
            >
              {l.toUpperCase()}
            </button>
          ))}
        </div>

        {/* Theme toggle */}
        <div className="flex bg-muted rounded-full p-0.5">
          {[
            { value: "light" as const, Icon: Sun },
            { value: "system" as const, Icon: Monitor },
            { value: "dark" as const, Icon: Moon },
          ].map(({ value, Icon }) => (
            <button
              key={value}
              onClick={() => setTheme(value)}
              className={`p-1.5 rounded-full transition-all cursor-pointer ${
                theme === value
                  ? "bg-card text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground"
              }`}
              aria-label={value}
            >
              <Icon className="w-3.5 h-3.5" />
            </button>
          ))}
        </div>

        <div className="w-px h-6 bg-border" />

        {/* User menu */}
        <div className="relative">
          <button
            onClick={() => setOpen((o) => !o)}
            className="flex items-center gap-2 hover:bg-muted rounded-lg px-2 py-1.5 cursor-pointer transition-colors"
          >
            <div className="w-7 h-7 rounded-full bg-primary/15 flex items-center justify-center">
              <span className="text-xs font-semibold text-primary">
                {initials(userId)}
              </span>
            </div>
            <ChevronDown className="w-3.5 h-3.5 text-muted-foreground" />
          </button>

          {open && (
            <>
              <div
                className="fixed inset-0 z-40"
                onClick={() => setOpen(false)}
              />
              <div className="absolute right-0 top-full mt-1 w-48 bg-card border border-border rounded-lg shadow-lg z-50 py-1">
                <div className="px-3 py-2 border-b border-border">
                  <p className="text-xs font-medium text-foreground font-mono">
                    {userId}
                  </p>
                </div>
                {canManageApiKeys && (
                  <button
                    onClick={() => {
                      setOpen(false);
                      navigate("/api-keys");
                    }}
                    className="w-full flex items-center gap-2 px-3 py-2 text-xs text-foreground hover:bg-muted transition-colors cursor-pointer"
                  >
                    <KeyRound className="w-3.5 h-3.5" />
                    {t("nav.apiKeys")}
                  </button>
                )}
                {canChangePassword && (
                  <button
                    onClick={() => {
                      setOpen(false);
                      setPasswordOpen(true);
                    }}
                    className="w-full flex items-center gap-2 px-3 py-2 text-xs text-foreground hover:bg-muted transition-colors cursor-pointer"
                  >
                    <KeyRound className="w-3.5 h-3.5" />
                    {t("auth.changePassword")}
                  </button>
                )}
                {canManageWebhooks && (
                  <button
                    onClick={() => {
                      setOpen(false);
                      navigate("/webhooks");
                    }}
                    className="w-full flex items-center gap-2 px-3 py-2 text-xs text-foreground hover:bg-muted transition-colors cursor-pointer"
                  >
                    <Webhook className="w-3.5 h-3.5" />
                    {t("nav.webhooks")}
                  </button>
                )}
                <button
                  onClick={() => {
                    setOpen(false);
                    onLogout();
                  }}
                  className="w-full flex items-center gap-2 px-3 py-2 text-xs text-destructive hover:bg-destructive/5 transition-colors"
                >
                  <LogOut className="w-3.5 h-3.5" />
                  {t("auth.logout")}
                </button>
              </div>
            </>
          )}
        </div>
      </div>

      {passwordOpen && (
        <ChangePasswordDialog onClose={() => setPasswordOpen(false)} />
      )}
    </header>
  );
}

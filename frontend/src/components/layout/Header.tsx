import { useState } from "react";
import { useLocation } from "react-router-dom";
import { ChevronDown, LogOut, Activity, Sun, Monitor, Moon } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useTheme } from "../../hooks/useTheme";

interface HeaderProps {
  userId: string;
  onLogout: () => void;
  language: string;
  onToggleLang: (lang: "fr" | "en") => void;
}

const BREADCRUMBS: Record<string, string> = {
  "/": "nav.patients",
  "/app-users": "nav.appUsers",
  "/roles": "nav.roles",
  "/audit": "nav.audit",
  "/system": "nav.system",
  "/quarantine": "nav.quarantine",
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
}: HeaderProps) {
  const { t } = useTranslation();
  const location = useLocation();
  const [open, setOpen] = useState(false);
  const { theme, setTheme } = useTheme();

  const breadcrumb = t(BREADCRUMBS[location.pathname] ?? "");

  return (
    <header className="h-14 bg-card border-b border-border shadow-sm flex items-center px-4 relative z-40 shrink-0">
      {/* Left: Logo */}
      <div className="flex items-center gap-3">
        <Activity className="w-5 h-5 text-primary" />
        <span className="font-semibold text-base text-foreground tracking-tight">
          ECG Hub
        </span>
        <span className="text-[10px] font-medium bg-primary/10 text-primary px-2 py-0.5 rounded-full">
          Admin
        </span>
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
              className={`text-xs font-medium px-3 py-1 rounded-full transition-all ${
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
          {([
            { value: "light" as const, Icon: Sun },
            { value: "system" as const, Icon: Monitor },
            { value: "dark" as const, Icon: Moon },
          ]).map(({ value, Icon }) => (
            <button
              key={value}
              onClick={() => setTheme(value)}
              className={`p-1.5 rounded-full transition-all ${
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
            className="flex items-center gap-2 hover:bg-muted rounded-lg px-2 py-1.5 transition-colors"
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
    </header>
  );
}

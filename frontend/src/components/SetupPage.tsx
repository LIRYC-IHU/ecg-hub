import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { Activity, Lock, User, Loader2, ShieldCheck } from "lucide-react";
import { setupAdmin } from "../lib/api";
import { useNotification } from "../context/NotificationContext";
import { useBranding } from "../hooks/useBranding";

export function SetupPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { centerName, logoBase64, hasLogo } = useBranding();
  const { notify } = useNotification();

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(false);
  const [serverError, setServerError] = useState("");

  function validate(): boolean {
    const newErrors: Record<string, string> = {};

    if (username.length < 3) {
      newErrors.username = t("setup.usernameTooShort");
    }
    if (password.length < 8) {
      newErrors.password = t("setup.passwordTooShort");
    } else if (!/\d/.test(password)) {
      newErrors.password = t("setup.passwordNeedsDigit");
    }
    if (password !== confirmPassword) {
      newErrors.confirmPassword = t("setup.passwordMismatch");
    }

    setErrors(newErrors);
    return Object.keys(newErrors).length === 0;
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setServerError("");

    if (!validate()) return;

    setLoading(true);
    try {
      await setupAdmin(username, password);
      await queryClient.invalidateQueries({ queryKey: ["setup-status"] });
      notify("success", t("setup.success"));
      navigate("/login", { replace: true });
    } catch (err: unknown) {
      const message =
        err && typeof err === "object" && "message" in err
          ? String((err as { message: string }).message)
          : t("common.error");
      setServerError(message);
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="min-h-screen flex">
      {/* Left branding panel */}
      <div className="w-[40%] bg-primary flex flex-col items-center justify-center px-12 relative overflow-hidden">
        {/* ECG pattern overlay */}
        <div className="absolute inset-0 opacity-10">
          <svg
            className="w-full h-full"
            viewBox="0 0 400 400"
            preserveAspectRatio="xMidYMid slice"
          >
            <defs>
              <pattern
                id="ecg-pattern-setup"
                x="0"
                y="0"
                width="100"
                height="50"
                patternUnits="userSpaceOnUse"
              >
                <polyline
                  points="0,25 20,25 25,10 30,40 35,15 40,35 45,25 100,25"
                  fill="none"
                  stroke="white"
                  strokeWidth="1.5"
                />
              </pattern>
            </defs>
            <rect width="100%" height="100%" fill="url(#ecg-pattern-setup)" />
          </svg>
        </div>

        <div className="relative z-10 text-center">
          <div className="w-16 h-16 bg-primary-foreground/20 rounded-2xl flex items-center justify-center mx-auto mb-6 overflow-hidden">
            {hasLogo
              ? <img src={logoBase64} alt="logo" className="w-full h-full object-contain p-1" />
              : <Activity className="w-8 h-8 text-primary-foreground" />
            }
          </div>
          <h1 className="text-3xl font-bold text-primary-foreground mb-3">
            ECG Hub
          </h1>
          <p className="text-primary-foreground/70 text-sm leading-relaxed max-w-xs">
            {t("setup.subtitle")}
          </p>
          <div className="mt-8 flex items-center justify-center gap-2 text-primary-foreground/50 text-xs">
            <ShieldCheck className="w-3.5 h-3.5" />
            <span>{centerName}</span>
          </div>
        </div>
      </div>

      {/* Right form panel */}
      <div className="flex-1 bg-card flex items-center justify-center px-16">
        <div className="w-full max-w-sm">
          <h2 className="text-xl font-semibold text-foreground mb-1">
            {t("setup.title")}
          </h2>
          <p className="text-sm text-muted-foreground mb-8">
            {t("setup.subtitle")}
          </p>

          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label className="text-xs font-medium text-muted-foreground mb-1.5 block">
                {t("setup.username")}
              </label>
              <div className="relative">
                <User className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
                <input
                  autoFocus
                  type="text"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  placeholder="admin"
                  className="w-full pl-10 pr-4 py-2.5 text-sm bg-background border border-input rounded-lg focus:outline-none focus:ring-2 focus:ring-ring/30 focus:border-primary transition-all placeholder:text-muted-foreground/50"
                  required
                />
              </div>
              {errors.username && (
                <p className="text-xs text-destructive mt-1">
                  {errors.username}
                </p>
              )}
            </div>

            <div>
              <label className="text-xs font-medium text-muted-foreground mb-1.5 block">
                {t("setup.password")}
              </label>
              <div className="relative">
                <Lock className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
                <input
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="••••••••"
                  className="w-full pl-10 pr-4 py-2.5 text-sm bg-background border border-input rounded-lg focus:outline-none focus:ring-2 focus:ring-ring/30 focus:border-primary transition-all placeholder:text-muted-foreground/50"
                  required
                />
              </div>
              {errors.password && (
                <p className="text-xs text-destructive mt-1">
                  {errors.password}
                </p>
              )}
            </div>

            <div>
              <label className="text-xs font-medium text-muted-foreground mb-1.5 block">
                {t("setup.confirmPassword")}
              </label>
              <div className="relative">
                <Lock className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
                <input
                  type="password"
                  value={confirmPassword}
                  onChange={(e) => setConfirmPassword(e.target.value)}
                  placeholder="••••••••"
                  className="w-full pl-10 pr-4 py-2.5 text-sm bg-background border border-input rounded-lg focus:outline-none focus:ring-2 focus:ring-ring/30 focus:border-primary transition-all placeholder:text-muted-foreground/50"
                  required
                />
              </div>
              {errors.confirmPassword && (
                <p className="text-xs text-destructive mt-1">
                  {errors.confirmPassword}
                </p>
              )}
            </div>

            {serverError && (
              <div className="bg-destructive/5 border border-destructive/20 rounded-lg px-3 py-2">
                <p className="text-xs text-destructive">{serverError}</p>
              </div>
            )}

            <button
              type="submit"
              disabled={loading}
              className="w-full bg-primary text-primary-foreground font-medium text-sm py-2.5 px-4 rounded-lg hover:bg-primary/90 transition-colors disabled:opacity-50 flex items-center justify-center gap-2"
            >
              {loading && <Loader2 className="w-4 h-4 animate-spin" />}
              {loading ? t("common.loading") : t("setup.submit")}
            </button>
          </form>

          <p className="text-[10px] text-muted-foreground text-center mt-8">
            {centerName} — ECG Hub
          </p>
        </div>
      </div>
    </div>
  );
}

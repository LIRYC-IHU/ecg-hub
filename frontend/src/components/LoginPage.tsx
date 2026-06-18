import { useState, useEffect, useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";
import {
  Activity,
  Lock,
  User,
  Loader2,
  ShieldCheck,
  ArrowRight,
} from "lucide-react";
import { gsap } from "gsap";
import { useGSAP } from "@gsap/react";
import { MotionPathPlugin } from "gsap/MotionPathPlugin";
import { fetchAuthProviders, loginWithLDAP, loginWithLocal } from "../lib/api";
import { useBranding } from "../hooks/useBranding";

gsap.registerPlugin(useGSAP, MotionPathPlugin);

const FONT_STACK = "'Outfit', system-ui, -apple-system, sans-serif";

/**
 * One real heartbeat — Lead II, extracted from an actual 12-lead FDA aECG
 * recording (500 Hz). It was denoised (0.5 Hz high-pass for baseline wander,
 * 40 Hz low-pass, Savitzky-Golay smoothing), then averaged across the 14 clean
 * beats of the strip (outlier beats rejected by correlation) to yield this
 * representative template. Normalized so the isoelectric baseline = 0 and the
 * R-wave peak = 1. Positive values point upward.
 */
const REAL_BEAT: number[] = [
  0.001, -0.001, -0.0, 0.001, 0.002, 0.003, -0.002, -0.001, 0.002, 0.002, 0.0,
  -0.002, -0.006, -0.009, -0.008, -0.01, -0.012, -0.011, -0.011, -0.01, -0.012,
  -0.014, -0.018, -0.018, -0.019, -0.018, -0.016, -0.012, -0.006, -0.001, 0.003,
  0.008, 0.012, 0.013, 0.017, 0.018, 0.023, 0.029, 0.037, 0.042, 0.045, 0.047,
  0.044, 0.033, 0.02, 0.013, 0.007, 0.003, -0.005, -0.013, -0.022, -0.032,
  -0.036, -0.035, -0.035, -0.037, -0.041, -0.042, -0.045, -0.042, -0.034,
  -0.037, -0.054, -0.079, -0.098, -0.086, -0.035, 0.073, 0.253, 0.491, 0.741,
  0.929, 0.991, 0.862, 0.577, 0.251, 0.006, -0.118, -0.135, -0.117, -0.086,
  -0.069, -0.058, -0.048, -0.038, -0.035, -0.03, -0.027, -0.025, -0.021, -0.021,
  -0.025, -0.024, -0.022, -0.016, -0.012, -0.009, -0.009, -0.007, -0.004, 0.0,
  0.005, 0.01, 0.016, 0.019, 0.023, 0.028, 0.032, 0.033, 0.038, 0.038, 0.042,
  0.047, 0.055, 0.058, 0.059, 0.059, 0.058, 0.057, 0.064, 0.071, 0.076, 0.078,
  0.077, 0.077, 0.084, 0.087, 0.088, 0.09, 0.087, 0.082, 0.078, 0.073, 0.068,
  0.059, 0.048, 0.041, 0.034, 0.026, 0.019, 0.012, 0.009, 0.009, 0.011, 0.015,
  0.016, 0.016, 0.019, 0.014, 0.01, 0.01, 0.009, 0.008, 0.008, 0.008, 0.009,
  0.006, 0.005, 0.007, 0.007, 0.007, 0.005, 0.002, 0.002, 0.003, 0.002, -0.0,
  -0.003, -0.004, -0.002, -0.001, -0.001, -0.004, -0.007, -0.006, -0.007,
  -0.008, -0.009, -0.007, -0.01,
];

/**
 * Tiles the real heartbeat across the viewBox to form a rhythm strip, mapped to
 * SVG coordinates (up = negative y). Densely sampled, so it doubles as a GSAP
 * draw-on stroke and a MotionPath target for the leading pulse dot.
 */
function buildEcgPath(width: number, height: number, beats: number): string {
  const mid = height / 2;
  const ampScale = height * 0.4; // pixels for a full-amplitude (R = 1) wave
  const n = REAL_BEAT.length;
  const total = beats * n;
  const pts: Array<[number, number]> = [];
  for (let i = 0; i <= total; i++) {
    const v = REAL_BEAT[i % n];
    pts.push([(i / total) * width, mid - v * ampScale]);
  }
  return pts
    .map(([px, py], i) => `${i === 0 ? "M" : "L"}${px.toFixed(1)},${py.toFixed(1)}`)
    .join(" ");
}

export function LoginPage() {
  const { t } = useTranslation();
  const { centerName, logoBase64, hasLogo } = useBranding();
  const [providers, setProviders] = useState<string[]>([]);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const rootRef = useRef<HTMLDivElement>(null);
  const traceRef = useRef<SVGPathElement>(null);
  const dotRef = useRef<SVGCircleElement>(null);

  const ecgPath = useMemo(() => buildEcgPath(1200, 560, 5), []);

  useEffect(() => {
    fetchAuthProviders().then(setProviders);
  }, []);

  const hasOIDC = providers.includes("oidc");
  const hasLDAP = providers.includes("ldap");
  const hasLocal = providers.includes("local");
  const hasCredentialForm = hasLDAP || hasLocal;

  async function handleCredentialSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setLoading(true);
    try {
      if (hasLocal && !hasLDAP) {
        await loginWithLocal(username, password);
      } else {
        await loginWithLDAP(username, password);
      }
      window.location.reload();
    } catch {
      setError(t("auth.invalidCredentials"));
    } finally {
      setLoading(false);
    }
  }

  // Entrance choreography + looping heartbeat trace.
  useGSAP(
    () => {
      const tl = gsap.timeline({ defaults: { ease: "power3.out" } });
      tl.from(".reveal", {
        y: 28,
        opacity: 0,
        duration: 0.9,
        stagger: 0.09,
      });

      // Ambient drift on the glow blobs.
      gsap.to(".glow-a", {
        x: 60,
        y: -40,
        scale: 1.15,
        duration: 9,
        repeat: -1,
        yoyo: true,
        ease: "sine.inOut",
      });
      gsap.to(".glow-b", {
        x: -50,
        y: 50,
        scale: 1.1,
        duration: 11,
        repeat: -1,
        yoyo: true,
        ease: "sine.inOut",
      });

      // Live monitor trace: the bright line draws on progressively while the
      // pulse dot rides its leading edge, then both fade and the loop restarts.
      if (traceRef.current && dotRef.current) {
        const path = traceRef.current;
        const dot = dotRef.current;
        const len = path.getTotalLength();
        // Hide the line before the first frame so it never flashes fully drawn.
        gsap.set(path, { strokeDasharray: len, strokeDashoffset: len });

        const beat = gsap.timeline({ repeat: -1 });
        beat
          // Reset to "nothing drawn" at the top of every loop.
          .set(path, { strokeDashoffset: len, opacity: 1 })
          .set(dot, { opacity: 1 })
          // Reveal the line and move the dot together, perfectly in sync.
          .to(path, { strokeDashoffset: 0, duration: 4.2, ease: "none" }, 0)
          .to(
            dot,
            {
              motionPath: {
                path,
                align: path,
                alignOrigin: [0.5, 0.5],
              },
              duration: 4.2,
              ease: "none",
            },
            0,
          )
          // Hold a beat, then fade both out before the next sweep.
          .to([path, dot], { opacity: 0, duration: 0.6 }, ">+0.35");
      }
    },
    { scope: rootRef },
  );

  return (
    <div
      ref={rootRef}
      className="relative min-h-screen w-full max-w-full overflow-hidden flex flex-col lg:flex-row bg-slate-950"
      style={{ fontFamily: FONT_STACK }}
    >
      {/* ============ LEFT: cinematic monitor panel ============ */}
      <div className="relative flex w-full lg:w-[58%] flex-col justify-between overflow-hidden px-8 py-12 sm:px-12 lg:px-16 lg:py-16">
        {/* Layered backdrop: deep slate gradient + radial glows + grain */}
        <div
          className="absolute inset-0"
          style={{
            background:
              "radial-gradient(120% 120% at 0% 0%, #0b1220 0%, #070b16 55%, #05070f 100%)",
          }}
        />
        <div className="glow-a absolute -left-24 top-10 h-[34rem] w-[34rem] rounded-full bg-blue-600/25 blur-[120px]" />
        <div className="glow-b absolute bottom-[-8rem] left-1/3 h-[28rem] w-[28rem] rounded-full bg-cyan-500/15 blur-[120px]" />
        <div
          className="pointer-events-none absolute inset-0 opacity-[0.06] mix-blend-overlay"
          style={{
            backgroundImage:
              "url(\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='120' height='120'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.85' numOctaves='3'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23n)'/%3E%3C/svg%3E\")",
          }}
        />

        {/* Full-bleed animated ECG trace sitting behind the copy */}
        <svg
          className="pointer-events-none absolute inset-x-0 top-1/2 -translate-y-1/2 h-[60%] w-full opacity-70"
          viewBox="0 0 1200 560"
          fill="none"
          preserveAspectRatio="xMidYMid slice"
        >
          <defs>
            <linearGradient id="trace-grad" x1="0" y1="0" x2="1" y2="0">
              <stop offset="0%" stopColor="#38bdf8" />
              <stop offset="100%" stopColor="#67e8f9" />
            </linearGradient>
            <filter id="trace-glow" x="-20%" y="-50%" width="140%" height="200%">
              <feGaussianBlur stdDeviation="6" result="b" />
              <feMerge>
                <feMergeNode in="b" />
                <feMergeNode in="SourceGraphic" />
              </feMerge>
            </filter>
          </defs>
          {/* faint static baseline grid line */}
          <line
            x1="0"
            y1="280"
            x2="1200"
            y2="280"
            stroke="#1e293b"
            strokeWidth="1"
            strokeDasharray="2 10"
          />
          {/* Bright trace: draws on progressively, in sync with the dot.
              Nothing is shown ahead of the dot; it resets each loop. */}
          <path
            ref={traceRef}
            d={ecgPath}
            stroke="url(#trace-grad)"
            strokeWidth="3"
            strokeLinecap="round"
            strokeLinejoin="round"
            filter="url(#trace-glow)"
          />
          <circle
            ref={dotRef}
            r="7"
            fill="#a5f3fc"
            filter="url(#trace-glow)"
          />
        </svg>

        {/* Brand mark */}
        <div className="reveal relative z-10 flex items-center gap-3">
          <div className="flex h-12 w-12 items-center justify-center overflow-hidden rounded-2xl border border-white/10 bg-white/5 backdrop-blur">
            {hasLogo ? (
              <img
                src={logoBase64}
                alt="logo"
                className="h-full w-full object-contain p-1.5"
              />
            ) : (
              <Activity className="h-6 w-6 text-cyan-300" />
            )}
          </div>
          <span className="text-lg font-semibold tracking-tight text-white">
            ECG&nbsp;Hub
          </span>
        </div>

        {/* Editorial headline — max-w-xl guarantees a 2-3 line horizontal flow */}
        <div className="relative z-10 my-auto py-16">
          <h1
            className="reveal max-w-xl font-semibold leading-[1.05] tracking-tight text-white"
            style={{ fontSize: "clamp(2.5rem, 4vw, 4rem)" }}
          >
            ECG Hub
          </h1>
          <p className="reveal mt-6 max-w-md text-base leading-relaxed text-slate-300/80">
            Gestion centralisée des électrocardiogrammes
          </p>
        </div>

        {/* Trust line */}
        <div className="reveal relative z-10 flex items-center gap-2 text-sm text-slate-400">
          <ShieldCheck className="h-4 w-4 text-cyan-400/80" />
          <span>{centerName}</span>
        </div>
      </div>

      {/* ============ RIGHT: clean, legible form panel ============ */}
      <div className="relative z-10 flex w-full flex-1 items-center justify-center bg-card px-6 py-12 sm:px-10 lg:px-16">
        <div className="w-full max-w-sm">
          <div className="reveal mb-8">
            <h2 className="text-2xl font-semibold tracking-tight text-foreground">
              {t("auth.login")}
            </h2>
            <p className="mt-1.5 text-sm text-muted-foreground">
              Accédez à votre espace de gestion ECG
            </p>
          </div>

          {providers.length === 0 && (
            <div className="reveal flex items-center justify-center py-8">
              <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
            </div>
          )}

          {hasOIDC && (
            <a
              href="/api/v1/auth/oidc/login"
              className="reveal group flex w-full items-center justify-center gap-2 rounded-xl bg-primary px-4 py-3 text-sm font-medium text-primary-foreground shadow-sm transition-all duration-300 hover:shadow-lg hover:shadow-primary/25 hover:brightness-110 active:scale-[0.98]"
            >
              <ShieldCheck className="h-4 w-4" />
              {t("auth.ssoLogin")}
              <ArrowRight className="h-4 w-4 transition-transform duration-300 group-hover:translate-x-0.5" />
            </a>
          )}

          {hasOIDC && hasCredentialForm && (
            <div className="reveal my-6 flex items-center gap-3">
              <div className="h-px flex-1 bg-border" />
              <span className="text-xs text-muted-foreground">ou</span>
              <div className="h-px flex-1 bg-border" />
            </div>
          )}

          {hasCredentialForm && (
            <form
              onSubmit={handleCredentialSubmit}
              className="reveal space-y-4"
            >
              <div>
                <label className="mb-1.5 block text-xs font-medium text-muted-foreground">
                  {t("auth.username")}
                </label>
                <div className="group relative">
                  <User className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground transition-colors group-focus-within:text-primary" />
                  <input
                    autoFocus={!hasOIDC}
                    type="text"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    placeholder="nom.utilisateur"
                    className="w-full rounded-xl border border-input bg-background py-3 pl-10 pr-4 text-sm transition-all placeholder:text-muted-foreground/50 focus:border-primary focus:outline-none focus:ring-2 focus:ring-ring/25"
                    required
                  />
                </div>
              </div>

              <div>
                <label className="mb-1.5 block text-xs font-medium text-muted-foreground">
                  {t("auth.password")}
                </label>
                <div className="group relative">
                  <Lock className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground transition-colors group-focus-within:text-primary" />
                  <input
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="••••••••"
                    className="w-full rounded-xl border border-input bg-background py-3 pl-10 pr-4 text-sm transition-all placeholder:text-muted-foreground/50 focus:border-primary focus:outline-none focus:ring-2 focus:ring-ring/25"
                    required
                  />
                </div>
              </div>

              {error && (
                <div className="rounded-xl border border-destructive/20 bg-destructive/5 px-3 py-2.5">
                  <p className="text-xs text-destructive">{error}</p>
                </div>
              )}

              <button
                type="submit"
                disabled={loading}
                className="group flex w-full items-center justify-center gap-2 rounded-xl bg-foreground px-4 py-3 text-sm font-medium text-background transition-all duration-300 hover:brightness-110 active:scale-[0.98] disabled:opacity-50"
              >
                {loading && <Loader2 className="h-4 w-4 animate-spin" />}
                {loading ? t("auth.signingIn") : t("auth.login")}
                {!loading && (
                  <ArrowRight className="h-4 w-4 transition-transform duration-300 group-hover:translate-x-0.5" />
                )}
              </button>
            </form>
          )}

          <p className="reveal mt-10 text-center text-[11px] text-muted-foreground">
            {centerName} — ECG Hub
          </p>
        </div>
      </div>
    </div>
  );
}

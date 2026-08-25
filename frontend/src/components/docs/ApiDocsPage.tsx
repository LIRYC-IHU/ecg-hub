import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Copy, Check, Search } from "lucide-react";
import { API_SECTIONS, type ApiEndpoint, type HttpMethod } from "../../lib/apiDocs";

/**
 * REST API reference for machine clients — the page that replaced the Swagger UI.
 *
 * Everything a webhook receiver needs on one scrollable page: how to
 * authenticate, what arrives in the payload, how to verify the signature, and
 * every endpoint it can call back. The endpoint list lives in lib/apiDocs.ts;
 * this file only renders it.
 */

const methodColors: Record<HttpMethod, string> = {
  GET: "bg-primary/10 text-primary ring-primary/20",
  POST: "bg-success/10 text-success ring-success/20",
  PUT: "bg-warning/10 text-warning ring-warning/20",
  DELETE: "bg-destructive/10 text-destructive ring-destructive/20",
};

function CodeBlock({ code, label }: { code: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="relative rounded-lg border border-border bg-background/60">
      {label && (
        <div className="px-3 pt-2 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
          {label}
        </div>
      )}
      <button
        type="button"
        onClick={() => {
          void navigator.clipboard.writeText(code);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        }}
        className="absolute top-2 right-2 p-1.5 rounded text-muted-foreground hover:text-foreground hover:bg-muted/60"
        aria-label="copy"
      >
        {copied ? <Check className="w-3.5 h-3.5 text-success" /> : <Copy className="w-3.5 h-3.5" />}
      </button>
      <pre className="overflow-x-auto px-3 py-2.5 text-[11px] leading-relaxed font-mono text-foreground/90">
        {code}
      </pre>
    </div>
  );
}

function EndpointCard({ endpoint, origin }: { endpoint: ApiEndpoint; origin: string }) {
  const { t } = useTranslation();
  const sample = `curl -H "X-API-Key: $ECG_HUB_KEY" \\\n  "${origin}${endpoint.path.replace(/\{(\w+)\}/g, "<$1>")}"`;
  return (
    <section id={endpoint.id} className="scroll-mt-6 rounded-xl border border-border bg-card p-5">
      <div className="flex flex-wrap items-center gap-2">
        <span
          className={`px-2 py-0.5 rounded text-[11px] font-bold ring-1 ${methodColors[endpoint.method]}`}
        >
          {endpoint.method}
        </span>
        <code className="text-sm font-mono text-foreground">{endpoint.path}</code>
        {endpoint.permission && (
          <span className="ml-auto text-[10px] font-mono px-2 py-0.5 rounded bg-muted text-muted-foreground">
            {endpoint.permission}
          </span>
        )}
      </div>

      <h3 className="mt-3 text-sm font-semibold text-foreground">{endpoint.summary}</h3>
      {endpoint.description && (
        <p className="mt-1 text-xs text-muted-foreground leading-relaxed">{endpoint.description}</p>
      )}

      {endpoint.params && endpoint.params.length > 0 && (
        <div className="mt-4 overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="text-left text-[10px] uppercase tracking-wide text-muted-foreground">
                <th className="pb-1.5 pr-3 font-semibold">{t("apiDocs.paramName")}</th>
                <th className="pb-1.5 pr-3 font-semibold">{t("apiDocs.paramIn")}</th>
                <th className="pb-1.5 pr-3 font-semibold">{t("apiDocs.paramType")}</th>
                <th className="pb-1.5 font-semibold">{t("apiDocs.paramDesc")}</th>
              </tr>
            </thead>
            <tbody className="align-top">
              {endpoint.params.map((p) => (
                <tr key={p.name} className="border-t border-border/60">
                  <td className="py-1.5 pr-3 font-mono text-foreground whitespace-nowrap">
                    {p.name}
                    {p.required && <span className="text-destructive"> *</span>}
                  </td>
                  <td className="py-1.5 pr-3 text-muted-foreground">{p.in}</td>
                  <td className="py-1.5 pr-3 font-mono text-muted-foreground">{p.type}</td>
                  <td className="py-1.5 text-muted-foreground">{p.description}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="mt-4 grid gap-3 lg:grid-cols-2">
        <CodeBlock label={t("apiDocs.request")} code={sample} />
        {endpoint.response ? (
          <CodeBlock label={t("apiDocs.response")} code={endpoint.response} />
        ) : (
          endpoint.responseNote && (
            <div className="rounded-lg border border-border bg-background/60 px-3 py-2.5">
              <div className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t("apiDocs.response")}
              </div>
              <p className="mt-1 text-xs text-muted-foreground">{endpoint.responseNote}</p>
            </div>
          )
        )}
      </div>
      {endpoint.response && endpoint.responseNote && (
        <p className="mt-2 text-[11px] text-muted-foreground">{endpoint.responseNote}</p>
      )}
    </section>
  );
}

export function ApiDocsPage() {
  const { t } = useTranslation();
  const [query, setQuery] = useState("");
  const origin = typeof window === "undefined" ? "https://ecg-hub.example.org" : window.location.origin;

  const sections = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return API_SECTIONS;
    return API_SECTIONS.map((s) => ({
      ...s,
      endpoints: s.endpoints.filter(
        (e) =>
          e.path.toLowerCase().includes(q) ||
          e.summary.toLowerCase().includes(q) ||
          e.method.toLowerCase() === q,
      ),
    })).filter((s) => s.endpoints.length > 0);
  }, [query]);

  const payloadSample = `POST /your/endpoint  HTTP/1.1
Content-Type: application/json
X-ECG-Hub-Event: ecg.ingested
X-ECG-Hub-Timestamp: 2026-08-24T11:33:50Z
X-ECG-Hub-Webhook-ID: 2683ad89-2b2d-47a6-aa87-b8c99405ede9
X-ECG-Hub-Signature: sha256=9fc6053a297a3878c20ba92f8eb211e24052c6d165602dd959ca169a75ed0f42

{
  "event": "ecg.ingested",
  "webhook_id": "2683ad89-2b2d-47a6-aa87-b8c99405ede9",
  "timestamp": "2026-08-24T11:33:50Z",
  "data": {
    "ecg_id": "6b958f3e-359b-4ac1-987f-3c78d8e2814c",
    "patient_id": "BS1016",
    "vendor": "philips",
    "filename": "philips-BS1016.xml"
  },
  "links": {
    "ecg_metadata": "${origin}/api/v1/ecgs/6b958f3e-359b-4ac1-987f-3c78d8e2814c/metadata",
    "ecg_download": "${origin}/api/v1/ecgs/6b958f3e-359b-4ac1-987f-3c78d8e2814c/download",
    "ecg_waveform": "${origin}/api/v1/ecgs/6b958f3e-359b-4ac1-987f-3c78d8e2814c/waveform",
    "patient_ecgs": "${origin}/api/v1/patients/BS1016/ecgs"
  }
}`;

  const verifySample = `import hmac, hashlib

def verify(body: bytes, header: str, secret: str) -> bool:
    expected = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(header, expected)   # compare on the raw body`;

  const authSample = `# preferred for machine clients
curl -H "X-API-Key: ecghub_…" ${origin}/api/v1/ecgs

# equivalent
curl -H "Authorization: Bearer ecghub_…" ${origin}/api/v1/ecgs`;

  return (
    <div className="flex min-h-full">
      {/* Sidebar */}
      <nav className="hidden lg:block w-60 shrink-0 border-r border-border p-4 sticky top-0 self-start max-h-screen overflow-y-auto">
        <div className="relative mb-3">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("apiDocs.searchPlaceholder")}
            className="w-full pl-8 pr-2 py-1.5 text-xs bg-background border border-border rounded-md focus:outline-none focus:ring-1 focus:ring-ring/20"
          />
        </div>
        <a href="#getting-started" className="block px-2 py-1 text-xs text-muted-foreground hover:text-foreground rounded hover:bg-muted/50">
          {t("apiDocs.gettingStarted")}
        </a>
        <a href="#webhook-payload" className="block px-2 py-1 text-xs text-muted-foreground hover:text-foreground rounded hover:bg-muted/50">
          {t("apiDocs.payloadTitle")}
        </a>
        {sections.map((s) => (
          <div key={s.id} className="mt-3">
            <div className="px-2 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
              {s.title}
            </div>
            {s.endpoints.map((e) => (
              <a
                key={e.id}
                href={`#${e.id}`}
                className="flex items-center gap-1.5 px-2 py-1 rounded hover:bg-muted/50 group"
              >
                <span className={`text-[9px] font-bold w-10 shrink-0 ${methodColors[e.method].split(" ")[1]}`}>
                  {e.method}
                </span>
                <span className="text-[11px] text-muted-foreground group-hover:text-foreground truncate">
                  {e.summary}
                </span>
              </a>
            ))}
          </div>
        ))}
      </nav>

      {/* Content */}
      <div className="flex-1 min-w-0 p-6 space-y-5 max-w-5xl">
        <header>
          <h1 className="text-2xl font-bold text-foreground">{t("apiDocs.title")}</h1>
          <p className="mt-1 text-sm text-muted-foreground">{t("apiDocs.subtitle")}</p>
        </header>

        <section id="getting-started" className="scroll-mt-6 rounded-xl border border-border bg-card p-5 space-y-3">
          <h2 className="text-sm font-semibold text-foreground">{t("apiDocs.gettingStarted")}</h2>
          <p className="text-xs text-muted-foreground leading-relaxed">{t("apiDocs.authIntro")}</p>
          <CodeBlock code={authSample} />
          <p className="text-xs text-muted-foreground leading-relaxed">{t("apiDocs.permIntro")}</p>
        </section>

        <section id="webhook-payload" className="scroll-mt-6 rounded-xl border border-border bg-card p-5 space-y-3">
          <h2 className="text-sm font-semibold text-foreground">{t("apiDocs.payloadTitle")}</h2>
          <p className="text-xs text-muted-foreground leading-relaxed">{t("apiDocs.payloadIntro")}</p>
          <CodeBlock label={t("apiDocs.delivery")} code={payloadSample} />
          <p className="text-xs text-muted-foreground leading-relaxed">{t("apiDocs.signatureIntro")}</p>
          <CodeBlock label="python" code={verifySample} />
          <p className="text-xs text-muted-foreground leading-relaxed">{t("apiDocs.retryIntro")}</p>
        </section>

        {sections.map((s) => (
          <div key={s.id} className="space-y-3">
            <div className="pt-2">
              <h2 className="text-lg font-semibold text-foreground">{s.title}</h2>
              {s.blurb && <p className="mt-1 text-xs text-muted-foreground leading-relaxed">{s.blurb}</p>}
            </div>
            {s.endpoints.map((e) => (
              <EndpointCard key={e.id} endpoint={e} origin={origin} />
            ))}
          </div>
        ))}

        {sections.length === 0 && (
          <p className="text-sm text-muted-foreground">{t("apiDocs.noMatch")}</p>
        )}
      </div>
    </div>
  );
}

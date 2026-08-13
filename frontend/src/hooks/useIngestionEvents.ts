import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNotification } from "../context/NotificationContext";
import { eventClient } from "../lib/grpc";

// IngestionEvent mirrors the gRPC Event message (snake_case app view).
interface IngestionEvent {
  type: "ecg.ingested" | "ecg.unidentified" | "ecg.quarantined" | "ecg.duplicate";
  ecg_id?: string;
  patient_id?: string;
  quarantine_id?: string;
  vendor?: string;
  filename?: string;
  reason?: string;
  at?: string;
}

// useIngestionEvents keeps a single gRPC server-stream (EventService.Subscribe)
// open while enabled. On each ingestion event it refreshes the relevant
// react-query caches (so mounted lists update without polling) and raises a
// notification:
//   - valid ECG  → green, links to the patient/ECG that just arrived
//   - unidentified / quarantined → orange, links to the quarantine review queue
//
// The stream authenticates via the HttpOnly "jwt" cookie (sent automatically by
// the Connect transport's credentials:"include").
export function useIngestionEvents(enabled: boolean): void {
  const queryClient = useQueryClient();
  const { notify } = useNotification();
  const { t } = useTranslation();

  // Keep the latest callbacks in refs so the socket isn't torn down on every
  // render or language change — the effect depends only on `enabled`.
  const handlersRef = useRef({ queryClient, notify, t });
  handlersRef.current = { queryClient, notify, t };

  useEffect(() => {
    if (!enabled) return;

    const abort = new AbortController();
    let cancelled = false;

    function handle(ev: IngestionEvent) {
      // keepalive frames only keep proxies from idling the stream out — ignore.
      if ((ev.type as string) === "keepalive") return;
      const { queryClient, notify, t } = handlersRef.current;
      // The patients list refreshes for every event (a new ECG may add a patient
      // or change counts). invalidate is cheap when the query isn't mounted.
      void queryClient.invalidateQueries({ queryKey: ["patients"] });
      // Admin stats feed the sidebar counters (e.g. the quarantine badge), so they
      // must refresh on every ingestion event.
      void queryClient.invalidateQueries({ queryKey: ["admin", "stats"] });

      const label = ev.filename ?? ev.vendor ?? "ECG";
      if (ev.type === "ecg.duplicate") {
        // Re-sent file already ingested — nothing changed, but stay visible:
        // a silent skip looks like a lost file to the operator.
        notify(
          "warn",
          t("events.duplicate", { label, patient: ev.patient_id ?? "?" }),
          ev.patient_id
            ? {
                label: t("events.view"),
                href: `/?patient=${encodeURIComponent(ev.patient_id)}`,
              }
            : undefined,
        );
        return;
      }
      if (ev.type === "ecg.ingested") {
        if (ev.patient_id) {
          void queryClient.invalidateQueries({
            queryKey: ["ecgs", ev.patient_id],
          });
        }
        const href =
          `/?patient=${encodeURIComponent(ev.patient_id ?? "")}` +
          (ev.ecg_id ? `&ecg=${encodeURIComponent(ev.ecg_id)}` : "");
        notify(
          "success",
          t("events.ingested", { vendor: ev.vendor || "", label }),
          { label: t("events.view"), href },
        );
      } else {
        void queryClient.invalidateQueries({
          queryKey: ["admin", "quarantine"],
        });
        const key =
          ev.type === "ecg.unidentified"
            ? "events.unidentified"
            : "events.quarantined";
        notify("warn", t(key, { vendor: ev.vendor || "", label }), {
          label: t("events.review"),
          href: "/quarantine",
        });
      }
    }

    // Consume the server-stream with exponential-backoff reconnection. A clean
    // stream end (proxy idle timeout, server restart) simply reconnects; an
    // AbortError from unmount stops the loop.
    async function run() {
      let retries = 0;
      while (!cancelled) {
        try {
          for await (const ev of eventClient.subscribe(
            {},
            { signal: abort.signal },
          )) {
            retries = 0;
            handle({
              type: ev.type as IngestionEvent["type"],
              ecg_id: ev.ecgId,
              patient_id: ev.patientId,
              quarantine_id: ev.quarantineId,
              vendor: ev.vendor,
              filename: ev.filename,
              reason: ev.reason,
              at: ev.at,
            });
          }
        } catch {
          // fall through to reconnect (AbortError is filtered by `cancelled`)
        }
        if (cancelled) break;
        const delay = Math.min(Math.pow(2, retries) * 1000, 15000);
        retries = Math.min(retries + 1, 6);
        await new Promise((r) => setTimeout(r, delay));
      }
    }

    void run();
    return () => {
      cancelled = true;
      abort.abort();
    };
  }, [enabled]);
}

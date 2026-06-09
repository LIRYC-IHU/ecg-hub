import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNotification } from "../context/NotificationContext";

// IngestionEvent mirrors the backend events.Event JSON payload.
interface IngestionEvent {
  type: "ecg.ingested" | "ecg.unidentified" | "ecg.quarantined";
  ecg_id?: string;
  patient_id?: string;
  quarantine_id?: string;
  vendor?: string;
  filename?: string;
  reason?: string;
  at?: string;
}

// useIngestionEvents keeps a single WebSocket to /api/v1/events/ws open while
// enabled. On each ingestion event it refreshes the relevant react-query caches
// (so mounted lists update without polling) and raises a notification:
//   - valid ECG  → green, links to the patient/ECG that just arrived
//   - unidentified / quarantined → orange, links to the quarantine review queue
//
// The WebSocket authenticates via the HttpOnly "jwt" cookie (sent automatically).
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

    let ws: WebSocket | null = null;
    let cancelled = false;
    let retries = 0;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;

    function handle(ev: IngestionEvent) {
      const { queryClient, notify, t } = handlersRef.current;
      // The patients list refreshes for every event (a new ECG may add a patient
      // or change counts). invalidate is cheap when the query isn't mounted.
      void queryClient.invalidateQueries({ queryKey: ["patients"] });
      // Admin stats feed the sidebar counters (e.g. the quarantine badge), so they
      // must refresh on every ingestion event.
      void queryClient.invalidateQueries({ queryKey: ["admin", "stats"] });

      const label = ev.filename ?? ev.vendor ?? "ECG";
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

    function connect() {
      const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
      ws = new WebSocket(
        `${protocol}//${window.location.host}/api/v1/events/ws`,
      );

      ws.onopen = () => {
        retries = 0;
      };
      ws.onmessage = (e) => {
        try {
          handle(JSON.parse(e.data as string) as IngestionEvent);
        } catch {
          // ignore malformed frames
        }
      };
      ws.onclose = (e) => {
        if (cancelled || e.code === 1000) return;
        if (retries < 6) {
          const delay = Math.min(Math.pow(2, retries) * 1000, 15000);
          retries++;
          retryTimer = setTimeout(connect, delay);
        }
      };
    }

    connect();
    return () => {
      cancelled = true;
      if (retryTimer) clearTimeout(retryTimer);
      ws?.close(1000, "unmount");
    };
  }, [enabled]);
}

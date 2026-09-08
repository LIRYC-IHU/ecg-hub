import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNotification } from "../context/NotificationContext";
import { deviceClient } from "../lib/grpc";

// useDeviceEvents keeps a DeviceService.SubscribeDevices server-stream open
// while enabled, refreshing the device list as hardware appears and changes.
//
// The pairing screen has to be live: a device is enrolled by plugging it in and
// having it send one ECG, so the operator is standing at the machine, not at
// the browser. Polling would make them wait for the next tick to see whether
// the device they just powered on has arrived.
//
// Mirrors useIngestionEvents: same reconnection loop, same reason for the refs
// (the stream must not be torn down on a re-render or a language change).
export function useDeviceEvents(enabled: boolean): void {
  const queryClient = useQueryClient();
  const { notify } = useNotification();
  const { t } = useTranslation();

  const handlersRef = useRef({ queryClient, notify, t });
  handlersRef.current = { queryClient, notify, t };

  useEffect(() => {
    if (!enabled) return;

    const abort = new AbortController();
    let cancelled = false;

    async function run() {
      let retries = 0;
      while (!cancelled) {
        try {
          for await (const ev of deviceClient.subscribeDevices(
            {},
            { signal: abort.signal },
          )) {
            retries = 0;
            // keepalive frames only keep proxies from idling the stream out.
            if (ev.type === "keepalive") continue;
            const { queryClient, notify, t } = handlersRef.current;
            void queryClient.invalidateQueries({ queryKey: ["devices"] });
            if (ev.type === "device.pending" && ev.device) {
              const d = ev.device;
              notify(
                "warn",
                t("admin.devices.event.pending", {
                  label: d.deviceModel || d.vendor || d.oui || d.mac,
                  mac: d.mac,
                }),
              );
            }
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

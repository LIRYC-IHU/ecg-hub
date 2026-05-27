import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchBranding } from "../lib/api";

export const BRANDING_FALLBACK = "IHU Liryc — Bordeaux";

export function useBranding() {
  const { data } = useQuery({
    queryKey: ["branding"],
    queryFn: fetchBranding,
    staleTime: 5 * 60_000,
  });

  const logoBase64 = data?.logo_base64 || "";
  const centerName = data?.center_name || BRANDING_FALLBACK;

  // Keep favicon and tab title in sync with branding settings.
  useEffect(() => {
    document.title = `ECG Hub — ${centerName}`;
    if (logoBase64) {
      const favicon = document.getElementById("app-favicon") as HTMLLinkElement | null;
      if (favicon) {
        favicon.href = logoBase64;
        // Derive mime type from data URI prefix, fall back to png.
        const mime = logoBase64.match(/^data:([^;]+);/)?.[1] ?? "image/png";
        favicon.type = mime;
      }
    }
  }, [logoBase64, centerName]);

  return {
    centerName,
    logoBase64,
    hasLogo: !!logoBase64,
  };
}

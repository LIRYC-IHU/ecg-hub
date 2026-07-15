import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

import { AuthService } from "../gen/v1/auth_pb";
import { BrandingService } from "../gen/v1/branding_pb";
import { HealthzService } from "../gen/v1/healthz_pb";
import { SessionService } from "../gen/v1/session_pb";
import { SetupService } from "../gen/v1/setup_pb";

const BASE_URL = (import.meta.env as Record<string, string>).VITE_API_URL ?? "";

// Connect transport for the gRPC/Connect API. baseUrl "/api" routes through
// nginx `location /api/` to the backend, which strips the /api prefix before
// the connect-go handler. Same-origin in dev, so the JWT cookie is sent
// automatically; credentials:"include" also covers a cross-origin VITE_API_URL.
export const connectTransport = createConnectTransport({
  baseUrl: `${BASE_URL}/api`,
  fetch: (input, init) => fetch(input, { ...init, credentials: "include" }),
});

// Typed clients — one per service as the API migrates to gRPC.
export const healthClient = createClient(HealthzService, connectTransport);
export const brandingClient = createClient(BrandingService, connectTransport);
export const setupClient = createClient(SetupService, connectTransport);
export const authClient = createClient(AuthService, connectTransport);
export const sessionClient = createClient(SessionService, connectTransport);

import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

import { AdminService } from "../gen/v1/admin_pb";
import { APIKeyService } from "../gen/v1/apikey_pb";
import { AuthService } from "../gen/v1/auth_pb";
import { AuthAdminService } from "../gen/v1/authadmin_pb";
import { BrandingService } from "../gen/v1/branding_pb";
import { ECGService } from "../gen/v1/ecg_pb";
import { EventService } from "../gen/v1/event_pb";
import { ExportService } from "../gen/v1/export_pb";
import { HealthzService } from "../gen/v1/healthz_pb";
import { HL7Service } from "../gen/v1/hl7_pb";
import { HL7AdminService } from "../gen/v1/hl7admin_pb";
import { ModuleService } from "../gen/v1/module_pb";
import { PatientService } from "../gen/v1/patient_pb";
import { PinService } from "../gen/v1/pin_pb";
import { SessionService } from "../gen/v1/session_pb";
import { SetupService } from "../gen/v1/setup_pb";
import { TagService } from "../gen/v1/tag_pb";
import { WebhookService } from "../gen/v1/webhook_pb";

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
export const ecgClient = createClient(ECGService, connectTransport);
export const patientClient = createClient(PatientService, connectTransport);
export const pinClient = createClient(PinService, connectTransport);
export const apiKeyClient = createClient(APIKeyService, connectTransport);
export const webhookClient = createClient(WebhookService, connectTransport);
export const adminClient = createClient(AdminService, connectTransport);
export const moduleClient = createClient(ModuleService, connectTransport);
export const hl7AdminClient = createClient(HL7AdminService, connectTransport);
export const authAdminClient = createClient(AuthAdminService, connectTransport);
export const tagClient = createClient(TagService, connectTransport);
export const eventClient = createClient(EventService, connectTransport);
export const exportClient = createClient(ExportService, connectTransport);
export const hl7Client = createClient(HL7Service, connectTransport);

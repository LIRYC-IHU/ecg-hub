/**
 * Catalogue of the REST surface a machine client uses.
 *
 * Data, not markup: the docs page renders whatever is listed here, so adding an
 * endpoint is one entry rather than a new block of JSX. It replaces the Swagger
 * UI, which documented a subset of the same endpoints from `swag` annotations
 * and shipped its own bundle to do it.
 */

export type HttpMethod = "GET" | "POST" | "PUT" | "DELETE";

export interface ApiParam {
  name: string;
  in: "path" | "query" | "body" | "header" | "form";
  type: string;
  required?: boolean;
  description: string;
}

export interface ApiEndpoint {
  id: string;
  method: HttpMethod;
  path: string;
  summary: string;
  description?: string;
  permission?: string;
  params?: ApiParam[];
  response?: string;
  responseNote?: string;
}

export interface ApiSection {
  id: string;
  title: string;
  blurb?: string;
  endpoints: ApiEndpoint[];
}

export const API_SECTIONS: ApiSection[] = [
  {
    id: "patients",
    title: "Patients",
    blurb:
      "Read-only lookups. A receiver that got a webhook usually starts here or goes straight to the ECG endpoints using the links in the payload.",
    endpoints: [
      {
        id: "search-patients",
        method: "GET",
        path: "/api/v1/patients",
        summary: "Search patients",
        permission: "patient.read",
        params: [
          { name: "q", in: "query", type: "string", description: "Free text on name or identifier. Empty returns the first page." },
          { name: "limit", in: "query", type: "int", description: "Page size (default 100)." },
          { name: "offset", in: "query", type: "int", description: "Pagination offset." },
        ],
        response: `{
  "data": [
    {
      "patient_id": "BS1016",
      "last_name": "Riviere",
      "first_name": "Camille",
      "date_of_birth": "1949-06-20T00:00:00Z",
      "gender": "F",
      "ecg_count": 2
    }
  ],
  "total": 1
}`,
      },
      {
        id: "patient-ecgs",
        method: "GET",
        path: "/api/v1/patients/{id}/ecgs",
        summary: "List a patient's ECGs",
        permission: "patient.read",
        params: [{ name: "id", in: "path", type: "string", required: true, description: "Business patient identifier (not a UUID) — e.g. BS1016." }],
        response: `{
  "data": [
    {
      "id": "6b958f3e-359b-4ac1-987f-3c78d8e2814c",
      "patient_id": "BS1016",
      "vendor": "philips",
      "device_mac": "00:0e:10:19:44:8a",
      "device_label": "Cardio B, room 214",
      "recorded_at": "2025-10-08T14:26:00Z",
      "original_filename": "philips-BS1016.xml",
      "hl7_status": "success"
    }
  ]
}`,
      },
      {
        id: "patient-tags",
        method: "GET",
        path: "/api/v1/patients/{id}/tags",
        summary: "List a patient's tags",
        permission: "patient.read",
        params: [{ name: "id", in: "path", type: "string", required: true, description: "Business patient identifier." }],
        response: `{ "data": [ { "id": "…", "name": "study-A", "color": "#3b82f6" } ] }`,
      },
    ],
  },
  {
    id: "ecgs",
    title: "ECGs",
    blurb: "Metadata, waveform and file download. Every id here is the ECG UUID carried by the webhook payload as data.ecg_id.",
    endpoints: [
      {
        id: "list-ecgs",
        method: "GET",
        path: "/api/v1/ecgs",
        summary: "List ECGs",
        permission: "patient.read",
        params: [
          { name: "vendor", in: "query", type: "string", description: "Filter by ingestion module (philips, dicom, muse, …)." },
          { name: "device_mac", in: "query", type: "string", description: "Filter by the machine that sent the ECG, by MAC address. /api/v1/ecgs/filters lists the devices that have sent something, with their labels." },
          { name: "from", in: "query", type: "date", description: "Recorded on or after (YYYY-MM-DD)." },
          { name: "to", in: "query", type: "date", description: "Recorded on or before (YYYY-MM-DD)." },
          { name: "limit", in: "query", type: "int", description: "Page size." },
          { name: "offset", in: "query", type: "int", description: "Pagination offset." },
        ],
        response: `{ "data": [ { "id": "…", "patient_id": "BS1016", "vendor": "philips", "device_mac": "00:0e:10:19:44:8a", "device_label": "Cardio B, room 214", "recorded_at": "2025-10-08T14:26:00Z" } ], "total": 1 }`,
      },
      {
        id: "ecg-filters",
        method: "GET",
        path: "/api/v1/ecgs/filters",
        summary: "Available filter values",
        description: "The vendors and date bounds currently present in the database — useful to build a filter UI without guessing.",
        permission: "patient.read",
        response: `{ "vendors": ["philips", "dicom"], "devices": [ { "mac": "00:0e:10:19:44:8a", "label": "Cardio B, room 214" } ], "min_date": "2025-10-08", "max_date": "2026-08-24" }`,
      },
      {
        id: "ecg-metadata",
        method: "GET",
        path: "/api/v1/ecgs/{id}/metadata",
        summary: "ECG metadata",
        description: "Editable field descriptors plus the current values. This is the endpoint the ecg_metadata webhook link points at.",
        permission: "ecg.read",
        params: [{ name: "id", in: "path", type: "uuid", required: true, description: "ECG id." }],
        response: `{
  "fields": [
    { "key": "recorded_at", "label": "Date d'enregistrement", "type": "datetime" },
    { "key": "last_name",   "label": "Nom",                   "type": "text" }
  ],
  "values": { "recorded_at": "2025-10-08T14:26:00Z", "last_name": "Riviere" }
}`,
      },
      {
        id: "ecg-tags",
        method: "GET",
        path: "/api/v1/ecgs/{id}/tags",
        summary: "List an ECG's tags",
        permission: "patient.read",
        params: [{ name: "id", in: "path", type: "uuid", required: true, description: "ECG id." }],
        response: `{ "data": [ { "id": "…", "name": "reviewed" } ] }`,
      },
      {
        id: "ecg-download",
        method: "GET",
        path: "/api/v1/ecgs/{id}/download",
        summary: "Download the ECG file",
        description:
          "Streams the stored file. Ask for one format and you get that file; ask for several and you get a ZIP. The file is integrity-checked against its ingestion hash before it is served.",
        permission: "ecg.download",
        params: [
          { name: "id", in: "path", type: "uuid", required: true, description: "ECG id." },
          { name: "format", in: "query", type: "string", description: "original (default), dicom, fda, pdf — repeat the parameter for several formats." },
        ],
        responseNote: "Binary body. Content-Disposition carries the filename; a 404 means the file is missing from the volume, not that the ECG is unknown.",
      },
      {
        id: "ecg-waveform",
        method: "GET",
        path: "/api/v1/ecgs/{id}/waveform",
        summary: "Waveform samples",
        description: "The binary wire format the built-in viewer consumes. Non-DICOM sources are converted on the fly and cached.",
        permission: "ecg.read",
        params: [{ name: "id", in: "path", type: "uuid", required: true, description: "ECG id." }],
        responseNote: "Binary body — leads, sampling rate and calibration in a compact header. Use /download?format=dicom for a standard file instead.",
      },
      {
        id: "ecg-delete",
        method: "DELETE",
        path: "/api/v1/ecgs/{id}",
        summary: "Delete an ECG",
        description: "Removes the database row and the file on the volume. Audited.",
        permission: "ecg.delete",
        params: [{ name: "id", in: "path", type: "uuid", required: true, description: "ECG id." }],
        responseNote: "204 No Content.",
      },
      {
        id: "uploads",
        method: "POST",
        path: "/api/v1/uploads",
        summary: "Upload ECG files",
        description:
          "Feeds the same ingestion pipeline as FTP and DICOM: valid files are stored, files without a patient identifier go to the review queue, unparsable files are rejected. Multipart, field name files, repeatable. Each file must stay under the server's ingestion size ceiling (ingest.max_file_bytes, 1 MiB by default) — the same limit the FTP and DICOM ports apply.",
        permission: "ecg.upload",
        params: [{ name: "files", in: "form", type: "file[]", required: true, description: "One or more ECG files." }],
        response: `{
  "queued": 1,
  "files": [ { "filename": "philips-BS1016.xml", "size": 278131, "status": "queued" } ]
}`,
      },
    ],
  },
  {
    id: "webhooks",
    title: "Webhooks",
    blurb:
      "Manage your own endpoints. Every route is scoped to the calling identity: an API key only ever sees the webhooks of the user who created it. The delivered payload looks like this — device_label is the operator's name for the machine the ECG came off, and travels beside its MAC so a rule keyed on the address keeps matching when the label is changed:\n\n" +
      `{\n  "event": "ecg.ingested",\n  "webhook_id": "2683ad89-…",\n  "timestamp": "2026-08-24T11:33:50Z",\n  "data": {\n    "ecg_id": "6b958f3e-…",\n    "patient_id": "BS1016",\n    "vendor": "philips",\n    "device_mac": "00:0e:10:19:44:8a",\n    "device_label": "Cardio B, room 214",\n    "filename": "philips-BS1016.xml"\n  },\n  "links": { "ecg_metadata": "…", "ecg_download": "…" }\n}`,
    endpoints: [
      {
        id: "webhook-options",
        method: "GET",
        path: "/api/v1/webhooks/options",
        summary: "Selectable events and vendors",
        permission: "webhook.manage",
        response: `{
  "events": ["ecg.ingested","ecg.unidentified","ecg.quarantined","ecg.duplicate","hl7.exhausted","hl7.rejected"],
  "vendors": [ { "name": "philips", "extensions": [".xml"] } ],
  "devices": [ { "mac": "00:0e:10:19:44:8a", "label": "Cardio B, room 214" } ]
}`,
      },
      {
        id: "webhook-list",
        method: "GET",
        path: "/api/v1/webhooks",
        summary: "List my webhooks",
        description: "Secrets are never returned. has_secret / has_auth_header tell you whether a value is configured.",
        permission: "webhook.manage",
        response: `[
  {
    "id": "2683ad89-2b2d-47a6-aa87-b8c99405ede9",
    "name": "Research receiver",
    "url": "https://receiver.example.org/hook",
    "enabled": true,
    "events": ["ecg.ingested"],
    "vendors": ["philips"],
    "devices": ["00:0e:10:19:44:8a"],
    "has_secret": true,
    "has_auth_header": false,
    "last_status_code": 200,
    "last_delivered_at": "2026-08-24T11:33:50Z"
  }
]`,
      },
      {
        id: "webhook-create",
        method: "POST",
        path: "/api/v1/webhooks",
        summary: "Create a webhook",
        permission: "webhook.manage",
        params: [
          { name: "name", in: "body", type: "string", required: true, description: "Max 100 characters." },
          { name: "url", in: "body", type: "string", required: true, description: "http(s) only. Loopback and link-local targets are refused (SSRF guard)." },
          { name: "secret", in: "body", type: "string", description: "HMAC-SHA256 signing secret. Stored encrypted, never returned." },
          { name: "auth_header", in: "body", type: "string", description: "Sent as-is in the Authorization header, e.g. \"Bearer …\"." },
          { name: "events", in: "body", type: "string[]", description: "Empty = every event." },
          { name: "vendors", in: "body", type: "string[]", description: "Empty = every vendor. Unknown names are rejected." },
          { name: "devices", in: "body", type: "string[]", description: "MAC addresses; empty = every device. Stored normalised, so the casing you send does not matter. Anything that is not an address is rejected — a filter matching nothing would stop deliveries with no error to show for it." },
          { name: "enabled", in: "body", type: "bool", description: "Defaults to true." },
          { name: "insecure_skip_verify", in: "body", type: "bool", description: "HTTPS receivers with a self-signed certificate only." },
        ],
        responseNote: "201 with the created webhook, secrets omitted.",
      },
      {
        id: "webhook-update",
        method: "PUT",
        path: "/api/v1/webhooks/{id}",
        summary: "Update a webhook",
        description:
          "Optional fields keep their stored value when omitted: enabled, insecure_skip_verify, secret and auth_header. Send \"\" in secret or auth_header to clear them.",
        permission: "webhook.manage",
        params: [{ name: "id", in: "path", type: "uuid", required: true, description: "Webhook id." }],
        responseNote: "200 with the updated webhook.",
      },
      {
        id: "webhook-delete",
        method: "DELETE",
        path: "/api/v1/webhooks/{id}",
        summary: "Delete a webhook",
        permission: "webhook.manage",
        params: [{ name: "id", in: "path", type: "uuid", required: true, description: "Webhook id." }],
        responseNote: "204 No Content.",
      },
      {
        id: "webhook-test",
        method: "POST",
        path: "/api/v1/webhooks/{id}/test",
        summary: "Send a test event",
        description: "Delivers a signed test payload synchronously and records it in the delivery history like any other delivery.",
        permission: "webhook.manage",
        params: [{ name: "id", in: "path", type: "uuid", required: true, description: "Webhook id." }],
        response: `{ "ok": true, "status_code": 200, "error": "" }`,
      },
      {
        id: "webhook-deliveries",
        method: "GET",
        path: "/api/v1/webhooks/{id}/deliveries",
        summary: "Delivery history",
        description: "Newest first, 50 per page. Retention is set by the server (30 days by default).",
        permission: "webhook.manage",
        params: [
          { name: "id", in: "path", type: "uuid", required: true, description: "Webhook id." },
          { name: "offset", in: "query", type: "int", description: "Pagination offset." },
        ],
        response: `[
  {
    "id": "63b5e2fe-799d-417b-80e9-0ce704add66d",
    "event": "ecg.ingested",
    "status_code": 200,
    "error": "",
    "attempts": 1,
    "delivered_at": "2026-08-24T11:33:50Z"
  }
]`,
      },
      {
        id: "webhook-resend",
        method: "POST",
        path: "/api/v1/webhooks/{id}/deliveries/{deliveryId}/resend",
        summary: "Replay a delivery",
        description:
          "Sends the stored payload again — byte for byte, so the signature stays verifiable — against the webhook's current URL and secret. Single attempt, synchronous, logged as a new delivery.",
        permission: "webhook.manage",
        params: [
          { name: "id", in: "path", type: "uuid", required: true, description: "Webhook id." },
          { name: "deliveryId", in: "path", type: "uuid", required: true, description: "Delivery id from the history." },
        ],
        response: `{ "ok": true, "status_code": 200, "error": "" }`,
      },
    ],
  },
];

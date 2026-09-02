// ECG Hub — k6 Load & Stress Test
// Usage: k6 run backend/loadtest/k6-stress.js
// Prereqs: brew install k6
//
// Env vars:
//   BASE_URL  (default: http://localhost)
//   USERNAME  (default: jo)
//   PASSWORD  (default: test)
//
// Run specific scenario:
//   k6 run --scenario smoke backend/loadtest/k6-stress.js

import http from "k6/http";
import { check, sleep, group } from "k6";
import { Rate, Trend, Counter } from "k6/metrics";

const BASE = __ENV.BASE_URL || "http://localhost";
const USERNAME = __ENV.USERNAME || "jo";
const PASSWORD = __ENV.PASSWORD || "test";

// Custom metrics
const errorRate = new Rate("errors");
const dbHeavyDuration = new Trend("db_heavy_query_duration", true);
const searchDuration = new Trend("search_duration", true);
const writeOps = new Counter("write_operations");

export const options = {
  scenarios: {
    smoke: {
      executor: "constant-vus",
      vus: 5,
      duration: "30s",
      tags: { scenario: "smoke" },
    },
    load: {
      executor: "constant-vus",
      vus: 50,
      duration: "2m",
      startTime: "35s",
      tags: { scenario: "load" },
    },
    stress: {
      executor: "ramping-vus",
      startVUs: 0,
      stages: [
        { duration: "15s", target: 200 },
        { duration: "1m", target: 200 },
        { duration: "15s", target: 0 },
      ],
      startTime: "2m40s",
      tags: { scenario: "stress" },
    },
    spike: {
      executor: "constant-vus",
      vus: 300,
      duration: "30s",
      startTime: "4m10s",
      tags: { scenario: "spike" },
    },
    db_stress: {
      executor: "constant-vus",
      vus: 30,
      duration: "2m",
      startTime: "5m",
      tags: { scenario: "db_stress" },
      exec: "dbStress",
    },
  },
  thresholds: {
    http_req_duration: ["p(95)<3000", "p(99)<5000"],
    errors: ["rate<0.15"],
    db_heavy_query_duration: ["p(95)<2000"],
  },
};

// --- Setup: login, get JWT cookie ---
export function setup() {
  const res = http.post(
    `${BASE}/api/v1/auth/login`,
    JSON.stringify({ username: USERNAME, password: PASSWORD }),
    { headers: { "Content-Type": "application/json" } },
  );
  check(res, { "login OK": (r) => r.status === 204 || r.status === 200 });

  const jar = {};
  for (const name of Object.keys(res.cookies)) {
    jar[name] = res.cookies[name][0].value;
  }

  // Fetch some ECG IDs for write operations later
  const ecgsRes = http.get(`${BASE}/api/v1/ecgs?per_page=50`, {
    headers: { Cookie: cookieStr(jar) },
  });
  let ecgIds = [];
  if (ecgsRes.status === 200) {
    const body = JSON.parse(ecgsRes.body);
    ecgIds = (body.data || []).map((e) => e.id);
  }

  // Fetch patient IDs
  const patsRes = http.get(`${BASE}/api/v1/patients?per_page=100`, {
    headers: { Cookie: cookieStr(jar) },
  });
  let patientIds = [];
  if (patsRes.status === 200) {
    const body = JSON.parse(patsRes.body);
    patientIds = (body.data || []).map((p) => p.patient_id);
  }

  return { jar, ecgIds, patientIds };
}

function cookieStr(jar) {
  return Object.entries(jar)
    .map(([k, v]) => `${k}=${v}`)
    .join("; ");
}

function params(data) {
  return { headers: { Cookie: cookieStr(data.jar) } };
}

function jsonParams(data) {
  return {
    headers: {
      Cookie: cookieStr(data.jar),
      "Content-Type": "application/json",
    },
  };
}

function pick(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}

// --- Main scenario: mixed realistic traffic ---
export default function (data) {
  const p = params(data);

  // 1. Health check — gRPC/Connect over HTTP (POST, JSON codec)
  group("health", () => {
    const r = http.post(
      `${BASE}/api/grpc.api.v1.HealthzService/CheckHealth`,
      "{}",
      Object.assign({}, p, {
        headers: Object.assign({}, p.headers, { "Content-Type": "application/json" }),
      }),
    );
    check(r, { 200: (res) => res.status === 200 });
    errorRate.add(r.status !== 200);
  });

  // 2. ECG timeline with random pagination + filters
  group("ecg_list", () => {
    const page = Math.floor(Math.random() * 10) + 1;
    const perPage = pick([25, 50, 100]);
    const statuses = ["", "pending", "success", "hl7_exhausted"];
    const status = pick(statuses);
    let url = `${BASE}/api/v1/ecgs?page=${page}&per_page=${perPage}`;
    if (status) url += `&hl7_status=${status}`;

    const r = http.get(url, p);
    check(r, { 200: (res) => res.status === 200 });
    errorRate.add(r.status !== 200);
  });

  // 3. Full-text search (ILIKE on multiple columns — heavy on DB)
  group("search_heavy", () => {
    const queries = [
      "BS1174",
      "patient",
      "ecg",
      "nihon",
      "philips",
      "12345",
      "dicom",
      "a",
      "test",
      "nicolas",
      "20260",
      "cardio",
    ];
    const q = pick(queries);
    const r = http.get(`${BASE}/api/v1/ecgs?q=${q}&per_page=100`, p);
    searchDuration.add(r.timings.duration);
    check(r, { 200: (res) => res.status === 200 });
    errorRate.add(r.status !== 200);
  });

  // 4. Patient navigation flow: list → pick patient → get ECGs
  group("patient_flow", () => {
    if (data.patientIds.length > 0) {
      const pid = pick(data.patientIds);
      const r = http.get(`${BASE}/api/v1/patients/${pid}/ecgs?per_page=50`, p);
      check(r, { 200: (res) => res.status === 200 });
      errorRate.add(r.status !== 200);
    }
  });

  // 5. Metadata read (random ECG)
  group("metadata_read", () => {
    if (data.ecgIds.length > 0) {
      const id = pick(data.ecgIds);
      const r = http.get(`${BASE}/api/v1/ecgs/${id}/metadata`, p);
      check(r, { 200: (res) => res.status === 200 });
      errorRate.add(r.status !== 200);
    }
  });

  // 6. Write operations (10% of iterations)
  if (Math.random() < 0.1 && data.ecgIds.length > 0) {
    group("metadata_write", () => {
      const id = pick(data.ecgIds);
      const r = http.patch(
        `${BASE}/api/v1/ecgs/${id}/metadata`,
        JSON.stringify({ device_model: `loadtest-${Date.now()}` }),
        jsonParams(data),
      );
      check(r, { 200: (res) => res.status === 200 });
      writeOps.add(1);
      errorRate.add(r.status !== 200 && r.status !== 403);
    });
  }

  // 7. Admin endpoints (lighter but hit different tables)
  group("admin", () => {
    const endpoints = [
      "/api/v1/admin/stats",
      "/api/v1/ecgs/filters",
      "/api/v1/modules",
      "/api/v1/admin/connectors",
      "/api/v1/admin/storage-metrics",
    ];
    const r = http.get(`${BASE}${pick(endpoints)}`, p);
    check(r, { "2xx": (res) => res.status >= 200 && res.status < 300 });
    errorRate.add(r.status >= 400);
  });

  sleep(Math.random() * 0.3 + 0.05);
}

// --- DB Stress scenario: hammers heavy queries ---
export function dbStress(data) {
  const p = params(data);

  // Deep pagination (forces large OFFSET)
  group("deep_pagination", () => {
    const page = Math.floor(Math.random() * 50) + 10;
    const r = http.get(`${BASE}/api/v1/ecgs?page=${page}&per_page=100`, p);
    dbHeavyDuration.add(r.timings.duration);
    check(r, { 200: (res) => res.status === 200 });
    errorRate.add(r.status !== 200);
  });

  // Concurrent ILIKE searches (full table scan potential)
  group("concurrent_search", () => {
    const heavyQueries = ["a", "e", "1", "BS", "%", "ecg_", "20"];
    const q = pick(heavyQueries);
    const r = http.get(`${BASE}/api/v1/ecgs?q=${q}&per_page=200`, p);
    dbHeavyDuration.add(r.timings.duration);
    check(r, { 200: (res) => res.status === 200 });
    errorRate.add(r.status !== 200);
  });

  // Combined filters (forces complex WHERE + JOIN)
  group("combined_filters", () => {
    const vendors = ["philips", "nihon-kohden", "dicom", "ge"];
    const formats = ["xml", "dat", "dcm"];
    const r = http.get(
      `${BASE}/api/v1/ecgs?vendor=${pick(vendors)}&file_format=${pick(formats)}&hl7_status=pending&per_page=100`,
      p,
    );
    dbHeavyDuration.add(r.timings.duration);
    check(r, { 200: (res) => res.status === 200 });
    errorRate.add(r.status !== 200);
  });

  // Patient search (ILIKE on patients table)
  group("patient_search", () => {
    const names = ["martin", "dupont", "a", "jean", "BS", "12"];
    const r = http.get(
      `${BASE}/api/v1/patients?q=${pick(names)}&per_page=100`,
      p,
    );
    dbHeavyDuration.add(r.timings.duration);
    check(r, { 200: (res) => res.status === 200 });
    errorRate.add(r.status !== 200);
  });

  // Audit log (large table scan)
  group("audit_log", () => {
    const r = http.get(
      `${BASE}/api/v1/audit-logs?per_page=100&page=${Math.floor(Math.random() * 20) + 1}`,
      p,
    );
    dbHeavyDuration.add(r.timings.duration);
    check(r, { 200: (res) => res.status === 200 });
    errorRate.add(r.status !== 200);
  });

  // Stats (aggregation query with GROUP BY)
  group("stats_aggregation", () => {
    const r = http.get(`${BASE}/api/v1/admin/stats`, p);
    dbHeavyDuration.add(r.timings.duration);
    check(r, { 200: (res) => res.status === 200 });
    errorRate.add(r.status !== 200);
  });

  sleep(Math.random() * 0.1);
}

export function teardown(data) {
  http.get(`${BASE}/api/v1/auth/logout`, params(data));
}

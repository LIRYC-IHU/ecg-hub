// ECG Hub — k6 Public API Load & Stress Test
// Usage: k6 run backend/loadtest/k6-public.js
// Prereqs: brew install k6
//
// Exercises ONLY the unauthenticated surface (no login, no cookie). These are
// the four public gRPC/Connect reads mounted without ConnectRequireAuth
// (Healthz has optional auth), plus a low-rate probe on the throttled login
// endpoint that asserts the per-IP rate limiter behaves.
//
// Env vars:
//   BASE_URL  (default: http://localhost)   — scheme+host, no trailing /api
//
// Run a specific scenario:
//   k6 run --scenario smoke backend/loadtest/k6-public.js

import http from "k6/http";
import { check, sleep, group } from "k6";
import { Rate, Trend } from "k6/metrics";

const BASE = __ENV.BASE_URL || "http://localhost";
// Connect handlers are double-mounted; the public entrypoint is under /api.
const GRPC = `${BASE}/api`;

// Custom metrics
const errorRate = new Rate("errors");
const healthDuration = new Trend("public_health_duration", true);
const brandingDuration = new Trend("public_branding_duration", true);
const setupStatusDuration = new Trend("public_setup_status_duration", true);
const providersDuration = new Trend("public_providers_duration", true);

const ALL_SCENARIOS = {
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
    // Low-rate probe of the throttled login endpoint. It is NOT a throughput
    // test: the per-IP limiter (~0.33 req/s) is expected to return 429 under
    // load — we only assert the endpoint answers 200/204/401/429, never 5xx.
    auth_throttle: {
      executor: "constant-vus",
      vus: 3,
      duration: "30s",
      startTime: "5m",
      tags: { scenario: "auth_throttle" },
      exec: "authThrottle",
    },
  // Capacity discovery: drive a *target request rate* (not VUs) with no
  // think-time, ramping up in steps until latency/errors climb. k6 allocates
  // VUs automatically to hold each rate. Best run in isolation:
  //   SCENARIO=capacity k6 run backend/loadtest/k6-public.js
  // In the full suite it starts last (after auth_throttle) so it never
  // competes with the other scenarios for the local CPU.
  capacity: {
    executor: "ramping-arrival-rate",
    startRate: 200,
    timeUnit: "1s",
    preAllocatedVUs: 200,
    maxVUs: 2000,
    stages: [
      { duration: "30s", target: 500 },
      { duration: "30s", target: 1000 },
      { duration: "30s", target: 2000 },
      { duration: "30s", target: 3000 },
      { duration: "30s", target: 5000 },
      { duration: "20s", target: 0 },
    ],
    startTime: "5m35s",
    tags: { scenario: "capacity" },
    exec: "capacity",
  },
};

// Select scenarios: run the whole suite by default, or a single one with
//   SCENARIO=capacity k6 run backend/loadtest/k6-public.js
// When isolated, its startTime offset is dropped so it begins immediately.
const SELECTED = __ENV.SCENARIO;
function selectedScenarios() {
  if (!SELECTED) return ALL_SCENARIOS;
  const one = ALL_SCENARIOS[SELECTED];
  if (!one) throw new Error(`unknown SCENARIO="${SELECTED}"`);
  const { startTime, ...rest } = one;
  return { [SELECTED]: rest };
}

export const options = {
  scenarios: selectedScenarios(),
  thresholds: {
    // Public reads are light DB/no-DB lookups — hold them to tight latencies.
    http_req_duration: ["p(95)<1000", "p(99)<2000"],
    errors: ["rate<0.05"],
    public_health_duration: ["p(95)<800"],
    public_providers_duration: ["p(95)<800"],
  },
};

// Connect unary call over HTTP with the JSON codec: POST + Content-Type JSON,
// empty message body "{}". Success is HTTP 200.
function connectPost(procedure) {
  return http.post(`${GRPC}${procedure}`, "{}", {
    headers: { "Content-Type": "application/json" },
  });
}

function pick(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}

// Weighted mix of the four public reads. Shared by the interactive `default`
// scenario (with think-time) and the `capacity` scenario (no sleep). Weighted
// so health/providers (the login-page critical path) dominate.
function publicRead() {
  const roll = Math.random();

  if (roll < 0.35) {
    group("public_health", () => {
      const r = connectPost("/grpc.api.v1.HealthzService/CheckHealth");
      healthDuration.add(r.timings.duration);
      check(r, { 200: (res) => res.status === 200 });
      errorRate.add(r.status !== 200);
    });
  } else if (roll < 0.65) {
    group("public_providers", () => {
      const r = connectPost("/grpc.api.v1.AuthService/GetProviders");
      providersDuration.add(r.timings.duration);
      check(r, { 200: (res) => res.status === 200 });
      errorRate.add(r.status !== 200);
    });
  } else if (roll < 0.85) {
    group("public_branding", () => {
      const r = connectPost("/grpc.api.v1.BrandingService/GetBranding");
      brandingDuration.add(r.timings.duration);
      check(r, { 200: (res) => res.status === 200 });
      errorRate.add(r.status !== 200);
    });
  } else {
    group("public_setup_status", () => {
      const r = connectPost("/grpc.api.v1.SetupService/GetStatus");
      setupStatusDuration.add(r.timings.duration);
      check(r, { 200: (res) => res.status === 200 });
      errorRate.add(r.status !== 200);
    });
  }
}

// --- Main scenario: mixed public read traffic with think-time (no auth) ---
export default function () {
  publicRead();
  sleep(Math.random() * 0.3 + 0.05);
}

// --- Capacity scenario: hold a target RPS with no think-time to find the
// max sustainable throughput. Same request mix, driven by arrival rate. ---
export function capacity() {
  publicRead();
}

// --- Auth throttle probe: verifies the login rate limiter answers cleanly ---
export function authThrottle() {
  const bogus = {
    username: pick(["probe", "nobody", "loadtest"]),
    // Never a real credential — this probe must not authenticate anything.
    password: `invalid-${Date.now()}`,
  };
  const r = http.post(
    `${BASE}/api/v1/auth/login`,
    JSON.stringify(bogus),
    { headers: { "Content-Type": "application/json" } },
  );
  // Acceptable: 401 (bad creds), 429 (throttled), 200/204 (unlikely). A 5xx is
  // a real failure.
  check(r, {
    "no server error": (res) => res.status < 500,
    "throttled or rejected": (res) => [200, 204, 401, 429].includes(res.status),
  });
  errorRate.add(r.status >= 500);
  sleep(Math.random() * 0.2 + 0.05);
}

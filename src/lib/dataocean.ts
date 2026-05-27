"use client";

import { ensureDeviceIdCookie } from "@/lib/device-id";

type JsonPrimitive = string | number | boolean | null;
type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue };
type JsonObject = { [key: string]: JsonValue };

const ANALYTICS_COLLECT_PATH = "/api/analytics/collect";
const SESSION_COOKIE_NAME = "uai_analytics_session_id";
const SENSITIVE_ANALYTICS_QUERY_KEYS = new Set([
  "api_trade_no",
  "buyer",
  "code",
  "money",
  "name",
  "out_trade_no",
  "param",
  "pid",
  "request_id",
  "requestid",
  "sign",
  "sign_type",
  "state",
  "timestamp",
  "token",
  "trade_no",
  "trade_status",
  "type"
]);
const NON_ACQUISITION_REFERRER_HOSTS = new Set([
  "accounts.google.com",
  "api.0-0.pro",
  "creem.io",
  "pay.lxsd.cn"
]);

let initialAcquisitionReferrer: string | null | undefined;

interface DataOceanTrackInput {
  properties?: JsonObject;
  context?: JsonObject;
}

function readCookie(name: string) {
  if (typeof document === "undefined") return null;
  const prefix = `${encodeURIComponent(name)}=`;
  const parts = document.cookie.split(";");
  for (const part of parts) {
    const trimmed = part.trim();
    if (!trimmed.startsWith(prefix)) continue;
    const raw = trimmed.slice(prefix.length);
    try {
      return decodeURIComponent(raw);
    } catch {
      return raw;
    }
  }
  return null;
}

function writeCookie(name: string, value: string, maxAgeSeconds: number) {
  if (typeof document === "undefined") return;
  const encoded = `${encodeURIComponent(name)}=${encodeURIComponent(value)}`;
  document.cookie = `${encoded}; Path=/; Max-Age=${maxAgeSeconds}; SameSite=Lax`;
}

function createId(prefix: string) {
  try {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
      return `${prefix}_${crypto.randomUUID()}`;
    }
  } catch {
    // ignore
  }
  return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2)}`;
}

function ensureSessionId() {
  const existing = readCookie(SESSION_COOKIE_NAME);
  if (existing && existing.length > 8) return existing;
  const created = createId("uas");
  writeCookie(SESSION_COOKIE_NAME, created, 60 * 30);
  return created;
}

function readUtm(): JsonObject {
  if (typeof window === "undefined") return {};
  const params = new URLSearchParams(window.location.search);
  const keys = ["source", "medium", "campaign", "content", "term"] as const;
  const output: JsonObject = {};
  for (const key of keys) {
    const value = params.get(`utm_${key}`);
    if (value && value.trim()) output[key] = value.trim();
  }
  return output;
}

function sanitizeUrl(value: string, output: "absolute" | "path") {
  try {
    const url = new URL(value, typeof window !== "undefined" ? window.location.origin : "https://0-0.pro");
    for (const key of Array.from(url.searchParams.keys())) {
      if (SENSITIVE_ANALYTICS_QUERY_KEYS.has(key.toLowerCase())) {
        url.searchParams.delete(key);
      }
    }
    if (output === "path") {
      return `${url.pathname}${url.search}`;
    }
    return url.toString();
  } catch {
    return value.slice(0, 2048);
  }
}

function isIpAddress(hostname: string) {
  return /^\d{1,3}(?:\.\d{1,3}){3}$/.test(hostname) || hostname.includes(":");
}

function normalizeAcquisitionReferrer(referrer: string, currentHref: string) {
  if (!referrer.trim()) return null;
  try {
    const referrerUrl = new URL(referrer);
    const currentUrl = new URL(currentHref);
    const host = referrerUrl.hostname.replace(/^www\./, "").toLowerCase();
    const currentHost = currentUrl.hostname.replace(/^www\./, "").toLowerCase();
    if (!host || host === currentHost) return null;
    if (isIpAddress(host)) return null;
    if (NON_ACQUISITION_REFERRER_HOSTS.has(host)) return null;
    return sanitizeUrl(referrerUrl.toString(), "absolute");
  } catch {
    return null;
  }
}

function shouldAttachAcquisitionReferrer(eventName: string) {
  return eventName === "landing_view" || eventName === "signup_started";
}

function getInitialAcquisitionReferrer(eventName: string) {
  if (!shouldAttachAcquisitionReferrer(eventName)) return null;
  if (typeof window === "undefined" || typeof document === "undefined") return null;
  if (initialAcquisitionReferrer !== undefined) return initialAcquisitionReferrer;
  initialAcquisitionReferrer = normalizeAcquisitionReferrer(document.referrer, window.location.href);
  return initialAcquisitionReferrer;
}

function currentContext(eventName: string, extra?: JsonObject): JsonObject {
  if (typeof window === "undefined" || typeof document === "undefined") {
    return extra ?? {};
  }
  return {
    url: sanitizeUrl(window.location.href, "absolute"),
    path: sanitizeUrl(window.location.href, "path"),
    title: document.title,
    referrer: getInitialAcquisitionReferrer(eventName),
    utm: readUtm(),
    viewport: {
      width: window.innerWidth,
      height: window.innerHeight,
    },
    ...(extra ?? {}),
  };
}

export function trackDataOceanEvent(name: string, input: DataOceanTrackInput = {}) {
  const anonymousId = ensureDeviceIdCookie();
  const sessionId = ensureSessionId();
  if (!anonymousId || !sessionId) return;

  const body = {
    eventId: createId("uae"),
    name,
    anonymousId,
    sessionId,
    timestamp: new Date().toISOString(),
    properties: input.properties ?? {},
    context: currentContext(name, input.context),
  };

  const json = JSON.stringify(body);
  try {
    if (typeof navigator !== "undefined" && typeof navigator.sendBeacon === "function") {
      const blob = new Blob([json], { type: "application/json" });
      if (navigator.sendBeacon(ANALYTICS_COLLECT_PATH, blob)) {
        return;
      }
    }
  } catch {
    // fall through to fetch
  }

  void fetch(ANALYTICS_COLLECT_PATH, {
    method: "POST",
    headers: {
      "content-type": "application/json",
    },
    body: json,
    credentials: "same-origin",
    keepalive: true,
  }).catch(() => null);
}

export function trackDataOceanPageView(kind: "page_view" | "landing_view") {
  trackDataOceanEvent(kind, {
    properties: {
      path: typeof window !== "undefined" ? sanitizeUrl(window.location.href, "path") : "",
    },
  });
}

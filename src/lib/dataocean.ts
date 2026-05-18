"use client";

import { ensureDeviceIdCookie } from "@/lib/device-id";

type JsonPrimitive = string | number | boolean | null;
type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue };
type JsonObject = { [key: string]: JsonValue };

const ANALYTICS_COLLECT_PATH = "/api/analytics/collect";
const SESSION_COOKIE_NAME = "uai_analytics_session_id";

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

function currentContext(extra?: JsonObject): JsonObject {
  if (typeof window === "undefined" || typeof document === "undefined") {
    return extra ?? {};
  }
  return {
    url: window.location.href,
    path: `${window.location.pathname}${window.location.search}`,
    title: document.title,
    referrer: document.referrer || null,
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
    context: currentContext(input.context),
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
      path: typeof window !== "undefined" ? `${window.location.pathname}${window.location.search}` : "",
    },
  });
}

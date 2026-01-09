import "server-only";

import { cache } from "react";

import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type {
  AnnouncementsListResponse,
  ApiKeysListResponse,
  DailyUsagePoint,
  UsageResponse
} from "@/lib/types";

function isApiKeysListResponse(value: unknown): value is ApiKeysListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

function isUsageResponse(value: unknown): value is UsageResponse {
  if (!value || typeof value !== "object") return false;
  if (!("summary" in value) || !("daily" in value) || !("topModels" in value)) return false;
  const daily = (value as { daily?: unknown }).daily;
  return Array.isArray(daily);
}

function isAnnouncementsListResponse(value: unknown): value is AnnouncementsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

function isoDate(d: Date) {
  return d.toISOString().slice(0, 10);
}

function buildEmptyDaily(days: number): DailyUsagePoint[] {
  const now = new Date();
  const today = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));
  const points: DailyUsagePoint[] = [];
  for (let i = days - 1; i >= 0; i -= 1) {
    const day = new Date(today);
    day.setUTCDate(today.getUTCDate() - i);
    points.push({
      date: isoDate(day),
      requests: 0,
      inputTokens: 0,
      outputTokens: 0,
      totalTokens: 0,
      errorRate: 0
    });
  }
  return points;
}

export const getDashboardUsage = cache(async (): Promise<UsageResponse> => {
  try {
    const headers = await getBackendAuthHeadersCached();
    const res = await fetch(buildBackendUrl("/usage"), { cache: "no-store", headers });
    if (!res.ok) {
      return {
        summary: { requests24h: 0, tokens24h: 0, errorRate24h: 0, spend24hUsd: 0 },
        daily: buildEmptyDaily(7),
        topModels: []
      };
    }
    const json: unknown = await res.json().catch(() => null);
    if (isUsageResponse(json)) return json;
  } catch {
    // ignore
  }
  return {
    summary: { requests24h: 0, tokens24h: 0, errorRate24h: 0, spend24hUsd: 0 },
    daily: buildEmptyDaily(7),
    topModels: []
  };
});

export const getDashboardApiKeys = cache(async (): Promise<ApiKeysListResponse["items"]> => {
  try {
    const headers = await getBackendAuthHeadersCached();
    const res = await fetch(buildBackendUrl("/keys"), { cache: "no-store", headers });
    if (!res.ok) return [];
    const json: unknown = await res.json().catch(() => null);
    if (isApiKeysListResponse(json)) return json.items;
  } catch {
    // ignore
  }
  return [];
});

export const getDashboardAnnouncements = cache(async (): Promise<AnnouncementsListResponse["items"]> => {
  try {
    const headers = await getBackendAuthHeadersCached();
    const res = await fetch(buildBackendUrl("/announcements"), { cache: "no-store", headers });
    if (!res.ok) return [];
    const json: unknown = await res.json().catch(() => null);
    if (isAnnouncementsListResponse(json)) return json.items;
  } catch {
    // ignore
  }
  return [];
});


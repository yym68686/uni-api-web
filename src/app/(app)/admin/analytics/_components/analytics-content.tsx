import type {
  AdminAnalyticsGranularity,
  AdminAnalyticsPreset,
  AdminAnalyticsResponse,
  AdminAnalyticsTab,
  AdminOverviewResponse
} from "@/lib/types";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import { AdminAnalyticsViewClient } from "./analytics-view-client";

function getParam(params: Record<string, string | string[] | undefined>, key: string) {
  const raw = params[key];
  if (typeof raw === "string") return raw;
  if (Array.isArray(raw)) return raw[0] ?? null;
  return null;
}

function isYmd(value: string) {
  return /^\d{4}-\d{2}-\d{2}$/.test(value);
}

function ymdUtc(date: Date) {
  const y = date.getUTCFullYear();
  const m = String(date.getUTCMonth() + 1).padStart(2, "0");
  const d = String(date.getUTCDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

function startOfDayUtc(ymd: string) {
  return new Date(`${ymd}T00:00:00.000Z`);
}

function endOfDayUtc(ymd: string) {
  return new Date(`${ymd}T23:59:59.999Z`);
}

function addDaysUtc(date: Date, days: number) {
  const next = new Date(date);
  next.setUTCDate(next.getUTCDate() + days);
  return next;
}

function diffDaysInclusiveUtc(fromYmd: string, toYmd: string) {
  const from = startOfDayUtc(fromYmd).getTime();
  const to = startOfDayUtc(toYmd).getTime();
  const deltaDays = Math.round((to - from) / (24 * 60 * 60 * 1000));
  return Math.max(1, deltaDays + 1);
}

function parseTab(value: string | null): AdminAnalyticsTab {
  switch (value) {
    case "users":
    case "models":
    case "errors":
    case "performance":
    case "summary":
      return value;
    default:
      return "summary";
  }
}

function parsePreset(value: string | null): AdminAnalyticsPreset {
  switch (value) {
    case "today":
    case "week":
    case "month":
    case "year":
    case "all":
    case "custom":
      return value;
    default:
      return "week";
  }
}

function toGranularity(daysInclusive: number): AdminAnalyticsGranularity {
  return daysInclusive <= 2 ? "hour" : "day";
}

function readNumber(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function readString(value: unknown) {
  return typeof value === "string" && value.length > 0 ? value : null;
}

function normalizeAnalyticsResponse(
  json: unknown,
  fallback: Pick<AdminAnalyticsResponse, "range">
): AdminAnalyticsResponse | null {
  if (!json || typeof json !== "object") return null;

  const obj = json as Record<string, unknown>;

  const rawKpis = (obj.kpis && typeof obj.kpis === "object" ? (obj.kpis as Record<string, unknown>) : null) ?? null;
  const rawSeries = (Array.isArray(obj.series) ? obj.series : Array.isArray(obj.timeseries) ? obj.timeseries : null) as
    | unknown[]
    | null;

  const rawLeaders =
    obj.leaders && typeof obj.leaders === "object" ? (obj.leaders as Record<string, unknown>) : null;

  const series: AdminAnalyticsResponse["series"] =
    rawSeries?.flatMap((p) => {
      if (!p || typeof p !== "object") return [];
      const point = p as Record<string, unknown>;
      const ts =
        readString(point.ts) ??
        readString(point.time) ??
        readString(point.date) ??
        readString(point.bucket) ??
        null;
      if (!ts) return [];
      return [
        {
          ts,
          spendUsd: readNumber(point.spendUsd ?? point.spend_usd ?? point.costUsd ?? point.cost_usd) ?? 0,
          calls: readNumber(point.calls ?? point.requests) ?? 0,
          errors: readNumber(point.errors ?? point.failures) ?? 0,
          inputTokens: readNumber(point.inputTokens ?? point.input_tokens) ?? 0,
          outputTokens: readNumber(point.outputTokens ?? point.output_tokens) ?? 0,
          cachedTokens: readNumber(point.cachedTokens ?? point.cached_tokens) ?? 0,
          p95LatencyMs: readNumber(point.p95LatencyMs ?? point.p95_latency_ms) ?? null
        }
      ];
    }) ?? [];

  const spendUsd =
    (rawKpis ? readNumber(rawKpis.spendUsd ?? rawKpis.spend_usd ?? rawKpis.costUsd ?? rawKpis.cost_usd) : null) ??
    series.reduce((acc, p) => acc + p.spendUsd, 0);
  const calls =
    (rawKpis ? readNumber(rawKpis.calls ?? rawKpis.requests) : null) ??
    series.reduce((acc, p) => acc + p.calls, 0);
  const errors =
    (rawKpis ? readNumber(rawKpis.errors ?? rawKpis.failures) : null) ??
    series.reduce((acc, p) => acc + p.errors, 0);
  const activeUsers = (rawKpis ? readNumber(rawKpis.activeUsers ?? rawKpis.active_users) : null) ?? 0;
  const inputTokens =
    (rawKpis ? readNumber(rawKpis.inputTokens ?? rawKpis.input_tokens) : null) ??
    series.reduce((acc, p) => acc + p.inputTokens, 0);
  const outputTokens =
    (rawKpis ? readNumber(rawKpis.outputTokens ?? rawKpis.output_tokens) : null) ??
    series.reduce((acc, p) => acc + p.outputTokens, 0);
  const cachedTokens =
    (rawKpis ? readNumber(rawKpis.cachedTokens ?? rawKpis.cached_tokens) : null) ??
    series.reduce((acc, p) => acc + p.cachedTokens, 0);
  const p95LatencyMs = (rawKpis ? readNumber(rawKpis.p95LatencyMs ?? rawKpis.p95_latency_ms) : null) ?? null;
  const p95TtftMs = (rawKpis ? readNumber(rawKpis.p95TtftMs ?? rawKpis.p95_ttft_ms) : null) ?? null;

  const usersRaw = rawLeaders && Array.isArray(rawLeaders.users) ? rawLeaders.users : null;
  const modelsRaw = rawLeaders && Array.isArray(rawLeaders.models) ? rawLeaders.models : null;
  const errorsRaw = rawLeaders && Array.isArray(rawLeaders.errors) ? rawLeaders.errors : null;

  const users =
    usersRaw?.flatMap((u) => {
      if (!u || typeof u !== "object") return [];
      const row = u as Record<string, unknown>;
      const userId = readString(row.userId ?? row.user_id ?? row.id) ?? null;
      if (!userId) return [];
      return [
        {
          userId,
          email: typeof row.email === "string" ? row.email : null,
          spendUsd: readNumber(row.spendUsd ?? row.spend_usd ?? row.costUsd ?? row.cost_usd) ?? 0,
          calls: readNumber(row.calls ?? row.requests) ?? 0,
          errors: readNumber(row.errors ?? row.failures) ?? 0,
          totalTokens: readNumber(row.totalTokens ?? row.total_tokens ?? row.tokens) ?? 0
        }
      ];
    }) ?? [];

  const models =
    modelsRaw?.flatMap((m) => {
      if (!m || typeof m !== "object") return [];
      const row = m as Record<string, unknown>;
      const model = readString(row.model ?? row.name) ?? null;
      if (!model) return [];
      return [
        {
          model,
          spendUsd: readNumber(row.spendUsd ?? row.spend_usd ?? row.costUsd ?? row.cost_usd) ?? 0,
          calls: readNumber(row.calls ?? row.requests) ?? 0,
          errors: readNumber(row.errors ?? row.failures) ?? 0,
          totalTokens: readNumber(row.totalTokens ?? row.total_tokens ?? row.tokens) ?? 0
        }
      ];
    }) ?? [];

  const errorRows =
    errorsRaw?.flatMap((e) => {
      if (!e || typeof e !== "object") return [];
      const row = e as Record<string, unknown>;
      const key = readString(row.key ?? row.code ?? row.status ?? row.statusCode) ?? null;
      if (!key) return [];
      const count = readNumber(row.count ?? row.value) ?? 0;
      const share = readNumber(row.share) ?? null;
      return [{ key, count, share }];
    }) ?? [];

  const totalErrorCount = errorRows.reduce((acc, e) => acc + e.count, 0);
  const errorsLeaders = errorRows.map((e) => {
    const computedShare = totalErrorCount > 0 ? e.count / totalErrorCount : null;
    return { ...e, share: e.share ?? computedShare };
  });

  return {
    range: fallback.range,
    kpis: {
      spendUsd,
      calls,
      errors,
      activeUsers,
      inputTokens,
      outputTokens,
      cachedTokens,
      p95LatencyMs,
      p95TtftMs
    },
    series,
    leaders: {
      users,
      models,
      errors: errorsLeaders
    }
  };
}

function isAdminOverviewResponse(value: unknown): value is AdminOverviewResponse {
  if (!value || typeof value !== "object") return false;
  if (!("kpis" in value) || !("counts" in value) || !("health" in value) || !("events" in value)) return false;
  const kpis = (value as { kpis?: unknown }).kpis;
  const counts = (value as { counts?: unknown }).counts;
  return Boolean(kpis && typeof kpis === "object" && counts && typeof counts === "object");
}

function readBackendMessage(json: unknown) {
  if (!json || typeof json !== "object") return null;
  if ("message" in json && typeof (json as { message?: unknown }).message === "string") {
    const msg = String((json as { message?: string }).message ?? "");
    return msg.length > 0 ? msg : null;
  }
  return null;
}

async function getAnalytics(range: AdminAnalyticsResponse["range"]) {
  const qs = new URLSearchParams();
  qs.set("from", startOfDayUtc(range.from).toISOString());
  qs.set("to", endOfDayUtc(range.to).toISOString());
  qs.set("tz", range.tz);
  qs.set("granularity", range.granularity);
  qs.set("limit", "10");

  const res = await fetch(buildBackendUrl(`/admin/analytics?${qs.toString()}`), {
    cache: "no-store",
    headers: await getBackendAuthHeadersCached()
  });
  const json: unknown = await res.json().catch(() => null);
  if (!res.ok) {
    return {
      data: null as AdminAnalyticsResponse | null,
      supported: false,
      status: res.status,
      message: readBackendMessage(json)
    };
  }
  const data = normalizeAnalyticsResponse(json, { range });
  return {
    data,
    supported: data != null,
    status: res.status,
    message: data ? null : "Unexpected response shape"
  };
}

async function getOverviewFallback(range: AdminAnalyticsResponse["range"]) {
  const res = await fetch(buildBackendUrl("/admin/overview"), {
    cache: "no-store",
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json().catch(() => null);
  if (!isAdminOverviewResponse(json)) return null;
  const overview = json;

  const kpis: AdminAnalyticsResponse["kpis"] = {
    spendUsd: overview.kpis.spendUsd24h,
    calls: overview.kpis.calls24h,
    errors: overview.kpis.errors24h,
    activeUsers: overview.kpis.activeUsers24h,
    inputTokens: 0,
    outputTokens: 0,
    cachedTokens: 0,
    p95LatencyMs: null,
    p95TtftMs: null
  };

  return {
    range,
    kpis,
    series: [],
    leaders: { users: [], models: [], errors: [] }
  } satisfies AdminAnalyticsResponse;
}

interface AdminAnalyticsContentProps {
  searchParams: Record<string, string | string[] | undefined>;
}

export async function AdminAnalyticsContent({ searchParams }: AdminAnalyticsContentProps) {
  const tab = parseTab(getParam(searchParams, "tab"));
  const preset = parsePreset(getParam(searchParams, "range"));
  const tz = getParam(searchParams, "tz") ?? "UTC";

  const today = ymdUtc(new Date());
  let from = today;
  let to = today;

  if (preset === "today") {
    from = today;
    to = today;
  } else if (preset === "week") {
    const d = startOfDayUtc(today);
    const weekday = d.getUTCDay(); // 0 Sun .. 6 Sat
    const mondayOffset = (weekday + 6) % 7; // 0 if Monday
    from = ymdUtc(addDaysUtc(d, -mondayOffset));
    to = today;
  } else if (preset === "month") {
    const d = startOfDayUtc(today);
    const monthStart = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), 1));
    from = ymdUtc(monthStart);
    to = today;
  } else if (preset === "year") {
    const d = startOfDayUtc(today);
    const yearStart = new Date(Date.UTC(d.getUTCFullYear(), 0, 1));
    from = ymdUtc(yearStart);
    to = today;
  } else if (preset === "all") {
    from = "1970-01-01";
    to = today;
  } else if (preset === "custom") {
    const rawFrom = getParam(searchParams, "from");
    const rawTo = getParam(searchParams, "to");
    if (rawFrom && rawTo && isYmd(rawFrom) && isYmd(rawTo)) {
      if (rawFrom <= rawTo) {
        from = rawFrom;
        to = rawTo;
      } else {
        from = rawTo;
        to = rawFrom;
      }
    } else {
      const d = startOfDayUtc(today);
      const weekday = d.getUTCDay();
      const mondayOffset = (weekday + 6) % 7;
      from = ymdUtc(addDaysUtc(d, -mondayOffset));
      to = today;
    }
  } else {
    const d = startOfDayUtc(today);
    const weekday = d.getUTCDay();
    const mondayOffset = (weekday + 6) % 7;
    from = ymdUtc(addDaysUtc(d, -mondayOffset));
    to = today;
  }

  const daysInclusive = diffDaysInclusiveUtc(from, to);
  const granularity = toGranularity(daysInclusive);

  const range: AdminAnalyticsResponse["range"] = {
    preset,
    from,
    to,
    tz,
    granularity
  };

  const analyticsResult = await getAnalytics(range);
  const analytics = analyticsResult.data ?? (await getOverviewFallback(range));
  const fallback = analyticsResult.data == null;

  return (
    <AdminAnalyticsViewClient
      initialTab={tab}
      initialRange={range}
      initialData={analytics}
      showFallbackNotice={fallback}
    />
  );
}

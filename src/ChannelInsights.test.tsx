import { expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import type { ReactNode } from "react";
import { ChannelAccess } from "./ChannelAccess";
import { CacheTrend } from "./CacheTrend";
import { QualityHistory, QualityProbability, qualityTooltip } from "./QualityHistory";
import { dashboardURL, SiteLink } from "./ChannelSite";
import { withSubQuality } from "./ChannelChecks";
import type { Checks } from "./ChannelChecks";
import type { Channel } from "./types";
import type { InstalledChannel, SubImportsQuery } from "./sub2apiImports";
import { emptyStats, ranges } from "./analytics";
import { providerId } from "./format";
import { loadFilters, saveFilters, defaultFilters } from "./preferences";

const row = { source_id: "source-two", provider: "same", model: "gpt-6-astra", upstream_model: "gpt-6-astra", stats: emptyStats() } as Channel;
const installed = { source_id: row.source_id!, provider: row.provider, account_id: "account", group_id: 7, api_key_id: "key-scope", source_name: "来源二", key_position: 2, key_prefix: "masked", models: [row.model], manageable: true, revision: "r2" } as InstalledChannel;
function setup(children: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return { ...render(<QueryClientProvider client={client}><Tooltip.Provider>{children}</Tooltip.Provider></QueryClientProvider>), user: userEvent.setup() };
}

it("combines successful detection counts once and keeps the latest outcome across both paths", () => {
  const checks = { results: new Map([[providerId(row), { ...row, verdict: "error", checked_at: 100, history: { passed: 2, successful: 3, total: 4 } }]]) } as unknown as Checks;
  const summaries = [{ account_id: "account", group_id: 7, history: { passed: 1, successful: 1, total: 2 }, check: { source_id: "", provider: "", model: row.model, verdict: "pass" as const, checked_at: 99, text: "21", duration_ms: 15 } }];
  const result = withSubQuality(checks, [installed, installed], summaries).results.get(providerId(row))!;
  expect(result.verdict).toBe("error");
  expect(result.history).toEqual({ passed: 3, successful: 4, total: 6 });
  setup(<QualityProbability history={result.history} />);
  expect(screen.getByText("75.0%")).toBeVisible();
  expect(screen.getByText("75.0%")).toHaveClass("quality-poor");
  expect(qualityTooltip(result.history!)).toContain("75.0%");
  expect(qualityTooltip(result.history!)).toContain("3 / 4");
});

it("scopes cache history to source, key, model, upstream, endpoint and streaming without inventing missing cache samples", async () => {
  const fetchMock = vi.fn(async () => new Response(JSON.stringify({ total: { cache_rate: .1 }, from: 100, to: 300, bucket_seconds: 60, data: [{ ...row, points: [{ timestamp: 120, bucket_end: 180, cache_rate: .1, input_tokens: 1000, cache_read_tokens: 100, cache_samples: 2 }, { timestamp: 180, bucket_end: 240, cache_rate: null }, { timestamp: 240, bucket_end: 300, cache_rate: 0, input_tokens: 100, cache_read_tokens: 0, cache_samples: 1 }] }] })));
  vi.stubGlobal("fetch", fetchMock);
  setup(<CacheTrend row={row} connection={{ base: "", key: "", session: "fixture", account: true, sourceId: "all" }} keyId="source-two::key-scope" window="12h" endpoint="/v1/responses" stream="true" refresh={0} />);
  await screen.findByText("所选范围缓存率");
  const url = new URL(String((fetchMock.mock.calls[0] as unknown as [string, RequestInit | undefined])[0]), location.origin);
  expect(Object.fromEntries(url.searchParams)).toMatchObject({ source_id: "source-two", key_id: "key-scope", range: "12h", model: "gpt-6-astra", upstream_model: "gpt-6-astra", provider: "same", endpoint: "/v1/responses", stream: "true", timeseries: "true" });
  expect(screen.getAllByRole("img")).toHaveLength(2);
  expect(screen.getByText("0.0%")).toBeVisible();
});

it("loads secrets only on demand and removes the exact imported binding after confirmation", async () => {
  const fetchMock = vi.fn(async (_path: string, init?: RequestInit) => new Response(JSON.stringify(init?.method === "PATCH" ? { message: "已移除" } : { api_keys: ["fixture-business-key"] })));
  vi.stubGlobal("fetch", fetchMock);
  const removed = vi.fn();
  const app = setup(<ChannelAccess row={row} imports={{ data: { data: [installed] } } as SubImportsQuery} onRemoved={removed} />);
  expect(fetchMock).not.toHaveBeenCalled();
  await app.user.click(screen.getByRole("button", { name: "显示" }));
  expect(await screen.findByText("fixture-business-key")).toBeVisible();
  await app.user.click(screen.getByRole("button", { name: "复制" }));
  expect(await navigator.clipboard.readText()).toBe("fixture-business-key");
  expect(screen.getByRole("button", { name: "已复制" })).toBeVisible();
  await app.user.click(screen.getByRole("button", { name: "隐藏" }));
  expect(screen.queryByText("fixture-business-key")).not.toBeInTheDocument();
  await app.user.click(screen.getByRole("button", { name: "从此 API key 移除" }));
  expect(fetchMock).toHaveBeenCalledTimes(1);
  await app.user.click(screen.getByRole("button", { name: "确认移除" }));
  await waitFor(() => expect(removed).toHaveBeenCalledOnce());
  const mutation = fetchMock.mock.calls.find(([, init]) => init?.method === "PATCH");
  expect(JSON.parse(String(mutation?.[1]?.body))).toEqual({ action: "delete", account_id: "account", group_id: 7, source_id: "source-two", api_key_id: "key-scope", revision: "r2" });
});

it("pages quality history only when expanded and keeps unsuccessful attempts visible", async () => {
  const fetchMock = vi.fn(async (path: string) => new Response(JSON.stringify({ summary: { passed: 1, successful: 1, total: 2 }, data: [{ id: path.includes("before=2") ? "1" : "2", verdict: path.includes("before=2") ? "pass" : "error", checked_at: 123, result: { text: "fixture history" } }], next: path.includes("before=2") ? "" : "2" })));
  vi.stubGlobal("fetch", fetchMock);
  const app = setup(<QualityHistory path="/v1/sources/source-two/channel-checks/history?provider=same" />);
  expect(fetchMock).not.toHaveBeenCalled();
  await app.user.click(screen.getByRole("button", { name: "查看降智检测历史" }));
  expect(await screen.findByText("检测失败")).toBeVisible();
  await app.user.click(screen.getByRole("button", { name: "加载更早记录" }));
  expect(await screen.findByText("不降智")).toBeVisible();
});

it("keeps dashboard links safe and persists every hourly filter", () => {
  expect(dashboardURL("https://example.com/v1/responses?key=bad#part")).toBe("https://example.com/dashboard");
  expect(dashboardURL("javascript:alert(1)")).toBeUndefined();
  expect(dashboardURL("https://user:secret@example.com")).toBeUndefined();
  setup(<SiteLink base="https://example.com/v1">渠道</SiteLink>);
  expect(screen.getByRole("link", { name: "渠道" })).toHaveAttribute("href", "https://example.com/dashboard");
  for (let hour = 1; hour <= 24; hour++) {
    const value = `${hour}h`;
    expect(ranges.some(([range]) => range === value)).toBe(true);
    saveFilters("hourly-fixture", { ...defaultFilters, window: value });
    expect(loadFilters("hourly-fixture").window).toBe(value);
  }
});

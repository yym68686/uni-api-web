import { expect, it, vi } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { LazyMotion, domAnimation, MotionConfig } from "motion/react";
import App from "./App";
import { emptyStats } from "./analytics";
import { defaultFilters, saveFilters } from "./preferences";

it("shows and refreshes channel statistics independently of slow or failed billing", async () => {
  saveFilters(location.origin, { ...defaultFilters, window: "24h", model: "gpt-6-luna", keyId: "primary::caller" });
  const channels = ["gpt-6-luna", "gpt-6-astra"].map(model => ({
    source_id: "primary", source_name: "Fugue", provider: "test-channel", model, upstream_model: model,
    eligible: true, reason: "eligible", endpoint: "all", stream: null,
    stats: { ...emptyStats(), success: 9, failed: 1, success_rate_denominator: 10, success_rate: .9,
      first_text: { p50_ms: 1200, p95_ms: 2000, sample_count: 9 }, estimated_cost_usd: 2 },
  }));
  let metricsReads = 0;
  let finishRefresh: (() => void) | undefined;
  const bills: { url: URL; signal: AbortSignal; fail: () => void }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    const u = new URL(input, location.origin);
    if (u.pathname.endsWith("/auth/me")) return Response.json({ enabled: true, authenticated: true, username: "billing-test" });
    if (u.pathname.endsWith("/sources")) return Response.json({ data: [{ id: "primary", name: "Fugue", base: "https://gateway.test", has_storage: true }] });
    if (u.pathname.endsWith("/api-keys")) return Response.json({ can_inspect_all: true, data: [{ key_id: "primary::caller", source_id: "primary", position: 1, prefix: "masked" }] });
    if (u.pathname.endsWith("/model-channels") || u.pathname.endsWith("/channel-metrics")) return Response.json({ data: channels });
    if (u.pathname.endsWith("/channel-controls")) return Response.json({ revision: "r1", instance_id: "gateway", rules: [] });
    if (u.pathname.endsWith("/analytics")) {
      // The former combined request cannot finish while billing is pending.
      if (u.searchParams.has("include_spend")) throw new Error("signal timed out");
      metricsReads++;
      if (metricsReads === 2) await new Promise<void>(resolve => { finishRefresh = resolve; });
      return Response.json({ data: channels, total: { ...emptyStats(), requests: 20 }, from: 100, to: 200 + metricsReads, coverage: "full" });
    }
    if (u.pathname.endsWith("/channel-spend")) return new Promise<Response>((resolve, reject) => {
      const signal = init!.signal!;
      bills.push({ url: u, signal, fail: () => resolve(new Response("账单核对暂不可用", { status: 503 })) });
      signal.addEventListener("abort", () => reject(signal.reason), { once: true });
    });
    return Response.json({ data: [], total: { requests: 0 }, unavailable_sources: [] });
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 300000 } } });
  const app = render(<QueryClientProvider client={client}><MotionConfig reducedMotion="always"><LazyMotion features={domAnimation}><Tooltip.Provider><App /></Tooltip.Provider></LazyMotion></MotionConfig></QueryClientProvider>);
  const user = userEvent.setup();
  const table = await screen.findByRole("table");
  expect(within(table).getByText("90.0%")).toBeVisible();
  await waitFor(() => expect(bills).toHaveLength(1));
  expect(Object.fromEntries(bills[0].url.searchParams)).toMatchObject({ source_id: "primary", key_id: "caller", model: "gpt-6-luna", from: "100", to: "201" });
  expect(screen.queryByText("暂时无法读取渠道")).not.toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: "刷新数据" }));
  await waitFor(() => expect(bills[0].signal.aborted).toBe(true));
  expect(within(screen.getByRole("table")).getByText("90.0%")).toBeVisible();
  expect(bills).toHaveLength(1);
  await act(async () => finishRefresh!());
  await waitFor(() => expect(bills).toHaveLength(2));
  expect(bills[1].url.searchParams.get("to")).toBe("202");
  await act(async () => bills[1].fail());
  await screen.findByText("查询失败");
  expect(within(screen.getByRole("table")).getByText("90.0%")).toBeVisible();
  expect(screen.queryByText("暂时无法读取渠道")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "刷新数据" })).toBeEnabled();

  await user.selectOptions(screen.getByLabelText("模型筛选"), "gpt-6-astra");
  await waitFor(() => expect(bills).toHaveLength(3));
  expect(metricsReads).toBe(2);
  expect(bills[2].url.searchParams.get("model")).toBe("gpt-6-astra");
  expect(bills[2].url.searchParams.get("to")).toBe("202");
  app.unmount();
  client.clear();
});

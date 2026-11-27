import { expect, it, vi } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { LazyMotion, domAnimation, MotionConfig } from "motion/react";
import App from "./App";
import { emptyStats } from "./analytics";
import { defaultFilters, saveFilters } from "./preferences";
import { readWorkspace, workspaceHash } from "./workspaceLocation";

it("shows and refreshes channel statistics independently of slow or failed billing", async () => {
  localStorage.setItem("uni-console:channel-columns:v1", JSON.stringify(["success", "balance"]));
  saveFilters(JSON.stringify([location.origin, "billing-test"]), { ...defaultFilters, window: "24h", model: "gpt-6-luna", keyId: "primary::caller" });
  const channels = ["gpt-6-luna", "gpt-6-astra"].map(model => ({
    source_id: "primary", source_name: "Fugue", provider: "test-channel", model, upstream_model: model,
    eligible: true, reason: "eligible", endpoint: "all", stream: null,
    stats: { ...emptyStats(), success: 9, failed: 1, success_rate_denominator: 10, success_rate: .9,
      first_text: { p50_ms: 1200, p95_ms: 2000, sample_count: 9 }, estimated_cost_usd: 2 },
  }));
  let metricsReads = 0;
  const metricsModels: (string | null)[] = [];
  let finishRefresh: (() => void) | undefined;
  const bills: { url: URL; signal: AbortSignal; fail: () => void }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    const u = new URL(input, location.origin);
    if (u.pathname.endsWith("/auth/me")) return Response.json({ enabled: true, authenticated: true, username: "billing-test" });
    if (u.pathname.endsWith("/sources")) return Response.json({ data: [{ id: "primary", name: "Fugue", base: "https://gateway.test", has_storage: true }] });
    if (u.pathname.endsWith("/api-keys")) return Response.json({ can_inspect_all: true, data: [{ key_id: "primary::caller", source_id: "primary", position: 1, prefix: "masked" }] });
    if (u.pathname.endsWith("/model-channels") || u.pathname.endsWith("/channel-metrics")) return Response.json({ data: channels });
    if (u.pathname.endsWith("/sub2api/channels")) return Response.json({
      data: [], labels: {primary: {"test-channel": "渠道-2026-0.04"}},
      model_billing: {primary: {"test-channel": {
        "gpt-6-luna": {name: "渠道-2026-0.04", rate: 0.04, rate_source: "advertised"},
        "gpt-6-astra": {name: "渠道-2026-未知倍率", rate: null},
      }}},
      unavailable_sources: [],
    });
    if (u.pathname.endsWith("/channel-controls")) return Response.json({ revision: "r1", instance_id: "gateway", rules: [] });
    if (u.pathname.endsWith("/analytics")) {
      // The former combined request cannot finish while billing is pending.
      if (u.searchParams.has("include_spend")) throw new Error("signal timed out");
      metricsReads++;
      metricsModels.push(u.searchParams.get("model"));
      if (metricsReads === 2) await new Promise<void>(resolve => { finishRefresh = resolve; });
      return Response.json({ data: channels.filter(row => !u.searchParams.get("model") || row.model === u.searchParams.get("model")), total: { ...emptyStats(), requests: 20 }, from: 100, to: 200 + metricsReads, coverage: "full" });
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
  await screen.findByRole("heading",{name:"渠道观测"},{timeout:5000});
  const migratedTable = await screen.findByRole("table");
  expect(within(migratedTable).getAllByRole("columnheader").map(cell => cell.textContent))
    .toEqual(["渠道", "倍率", "尝试成功率", "站点余额"]);
  localStorage.removeItem("uni-console:channel-columns:v1");
  await user.click(document.querySelector<HTMLElement>(".channel-browser .workbench-disclosure:last-child summary")!);
  await user.selectOptions(screen.getByLabelText("渠道表列布局"),"all");
  const table = await screen.findByRole("table");
  const rateColumn = within(table).getByRole("columnheader", { name: "倍率" }) as HTMLTableCellElement;
  const channelRow = (await within(table).findByRole("button", { name: "查看 渠道-2026 gpt-6-luna 详情" })).closest("tr")!;
  expect(within(channelRow).getAllByRole("cell")[rateColumn.cellIndex]).toHaveTextContent(/^0\.04公$/);
  expect(within(channelRow).queryByText("渠道-2026-0.04")).not.toBeInTheDocument();
  expect(within(channelRow).queryByText("Fugue")).not.toBeInTheDocument();
  expect(within(table).getByText("90.0%")).toBeVisible();
  await waitFor(() => expect(bills).toHaveLength(1));
  expect(metricsModels).toEqual(["gpt-6-luna"]);
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
  expect(metricsReads).toBe(3);
  const unknownRow = (await screen.findByRole("button", { name: "查看 渠道-2026 gpt-6-astra 详情" })).closest("tr")!;
  expect(within(unknownRow).getAllByRole("cell")[rateColumn.cellIndex]).toHaveTextContent(/^—$/);
  expect(metricsModels).toEqual(["gpt-6-luna", "gpt-6-luna", "gpt-6-astra"]);
  expect(bills[2].url.searchParams.get("model")).toBe("gpt-6-astra");
  expect(bills[2].url.searchParams.get("to")).toBe("203");
  expect(screen.getByRole("option", { name: "gpt-6-luna" })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "总览" }));
  await waitFor(() => expect(metricsReads).toBe(4));
  expect(metricsModels[3]).toBeNull();
  app.unmount();
  client.clear();
});

it("limits billing and source labels to the filtered scope", async () => {
  saveFilters(JSON.stringify([location.origin, "provider-scope-test"]), { ...defaultFilters, window: "24h", model: "gpt-6-luna", search: "needle-channel" });
  const sources = [
    { id: "primary", name: "Fugue", base: "https://gateway.test", has_storage: true },
    { id: "backup", name: "备用", base: "https://backup.test", has_storage: true },
  ];
  const channels = ["needle-channel", "unrelated-channel"].map((provider, index) => ({
    source_id: sources[index].id, source_name: sources[index].name, provider, model: "gpt-6-luna", upstream_model: "gpt-6-luna",
    eligible: true, reason: "eligible", endpoint: "all", stream: null,
    stats: { ...emptyStats(), success: 9, failed: 1, success_rate_denominator: 10, success_rate: .9 },
  }));
  const bills: URL[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string) => {
    const u = new URL(input, location.origin);
    if (u.pathname.endsWith("/auth/me")) return Response.json({ enabled: true, authenticated: true, username: "provider-scope-test" });
    if (u.pathname.endsWith("/sources")) return Response.json({ data: sources });
    if (u.pathname.endsWith("/api-keys")) return Response.json({ can_inspect_all: true, data: [{ key_id: "primary::caller", source_id: "primary", position: 1, prefix: "masked" }] });
    if (u.pathname.endsWith("/model-channels") || u.pathname.endsWith("/channel-metrics")) return Response.json({ data: channels });
    if (u.pathname.endsWith("/channel-controls")) return Response.json({ revision: "r1", instance_id: "gateway", rules: [] });
    if (u.pathname.endsWith("/analytics")) return Response.json({ data: channels, total: { ...emptyStats(), requests: 20 }, from: 100, to: 200, coverage: "full" });
    if (u.pathname.endsWith("/channel-spend")) {
      bills.push(u);
      return Response.json({ data: channels.filter(c => !u.searchParams.get("provider") || c.provider === u.searchParams.get("provider")).map(c => ({ ...c, status: "complete", scope: "matched_requests", from: 100, to: 200, actual_cost_usd: c.provider === "needle-channel" ? 1 : 2 })) });
    }
    return Response.json({ data: [], total: { requests: 0 }, unavailable_sources: [] });
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const app = render(<QueryClientProvider client={client}><MotionConfig reducedMotion="always"><LazyMotion features={domAnimation}><Tooltip.Provider><App /></Tooltip.Provider></LazyMotion></MotionConfig></QueryClientProvider>);
  const user = userEvent.setup();
  await screen.findByRole("heading",{name:"渠道观测"},{timeout:5000});
  await user.click(document.querySelector<HTMLElement>(".channel-browser .workbench-disclosure:last-child summary")!);
  await user.selectOptions(screen.getByLabelText("渠道表列布局"),"all");
  await waitFor(() => expect(bills).toHaveLength(1));
  expect(bills[0].searchParams.get("provider")).toBe("needle-channel");
  expect(await screen.findByText("$1.00")).toBeVisible();
  const table = within(screen.getByRole("table"));
  expect(table.queryByText("Fugue")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "清除搜索" }));
  await waitFor(() => expect(bills).toHaveLength(2));
  expect(bills[1].searchParams.has("provider")).toBe(false);
  expect(await screen.findByText("$2.00")).toBeVisible();
  expect(table.getByText("Fugue")).toBeVisible();
  expect(table.getByText("备用")).toBeVisible();
  const reads = bills.length;
  await user.type(screen.getByRole("textbox", { name: "搜索渠道或模型" }), "nonexistent-provider");
  await screen.findByText("没有匹配的渠道");
  expect(bills).toHaveLength(reads);
  app.unmount();
  client.clear();
});

it("opens the exact source, channel and model from the profit rankings", async () => {
  const overview = { ...defaultFilters, keyId: "primary::caller", window: "7d", endpoint: "/v1/responses", stream: "true" };
  history.replaceState(null, "", workspaceHash("overview", overview));
  saveFilters(JSON.stringify([location.origin, "profit-ranking-test"]), { ...defaultFilters, search: "old unrelated search" });
  const makeChannel = (provider: string, model: string, source_id = "primary") => ({
    provider, model, source_id, source_name: source_id === "primary" ? "Fugue" : "备用",
    upstream_model: model, eligible: true, reason: "eligible", endpoint: "all", stream: null,
    stats: { ...emptyStats(), started: 1, requests: 1, usage_samples: 1, priced_samples: 1,
      estimated_cost_usd: 100, sale_percent: 10 },
  });
  const a = makeChannel("target", "gpt-a"), b = makeChannel("target", "gpt-b");
  const similarlyNamed = makeChannel("target-backup", "gpt-b");
  const otherSource = makeChannel("target", "gpt-b", "backup");
  const historical = makeChannel("retired", "gpt-retired");
  const catalog = [a, b, similarlyNamed, otherSource];
  const historyRows = [a, b, similarlyNamed, historical];
  vi.stubGlobal("fetch", vi.fn(async (input: string) => {
    const url = new URL(input, location.origin);
    const { pathname, searchParams } = url;
    if (pathname.endsWith("/auth/me")) return Response.json({ enabled: true, authenticated: true, username: "profit-ranking-test" });
    if (pathname.endsWith("/sources")) return Response.json({ data: ["primary", "backup"].map(id => ({id, name:id, base:`https://${id}.test`, has_storage:true})) });
    if (pathname.endsWith("/api-keys")) return Response.json({ can_inspect_all: true, data: [{ key_id: "primary::caller", source_id: "primary", position: 1, prefix: "masked" }] });
    if (pathname.endsWith("/model-channels") || pathname.endsWith("/channel-metrics")) return Response.json({ data: catalog });
    if (pathname.endsWith("/analytics")) return Response.json({ data: historyRows.filter(row => !searchParams.get("model") || row.model === searchParams.get("model")),
      total: { ...emptyStats(), requests: 4 }, from: 100, to: 200, coverage: "full" });
    if (pathname.endsWith("/channel-spend")) return Response.json({ data: historyRows.map((row, i) => ({ ...row,
      status: "complete", scope: "matched_requests", actual_cost_usd: i === 2 ? 19 : 100 + i, from: 100, to: 200 })) });
    return Response.json({ data: [], unavailable_sources: [] });
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const app = render(<QueryClientProvider client={client}><MotionConfig reducedMotion="always"><LazyMotion features={domAnimation}><Tooltip.Provider><App /></Tooltip.Provider></LazyMotion></MotionConfig></QueryClientProvider>);
  const user = userEvent.setup();
  try {
    const losses = within(await screen.findByRole("region", { name: "亏损渠道 TOP 8" }));
    expect(await losses.findByRole("button", {name: "在渠道观测筛选 target · gpt-a"})).toBeVisible();
    await user.click(losses.getByRole("button", {name: "在渠道观测筛选 target · gpt-b"}));
    const expected = { ...overview, sourceId: "primary", provider: "target", model: "gpt-b" };
    expect(readWorkspace(location.hash, true)).toMatchObject({ view: "channels", detail: null, filters: expected });
    const table = within(await screen.findByRole("region", { name: "渠道观测表" }));
    expect(table.getAllByRole("row")).toHaveLength(2);
    expect(table.getByRole("button", { name: "查看 target gpt-b 详情" })).toBeVisible();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "清除渠道筛选" }));
    expect(table.getByRole("button", { name: "查看 target-backup gpt-b 详情" })).toBeVisible();
    expect(readWorkspace(location.hash,true).filters).toEqual({ ...expected, provider: "" });

    await act(async () => history.back());
    const returned = await screen.findByRole("button", { name: "在渠道观测筛选 retired · gpt-retired" });
    expect(readWorkspace(location.hash, true).filters).toEqual(overview);
    await user.click(returned);
    expect(await screen.findByText(/当前配置或所选 API Key 中没有此渠道与模型/)).toBeVisible();
    expect(screen.queryByRole("region", {name: "渠道观测表"})).not.toBeInTheDocument();
    expect(screen.getByRole("button", {name: "清除渠道筛选"})).toHaveTextContent("retired · gpt-retired");
    await act(async () => history.back());
    await screen.findByRole("heading", { name: "总览" });
    await act(async () => history.forward());
    await screen.findByText(/当前配置或所选 API Key 中没有此渠道与模型/);
    expect(readWorkspace(location.hash,true).filters).toMatchObject({ sourceId: "primary", provider: "retired", model: "gpt-retired" });
  } finally {
    app.unmount();
    client.clear();
    history.replaceState(null, "", "/");
  }
});

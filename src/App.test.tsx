import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { MotionConfig, LazyMotion, domAnimation } from "motion/react";
import { LegacyConsole as App } from "./App";
import { defaultFilters, saveFilters } from "./preferences";
import { loadConnection, saveConnection } from "./session";
const rows = ["first", "second", "third"].map((provider, i) => ({
  provider,
  model: "model-a",
  upstream_model: "upstream-a",
  endpoint: "/v1/responses",
  stream: true,
  eligible: i !== 2,
  reason: i !== 2 ? "eligible" : "channel_cooldown",
  stats: {
    success: i === 0 ? 3 : 0,
    failed: i === 0 ? 1 : 0,
    success_rate_denominator: i === 0 ? 4 : 0,
    success_rate: i === 0 ? 0.75 : null,
    response_created: { p50_ms: 1500, p95_ms: 3000, sample_count: 4 },
    first_text: { p50_ms: 5500, p95_ms: 9000, sample_count: 4 },
    request_to_dispatch: {
      p50_ms: 50,
      p95_ms: 100,
      last_ms: 42,
      sample_count: 4,
    },
  },
}));
function setup(
  options: {
    keyStatus?: number;
    keyNetworkError?: boolean;
    denyPlatform?: boolean;
    legacyMetrics?: boolean;
    historyInitializing?: boolean;
  } = {},
) {
  const calls: string[] = [];
  const fetchMock = vi.fn(async (input: string, init?: RequestInit) => {
    calls.push(input);
    expect(input).not.toContain("platform-secret");
    expect((init?.headers as Record<string, string>).Authorization).toBe(
      "Bearer platform-secret",
    );
    const url = new URL(input);
    if (url.pathname.endsWith("api-keys")) {
      if (options.keyNetworkError) throw new TypeError("Failed to fetch");
      if (options.keyStatus)
        return new Response("{}", { status: options.keyStatus });
    }
    if (url.pathname.endsWith("/analytics") && options.historyInitializing) return new Response(JSON.stringify({ code: "analytics_initializing" }), { status: 503 });
    const selected = url.searchParams.has("api_key_id");
    const data = selected ? [rows[2], rows[0]] : rows;
    const result = url.pathname.endsWith("api-keys")
      ? {
          can_inspect_all: !options.denyPlatform,
          data: [
            { position: 1, key_id: "key-first", prefix: "••••" },
            { position: 2, key_id: "key-second", prefix: "••••" },
          ],
        }
      : url.pathname.endsWith("channel-balances")
        ? {
            provider: url.searchParams.get("provider"),
            status: "complete",
            key_count: 1,
            keys: [
              {
                position: 1,
                status:
                  url.searchParams.get("provider") === "second"
                    ? "timeout"
                    : "ok",
                kind: "wallet",
                amount: url.searchParams.get("provider") === "third" ? -1 : 100,
                currency: "USD",
              },
            ],
          }
        : {
            data,
            total: options.legacyMetrics ? undefined : { requests: 4 },
            models: [],
            filters: options.legacyMetrics
              ? undefined
              : {
                  endpoint: url.searchParams.get("endpoint"),
                  stream: url.searchParams.get("stream"),
                },
            available_endpoints: [
              "/v1/messages",
              "/v1/chat/completions",
              "/v1/responses",
            ],
            snapshot_revision: "v1",
            coverage: "complete_for_instance",
            generated_at: 1000,
            window_minutes: 15,
          };
    return new Response(JSON.stringify(result), { status: 200 });
  });
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 300000, gcTime: 0 } },
  });
  const { unmount } = render(
    <QueryClientProvider client={client}>
      <MotionConfig reducedMotion="always">
        <LazyMotion features={domAnimation}>
          <Tooltip.Provider>
            <App />
          </Tooltip.Provider>
        </LazyMotion>
      </MotionConfig>
    </QueryClientProvider>,
  );
  return { user: userEvent.setup(), calls, client, unmount };
}
async function connect(
  user: ReturnType<typeof userEvent.setup>,
  base = "https://mock.example/v1",
  expectTable = true,
) {
  await user.type(screen.getByLabelText(/服务地址/), base);
  await user.type(screen.getByLabelText(/访问密钥/), "platform-secret");
  await user.click(screen.getByRole("button", { name: /进入控制台/ }));
  if (expectTable) await screen.findByRole("table");
}
describe("dashboard workflows", () => {
  it("keeps key routing order, filters exhausted balances, exposes timing and clears credentials on disconnect", async () => {
    const { user, client } = setup();
    await connect(user);
    await waitFor(() => expect(screen.getByText("$-1.00")).toBeInTheDocument());
    await user.selectOptions(
      screen.getByLabelText("API key 筛选"),
      "key-second",
    );
    await waitFor(() => expect(screen.getAllByRole("row")).toHaveLength(3));
    expect(
      within(screen.getAllByRole("row")[1]).getByText("third"),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /^余额不足$/ }));
    await waitFor(() => expect(screen.getAllByRole("row")).toHaveLength(2));
    expect(
      screen.queryByRole("button", { name: /查看 first/ }),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /查看 third/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("最近一次请求前等待")).toBeInTheDocument();
    expect(within(dialog).getByText("42 ms")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: "断开连接" }));
    await screen.findByRole("button", { name: /进入控制台/ });
    expect(screen.getByLabelText(/访问密钥/)).toHaveValue("");
    expect(JSON.stringify(client.getQueryCache().getAll())).not.toContain(
      "platform-secret",
    );
    expect(JSON.stringify(localStorage)).not.toContain("platform-secret");
    expect(JSON.stringify(sessionStorage)).not.toContain("platform-secret");
  });
  it("filters balance rows by per-model priority and exclusive USD threshold", async () => {
    const { user, calls } = setup();
    await connect(user);
    await user.click(screen.getByRole("button", { name: /^余额管理/ }));
    const table = await screen.findByRole("table");
    await waitFor(() => expect(within(table).getByText("third")).toBeInTheDocument());

    for (const label of [
      "时间范围筛选",
      "端点筛选",
      "流式状态筛选",
      "渠道状态筛选",
      "排序",
    ]) {
      expect(screen.queryByLabelText(label)).not.toBeInTheDocument();
    }
    await user.selectOptions(screen.getByLabelText("模型筛选"), "model-a");
    await user.selectOptions(
      screen.getByLabelText("模型优先级筛选"),
      "1",
    );
    await waitFor(() => {
      expect(screen.getAllByRole("row")).toHaveLength(2);
      expect(within(screen.getAllByRole("row")[1]).getByText("first")).toBeInTheDocument();
    });

    const threshold = screen.getByLabelText("余额低于美元");
    await user.clear(threshold);
    await user.type(threshold, "0");
    await waitFor(() => {
      expect(screen.getByText("没有匹配的渠道")).toBeInTheDocument();
    });
    await user.selectOptions(
      screen.getByLabelText("模型优先级筛选"),
      "",
    );
    await waitFor(() => {
      expect(screen.getAllByRole("row")).toHaveLength(2);
      expect(within(screen.getAllByRole("row")[1]).getByText("third")).toBeInTheDocument();
    });
    const balanceCatalog = calls
      .filter((url) => new URL(url).pathname.endsWith("/v1/model-channels"))
      .map((url) => new URL(url))
      .at(-1)!;
    expect(balanceCatalog.searchParams.get("endpoint")).toBe("all");
    expect(balanceCatalog.searchParams.get("stream")).toBe("all");
  });
  it("shows auth error without entering a dashboard", async () => {
    const { user } = setup();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("{}", { status: 403 })),
    );
    await user.type(screen.getByLabelText(/服务地址/), "https://mock.example");
    await user.type(screen.getByLabelText(/访问密钥/), "business-key");
    await user.click(screen.getByRole("button", { name: /进入控制台/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "密钥没有平台查看权限",
    );
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });
  it("restores every filter after a fresh page mount, isolates services and persists reset", async () => {
    let app = setup();
    await connect(app.user);
    await app.user.selectOptions(
      screen.getByLabelText("API key 筛选"),
      "key-second",
    );
    await app.user.selectOptions(screen.getByLabelText("模型筛选"), "model-a");
    await app.user.selectOptions(screen.getByLabelText("时间范围筛选"), "1h");
    await app.user.type(screen.getByLabelText("搜索渠道或模型"), "third");
    await app.user.selectOptions(
      screen.getByLabelText("渠道状态筛选"),
      "unavailable",
    );
    await app.user.selectOptions(screen.getByLabelText("排序"), "latency");
    await app.user.selectOptions(
      screen.getByLabelText("端点筛选"),
      "/v1/messages",
    );
    await app.user.selectOptions(screen.getByLabelText("流式状态筛选"), "true");
    await app.user.click(screen.getByRole("button", { name: /^余额不足$/ }));
    await waitFor(() => expect(screen.getAllByRole("row")).toHaveLength(2));
    app.unmount();

    app = setup();
    await screen.findByRole("table");
    expect(screen.queryByLabelText(/访问密钥/)).not.toBeInTheDocument();
    expect(app.calls[0]).toBe("https://mock.example/v1/api-keys");
    expect(screen.getByLabelText("API key 筛选")).toHaveValue("key-second");
    expect(screen.getByLabelText("模型筛选")).toHaveValue("model-a");
    expect(screen.getByLabelText("时间范围筛选")).toHaveValue("1h");
    expect(screen.getByLabelText("搜索渠道或模型")).toHaveValue("third");
    expect(screen.getByLabelText("渠道状态筛选")).toHaveValue("unavailable");
    expect(screen.getByLabelText("排序")).toHaveValue("latency");
    expect(screen.getByLabelText("端点筛选")).toHaveValue("/v1/messages");
    expect(screen.getByLabelText("流式状态筛选")).toHaveValue("true");
    expect(screen.getByRole("button", { name: /^余额不足$/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getAllByRole("row")).toHaveLength(2);
    const firstMetrics = new URL(
      app.calls.find((url) => url.includes("/analytics/v1/analytics"))!,
    );
    expect(firstMetrics.searchParams.get("key_id")).toBe("key-second");
    expect(firstMetrics.searchParams.get("range")).toBe("1h");
    expect(firstMetrics.searchParams.get("endpoint")).toBe("/v1/messages");
    expect(firstMetrics.searchParams.get("stream")).toBe("true");
    expect(JSON.stringify(localStorage)).not.toContain("platform-secret");

    await app.user.click(screen.getByRole("button", { name: "断开连接" }));
    await screen.findByRole("button", { name: /进入控制台/ });
    await connect(app.user, "https://other.example");
    expect(screen.getByLabelText("API key 筛选")).toHaveValue("");
    expect(screen.getByLabelText("模型筛选")).toHaveValue("");
    expect(screen.getByLabelText("搜索渠道或模型")).toHaveValue("");
    expect(screen.getAllByRole("row")).toHaveLength(4);
    await app.user.click(screen.getByRole("button", { name: "断开连接" }));
    await screen.findByRole("button", { name: /进入控制台/ });
    app.unmount();

    app = setup();
    await connect(app.user);
    expect(screen.getByLabelText("搜索渠道或模型")).toHaveValue("third");
    await app.user.click(screen.getByRole("button", { name: "重置筛选" }));
    await waitFor(() => expect(screen.getAllByRole("row")).toHaveLength(4));
    app.unmount();
    app = setup();
    await screen.findByRole("table");
    expect(screen.getByLabelText("API key 筛选")).toHaveValue("");
    expect(screen.getByLabelText("模型筛选")).toHaveValue("");
    expect(screen.getByLabelText("搜索渠道或模型")).toHaveValue("");
    expect(screen.getByLabelText("排序")).toHaveValue("config");
    expect(screen.getByLabelText("端点筛选")).toHaveValue("all");
    expect(screen.getByLabelText("流式状态筛选")).toHaveValue("all");
    expect(screen.getByLabelText("渠道状态筛选")).toHaveValue("");
    expect(screen.getByLabelText("时间范围筛选")).toHaveValue("15m");
    expect(screen.getByRole("button", { name: /^余额不足$/ })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    expect(
      screen.queryByRole("button", { name: "重置筛选" }),
    ).not.toBeInTheDocument();
  });
  it("keeps a removed saved key visible and does not query all channels instead", async () => {
    saveFilters("https://mock.example", {
      ...defaultFilters,
      keyId: "key-removed",
    });
    const { user, calls } = setup();
    await connect(user, "https://mock.example", false);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "所选 API key 已移除",
    );
    expect(screen.getByLabelText("API key 筛选")).toHaveValue("key-removed");
    expect(calls).toHaveLength(1);
    await user.click(screen.getByRole("button", { name: "重置筛选" }));
    await screen.findByRole("table");
  });
  it("defaults to both aggregate dimensions and scopes catalog, metrics and trend queries independently", async () => {
    const { user, calls } = setup();
    await connect(user);
    const first = new URL(
      calls.find((url) => url.includes("/analytics/v1/analytics"))!,
    );
    expect(first.searchParams.get("endpoint")).toBe("all");
    expect(first.searchParams.get("stream")).toBe("all");
    await user.selectOptions(screen.getByLabelText("端点筛选"), "/v1/messages");
    await user.selectOptions(screen.getByLabelText("流式状态筛选"), "false");
    await user.click(screen.getByRole("button", { name: "查看趋势" }));
    await waitFor(() =>
      expect(calls.some((url) => new URL(url).searchParams.has("timeseries"))).toBe(true),
    );
    for (const path of [
      "/v1/model-channels",
      "/analytics/v1/analytics",
    ]) {
      const query = new URL(
        calls.filter((url) => new URL(url).pathname === path).at(-1)!,
      );
      expect(query.searchParams.get("endpoint")).toBe("/v1/messages");
      expect(query.searchParams.get("stream")).toBe("false");
    }
    await user.selectOptions(screen.getByLabelText("端点筛选"), "all");
    await waitFor(() =>
      expect(
        calls.some(
          (url) =>
            new URL(url).searchParams.has("timeseries") &&
            new URL(url).searchParams.get("endpoint") === "all" &&
            new URL(url).searchParams.get("stream") === "false",
        ),
      ).toBe(true),
    );
  });
  it("queries only the selected history range on entry and refresh", async () => {
    const { user, calls } = setup();
    await connect(user);
    await user.selectOptions(screen.getByLabelText("时间范围筛选"), "4h");
    await waitFor(() => expect(screen.getByRole("button", { name: "刷新数据" })).toBeEnabled());
    await user.click(screen.getByRole("button", { name: "刷新数据" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "刷新数据" })).toBeEnabled());
    const history = calls.map(url => new URL(url)).filter(url => url.pathname.endsWith("/analytics"));
    expect(history.length).toBeGreaterThanOrEqual(3);
    expect([...new Set(history.map(url => url.searchParams.get("range")))].sort()).toEqual(["15m", "4h"]);
  });
  it("shows invalid analytics as an error without falling back to volatile statistics", async () => {
    const options = { legacyMetrics: true };
    const { user, calls } = setup(options);
    await connect(user, "https://mock.example", false);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "无效统计数据",
    );
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(calls.some(url => new URL(url).pathname === "/v1/channel-metrics" && new URL(url).searchParams.get("window") !== "1m")).toBe(false);
    options.legacyMetrics = false;
    await user.click(screen.getByRole("button", {name:"重新读取"}));
    await screen.findByRole("table");
  });
  it.each([401, 403])(
    "clears a restored credential rejected with HTTP %s and returns to login",
    async (keyStatus) => {
      saveConnection({
        base: "https://mock.example",
        key: "platform-secret",
        session: "previous-page",
      });
      const { calls } = setup({ keyStatus });
      await screen.findByRole("button", { name: /进入控制台/ });
      expect(await screen.findByRole("alert")).toHaveTextContent(
        "密钥没有平台查看权限",
      );
      expect(screen.getByLabelText(/访问密钥/)).toHaveValue("");
      expect(loadConnection()).toBeNull();
      expect(calls).toEqual(["https://mock.example/v1/api-keys"]);
    },
  );
  it("requires platform permission before restoring channel queries", async () => {
    saveConnection({
      base: "https://mock.example",
      key: "platform-secret",
      session: "previous-page",
    });
    const { calls } = setup({ denyPlatform: true });
    await screen.findByRole("button", { name: /进入控制台/ });
    expect(loadConnection()).toBeNull();
    expect(calls).toHaveLength(1);
  });
  it("keeps a restored session on network failure and retries authentication before channels", async () => {
    saveConnection({
      base: "https://mock.example",
      key: "platform-secret",
      session: "previous-page",
    });
    const options = { keyNetworkError: true };
    const { user, calls } = setup(options);
    expect(await screen.findByRole("alert")).toHaveTextContent("无法连接服务");
    expect(screen.queryByLabelText(/访问密钥/)).not.toBeInTheDocument();
    expect(loadConnection()?.base).toBe("https://mock.example");
    expect(calls).toHaveLength(1);
    options.keyNetworkError = false;
    await user.click(screen.getByRole("button", { name: "重新读取" }));
    await screen.findByRole("table");
    expect(calls.slice(0, 2)).toEqual([
      "https://mock.example/v1/api-keys",
      "https://mock.example/v1/api-keys",
    ]);
    await user.click(screen.getByRole("button", { name: "断开连接" }));
    await screen.findByRole("button", { name: /进入控制台/ });
    expect(loadConnection()).toBeNull();
  });
});

it("retains last successful rows during warmup but never reuses them for a different API key", async () => {
  const options = { historyInitializing: false };
  const { user } = setup(options);
  await connect(user);
  expect(within(screen.getByRole("table")).getByText("first")).toBeVisible();
  options.historyInitializing = true;
  await user.click(screen.getByRole("button", { name: "刷新数据" }));
  await screen.findByText(/历史数据正在初始化/);
  expect(within(screen.getByRole("table")).getByText("first")).toBeVisible();
  expect(screen.getByText(/暂显示上次成功读取的数据/)).toBeVisible();
  expect(screen.queryByText(/请检查来源的 S3 导出配置/)).not.toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("API key 筛选"), "key-second");
  await screen.findByLabelText("加载渠道");
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
  expect(screen.queryByText(/暂显示上次成功读取的数据/)).not.toBeInTheDocument();
  options.historyInitializing = false;
  await user.click(screen.getByRole("button", { name: "刷新数据" }));
  await screen.findByRole("table");
  expect(screen.queryByText(/历史数据正在初始化/)).not.toBeInTheDocument();
});

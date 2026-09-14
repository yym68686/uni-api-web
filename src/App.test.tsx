import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { MotionConfig, LazyMotion, domAnimation } from "motion/react";
import App from "./App";
import { defaultFilters, saveFilters } from "./preferences";
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
    first_output: { p50_ms: 1500, p95_ms: 3000, sample_count: 4 },
    request_to_dispatch: {
      p50_ms: 50,
      p95_ms: 100,
      last_ms: 42,
      sample_count: 4,
    },
  },
}));
function setup() {
  const calls: string[] = [];
  const fetchMock = vi.fn(async (input: string, init?: RequestInit) => {
    calls.push(input);
    expect(input).not.toContain("platform-secret");
    expect((init?.headers as Record<string, string>).Authorization).toBe(
      "Bearer platform-secret",
    );
    const url = new URL(input);
    const selected = url.searchParams.has("api_key_id");
    const data = selected ? [rows[2], rows[0]] : rows;
    const result = url.pathname.endsWith("api-keys")
      ? {
          can_inspect_all: true,
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
    await app.user.click(screen.getByRole("button", { name: "1 小时" }));
    await app.user.type(screen.getByLabelText("搜索渠道或模型"), "third");
    await app.user.selectOptions(
      screen.getByLabelText("渠道状态筛选"),
      "unavailable",
    );
    await app.user.selectOptions(screen.getByLabelText("排序"), "latency");
    await app.user.click(screen.getByRole("button", { name: /^余额不足$/ }));
    await waitFor(() => expect(screen.getAllByRole("row")).toHaveLength(2));
    app.unmount();

    app = setup();
    await connect(app.user, "https://mock.example/");
    expect(screen.getByLabelText("API key 筛选")).toHaveValue("key-second");
    expect(screen.getByLabelText("模型筛选")).toHaveValue("model-a");
    expect(screen.getByRole("button", { name: "1 小时" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByLabelText("搜索渠道或模型")).toHaveValue("third");
    expect(screen.getByLabelText("渠道状态筛选")).toHaveValue("unavailable");
    expect(screen.getByLabelText("排序")).toHaveValue("latency");
    expect(screen.getByRole("button", { name: /^余额不足$/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getAllByRole("row")).toHaveLength(2);
    const firstMetrics = new URL(
      app.calls.find((url) => url.includes("channel-metrics"))!,
    );
    expect(firstMetrics.searchParams.get("api_key_id")).toBe("key-second");
    expect(firstMetrics.searchParams.get("window")).toBe("1h");
    expect(JSON.stringify(localStorage)).not.toContain("platform-secret");

    await app.user.click(screen.getByRole("button", { name: "断开连接" }));
    await screen.findByRole("button", { name: /进入控制台/ });
    await connect(app.user, "https://other.example");
    expect(screen.getByLabelText("API key 筛选")).toHaveValue("");
    expect(screen.getByLabelText("模型筛选")).toHaveValue("");
    expect(screen.getByLabelText("搜索渠道或模型")).toHaveValue("");
    expect(screen.getAllByRole("row")).toHaveLength(4);
    app.unmount();

    app = setup();
    await connect(app.user);
    expect(screen.getByLabelText("搜索渠道或模型")).toHaveValue("third");
    await app.user.click(screen.getByRole("button", { name: "重置筛选" }));
    await waitFor(() => expect(screen.getAllByRole("row")).toHaveLength(4));
    app.unmount();
    app = setup();
    await connect(app.user);
    expect(screen.getByLabelText("API key 筛选")).toHaveValue("");
    expect(screen.getByLabelText("模型筛选")).toHaveValue("");
    expect(screen.getByLabelText("搜索渠道或模型")).toHaveValue("");
    expect(screen.getByLabelText("排序")).toHaveValue("config");
    expect(screen.getByLabelText("渠道状态筛选")).toHaveValue("");
    expect(screen.getByRole("button", { name: "15 分钟" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
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
});

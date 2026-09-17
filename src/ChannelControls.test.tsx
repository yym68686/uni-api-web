import { expect, it, vi, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import * as Tooltip from "@radix-ui/react-tooltip";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChannelControls, inheritedDisabled } from "./ChannelControls";
import type { ControlState } from "./ChannelControls";

afterEach(() => vi.unstubAllGlobals());
it("stages scoped edits, saves through source API, and restores manually", async () => {
  let state: ControlState = {
    revision: "boot:0",
    instance_id: "boot",
    config_revision: "config",
    rules: [],
    reset_on_restart: true,
  };
  const mutations: any[] = [];
  const queries: URL[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const u = new URL(input, "https://console.test");
      queries.push(u);
      let body: any;
      if (u.pathname.endsWith("channel-controls")) {
        if (init?.method === "POST") {
          const data = JSON.parse(init.body as string);
          mutations.push(data);
          state = {
            ...state,
            revision: "boot:" + mutations.length,
            rules:
              data.action === "reset"
                ? []
                : [
                    {
                      api_key_id: data.api_key_id,
                      model: data.model,
                      order: data.order,
                      disabled: data.disabled,
                    },
                  ],
          };
        }
        body = state;
      } else if (u.pathname.endsWith("api-keys"))
        body = {
          data: [{ key_id: "do::key-one", prefix: "masked", position: 1 }],
        };
      else if (u.pathname.endsWith("channel-balances"))
        body = {
          provider: u.searchParams.get("provider"),
          status: "complete",
          actual_cost_usd: 9.25,
          keys: [
            {
              position: 1,
              status: "ok",
              kind: "wallet",
              amount: 123,
              currency: "USD",
            },
          ],
        };
      else {
        const isMetrics =
          u.pathname.endsWith("analytics") ||
          u.pathname.endsWith("channel-metrics");
        const requests = u.searchParams.get("range") === "today" ? 28 : 7;
        body = {
          data: ["first", "second"].map((provider) => ({
            source_id: "do",
            provider,
            model: "m",
            upstream_model: "m",
            endpoint: "all",
            stream: null,
            eligible: true,
            reason: "eligible",
            stats: isMetrics
              ? {
                  success: requests,
                  failed: 0,
                  success_rate_denominator: requests,
                  success_rate: 1,
                  inflight: 3,
                  started: requests,
                  input_tokens: 1000,
                  output_tokens: 200,
                  usage_samples: requests,
                  cache_rate: 0.75,
                  estimated_cost_usd: 2.5,
                  first_output: {
                    p50_ms: 400,
                    p95_ms: 900,
                    sample_count: requests,
                  },
                  request_to_dispatch: {
                    p50_ms: 50,
                    p95_ms: 200,
                    sample_count: requests,
                  },
                }
              : undefined,
          })),
          snapshot_revision: "config",
          total: { requests },
          generated_at: Date.now() / 1000,
          coverage: "full",
        };
      }
      return new Response(JSON.stringify(body));
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const user = userEvent.setup();
  const app = render(
    <QueryClientProvider client={client}>
      <Tooltip.Provider>
        <ChannelControls
          connection={{
            base: "https://console.test",
            key: "",
            account: true,
            session: "test",
            sourceId: "do",
          }}
          sources={[
            {
              id: "do",
              name: "DigitalOcean",
              base: "https://gateway.test",
              created_at: 0,
              has_storage: true,
            },
          ]}
          initialKey=""
          initialModel=""
          onApplied={() => {}}
        />
      </Tooltip.Provider>
    </QueryClientProvider>,
  );
  await screen.findByLabelText("临时停用 first");
  await user.selectOptions(
    screen.getByLabelText("控制 API key"),
    "do::key-one",
  );
  await user.selectOptions(screen.getByLabelText("控制模型"), "m");
  await screen.findByLabelText("临时停用 first");
  await user.click(screen.getByLabelText("上移 second"));
  await user.click(screen.getByLabelText("临时停用 first"));
  expect(mutations).toHaveLength(0);
  for (const name of [
    "状态",
    "当前并发",
    "成功率",
    "尝试数",
    "Token / 缓存率",
    "估算消费",
    "实际消费",
    "余额 / 额度",
  ])
    expect(
      screen.getByRole("columnheader", {
        name: new RegExp(name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")),
      }),
    ).toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("控制时间范围"), "today");
  await waitFor(() =>
    expect(
      queries.some(
        (u) =>
          u.pathname.endsWith("analytics") &&
          u.searchParams.get("range") === "today" &&
          u.searchParams.get("source_id") === "do",
      ),
    ).toBe(true),
  );
  await screen.findAllByText("28");
  expect(screen.getByLabelText("临时停用 first")).toBeChecked();
  expect(
    screen.getByRole("checkbox", { name: "自定义优先顺序" }),
  ).toBeChecked();
  await screen.findAllByText("$9.25");
  expect(
    queries.some(
      (u) =>
        u.pathname.endsWith("channel-balances") &&
        u.searchParams.get("model") === "m" &&
        u.searchParams.has("start_date"),
    ),
  ).toBe(true);
  expect(mutations).toHaveLength(0);
  await user.click(screen.getByRole("button", { name: "应用临时修改" }));
  await waitFor(() => expect(mutations).toHaveLength(1));
  expect(mutations[0]).toEqual({
    revision: "boot:0",
    action: "set",
    api_key_id: "key-one",
    model: "m",
    order: ["second", "first"],
    disabled: ["first"],
  });
  await screen.findByRole("button", { name: "恢复此规则" });
  await user.click(screen.getByRole("button", { name: "恢复此规则" }));
  await waitFor(() => expect(state.rules).toHaveLength(0));
  expect(mutations[1].action).toBe("reset");
  expect(mutations[1].revision).toBe("boot:1");
  expect(screen.getByText(/无到期时间/)).toBeInTheDocument();
  app.unmount();
  client.clear();
});
it("global disables cannot be lifted by a narrower rule or leak to other scopes", () => {
  const rules = [
    { api_key_id: "", model: "", order: [], disabled: ["global"] },
    { api_key_id: "k", model: "m", order: [], disabled: ["specific"] },
  ];
  expect(inheritedDisabled(rules, "k", "m", "global")).toBe(true);
  expect(inheritedDisabled(rules, "other", "other", "specific")).toBe(false);
  expect(inheritedDisabled(rules, "k", "m", "specific")).toBe(false);
});

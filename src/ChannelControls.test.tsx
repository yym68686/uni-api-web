import { afterEach, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { MotionConfig, LazyMotion, domAnimation } from "motion/react";
import App from "./App";
import { emptyStats } from "./analytics";
import { inheritedDisabled, RESET_SCOPE_LABEL } from "./ChannelControls";
import type { ControlState } from "./ChannelControls";

afterEach(() => vi.unstubAllGlobals());
function setup(controlUnavailable = false) {
  const states: Record<string, ControlState> = {
    one: { revision: "one:0", instance_id: "one", rules: [] },
    two: { revision: "two:0", instance_id: "two", rules: [] },
  };
  const writes: { source: string; body: any }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const u = new URL(input, location.origin),
        source =
          u.pathname.match(/\/sources\/([^/]+)\//)?.[1] ||
          u.searchParams.get("source_id") ||
          "all";
      const keySource = u.searchParams.get("api_key_id")?.split("::")[0];
      const ids =
        source !== "all" ? [source] : keySource ? [keySource] : ["one", "two"];
      const rows = ids.flatMap((id) =>
        ["visible-first", "hidden", "visible-second"].map((provider) => ({
          source_id: id,
          source_name: id === "one" ? "One" : "Two",
          provider,
          model: "m",
          upstream_model: "m",
          endpoint: u.searchParams.get("endpoint") || "all",
          stream: u.searchParams.get("stream") === "true" ? true : null,
          eligible: true,
          reason: "eligible",
          stats: {
            ...emptyStats(),
            success: 7,
            success_rate_denominator: 7,
            success_rate: 1,
            inflight: 2,
            usage_samples: 7,
            input_tokens: 1000,
            output_tokens: 100,
            estimated_cost_usd: 2.5,
          },
        })),
      );
      let body: any;
      if (u.pathname.endsWith("/auth/me"))
        body = { enabled: true, authenticated: true, username: "admin" };
      else if (u.pathname.endsWith("/sources"))
        body = {
          data: [
            {
              id: "one",
              name: "One",
              base: "https://one.test",
              has_storage: true,
            },
            {
              id: "two",
              name: "Two",
              base: "https://two.test",
              has_storage: true,
            },
          ],
        };
      else if (u.pathname.endsWith("channel-controls")) {
        if (controlUnavailable)
          return new Response("控制接口暂不可用", { status: 503 });
        if (init?.method === "POST") {
          const v = JSON.parse(init.body as string);
          writes.push({ source, body: v });
          states[source] = {
            ...states[source],
            revision: source + ":" + writes.length,
            rules: [
              ...states[source].rules.filter(
                (rule) =>
                  rule.api_key_id !== v.api_key_id || rule.model !== v.model,
              ),
              ...(v.action === "reset"
                ? []
                : [
                    {
                      api_key_id: v.api_key_id,
                      model: v.model,
                      order: v.order,
                      disabled: v.disabled,
                    },
                  ]),
            ],
          };
        }
        body = states[source];
      } else if (u.pathname.endsWith("api-keys"))
        body = {
          can_inspect_all: true,
          data: ids.map((id) => ({
            key_id: id + "::key",
            source_id: id,
            position: 1,
            prefix: "masked",
          })),
        };
      else if (u.pathname.endsWith("channel-balances"))
        body = {
          provider: u.searchParams.get("provider"),
          status: "unsupported",
        };
      else if (u.pathname.endsWith("channel-checks")) body = { data: [] };
      else
        body = {
          data: rows,
          total: { requests: rows.length * 7 },
          snapshot_revision: "config",
          generated_at: Date.now() / 1000,
          coverage: "full",
        };
      return new Response(JSON.stringify(body));
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const user = userEvent.setup();
  const app = render(
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
  return { ...app, user, writes, states, client };
}
it("switches immediately from observation to controls with the same table, filters and metrics, including all sources", async () => {
  const app = setup();
  const table = await screen.findByRole("table");
  const source = screen.getByLabelText("uni-api 来源"),
    range = screen.getByLabelText("时间范围筛选");
  expect(source).toHaveValue("");
  expect(within(table).getAllByRole("row")).toHaveLength(7);
  const metricsBefore = within(table)
    .getAllByRole("row")
    .slice(1)
    .map((r) =>
      within(r)
        .getAllByRole("cell")
        .slice(0, 12)
        .map((c) => c.textContent),
    );
  await app.user.click(screen.getByRole("button", { name: "渠道控制" }));
  expect(screen.getByRole("table")).toBe(table);
  expect(screen.getByLabelText("uni-api 来源")).toBe(source);
  expect(source).toHaveValue("");
  expect(screen.getByLabelText("时间范围筛选")).toBe(range);
  expect(screen.queryByLabelText("控制来源")).not.toBeInTheDocument();
  expect(
    screen.queryByText("请选择来源后查看和编辑临时规则。"),
  ).not.toBeInTheDocument();
  expect(
    within(table)
      .getAllByRole("row")
      .slice(1)
      .map((r) =>
        within(r)
          .getAllByRole("cell")
          .slice(0, 12)
          .map((c) => c.textContent),
      ),
  ).toEqual(metricsBefore);
  await screen.findByLabelText("临时停用 One visible-first m");
  await screen.findByLabelText("临时停用 Two visible-first m");
  await app.user.selectOptions(source, "one");
  await app.user.selectOptions(
    screen.getByLabelText("API key 筛选"),
    "one::key",
  );
  await app.user.selectOptions(screen.getByLabelText("模型筛选"), "m");
  await app.user.selectOptions(range, "today");
  await app.user.type(screen.getByLabelText("搜索渠道或模型"), "visible");
  await app.user.selectOptions(
    screen.getByLabelText("端点筛选"),
    "/v1/responses",
  );
  await app.user.selectOptions(screen.getByLabelText("流式状态筛选"), "true");
  await app.user.selectOptions(
    screen.getByLabelText("渠道状态筛选"),
    "eligible",
  );
  await app.user.selectOptions(screen.getByLabelText("排序"), "success");
  const labels = [
    "uni-api 来源",
    "API key 筛选",
    "模型筛选",
    "时间范围筛选",
    "搜索渠道或模型",
    "端点筛选",
    "流式状态筛选",
    "渠道状态筛选",
    "排序",
  ];
  const values = labels.map(
    (label) => (screen.getByLabelText(label) as HTMLInputElement).value,
  );
  for (const view of ["渠道检测", "渠道观测", "渠道控制"]) {
    await app.user.click(
      screen.getByRole("button", { name: new RegExp("^" + view) }),
    );
    expect(
      labels.map(
        (label) => (screen.getByLabelText(label) as HTMLInputElement).value,
      ),
    ).toEqual(values);
  }
  expect(app.writes).toHaveLength(0);
  app.unmount();
  app.client.clear();
});
it("stages source-specific controls inline, retains hidden channels and drafts across shared filters and views", async () => {
  const app = setup();
  await screen.findByRole("table");
  await app.user.click(screen.getByRole("button", { name: "渠道控制" }));
  await screen.findByLabelText("临时停用 One visible-first m");
  await app.user.type(screen.getByLabelText("搜索渠道或模型"), "visible");
  await app.user.click(screen.getByLabelText("上移 One visible-second m"));
  await app.user.click(screen.getByLabelText("临时停用 One visible-first m"));
  await app.user.selectOptions(screen.getByLabelText("时间范围筛选"), "today");
  await app.user.click(screen.getByRole("button", { name: /^渠道观测/ }));
  await app.user.click(screen.getByRole("button", { name: "渠道控制" }));
  expect(screen.getByLabelText("临时停用 One visible-first m")).toBeChecked();
  expect(
    screen.getByLabelText("临时停用 Two visible-first m"),
  ).not.toBeChecked();
  expect(app.writes).toHaveLength(0);
  await app.user.click(
    screen.getByLabelText("应用临时修改 One visible-first m"),
  );
  await waitFor(() => expect(app.writes).toHaveLength(1));
  expect(app.writes[0]).toEqual({
    source: "one",
    body: {
      revision: "one:0",
      action: "set",
      api_key_id: "",
      model: "",
      order: ["visible-second", "hidden", "visible-first"],
      disabled: ["visible-first"],
    },
  });
  const reset = await screen.findByRole("button", { name: RESET_SCOPE_LABEL });
  expect(
    within(screen.getByRole("table")).queryByRole("button", {
      name: RESET_SCOPE_LABEL,
    }),
  ).not.toBeInTheDocument();
  const scope = screen.getByRole("article", {
    name: "One / 全部 API key / 全部模型",
  });
  expect(
    within(scope).getByText("已调整 3 个渠道的顺序 · 临时停用 1 个渠道"),
  ).toBeInTheDocument();
  await app.user.click(reset);
  await waitFor(() => expect(app.writes).toHaveLength(2));
  expect(app.states.one.rules).toHaveLength(0);
  expect(app.states.two.rules).toHaveLength(0);
  app.unmount();
  app.client.clear();
});
it("combines inherited disables without leaking narrower rules", () => {
  const rules = [
    { api_key_id: "", model: "", order: [], disabled: ["global"] },
    { api_key_id: "k", model: "m", order: [], disabled: ["specific"] },
  ];
  expect(inheritedDisabled(rules, "k", "m", "global")).toBe(true);
  expect(inheritedDisabled(rules, "other", "other", "specific")).toBe(false);
  expect(inheritedDisabled(rules, "k", "m", "specific")).toBe(false);
});

it("keeps the observation table visible when a source control endpoint is unavailable", async () => {
  const app = setup(true);
  const table = await screen.findByRole("table");
  await app.user.click(screen.getByRole("button", { name: "渠道控制" }));
  await screen.findAllByText("控制接口暂不可用");
  expect(screen.getByRole("table")).toBe(table);
  expect(within(table).getAllByRole("row")).toHaveLength(7);
  expect(screen.getByLabelText("uni-api 来源")).toHaveValue("");
  app.unmount();
  app.client.clear();
});

it("shows one reset per exact source/key/model scope above the table and retains hidden scope access", async () => {
  const app = setup();
  app.states.one.rules = [
    {
      api_key_id: "",
      model: "",
      order: ["visible-second", "hidden", "visible-first"],
      disabled: ["hidden"],
    },
    { api_key_id: "key", model: "m", order: [], disabled: ["visible-first"] },
  ];
  app.states.two.rules = [
    { api_key_id: "", model: "", order: [], disabled: ["visible-second"] },
  ];
  await screen.findByRole("table");
  await app.user.click(screen.getByRole("button", { name: "渠道控制" }));
  await waitFor(() =>
    expect(
      screen.getAllByRole("button", { name: RESET_SCOPE_LABEL }),
    ).toHaveLength(2),
  );
  const one = screen.getByRole("article", {
      name: "One / 全部 API key / 全部模型",
    }),
    two = screen.getByRole("article", {
      name: "Two / 全部 API key / 全部模型",
    });
  expect(within(one).getByText(/已调整 3 个渠道/)).toBeInTheDocument();
  expect(within(two).getByText(/临时停用 1 个渠道/)).toBeInTheDocument();
  expect(
    within(screen.getByRole("table")).queryByRole("button", {
      name: RESET_SCOPE_LABEL,
    }),
  ).not.toBeInTheDocument();
  await app.user.type(
    screen.getByLabelText("搜索渠道或模型"),
    "no-matching-channel",
  );
  await screen.findByText("没有匹配的渠道");
  expect(
    screen.getAllByRole("button", { name: RESET_SCOPE_LABEL }),
  ).toHaveLength(2);
  await app.user.click(
    within(two).getByRole("button", { name: RESET_SCOPE_LABEL }),
  );
  await waitFor(() => expect(app.writes).toHaveLength(1));
  expect(app.writes[0].source).toBe("two");
  expect(app.states.one.rules).toHaveLength(2);
  await app.user.clear(screen.getByLabelText("搜索渠道或模型"));
  await app.user.selectOptions(screen.getByLabelText("uni-api 来源"), "one");
  await app.user.selectOptions(
    screen.getByLabelText("API key 筛选"),
    "one::key",
  );
  await app.user.selectOptions(screen.getByLabelText("模型筛选"), "m");
  const scoped = await screen.findByRole("article", {
    name: "One / Key 1 · masked / m",
  });
  expect(within(scoped).getByText("模型：m")).toBeInTheDocument();
  expect(
    screen.getAllByRole("button", { name: RESET_SCOPE_LABEL }),
  ).toHaveLength(1);
  await app.user.click(
    within(scoped).getByRole("button", { name: RESET_SCOPE_LABEL }),
  );
  await waitFor(() => expect(app.writes).toHaveLength(2));
  expect(app.writes[1].body.api_key_id).toBe("key");
  expect(app.writes[1].body.model).toBe("m");
  expect(app.states.one.rules).toHaveLength(1);
  expect(app.states.one.rules[0].api_key_id).toBe("");
  app.unmount();
  app.client.clear();
});

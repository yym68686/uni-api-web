import { afterEach, expect, it, vi } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { MotionConfig, LazyMotion, domAnimation } from "motion/react";
import App from "./App";
import { emptyStats } from "./analytics";
import { inheritedDisabled, RESET_SCOPE_LABEL } from "./ChannelControls";
import type { ControlState } from "./ChannelControls";
import type { ChannelCheck } from "./ChannelChecks";

afterEach(() => vi.unstubAllGlobals());
interface CheckFixture {
  data: ChannelCheck[];
  readError?: string;
  run?: (
    source: string,
    provider: string,
    signal: AbortSignal,
  ) => Promise<ChannelCheck>;
}
function setup(
  controlUnavailable = false,
  beforeWrite?: (
    source: string,
    body: any,
  ) => Promise<Response | undefined> | Response | undefined,
  checkFixture: CheckFixture = { data: [] },
  catalogFixture: { providers?: string[]; models?: string[] } = {},
) {
  const states: Record<string, ControlState> = {
    one: { revision: "one:0", instance_id: "one", rules: [] },
    two: { revision: "two:0", instance_id: "two", rules: [] },
  };
  const writes: { source: string; body: any }[] = [];
  const checkWrites: { source: string; provider: string }[] = [];
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
        (
          catalogFixture.providers || [
            "visible-first",
            "hidden",
            "visible-second",
          ]
        ).flatMap((provider) =>
          (catalogFixture.models || ["m"]).map((model) => ({
            source_id: id,
            source_name: id === "one" ? "One" : "Two",
            provider,
            model,
            upstream_model: model,
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
        ),
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
          const failure = await beforeWrite?.(source, v);
          if (failure) return failure;
          if (v.revision !== states[source].revision)
            return new Response("规则版本冲突，请刷新核对", { status: 409 });
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
      else if (u.pathname.endsWith("channel-checks")) {
        if (init?.method === "POST") {
          const { provider } = JSON.parse(init.body as string);
          checkWrites.push({ source, provider });
          const result = checkFixture.run
            ? await checkFixture.run(source, provider, init.signal!)
            : {
                source_id: source,
                provider,
                model: "gpt-6-astra",
                verdict: "pass" as const,
                text: "未知",
                checked_at: Date.now() / 1000,
                duration_ms: 1234,
              };
          init.signal?.throwIfAborted();
          checkFixture.data = [
            ...checkFixture.data.filter(
              (item) => item.source_id !== source || item.provider !== provider,
            ),
            result,
          ];
          body = result;
        } else {
          if (checkFixture.readError)
            return new Response(checkFixture.readError, { status: 503 });
          body = { data: checkFixture.data };
        }
      } else
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
  return { ...app, user, writes, states, client, checkWrites };
}
it("toggles adjustment inside observation with the same table, filters and metrics, including all sources", async () => {
  const app = setup();
  const table = await screen.findByRole("table");
  const source = screen.getByLabelText("uni-api 来源"),
    range = screen.getByLabelText("时间范围筛选");
  expect(source).toHaveValue("");
  expect(within(table).getAllByRole("row")).toHaveLength(7);
  expect(
    screen.queryByRole("button", { name: "渠道控制" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("columnheader", { name: "临时控制" }),
  ).not.toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "调整顺序" }).closest(".data-actions"),
  ).toBe(screen.getByLabelText("搜索渠道或模型").closest(".data-actions"));
  const metricsBefore = within(table)
    .getAllByRole("row")
    .slice(1)
    .map((r) =>
      within(r)
        .getAllByRole("cell")
        .slice(0, 12)
        .map((c) => c.textContent),
    );
  await app.user.click(screen.getByRole("button", { name: "调整顺序" }));
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
  await app.user.click(screen.getByRole("button", { name: /^余额管理/ }));
  expect(screen.getByLabelText("uni-api 来源")).toHaveValue("one");
  expect(screen.getByLabelText("API key 筛选")).toHaveValue("one::key");
  expect(screen.getByLabelText("模型筛选")).toHaveValue("m");
  expect(screen.getByLabelText("搜索渠道或模型")).toHaveValue("visible");
  expect(screen.getByLabelText("模型优先级筛选")).toHaveValue("");
  expect(screen.getByLabelText("余额低于美元")).toHaveValue(null);
  for (const label of [
    "时间范围筛选",
    "端点筛选",
    "流式状态筛选",
    "渠道状态筛选",
    "排序",
  ]) {
    expect(screen.queryByLabelText(label)).not.toBeInTheDocument();
  }
  await app.user.click(screen.getByRole("button", { name: /^渠道观测/ }));
  expect(
    labels.map(
      (label) => (screen.getByLabelText(label) as HTMLInputElement).value,
    ),
  ).toEqual(values);
  expect(app.writes).toHaveLength(0);
  const tableBeforeCancel = screen.getByRole("table");
  await app.user.click(screen.getByRole("button", { name: "取消调整" }));
  expect(
    screen.queryByRole("columnheader", { name: "临时控制" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("table")).toBe(tableBeforeCancel);
  expect(
    labels.map(
      (label) => (screen.getByLabelText(label) as HTMLInputElement).value,
    ),
  ).toEqual(values);
  app.unmount();
  app.client.clear();
});
it("stages source-specific controls inline, retains hidden channels and drafts across shared filters and views", async () => {
  const app = setup();
  await screen.findByRole("table");
  await app.user.click(screen.getByRole("button", { name: "调整顺序" }));
  await screen.findByLabelText("临时停用 One visible-first m");
  await app.user.type(screen.getByLabelText("搜索渠道或模型"), "visible");
  await app.user.click(screen.getByLabelText("上移 One visible-second m"));
  await app.user.click(screen.getByLabelText("临时停用 One visible-first m"));
  await app.user.selectOptions(screen.getByLabelText("时间范围筛选"), "today");
  await app.user.click(screen.getByRole("button", { name: "余额管理" }));
  await app.user.click(screen.getByRole("button", { name: /^渠道观测/ }));
  expect(screen.getByLabelText("临时停用 One visible-first m")).toBeChecked();
  expect(
    screen.getByLabelText("临时停用 Two visible-first m"),
  ).not.toBeChecked();
  expect(app.writes).toHaveLength(0);
  await app.user.click(screen.getByLabelText("应用全部临时修改"));
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
  expect(reset).toHaveAttribute(
    "data-reset-scope",
    "One / 全部 API key / 全部模型",
  );
  expect(reset.closest(".data-actions")).toBe(
    screen.getByLabelText("搜索渠道或模型").closest(".data-actions"),
  );
  expect(
    screen.queryByRole("region", { name: "当前范围的临时修改" }),
  ).not.toBeInTheDocument();
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
  await app.user.click(screen.getByRole("button", { name: "调整顺序" }));
  await screen.findAllByText("控制接口暂不可用");
  expect(screen.getByRole("table")).toBe(table);
  expect(within(table).getAllByRole("row")).toHaveLength(7);
  expect(screen.getByLabelText("uni-api 来源")).toHaveValue("");
  app.unmount();
  app.client.clear();
});

it("keeps a single reset beside search with scope details in the menu and tooltip only", async () => {
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
  await app.user.click(screen.getByRole("button", { name: "调整顺序" }));
  const reset = await screen.findByRole("button", { name: RESET_SCOPE_LABEL });
  expect(
    screen.getAllByRole("button", { name: RESET_SCOPE_LABEL }),
  ).toHaveLength(1);
  expect(reset.closest(".data-actions")).toBe(
    screen.getByLabelText("搜索渠道或模型").closest(".data-actions"),
  );
  expect(
    screen.queryByRole("region", { name: "当前范围的临时修改" }),
  ).not.toBeInTheDocument();
  expect(
    within(screen.getByRole("table")).queryByRole("button", {
      name: RESET_SCOPE_LABEL,
    }),
  ).not.toBeInTheDocument();
  await app.user.click(reset);
  expect(reset.closest("details")).toHaveAttribute("open");
  expect(
    screen.getByRole("menuitem", { name: "One / 全部 API key / 全部模型" }),
  ).toBeInTheDocument();
  await app.user.type(
    screen.getByLabelText("搜索渠道或模型"),
    "no-matching-channel",
  );
  await screen.findByText("没有匹配的渠道");
  expect(reset.closest("details")).not.toHaveAttribute("open");
  await app.user.click(reset);
  await app.user.click(
    screen.getByRole("menuitem", { name: "Two / 全部 API key / 全部模型" }),
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
  const scoped = await screen.findByRole("button", { name: RESET_SCOPE_LABEL });
  expect(scoped).toHaveAttribute(
    "data-reset-scope",
    "One / Key 1 · masked / m",
  );
  await app.user.click(scoped);
  await waitFor(() => expect(app.writes).toHaveLength(2));
  expect(app.writes[1].body.api_key_id).toBe("key");
  expect(app.writes[1].body.model).toBe("m");
  expect(app.states.one.rules).toHaveLength(1);
  expect(app.states.one.rules[0].api_key_id).toBe("");
  app.unmount();
  app.client.clear();
});

function providerOrder(source: string) {
  return within(screen.getByRole("table"))
    .getAllByRole("row")
    .slice(1)
    .filter((row) => row.querySelector(".source-label")?.textContent === source)
    .map((row) => row.querySelector(".channel-link strong")?.textContent);
}
async function openControls(app: ReturnType<typeof setup>) {
  await screen.findByRole("table");
  await app.user.click(screen.getByRole("button", { name: "调整顺序" }));
  await waitFor(() =>
    expect(screen.getByLabelText("临时停用 One visible-first m")).toBeEnabled(),
  );
  await screen.findByLabelText("临时停用 Two visible-first m");
}

it("applies moves from both sources with one toolbar action and keeps acknowledged ordering when catalog data lags", async () => {
  const app = setup();
  await openControls(app);
  await app.user.type(screen.getByLabelText("搜索渠道或模型"), "visible");
  await app.user.click(screen.getByLabelText("上移 One visible-second m"));
  await app.user.click(screen.getByLabelText("上移 Two visible-second m"));
  const apply = screen.getByRole("button", { name: "应用全部临时修改" });
  expect(
    screen.getAllByRole("button", { name: "应用全部临时修改" }),
  ).toHaveLength(1);
  expect(
    screen.getAllByRole("button", { name: "放弃全部临时修改" }),
  ).toHaveLength(1);
  expect(
    within(screen.getByRole("table")).queryByRole("button", {
      name: /应用|放弃/,
    }),
  ).not.toBeInTheDocument();
  expect(app.writes).toHaveLength(0);
  await app.user.click(apply);
  await waitFor(() => expect(app.writes).toHaveLength(2));
  await waitFor(() =>
    expect(screen.queryByLabelText("应用全部临时修改")).not.toBeInTheDocument(),
  );
  expect(app.writes.map((write) => write.source).sort()).toEqual([
    "one",
    "two",
  ]);
  for (const source of ["one", "two"])
    expect(app.states[source].rules[0].order).toEqual([
      "visible-second",
      "hidden",
      "visible-first",
    ]);
  expect(providerOrder("One")).toEqual(["visible-second", "visible-first"]);
  expect(providerOrder("Two")).toEqual(["visible-second", "visible-first"]);
  await app.user.click(screen.getByLabelText("临时停用 One visible-first m"));
  const reset = screen.getByRole("button", { name: RESET_SCOPE_LABEL });
  expect(
    screen
      .getByLabelText("应用全部临时修改")
      .closest(".control-toolbar-actions"),
  ).toBe(reset.closest(".control-toolbar-actions"));
  expect(
    screen
      .getByLabelText("放弃全部临时修改")
      .closest(".control-toolbar-actions"),
  ).toBe(reset.closest(".control-toolbar-actions"));
  app.unmount();
  app.client.clear();
});

it("discards all drafts even when search hides every modified channel, preserving applied rules", async () => {
  const app = setup();
  app.states.one.rules = [
    {
      api_key_id: "",
      model: "",
      order: ["visible-second", "hidden", "visible-first"],
      disabled: [],
    },
  ];
  await openControls(app);
  await app.user.click(screen.getByLabelText("临时停用 One visible-first m"));
  await app.user.click(screen.getByLabelText("上移 Two visible-second m"));
  await app.user.type(screen.getByLabelText("搜索渠道或模型"), "no-match");
  await screen.findByText("没有匹配的渠道");
  await app.user.click(screen.getByLabelText("放弃全部临时修改"));
  expect(app.writes).toHaveLength(0);
  await app.user.clear(screen.getByLabelText("搜索渠道或模型"));
  expect(
    screen.getByLabelText("临时停用 One visible-first m"),
  ).not.toBeChecked();
  expect(providerOrder("One")).toEqual([
    "visible-second",
    "hidden",
    "visible-first",
  ]);
  expect(providerOrder("Two")).toEqual([
    "visible-first",
    "hidden",
    "visible-second",
  ]);
  expect(screen.queryByLabelText("应用全部临时修改")).not.toBeInTheDocument();
  app.unmount();
  app.client.clear();
});

it("applies drafts hidden by source and model selection and chains revisions for multiple scopes in one source", async () => {
  const app = setup();
  await openControls(app);
  await app.user.click(screen.getByLabelText("下移 One visible-first m"));
  await app.user.click(screen.getByLabelText("临时停用 Two visible-first m"));
  await app.user.selectOptions(screen.getByLabelText("uni-api 来源"), "one");
  await app.user.selectOptions(
    screen.getByLabelText("API key 筛选"),
    "one::key",
  );
  await app.user.selectOptions(screen.getByLabelText("模型筛选"), "m");
  await waitFor(() =>
    expect(screen.getByLabelText("临时停用 One visible-first m")).toBeEnabled(),
  );
  await app.user.click(screen.getByLabelText("临时停用 One visible-first m"));
  await app.user.click(screen.getByLabelText("应用全部临时修改"));
  await waitFor(() =>
    expect(screen.queryByLabelText("应用全部临时修改")).not.toBeInTheDocument(),
  );
  expect(app.writes).toHaveLength(3);
  const one = app.writes.filter((write) => write.source === "one");
  expect(one[0].body).toMatchObject({
    revision: "one:0",
    api_key_id: "",
    model: "",
  });
  expect(one[1].body.revision).not.toBe("one:0");
  expect(one[1].body).toMatchObject({
    api_key_id: "key",
    model: "m",
    disabled: ["visible-first"],
  });
  expect(app.states.one.rules).toHaveLength(2);
  expect(app.states.two.rules[0].disabled).toEqual(["visible-first"]);
  app.unmount();
  app.client.clear();
});

it("locks all draft actions during a batch and retains only failed changes for an explicit retry", async () => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  let fail = true;
  const app = setup(false, async (source) => {
    await gate;
    if (source === "two" && fail)
      return new Response("来源暂不可用", { status: 503 });
  });
  await openControls(app);
  await app.user.click(screen.getByLabelText("临时停用 One visible-first m"));
  await app.user.click(screen.getByLabelText("临时停用 Two visible-first m"));
  const apply = screen.getByLabelText("应用全部临时修改");
  await app.user.dblClick(apply);
  await waitFor(() => expect(app.writes).toHaveLength(2));
  expect(apply).toBeDisabled();
  expect(screen.getByLabelText("放弃全部临时修改")).toBeDisabled();
  expect(screen.getByRole("button", { name: "取消调整" })).toBeDisabled();
  expect(screen.getByLabelText("临时停用 One visible-second m")).toBeDisabled();
  expect(screen.getByLabelText("上移 Two visible-second m")).toBeDisabled();
  await act(async () => {
    release();
  });
  await waitFor(() => expect(apply).toBeEnabled());
  expect(app.states.one.rules).toHaveLength(1);
  expect(app.states.two.rules).toHaveLength(0);
  expect(screen.getByText(/未应用的修改已保留：Two/)).toHaveTextContent(
    "来源暂不可用",
  );
  fail = false;
  await app.user.click(apply);
  await waitFor(() =>
    expect(screen.queryByLabelText("应用全部临时修改")).not.toBeInTheDocument(),
  );
  expect(app.writes.map((write) => write.source)).toEqual([
    "one",
    "two",
    "two",
  ]);
  expect(app.states.two.rules[0].disabled).toEqual(["visible-first"]);
  app.unmount();
  app.client.clear();
});

it("does not overwrite an external revision when applying other valid drafts", async () => {
  const app = setup();
  await openControls(app);
  await app.user.click(screen.getByLabelText("临时停用 One visible-first m"));
  await app.user.click(screen.getByLabelText("临时停用 Two visible-first m"));
  app.states.one = {
    ...app.states.one,
    revision: "one:external",
    rules: [{ api_key_id: "", model: "", order: [], disabled: ["hidden"] }],
  };
  await act(async () => {
    await app.client.invalidateQueries({ queryKey: ["channel-controls"] });
  });
  await app.user.click(screen.getByLabelText("应用全部临时修改"));
  await waitFor(() => expect(app.writes).toHaveLength(1));
  await waitFor(() =>
    expect(screen.getByLabelText("应用全部临时修改")).toBeEnabled(),
  );
  expect(app.writes[0].source).toBe("two");
  expect(app.states.one.rules[0].disabled).toEqual(["hidden"]);
  expect(screen.getByText(/未应用的修改已保留：One/)).toHaveTextContent(
    "规则已变化",
  );
  await app.user.click(screen.getByLabelText("放弃全部临时修改"));
  expect(screen.getByLabelText("临时停用 One hidden m")).toBeChecked();
  expect(
    screen.getByLabelText("临时停用 One visible-first m"),
  ).not.toBeChecked();
  app.unmount();
  app.client.clear();
});

it("cancels adjustment and clears drafts from hidden sources and scopes while preserving applied rules", async () => {
  const app = setup();
  await openControls(app);
  await app.user.click(screen.getByLabelText("上移 One visible-second m"));
  await app.user.click(screen.getByLabelText("应用全部临时修改"));
  await waitFor(() =>
    expect(screen.queryByLabelText("应用全部临时修改")).not.toBeInTheDocument(),
  );
  const appliedOrder = providerOrder("One");
  await app.user.click(screen.getByLabelText("临时停用 One visible-first m"));
  await app.user.click(screen.getByLabelText("临时停用 Two visible-first m"));
  await app.user.selectOptions(screen.getByLabelText("uni-api 来源"), "one");
  await app.user.selectOptions(screen.getByLabelText("模型筛选"), "m");
  await app.user.click(screen.getByLabelText("临时停用 One visible-first m"));
  await app.user.type(screen.getByLabelText("搜索渠道或模型"), "no-match");
  await screen.findByText("没有匹配的渠道");
  await app.user.click(screen.getByRole("button", { name: "取消调整" }));
  expect(app.writes).toHaveLength(1);
  expect(screen.queryByLabelText("应用全部临时修改")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: RESET_SCOPE_LABEL }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "调整顺序" })).toHaveAttribute(
    "aria-pressed",
    "false",
  );
  await app.user.clear(screen.getByLabelText("搜索渠道或模型"));
  await app.user.selectOptions(screen.getByLabelText("模型筛选"), "");
  await app.user.selectOptions(screen.getByLabelText("uni-api 来源"), "");
  expect(providerOrder("One")).toEqual(appliedOrder);
  expect(
    screen.queryByRole("columnheader", { name: "临时控制" }),
  ).not.toBeInTheDocument();
  await app.user.click(screen.getByRole("button", { name: "调整顺序" }));
  expect(
    screen.getByLabelText("临时停用 One visible-first m"),
  ).not.toBeChecked();
  expect(
    screen.getByLabelText("临时停用 Two visible-first m"),
  ).not.toBeChecked();
  await app.user.selectOptions(screen.getByLabelText("模型筛选"), "m");
  expect(
    screen.getByLabelText("临时停用 One visible-first m"),
  ).not.toBeChecked();
  expect(screen.queryByLabelText("应用全部临时修改")).not.toBeInTheDocument();
  app.unmount();
  app.client.clear();
});

function storedCheck(
  source: string,
  provider: string,
  verdict: ChannelCheck["verdict"],
  checked_at = 1800000000,
): ChannelCheck {
  return {
    source_id: source,
    provider,
    model: "gpt-6-astra",
    verdict,
    checked_at,
    duration_ms: 1234,
    text: verdict === "pass" ? "未知" : verdict === "fail" ? "2024-06" : "",
    message: verdict === "error" ? "渠道不可用" : undefined,
  };
}
function observedCheck(source: string, provider: string) {
  const row = within(screen.getByRole("table"))
    .getAllByRole("row")
    .find(
      (row) =>
        row.querySelector(".channel-link strong")?.textContent === provider &&
        row.querySelector(".source-label")?.textContent === source,
    )!;
  return row.querySelector(".latest-channel-check")!;
}

it("shows persisted latest check verdicts by source and provider, independent of metric filters", async () => {
  const fixture = {
    data: [
      storedCheck("one", "visible-first", "pass"),
      storedCheck("one", "hidden", "fail"),
      storedCheck("one", "visible-second", "inconclusive"),
      storedCheck("two", "visible-first", "error"),
    ],
  };
  const app = setup(false, undefined, fixture);
  await screen.findByRole("columnheader", { name: "是否降智" });
  expect(observedCheck("One", "visible-first")).toHaveTextContent("不降智");
  expect(observedCheck("One", "hidden")).toHaveTextContent("降智");
  expect(observedCheck("One", "visible-second")).toHaveTextContent("无法判定");
  expect(observedCheck("Two", "visible-first")).toHaveTextContent("检测失败");
  expect(observedCheck("Two", "hidden")).toHaveTextContent("未检测");
  expect(observedCheck("One", "visible-first")).not.toHaveTextContent("16:00:00");
  await app.user.selectOptions(
    screen.getByLabelText("API key 筛选"),
    "one::key",
  );
  await app.user.selectOptions(screen.getByLabelText("模型筛选"), "m");
  await app.user.selectOptions(screen.getByLabelText("时间范围筛选"), "5m");
  expect(observedCheck("One", "visible-first")).toHaveTextContent("不降智");
  expect(observedCheck("One", "hidden")).toHaveTextContent("降智");
  expect(app.checkWrites).toHaveLength(0);
  expect(app.writes).toHaveLength(0);
  app.unmount();
  app.client.clear();
});

it("keeps the last check while retesting, updates observation immediately, and restores the new result after remount", async () => {
  let finish!: (result: ChannelCheck) => void;
  const fixture: CheckFixture = {
    data: [storedCheck("one", "visible-first", "pass")],
    run: () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  };
  let app = setup(false, undefined, fixture);
  await screen.findByRole("table");
  await app.user.click(screen.getByRole("button", { name: "降智检测" }));
  await app.user.click(screen.getByLabelText("重新检测 One visible-first m"));
  await waitFor(() => expect(app.checkWrites).toHaveLength(1));
  await app.user.click(screen.getByRole("button", { name: /^渠道观测/ }));
  expect(observedCheck("One", "visible-first")).toHaveTextContent("不降智");
  expect(observedCheck("One", "visible-first")).toHaveTextContent(
    "检测中 · 上次结果",
  );
  await act(async () =>
    finish(storedCheck("one", "visible-first", "fail", 1800000100)),
  );
  await waitFor(() =>
    expect(observedCheck("One", "visible-first")).not.toHaveTextContent(
      "不降智",
    ),
  );
  expect(observedCheck("One", "visible-first")).toHaveTextContent("降智");
  expect(observedCheck("One", "visible-first")).not.toHaveTextContent("16:01:40");
  expect(observedCheck("One", "visible-first")).not.toHaveTextContent("检测中");
  expect(observedCheck("Two", "visible-first")).toHaveTextContent("未检测");
  app.unmount();
  app.client.clear();
  app = setup(false, undefined, fixture);
  await screen.findByRole("table");
  expect(observedCheck("One", "visible-first")).toHaveTextContent("降智");
  expect(observedCheck("One", "visible-first")).not.toHaveTextContent("不降智");
  expect(app.checkWrites).toHaveLength(0);
  app.unmount();
  app.client.clear();
});

it("distinguishes unavailable check history from untested channels and keeps metrics usable", async () => {
  const app = setup(false, undefined, {
    data: [],
    readError: "检测记录暂不可用",
  });
  const table = await screen.findByRole("table");
  await screen.findByText("最近检测结果读取失败：检测记录暂不可用");
  expect(observedCheck("One", "visible-first")).toHaveTextContent("读取失败");
  expect(within(table).queryByText("未检测")).not.toBeInTheDocument();
  expect(within(table).getAllByRole("row")).toHaveLength(7);
  await app.user.click(screen.getByRole("button", { name: "调整顺序" }));
  expect(screen.getByLabelText("临时停用 One visible-first m")).toBeEnabled();
  app.unmount();
  app.client.clear();
});

it("opens detection controls inside observation without sending probes or changing filters, metrics and adjustment drafts", async () => {
  const app = setup();
  const table = await screen.findByRole("table");
  expect(
    screen.queryByRole("button", { name: "渠道检测" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("columnheader", { name: "检测操作" }),
  ).not.toBeInTheDocument();
  const enter = screen.getByRole("button", { name: "降智检测" });
  expect(enter.closest(".data-actions")).toBe(
    screen.getByLabelText("搜索渠道或模型").closest(".data-actions"),
  );
  await app.user.click(screen.getByRole("button", { name: "调整顺序" }));
  await app.user.click(screen.getByLabelText("临时停用 One visible-first m"));
  await app.user.type(screen.getByLabelText("搜索渠道或模型"), "visible");
  const metrics = () =>
    within(table)
      .getAllByRole("row")
      .slice(1)
      .map((row) => row.querySelector(".channel-link")?.textContent);
  const before = metrics();
  await app.user.click(enter);
  expect(screen.getByRole("table")).toBe(table);
  expect(
    screen.queryByRole("button", { name: "降智检测" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "一键检测 · 4" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "取消检测" })).toBeEnabled();
  expect(screen.getAllByRole("button", { name: /^重新检测 / })).toHaveLength(4);
  expect(
    screen.getByLabelText("重新检测 One visible-first m"),
  ).toHaveTextContent("重新检测");
  expect(screen.getByLabelText("搜索渠道或模型")).toHaveValue("visible");
  expect(metrics()).toEqual(before);
  expect(app.checkWrites).toHaveLength(0);
  await app.user.click(screen.getByRole("button", { name: "取消检测" }));
  expect(screen.getByRole("table")).toBe(table);
  expect(
    screen.queryByRole("columnheader", { name: "检测操作" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /^重新检测 / }),
  ).not.toBeInTheDocument();
  expect(
    screen.getByRole("columnheader", { name: "是否降智" }),
  ).toBeInTheDocument();
  expect(screen.getByLabelText("临时停用 One visible-first m")).toBeChecked();
  expect(screen.getByLabelText("应用全部临时修改")).toBeEnabled();
  expect(app.checkWrites).toHaveLength(0);
  expect(app.writes).toHaveLength(0);
  app.unmount();
  app.client.clear();
});

it("runs one check per matching provider across all observation pages and models", async () => {
  const providers = Array.from({ length: 27 }, (_, i) => `visible-${i}`);
  const app = setup(
    false,
    undefined,
    { data: [] },
    { providers: [...providers, "hidden"], models: ["m", "other"] },
  );
  await screen.findByRole("table");
  await app.user.selectOptions(screen.getByLabelText("uni-api 来源"), "one");
  await app.user.type(screen.getByLabelText("搜索渠道或模型"), "visible");
  await app.user.click(screen.getByRole("button", { name: "降智检测" }));
  expect(screen.getAllByRole("button", { name: /^重新检测 / })).toHaveLength(
    25,
  );
  await app.user.click(screen.getByRole("button", { name: "一键检测 · 27" }));
  await waitFor(() => expect(app.checkWrites).toHaveLength(27));
  expect(app.checkWrites.every((write) => write.source === "one")).toBe(true);
  expect(new Set(app.checkWrites.map((write) => write.provider))).toEqual(
    new Set(providers),
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "一键检测 · 27" })).toBeEnabled(),
  );
  expect(screen.getByRole("button", { name: "取消检测" })).toBeEnabled();
  expect(observedCheck("One", "visible-0")).toHaveTextContent("不降智");
  app.unmount();
  app.client.clear();
});

it("cancels individual in-flight checks, hides their actions, and retains the last result", async () => {
  const signals: AbortSignal[] = [];
  const fixture: CheckFixture = {
    data: [storedCheck("one", "visible-first", "pass")],
    run: (_source, _provider, signal) =>
      new Promise((_resolve, reject) => {
        signals.push(signal);
        signal.addEventListener(
          "abort",
          () => reject(new DOMException("Aborted", "AbortError")),
          { once: true },
        );
      }),
  };
  const app = setup(false, undefined, fixture);
  await screen.findByRole("table");
  await app.user.click(screen.getByRole("button", { name: "降智检测" }));
  await app.user.click(screen.getByLabelText("重新检测 One visible-first m"));
  await app.user.click(screen.getByLabelText("重新检测 Two visible-first m"));
  await waitFor(() => expect(signals).toHaveLength(2));
  expect(screen.getByLabelText("重新检测 One visible-first m")).toBeDisabled();
  expect(screen.getByRole("button", { name: "一键检测 · 6" })).toBeDisabled();
  await app.user.click(screen.getByRole("button", { name: "取消检测" }));
  expect(signals.every((signal) => signal.aborted)).toBe(true);
  expect(screen.getByRole("button", { name: "降智检测" })).toBeEnabled();
  expect(observedCheck("One", "visible-first")).toHaveTextContent("不降智");
  expect(observedCheck("One", "visible-first")).not.toHaveTextContent("检测中");
  expect(observedCheck("Two", "visible-first")).toHaveTextContent("未检测");
  await app.user.click(screen.getByRole("button", { name: "降智检测" }));
  expect(screen.getByLabelText("重新检测 One visible-first m")).toBeEnabled();
  expect(screen.getByRole("button", { name: "一键检测 · 6" })).toBeEnabled();
  app.unmount();
  app.client.clear();
});

it("keeps both batch buttons visible while running and preserves completed results when canceled", async () => {
  const signals: AbortSignal[] = [];
  const fixture: CheckFixture = {
    data: [],
    run: (source, provider, signal) =>
      source === "one" && provider === "visible-first"
        ? Promise.resolve(storedCheck(source, provider, "pass"))
        : new Promise((_resolve, reject) => {
            signals.push(signal);
            signal.addEventListener(
              "abort",
              () => reject(new DOMException("Aborted", "AbortError")),
              { once: true },
            );
          }),
  };
  const app = setup(false, undefined, fixture);
  await screen.findByRole("table");
  await app.user.click(screen.getByRole("button", { name: "降智检测" }));
  await app.user.click(screen.getByRole("button", { name: "一键检测 · 6" }));
  await waitFor(() => expect(app.checkWrites).toHaveLength(6));
  await waitFor(() =>
    expect(observedCheck("One", "visible-first")).toHaveTextContent("不降智"),
  );
  expect(screen.getByRole("button", { name: "一键检测 · 6" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "取消检测" })).toBeEnabled();
  await app.user.click(screen.getByRole("button", { name: "取消检测" }));
  expect(signals).toHaveLength(5);
  expect(signals.every((signal) => signal.aborted)).toBe(true);
  expect(
    screen.queryByRole("columnheader", { name: "检测操作" }),
  ).not.toBeInTheDocument();
  expect(observedCheck("One", "visible-first")).toHaveTextContent("不降智");
  expect(observedCheck("Two", "visible-first")).toHaveTextContent("未检测");
  expect(app.checkWrites).toHaveLength(6);
  app.unmount();
  app.client.clear();
});

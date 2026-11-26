import { afterEach, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { MotionConfig, LazyMotion, domAnimation } from "motion/react";
import App from "./App";
import { emptyStats } from "./analytics";
import { inheritedDisabled, moveVisibleProvider, RESET_SCOPE_LABEL } from "./ChannelControls";
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
function openFilter(tab = "条件") {
  const disclosure = document.querySelector<HTMLDetailsElement>(".channel-browser .workbench-disclosure:last-child");
  if (disclosure && !disclosure.open) fireEvent.click(disclosure.querySelector("summary")!);
  if (disclosure) fireEvent.click(within(disclosure).getByRole("button", { name: tab }));
}
function tool(name: string) {
  openFilter("工具");
  return screen.getByRole("button", { name });
}
function condition(name: string) {
  openFilter();
  return screen.getByLabelText(name);
}
function move(label: string, key: "ArrowUp" | "ArrowDown") {
  fireEvent.keyDown(screen.getByRole("button", {name:`移动 ${label}`}), {key});
}
async function openRouting(app: ReturnType<typeof setup>) {
  const navigation = screen.getByRole("navigation", { name: "主导航" });
  await app.user.click(within(navigation).getByRole("button", { name: /^渠道管理/ }));
  // The management page is a lazy chunk; on its first render there is no routing button yet.
  await app.user.click(await screen.findByRole("button", { name: "路由顺序" }, { timeout: 5000 }));
}
function setup(
  controlUnavailable = false,
  beforeWrite?: (
    source: string,
    body: any,
  ) => Promise<Response | undefined> | Response | undefined,
  checkFixture: CheckFixture = { data: [] },
  catalogFixture: { providers?: string[]; models?: string[]; detection?: boolean; rejections?: boolean; sorting?: boolean } = {},
) {
  const states: Record<string, ControlState> = {
    one: { revision: "one:0", instance_id: "one", rules: [] },
    two: { revision: "two:0", instance_id: "two", rules: [] },
  };
  const writes: { source: string; body: any }[] = [];
  const checkWrites: { source: string; provider: string }[] = [];
  const detectionWrites: { path: string; body: any }[] = [];
  const reads: string[] = [];
  const sortCalls: { action: string; models?: { model: string; providers: string[] }[] }[] = [];
  let sortedOrder: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const u = new URL(input, location.origin),
        source =
          u.pathname.match(/\/sources\/([^/]+)\//)?.[1] ||
          u.searchParams.get("source_id") ||
          "all";
      if (!init?.method || init.method === "GET") reads.push(u.pathname);
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
            eligible: !(catalogFixture.rejections && provider === "temp-off"),
            reason: catalogFixture.rejections && provider === "temp-off" ? "temporarily_disabled" : "eligible",
            stats: {
              ...emptyStats(),
              success: 7,
              success_rate_denominator: 7,
              success_rate: catalogFixture.sorting ? (provider === "visible-second" ? 0.9 : 0.1) : 1,
              inflight: 2,
              usage_samples: 7,
              input_tokens: 1000,
              output_tokens: 100,
              estimated_cost_usd: 2.5,
            },
          })),
        ),
      );
      // The real catalog already includes applied gateway rules; drafts do not.
      rows.sort((a,b)=>{
        if(a.source_id!==b.source_id)return ids.indexOf(a.source_id)-ids.indexOf(b.source_id);
        const key=(u.searchParams.get("api_key_id")||"").split("::").at(-1)||"";
        const rule=states[a.source_id].rules.find(r=>r.api_key_id===key && r.model===(u.searchParams.get("model")||""));
        const rank=(p:string)=>rule?.order.includes(p)?rule.order.indexOf(p):9999;
        return rank(a.provider)-rank(b.provider);
      });
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
      else if (catalogFixture.detection && u.pathname.endsWith("/channel-management")) {
        body = { data: ids.flatMap(source_id => catalogFixture.providers!.map(provider => ({
          source_id, provider, models: catalogFixture.models, probe_fingerprint: "revision",
          credential_count: provider === "multi-key" ? 2 : 1,
        }))) };
      } else if (catalogFixture.detection && u.pathname.endsWith("/detection-settings")) {
        body = { disabled: [{ source_id: "one", provider: "visible-0" }] };
      } else if (catalogFixture.detection && (u.pathname.endsWith("/model-fingerprints") || u.pathname.endsWith("/channel-management/checks"))) {
        if (init?.method === "POST") {
          detectionWrites.push({ path: u.pathname, body: JSON.parse(String(init.body)) });
          return Response.json({ accepted: true }, { status: 202 });
        }
        const model = "deepseek-v4.1-flash-1017";
        body = { data: catalogFixture.rejections && u.pathname.endsWith("/model-fingerprints") ? ["deleted", "multi-key", "changed-key"].map(provider => ({
          source_id: "one", provider, model, state: "done", revision: provider === "changed-key" ? "old" : "revision",
          result: {status: "error", checked_at: 1, samples:[{request_id: "sample", state: "error", http_status: 403, error_code: "group_deleted"}]},
        })) : [], supported_models: [model] };
      } else if (catalogFixture.sorting && u.pathname.endsWith("/channel-sort")) {
        const request = JSON.parse(String(init?.body));
        sortCalls.push(request);
        if (request.action === "prepare") {
          sortedOrder = request.models[0].providers;
          body = { receipt: "test-receipt", revision: states[source].revision, changes: [{
            api_key_id: "key", key_position: 1, model: "m",
            before: ["visible-first", "hidden", "visible-second"], after: sortedOrder, upstreams: {},
          }] };
        } else if (request.action === "apply") {
          expect(request.receipt).toBe("test-receipt");
          states[source] = { ...states[source], revision: "one:1", rules: [
            { api_key_id: "key", model: "m", order: sortedOrder, disabled: [] },
          ] };
          body = { status: "applied", revision: "one:1" };
        }
      } else if (u.pathname.endsWith("channel-controls")) {
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
  return { ...app, user, writes, states, client, checkWrites, detectionWrites, reads, sortCalls };
}

it("preloads routing data and lets observation preview and apply sorting while editing", async () => {
  history.replaceState(null, "", "#view=channels");
  const app = setup(false, undefined, { data: [] }, { sorting: true });
  await screen.findByRole("table", {}, { timeout: 5000 });
  await app.user.selectOptions(condition("uni-api 来源"), "one");
  await app.user.selectOptions(screen.getByLabelText("API key 筛选"), "one::key");
  await app.user.selectOptions(screen.getByLabelText("模型筛选"), "m");
  await waitFor(() => {
    expect(app.reads).toContain("/analytics/v1/sources/one/channel-controls");
    expect(app.client.getQueriesData({ queryKey: ["control-catalog"] }).some(([key, data]) =>
      key[2] === "one" && key[3] === "one::key" && data != null)).toBe(true);
    expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(4);
  });
  const firstRow = () => within(screen.getByRole("table")).getAllByRole("row")[1];
  expect(firstRow()).toHaveTextContent("visible-first");
  await app.user.click(tool("调整顺序"));
  await waitFor(() => expect(within(firstRow()).getByRole("button", { name: /^移动 / })).toBeEnabled());
  openFilter("显示");
  await app.user.click(screen.getByRole("button", { name: "多条件排序" }));
  await app.user.click(screen.getByRole("button", { name: "添加排序依据" }));
  await app.user.selectOptions(screen.getByLabelText("第 1 排序依据"), "success");
  await app.user.click(screen.getByRole("button", { name: "预览" }));
  await waitFor(() => expect(firstRow()).toHaveTextContent("visible-second"));
  expect(screen.getByRole("button", { name: "完成调整" })).toBeVisible();
  expect(within(firstRow()).getByRole("button", { name: /^移动 / })).toBeDisabled();
  const filter = document.querySelector<HTMLDetailsElement>(".channel-filter-disclosure")!;
  await app.user.click(within(filter).getByRole("button", { name: /^关闭筛选/ }));
  await waitFor(() => expect(screen.getByRole("button", { name: "应用排序" })).toBeEnabled());
  expect(app.sortCalls.map(call => call.action)).toEqual(["prepare"]);
  expect(app.states.one.rules).toEqual([]);
  await app.user.click(screen.getByRole("button", { name: "应用排序" }));
  await waitFor(() => expect(app.states.one.rules[0]?.order[0]).toBe("visible-second"));
  expect(app.sortCalls.map(call => call.action)).toEqual(["prepare", "apply"]);
});
it("adds handles to existing rows without opening a dialog or adding headers or toolbar rows", async () => {
  history.replaceState(null, "", "#view=routing");
  const app = setup();
  const table = await screen.findByRole("table", {}, {timeout:5000});
  openFilter();
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
    tool("调整顺序").closest(".workbench-popover"),
  ).not.toBeNull();
  const metricsBefore = within(table)
    .getAllByRole("row")
    .slice(1)
    .map((r) =>
      within(r)
        .getAllByRole("cell")
        .slice(0, 7)
        .map((c) => c.textContent),
    );
  const headers = [...table.querySelectorAll("th")];
  const heading = table.closest(".channel-browser")!.querySelector(".workbench-heading");
  const slots = [...heading!.querySelector(".workbench-tools")!.children];
  await app.user.click(tool("调整顺序"));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect([...table.querySelectorAll("th")]).toEqual(headers);
  expect([...heading!.querySelector(".workbench-tools")!.children]).toEqual(slots);
  expect(within(table).getAllByRole("button", {name:/^移动 /})).toHaveLength(6);
  openFilter();
  expect(screen.getByRole("table")).toBe(table);
  expect(screen.getByLabelText("uni-api 来源")).toBe(source);
  expect(source).toHaveValue("");
  expect(screen.getByLabelText("时间范围筛选")).toHaveValue((range as HTMLSelectElement).value);
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
          .slice(0, 7)
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
  await app.user.selectOptions(condition("时间范围筛选"), "today");
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
  expect(screen.getByLabelText("排序")).toHaveTextContent(/请求顺序|配置顺序/);
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
  await app.user.click(within(screen.getByRole("navigation",{name:"主导航"})).getByRole("button", { name: /^余额管理/ }));
  openFilter();
  expect(screen.getByLabelText("uni-api 来源")).toHaveValue("");
  expect(screen.getByLabelText("API key 筛选")).toHaveValue("");
  expect(screen.getByLabelText("模型筛选")).toHaveValue("");
  expect(screen.getByLabelText("时间范围筛选")).toHaveValue("15m");
  expect(screen.getByLabelText("搜索渠道或模型")).toHaveValue("");
  expect(screen.getByLabelText("模型优先级筛选")).toHaveValue("");
  expect(screen.getByLabelText("余额低于美元")).toHaveValue(null);
  for (const label of [
    "端点筛选",
    "流式状态筛选",
    "渠道状态筛选",
    "排序",
  ]) {
    expect(screen.queryByLabelText(label)).not.toBeInTheDocument();
  }
  await app.user.selectOptions(screen.getByLabelText("uni-api 来源"), "two");
  await waitFor(() => expect(screen.getByLabelText("API key 筛选")).toContainHTML('value="two::key"'));
  await app.user.selectOptions(screen.getByLabelText("API key 筛选"), "two::key");
  await app.user.selectOptions(screen.getByLabelText("时间范围筛选"), "1h");
  await app.user.type(screen.getByLabelText("搜索渠道或模型"), "second");
  await openRouting(app);
  openFilter();
  expect(
    labels.map(
      (label) => (screen.getByLabelText(label) as HTMLInputElement).value,
    ),
  ).toEqual(values);
  expect(app.writes).toHaveLength(0);
  await app.user.click(within(screen.getByRole("navigation",{name:"主导航"})).getByRole("button", { name: /^余额管理/ }));
  openFilter();
  expect(screen.getByLabelText("uni-api 来源")).toHaveValue("two");
  expect(screen.getByLabelText("API key 筛选")).toHaveValue("two::key");
  expect(screen.getByLabelText("时间范围筛选")).toHaveValue("1h");
  expect(screen.getByLabelText("搜索渠道或模型")).toHaveValue("second");
  await app.user.click(screen.getByRole("button", { name: "重置筛选" }));
  await openRouting(app);
  openFilter();
  expect(labels.map((label) => (screen.getByLabelText(label) as HTMLInputElement).value)).toEqual(values);
  const tableBeforeCancel = screen.getByRole("table");
  await app.user.click(screen.getByRole("button", { name: "取消调整" }));
  openFilter();
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
}, 10_000);
it("stages source-specific controls inline and retains hidden channels and drafts across shared filters and views", async () => {
  history.replaceState(null, "", "#view=routing");
  const app = setup();
  await screen.findByRole("table");
  openFilter();
  await enterRoute(app);
  await screen.findByLabelText("临时停用 One visible-first m");
  expect(screen.queryByText(/各来源独立|拖动仅调整本页顺位|应用保存各范围的完整顺序/)).not.toBeInTheDocument();
  expect(screen.queryByText(/草稿范围/)).not.toBeInTheDocument();
  await app.user.type(screen.getByLabelText("搜索渠道或模型"), "visible");
  move("One visible-second m", "ArrowUp");
  await app.user.click(screen.getByLabelText("临时停用 One visible-first m"));
  expect(screen.getByText("草稿范围 · 1 项")).toBeVisible();
  expect(screen.queryByText(/拖动仅调整本页顺位|应用保存各范围的完整顺序/)).not.toBeInTheDocument();
  await app.user.selectOptions(condition("时间范围筛选"), "today");
  await app.user.click(screen.getByRole("button", { name: "余额管理" }));
  await openRouting(app);
  openFilter();
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
  openFilter("工具");
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
  expect(reset.closest(".channel-filter-toolbox")).not.toBeNull();
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
  history.replaceState(null, "", "#view=routing");
  const app = setup(true);
  const table = await screen.findByRole("table");
  openFilter();
  await enterRoute(app);
  await waitFor(() => expect(within(table).getByRole("button", {name:"移动 One visible-first m"})).toHaveAttribute("title", expect.stringContaining("控制接口暂不可用")));
  expect(within(table).getByRole("button", {name:"移动 One visible-first m"})).toBeDisabled();
  expect(screen.getByRole("table")).toBe(table);
  expect(within(table).getAllByRole("row")).toHaveLength(7);
  expect(screen.getByLabelText("uni-api 来源")).toHaveValue("");
  app.unmount();
  app.client.clear();
});

it("keeps reset inside the existing filter with scope details in the menu and tooltip", async () => {
  history.replaceState(null, "", "#view=routing");
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
  openFilter();
  await enterRoute(app);
  openFilter("工具");
  const reset = await screen.findByRole("button", { name: RESET_SCOPE_LABEL });
  expect(
    screen.getAllByRole("button", { name: RESET_SCOPE_LABEL }),
  ).toHaveLength(1);
  expect(reset.closest(".channel-filter-toolbox")).not.toBeNull();
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
  openFilter();
  await app.user.type(
    screen.getByLabelText("搜索渠道或模型"),
    "no-matching-channel",
  );
  await screen.findByText("没有匹配的渠道");
  openFilter("工具");
  await app.user.click(reset);
  await app.user.click(
    screen.getByRole("menuitem", { name: "Two / 全部 API key / 全部模型" }),
  );
  await waitFor(() => expect(app.writes).toHaveLength(1));
  expect(app.writes[0].source).toBe("two");
  expect(app.states.one.rules).toHaveLength(2);
  await app.user.clear(condition("搜索渠道或模型"));
  await app.user.selectOptions(screen.getByLabelText("uni-api 来源"), "one");
  await app.user.selectOptions(
    screen.getByLabelText("API key 筛选"),
    "one::key",
  );
  await app.user.selectOptions(screen.getByLabelText("模型筛选"), "m");
  openFilter("工具");
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
    .filter((row) => row.getAttribute("data-source-name") === source)
    .map((row) => row.querySelector(".channel-link strong")?.textContent);
}
async function enterRoute(app: ReturnType<typeof setup>) {
  await app.user.click(tool("调整顺序"));
  openFilter();
}
async function openControls(app: ReturnType<typeof setup>) {
  await screen.findByRole("table");
  openFilter();
  await enterRoute(app);
  await waitFor(() =>
    expect(screen.getByLabelText("临时停用 One visible-first m")).toBeEnabled(),
  );
  await screen.findByLabelText("临时停用 Two visible-first m");
}

it("reorders only the visible provider slots and ignores an already-matching drop", () => {
  const order = ["a", "hidden1", "b", "hidden2", "c", "d"];
  const visible = new Set(["a", "b", "c", "d"]);
  expect(moveVisibleProvider(order, visible, "d", "b", false)).toEqual(["a", "hidden1", "d", "hidden2", "b", "c"]);
  expect(moveVisibleProvider(order, visible, "a", "c", true)).toEqual(["b", "hidden1", "c", "hidden2", "a", "d"]);
  expect(moveVisibleProvider(order, visible, "c", "d", false)).toBe(order);
  expect(moveVisibleProvider(order, visible, "a", "hidden1", true)).toBe(order);
});

it("drags a row in the live table, rejects drops across sources, and stages until applied", async () => {
  history.replaceState(null, "", "#view=routing");
  const app = setup();
  await openControls(app);
  await app.user.type(condition("搜索渠道或模型"), "visible");
  await waitFor(() => expect(providerOrder("One")).toEqual(["visible-first", "visible-second"]));
  const from = screen.getByRole("button", {name: "移动 One visible-second m"});
  const same = screen.getByRole("button", {name: "移动 One visible-first m"}).closest("tr")!;
  const other = screen.getByRole("button", {name: "移动 Two visible-first m"}).closest("tr")!;
  const previous = Object.getOwnPropertyDescriptor(document, "elementFromPoint");
  const hit = vi.fn(() => other);
  Object.defineProperty(document, "elementFromPoint", {configurable: true, value: hit});
  const pointer = (type: string, clientY: number) => fireEvent(from, Object.assign(
    new Event(type, {bubbles:true}), {pointerId:1, isPrimary:true, button:0, clientX:10, clientY},
  ));
  try {
    pointer("pointerdown", 30);
    pointer("pointermove", 0);
    expect(other).not.toHaveAttribute("data-route-drop");
    pointer("pointerup", 0);
    expect(screen.queryByRole("button", {name:"应用全部临时修改"})).not.toBeInTheDocument();
    hit.mockReturnValue(same);
    vi.spyOn(same, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 100, 30));
    pointer("pointerdown", 30);
    pointer("pointermove", 5);
    expect(same).toHaveAttribute("data-route-drop", "before");
    pointer("pointercancel", 5);
    expect(same).not.toHaveAttribute("data-route-drop");
    expect(providerOrder("One")).toEqual(["visible-first", "visible-second"]);
    pointer("pointerdown", 30);
    pointer("pointermove", 5);
    pointer("pointerup", 5);
    expect(providerOrder("One")).toEqual(["visible-second", "visible-first"]);
    expect(providerOrder("Two")).toEqual(["visible-first", "visible-second"]);
    expect(app.writes).toHaveLength(0);
    await app.user.click(screen.getByRole("button", {name:"应用全部临时修改"}));
    await waitFor(() => expect(app.states.one.rules[0].order).toEqual(["visible-second", "hidden", "visible-first"]));
    expect(app.writes).toHaveLength(1);
  } finally {
    if (previous) Object.defineProperty(document, "elementFromPoint", previous);
    else Reflect.deleteProperty(document, "elementFromPoint");
    app.unmount();
    app.client.clear();
  }
});

it("applies moves from both sources with one toolbar action and keeps acknowledged ordering when catalog data lags", async () => {
  history.replaceState(null, "", "#view=routing");
  const app = setup();
  await openControls(app);
  await app.user.type(screen.getByLabelText("搜索渠道或模型"), "visible");
  move("One visible-second m", "ArrowUp");
  move("Two visible-second m", "ArrowUp");
  const apply = screen.getByRole("button", { name: "应用全部临时修改" });
  expect(
    screen.getAllByRole("button", { name: "应用全部临时修改" }),
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
  const reset = tool(RESET_SCOPE_LABEL);
  expect(
    screen
      .getByLabelText("应用全部临时修改")
      .closest(".workbench-heading"),
  ).toBe(reset.closest(".workbench-heading"));
  expect(
    screen
      .getByLabelText("放弃全部临时修改")
      .closest(".channel-filter-toolbox"),
  ).toBe(reset.closest(".channel-filter-toolbox"));
  app.unmount();
  app.client.clear();
});

it("discards all drafts even when search hides every modified channel, preserving applied rules", async () => {
  history.replaceState(null, "", "#view=routing");
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
  move("Two visible-second m", "ArrowUp");
  await app.user.type(screen.getByLabelText("搜索渠道或模型"), "no-match");
  await screen.findByText("没有匹配的渠道");
  await app.user.click(tool("放弃全部临时修改"));
  expect(app.writes).toHaveLength(0);
  await app.user.clear(condition("搜索渠道或模型"));
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
  history.replaceState(null, "", "#view=routing");
  const app = setup();
  await openControls(app);
  move("One visible-first m", "ArrowDown");
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
  history.replaceState(null, "", "#view=routing");
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
  expect(screen.getByLabelText("移动 Two visible-second m")).toBeDisabled();
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
  history.replaceState(null, "", "#view=routing");
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
  await app.user.click(tool("放弃全部临时修改"));
  expect(screen.getByLabelText("临时停用 One hidden m")).toBeChecked();
  expect(
    screen.getByLabelText("临时停用 One visible-first m"),
  ).not.toBeChecked();
  app.unmount();
  app.client.clear();
});

it("lets users stage edits in another source while a save is pending", async () => {
  let release!: () => void;
  history.replaceState(null, "", "#view=routing");
  const app = setup(false, async source => {
    if (source === "one") await new Promise<void>(resolve => { release = resolve; });
  });
  await openControls(app);
  await app.user.click(screen.getByLabelText("临时停用 One visible-first m"));
  await app.user.click(screen.getByLabelText("应用全部临时修改"));
  await waitFor(() => expect(release).toBeDefined());
  expect(screen.getByLabelText("临时停用 One visible-second m")).toBeDisabled();
  const other = screen.getByLabelText("临时停用 Two visible-first m");
  await app.user.click(other);
  expect(other).toBeChecked();
  expect(app.writes.map(write => write.source)).toEqual(["one"]);
  await act(async () => release());
  const apply = screen.getByLabelText("应用全部临时修改");
  await waitFor(() => expect(apply).toBeEnabled());
  await app.user.click(apply);
  await waitFor(() => expect(app.states.two.rules[0]?.disabled).toEqual(["visible-first"]));
  expect(app.writes.map(write => write.source)).toEqual(["one", "two"]);
  app.unmount();
  app.client.clear();
});

it("cancels adjustment and clears drafts from hidden sources and scopes while preserving applied rules", async () => {
  history.replaceState(null, "", "#view=routing");
  const app = setup();
  await openControls(app);
  move("One visible-second m", "ArrowUp");
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
  expect(tool("调整顺序")).toHaveAttribute(
    "aria-pressed",
    "false",
  );
  await app.user.clear(condition("搜索渠道或模型"));
  await app.user.selectOptions(screen.getByLabelText("模型筛选"), "");
  await app.user.selectOptions(screen.getByLabelText("uni-api 来源"), "");
  expect(providerOrder("One")).toEqual(appliedOrder);
  expect(
    screen.queryByRole("columnheader", { name: "临时控制" }),
  ).not.toBeInTheDocument();
  await enterRoute(app);
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
        row.getAttribute("data-source-name") === source,
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
  await screen.findByRole("table");
  openFilter();
  await app.user.selectOptions(screen.getByLabelText("渠道表列布局"),"all");
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
  openFilter();
  await app.user.selectOptions(screen.getByLabelText("渠道表列布局"),"all");
  await app.user.selectOptions(screen.getByLabelText("渠道表列布局"),"all");
  await app.user.click(tool("降智检测"));
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
  openFilter();
  await app.user.selectOptions(screen.getByLabelText("渠道表列布局"),"all");
  // Metrics can render from cache before the independent check-history fetch.
  await waitFor(() => {
    expect(observedCheck("One", "visible-first")).toHaveTextContent("降智");
    expect(observedCheck("One", "visible-first")).not.toHaveTextContent("不降智");
  });
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
  openFilter();
  await app.user.selectOptions(screen.getByLabelText("渠道表列布局"),"all");
  await screen.findByText("最近检测结果读取失败：检测记录暂不可用");
  expect(observedCheck("One", "visible-first")).toHaveTextContent("读取失败");
  expect(within(table).queryByText("未检测")).not.toBeInTheDocument();
  expect(within(table).getAllByRole("row")).toHaveLength(7);
  expect(tool("调整顺序")).toBeEnabled();
  app.unmount();
  app.client.clear();
});

it("opens detection controls inside observation without sending probes or changing filters, metrics and adjustment drafts", async () => {
  history.replaceState(null, "", "#view=routing");
  const app = setup();
  const table = await screen.findByRole("table");
  openFilter();
  await app.user.selectOptions(screen.getByLabelText("渠道表列布局"),"all");
  expect(
    screen.queryByRole("button", { name: "渠道检测" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("columnheader", { name: "检测操作" }),
  ).not.toBeInTheDocument();
  const enter = tool("降智检测");
  expect(enter.closest(".workbench-popover")).toBe(
    screen.getByLabelText("搜索渠道或模型").closest(".workbench-popover"),
  );
  await enterRoute(app);
  await app.user.click(screen.getByLabelText("临时停用 One visible-first m"));
  await app.user.type(screen.getByLabelText("搜索渠道或模型"), "visible");
  const metrics = () =>
    within(table)
      .getAllByRole("row")
      .slice(1)
      .map((row) => row.querySelector(".channel-link")?.textContent);
  const before = metrics();
  await app.user.click(tool("降智检测"));
  expect(screen.getByRole("table")).toBe(table);
  expect(
    screen.queryByRole("button", { name: "降智检测" }),
  ).not.toBeInTheDocument();
  expect(tool("一键检测 · 4")).toBeEnabled();
  expect(tool("取消检测")).toBeEnabled();
  expect(screen.getAllByRole("button", { name: /^重新检测 / })).toHaveLength(4);
  expect(
    screen.getByLabelText("重新检测 One visible-first m"),
  ).toHaveTextContent("重新检测");
  expect(screen.getByLabelText("搜索渠道或模型")).toHaveValue("visible");
  expect(metrics()).toEqual(before);
  expect(app.checkWrites).toHaveLength(0);
  await app.user.click(tool("取消检测"));
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
  openFilter();
  await app.user.selectOptions(screen.getByLabelText("uni-api 来源"), "one");
  await app.user.type(screen.getByLabelText("搜索渠道或模型"), "visible");
  await app.user.selectOptions(screen.getByLabelText("渠道表列布局"),"all");
  await app.user.click(tool("降智检测"));
  expect(screen.getAllByRole("button", { name: /^重新检测 / })).toHaveLength(
    25,
  );
  await app.user.click(tool("一键检测 · 27"));
  await waitFor(() => expect(app.checkWrites).toHaveLength(27));
  expect(app.checkWrites.every((write) => write.source === "one")).toBe(true);
  expect(new Set(app.checkWrites.map((write) => write.provider))).toEqual(
    new Set(providers),
  );
  await waitFor(() =>
    expect(tool("一键检测 · 27")).toBeEnabled(),
  );
  expect(tool("取消检测")).toBeEnabled();
  expect(observedCheck("One", "visible-0")).toHaveTextContent("不降智");
  app.unmount();
  app.client.clear();
});

it("remembers the observation project and queues supported fingerprints across pages without touching routing", async () => {
  const providers = Array.from({ length: 27 }, (_, i) => `visible-${i}`);
  const model = "deepseek-v4.1-flash-1017";
  const fixture = { providers: [...providers, "hidden"], models: [model, "unrelated"], detection: true };
  let app = setup(false, undefined, { data: [] }, fixture);
  await screen.findByRole("table");
  openFilter();
  openFilter("工具");
  const picker = screen.getByLabelText("选择检测项目");
  expect(picker).toHaveValue("quality");
  await app.user.selectOptions(picker, "fingerprint");
  expect(app.detectionWrites).toHaveLength(0);
  app.unmount();
  app.client.clear();
  app = setup(false, undefined, { data: [] }, fixture);
  await screen.findByRole("table");
  openFilter();
  expect(screen.getByLabelText("选择检测项目")).toHaveValue("fingerprint");
  await app.user.selectOptions(screen.getByLabelText("uni-api 来源"), "one");
  await app.user.type(screen.getByLabelText("搜索渠道或模型"), "visible");
  await app.user.click(tool("指纹检测"));
  const batch = await screen.findByRole("button", { name: "一键指纹检测 · 26" });
  expect(app.detectionWrites).toHaveLength(0);
  await app.user.click(batch);
  await waitFor(() => expect(app.detectionWrites).toEqual([{
    path: "/analytics/v1/model-fingerprints",
    body: { targets: providers.slice(1).map(provider => ({ source_id: "one", provider, model })) },
  }]));
  expect(app.checkWrites).toHaveLength(0);
  expect(app.writes).toHaveLength(0);
  await screen.findByText("已提交指纹检测 · 26 项");
  await app.user.click(tool("收起检测"));
  expect(screen.queryByRole("columnheader", { name: "检测操作" })).not.toBeInTheDocument();
});

it("marks only the proven single-key channel as disabled, skips it in batches and allows a manual recheck", async () => {
  const model = "deepseek-v4.1-flash-1017";
  const app = setup(false, undefined, { data: [] }, {
    providers: ["deleted", "temp-off", "multi-key", "changed-key", "healthy"], models: [model], detection: true, rejections: true,
  });
  await screen.findByRole("table", {}, {timeout:5000});
  openFilter();
  openFilter("工具");
  await app.user.selectOptions(screen.getByLabelText("uni-api 来源"), "one");
  const row = (provider: string, source = "One") => within(screen.getByRole("table"))
    .getAllByRole("button",{name:`查看 ${provider} ${model} 详情`})
    .map(button=>button.closest("tr")!).find(item=>item.dataset.sourceName === source)!;
  await waitFor(() => expect(within(row("deleted")).getByText("分组已停用",{selector:".status-pill"})).toBeVisible(), {timeout:5000});
  expect(within(row("temp-off")).getByText("临时停用",{selector:".status-pill"})).toBeVisible();
  for (const provider of ["multi-key","changed-key","healthy"])
    expect(within(row(provider)).getByText("可用",{selector:".status-pill"})).toBeVisible();
  await app.user.selectOptions(screen.getByLabelText("渠道状态筛选"),"unavailable");
  await waitFor(() => expect(row("deleted")).toBeVisible());
  expect(screen.queryByRole("button",{name:`查看 healthy ${model} 详情`})).not.toBeInTheDocument();
  await app.user.selectOptions(screen.getByLabelText("渠道状态筛选"),"");
  await app.user.selectOptions(screen.getByLabelText("选择检测项目"),"fingerprint");
  await app.user.click(tool("指纹检测"));
  const batch = await screen.findByRole("button", { name: "一键指纹检测 · 3" }, {timeout:5000});
  expect(screen.getByText("已跳过 2 个停用或无密钥项")).toBeVisible();
  await app.user.click(batch);
  await waitFor(() => expect(app.detectionWrites[0]?.body.targets.map((target: {provider: string})=>target.provider)).toEqual(["multi-key","changed-key","healthy"]));
  await app.user.click(within(row("deleted")).getByRole("button", {name:`重新检测 One deleted ${model}`}));
  await waitFor(() => expect(app.detectionWrites[1]?.body.targets).toEqual([{source_id:"one",provider:"deleted",model}]));
  expect(app.writes).toHaveLength(0);
});

it.each([
  ["tool-use", "Tool use 检测", ["gpt-6-astra"]],
  ["compaction", "压缩检测", []],
] as const)("queues observation %s with the existing capability endpoint", async (project, label, models) => {
  const model = "gpt-6-astra";
  const app = setup(false, undefined, { data: [] }, { providers: ["visible-0"], models: [model, "unrelated"], detection: true });
  await screen.findByRole("table");
  openFilter();
  openFilter("工具");
  await app.user.selectOptions(screen.getByLabelText("选择检测项目"), project);
  await app.user.click(tool(label));
  const batch = await screen.findByRole("button", { name: `一键${label} · 1` });
  expect(screen.getByLabelText("重新检测 One visible-0 unrelated")).toBeDisabled();
  await app.user.click(batch);
  await waitFor(() => expect(app.detectionWrites[0]).toEqual({
    path: "/analytics/v1/channel-management/checks",
    body: { kind: project, targets: [{ source_id: "two", provider: "visible-0", models }] },
  }));
  const row = screen.getByLabelText(`重新检测 One visible-0 ${model}`);
  await waitFor(() => expect(row).toBeEnabled());
  await app.user.click(row);
  await waitFor(() => expect(app.detectionWrites[1]?.body).toEqual({
    kind: project, manual: true, targets: [{ source_id: "one", provider: "visible-0", models }],
  }));
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
  openFilter();
  await app.user.selectOptions(screen.getByLabelText("渠道表列布局"),"all");
  await app.user.click(tool("降智检测"));
  await app.user.click(screen.getByLabelText("重新检测 One visible-first m"));
  await app.user.click(screen.getByLabelText("重新检测 Two visible-first m"));
  await waitFor(() => expect(signals).toHaveLength(2));
  expect(screen.getByLabelText("重新检测 One visible-first m")).toBeDisabled();
  expect(tool("一键检测 · 4")).toBeEnabled();
  await app.user.click(tool("取消检测"));
  expect(signals.every((signal) => signal.aborted)).toBe(true);
  expect(tool("降智检测")).toBeEnabled();
  expect(observedCheck("One", "visible-first")).toHaveTextContent("不降智");
  expect(observedCheck("One", "visible-first")).not.toHaveTextContent("检测中");
  expect(observedCheck("Two", "visible-first")).toHaveTextContent("未检测");
  await app.user.selectOptions(screen.getByLabelText("渠道表列布局"),"all");
  await app.user.click(tool("降智检测"));
  expect(screen.getByLabelText("重新检测 One visible-first m")).toBeEnabled();
  expect(tool("一键检测 · 6")).toBeEnabled();
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
  openFilter();
  await app.user.selectOptions(screen.getByLabelText("渠道表列布局"),"all");
  await app.user.click(tool("降智检测"));
  await app.user.click(tool("一键检测 · 6"));
  await waitFor(() => expect(app.checkWrites).toHaveLength(6));
  await waitFor(() =>
    expect(observedCheck("One", "visible-first")).toHaveTextContent("不降智"),
  );
  expect(tool("一键检测 · 6")).toBeDisabled();
  expect(tool("取消检测")).toBeEnabled();
  await app.user.click(tool("取消检测"));
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

it("moves a channel to the true source-first position while retaining hidden channels and other source drafts", async()=>{
 history.replaceState(null, "", "#view=routing");
 const app=setup();
 await screen.findByRole("table");
  openFilter();
 await enterRoute(app);
 await screen.findByLabelText("置顶 One visible-second m");
 await app.user.type(screen.getByLabelText("搜索渠道或模型"),"second");
 expect(screen.getByLabelText("置顶 One visible-second m")).toBeEnabled();
 await app.user.click(screen.getByLabelText("置顶 One visible-second m"));
 expect(screen.getByLabelText("置顶 One visible-second m")).toBeDisabled();
 expect(screen.getByLabelText("置顶 Two visible-second m")).toBeEnabled();
 expect(app.writes).toHaveLength(0);
 await app.user.click(screen.getByLabelText("应用全部临时修改"));
 await waitFor(()=>expect(app.writes).toHaveLength(1));
 expect(app.writes[0].source).toBe("one");
 expect(app.writes[0].body.order).toEqual(["visible-second","visible-first","hidden"]);
 app.unmount();app.client.clear();
});

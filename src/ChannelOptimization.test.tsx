import { expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChannelOptimization } from "./ChannelOptimization";
import {
  optimizationFixture,
  modelCheck,
} from "./channelOptimization.test-data";

it("keeps the toolbar unchanged on a long read error and retries from a dialog", async () => {
  const error = "数据读取超时。（请求编号：" + "x".repeat(200) + "）";
  const snapshot = vi
    .fn()
    .mockRejectedValueOnce(new Error(error))
    .mockResolvedValueOnce(optimizationFixture());
  render(
    <QueryClientProvider client={new QueryClient()}>
      <div data-testid="toolbar">
        <ChannelOptimization snapshot={snapshot} />
      </div>
    </QueryClientProvider>,
  );
  const user = userEvent.setup();
  const toolbar = screen.getByTestId("toolbar");
  const button = screen.getByRole("button", { name: "优化渠道模型" });
  await user.click(button);
  const failure = await screen.findByRole("dialog", {
    name: "优化预览读取失败",
  });
  expect(within(failure).getByRole("alert")).toHaveTextContent(error);
  expect(toolbar.children).toHaveLength(1);
  expect(toolbar.firstElementChild).toBe(button);
  expect(toolbar).toHaveTextContent(/^优化$/);
  await user.click(within(failure).getByRole("button", { name: "重新读取" }));
  expect(
    await screen.findByRole("dialog", { name: "优化渠道模型" }),
  ).toBeVisible();
  expect(snapshot).toHaveBeenCalledTimes(2);
});

it("offers all four speed-tier events and applies only chosen changes in the shared batch", async () => {
  const snapshot = optimizationFixture();
  const scope = "a".repeat(64);
  const binding = "b".repeat(64);
  const now = Math.floor(Date.now() / 1000);
  snapshot.routes.s.optimize_extended = true;
  Object.assign(snapshot.imports.data[0], {
    service_tier_scope: scope,
    models: ["gpt-old"],
    service_tier_routing: { fast: "enabled", ultrafast: "disabled_verified" },
  });
  snapshot.routes.s.data[0].model = snapshot.routes.s.data[0].upstream_model =
    "gpt-old";
  snapshot.accounts[0].targets[0].models![0] = modelCheck("gpt-old");
  snapshot.checks = [
    { kind: "fast", status: "not_honored" },
    { kind: "ultrafast", status: "supported" },
  ].map(({ kind, status }) => ({
    source_id: "s",
    provider: "p",
    kind: kind as "fast" | "ultrafast",
    model: "gpt-old",
    fingerprint: scope,
    state: "done",
    message: "",
    history: undefined,
    result: {
      binding,
      status: status as "not_honored" | "supported",
      checked_at: now,
      verified_at: now,
      verified_binding: binding,
    },
  }));
  const fetch = vi.fn(async (_url: string, _init?: RequestInit) =>
    Response.json({ revision: "r2", tier_changes: 1 }),
  );
  vi.stubGlobal("fetch", fetch);
  try {
    const user = userEvent.setup();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <ChannelOptimization snapshot={() => snapshot} />
      </QueryClientProvider>,
    );
    await user.click(screen.getByRole("button", { name: "优化渠道模型" }));
    const events = within(screen.getByRole("group", { name: "优化事件" }));
    for (const name of [
      "新增 Fast",
      "取消 Fast",
      "新增 Ultrafast",
      "取消 Ultrafast",
    ]) {
      expect(events.getByRole("checkbox", { name })).toBeInTheDocument();
    }
    expect(
      screen.queryByRole("button", { name: "保存档位" }),
    ).not.toBeInTheDocument();
    await user.click(events.getByRole("checkbox", { name: "新增 Ultrafast" }));
    expect(
      screen.queryByRole("checkbox", {
        name: "新增 Ultrafast · Fugue Key 1 Site-0.1",
      }),
    ).not.toBeInTheDocument();
    await user.click(
      screen.getByRole("checkbox", {
        name: "取消 Fast · Fugue Key 1 Site-0.1",
      }),
    );
    await user.click(
      screen.getByRole("checkbox", { name: "新增 new · Fugue Key 1 Site-0.1" }),
    );
    await user.click(screen.getByRole("button", { name: "一键应用优化" }));
    await screen.findByText("已应用");
    expect(fetch).toHaveBeenCalledTimes(1);
    const [url, init] = fetch.mock.calls[0];
    expect(new URL(url).pathname).toBe("/analytics/v1/sources/s/channel-batch");
    expect(JSON.parse(String(init?.body))).toMatchObject({
      revision: "r1",
      targets: [
        { provider: "p", models: { "gpt-old": "gpt-old", new: "new" } },
      ],
      tier_choices: [{ provider: "p", scope, tier: "fast", enabled: false }],
    });
  } finally {
    vi.unstubAllGlobals();
  }
});

it("previews a selectable new protocol on an automatic site import and submits the selected split", async () => {
  const snapshot = optimizationFixture();
  snapshot.routes.s.optimize_extended =
    snapshot.routes.s.legacy_engine_migration = true;
  Object.assign(snapshot.imports.data[0], {
    provider: "p--messages",
    engine: "claude",
    engine_mode: "auto",
  });
  Object.assign(snapshot.routes.s.data[0], {
    provider: "p--messages",
    engine: "claude",
    engine_mode: "auto",
  });
  const fetch = vi.fn(async (_url: string, _init?: RequestInit) =>
    Response.json({ revision: "r2" }),
  );
  vi.stubGlobal("fetch", fetch);
  try {
    const user = userEvent.setup();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <ChannelOptimization snapshot={() => snapshot} />
      </QueryClientProvider>,
    );
    await user.click(screen.getByRole("button", { name: "优化渠道模型" }));
    expect(
      screen.getByText("当前协议：Claude Messages · claude"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("新接入协议：Responses / Codex · codex"),
    ).toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
    await user.click(
      screen.getByRole("checkbox", { name: "新增 new · Fugue Key 1 Site-0.1" }),
    );
    await user.click(screen.getByRole("button", { name: "一键应用优化" }));
    await screen.findByText("已应用");
    expect(
      JSON.parse(String(fetch.mock.calls[0][1]?.body)).targets[0],
    ).toMatchObject({
      provider: "p--messages",
      current: { old: "old" },
      models: { old: "old", new: "new" },
      split_protocols: { new: "responses" },
      positions: { old: 1, new: 2 },
    });
  } finally {
    vi.unstubAllGlobals();
  }
});
it("shows legacy retirement and couples a model addition to its selected protocol upgrade", async () => {
  const snapshot = optimizationFixture();
  snapshot.imports.data[0].engine = "gpt";
  snapshot.routes.s.data[0].engine = "gpt";
  snapshot.routes.s.data[0].engine_mode = "explicit";
  snapshot.routes.s.data[0].engine_origin = "legacy_default";
  snapshot.routes.s.legacy_engine_migration = true;
  const fetch = vi.fn(async (_url: string, _init?: RequestInit) =>
    Response.json({ revision: "r2" }),
  );
  vi.stubGlobal("fetch", fetch);
  const user = userEvent.setup();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelOptimization snapshot={() => snapshot} />
    </QueryClientProvider>,
  );
  await user.click(screen.getByRole("button", { name: "优化渠道模型" }));
  expect(screen.getByText("旧版默认 gpt → 自动识别 codex")).toBeInTheDocument();
  expect(screen.getByText("旧版默认：gpt")).toBeInTheDocument();
  const add = screen.getByRole("checkbox", {
    name: "新增 new · Fugue Key 1 Site-0.1",
  });
  const migrate = screen.getByRole("checkbox", {
    name: "升级旧版默认协议 · Fugue Key 1 Site-0.1",
  });
  await user.click(add);
  expect(migrate).toBeChecked();
  await user.click(migrate);
  expect(add).not.toBeChecked();
  await user.click(add);
  await user.click(screen.getByRole("button", { name: "一键应用优化" }));
  await screen.findByText("已应用");
  expect(
    JSON.parse(fetch.mock.calls[0][1]!.body as string).targets[0],
  ).toMatchObject({
    migrate_legacy_engine: true,
    models: { old: "old", new: "new" },
  });
});

it("shows current and detected protocols per channel and all-selection excludes incompatible additions", async () => {
  const snapshot = optimizationFixture();
  snapshot.imports.data.push({
    ...snapshot.imports.data[0],
    provider: "claude-copy",
    name: "Messages Site",
    engine: "claude",
    engine_mode: "explicit",
  });
  snapshot.routes.s.data.push({
    ...snapshot.routes.s.data[0],
    provider: "claude-copy",
    engine: "claude",
  });
  const fetch = vi.fn(async () => Response.json({ revision: "r2" }));
  vi.stubGlobal("fetch", fetch);
  const user = userEvent.setup();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelOptimization snapshot={() => snapshot} />
    </QueryClientProvider>,
  );
  await user.click(screen.getByRole("button", { name: "优化渠道模型" }));
  const blocked = screen.getByRole("checkbox", {
    name: "新增 new · Fugue Key 1 Messages Site",
  });
  expect(blocked).toBeDisabled();
  const group = blocked.closest(".optimization-binding") as HTMLElement;
  expect(
    within(group).getByText("当前协议：Claude Messages · claude"),
  ).toBeInTheDocument();
  expect(
    within(group).getByText("检测协议：Responses / Codex · codex"),
  ).toBeInTheDocument();
  expect(
    within(group).getByText("显式配置：claude（来源未确认）"),
  ).toBeInTheDocument();
  await user.click(screen.getByRole("checkbox", { name: "选择全部优化变更" }));
  expect(blocked).not.toBeChecked();
  expect(screen.getByText("已选 1 项变更")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "一键应用优化" }));
  await screen.findByText("已应用");
  expect(
    JSON.parse(
      (fetch.mock.calls as unknown as [string, RequestInit][])[0][1]
        .body as string,
    ).targets.map((t: { provider: string }) => t.provider),
  ).toEqual(["p"]);
});

it("previews without writes and applies only explicitly checked changes", async () => {
  const snapshot = optimizationFixture();
  snapshot.accounts[0].targets[0].models = [
    modelCheck("old", "error"),
    modelCheck("new"),
  ];
  const fetch = vi.fn(async (_url: string, _init?: RequestInit) =>
    Response.json({ revision: "r2" }),
  );
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const user = userEvent.setup();
  render(
    <QueryClientProvider client={client}>
      <ChannelOptimization snapshot={() => snapshot} />
    </QueryClientProvider>,
  );
  await user.click(screen.getByRole("button", { name: "优化渠道模型" }));
  expect(
    screen.getByRole("dialog", { name: "优化渠道模型" }),
  ).toBeInTheDocument();
  expect(fetch).not.toHaveBeenCalled();
  expect(screen.getByRole("button", { name: "一键应用优化" })).toBeDisabled();
  await user.click(
    screen.getByRole("checkbox", { name: "取消 old · Fugue Key 1 Site-0.1" }),
  );
  expect(screen.getByText(/此 API key 将移除该渠道接入/)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "一键应用优化" }));
  await screen.findByText("已应用");
  const body = JSON.parse(fetch.mock.calls[0][1]!.body as string);
  expect(body).toMatchObject({
    revision: "r1",
    part: "optimize",
    targets: [
      { provider: "p", current: { old: "old" }, models: {}, positions: {} },
    ],
  });
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "一键应用优化" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "完成" }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
});

it("supports event selection and cancel; freezes the preview scope while open", async () => {
  const snapshot = optimizationFixture();
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  const user = userEvent.setup();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelOptimization snapshot={() => snapshot} />
    </QueryClientProvider>,
  );
  await user.click(screen.getByRole("button", { name: "优化渠道模型" }));
  snapshot.filters.model = "unrelated";
  expect(
    screen.getByRole("checkbox", { name: "新增 new · Fugue Key 1 Site-0.1" }),
  ).toBeInTheDocument();
  await user.click(screen.getByRole("checkbox", { name: "选择全部优化变更" }));
  await user.click(screen.getByRole("checkbox", { name: "新增可用模型" }));
  expect(screen.getByRole("button", { name: "一键应用优化" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "取消" }));
  expect(fetch).not.toHaveBeenCalled();
});

it.each([["vendor/actual-model"], [undefined]])(
  "shows the recorded mismatched upstream model in the preview when available (%s)",
  async (returnedModel) => {
    const snapshot = optimizationFixture();
    const check = modelCheck("old");
    check.result!.availability.model_match = "mismatch";
    check.result!.availability.response_model = returnedModel;
    snapshot.accounts[0].targets[0].models = [check];
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    const user = userEvent.setup();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <ChannelOptimization snapshot={() => snapshot} />
      </QueryClientProvider>,
    );
    await user.click(screen.getByRole("button", { name: "优化渠道模型" }));
    expect(screen.getByText("返回模型不匹配")).toBeInTheDocument();
    if (returnedModel) {
      expect(screen.getByText(`返回模型：${returnedModel}`)).toBeVisible();
    } else {
      expect(screen.queryByText(/^返回模型：/)).not.toBeInTheDocument();
    }
    expect(fetch).not.toHaveBeenCalled();
  },
);

it("reports a rejected atomic source batch once without accusing every selected channel", async () => {
  const snapshot = optimizationFixture();
  snapshot.imports.data.push({
    ...snapshot.imports.data[0],
    provider: "peer-binding",
    name: "Other Site",
  });
  snapshot.routes.s.data.push({
    ...snapshot.routes.s.data[0],
    provider: "peer-binding",
    position: 2,
  });
  const fetch = vi.fn(
    async () =>
      new Response(
        "新增或重命名模型必须在当前渠道检测可用，请先完成检测；未通过：new",
        { status: 400 },
      ),
  );
  vi.stubGlobal("fetch", fetch);
  const user = userEvent.setup();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelOptimization snapshot={() => snapshot} />
    </QueryClientProvider>,
  );
  await user.click(screen.getByRole("button", { name: "优化渠道模型" }));
  await user.click(screen.getByRole("checkbox", { name: "选择全部优化变更" }));
  await user.click(screen.getByRole("button", { name: "一键应用优化" }));
  const alert = await screen.findByRole("alert");
  expect(alert).toHaveTextContent("Fugue：本批次优化未确认");
  expect(alert).toHaveTextContent("此提示不代表每个渠道都未通过检测");
  expect(alert.closest(".optimization-binding")).toBeNull();
  expect(screen.getAllByRole("alert")).toHaveLength(1);
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "一键应用优化" })).toBeDisabled();
});

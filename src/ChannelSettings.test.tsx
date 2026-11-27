import { beforeEach, expect, it, vi } from "vitest";
import { render, screen, waitFor, fireEvent, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChannelSettings } from "./ChannelSettings";
import type { Channel } from "./types";
import { setImageGenerationRemoval } from "./imageGenerationRemoval";

const base = {
  provider: "one",
  base_url: "https://example.com/v1/responses",
  model: ["model-a"],
  api: [{ $secret: "opaque-1" }, { $secret: "opaque-2" }],
  preferences: { cooldown_period: 30, headers: { $secret: "header-ref" } },
  extension: { keep: true },
};
let effective: typeof base,
  revision: string,
  writes: Record<string, unknown>[],
  reads: number;
let catalog: Partial<Channel>[];
let bodyOverrides: unknown;
let tierRouting: Record<string,string>;
let groupResults: Record<string, {status: string; checked_at: number}>;
const tierScope = "a".repeat(64);
beforeEach(() => {
  effective = structuredClone(base);
  effective.preferences.cooldown_period = 90;
  revision = "v1";
  writes = [];
  reads = 0;
  catalog = [{ provider: "one", engine: "codex", engine_mode: "auto" }];
  bodyOverrides = undefined;
  tierRouting = {fast:"enabled",ultrafast:"disabled_verified"};
  groupResults = {};
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      if (url.includes("/service-tier-routing?"))
        return Response.json({scope:tierScope,routing:tierRouting,group_results:groupResults});
      if (url.includes("/secrets?"))
        return new Response(
          JSON.stringify({
            keys: {
              "opaque-1": "upstream-secret-one",
              "opaque-2": "upstream-secret-two",
            },
          }),
        );
      if (url.includes("model-channels"))
        return new Response(JSON.stringify({ data: catalog }));
      if (
        url.includes("channel-setting-templates") ||
        url.endsWith("/operations")
      )
        return new Response(JSON.stringify({ data: [] }));
      if (init?.method === "POST" || init?.method === "PATCH") {
        const body = JSON.parse(String(init.body));
        writes.push({ method: init.method, ...body });
        const result = {
          status: init.method === "POST" ? "validated" : "applied",
          operation_id: body.operation_id,
          revision,
          previews: [],
        };
        if (init.method === "PATCH") {
          for (const choice of body.service_tier_choices || [])
            tierRouting[choice.tier] = choice.enabled ? "enabled" : "disabled_verified";
          const change = body.changes[0];
          if ("/preferences/cooldown_period" in change.set)
            effective.preferences.cooldown_period =
              change.set["/preferences/cooldown_period"];
          if ("/preferences/post_body_parameter_overrides" in change.set)
            bodyOverrides =
              change.set["/preferences/post_body_parameter_overrides"];
          if (
            change.remove?.includes(
              "/preferences/post_body_parameter_overrides",
            )
          )
            bodyOverrides = undefined;
          revision = "v2";
        }
        return new Response(JSON.stringify(result));
      }
      reads++;
      return new Response(
        JSON.stringify({
          provider: "one",
          revision,
          kind: "configured",
          base,
          effective: {
            ...effective,
            preferences: {
              ...effective.preferences,
              post_body_parameter_overrides: bodyOverrides,
            },
          },
          override_paths: ["/preferences/cooldown_period"],
          available_keys: [],
          affected_keys: [{ key_id: "caller", models: ["model-a"] }],
          global_preferences: {},
          schema: {
            engines: ["gpt", "codex", "claude", "gemini"],
            fields: [
              { path: "/base_url", type: "string", group: "基本与模型" },
              { path: "/engine", type: "engine", group: "基本与模型" },
              { path: "/model", type: "models", group: "基本与模型" },
              { path: "/api", type: "keys", group: "密钥" },
              { path: "/only_request_types", type: "json", group: "请求规则" },
              { path: "/preferences/compaction_ultrafast_only", type: "boolean", group: "请求规则" },
              {
                path: "/exclude_request_types",
                type: "json",
                group: "请求规则",
              },
              { path: "/preferences/headers", type: "json", group: "请求改写" },
              {
                path: "/preferences/post_body_parameter_overrides",
                type: "json",
                group: "请求改写",
              },
              {
                path: "/preferences/upstream_stream_mode",
                type: "json",
                group: "网络与适配",
              },
              {
                path: "/preferences/cooldown_period",
                type: "number",
                group: "超时与冷却",
              },
            ],
          },
        }),
      );
    }),
  );
});
async function mount(provider = "one") {
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <ChannelSettings
        row={
          { provider, model: "model-a", source_id: "primary" } as Channel
        }
      />
    </QueryClientProvider>,
  );
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "渠道设置" }));
  await screen.findByRole("button", { name: "校验与预览" });
  return user;
}
it("does not render the retired protocol repair action for an imported channel", async () => {
  await mount("sub2api-imported");
  expect(screen.queryByRole("button",{name:"预览旧渠道协议修复"})).not.toBeInTheDocument();
});
it("shows group Fast support without making an unconfigured Ultrafast switch look enabled", async () => {
  tierRouting = {fast:"unconfigured",ultrafast:"unconfigured"};
  const checked = Math.floor(Date.now()/1000);
  groupResults = {fast:{status:"supported",checked_at:checked},ultrafast:{status:"not_honored",checked_at:checked}};
  const user = await mount("sub2api-imported");
  await user.click(screen.getByRole("button",{name:"请求规则"}));
  await screen.findByText("未开启 · 已验证支持");
  expect(screen.getByText("未命中")).toBeInTheDocument();
  const fast = screen.getByRole("switch",{name:"允许 Fast 请求"});
  const ultra = screen.getByRole("switch",{name:"允许 Ultrafast 请求"});
  expect(fast).not.toBeChecked();
  expect(ultra).not.toBeChecked();
  await user.click(fast);
  await user.click(screen.getByRole("button",{name:"校验与预览"}));
  await waitFor(() => expect(screen.getByRole("button",{name:"保存并应用"})).toBeEnabled());
  await user.click(screen.getByRole("button",{name:"保存并应用"}));
  await screen.findByText("设置已保存并应用");
  await waitFor(() => expect(fast).toBeChecked());
  expect(ultra).not.toBeChecked();
  expect(writes[1].service_tier_choices).toEqual([{provider:"sub2api-imported",scope:tierScope,tier:"fast",enabled:true}]);
});
it("stages the two route switches and saves both through the existing settings operation", async () => {
  const user = await mount();
  await user.click(screen.getByRole("button",{name:"请求规则"}));
  const fast = screen.getByRole("switch",{name:"允许 Fast 请求"});
  const ultra = screen.getByRole("switch",{name:"允许 Ultrafast 请求"});
  await waitFor(() => expect(fast).toBeEnabled());
  expect(fast).toBeChecked();
  expect(ultra).not.toBeChecked();
  await user.click(fast);
  await user.click(ultra);
  expect(fast).not.toBeChecked();
  expect(ultra).toBeChecked();
  expect(writes).toHaveLength(0);
  await user.click(screen.getByRole("button",{name:"撤销修改"}));
  await waitFor(() => expect(fast).toBeEnabled());
  expect(fast).toBeChecked();
  expect(ultra).not.toBeChecked();
  await user.click(fast);
  await user.click(ultra);
  await user.click(screen.getByRole("button",{name:"校验与预览"}));
  await waitFor(() => expect(writes).toHaveLength(1));
  const choices = [{provider:"one",scope:tierScope,tier:"fast",enabled:false},{provider:"one",scope:tierScope,tier:"ultrafast",enabled:true}];
  expect(writes[0].service_tier_choices).toEqual(choices);
  await user.click(screen.getByRole("button",{name:"保存并应用"}));
  await screen.findByText("设置已保存并应用");
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1].service_tier_choices).toEqual(choices);
  await waitFor(() => expect(fast).toBeEnabled());
  expect(fast).not.toBeChecked();
  expect(ultra).toBeChecked();
});
it("keeps image removal off by default and previews only the requested removal rules", async () => {
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "请求改写" }));
  const checkbox = screen.getByRole("checkbox", { name: "移除生图参数" });
  expect(checkbox).not.toBeChecked();
  expect(writes).toHaveLength(0);
  await user.click(checkbox);
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect((writes[0].changes as { set: object }[])[0].set).toEqual({
    "/preferences/post_body_parameter_overrides": setImageGenerationRemoval(
      undefined,
      true,
    ),
  });
  expect(screen.getByRole("button", { name: "保存并应用" })).toBeEnabled();
  await user.click(screen.getByRole("button", { name: "保存并应用" }));
  await screen.findByText("设置已保存并应用");
  expect(screen.getByRole("checkbox", { name: "移除生图参数" })).toBeChecked();
  expect(bodyOverrides).toEqual(setImageGenerationRemoval(undefined, true));
  expect((writes[1].changes as object[])[0]).toEqual(
    (writes[0].changes as object[])[0],
  );
});
it("reflects existing YAML rules and preserves advanced overrides when disabled", async () => {
  bodyOverrides = setImageGenerationRemoval(
    { temperature: 0.2, __remove__: ["metadata"] },
    true,
  );
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "请求改写" }));
  const checkbox = screen.getByRole("checkbox", { name: "移除生图参数" });
  expect(checkbox).toBeChecked();
  await user.click(checkbox);
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect((writes[0].changes as { set: object }[])[0].set).toEqual({
    "/preferences/post_body_parameter_overrides": {
      temperature: 0.2,
      __remove__: ["metadata"],
    },
  });
});
it("leaves no pending changes when image removal is enabled then disabled", async () => {
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "请求改写" }));
  const checkbox = screen.getByRole("checkbox", { name: "移除生图参数" });
  await user.click(checkbox);
  await user.click(checkbox);
  expect(screen.getByRole("button", { name: "撤销修改" })).toBeDisabled();
  expect(writes).toHaveLength(0);
});
it("shows the gateway resolved engine for automatic settings without guessing from the URL", async () => {
  catalog = [{ provider: "one", engine: "claude", engine_mode: "auto" }];
  const user = await mount();
  expect(
    await screen.findByRole("option", { name: "自动识别（实际：claude）" }),
  ).toBeVisible();
  expect(screen.getByLabelText("引擎")).toHaveValue("");
  await user.clear(screen.getByLabelText("上游地址"));
  await user.type(
    screen.getByLabelText("上游地址"),
    "https://other.example/v1/responses",
  );
  expect(
    screen.getByRole("option", { name: "自动识别" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("option", { name: "自动识别（实际：claude）" }),
  ).not.toBeInTheDocument();
  expect(writes).toHaveLength(0);
});
it("does not mislabel an unknown or explicit runtime engine as automatic", async () => {
  catalog = [{ provider: "one", engine: "codex" }];
  await mount();
  expect(
    await screen.findByRole("option", { name: "自动识别" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("option", { name: "自动识别（实际：codex）" }),
  ).not.toBeInTheDocument();
});
it("sends only changed fields, preserves zero, and refreshes the draft after applying", async () => {
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "超时与冷却" }));
  const input = screen.getByLabelText("渠道冷却（秒）");
  await user.clear(input);
  await user.type(input, "0");
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "保存并应用" })).toBeEnabled(),
  );
  await user.click(screen.getByRole("button", { name: "保存并应用" }));
  await screen.findByText("设置已保存并应用");
  expect((writes[1].changes as { set: object }[])[0].set).toEqual({
    "/preferences/cooldown_period": 0,
  });
  expect(input).toHaveValue(0);
  expect(reads).toBeGreaterThan(1);
  expect(JSON.stringify(writes)).not.toContain("opaque-");
});
it("groups compaction switches with service tiers and clears contradictory draft rules", async () => {
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "请求规则" }));
  const routing = within(screen.getByRole("region", { name: "服务档位与远程压缩" }));
  expect(routing.getByRole("switch", { name: "允许 Fast 请求" })).toBeInTheDocument();
  expect(routing.getAllByRole("switch")).toHaveLength(5);
  const excluded = routing.getByRole("switch", { name: "排除压缩请求" });
  excluded.focus();
  await user.keyboard("[Space]");
  expect(excluded).toBeChecked();
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect((writes[0].changes as { set: object }[])[0].set).toEqual({
    "/exclude_request_types": ["compaction"],
  });
  await user.click(routing.getByRole("switch", { name: "仅允许压缩请求" }));
  expect(excluded).not.toBeChecked();
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect((writes[1].changes as { set: object }[])[0].set).toEqual({
    "/only_request_types": ["compaction"],
  });
});
it("previews and saves Ultrafast-only compaction instead of excluding all compaction", async () => {
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "请求规则" }));
  const excluded = screen.getByRole("switch", { name: "排除压缩请求" });
  const ultrafast = screen.getByRole("switch", { name: "仅对 Ultrafast 请求开启远程压缩" });
  await user.click(excluded);
  await user.click(ultrafast);
  expect(ultrafast).toBeChecked();
  expect(excluded).not.toBeChecked();
  await user.click(excluded);
  expect(ultrafast).not.toBeChecked();
  await user.click(ultrafast);
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "保存并应用" })).toBeEnabled());
  await user.click(screen.getByRole("button", { name: "保存并应用" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  for (const write of writes) {
    expect((write.changes as {set: object}[])[0].set).toEqual({
      "/preferences/compaction_ultrafast_only": true,
    });
  }
});
it("keeps advanced request types and recognizes legacy compaction spelling", async () => {
  Object.assign(effective, { exclude_request_types: [" COMPACTION ", "future_type"] });
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "请求规则" }));
  const excluded = screen.getByRole("switch", { name: "排除压缩请求" });
  expect(excluded).toBeChecked();
  await user.click(excluded);
  await user.click(screen.getByText("编辑请求类型列表（高级）"));
  const list = screen.getByRole("textbox", { name: "排除请求类型" });
  expect(JSON.parse((list as HTMLTextAreaElement).value)).toEqual(["future_type"]);
  fireEvent.change(list, { target: { value: '["future_type", "another_type"]' } });
  await user.click(excluded);
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect((writes[0].changes as { set: object }[])[0].set).toEqual({
    "/exclude_request_types": ["future_type", "another_type", "compaction"],
  });
});
it("leaves no draft after switching an unset compaction option on and off", async () => {
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "请求规则" }));
  const group = within(screen.getByRole("group", { name: "远程压缩规则" }));
  for (const control of group.getAllByRole("switch")) {
    await user.click(control);
    await user.click(control);
  }
  expect(screen.getByRole("button", { name: "撤销修改" })).toBeDisabled();
  expect(writes).toHaveLength(0);
});
it("reset then edit applies the edited value over the base and never discards edits silently", async () => {
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "恢复基础配置" }));
  await waitFor(() => expect(screen.getByRole("button", {name:"保存并应用"})).toBeEnabled());
  await user.click(screen.getByRole("button", { name: "超时与冷却" }));
  const input = screen.getByLabelText("渠道冷却（秒）");
  await user.clear(input);
  await user.type(input, "12");
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes.length).toBe(2));
  expect((writes[1].changes as object[])[0]).toEqual({
    provider: "one",
    reset: true,
    set: { "/preferences/cooldown_period": 12 },
    remove: [],
  });
});
it("invalid JSON cannot be hidden by switching tabs or saved", async () => {
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "请求改写" }));
  const input = screen.getByLabelText("自定义请求头");
  await user.clear(input);
  await user.type(input, "invalid");
  await user.click(screen.getByRole("button", { name: "超时与冷却" }));
  expect(screen.getByLabelText("自定义请求头")).toBeVisible();
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  expect(writes).toHaveLength(0);
  expect(screen.getByRole("button", { name: "保存并应用" })).toBeDisabled();
});
it("equivalent reordered advanced JSON preserves unknown fields and credential refs", async () => {
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "高级配置" }));
  const input = screen.getByLabelText("渠道高级配置");
  const reordered = Object.fromEntries(Object.entries(effective).reverse());
  const { fireEvent } = await import("@testing-library/react");
  fireEvent.change(input, { target: { value: JSON.stringify(reordered) } });
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect((writes[0].changes as object[])[0]).toEqual({
    provider: "one",
    set: {},
    remove: [],
  });
});

it("reveals saved keys on demand and keeps references intact through reorder, copy and hide", async () => {
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "密钥" }));
  const input = () => screen.getByLabelText("上游密钥 1");
  expect(input()).toHaveValue("");
  expect(input()).toHaveAttribute("type", "password");
  const reveals = () =>
    vi
      .mocked(fetch)
      .mock.calls.filter(([url]) => String(url).includes("/secrets?"));
  expect(reveals()).toHaveLength(0);
  await user.click(screen.getByRole("button", { name: "下移密钥 1" }));
  await user.click(screen.getByRole("button", { name: "显示密钥" }));
  await waitFor(() => expect(input()).toHaveValue("upstream-secret-two"));
  expect(input()).toHaveAttribute("type", "text");
  expect(reveals()).toHaveLength(1);
  expect(String(reveals()[0][0])).toContain("revision=v1");
  await user.click(screen.getByRole("button", { name: "复制密钥 1" }));
  expect(await navigator.clipboard.readText()).toBe("upstream-secret-two");
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect((writes[0].changes as { set: object }[])[0].set).toEqual({
    "/api": [{ $secret: "opaque-2" }, { $secret: "opaque-1" }],
  });
  expect(JSON.stringify(writes)).not.toContain("upstream-secret");
  await user.click(screen.getByRole("button", { name: "隐藏密钥" }));
  expect(input()).toHaveValue("");
  expect(input()).toHaveAttribute("type", "password");
  await user.click(screen.getByRole("button", { name: "显示密钥" }));
  await waitFor(() => expect(input()).toHaveValue("upstream-secret-two"));
  fireEvent.change(input(), { target: { value: "edited-key" } });
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect((writes[1].changes as { set: object }[])[0].set).toEqual({
    "/api": ["edited-key", { $secret: "opaque-1" }],
  });
  await user.click(screen.getByRole("button", { name: "关闭渠道设置" }));
  await user.click(screen.getByRole("button", { name: "渠道设置" }));
  await user.click(await screen.findByRole("button", { name: "密钥" }));
  expect(input()).toHaveValue("");
  expect(input()).toHaveAttribute("type", "password");
});
it("keeps keys hidden after an unsuccessful reveal", async () => {
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "密钥" }));
  vi.mocked(fetch).mockResolvedValueOnce(
    new Response("配置版本已变化", { status: 409 }),
  );
  await user.click(screen.getByRole("button", { name: "显示密钥" }));
  await screen.findByRole("alert");
  expect(screen.getByLabelText("上游密钥 1")).toHaveValue("");
  expect(screen.getByLabelText("上游密钥 1")).toHaveAttribute(
    "type",
    "password",
  );
  expect(writes).toHaveLength(0);
});

it("shows inherited current values without writing them back and removes only the channel override", async () => {
  const original = globalThis.fetch;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const response = await original(input, init);
      if (
        !init?.method &&
        input.includes("/channel-settings?") &&
        response.ok
      ) {
        const view = await response.json();
        delete view.effective.preferences.cooldown_period;
        delete view.base.preferences.cooldown_period;
        view.resolved_fields = {
          "/preferences/cooldown_period": {
            value: 42,
            source: "global",
            inherited_value: 42,
            inherited_source: "global",
            can_inherit: true,
          },
        };
        return Response.json(view);
      }
      return response;
    }),
  );
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "超时与冷却" }));
  const field = screen.getByLabelText("渠道冷却（秒）");
  expect(field).toHaveValue(42);
  expect(screen.queryByRole("button", { name: "渠道冷却（秒）：继承全局设置" })).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect((writes[0].changes as any[])[0]).toMatchObject({
    set: {},
    remove: [],
  });
  await user.clear(field);
  await user.type(field, "7");
  expect(field).toHaveValue(7);
  expect(screen.getByRole("button", { name: "渠道冷却（秒）：继承全局设置" })).toBeEnabled();
  await user.click(
    screen.getByRole("button", { name: "渠道冷却（秒）：继承全局设置" }),
  );
  expect(field).toHaveValue(42);
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect((writes[1].changes as any[])[0]).toMatchObject({
    set: {},
    remove: [],
  });
});

it("can remove a base channel value to inherit the source global value", async () => {
  const original = globalThis.fetch;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const response = await original(input, init);
      if (
        !init?.method &&
        input.includes("/channel-settings?") &&
        response.ok
      ) {
        const view = await response.json();
        view.resolved_fields = {
          "/preferences/cooldown_period": {
            value: 90,
            source: "channel",
            inherited_value: 12,
            inherited_source: "global",
            can_inherit: true,
          },
        };
        return Response.json(view);
      }
      return response;
    }),
  );
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "超时与冷却" }));
  await user.click(
    screen.getByRole("button", { name: "渠道冷却（秒）：继承全局设置" }),
  );
  expect(screen.getByLabelText("渠道冷却（秒）")).toHaveValue(12);
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect((writes[0].changes as any[])[0]).toMatchObject({
    set: {},
    remove: ["/preferences/cooldown_period"],
  });
});

it("lets a user replace an inherited JSON timeout without resetting partial input", async () => {
  const original = globalThis.fetch;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const response = await original(input, init);
      if (!init?.method && input.includes("/channel-settings?")) {
        const view = await response.json();
        view.schema.fields.push({
          path: "/preferences/model_timeout",
          type: "json",
          group: "超时与冷却",
        });
        view.resolved_fields = {
          "/preferences/model_timeout": {
            value: 100,
            inherited_value: 100,
            source: "global",
            inherited_source: "global",
            can_inherit: true,
          },
        };
        return Response.json(view);
      }
      return response;
    }),
  );
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "超时与冷却" }));
  const field = screen.getByLabelText("模型超时（秒）");
  await user.clear(field);
  await user.type(field, "180");
  await user.tab();
  expect(field).toHaveValue("180");
  await user.clear(field);
  await user.type(field, "240");
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect((writes[0].changes as any[])[0].set).toEqual({
    "/preferences/model_timeout": 240,
  });
});

it("previews and applies stream-only as channel intent without rewriting the sample request", async () => {
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "网络与适配" }));
  await user.selectOptions(
    screen.getByLabelText("上游流式模式（Responses）"),
    "stream_only",
  );
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "保存并应用" })).toBeEnabled(),
  );
  await user.click(screen.getByRole("button", { name: "保存并应用" }));
  await screen.findByText("设置已保存并应用");
  expect((writes[1].changes as { set: object }[])[0].set).toEqual({
    "/preferences/upstream_stream_mode": "stream_only",
  });
  expect(JSON.stringify(writes)).not.toContain("post_body_parameter_overrides");
});

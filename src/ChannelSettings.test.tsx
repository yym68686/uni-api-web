import { beforeEach, expect, it, vi } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChannelSettings } from "./ChannelSettings";
import type { Channel } from "./types";

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
beforeEach(() => {
  effective = structuredClone(base);
  effective.preferences.cooldown_period = 90;
  revision = "v1";
  writes = [];
  reads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      if (url.includes("/secrets?"))
        return new Response(
          JSON.stringify({
            keys: {
              "opaque-1": "upstream-secret-one",
              "opaque-2": "upstream-secret-two",
            },
          }),
        );
      if (
        url.includes("channel-setting-templates") ||
        url.endsWith("/operations") ||
        url.includes("model-channels")
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
          effective.preferences.cooldown_period =
            body.changes[0].set["/preferences/cooldown_period"] ?? 30;
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
          effective,
          override_paths: ["/preferences/cooldown_period"],
          available_keys: [],
          affected_keys: [{ key_id: "caller", models: ["model-a"] }],
          global_preferences: {},
          schema: {
            fields: [
              { path: "/base_url", type: "string", group: "基本与模型" },
              { path: "/model", type: "models", group: "基本与模型" },
              { path: "/api", type: "keys", group: "密钥" },
              { path: "/preferences/headers", type: "json", group: "请求改写" },
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
async function mount() {
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <ChannelSettings
        row={
          { provider: "one", model: "model-a", source_id: "primary" } as Channel
        }
      />
    </QueryClientProvider>,
  );
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "渠道设置" }));
  await screen.findByText("来源共享渠道");
  return user;
}
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
it("reset then edit applies the edited value over the base and never discards edits silently", async () => {
  const user = await mount();
  await user.click(screen.getByRole("button", { name: "恢复基础配置" }));
  await screen.findByText("校验通过。请核对下方影响范围和差异。");
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

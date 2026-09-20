import { beforeEach, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
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

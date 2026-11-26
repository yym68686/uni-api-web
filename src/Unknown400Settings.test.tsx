import { afterEach, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChannelSettings, GLOBAL_SETTINGS_SCOPE } from "./ChannelSettings";

const path = "/preferences/unknown_upstream_400_retries";
afterEach(() => vi.unstubAllGlobals());

async function setup(initial?: number, supported = true, maximum = 99) {
  const writes: { method: string; changes: unknown[] }[] = [];
  let prefs =
    initial === undefined ? {} : { unknown_upstream_400_retries: initial };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      if (init?.method) {
        const body = JSON.parse(String(init.body));
        writes.push({ method: init.method, ...body });
        if (init.method === "PATCH") {
          const change = body.changes[0];
          prefs = change.remove.includes(path)
            ? {}
            : { unknown_upstream_400_retries: change.set[path] };
        }
        return Response.json({
          status: init.method === "POST" ? "validated" : "applied",
          operation_id: body.operation_id,
          previews: [],
        });
      }
      if (
        input.includes("channel-setting-templates") ||
        input.endsWith("/operations")
      )
        return Response.json({ data: [] });
      return Response.json({
        provider: GLOBAL_SETTINGS_SCOPE,
        kind: "global",
        revision: "fixture-r1",
        base: { preferences: {} },
        effective: { preferences: prefs },
        override_paths: [],
        available_keys: [],
        affected_keys: [],
        global_preferences: {},
        resolved_fields: {
          [path]: {
            value: prefs.unknown_upstream_400_retries ?? 0,
            inherited_value: 0,
            source: "default",
            inherited_source: "default",
            can_inherit: true,
            note: "所有上游400统一计数；400预算独立于AUTO_RETRY，成功即停；重试可能增加费用与延迟。",
          },
        },
        schema: {
          all_upstream_400_retries: supported,
          fields: [
            {
              path: "/preferences/cooldown_period",
              group: "超时与冷却",
              type: "number",
            },
            {
              path,
              group: "重试保护",
              type: "integer",
              minimum: 0,
              maximum,
            },
          ],
          engines: [],
          key_algorithms: [],
        },
      });
    }),
  );
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <ChannelSettings
        row={{
          provider: GLOBAL_SETTINGS_SCOPE,
          source_id: "fixture",
          source_name: "隔离来源",
          provider_name: "全局设置",
          model: "",
        }}
      />
    </QueryClientProvider>,
  );
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "全局设置" }));
  await screen.findByRole("button", { name: "校验与预览" });
  return { writes, user };
}

it.each([0, 3, 17, 99])(
  "previews and saves integer %i without enabling other policies",
  async (n) => {
    const { writes, user } = await setup();
    await user.click(screen.getByRole("button", { name: "重试保护" }));
    const input = screen.getByRole("textbox", {
      name: "所有上游400换渠道次数",
    });
    expect(input).toHaveValue("");
    expect(input).toHaveAttribute("inputmode", "numeric");
    expect(input).toHaveAccessibleDescription("0–99 次，0 为关闭");
    expect(screen.queryByText(/所有上游400统一计数/)).not.toBeInTheDocument();
    const save = screen.getByRole("button", { name: "保存并应用" });
    expect(save).toBeDisabled();
    await user.type(input, String(n));
    await user.click(screen.getByRole("button", { name: "校验与预览" }));
    await waitFor(() => expect(save).toBeEnabled());
    expect(writes).toHaveLength(1);
    expect(writes[0].changes).toEqual([
      { provider: GLOBAL_SETTINGS_SCOPE, set: { [path]: n }, remove: [] },
    ]);
    await user.click(save);
    await screen.findByText("设置已保存并应用");
    expect(writes).toHaveLength(2);
    expect(writes[1].changes).toEqual(writes[0].changes);
    expect(writes[1].method).toBe("PATCH");
  },
);

it("removes an explicit override and invalidates an earlier preview", async () => {
  const { writes, user } = await setup(2);
  await user.click(screen.getByRole("button", { name: "重试保护" }));
  const input = screen.getByRole("textbox", {
    name: "所有上游400换渠道次数",
  });
  expect(input).toHaveValue("2");
  await user.clear(input);
  await user.type(input, "13");
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "保存并应用" })).toBeEnabled(),
  );
  await user.click(
    screen.getByRole("button", {
      name: "所有上游400换渠道次数：使用运行时默认值",
    }),
  );
  expect(input).toHaveValue("");
  expect(screen.getByRole("button", { name: "保存并应用" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1].changes).toEqual([
    { provider: GLOBAL_SETTINGS_SCOPE, set: {}, remove: [path] },
  ]);
});

it.each([[99, "-1"], [99, "2.5"], [99, "100"], [3, "4"]])(
  "blocks invalid entries for a gateway limit of %i: %s",
  async (maximum, value) => {
    const { writes, user } = await setup(2, true, maximum);
    await user.click(screen.getByRole("button", { name: "重试保护" }));
    const input = screen.getByRole("textbox", { name: "所有上游400换渠道次数" });
    await user.clear(input);
    await user.type(input, value);
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAccessibleDescription(`请输入 0–${maximum} 的整数`);
    expect(screen.getByRole("button", { name: "校验与预览" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "保存并应用" })).toBeDisabled();
    expect(writes).toEqual([]);
    await user.click(screen.getByRole("button", { name: "撤销修改" }));
    expect(input).toHaveValue("2");
    expect(screen.getByRole("button", { name: "校验与预览" })).toBeEnabled();
  },
);

it("does not offer retry configuration for an old gateway schema", async () => {
  await setup(undefined, false);
  expect(
    screen.queryByRole("button", { name: "重试保护" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByLabelText("所有上游400换渠道次数"),
  ).not.toBeInTheDocument();
});

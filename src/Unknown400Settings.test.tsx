import { afterEach, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChannelSettings, GLOBAL_SETTINGS_SCOPE } from "./ChannelSettings";

const path = "/preferences/unknown_upstream_400_retries";
afterEach(() => vi.unstubAllGlobals());

async function setup(initial?: number, supported = true) {
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
            note: "仍受AUTO_RETRY总预算约束；重试可能增加费用与延迟，不改变已有精确规则。",
          },
        },
        schema: {
          fields: [
            {
              path: "/preferences/cooldown_period",
              group: "超时与冷却",
              type: "number",
            },
            ...(supported
              ? [
                  {
                    path,
                    group: "重试保护",
                    type: "integer",
                    minimum: 0,
                    maximum: 3,
                  },
                ]
              : []),
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
  await screen.findByText("来源全局设置");
  return { writes, user };
}

it.each([0, 1, 3])(
  "previews and saves integer %i without enabling other policies",
  async (n) => {
    const { writes, user } = await setup();
    await user.click(screen.getByRole("button", { name: "重试保护" }));
    const select = screen.getByRole("combobox", { name: "未知400换渠道次数" });
    expect(select).toHaveValue("");
    expect(
      within(select)
        .getAllByRole("option")
        .map((o) => (o as HTMLOptionElement).value),
    ).toEqual(["", "0", "1", "2", "3"]);
    expect(screen.getByText(/仍受AUTO_RETRY总预算约束/)).toBeInTheDocument();
    const save = screen.getByRole("button", { name: "保存并应用" });
    expect(save).toBeDisabled();
    await user.selectOptions(select, String(n));
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
  const select = screen.getByRole("combobox", { name: "未知400换渠道次数" });
  expect(select).toHaveValue("2");
  await user.selectOptions(select, "3");
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "保存并应用" })).toBeEnabled(),
  );
  await user.click(
    screen.getByRole("button", { name: "未知400换渠道次数：使用运行时默认值" }),
  );
  expect(select).toHaveValue("");
  expect(screen.getByRole("button", { name: "保存并应用" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1].changes).toEqual([
    { provider: GLOBAL_SETTINGS_SCOPE, set: {}, remove: [path] },
  ]);
});

it("does not offer retry configuration for an old gateway schema", async () => {
  await setup(undefined, false);
  expect(
    screen.queryByRole("button", { name: "重试保护" }),
  ).not.toBeInTheDocument();
  expect(screen.queryByLabelText("未知400换渠道次数")).not.toBeInTheDocument();
});

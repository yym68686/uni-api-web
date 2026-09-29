import { expect, it, vi, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChannelSettings, GLOBAL_SETTINGS_SCOPE } from "./ChannelSettings";

afterEach(() => vi.unstubAllGlobals());
it("edits source global timeouts and hedging through the retained preview/apply contract", async () => {
  const writes: any[] = [];
  const prefs = {
    timeout_policy: {
      rules: [
        {
          match: {
            endpoint: "/v1/responses",
            stream: false,
            model: ["gpt-5*", "gpt-6*"],
          },
          timeout: { total: 100 },
        },
      ],
    },
    hedging: {
      enabled: true,
      max_inflight_attempts: 3,
      winner_policy: "first_valid_success",
    },
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      if (init?.method) {
        const body = JSON.parse(String(init.body));
        writes.push({ path: input, method: init.method, ...body });
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
        revision: "r1",
        base: { provider: GLOBAL_SETTINGS_SCOPE, preferences: prefs },
        effective: { provider: GLOBAL_SETTINGS_SCOPE, preferences: prefs },
        override_paths: [],
        available_keys: [],
        affected_keys: [{ key_id: "all", models: [] }],
        global_preferences: {},
        schema: {
          fields: [
            {
              path: "/preferences/timeout_policy",
              group: "超时与冷却",
              type: "json",
            },
            { path: "/preferences/hedging", group: "并行请求", type: "json" },
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
          source_id: "do",
          source_name: "DigitalOcean",
          provider_name: "全局设置",
          model: "",
        }}
      />
    </QueryClientProvider>,
  );
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "全局设置" }));
  await screen.findByText("来源全局设置");
  expect(screen.getByLabelText("规则 1 模型")).toHaveValue("gpt-5*, gpt-6*");
  expect(screen.getByLabelText("规则 1 流式状态")).toHaveValue("false");
  expect(screen.getByLabelText("规则 1 总时长超时（秒）")).toHaveValue(100);
  await user.clear(screen.getByLabelText("规则 1 总时长超时（秒）"));
  await user.type(screen.getByLabelText("规则 1 总时长超时（秒）"), "180");
  await user.click(screen.getByRole("button", { name: "并行请求" }));
  expect(screen.getByRole("checkbox", { name: "启用 hedging" })).toBeChecked();
  await user.click(screen.getByRole("checkbox", { name: "启用 hedging" }));
  await user.click(screen.getByRole("button", { name: "校验与预览" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toMatchObject({
    path: expect.stringContaining(
      "/analytics/v1/sources/do/channel-settings/validate",
    ),
    changes: [
      {
        provider: GLOBAL_SETTINGS_SCOPE,
        set: {
          "/preferences/hedging": {
            enabled: false,
            max_inflight_attempts: 3,
            winner_policy: "first_valid_success",
          },
          "/preferences/timeout_policy": {
            rules: [
              {
                match: {
                  endpoint: "/v1/responses",
                  stream: false,
                  model: ["gpt-5*", "gpt-6*"],
                },
                timeout: { total: 180 },
              },
            ],
          },
        },
      },
    ],
  });
  expect(
    screen.queryByRole("button", { name: "模板与批量" }),
  ).not.toBeInTheDocument();
});

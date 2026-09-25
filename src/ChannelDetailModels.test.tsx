import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { MotionConfig, LazyMotion, domAnimation } from "motion/react";
import { expect, it, vi } from "vitest";
import App from "./App";
import { emptyStats } from "./analytics";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";

function check(
  model: string,
  state = "done",
  success = true,
): NonNullable<SubTarget["models"]>[number] {
  return {
    model,
    state,
    message: "",
    result: {
      model,
      checked_at: 1800000000,
      verdict: "pass",
      availability: {
        status: success ? "success" : "error",
        text: "test",
        ttft_ms: 100,
        duration_ms: 500,
      },
      quality: {
        status: "success",
        text: "未知",
        ttft_ms: 100,
        duration_ms: 500,
      },
    },
  };
}
function setup() {
  const target = (models: NonNullable<SubTarget["models"]>): SubTarget => ({
    group_id: 1,
    name: "same-group",
    platform: "openai",
    channel: "same-provider",
    rate: 1,
    key_id: 1,
    active: true,
    state: "done",
    message: "",
    result: null,
    models,
  });
  const accounts: SubAccount[] = ["a", "b"].map((id) => ({
    id,
    name: "same-name",
    base: "https://" + id + ".test",
    email: id + "@example.test",
    state: "idle",
    message: "",
    synced_at: 1,
    targets: [
      target(
        id === "a"
          ? [
              check("gpt-6-astra"),
              check("gpt-5.6-sol"),
              check("gpt-5.6-terra", "done", false),
              check("gpt-5.6-luna", "running"),
            ]
          : [check("gpt-5.5")],
      ),
    ],
  }));
  const rows = ["one", "two"].map((source_id) => ({
    source_id,
    source_name: source_id,
    provider: "same-provider",
    model: "gpt-6-astra",
    upstream_model: "gpt-6-astra",
    eligible: true,
    reason: "eligible",
    stats: emptyStats(),
  }));
  rows.push(
    ...["gpt-6-astra", "gpt-5.4"].map((model) => ({
      ...rows[0],
      provider: "configured-provider",
      model,
      upstream_model: model,
    })),
  );
  let readAccounts = async () =>
    new Response(JSON.stringify({ data: accounts }));
  const fetchMock = vi.fn(async (input: string) => {
    const { pathname } = new URL(input, location.origin);
    if (pathname.endsWith("/sub2api/accounts")) return readAccounts();
    const body = pathname.endsWith("/auth/me")
      ? { enabled: true, authenticated: true, username: "admin" }
      : pathname.endsWith("/sources")
        ? {
            data: ["one", "two"].map((id) => ({
              id,
              name: id,
              base: "https://" + id + ".test",
              has_storage: true,
            })),
          }
        : pathname.endsWith("/sub2api/channels")
          ? {
              data: ["one", "two"].map((source_id, i) => ({
                source_id,
                provider: "same-provider",
                account_id: i ? "b" : "a",
                group_id: 1,
                api_key_id: "key",
                models: [i ? "gpt-5.5" : "gpt-6-astra"],
              })),
              labels: {},
              unavailable_sources: [],
            }
          : pathname.endsWith("/api-keys")
            ? { data: [], can_inspect_all: true }
            : /\/(model-channels|channel-metrics|analytics)$/.test(pathname)
              ? { data: rows, snapshot_revision: "v1", total: { requests: 0 } }
              : { data: [], rules: [], status: "unsupported" };
    return new Response(JSON.stringify(body));
  });
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
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
  return {
    ...app,
    client,
    fetchMock,
    accounts,
    user: userEvent.setup(),
    setReadAccounts: (reader: typeof readAccounts) => {
      readAccounts = reader;
    },
  };
}

async function openDetail(app: ReturnType<typeof setup>, index = 0) {
  await screen.findByRole("table");
  await app.user.click(
    screen.getAllByRole("button", {
      name: "查看 same-provider gpt-6-astra 详情",
    })[index],
  );
  return within(await screen.findByRole("dialog"));
}

it("shows all tested available models in the drawer and isolates same-name providers by source and account", async () => {
  const app = setup();
  await screen.findByRole("table");
  expect(
    app.fetchMock.mock.calls.some(([path]) =>
      path.endsWith("/sub2api/accounts"),
    ),
  ).toBe(false);
  let detail = await openDetail(app);
  const section = within(
    await detail.findByRole("region", { name: "可用模型" }),
  );
  await section.findByText("gpt-5.6-sol");
  expect(section.getAllByRole("listitem")).toHaveLength(2);
  expect(section.getByText("gpt-6-astra").closest("li")).toHaveTextContent(
    "已添加",
  );
  expect(section.getByText("gpt-5.6-sol").closest("li")).toHaveTextContent(
    "未添加",
  );
  for (const model of ["gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5"])
    expect(section.queryByText(model)).not.toBeInTheDocument();
  await app.user.click(detail.getByRole("button", { name: "关闭详情" }));
  detail = await openDetail(app, 1);
  const second = within(
    await detail.findByRole("region", { name: "可用模型" }),
  );
  expect(await second.findByText("gpt-5.5")).toBeVisible();
  expect(second.queryByText("gpt-5.6-sol")).not.toBeInTheDocument();
});

it("shows loading and recoverable errors instead of declaring models unavailable", async () => {
  const app = setup();
  let finish!: (response: Response) => void;
  app.setReadAccounts(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  const detail = await openDetail(app);
  expect(await within(detail.getByRole("region",{name:"可用模型"})).findByRole("status")).toHaveTextContent(
    "正在读取可用模型",
  );
  await act(async () => finish(new Response("unavailable", { status: 503 })));
  expect(await detail.findByRole("alert")).toHaveTextContent(
    "可用模型暂时无法读取",
  );
  app.setReadAccounts(
    async () => new Response(JSON.stringify({ data: app.accounts })),
  );
  await app.user.click(detail.getByRole("button", { name: "重试" }));
  expect(await detail.findByText("gpt-5.6-sol")).toBeVisible();
});

it("does not invent available models when tests fail or the saved group is gone", async () => {
  const app = setup();
  app.accounts[0].targets[0].models = [check("gpt-6-astra", "done", false)];
  const detail = await openDetail(app);
  expect(await detail.findByText("暂无检测通过的模型。")).toBeVisible();
  app.accounts[0].targets = [];
  await act(async () => {
    await app.client.invalidateQueries({ queryKey: ["sub2api"] });
  });
  await waitFor(() =>
    expect(detail.getByText("未找到对应的 sub2api 检测记录。")).toBeVisible(),
  );
});

it("shows configured models for ordinary channels even when observation is filtered to one model", async () => {
  const app = setup();
  await screen.findByRole("table");
  await app.user.selectOptions(
    screen.getByLabelText("模型筛选"),
    "gpt-6-astra",
  );
  await app.user.click(
    screen.getByRole("button", {
      name: "查看 configured-provider gpt-6-astra 详情",
    }),
  );
  const detail = within(await screen.findByRole("dialog"));
  const models = within(
    await detail.findByRole("region", { name: "可用模型" }),
  );
  expect(await models.findByText("gpt-5.4")).toBeVisible();
  expect(models.getAllByText("已配置")).toHaveLength(2);
  expect(models.queryByText("sub2api 检测通过")).not.toBeInTheDocument();
  expect(
    app.fetchMock.mock.calls.some(([path]) =>
      path.endsWith("/sub2api/accounts"),
    ),
  ).toBe(false);
});

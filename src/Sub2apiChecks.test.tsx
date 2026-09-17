import { afterEach, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { Sub2apiChecks } from "./Sub2apiChecks";
import type { SubAccount } from "./Sub2apiChecks";
import { LatencyBadge } from "./LatencyBadge";
import { Timing } from "./ChannelMetrics";
import { SUB_MODELS } from "./sub2apiModels";

afterEach(() => vi.unstubAllGlobals());
function mount() {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <Tooltip.Provider>
        <Sub2apiChecks />
      </Tooltip.Provider>
    </QueryClientProvider>,
  );
}
function fixtures(): SubAccount[] {
  return ["one", "two"].map((id) => ({
    id,
    name: id,
    base: `https://${id}.test`,
    email: `${id}@test.com`,
    state: "idle",
    message: "",
    synced_at: 1,
    targets: [
      {
        group_id: 1,
        name: "same-group",
        platform: "openai",
        channel: "same-channel",
        rate: 0.01,
        billing: { rate: 0.01, source: "key", checked_at: 1 },
        key_id: 42,
        active: true,
        state: "done",
        message: "",
        result: {
          model: "gpt-6-astra",
          checked_at: 1,
          verdict: id === "one" ? "pass" : "fail",
          availability: {
            status: "success",
            text: "test",
            ttft_ms: id === "one" ? 1000 : 6500,
            duration_ms: 8000,
          },
          quality: {
            status: "success",
            text: id === "one" ? "未知" : "2024-06",
            ttft_ms: 123,
            duration_ms: 456,
          },
        },
      },
    ],
  }));
}
it("shows multipliers as numbers, filters all-page batch targets, and restores persisted results", async () => {
  const data = fixtures();
  const writes: any[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        writes.push(JSON.parse(init.body as string));
        return new Response('{"queued":true}', { status: 202 });
      }
      return new Response(JSON.stringify({ data }));
    }),
  );
  const user = userEvent.setup();
  const first = mount();
  expect(
    await screen.findByRole("columnheader", { name: "倍率" }),
  ).toBeVisible();
  await user.selectOptions(
    screen.getByLabelText("检测模型筛选"),
    "gpt-6-astra",
  );
  expect(await screen.findAllByText("0.01")).toHaveLength(2);
  expect(screen.getByText("不降智", { selector: "span" })).toBeVisible();
  expect(screen.getByText("降智", { selector: "span" })).toBeVisible();
  const table = screen.getByRole("table");
  expect(table.querySelectorAll(".latency-badge.fast")).toHaveLength(1);
  expect(table.querySelectorAll(".latency-badge.medium")).toHaveLength(1);
  await user.selectOptions(screen.getByLabelText("sub2api 账号筛选"), "two");
  await user.click(
    screen.getByRole("button", { name: "检测所选模型 · 1 个渠道" }),
  );
  await waitFor(() =>
    expect(writes).toEqual([
      {
        targets: [{ account_id: "two", group_id: 1, models: ["gpt-6-astra"] }],
      },
    ]),
  );
  first.unmount();
  mount();
  expect(await screen.findByText("不降智", { selector: "span" })).toBeVisible();
  expect(writes).toHaveLength(1);
});
it("creates an account without storing its password and supports the 2FA step", async () => {
  const writes: any[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        writes.push(JSON.parse(init.body as string));
        return new Response(
          JSON.stringify(
            writes.length === 1
              ? { requires_2fa: true, challenge: "opaque" }
              : { queued: true },
          ),
          { status: 200 },
        );
      }
      return new Response('{"data":[]}');
    }),
  );
  const user = userEvent.setup();
  mount();
  await user.click(screen.getByRole("button", { name: "添加账号" }));
  await user.type(screen.getByLabelText("站点地址"), "https://example.com");
  await user.type(screen.getByLabelText("账号邮箱"), "me@example.com");
  await user.type(screen.getByLabelText("账号密码"), "private-password");
  await user.click(screen.getByRole("button", { name: "连接并检测" }));
  await user.type(await screen.findByLabelText("六位验证码"), "123456");
  expect(screen.queryByLabelText("账号密码")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "验证并检测" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1]).toEqual({ challenge: "opaque", totp_code: "123456" });
  expect(JSON.stringify(localStorage)).not.toContain("private-password");
  expect(JSON.stringify(sessionStorage)).not.toContain("private-password");
});
it("keeps busy accounts from duplicate checks and removes only after explicit selection", async () => {
  const data = fixtures();
  data[0].state = "running";
  data[0].targets[0].state = "running";
  const methods: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) => {
      if (init?.method) {
        methods.push(init.method);
        return new Response('{"ok":true}');
      }
      return new Response(JSON.stringify({ data }));
    }),
  );
  const user = userEvent.setup();
  mount();
  await screen.findByLabelText("检测模型筛选");
  await user.selectOptions(
    screen.getByLabelText("检测模型筛选"),
    "gpt-6-astra",
  );
  const one = await screen.findByRole("button", {
    name: "检测 one same-group",
  });
  expect(one).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "检测所选模型 · 1 个渠道" }),
  ).toBeEnabled();
  await user.click(screen.getByRole("button", { name: "移除账号 two" }));
  expect(methods).toHaveLength(0);
  await user.click(screen.getByRole("button", { name: "确认移除" }));
  await waitFor(() => expect(methods).toEqual(["DELETE"]));
});
it("colors only first-output p50 with exact 5s and 10s boundaries", () => {
  const { container } = render(
    <Tooltip.Provider>
      <div data-testid="badges">
        {[0, 5000, 5001, 10000, 10001, null].map((value, i) => (
          <LatencyBadge key={i} value={value} />
        ))}
      </div>
      <div data-testid="first">
        <Timing
          value={{
            p50_ms: 5001,
            p95_ms: 25000,
            last_ms: 30000,
            sample_count: 10,
            mean_ms: 10000,
          }}
        />
      </div>
      <div data-testid="wait">
        <Timing
          wait
          value={{
            p50_ms: 20000,
            p95_ms: 25000,
            last_ms: 30000,
            sample_count: 10,
            mean_ms: 10000,
          }}
        />
      </div>
    </Tooltip.Provider>,
  );
  const badges = screen.getByTestId("badges");
  expect(badges.querySelectorAll(".fast")).toHaveLength(2);
  expect(badges.querySelectorAll(".medium")).toHaveLength(2);
  expect(badges.querySelectorAll(".slow")).toHaveLength(1);
  expect(screen.getByTestId("wait").querySelector(".latency-badge")).toBeNull();
  expect(
    screen.getByTestId("first").querySelectorAll(".latency-badge"),
  ).toHaveLength(1);
  expect(container).not.toHaveTextContent("NaN");
});

it("filters models, derives rate options from other filters, applies inclusive caps and both sorts", async () => {
  const data = fixtures();
  data.push({
    ...data[1],
    id: "three",
    name: "three",
    email: "three@test.com",
  });
  data.forEach((a, i) => {
    a.targets = [
      {
        ...a.targets[0],
        billing: { rate: [0.2, 0.07, 0.1][i], source: "key", checked_at: 1 },
        models: [
          {
            model: "gpt-6-astra",
            state: "done",
            message: "",
            result: a.targets[0].result,
          },
          {
            model: "gpt-5.6-sol",
            state: "done",
            message: "",
            result: {
              ...a.targets[0].result!,
              model: "gpt-5.6-sol",
              verdict: "not_applicable",
              availability: {
                ...a.targets[0].result!.availability,
                status: i === 1 ? "error" : "success",
              },
              quality: {
                status: "not_applicable",
                text: "",
                ttft_ms: null,
                duration_ms: 0,
              },
            },
          },
        ],
      },
    ];
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify({ data }))),
  );
  const user = userEvent.setup();
  mount();
  await screen.findByRole("table");
  await user.selectOptions(
    screen.getByLabelText("检测模型筛选"),
    "gpt-5.6-sol",
  );
  await user.selectOptions(screen.getByLabelText("可用性筛选"), "success");
  const cap = screen.getByLabelText("倍率上限筛选");
  expect([...cap.querySelectorAll("option")].map((o) => o.value)).toEqual([
    "",
    "0.1",
    "0.2",
  ]);
  await user.selectOptions(screen.getByLabelText("倍率排序"), "asc");
  expect(
    screen.getByRole("table").querySelector("tbody tr")?.textContent,
  ).toContain("three");
  await user.selectOptions(screen.getByLabelText("倍率排序"), "desc");
  expect(
    screen.getByRole("table").querySelector("tbody tr")?.textContent,
  ).toContain("one");
  await user.selectOptions(cap, "0.1");
  expect(screen.getByRole("table").querySelectorAll("tbody tr")).toHaveLength(
    1,
  );
  expect(screen.getByRole("table")).toHaveTextContent("three");
  expect(screen.getByRole("table")).toHaveTextContent("降智");
  expect(
    screen.getByRole("columnheader", { name: "Astra 降智" }),
  ).toBeVisible();
  expect(screen.queryByText(/每个分组独立测试/)).not.toBeInTheDocument();
  expect(screen.queryByText("gpt-6-astra · Responses")).not.toBeInTheDocument();
});

it("preselects successful models and imports into the selected source key at the selected position", async () => {
  const data = fixtures().slice(0, 1),
    writes: any[] = [];
  data[0].targets[0].models = [
    {
      model: "gpt-6-astra",
      state: "done",
      message: "",
      result: data[0].targets[0].result,
    },
    {
      model: "gpt-5.6-sol",
      state: "done",
      message: "",
      result: {
        ...data[0].targets[0].result!,
        model: "gpt-5.6-sol",
        verdict: "not_applicable",
      },
    },
    {
      model: "gpt-5.5",
      state: "done",
      message: "",
      result: {
        ...data[0].targets[0].result!,
        model: "gpt-5.5",
        availability: {
          ...data[0].targets[0].result!.availability,
          status: "error",
        },
      },
    },
  ];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const u = new URL(input);
      if (u.pathname.endsWith("/sub2api/channels")) {
        writes.push(JSON.parse(init?.body as string));
        return new Response(JSON.stringify({ message: "已临时添加至第 2 位" }));
      }
      if (u.pathname.endsWith("/sources"))
        return new Response(
          JSON.stringify({
            data: [
              { id: "source", name: "DigitalOcean", base: "https://do.test" },
            ],
          }),
        );
      if (u.pathname.endsWith("/channel-options"))
        return new Response(
          JSON.stringify({
            supported: true,
            revision: "revision-1",
            keys: [{ key_id: "key-target", position: 2, prefix: "masked" }],
            channels: [
              { provider: "existing", model: "gpt-6-astra" },
              { provider: "existing", model: "gpt-5.6-sol" },
            ],
          }),
        );
      return new Response(JSON.stringify({ data }));
    }),
  );
  const user = userEvent.setup();
  mount();
  await screen.findByRole("table");
  await user.selectOptions(
    screen.getByLabelText("检测模型筛选"),
    "gpt-6-astra",
  );
  await user.click(screen.getByRole("button", { name: "添加到渠道" }));
  const dialog = screen.getByRole("dialog");
  expect(
    within(dialog).getByRole("checkbox", { name: "gpt-6-astra" }),
  ).toBeChecked();
  expect(
    within(dialog).getByRole("checkbox", { name: "gpt-5.6-sol" }),
  ).toBeChecked();
  expect(
    within(dialog).getByRole("checkbox", { name: /gpt-5.5/ }),
  ).toBeDisabled();
  await user.selectOptions(
    within(dialog).getByLabelText("添加到 uni-api 来源"),
    "source",
  );
  await waitFor(() =>
    expect(within(dialog).getByLabelText("添加到 API key")).toBeEnabled(),
  );
  await user.selectOptions(
    within(dialog).getByLabelText("添加到 API key"),
    "key-target",
  );
  await waitFor(() =>
    expect(within(dialog).getByLabelText("渠道添加位置")).toBeEnabled(),
  );
  await user.selectOptions(within(dialog).getByLabelText("渠道添加位置"), "2");
  await user.click(within(dialog).getByRole("button", { name: "添加到渠道" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({
    account_id: "one",
    group_id: 1,
    source_id: "source",
    api_key_id: "key-target",
    models: ["gpt-6-astra", "gpt-5.6-sol"],
    position: 2,
    revision: "revision-1",
  });
  expect(JSON.stringify(writes)).not.toContain("secret");
  await screen.findByText("已临时添加至第 2 位");
});

it("all-models groups channels and probes all six despite available/pass filters", async () => {
  const data = fixtures();
  const writes: any[] = [];
  data[0].targets[0].billing = { rate: 0.18, source: "key", checked_at: 1 };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        writes.push(JSON.parse(init.body as string));
        return new Response('{"queued":true}');
      }
      return new Response(JSON.stringify({ data }));
    }),
  );
  const user = userEvent.setup();
  mount();
  await screen.findByRole("table");
  expect(
    screen.queryByRole("columnheader", { name: "模型" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("table").querySelectorAll("tbody tr")).toHaveLength(
    2,
  );
  await user.selectOptions(screen.getByLabelText("可用性筛选"), "success");
  await user.selectOptions(screen.getByLabelText("降智筛选"), "pass");
  await user.selectOptions(screen.getByLabelText("倍率上限筛选"), "0.18");
  const table = screen.getByRole("table");
  expect(table.querySelectorAll("tbody tr")).toHaveLength(1);
  expect(table).toHaveTextContent("1/6 可用");
  expect(table).toHaveTextContent("5 个未检测");
  const latencyIndex = within(table)
    .getAllByRole("columnheader")
    .findIndex((h) => h.textContent === "首字延迟");
  expect(
    table.querySelector("tbody tr")?.children[latencyIndex],
  ).toHaveTextContent("—");
  await user.click(
    screen.getByRole("button", { name: "检测全部模型 · 1 个渠道" }),
  );
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({
    targets: [{ account_id: "one", group_id: 1, models: [...SUB_MODELS] }],
  });
  await user.click(screen.getByRole("button", { name: "检测 one same-group" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1]).toEqual(writes[0]);
  await user.click(screen.getByText("各模型结果"));
  const details = table.querySelector("details")!;
  expect(details.open).toBe(true);
  for (const model of SUB_MODELS) expect(details).toHaveTextContent(model);
  expect(details.querySelectorAll(".latency-badge")).toHaveLength(1);
  await user.click(screen.getByRole("button", { name: "添加到渠道" }));
  const dialog = screen.getByRole("dialog");
  expect(
    within(dialog).getByRole("checkbox", { name: "gpt-6-astra" }),
  ).toBeChecked();
  expect(
    within(dialog).getByRole("checkbox", { name: /gpt-5\.6-sol.*未检测/ }),
  ).toBeDisabled();
});

it("specific model uses Astra group quality and only tests the selected model", async () => {
  const data = fixtures();
  const writes: any[] = [];
  data.forEach((a) => {
    a.targets[0].models = [
      {
        model: "gpt-6-astra",
        state: "done",
        message: "",
        result: a.targets[0].result,
      },
      {
        model: "gpt-5.6-sol",
        state: "done",
        message: "",
        result: {
          ...a.targets[0].result!,
          model: "gpt-5.6-sol",
          verdict: "not_applicable",
          quality: {
            status: "not_applicable",
            text: "",
            ttft_ms: null,
            duration_ms: 0,
          },
        },
      },
    ];
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        writes.push(JSON.parse(init.body as string));
        return new Response('{"queued":true}');
      }
      return new Response(JSON.stringify({ data }));
    }),
  );
  const user = userEvent.setup();
  mount();
  await screen.findByRole("table");
  await user.selectOptions(
    screen.getByLabelText("检测模型筛选"),
    "gpt-5.6-sol",
  );
  await user.selectOptions(screen.getByLabelText("降智筛选"), "pass");
  await user.selectOptions(screen.getByLabelText("可用性筛选"), "success");
  const table = screen.getByRole("table");
  expect(table.querySelectorAll("tbody tr")).toHaveLength(1);
  expect(table).toHaveTextContent("one");
  expect(table).toHaveTextContent("gpt-5.6-sol");
  expect(table).toHaveTextContent("不降智");
  await user.click(
    screen.getByRole("button", { name: "检测所选模型 · 1 个渠道" }),
  );
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0].targets).toEqual([
    { account_id: "one", group_id: 1, models: ["gpt-5.6-sol"] },
  ]);
  await user.selectOptions(screen.getByLabelText("检测模型筛选"), "");
  expect(screen.getByRole("table")).toHaveTextContent("2/6 可用");
  await user.click(
    screen.getByRole("button", { name: "检测全部模型 · 1 个渠道" }),
  );
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1].targets[0].models).toEqual([...SUB_MODELS]);
});

it("all-model batch spans all filtered pages and includes failed and untested siblings", async () => {
  const data = fixtures().slice(0, 1);
  const first = data[0].targets[0];
  data[0].targets = Array.from({ length: 28 }, (_, i) => ({
    ...first,
    group_id: i + 1,
    name: "group-" + (i + 1),
    models: [
      {
        model: "gpt-6-astra",
        state: "done",
        message: "",
        result: first.result,
      },
      {
        model: "gpt-5.5",
        state: "done",
        message: "",
        result: {
          ...first.result!,
          model: "gpt-5.5",
          availability: { ...first.result!.availability, status: "error" },
        },
      },
    ],
  }));
  const writes: any[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        writes.push(JSON.parse(init.body as string));
        return new Response('{"queued":true}');
      }
      return new Response(JSON.stringify({ data }));
    }),
  );
  const user = userEvent.setup();
  mount();
  await screen.findByRole("table");
  await user.selectOptions(screen.getByLabelText("可用性筛选"), "error");
  expect(screen.getByRole("table").querySelectorAll("tbody tr")).toHaveLength(
    25,
  );
  expect(screen.getAllByText(/1 个失败/).length).toBeGreaterThan(0);
  await user.click(screen.getByRole("button", { name: "下一页" }));
  expect(screen.getByRole("table").querySelectorAll("tbody tr")).toHaveLength(
    3,
  );
  await user.click(
    screen.getByRole("button", { name: "检测全部模型 · 28 个渠道" }),
  );
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0].targets).toHaveLength(28);
  for (const target of writes[0].targets)
    expect(target.models).toEqual([...SUB_MODELS]);
});

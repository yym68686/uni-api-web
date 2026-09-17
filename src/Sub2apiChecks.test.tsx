import { afterEach, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { Sub2apiChecks } from "./Sub2apiChecks";
import type { SubAccount } from "./Sub2apiChecks";
import { LatencyBadge } from "./LatencyBadge";
import { Timing } from "./ChannelMetrics";

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
  expect(await screen.findAllByText("0.01")).toHaveLength(2);
  expect(screen.getByText("不降智", { selector: "span" })).toBeVisible();
  expect(screen.getByText("降智", { selector: "span" })).toBeVisible();
  const table = screen.getByRole("table");
  expect(table.querySelectorAll(".latency-badge.fast")).toHaveLength(1);
  expect(table.querySelectorAll(".latency-badge.medium")).toHaveLength(1);
  await user.selectOptions(screen.getByLabelText("sub2api 账号筛选"), "two");
  await user.click(screen.getByRole("button", { name: "一键检测 · 1" }));
  await waitFor(() =>
    expect(writes).toEqual([{ targets: [{ account_id: "two", group_id: 1 }] }]),
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
  const one = await screen.findByRole("button", {
    name: "检测 one same-group",
  });
  expect(one).toBeDisabled();
  expect(screen.getByRole("button", { name: "一键检测 · 1" })).toBeEnabled();
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

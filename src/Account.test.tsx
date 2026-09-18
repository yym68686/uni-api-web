import { it, expect, vi } from "vitest";
import { render, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { MotionConfig, LazyMotion, domAnimation } from "motion/react";
import App from "./App";
import { emptyStats } from "./analytics";
function mount() {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MotionConfig reducedMotion="always">
        <LazyMotion features={domAnimation}>
          <Tooltip.Provider>
            <App />
          </Tooltip.Provider>
        </LazyMotion>
      </MotionConfig>
    </QueryClientProvider>,
  );
}
it("uses account login, restores a cookie session and scopes same-name channels by source", async () => {
  let logged = false;
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, location.origin);
      calls.push(url.pathname + url.search);
      expect(
        (init?.headers as Record<string, string>)?.Authorization,
      ).toBeUndefined();
      let body: any = {};
      const selected =
        url.pathname.includes("/sources/one/") ||
        url.searchParams.get("source_id") === "one";
      const rows = (selected ? ["one"] : ["one", "two"]).map(
        (source_id, i) => ({
          source_id,
          source_name: source_id,
          provider: "shared",
          model: "model-a",
          upstream_model: "model-a",
          endpoint: "all",
          stream: null,
          eligible: true,
          reason: "eligible",
          stats: {
            ...emptyStats(),
            success: i + 1,
            success_rate_denominator: i + 1,
          },
        }),
      );
      if (url.pathname.endsWith("/auth/me"))
        body = {
          enabled: true,
          authenticated: logged,
          username: logged ? "admin" : "",
        };
      else if (url.pathname.endsWith("/auth/login")) {
        logged = true;
        body = { ok: true };
      } else if (url.pathname.endsWith("/auth/logout")) {
        logged = false;
        body = { ok: true };
      } else if (url.pathname.endsWith("/sources"))
        body = {
          data: [
            {
              id: "one",
              name: "One",
              base: "https://one.test",
              has_storage: true,
            },
            {
              id: "two",
              name: "Two",
              base: "https://two.test",
              has_storage: true,
            },
          ],
        };
      else if (url.pathname.endsWith("channel-checks")) body = { data: [] };
      else if (url.pathname.endsWith("api-keys"))
        body = { can_inspect_all: true, data: [] };
      else if (url.pathname.endsWith("channel-balances"))
        body = { status: "unsupported", provider: "shared" };
      else if (url.pathname.endsWith("analytics"))
        body = {
          data: rows,
          total: { requests: 3 },
          models: [],
          coverage: "available_history",
        };
      else body = { data: rows, snapshot_revision: "1" };
      return new Response(JSON.stringify(body));
    }),
  );
  const user = userEvent.setup();
  const first = mount();
  await user.type(await screen.findByLabelText("用户名"), "admin");
  await user.type(screen.getByLabelText("密码"), "password-test");
  await user.click(screen.getByRole("button", { name: /登录/ }));
  const table = await screen.findByRole("table");
  expect(within(table).getAllByRole("row")).toHaveLength(3);
  expect(screen.getByLabelText("uni-api 来源")).toHaveValue("");
  await user.selectOptions(screen.getByLabelText("uni-api 来源"), "one");
  await waitFor(() =>
    expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(
      2,
    ),
  );
  expect(calls.some((x) => x.includes("source_id=one"))).toBe(true);
  first.unmount();
  mount();
  await screen.findByRole("table");
  expect(screen.queryByLabelText("密码")).toBeNull();
  await user.click(screen.getByRole("button", { name: "退出登录" }));
  await screen.findByLabelText("用户名");
  expect(logged).toBe(false);
});
it("does not downgrade authentication on a network error", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => {
      throw new TypeError("network");
    }),
  );
  mount();
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "账户服务暂不可用",
  );
  expect(screen.queryByLabelText(/访问密钥/)).toBeNull();
});

it("shows zero attempts for an idle selected key while shared channels have traffic from another key", async () => {
  const calls: URL[] = [];
  const channel = (provider: string) => ({
    source_id: "primary",
    source_name: "Fugue",
    provider,
    model: "gpt-6-astra",
    upstream_model: "gpt-6-astra",
    endpoint: "all",
    stream: null,
    eligible: true,
    reason: "eligible",
    stats: emptyStats(),
  });
  const catalog = [channel("self-channel"), channel("business-channel")];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      const url = new URL(input, location.origin);
      calls.push(url);
      let body: unknown;
      if (url.pathname.endsWith("/auth/me"))
        body = { enabled: true, authenticated: true, username: "admin" };
      else if (url.pathname.endsWith("/sources"))
        body = {
          data: [
            {
              id: "primary",
              name: "Fugue",
              base: "https://gateway.example",
              has_storage: true,
            },
          ],
        };
      else if (url.pathname.endsWith("/api-keys"))
        body = {
          can_inspect_all: true,
          data: [
            {
              key_id: "primary::key-business",
              position: 1,
              prefix: "business",
            },
            { key_id: "primary::key-2b", position: 2, prefix: "2b" },
          ],
        };
      else if (url.pathname.endsWith("/model-channels"))
        body = { data: catalog, snapshot_revision: "1" };
      else if (url.pathname.endsWith("/channel-metrics"))
        body = {
          data: catalog.map((row) => ({
            ...row,
            stats: { ...emptyStats(), inflight: 9 },
          })),
          generated_at: 1800000000,
        };
      else if (url.pathname.endsWith("/analytics")) {
        const idle = url.searchParams.get("key_id") === "key-2b";
        body = {
          data: idle
            ? []
            : [
                {
                  ...catalog[1],
                  stats: {
                    ...emptyStats(),
                    success: 7,
                    success_rate_denominator: 7,
                    success_rate: 1,
                    input_tokens: 100,
                    output_tokens: 20,
                    usage_samples: 7,
                    estimated_cost_usd: 5,
                  },
                },
              ],
          total: { requests: idle ? 0 : 7, attempts: idle ? 0 : 7 },
          models: [],
          coverage: "available_history",
        };
      } else if (url.pathname.endsWith("/channel-balances"))
        body = { status: "unsupported" };
      else body = { data: [], labels: {}, unavailable_sources: [] };
      return new Response(JSON.stringify(body));
    }),
  );
  mount();
  const user = userEvent.setup();
  const business = () =>
    screen.getByText("business-channel", { selector: "strong" }).closest("tr")!;
  await screen.findByText("business-channel");
  await waitFor(() =>
    expect(within(business()).getAllByRole("cell")[6]).toHaveTextContent(/^7$/),
  );
  await user.selectOptions(
    screen.getByLabelText("API key 筛选"),
    "primary::key-2b",
  );
  await waitFor(() =>
    expect(within(business()).getAllByRole("cell")[6]).toHaveTextContent(/^0$/),
  );
  const cells = within(business()).getAllByRole("cell");
  expect(cells[5]).toHaveTextContent("—");
  expect(cells[9]).not.toHaveTextContent("120");
  expect(cells[10]).not.toHaveTextContent("5.00");
  expect(
    screen.getByRole("columnheader", { name: "渠道总并发" }),
  ).toBeVisible();
  expect(
    screen.getByRole("columnheader", { name: "渠道实际消费" }),
  ).toBeVisible();
  expect(
    calls.some(
      (u) =>
        u.pathname.endsWith("/analytics") &&
        u.searchParams.get("source_id") === "primary" &&
        u.searchParams.get("key_id") === "key-2b",
    ),
  ).toBe(true);
  await user.click(screen.getByRole("button", { name: "查看趋势" }));
  await waitFor(() =>
    expect(
      calls.some(
        (u) =>
          u.searchParams.get("timeseries") === "true" &&
          u.searchParams.get("key_id") === "key-2b",
      ),
    ).toBe(true),
  );
  await user.selectOptions(screen.getByLabelText("API key 筛选"), "");
  await waitFor(() =>
    expect(within(business()).getAllByRole("cell")[6]).toHaveTextContent(/^7$/),
  );
});

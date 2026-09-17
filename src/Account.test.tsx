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

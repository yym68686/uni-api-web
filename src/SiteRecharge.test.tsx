import { afterEach, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SiteRecharge } from "./SiteRecharge";
import { SiteAccountSettings } from "./SiteAccountSettings";
import { controlRequest } from "./api";
import type { SubAccount } from "./Sub2apiChecks";

vi.mock("./api", async (original) => ({
  ...(await original<typeof import("./api")>()),
  controlRequest: vi.fn(),
}));
const request = vi.mocked(controlRequest);
const account = {
  id: "account/a",
  name: "Site",
  base: "https://site.example",
  email: "me@example.com",
  targets: [],
} as unknown as SubAccount;
const config = {
  enabled: true,
  shop_url: "https://wzyp.cn/shop/SHOP123",
  products: ["PRODUCT1"],
  contact: "buyer",
  max_cny: "10.00",
  target_base: account.base,
  target_email: account.email,
};
const quote = {
  id: "quote-1",
  product: "PRODUCT1",
  name: "1元充值",
  merchant: "shop",
  target: "https://site.example · me@example.com",
  total_cny: "1.03",
  fee_cny: "0.03",
  expires_at: 9999999999,
};
function fixture(
  configured = true,
  mutate?: (path: string, init: RequestInit) => unknown,
) {
  request.mockImplementation(async (path, init = {}) => {
    if (init.method && mutate) return (await mutate(path, init)) as never;
    if (path.endsWith("/settings"))
      return {
        config: { ...config },
        supported: true,
        payment_configured: configured,
        identity_current: true,
      } as never;
    if (path.endsWith("/jobs")) return { jobs: [] } as never;
    throw new Error("unexpected read: " + path);
  });
}
afterEach(() => {
  request.mockReset();
  vi.useRealTimers();
});

it("does not load recharge or mutate anything until the settings tab is explicitly opened", async () => {
  fixture();
  const user = userEvent.setup();
  render(
    <SiteAccountSettings account={account} close={vi.fn()} saved={vi.fn()} />,
  );
  expect(request).not.toHaveBeenCalled();
  await user.click(screen.getByRole("button", { name: "充值设置" }));
  expect(
    await screen.findByRole("heading", { name: "实时充值" }),
  ).toBeVisible();
  expect(request.mock.calls).toHaveLength(2);
  expect(request.mock.calls.every(([, init]) => !init?.method)).toBe(true);
});

it("keeps payment disabled without an explicitly configured payment agent", async () => {
  fixture(false);
  render(<SiteRecharge account={account} />);
  expect(await screen.findByText(/支付代理尚未配置/)).toBeVisible();
  fireEvent.change(screen.getByRole("combobox"), {
    target: { value: "PRODUCT1" },
  });
  expect(
    screen.getByRole("button", { name: /获取实时含手续费报价/ }),
  ).toBeDisabled();
  expect(screen.getByRole("button", { name: "开通支付宝授权" })).toBeDisabled();
});

it("requires exact quote confirmation and never automatically retries an unknown submission", async () => {
  const writes: { path: string; body: Record<string, unknown> }[] = [];
  fixture(true, (path, init) => {
    writes.push({ path, body: JSON.parse(init.body as string) });
    if (path.endsWith("/quotes")) return quote;
    if (path.endsWith("/jobs")) throw new Error("提交结果未确认，请核对原订单");
    throw new Error("unexpected write");
  });
  const user = userEvent.setup();
  render(<SiteRecharge account={account} />);
  await screen.findByRole("heading", { name: "实时充值" });
  await user.selectOptions(screen.getByRole("combobox"), "PRODUCT1");
  await user.click(
    screen.getByRole("button", { name: /获取实时含手续费报价/ }),
  );
  expect(await screen.findByText("实付 ¥1.03，含手续费 ¥0.03")).toBeVisible();
  const pay = screen.getByRole("button", { name: "确认支付 ¥1.03 并自动兑换" });
  expect(pay).toBeDisabled();
  expect(writes).toHaveLength(1);
  await user.click(
    screen.getByRole("checkbox", { name: /确认本次商品、目标账号及实付金额/ }),
  );
  await user.click(pay);
  expect(await screen.findByRole("alert")).toHaveTextContent("提交结果未确认");
  expect(writes).toHaveLength(2);
  const first = writes[1].body;
  expect(first).toMatchObject({
    quote_id: "quote-1",
    total_cny: "1.03",
    confirmed: true,
  });
  expect(first.idempotency_key).toBeTruthy();
  await user.click(pay);
  await waitFor(() => expect(writes).toHaveLength(3));
  expect(writes[2].body).toEqual(first); // explicit retry reuses original key
});

it("shares saved bindings rather than silently buying products from unsaved form edits", async () => {
  fixture();
  const user = userEvent.setup();
  render(<SiteRecharge account={account} />);
  await screen.findByRole("heading", { name: "实时充值" });
  await user.clear(screen.getByRole("textbox", { name: "链动店铺地址" }));
  await user.type(
    screen.getByRole("textbox", { name: "链动店铺地址" }),
    "https://wzyp.cn/shop/OTHER",
  );
  expect(request.mock.calls.every(([, init]) => !init?.method)).toBe(true);
  expect(screen.getByRole("button", { name: "保存充值设置" })).toBeDisabled();
});

it("never renders a non-official authorization link returned by a provider", async () => {
  fixture(true, () => ({
    status: "pending",
    message: "授权待确认",
    action_url: "https://evil.example/phish",
  }));
  const user = userEvent.setup();
  render(<SiteRecharge account={account} />);
  await screen.findByRole("heading", { name: "实时充值" });
  await user.click(screen.getByRole("button", { name: "开通支付宝授权" }));
  expect(await screen.findByText("授权待确认")).toBeVisible();
  expect(
    screen.queryByRole("link", { name: /打开支付宝/ }),
  ).not.toBeInTheDocument();
});

it("blocks another purchase while the original order needs reconciliation", async () => {
  fixture();
  const original = request.getMockImplementation()!;
  request.mockImplementation(async (path, init) =>
    path.endsWith("/jobs")
      ? ({
          jobs: [
            { id: "job-1", state: "review", message: "原订单待核对", quote },
          ],
        } as never)
      : original(path, init),
  );
  const user = userEvent.setup();
  render(<SiteRecharge account={account} />);
  expect(await screen.findByText("原订单待核对")).toBeVisible();
  await user.selectOptions(screen.getByRole("combobox"), "PRODUCT1");
  expect(
    screen.getByRole("button", { name: /获取实时含手续费报价/ }),
  ).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "核对原订单并继续交付" }),
  ).toBeEnabled();
});

it("marks failed background refreshes as stale without resubmitting payment", async () => {
  vi.useFakeTimers();
  fixture();
  render(<SiteRecharge account={account} />);
  await act(async () => {
    await Promise.resolve();
  });
  expect(screen.getByRole("heading", { name: "实时充值" })).toBeVisible();
  request.mockRejectedValue(new Error("offline"));
  await act(async () => {
    await vi.advanceTimersByTimeAsync(15000);
  });
  expect(screen.getByRole("alert")).toHaveTextContent("充值状态刷新失败");
  expect(request.mock.calls.every(([, init]) => !init?.method)).toBe(true);
});

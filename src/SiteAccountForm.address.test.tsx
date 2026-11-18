import { beforeEach, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { controlRequest } from "./api";
import { AccountForm } from "./SiteAccountForm";
import { browserHelperAvailable, loginWithBrowser } from "./sub2apiBrowser";
import type { SubAccount } from "./Sub2apiChecks";

vi.mock("./api", () => ({ controlRequest: vi.fn() }));
vi.mock("./sub2apiBrowser", () => ({
  browserHelperAvailable: vi.fn().mockResolvedValue(false),
  loginWithBrowser: vi.fn(),
}));

const initial: SubAccount = {
  id: "account",
  name: "Site",
  base: "https://old.example",
  email: "fixture@example.com",
  has_saved_password: true,
  state: "idle",
  message: "",
  synced_at: 123,
  targets: [],
};
const request = vi.mocked(controlRequest);
beforeEach(() => {
  request.mockReset();
  vi.mocked(browserHelperAvailable).mockResolvedValue(false);
  vi.mocked(loginWithBrowser).mockReset();
});

it.each([true, false])(
  "edits and saves the address only after backend confirmation (%s)",
  async (supported) => {
    const writes: { url: string; body: any }[] = [];
    request.mockImplementation(async (url, init) => {
      if (url.endsWith("/credentials")) return { password: "fixture-password" };
      writes.push({ url, body: JSON.parse(init?.body as string) });
      return supported
        ? { base: "https://new.example", authenticated: true }
        : { authenticated: true };
    });
    const saved = vi.fn();
    const user = userEvent.setup();
    render(
      <AccountForm
        initial={initial}
        close={vi.fn()}
        saved={saved}
        detectionModels={["gpt-6-astra"]}
      />,
    );
    const input = screen.getByLabelText("站点地址");
    await waitFor(() => expect(input).toBeEnabled());
    expect(input).not.toHaveAttribute("readonly");
    await user.clear(input);
    await user.type(input, "https://new.example");
    expect(screen.getByRole("note")).toHaveTextContent(
      "已有网关路由不会自动更改",
    );
    await user.click(screen.getByRole("button", { name: "连接并检测" }));
    await waitFor(() => expect(writes.length).toBeGreaterThan(0));
    expect(writes[0]).toEqual({
      url: "/v1/sub2api/accounts/account/login",
      body: {
        name: "Site",
        base: "https://new.example",
        previous_base: initial.base,
        email: initial.email,
        password: "fixture-password",
      },
    });
    if (supported) {
      await waitFor(() => expect(saved).toHaveBeenCalledOnce());
      expect(writes[1]).toEqual({
        url: "/v1/sub2api/accounts/account/sync-selected",
        body: { models: ["gpt-6-astra"] },
      });
    } else {
      expect(await screen.findByRole("alert")).toHaveTextContent(
        "后端尚未确认新站点地址",
      );
      expect(saved).not.toHaveBeenCalled();
      expect(writes).toHaveLength(1);
    }
  },
);

it("keeps a session-only import draft bound to its original address", async () => {
  request.mockResolvedValue({ password: "fixture-password" });
  render(
    <AccountForm
      initial={initial}
      sessionOnly
      close={vi.fn()}
      saved={vi.fn()}
    />,
  );
  await waitFor(() => expect(screen.getByLabelText("站点地址")).toBeEnabled());
  expect(screen.getByLabelText("站点地址")).toHaveAttribute("readonly");
});

it.each(["totp", "browser"])(
  "retains the address change through %s verification",
  async (flow) => {
    const writes: any[] = [];
    request.mockImplementation(async (url, init) => {
      if (url.endsWith("/credentials")) return { password: "fixture-password" };
      if (url.endsWith("/sync-selected")) return { queued: true };
      writes.push(JSON.parse(init?.body as string));
      if (writes.length === 1)
        return flow === "totp"
          ? { requires_2fa: true, challenge: "encrypted-challenge" }
          : {
              requires_browser: true,
              browser_kind: "newapi",
              browser_base: "https://new.example",
            };
      return { base: "https://new.example", authenticated: true };
    });
    vi.mocked(browserHelperAvailable).mockResolvedValue(true);
    vi.mocked(loginWithBrowser).mockResolvedValue({
      access_token: "fresh-access",
      refresh_token: "fresh-refresh",
    });
    const saved = vi.fn();
    const user = userEvent.setup();
    render(
      <AccountForm
        initial={initial}
        close={vi.fn()}
        saved={saved}
        detectionModels={["gpt-6-astra"]}
      />,
    );
    const input = screen.getByLabelText("站点地址");
    await waitFor(() => expect(input).toBeEnabled());
    await user.clear(input);
    await user.type(input, "https://new.example");
    await user.click(screen.getByRole("button", { name: "连接并检测" }));
    if (flow === "totp") {
      await user.type(await screen.findByLabelText("六位验证码"), "123456");
      await user.click(screen.getByRole("button", { name: "验证并检测" }));
      expect(writes[1]).toEqual({
        challenge: "encrypted-challenge",
        totp_code: "123456",
      });
    } else {
      await user.click(
        await screen.findByRole("button", { name: "使用浏览器登录" }),
      );
      expect(writes[1]).toMatchObject({
        base: "https://new.example",
        previous_base: initial.base,
        access_token: "fresh-access",
      });
    }
    await waitFor(() => expect(saved).toHaveBeenCalledOnce());
  },
);

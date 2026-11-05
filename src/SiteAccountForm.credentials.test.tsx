import { expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AccountForm } from "./SiteAccountForm";
import type { SubAccount } from "./Sub2apiChecks";

const account = {
  id: "owned-account",
  name: "Saved site",
  base: "https://site.test",
  email: "email@site.test",
  login_name: "saved-user",
  has_saved_password: true,
} as SubAccount;

it("waits for the password before enabling login and aborts the read when closed", async () => {
  let finish!: (value: Response) => void;
  const fetch = vi.fn(
    (_url: string, _init?: RequestInit) =>
      new Promise<Response>((resolve) => {
        finish = resolve;
      }),
  );
  vi.stubGlobal("fetch", fetch);
  const view = render(
    <AccountForm initial={account} close={() => {}} saved={() => {}} />,
  );
  expect(screen.getByLabelText("用户名 / 邮箱")).toHaveValue("saved-user");
  expect(screen.getByRole("button", { name: "连接并检测" })).toBeDisabled();
  expect(screen.getByLabelText("账号密码")).toBeDisabled();
  expect(fetch.mock.calls[0][0]).toContain("/owned-account/credentials");
  expect(fetch.mock.calls[0][1]?.cache).toBe("no-store");
  const signal = fetch.mock.calls[0][1]?.signal;
  view.unmount();
  expect(signal?.aborted).toBe(true);
  finish(Response.json({ password: "late-password" }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
});

it("allows server-side password reuse if the display request fails", async () => {
  const bodies: unknown[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        bodies.push(JSON.parse(String(init.body)));
        return Response.json({ authenticated: true });
      }
      return new Response("temporarily unavailable", { status: 503 });
    }),
  );
  const saved = vi.fn();
  const user = userEvent.setup();
  render(
    <AccountForm
      initial={account}
      sessionOnly
      close={() => {}}
      saved={saved}
    />,
  );
  expect(await screen.findByText(/已保存密码暂时无法显示/)).toBeVisible();
  expect(screen.getByLabelText("账号密码")).toHaveValue("");
  await user.click(screen.getByRole("button", { name: "登录并返回" }));
  await waitFor(() => expect(saved).toHaveBeenCalledOnce());
  expect(bodies).toEqual([
    { name: "Saved site", base: "https://site.test", email: "saved-user" },
  ]);
});

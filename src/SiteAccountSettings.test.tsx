import { afterEach, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SiteAccountSettings } from "./SiteAccountSettings";
import type { SubAccount } from "./Sub2apiChecks";

afterEach(() => vi.unstubAllGlobals());
const account = {
  id: "account/a",
  name: "站点一",
  targets: [],
} as unknown as SubAccount;

it("defaults to one and saves only the coefficient for this account", async () => {
  const fetch = vi.fn(async () => Response.json({ rate_coefficient: 0.1 }));
  vi.stubGlobal("fetch", fetch);
  const saved = vi.fn(),
    close = vi.fn(),
    user = userEvent.setup();
  render(<SiteAccountSettings account={account} close={close} saved={saved} />);
  const input = screen.getByRole("spinbutton", { name: "倍率系数" });
  expect(input).toHaveValue(1);
  await user.clear(input);
  await user.type(input, "0.1");
  await user.click(screen.getByRole("button", { name: "保存设置" }));
  await waitFor(() => expect(saved).toHaveBeenCalledOnce());
  expect(fetch).toHaveBeenCalledOnce();
  const [url, options] = fetch.mock.calls[0] as unknown as [
    string,
    RequestInit,
  ];
  expect(url).toContain("/sub2api/accounts/account%2Fa/settings");
  expect(options.method).toBe("PATCH");
  expect(JSON.parse(options.body as string)).toEqual({ rate_coefficient: 0.1 });
  expect(close).not.toHaveBeenCalled();
});

it("blocks invalid values and retains the form on save failure without retrying a mutation", async () => {
  const fetch = vi.fn(
    async () => new Response("temporary unavailable", { status: 503 }),
  );
  vi.stubGlobal("fetch", fetch);
  const saved = vi.fn(),
    user = userEvent.setup();
  render(
    <SiteAccountSettings
      account={{ ...account, rate_coefficient: 0.25 }}
      close={vi.fn()}
      saved={saved}
    />,
  );
  const input = screen.getByRole("spinbutton", { name: "倍率系数" });
  expect(input).toHaveValue(0.25);
  for (const value of ["", "0", "-1", "1000001"]) {
    await user.clear(input);
    if (value) await user.type(input, value);
    expect(screen.getByRole("button", { name: "保存设置" })).toBeDisabled();
  }
  expect(fetch).not.toHaveBeenCalled();
  await user.clear(input);
  await user.type(input, "0.1");
  await user.click(screen.getByRole("button", { name: "保存设置" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "temporary unavailable",
  );
  expect(input).toHaveValue(0.1);
  expect(saved).not.toHaveBeenCalled();
  expect(fetch).toHaveBeenCalledOnce();
});

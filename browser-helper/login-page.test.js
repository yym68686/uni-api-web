import { beforeEach, expect, it, vi } from "vitest";
import { loginPageStep } from "./login-page.js";
beforeEach(() => {
  localStorage.clear();
  document.body.innerHTML = "";
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify({ data: { email: "me@example.com" } })),
    ),
  );
});
const input = () => ({
  origin: location.origin,
  base: location.origin,
  email: "me@example.com",
  password: "private-password",
  agreed: true,
});
it("fills the native form, waits for verification and submits only once", async () => {
  document.body.innerHTML =
    '<form><input id="email"><input id="password" type="password"><button type="submit" disabled>登录</button></form>';
  const submit = vi.fn((e) => e.preventDefault());
  document.querySelector("form").addEventListener("submit", submit);
  const change = vi.fn();
  document.querySelector("#email").addEventListener("input", change);
  expect(await loginPageStep(input())).toEqual({ filled: true });
  expect(document.querySelector("#password").value).toBe("private-password");
  expect(change).toHaveBeenCalledOnce();
  await loginPageStep({ ...input(), password: "" });
  expect(submit).not.toHaveBeenCalled();
  document.querySelector("button").disabled = false;
  await loginPageStep({ ...input(), password: "" });
  await loginPageStep({ ...input(), password: "" });
  expect(submit).toHaveBeenCalledOnce();
});
it("returns only the matching verified site session and rejects cross-origin navigation or another user", async () => {
  localStorage.setItem("auth_token", "session");
  localStorage.setItem("refresh_token", "refresh");
  localStorage.setItem(
    "auth_user",
    JSON.stringify({ email: "me@example.com" }),
  );
  expect(await loginPageStep(input())).toEqual({
    auth: { access_token: "session", refresh_token: "refresh" },
  });
  expect(
    (await loginPageStep({ ...input(), origin: "https://other.example" }))
      .error,
  ).toContain("其他域名");
  expect(
    (await loginPageStep({ ...input(), email: "foreign@example.com" })).error,
  ).toContain("其他账号");
});
it("does not accept site terms without consent", async () => {
  document.body.innerHTML =
    '<form><input id="email" disabled><input id="password"><input id="login-agreement-consent" type="checkbox"><button type="submit">登录</button></form>';
  expect((await loginPageStep({ ...input(), agreed: false })).error).toContain(
    "协议",
  );
  expect(document.querySelector("#login-agreement-consent").checked).toBe(
    false,
  );
  await loginPageStep(input());
  expect(document.querySelector("#login-agreement-consent").checked).toBe(true);
});
it("does not return an expired or rejected session", async () => {
  localStorage.setItem("auth_token", "expired");
  localStorage.setItem(
    "auth_user",
    JSON.stringify({ email: "me@example.com" }),
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response("{}", { status: 401 })),
  );
  expect((await loginPageStep(input())).auth).toBeUndefined();
});

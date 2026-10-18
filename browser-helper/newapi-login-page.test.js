import { beforeEach, expect, it, vi } from "vitest";
import { newAPILoginPageStep } from "./newapi-login-page.js";
beforeEach(() => {
  localStorage.clear();
  document.body.innerHTML = "";
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response("{}", { status: 401 })),
  );
});
const input = () => ({
  origin: location.origin,
  base: location.origin,
  email: "me@example.com",
  password: "private-password",
});
it("refreshes and verifies an already signed-in New API browser session", async () => {
  document.body.innerHTML = "";
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url) => {
      if (String(url).endsWith("/api/user/auth/refresh"))
        return new Response(
          JSON.stringify({
            success: true,
            data: {
              access_token: "newapi-access",
              access_expires_at: 1234,
              session: { sid: "sid-1" },
              user: { id: 42, username: "me", email: "me@example.com" },
            },
          }),
        );
      return new Response(
        JSON.stringify({
          success: true,
          data: { id: 42, username: "me", email: "me@example.com" },
        }),
      );
    }),
  );
  expect(await newAPILoginPageStep({ ...input(), hasRefresh: true })).toEqual({
    auth: {
      access_token: "newapi-access",
      refresh_token: "",
      browser_kind: "newapi",
      browser_user_id: 42,
      browser_username: "me",
      browser_session_id: "sid-1",
      browser_access_expires_at: 1234,
    },
  });
  expect(
    fetch.mock.calls.map(([url]) => new URL(String(url)).pathname),
  ).toEqual(["/api/user/auth/refresh", "/api/user/self"]);
});

it("fills New API login but leaves verification and submit to the user", async () => {
  document.body.innerHTML =
    '<form><input name="username"><input name="password" type="password"><button type="submit">Sign in</button></form>';
  const submit = vi.fn((e) => e.preventDefault());
  document.querySelector("form").addEventListener("submit", submit);
  expect(await newAPILoginPageStep({ ...input(), hasRefresh: true })).toEqual({
    filled: true,
  });
  expect(document.querySelector('[name="username"]').value).toBe(
    "me@example.com",
  );
  expect(document.querySelector('[name="password"]').value).toBe(
    "private-password",
  );
  expect(
    await newAPILoginPageStep({ ...input(), kind: "newapi", password: "" }),
  ).toEqual({ waiting: true, filled: true });
  expect(submit).not.toHaveBeenCalled();
});

it("reads legacy session identity without calling refresh and rejects other accounts", async () => {
  localStorage.setItem(
    "user",
    JSON.stringify({ id: 42, username: "me", email: "me@example.com" }),
  );
  fetch.mockResolvedValue(
    new Response(
      JSON.stringify({
        success: true,
        data: { id: 42, username: "me", email: "me@example.com" },
      }),
    ),
  );
  const result = await newAPILoginPageStep({ ...input(), hasSession: true });
  expect(result.auth).toMatchObject({
    access_token: "",
    browser_kind: "newapi",
    browser_user_id: 42,
    browser_username: "me",
  });
  expect(fetch).toHaveBeenCalledExactlyOnceWith(
    location.origin + "/api/user/self",
    expect.objectContaining({
      credentials: "include",
      headers: { Accept: "application/json", "New-Api-User": "42" },
    }),
  );
  fetch.mockClear();
  expect(
    (
      await newAPILoginPageStep({
        ...input(),
        email: "another@example.com",
        hasSession: true,
      })
    ).error,
  ).toContain("其他账号");
  expect(fetch).not.toHaveBeenCalled();
});
it("rejects verified identity changes, cross-origin navigation and never returns failed sessions", async () => {
  expect(
    (
      await newAPILoginPageStep({
        ...input(),
        origin: "https://different.test",
      })
    ).error,
  ).toContain("其他域名");
  expect(fetch).not.toHaveBeenCalled();
  expect(
    (await newAPILoginPageStep({ ...input(), hasRefresh: true })).auth,
  ).toBeUndefined();
  fetch.mockImplementation(
    async (url) =>
      new Response(
        JSON.stringify({
          success: true,
          data: String(url).endsWith("/refresh")
            ? {
                access_token: "access",
                user: { id: 42, username: "me", email: "me@example.com" },
              }
            : { id: 43, username: "me", email: "me@example.com" },
        }),
      ),
  );
  expect(
    (await newAPILoginPageStep({ ...input(), hasRefresh: true })).error,
  ).toContain("身份不一致");
});
it("works after serialization into the isolated extension world without module closures", async () => {
  const injected = Function(
    "return (" + newAPILoginPageStep.toString() + ")",
  )();
  fetch.mockResolvedValue(
    new Response(
      JSON.stringify({
        success: true,
        data: { id: 42, username: "me", email: "me@example.com" },
      }),
    ),
  );
  expect(
    (await injected({ ...input(), hasSession: true })).auth.browser_user_id,
  ).toBe(42);
});

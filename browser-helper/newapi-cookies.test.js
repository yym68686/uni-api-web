import { expect, it, vi, afterEach } from "vitest";
import { newAPISessionCookies } from "./newapi-cookies.js";
afterEach(() => vi.unstubAllGlobals());
it("reads only session cookies for the exact authorized tab, origin and store", async () => {
  const get = vi.fn(async ({ name }) => ({
    name,
    value: name + "-value",
    domain: "site.test",
  }));
  vi.stubGlobal("chrome", {
    cookies: {
      getAllCookieStores: async () => [
        { id: "private", tabIds: [7] },
        { id: "other", tabIds: [1] },
      ],
      get,
    },
  });
  expect(
    await newAPISessionCookies("https://site.test", {
      id: 7,
      url: "https://site.test/dashboard",
    }),
  ).toEqual({
    session: "session-value",
    new_api_refresh: "new_api_refresh-value",
  });
  expect(get.mock.calls.map(([v]) => v)).toEqual([
    {
      name: "session",
      url: "https://site.test/api/user/self",
      storeId: "private",
    },
    {
      name: "new_api_refresh",
      url: "https://site.test/api/user/auth/refresh",
      storeId: "private",
    },
  ]);
  await expect(
    newAPISessionCookies("https://site.test", {
      id: 7,
      url: "https://other.test",
    }),
  ).rejects.toThrow("其他域名");
  expect(get).toHaveBeenCalledTimes(2);
});
it("does not export parent domain, partitioned or oversized cookies", async () => {
  vi.stubGlobal("chrome", {
    cookies: {
      getAllCookieStores: async () => [{ id: "s", tabIds: [7] }],
      get: vi.fn(async ({ name }) =>
        name === "session"
          ? { value: "parent", domain: ".site.test" }
          : {
              value: "partitioned",
              domain: "sub.site.test",
              partitionKey: { topLevelSite: "https://elsewhere.test" },
            },
      ),
    },
  });
  expect(
    await newAPISessionCookies("https://sub.site.test", {
      id: 7,
      url: "https://sub.site.test",
    }),
  ).toEqual({});
});

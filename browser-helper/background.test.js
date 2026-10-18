import { it, expect, vi, afterEach } from "vitest";
afterEach(() => vi.unstubAllGlobals());
it("opens the target login immediately with default consent and returns only to its initiating console", async () => {
  vi.resetModules();
  let handler;
  const tabs = {
    create: vi.fn(async () => ({ id: 42 })),
    remove: vi.fn(async () => {}),
    update: vi.fn(async () => {}),
  };
  const executeScript = vi.fn(async () => [
    { result: { auth: { access_token: "session", refresh_token: "refresh" } } },
  ]);
  vi.stubGlobal("chrome", {
    runtime: { onMessage: { addListener: (fn) => (handler = fn) } },
    tabs,
    scripting: { executeScript },
    action: { onClicked: { addListener: vi.fn() } },
  });
  await import("./background.js");
  const input = {
    base: "https://mxamaxai.com",
    email: "test@example.com",
    password: "password",
  };
  const reply = vi.fn();
  handler(
    { type: "login", id: "bad", input },
    { origin: "https://other.example", frameId: 0, tab: { id: 1 } },
    reply,
  );
  expect(tabs.create).not.toHaveBeenCalled();
  handler(
    { type: "login", id: "test", input },
    { origin: "https://uni-api-console.fugue.pro", frameId: 0, tab: { id: 1 } },
    reply,
  );
  await vi.waitFor(() =>
    expect(reply).toHaveBeenCalledWith({
      auth: { access_token: "session", refresh_token: "refresh" },
    }),
  );
  expect(tabs.create).toHaveBeenCalledExactlyOnceWith({
    url: "https://mxamaxai.com/login",
    active: true,
  });
  expect(executeScript.mock.calls[0][0].args[0]).toMatchObject({
    origin: "https://mxamaxai.com",
    agreed: true,
  });
  expect(tabs.update).toHaveBeenCalledWith(1, { active: true });
  expect(input.password).toBe("");
});

it.each([true, false])(
  "exports only the newly verified New API session with explicit cookies permission (%s)",
  async (allowed) => {
    vi.resetModules();
    let handler;
    const cookies = { session: "fixture-session" };
    const get = vi.fn(async () => ({
      name: "session",
      value: "fixture-session",
      domain: "site.test",
    }));
    const tabs = {
      create: vi.fn(async () => ({ id: 42 })),
      get: vi.fn(async () => ({ id: 42, url: "https://site.test/dashboard" })),
      remove: vi.fn(async () => {}),
      update: vi.fn(async () => {}),
    };
    const executeScript = vi.fn(async () => [
      {
        result: {
          auth: {
            browser_kind: "newapi",
            access_token: "",
            browser_user_id: 7,
            browser_username: "me",
          },
        },
      },
    ]);
    vi.stubGlobal("chrome", {
      runtime: { onMessage: { addListener: (fn) => (handler = fn) } },
      permissions: { contains: async () => allowed },
      cookies: {
        getAllCookieStores: async () => [{ id: "s", tabIds: [42] }],
        get: vi.fn(async ({ name }) =>
          name === "session"
            ? { value: "fixture-session", domain: "site.test" }
            : null,
        ),
      },
      tabs,
      scripting: { executeScript },
    });
    await import("./background.js");
    const reply = vi.fn();
    handler(
      {
        type: "login",
        id: "newapi",
        input: {
          base: "https://site.test",
          email: "me",
          password: "",
          kind: "newapi",
        },
      },
      {
        origin: "https://uni-api-console.fugue.pro",
        frameId: 0,
        tab: { id: 1 },
      },
      reply,
    );
    await vi.waitFor(() => expect(reply).toHaveBeenCalledOnce());
    if (allowed) {
      expect(tabs.create).toHaveBeenCalledWith({
        url: "https://site.test/sign-in",
        active: true,
      });
      expect(executeScript.mock.calls[0][0].args[0]).toMatchObject({
        hasSession: true,
        hasRefresh: false,
      });
      expect(reply.mock.calls[0][0].auth.browser_cookies).toEqual(cookies);
    } else {
      expect(tabs.create).not.toHaveBeenCalled();
      expect(reply.mock.calls[0][0].error).toContain("授权");
    }
  },
);
it("cancels New API login without returning a session to a different or canceled tab", async () => {
  vi.resetModules();
  let handler, complete;
  const tabs = {
    create: vi.fn(async () => ({ id: 42 })),
    get: vi.fn(async () => ({ id: 42, url: "https://site.test/sign-in" })),
    remove: vi.fn(async () => {}),
    update: vi.fn(async () => {}),
  };
  const executeScript = vi.fn(
    () =>
      new Promise((resolve) => {
        complete = resolve;
      }),
  );
  vi.stubGlobal("chrome", {
    runtime: { onMessage: { addListener: (fn) => (handler = fn) } },
    permissions: { contains: async () => true },
    cookies: {
      getAllCookieStores: async () => [{ id: "s", tabIds: [42] }],
      get: async () => null,
    },
    tabs,
    scripting: { executeScript },
  });
  await import("./background.js");
  const reply = vi.fn();
  const sender = {
    origin: "https://uni-api-console.fugue.pro",
    frameId: 0,
    tab: { id: 1 },
  };
  handler(
    {
      type: "login",
      id: "cancel",
      input: {
        base: "https://site.test",
        email: "me",
        password: "private",
        kind: "newapi",
      },
    },
    sender,
    reply,
  );
  await vi.waitFor(() => expect(executeScript).toHaveBeenCalledOnce());
  handler(
    { type: "cancel", id: "cancel" },
    { ...sender, tab: { id: 2 } },
    vi.fn(),
  );
  expect(tabs.remove).not.toHaveBeenCalled();
  handler({ type: "cancel", id: "cancel" }, sender, vi.fn());
  complete([
    {
      result: {
        auth: { browser_kind: "newapi", access_token: "must-not-return" },
      },
    },
  ]);
  await vi.waitFor(() => expect(reply).toHaveBeenCalledOnce());
  expect(reply.mock.calls[0][0].error).toContain("已取消");
  expect(reply.mock.calls[0][0].auth).toBeUndefined();
});

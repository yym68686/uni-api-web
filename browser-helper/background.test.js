import { it, expect, vi, afterEach } from "vitest";
afterEach(() => vi.unstubAllGlobals());
it.each(["chrome", "browser"])(
  "waits for the live tab URL instead of a stale about:blank snapshot (%s)",
  async (namespace) => {
    vi.resetModules();
    vi.useFakeTimers();
    try {
      let handler;
      const tabs = {
        create: vi.fn(async () => ({ id: 42, url: "about:blank" })),
        get: vi
          .fn()
          .mockResolvedValueOnce({ id: 42, url: "about:blank" })
          .mockResolvedValue({ id: 42, url: "https://site.test/login" }),
        remove: vi.fn(async () => {}),
        update: vi.fn(async () => {}),
      };
      const executeScript = vi.fn(async () => [
        { result: { auth: { access_token: "verified" } } },
      ]);
      vi.stubGlobal(namespace, {
        runtime: {
          onMessage: {
            addListener: (fn) => {
              handler = fn;
            },
          },
        },
        tabs,
        scripting: { executeScript },
      });
      await import("./background.js");
      const reply = vi.fn();
      expect(
        handler(
          {
            type: "login",
            id: "navigation",
            input: {
              base: "https://site.test",
              email: "me",
              password: "password",
            },
          },
          {
            url: "https://uni-api-console.fugue.pro",
            frameId: 0,
            tab: { id: 1 },
          },
          reply,
        ),
      ).toBe(true);
      await vi.advanceTimersByTimeAsync(0);
      expect(executeScript).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(1000);
      expect(reply).toHaveBeenCalledExactlyOnceWith({
        auth: { access_token: "verified" },
      });
      expect(executeScript).toHaveBeenCalledOnce();
      expect(tabs.get).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  },
);
it.each(["chrome", "browser"])(
  "opens the target login immediately and returns only to its initiating console (%s)",
  async (namespace) => {
    vi.resetModules();
    let handler;
    const tabs = {
      create: vi.fn(async () => ({ id: 42 })),
      get: vi.fn(async () => ({ id: 42, url: "https://mxamaxai.com/login" })),
      remove: vi.fn(async () => {}),
      update: vi.fn(async () => {}),
    };
    const executeScript = vi.fn(async () => [
      {
        result: { auth: { access_token: "session", refresh_token: "refresh" } },
      },
    ]);
    vi.stubGlobal(namespace, {
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
      {
        origin: "https://uni-api-console.fugue.pro",
        frameId: 0,
        tab: { id: 1, cookieStoreId: "firefox-default" },
      },
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
  },
);

it.each(
  ["chrome", "browser"].flatMap((namespace) =>
    [true, false].map((allowed) => ({ namespace, allowed })),
  ),
)(
  "exports only the newly verified New API session ($namespace, cookies permission: $allowed)",
  async ({ namespace, allowed }) => {
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
    vi.stubGlobal(namespace, {
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
it.each(["chrome", "browser"])(
  "cancels New API login without returning a session to a different or canceled tab (%s)",
  async (namespace) => {
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
    vi.stubGlobal(namespace, {
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
  },
);

it("accepts Firefox's URL-only sender, prefers browser promises and preserves the container", async () => {
  vi.resetModules();
  let handler;
  const tabs = {
    create: vi.fn(async () => ({ id: 42 })),
    get: vi.fn(async () => ({ id: 42, url: "https://site.test/dashboard" })),
    remove: vi.fn(async () => {}),
    update: vi.fn(async () => {}),
  };
  const getCookie = vi.fn(async ({ name }) =>
    name === "session"
      ? { value: "container-session", domain: "site.test" }
      : null,
  );
  const chromeListener = vi.fn();
  vi.stubGlobal("chrome", {
    runtime: { onMessage: { addListener: chromeListener } },
  });
  vi.stubGlobal("browser", {
    runtime: { onMessage: { addListener: (fn) => (handler = fn) } },
    permissions: { contains: async () => true },
    cookies: {
      getAllCookieStores: async () => [
        { id: "firefox-default", tabIds: [2] },
        { id: "firefox-container-3", tabIds: [1, 42] },
      ],
      get: getCookie,
    },
    tabs,
    scripting: {
      executeScript: async () => [
        { result: { auth: { browser_kind: "newapi" } } },
      ],
    },
  });
  await import("./background.js");
  const reply = vi.fn();
  const input = {
    base: "https://site.test",
    email: "me",
    password: "",
    kind: "newapi",
  };
  expect(
    handler(
      { type: "login", id: "firefox", input },
      {
        url: "https://uni-api-console.fugue.pro/channels",
        frameId: 0,
        tab: { id: 1, windowId: 5, cookieStoreId: "firefox-container-3" },
      },
      reply,
    ),
  ).toBe(true);
  await vi.waitFor(() => expect(reply).toHaveBeenCalledOnce());
  expect(reply.mock.calls[0][0].auth.browser_cookies).toEqual({
    session: "container-session",
  });
  expect(tabs.create).toHaveBeenCalledExactlyOnceWith({
    url: "https://site.test/sign-in",
    active: true,
    windowId: 5,
    cookieStoreId: "firefox-container-3",
  });
  expect(
    getCookie.mock.calls.every(
      ([details]) => details.storeId === "firefox-container-3",
    ),
  ).toBe(true);
  expect(chromeListener).not.toHaveBeenCalled();
});

it.each([
  { url: "https://other.test", frameId: 0, tab: { id: 1 } },
  {
    url: "https://uni-api-console.fugue.pro.evil.test",
    frameId: 0,
    tab: { id: 1 },
  },
  {
    url: "https://uni-api-console.fugue.pro",
    origin: "null",
    frameId: 0,
    tab: { id: 1 },
  },
  {
    url: "https://other.test",
    origin: "https://uni-api-console.fugue.pro",
    frameId: 0,
    tab: { id: 1 },
  },
  { url: "not a URL", frameId: 0, tab: { id: 1 } },
  { url: "https://uni-api-console.fugue.pro", frameId: 1, tab: { id: 1 } },
  { frameId: 0, tab: { id: 1 } },
  { origin: "https://uni-api-console.fugue.pro", frameId: 0 },
])("rejects untrusted or incomplete senders: %j", async (sender) => {
  vi.resetModules();
  let handler;
  const create = vi.fn();
  vi.stubGlobal("browser", {
    runtime: {
      onMessage: {
        addListener: (fn) => {
          handler = fn;
        },
      },
    },
    tabs: { create },
  });
  await import("./background.js");
  const reply = vi.fn();
  handler(
    {
      type: "login",
      id: "untrusted",
      input: { base: "https://site.test", email: "me", password: "secret" },
    },
    sender,
    reply,
  );
  expect(create).not.toHaveBeenCalled();
  expect(reply).not.toHaveBeenCalled();
});

it.each([undefined, null, {}, { base: 123 }])(
  "rejects malformed inputs without crashing: %j",
  async (input) => {
    vi.resetModules();
    let handler;
    vi.stubGlobal("browser", {
      runtime: {
        onMessage: {
          addListener: (fn) => {
            handler = fn;
          },
        },
      },
    });
    await import("./background.js");
    const reply = vi.fn();
    handler(
      { type: "login", id: "invalid", input },
      { url: "https://uni-api-console.fugue.pro", frameId: 0, tab: { id: 1 } },
      reply,
    );
    expect(reply).toHaveBeenCalledExactlyOnceWith({ error: "站点地址无效" });
  },
);

it("supports Firefox senders which expose only their exact console URL", async () => {
  vi.resetModules();
  let handler;
  const tabs = {
    create: vi.fn(async () => ({ id: 42 })),
    get: vi.fn(async () => ({ id: 42, url: "https://site.test/login" })),
    remove: vi.fn(async () => {}),
  };
  vi.stubGlobal("browser", {
    runtime: { onMessage: { addListener: (fn) => (handler = fn) } },
    tabs,
    scripting: {
      executeScript: async () => [
        { result: { auth: { access_token: "verified" } } },
      ],
    },
  });
  await import("./background.js");
  const reply = vi.fn();
  handler(
    {
      type: "login",
      id: "url-only",
      input: { base: "https://site.test", email: "me", password: "password" },
    },
    {
      origin: "https://uni-api-console.fugue.pro",
      url: "https://uni-api-console.fugue.pro/",
      frameId: undefined,
    },
    reply,
  );
  await vi.waitFor(() =>
    expect(reply).toHaveBeenCalledExactlyOnceWith({
      auth: { access_token: "verified" },
    }),
  );
  expect(tabs.create).toHaveBeenCalledExactlyOnceWith({
    url: "https://site.test/login",
    active: true,
  });
});

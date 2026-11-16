import { afterEach, expect, it, vi } from "vitest";

afterEach(() => vi.unstubAllGlobals());

it.each(["chrome", "browser"])(
  "bridges only same-window console messages using %s promises",
  async (namespace) => {
    vi.resetModules();
    let onMessage;
    const postMessage = vi.fn();
    const page = {
      addEventListener: (_event, fn) => {
        onMessage = fn;
      },
      postMessage,
    };
    page.top = page;
    vi.stubGlobal("window", page);
    vi.stubGlobal("location", { origin: "https://uni-api-console.fugue.pro" });
    const sendMessage = vi.fn(async ({ type }) =>
      type === "login"
        ? { auth: { access_token: "verified-session" } }
        : undefined,
    );
    vi.stubGlobal("chrome", {
      runtime: {
        sendMessage:
          namespace === "chrome"
            ? sendMessage
            : vi.fn(() => {
                throw Error("callback API used");
              }),
      },
    });
    if (namespace === "browser")
      vi.stubGlobal("browser", { runtime: { sendMessage } });
    await import("./bridge.js");
    const data = {
      channel: "uni-api-browser-login-v1",
      id: "fixture",
      type: "ping",
    };
    const event = { source: page, origin: location.origin, data };
    await onMessage({ ...event, source: {} });
    await onMessage({ ...event, origin: "https://other.test" });
    expect(postMessage).not.toHaveBeenCalled();
    await onMessage(event);
    expect(postMessage).toHaveBeenCalledWith(
      { ...data, type: "ready", capabilities: ["sub2api", "newapi"] },
      location.origin,
    );
    const input = {
      base: "https://site.test",
      email: "me",
      password: "private",
    };
    await onMessage({ ...event, data: { ...data, type: "login", input } });
    expect(sendMessage).toHaveBeenCalledWith({
      type: "login",
      id: "fixture",
      input,
    });
    expect(postMessage).toHaveBeenLastCalledWith(
      { ...data, type: "result", auth: { access_token: "verified-session" } },
      location.origin,
    );
    sendMessage.mockRejectedValueOnce(Error("extension reloaded"));
    await onMessage({ ...event, data: { ...data, type: "login", input } });
    expect(postMessage.mock.lastCall[0].error).toContain("已断开");
    // Cancellation after a reload must not leave an unhandled rejection.
    sendMessage.mockRejectedValueOnce(Error("extension reloaded"));
    await onMessage({ ...event, data: { ...data, type: "cancel" } });
  },
);

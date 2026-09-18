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

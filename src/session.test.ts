import { describe, expect, it, vi } from "vitest";
import { clearConnection, loadConnection, saveConnection } from "./session";

describe("tab connection session", () => {
  it("restores only a normalized credential pair with a fresh query-cache identity", () => {
    saveConnection({
      base: "https://mock.example/v1/",
      key: " platform-secret ",
      session: "old",
    });
    const first = loadConnection()!;
    const second = loadConnection()!;
    expect(first).toMatchObject({
      base: "https://mock.example",
      key: "platform-secret",
    });
    expect(first.session).not.toBe("old");
    expect(second.session).not.toBe(first.session);
    expect(JSON.stringify(localStorage)).not.toContain("platform-secret");
    clearConnection();
    expect(loadConnection()).toBeNull();
  });
  it.each([
    "not json",
    "null",
    JSON.stringify({ base: "javascript:alert(1)", key: "secret" }),
    JSON.stringify({ base: "https://example.com?key=secret", key: "secret" }),
    JSON.stringify({ base: "https://example.com", key: 123 }),
  ])("ignores invalid stored connections: %s", (saved) => {
    sessionStorage.setItem("uni-console-connection:v1", saved);
    expect(loadConnection()).toBeNull();
  });
  it("keeps manual login available when session storage is unavailable", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(loadConnection()).toBeNull();
    expect(() =>
      saveConnection({
        base: "https://mock.example",
        key: "secret",
        session: "1",
      }),
    ).not.toThrow();
    expect(() => clearConnection()).not.toThrow();
  });
});

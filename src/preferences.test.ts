import { describe, expect, it, vi } from "vitest";
import { defaultFilters, loadFilters, saveFilters } from "./preferences";

describe("filter preferences", () => {
  const base = "https://mock.example";
  const key = `uni-console-filters:v1:${base}`;
  it("ignores corrupt data and validates individual fields from saved storage", () => {
    localStorage.setItem(key, "not-json");
    expect(loadFilters(base)).toEqual(defaultFilters);
    localStorage.setItem(
      key,
      JSON.stringify({
        model: "model-a",
        window: "forever",
        sort: null,
        search: [],
        balanceFilter: "unknown",
      }),
    );
    expect(loadFilters(base)).toEqual({ ...defaultFilters, model: "model-a" });
    saveFilters(base, {
      ...defaultFilters,
      connection: { key: "platform-secret" },
    } as typeof defaultFilters);
    expect(localStorage.getItem(key)).not.toContain("platform-secret");
  });
  it("continues with in-memory defaults when browser storage is blocked", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota exceeded");
    });
    expect(loadFilters(base)).toEqual(defaultFilters);
    expect(() => saveFilters(base, defaultFilters)).not.toThrow();
  });
});

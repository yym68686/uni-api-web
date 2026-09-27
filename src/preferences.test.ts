import { describe, expect, it, vi } from "vitest";
import {
  defaultFilters,
  loadFilters,
  saveFilters,
  loadView,
  saveView,
} from "./preferences";

describe("page preferences", () => {
  const base = "https://mock.example";
  it("ignores unknown and removed pages, and restricts account pages to account mode", () => {
    for (const value of ["invalid", "checks", "controls"]) {
      localStorage.setItem(`uni-console-view:v1:${base}`, value);
      expect(loadView(base, true)).toBe("channels");
    }
    for (const view of ["sources", "sub2api"] as const) {
      saveView(base, view);
      expect(loadView(base, true)).toBe(view);
      expect(loadView(base, false)).toBe("channels");
    }
    saveView(base, "balances");
    expect(loadView(base, false)).toBe("balances");
    expect(loadView("https://other.example", true)).toBe("channels");
  });
  it("keeps navigation usable when browser storage is blocked", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota exceeded");
    });
    expect(loadView(base, true)).toBe("channels");
    expect(() => saveView(base, "prices")).not.toThrow();
  });
});

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
        balanceTopN: "11",
        balanceThreshold: "15",
      }),
    );
    expect(loadFilters(base)).toEqual({ ...defaultFilters, model: "model-a" });
    saveFilters(base, {
      ...defaultFilters,
      connection: { key: "platform-secret" },
    } as typeof defaultFilters);
    expect(
      localStorage.getItem(`uni-console-filters:v2:channels:${base}`),
    ).not.toContain("platform-secret");
  });
  it("migrates shared preferences once and isolates page changes, resets and services", () => {
    const legacy = { ...defaultFilters, model: "model-a", window: "1h" };
    localStorage.setItem(key, JSON.stringify(legacy));
    expect(loadFilters(base, "channels")).toEqual(legacy);
    expect(loadFilters(base, "balances")).toEqual(legacy);
    saveFilters(base, legacy, "channels");
    saveFilters(
      base,
      { ...legacy, model: "model-b", window: "today", balanceTopN: "3" },
      "balances",
    );
    expect(loadFilters(base, "channels")).toEqual(legacy);
    expect(loadFilters(base, "balances")).toEqual({
      ...legacy,
      model: "model-b",
      window: "today",
      balanceTopN: "3",
    });
    saveFilters(base, defaultFilters, "channels");
    expect(loadFilters(base, "channels")).toEqual(defaultFilters);
    expect(loadFilters(base, "balances").model).toBe("model-b");
    expect(loadFilters("https://other.example", "balances")).toEqual(
      defaultFilters,
    );
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

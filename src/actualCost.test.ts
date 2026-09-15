import { describe, expect, it } from "vitest";
import { actualCostRange } from "./actualCost";

describe("actualCostRange", () => {
  const now = new Date("2026-09-16T12:00:00Z");

  it("does not present daily upstream totals as rolling-window totals", () => {
    expect(actualCostRange("1h", now)).toEqual({ supported: false });
    expect(actualCostRange("15m", now)).toEqual({ supported: false });
  });

  it("builds inclusive calendar-day ranges", () => {
    expect(actualCostRange("7d", now)).toEqual({
      supported: true,
      startDate: "2026-09-10",
      endDate: "2026-09-16",
    });
    expect(actualCostRange("month", now)).toEqual({
      supported: true,
      startDate: "2026-09-01",
      endDate: "2026-09-16",
    });
  });
});

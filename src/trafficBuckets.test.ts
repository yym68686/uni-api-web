import { expect, it } from "vitest";
import { trafficBuckets } from "./trafficBuckets";
import type { Channel } from "./types";

it("merges display periods without losing isolated failures or hiding missing data", () => {
  const points = Array.from({length:121}, (_, i) => ({
    timestamp: 60 * i, covered: true, success: i === 17 ? 0 : 1, failed: i === 17 ? 10 : 0,
  })).filter(point => point.timestamp !== 60 * 100);
  const chart = trafficBuckets([{points} as Channel], 60);
  expect(chart.seconds).toBe(120);
  expect(chart.points).toHaveLength(61);
  expect(chart.points.reduce((total, point) => total + point.failed,0)).toBe(10);
  expect(chart.points[8]).toMatchObject({ peak: 10, failed: 10, success: 1, covered: true });
  expect(chart.points[50]).toMatchObject({ covered: false, samples: 1 });
});

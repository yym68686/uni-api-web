import { describe, expect, it } from "vitest";
import { channelLatency, channelLatencyKind } from "./channelLatency";
import type { Stats } from "./types";
const distribution = (ms: number) => ({ sample_count: 2, p50_ms: ms, p95_ms: ms * 2, last_ms: ms, mean_ms: ms });
describe("channel latency clocks", () => {
  it("prefers successful creation events and ignores unscoped clocks", () => {
    const successful = distribution(5000);
    const legacy: Partial<Stats> = { response_created: distribution(20), first_output: distribution(10), first_text: distribution(30) };
    expect(channelLatency({ ...legacy, successful_response_created: successful, successful_first_text: distribution(6000) })).toBe(successful);
    expect(channelLatency(legacy)).toBeUndefined();
    expect(channelLatency()).toBeUndefined();
  });
  it("falls back to measured text on successful attempts when creation events are absent", () => {
    const text = distribution(0);
    const stats = { successful_response_created: { ...distribution(0), sample_count: 0, p50_ms: null }, successful_first_text: text };
    expect(channelLatencyKind(stats)).toBe("text");
    expect(channelLatency(stats)).toBe(text);
  });
  it("keeps measured zero", () => {
    expect(channelLatency({ successful_response_created: distribution(0) })?.p50_ms).toBe(0);
  });
});

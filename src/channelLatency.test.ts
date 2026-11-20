import { describe, expect, it } from "vitest";
import { channelLatency } from "./channelLatency";
import type { Stats } from "./types";
const distribution = (ms: number) => ({ sample_count: 2, p50_ms: ms, p95_ms: ms * 2, last_ms: ms, mean_ms: ms });
describe("channel latency clocks", () => {
  it("uses only the successful response-created distribution", () => {
    const successful = distribution(5000);
    const legacy: Partial<Stats> = { response_created: distribution(20), first_output: distribution(10) };
    expect(channelLatency({ ...legacy, successful_response_created: successful })).toBe(successful);
    expect(channelLatency(legacy)).toBeUndefined();
    expect(channelLatency()).toBeUndefined();
  });
  it("keeps measured zero", () => {
    expect(channelLatency({ successful_response_created: distribution(0) })?.p50_ms).toBe(0);
  });
});

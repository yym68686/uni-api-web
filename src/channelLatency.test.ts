import { describe, expect, it } from "vitest";
import { channelLatency } from "./channelLatency";
const distribution = (ms: number) => ({ sample_count: 2, p50_ms: ms, p95_ms: ms * 2, last_ms: ms, mean_ms: ms });
describe("channel latency clocks", () => {
  it("keeps response-created samples separate from native output samples", () => {
    const created = distribution(5000), output = distribution(45000);
    expect(channelLatency({ response_created: created, first_output: output })).toEqual({ value: created, kind: "created" });
    expect(channelLatency({ first_output: output })).toEqual({ value: output, kind: "output" });
    expect(channelLatency()).toEqual({ value: undefined, kind: "output" });
  });
  it("keeps measured zero and never invents a clock for success without samples", () => {
    expect(channelLatency({ response_created: distribution(0) }).kind).toBe("created");
    expect(channelLatency({ response_created: { ...distribution(0), sample_count: 0, p50_ms: null } }).value).toBeUndefined();
  });
});

import {expect, it} from "vitest";
import {encodeSummaryCache, decodeSummaryCache} from "./summaryCacheCodec";

it("retains a complete large snapshot that the raw 8 MB limit used to discard", async () => {
  const value = {version: "complete", rows: Array.from({length: 48}, (_, i) => ({
    id: String(i), value: {models: Array.from({length: 2500}, (_, m) => ({
      model: `model-${m}`, state: "done", result: {availability: {status: "success", ttft_ms: 152, usage: {status: "matched", input_price: 2, output_price: 10}}},
    }))},
  }))};
  expect(JSON.stringify(value).length).toBeGreaterThan(8_000_000);
  const encoded = await encodeSummaryCache(value);
  expect(encoded?.gzip).toBeInstanceOf(ArrayBuffer);
  expect(encoded!.gzip!.byteLength).toBeLessThan(8_000_000);
  expect(await decodeSummaryCache(encoded!)).toEqual(value);
});
it("keeps existing small records readable and rejects corrupt compressed bytes", async () => {
  const summary = {version: "v1", rows: [{id: "a", value: {state: "queued"}}]};
  expect(await decodeSummaryCache({summary})).toEqual(summary);
  await expect(decodeSummaryCache({gzip: new Uint8Array([1,2,3]).buffer})).rejects.toThrow();
});

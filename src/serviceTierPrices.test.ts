import { expect, it } from "vitest";
import { serviceTierPrice, tierPriceText } from "./serviceTierPrices";

it("shows only published tier prices with independent cash rates", () => {
  const now = Date.parse("2026-11-26");
  expect(tierPriceText(serviceTierPrice("gpt-6.2-sol", "fast", now)!.rate)).toBe("$4 / $20 / $0.2");
  expect(serviceTierPrice("gpt-6.1-terra", "fast", now)).toBeUndefined();
  expect(serviceTierPrice("gpt-6-luna", "ultrafast", now)).toBeUndefined();
  expect(serviceTierPrice("claude-opus-5-5", "ultrafast", now)).toBeUndefined();
  expect(serviceTierPrice("claude-opus-5-5", "fast", now)!.rate).toEqual([8,40,.4,10,16]);
});

it("transitions the GPT-6.2 Ultrafast promotion at Pacific midnight", () => {
  expect(serviceTierPrice("gpt-6.2-astra", "ultrafast", Date.parse("2027-03-01T07:59:59Z"))!.rate).toEqual([24,120,1.2,30,60]);
  expect(serviceTierPrice("gpt-6.2-astra", "ultrafast", Date.parse("2027-03-01T08:00:00Z"))!.rate).toEqual([48,240,2.4,60,120]);
});

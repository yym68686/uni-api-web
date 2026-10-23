import { afterEach, expect, it, vi } from "vitest";
import { analyticsRequest, controlRequest } from "./api";
import { readSummary } from "./summaryFeed";
import type { Connection } from "./types";
import { RequestTimeoutError } from "./requestDeadline";

afterEach(() => vi.restoreAllMocks());
for (const body of [false, true]) {
  it(`classifies deadline during ${body ? "body decoding" : "headers"} across read APIs`, async () => {
    const warning = vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.spyOn(globalThis, "fetch").mockImplementation(async (_url, init) => {
      expect(new Headers(init?.headers).get("X-Console-Request-ID")).toMatch(/^[\da-f-]{36}$/);
      if (!body) throw new DOMException("signal timed out", "TimeoutError");
      const response = Response.json({});
      vi.spyOn(response, "json").mockRejectedValue(new DOMException("signal timed out", "TimeoutError"));
      return response;
    });
    for (const run of [
      () => analyticsRequest({} as Connection, "/analytics/v1/analytics"),
      () => controlRequest("/v1/sub2api/channels"),
      () => readSummary("/v1/sub2api/accounts", undefined, new AbortController().signal),
    ]) {
      await expect(run()).rejects.toBeInstanceOf(RequestTimeoutError);
      await expect(run()).rejects.toThrow("数据读取超时");
    }
    for (const [event, detail] of warning.mock.calls) {
      expect(event).toBe("console_request_timeout");
      expect(detail).toEqual({
        request_id: expect.stringMatching(/^[\da-f-]{36}$/),
        phase: body ? "response_body" : "response_headers",
        elapsed_ms: expect.any(Number),
        headers_ms: body ? expect.any(Number) : null,
        body_ms: body ? expect.any(Number) : null,
      });
    }
    expect(warning).toHaveBeenCalledTimes(6);
  });
}
it("preserves cancellation and never retries an uncertain write", async () => {
  const warning = vi.spyOn(console, "warn").mockImplementation(() => {});
  const fetch = vi.spyOn(globalThis, "fetch").mockRejectedValue(new DOMException("signal timed out", "TimeoutError"));
  await expect(controlRequest("/v1/write", {method:"PATCH"})).rejects.toThrow("保存结果尚未确认");
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(JSON.stringify(warning.mock.calls)).not.toContain("/v1/write");
  warning.mockClear();
  const c = new AbortController(); c.abort();
  const reason = new DOMException("left page", "AbortError"); fetch.mockRejectedValueOnce(reason);
  await expect(controlRequest("/v1/read", {signal:c.signal})).rejects.toBe(reason);
  expect(warning).not.toHaveBeenCalled();
});

it("classifies an explicit per-operation timeout without treating it as navigation", async () => {
  const c = new AbortController();
  const timeout = new DOMException("signal timed out", "TimeoutError");
  c.abort(timeout);
  vi.spyOn(globalThis, "fetch").mockRejectedValue(timeout);
  await expect(controlRequest("/v1/read", {signal:c.signal})).rejects.toBeInstanceOf(RequestTimeoutError);
});

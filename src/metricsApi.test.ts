import { expect, it, vi } from "vitest";
import { AnalyticsInitializingError } from "./api";
import { readMetrics } from "./metricsApi";
import type { Connection } from "./types";
const connection: Connection = {
  base: location.origin,
  key: "",
  session: "user",
  account: true,
};
function mockAnalytics() {
  const fetch = vi.fn(
    async (_input: RequestInfo | URL, _init?: RequestInit) =>
      new Response(
        JSON.stringify({ data: [], total: { requests: 0 }, models: [] }),
      ),
  );
  vi.stubGlobal("fetch", fetch);
  return fetch;
}
it.each(["/v1/channel-metrics", "/v1/channel-metrics/timeseries"])(
  "scopes %s to the selected source and key",
  async (path) => {
    const fetch = mockAnalytics();
    await readMetrics(
      connection,
      path + "?api_key_id=primary%3A%3Akey-two&window=1h&model=gpt-6-astra",
      new AbortController().signal,
      "all",
      "all",
    );
    const url = new URL(String(fetch.mock.calls[0][0]));
    expect(url.searchParams.get("key_id")).toBe("key-two");
    expect(url.searchParams.get("source_id")).toBe("primary");
    expect(url.searchParams.has("api_key_id")).toBe(false);
    expect(url.searchParams.get("range")).toBe("1h");
    expect(url.searchParams.get("model")).toBe("gpt-6-astra");
    expect(url.searchParams.get("endpoint")).toBe("all");
    expect(url.searchParams.get("stream")).toBe("all");
    expect(url.searchParams.get("timeseries")).toBe(
      path.endsWith("timeseries") ? "true" : null,
    );
  },
);
it("keeps raw gateway key ids and allows all-key aggregation after clearing the selector", async () => {
  const fetch = mockAnalytics();
  for (const key of ["key-two", ""]) {
    await readMetrics(
      { ...connection, sourceId: "primary" },
      "/v1/channel-metrics?window=today&api_key_id=" + key,
      new AbortController().signal,
      "/v1/responses",
      "false",
    );
  }
  const selected = new URL(String(fetch.mock.calls[0][0])),
    all = new URL(String(fetch.mock.calls[1][0]));
  expect(selected.searchParams.get("key_id")).toBe("key-two");
  expect(all.searchParams.has("key_id")).toBe(false);
  expect(all.searchParams.get("source_id")).toBe("primary");
});
it("rejects keys belonging to another source before querying statistics", async () => {
  const fetch = mockAnalytics();
  await expect(
    readMetrics(
      { ...connection, sourceId: "digitalocean" },
      "/v1/channel-metrics?api_key_id=primary%3A%3Akey-two",
      new AbortController().signal,
      "all",
      "all",
    ),
  ).rejects.toThrow("不属于当前来源");
  expect(fetch).not.toHaveBeenCalled();
});

it("distinguishes incomplete analytics from other service failures", async () => {
  const fetch = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ code: "analytics_initializing" }), { status: 503 }))
    .mockResolvedValueOnce(new Response("proxy offline", { status: 503 }));
  vi.stubGlobal("fetch", fetch);
  const read = () => readMetrics(connection, "/v1/channel-metrics", new AbortController().signal, "all", "all");
  await expect(read()).rejects.toBeInstanceOf(AnalyticsInitializingError);
  await expect(read()).rejects.toThrow("分析服务暂时无法响应（HTTP 503）");
});

import { afterEach, expect, it, vi } from "vitest";
import { controlRequest } from "./api";
import { managementReadTrace, rememberManagementReadTrace } from "./managementReadTrace";

afterEach(() => vi.restoreAllMocks());
const init = { cache: "no-cache", headers: { "X-Console-Current-Validation": "1" } } satisfies RequestInit;
const path = "/v1/channel-management/query?filters=private-filter";

it.each([false, true])("records headers/body timing without changing the payload (attested=%s)", async attested => {
  let clock = 10, requestId = "";
  vi.spyOn(performance, "now").mockImplementation(() => clock);
  const payload = Object.freeze({ read_at_ms: attested ? 0 : 123, private_value: "not-telemetry" });
  const before = JSON.stringify(payload);
  const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async (_url, options) => {
    requestId = new Headers(options?.headers).get("X-Console-Request-ID")!;
    clock = 20;
    const response = Response.json(payload, { headers: attested ? {
      "X-Console-Validated-Request-ID": requestId, "X-Console-Read-At-Ms": "456",
    } : {} });
    vi.spyOn(response, "json").mockImplementation(async () => { clock = 30; return payload; });
    return response;
  });
  const got = await controlRequest(path, init);
  expect(got).toEqual({ ...payload, read_at_ms: attested ? 456 : 123 });
  expect(Object.keys(got as object)).toEqual(Object.keys(payload));
  expect(JSON.stringify(payload)).toBe(before);
  expect(managementReadTrace(got)).toEqual({ requestId, validationAcknowledged: attested, started: 10, headers: 20, decoded: 30 });
  expect(fetch).toHaveBeenCalledTimes(1);
});

it("keeps independently completed requests associated with their own nonce", async () => {
  const pending: { id: string; finish: (response: Response) => void }[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation((_url, options) => new Promise(resolve => {
    pending.push({ id: new Headers(options?.headers).get("X-Console-Request-ID")!, finish: resolve });
  }));
  const first = controlRequest(path, init), second = controlRequest(path, init);
  pending[1].finish(Response.json({ read_at_ms: 2 }));
  const secondPage = await second;
  pending[0].finish(Response.json({ read_at_ms: 1 }));
  const firstPage = await first;
  expect(managementReadTrace(firstPage)?.requestId).toBe(pending[0].id);
  expect(managementReadTrace(secondPage)?.requestId).toBe(pending[1].id);
  expect(pending[0].id).not.toBe(pending[1].id);
});

it("does not trace unrelated reads or writes, nor accept failed attestations", async () => {
  const payload = { read_at_ms: 0 };
  vi.spyOn(globalThis, "fetch").mockImplementation(async (_url, options) => {
    const response = Response.json(payload, { headers: { "X-Console-Validated-Request-ID": "wrong-nonce", "X-Console-Read-At-Ms": "123" } });
    vi.spyOn(response, "json").mockResolvedValue(payload);
    expect(options?.signal).toBeDefined();
    return response;
  });
  await expect(controlRequest(path, init)).rejects.toThrow("实时核验失败");
  expect(managementReadTrace(payload)).toBeUndefined();
  for (const [url, options] of [["/v1/other", init], [path, { ...init, method: "POST" }], [path, {}]] as const) {
    const value = await controlRequest(url, options);
    expect(value).toBe(payload);
    expect(managementReadTrace(value)).toBeUndefined();
  }
});

it("retains metadata only out of band and ignores non-object inputs", () => {
  const trace = { requestId: "safe-id", validationAcknowledged: true, started: 1, headers: 2, decoded: 3 };
  for (const value of [undefined, null, false, 0, "text", []]) {
    rememberManagementReadTrace(value, trace);
    expect(managementReadTrace(value)).toBeUndefined();
  }
  const value = Object.freeze({ result: 1 });
  rememberManagementReadTrace(value, trace);
  expect(managementReadTrace(value)).toBe(trace);
  expect(JSON.stringify(value)).toBe('{"result":1}');
  expect(managementReadTrace({ result: 1 })).toBeUndefined();
});

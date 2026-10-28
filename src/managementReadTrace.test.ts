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
  vi.spyOn(console, "info").mockImplementation(() => {});
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

it.each(["response_headers", "response_status", "body_json", "current_validation"])("reports the observed failure phase %s without changing the error or retrying", async phase => {
  let clock=10,requestId="";
  const original=Error("private error message and credential");
  vi.spyOn(performance,"now").mockImplementation(()=>clock);
  const output=vi.spyOn(console,"info").mockImplementation(()=>{});
  const fetch=vi.spyOn(globalThis,"fetch").mockImplementation(async(_url,options)=>{
    requestId=new Headers(options?.headers).get("X-Console-Request-ID")!;
    clock=20;
    if(phase==="response_headers")throw original;
    const response=Response.json({read_at_ms:0},{status:phase==="response_status"?409:200,headers:{"X-Console-Validated-Request-ID":"wrong-private-nonce","X-Console-Read-At-Ms":"123"}});
    vi.spyOn(response,"text").mockImplementation(async()=>{clock=30;return "private server error";});
    vi.spyOn(response,"json").mockImplementation(async()=>{clock=30;if(phase==="body_json")throw original;return {read_at_ms:0};});
    return response;
  });
  let failure:unknown;
  try {await controlRequest(path,init);}catch(error){failure=error;}
  if(phase==="response_headers"||phase==="body_json")expect(failure).toBe(original);
  else expect(failure).toBeInstanceOf(Error);
  expect(fetch).toHaveBeenCalledTimes(1);
  const [event,raw]=output.mock.calls[0];
  expect(event).toBe("management_read_failed");
  expect(JSON.parse(raw)).toEqual({request_id:requestId,phase,status:phase==="response_headers"?null:phase==="response_status"?409:200,
    elapsed_ms:phase==="response_headers"?10:20,response_headers_ms:phase==="response_headers"?null:10,after_headers_ms:phase==="response_headers"?null:10,
    caller_signal_aborted:false,combined_signal_aborted:false});
  expect(raw).not.toMatch(/private|filters|credential|nonce|server error|analytics\//);
});

it("reports caller cancellation as a signal observation, preserving its exact rejection", async()=>{
  const controller=new AbortController();
  const reason=new DOMException("private navigation reason","AbortError");
  const output=vi.spyOn(console,"info").mockImplementation(()=>{});
  const warning=vi.spyOn(console,"warn").mockImplementation(()=>{});
  vi.spyOn(globalThis,"fetch").mockImplementation(async()=>{controller.abort(reason);throw reason;});
  await expect(controlRequest(path,{...init,signal:controller.signal})).rejects.toBe(reason);
  expect(JSON.parse(output.mock.calls[0][1])).toMatchObject({phase:"response_headers",caller_signal_aborted:true,combined_signal_aborted:true});
  expect(JSON.stringify(output.mock.calls)).not.toContain("private navigation reason");
  expect(warning).not.toHaveBeenCalled();
});

it("leaves ordinary API failures unlogged by the management diagnostic", async()=>{
  const reason=Error("private error");
  const output=vi.spyOn(console,"info").mockImplementation(()=>{});
  vi.spyOn(globalThis,"fetch").mockRejectedValue(reason);
  await expect(controlRequest("/v1/other")).rejects.toBe(reason);
  await expect(controlRequest(path,{...init,method:"POST"})).rejects.toBe(reason);
  expect(output).not.toHaveBeenCalled();
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

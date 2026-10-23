// One deadline covers headers AND body decoding. Caller cancellation stays
// cancellation; timeouts carry a safe correlation ID and never retry writes.
export class RequestTimeoutError extends Error {
  readonly requestId: string;
  constructor(requestId: string, write: boolean) {
    super((write
      ? "请求超时，保存结果尚未确认。请先读取最新状态，勿重复提交。"
      : "数据读取超时，请稍后重试读取。") + `（请求编号：${requestId}）`);
    this.name = "RequestTimeoutError";
    this.requestId = requestId;
  }
}

export async function withRequestDeadline<T>(
  signal: AbortSignal | null | undefined,
  milliseconds: number,
  write: boolean,
  run: (signal: AbortSignal, requestId: string, responseReceived: () => void) => Promise<T>,
): Promise<T> {
  const timeout = AbortSignal.timeout(milliseconds);
  const combined = signal ? AbortSignal.any([signal, timeout]) : timeout;
  const requestId = crypto.randomUUID();
  const started = performance.now();
  let headersAt: number | undefined;
  try {
    return await run(combined, requestId, () => { headersAt ??= performance.now(); });
  } catch (error) {
    if (signal?.aborted && signal.reason?.name !== "TimeoutError") throw error;
    if (timeout.aborted || signal?.reason?.name === "TimeoutError" || (typeof error === "object" && error !== null && "name" in error && error.name === "TimeoutError")) {
      const ended = performance.now();
      // Correlate browser-only delays without logging URLs, filters, headers,
      // response bodies, credentials or raw exception messages.
      console.warn("console_request_timeout", {
        request_id: requestId,
        phase: headersAt === undefined ? "response_headers" : "response_body",
        elapsed_ms: Math.round(ended - started),
        headers_ms: headersAt === undefined ? null : Math.round(headersAt - started),
        body_ms: headersAt === undefined ? null : Math.round(ended - headersAt),
      });
      throw new RequestTimeoutError(requestId, write);
    }
    throw error;
  }
}

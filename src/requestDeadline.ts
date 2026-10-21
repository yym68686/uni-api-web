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
  run: (signal: AbortSignal, requestId: string) => Promise<T>,
): Promise<T> {
  const timeout = AbortSignal.timeout(milliseconds);
  const combined = signal ? AbortSignal.any([signal, timeout]) : timeout;
  const requestId = crypto.randomUUID();
  try {
    return await run(combined, requestId);
  } catch (error) {
    if (signal?.aborted && signal.reason?.name !== "TimeoutError") throw error;
    if (timeout.aborted || signal?.reason?.name === "TimeoutError" || (typeof error === "object" && error !== null && "name" in error && error.name === "TimeoutError"))
      throw new RequestTimeoutError(requestId, write);
    throw error;
  }
}

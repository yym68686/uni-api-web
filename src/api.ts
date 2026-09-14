import type { Connection } from "./types";
export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}
export function cleanBase(value: string) {
  let url: URL;
  try {
    url = new URL(value.trim());
  } catch {
    throw new Error("请输入完整地址，例如 https://api.example.com");
  }
  if (
    !["http:", "https:"].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash
  )
    throw new Error("服务地址须为 HTTP(S)，且不包含密钥、查询参数或用户名。");
  if (
    typeof location !== "undefined" &&
    location.protocol === "https:" &&
    url.protocol === "http:" &&
    !["localhost", "127.0.0.1", "[::1]"].includes(url.hostname)
  )
    throw new Error("当前页面使用 HTTPS，请填写 uni-api 的 HTTPS 公网地址。");
  return url.href.replace(/\/+$/, "").replace(/\/v1$/, "");
}
export async function request<T>(
  connection: Connection,
  path: string,
  signal?: AbortSignal,
): Promise<T> {
  const timeout = AbortSignal.timeout(20_000);
  let response: Response;
  try {
    response = await fetch(connection.base + path, {
      headers: {
        Authorization: `Bearer ${connection.key}`,
        Accept: "application/json",
      },
      cache: "no-store",
      credentials: "omit",
      redirect: "error",
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
    });
  } catch (error) {
    if (signal?.aborted) throw error;
    throw new Error(
      timeout.aborted
        ? "连接超时，请稍后重试。"
        : "无法连接服务，请检查网络、服务地址和跨域配置。",
    );
  }
  if (response.status === 401 || response.status === 403)
    throw new ApiError(
      "密钥没有平台查看权限，请使用配置中的第一个密钥或管理员密钥。",
      response.status,
    );
  if (response.status === 404)
    throw new ApiError(
      "接口或所选 API key 不存在，请检查后端版本或重新选择。",
      404,
    );
  if (!response.ok)
    throw new ApiError(
      `服务暂时无法响应（HTTP ${response.status}），请稍后重试。`,
      response.status,
    );
  try {
    return (await response.json()) as T;
  } catch {
    throw new Error("服务返回了无效数据，请检查地址和后端状态。");
  }
}
export function channelParams(
  keyId: string,
  window = "15m",
  model = "",
  endpoint = "all",
  stream = "all",
) {
  const params = new URLSearchParams({
    endpoint,
    stream,
    window,
  });
  if (keyId) params.set("api_key_id", keyId);
  if (model) params.set("model", model);
  return params;
}
// Session-only queue. Aborted queued jobs do not send requests or consume a slot.
export function makeLimiter(limit: number) {
  let active = 0;
  const waiting: (() => void)[] = [];
  return async function limited<T>(
    job: () => Promise<T>,
    signal: AbortSignal,
  ): Promise<T> {
    await new Promise<void>((resolve, reject) => {
      const ready = () => {
        signal.removeEventListener("abort", abort);
        active++;
        resolve();
      };
      const abort = () => {
        const i = waiting.indexOf(ready);
        if (i >= 0) waiting.splice(i, 1);
        reject(signal.reason);
      };
      if (signal.aborted) {
        reject(signal.reason);
        return;
      }
      if (active < limit) {
        ready();
        return;
      }
      waiting.push(ready);
      signal.addEventListener("abort", abort, { once: true });
    });
    try {
      signal.throwIfAborted();
      return await job();
    } finally {
      active--;
      waiting.shift()?.();
    }
  };
}

export async function analyticsRequest<T>(connection: Connection,path: string,signal?: AbortSignal, init: RequestInit = {}): Promise<T> {
  if (typeof window === "undefined") throw new Error("analytics API requires a browser session");
  const timeout=AbortSignal.timeout(20_000); const response=await fetch(window.location.origin+path,{...init,headers:{Authorization:`Bearer ${connection.key}`,Accept:"application/json",...(init.headers||{})},cache:"no-store",credentials:"omit",redirect:"error",signal:signal?AbortSignal.any([signal,timeout]):timeout});
  if(response.status===401||response.status===403) throw new ApiError("分析服务未接受平台密钥，请检查控制台后端配置。",response.status);
  if(!response.ok) throw new ApiError(`分析服务暂时无法响应（HTTP ${response.status}）。`,response.status);
  return await response.json() as T;
}

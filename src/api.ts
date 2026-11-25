import { withRequestDeadline } from "./requestDeadline";
import { rememberManagementReadTrace } from "./managementReadTrace";
import type { Connection } from "./types";
export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}
export class AnalyticsInitializingError extends ApiError {
  constructor() {
    super("历史数据正在初始化，完成后将自动更新。", 503);
  }
}
// Initialization recovery must work even when live auto-refresh is disabled.
export function initializationRetryInterval(query: { state: { error: Error | null } }): 5000 | false {
  return query.state.error instanceof AnalyticsInitializingError ? 5000 : false;
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
  const target = connection.account
    ? `/analytics/v1/sources/${encodeURIComponent(connection.sourceId || "all")}/proxy${path}`
    : connection.base + path;
  try {
    response = await fetch(target, {
      headers: {
        ...(connection.account?{}:{Authorization: `Bearer ${connection.key}`}),
        Accept: "application/json",
      },
      cache: "no-store",
      credentials: connection.account ? "include" : "omit",
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
      connection.account ? "登录已过期，请重新登录。" : "密钥没有平台查看权限，请使用配置中的第一个密钥或管理员密钥。",
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
  return withRequestDeadline(signal, 20_000, false, async (combined, requestId, responseReceived) => {
  const response=await fetch(window.location.origin+path,{...init,headers:{...(connection.key?{Authorization:`Bearer ${connection.key}`}:{ }),Accept:"application/json",...(init.headers||{}),"X-Console-Request-ID": requestId},cache:"no-store",credentials:"include",redirect:"error",signal:combined});
  responseReceived();
  if(response.status===401||response.status===403) throw new ApiError("分析服务未接受平台密钥，请检查控制台后端配置。",response.status);
  if (!response.ok) {
    if (response.status === 503) {
      const problem = await response.json().catch(() => null);
      if (problem?.code === "analytics_initializing") throw new AnalyticsInitializingError();
    }
    throw new ApiError(`分析服务暂时无法响应（HTTP ${response.status}）。`, response.status);
  }
  return await response.json() as T;
  });
}

let controlWriteGeneration = 0;
let blockingControlWriteGeneration = 0;
export const currentControlWriteGeneration = () => controlWriteGeneration;
export const currentBlockingControlWriteGeneration = () => blockingControlWriteGeneration;

// Only identity-changing writes must invalidate actions on an already validated
// page. Evidence and settings for independent tools have their own refresh path.
function managementWriteImpact(path: string, method: string, backgroundChannelAdd: boolean) {
 const endpoint = path.split("?", 1)[0];
 if (endpoint.includes("/recharge/") ||
     endpoint === "/v1/sub2api/channel-tier-options" ||
     /^\/v1\/sub2api\/accounts(?:\/[^/]+)?\/(?:sync|sync-selected|sync-only)$/.test(endpoint) ||
     /^\/v1\/(?:sub2api\/check-curl|channel-management\/detection-settings|channel-setting-templates)$/.test(endpoint) ||
     /^\/v1\/automations(?:\/[^/]+)?$/.test(endpoint) ||
     /^\/v1\/sources\/[^/]+\/channel-settings\/(?:validate|discover|discover-draft)$/.test(endpoint))
   return "none";
 if (/^\/v1\/(?:channel-management\/checks|model-fingerprints(?:\/settings)?|sub2api\/(?:checks|quality-checks|compaction-checks|tool-use-checks)|sub2api\/accounts\/[^/]+\/(?:stop|probe-limits|settings)|sources\/[^/]+\/(?:channel-checks|channel-controls)|automations\/(?:[^/]+\/run|audit\/[^/]+\/(?:review|apply|rollback)))$/.test(endpoint) ||
     (backgroundChannelAdd && method === "POST" &&
      (endpoint === "/v1/sub2api/channels" || endpoint === "/v1/channel-management")))
   return "background";
 return "blocking";
}

export async function controlRequest<T>(path:string,init:RequestInit={}, { backgroundChannelAdd = false, localCheckRefresh = false } = {}):Promise<T>{
 const method=init.method?.toUpperCase() || "GET";
 const read=method==="GET";
 return withRequestDeadline(init.signal, read ? 30_000 : 120_000, !read, async (signal, requestId, responseReceived) => {
 const currentManagement=read && path.startsWith("/v1/channel-management/query?") && init.cache==="no-cache" && new Headers(init.headers).get("X-Console-Current-Validation")==="1";
 const started=currentManagement?performance.now():0;
 let headers:number|undefined,phase="response_headers",status:number|null=null;
 try {
 const response=await fetch(globalThis.location.origin+"/analytics"+path,{...init,headers:{Accept:"application/json","Content-Type":"application/json",...init.headers,"X-Console-Request-ID":requestId},credentials:"same-origin",cache:currentManagement?"no-cache":"no-store",signal});
 headers=currentManagement?performance.now():undefined;
 phase="response_status";
 status=response.status;
 responseReceived();
 if(!response.ok){const message=await response.text();throw new ApiError(message||`HTTP ${response.status}`,response.status)}
 const impact = read || (localCheckRefresh && method === "POST" && /^\/v1\/(?:channel-management\/checks|sub2api\/(?:checks|quality-checks|compaction-checks|tool-use-checks))$/.test(path))
   ? "none" : managementWriteImpact(path, method, backgroundChannelAdd);
 if(impact !== "none" && typeof window !== "undefined") {
   controlWriteGeneration++;
   if (impact === "blocking") blockingControlWriteGeneration = controlWriteGeneration;
   window.dispatchEvent(new Event("uni-console-control-write"));
 }
 phase="body_json";
 let data=await response.json();
 const decoded=currentManagement?performance.now():0;
 if(currentManagement) {
   phase="current_validation";
   const acknowledgement=response.headers.get("X-Console-Validated-Request-ID");
   if(acknowledgement!==null) {
     const at=Number(response.headers.get("X-Console-Read-At-Ms"));
     if(acknowledgement!==requestId||!Number.isSafeInteger(at)||at<=0||!data||typeof data!=="object"||Array.isArray(data)) throw Error("渠道数据实时核验失败，请重新读取");
     data={...data,read_at_ms:at};
   } else {
     // Legacy backends return a full no-store page. A cached zero-timestamp
     // representation must never be displayed without THIS request's nonce.
     if(data?.read_at_ms===0) throw Error("渠道数据实时核验失败，请重新读取");
   }
   rememberManagementReadTrace(data,{requestId,validationAcknowledged:acknowledgement===requestId,started,headers:headers!,decoded});
 }
 return data as T;
 } catch(error) {
   if(currentManagement) {
     const ended=performance.now();
     // Describe only observed phases/signals. A canceled fetch alone cannot
     // prove navigation, a proxy disconnect, or a server-side timeout.
     console.info("management_read_failed",JSON.stringify({
       request_id:requestId,phase,status,
       elapsed_ms:Math.round(ended-started),
       response_headers_ms:headers===undefined?null:Math.round(headers-started),
       after_headers_ms:headers===undefined?null:Math.round(ended-headers),
       caller_signal_aborted:init.signal?.aborted??false,
       combined_signal_aborted:signal.aborted,
     }));
   }
   throw error;
 }
 });
}

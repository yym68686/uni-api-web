const channel = "uni-api-browser-login-v1";
export interface BrowserLoginInput {
  base: string;
  email: string;
  password: string;
  agreed: boolean;
}
export interface BrowserAuth {
  access_token: string;
  refresh_token: string;
}
export function browserHelperAvailable(): Promise<boolean> {
  const id = crypto.randomUUID();
  return new Promise((resolve) => {
    const listener = (event: MessageEvent) => {
      if (
        event.source === window &&
        event.origin === location.origin &&
        event.data?.channel === channel &&
        event.data.id === id &&
        event.data.type === "ready"
      ) {
        cleanup();
        resolve(true);
      }
    };
    const cleanup = () => {
      clearTimeout(timer);
      window.removeEventListener("message", listener);
    };
    const timer = setTimeout(() => {
      cleanup();
      resolve(false);
    }, 800);
    window.addEventListener("message", listener);
    window.postMessage({ channel, id, type: "ping" }, location.origin);
  });
}
export function loginWithBrowser(
  input: BrowserLoginInput,
  signal: AbortSignal,
): Promise<BrowserAuth> {
  const id = crypto.randomUUID();
  return new Promise((resolve, reject) => {
    const cleanup = () => {
      clearTimeout(timer);
      signal.removeEventListener("abort", abort);
      window.removeEventListener("message", listener);
    };
    const abort = () => {
      cleanup();
      window.postMessage({ channel, id, type: "cancel" }, location.origin);
      reject(new Error("浏览器登录已取消"));
    };
    const listener = (event: MessageEvent) => {
      if (
        event.source !== window ||
        event.origin !== location.origin ||
        event.data?.channel !== channel ||
        event.data.id !== id ||
        event.data.type !== "result"
      )
        return;
      cleanup();
      const { auth, error } = event.data;
      if (typeof auth?.access_token === "string" && auth.access_token)
        resolve({
          access_token: auth.access_token,
          refresh_token:
            typeof auth.refresh_token === "string" ? auth.refresh_token : "",
        });
      else
        reject(
          new Error(typeof error === "string" ? error : "浏览器未返回登录会话"),
        );
    };
    const timer = setTimeout(abort, 190000);
    if (signal.aborted) {
      abort();
      return;
    }
    signal.addEventListener("abort", abort, { once: true });
    window.addEventListener("message", listener);
    window.postMessage({ channel, id, type: "login", input }, location.origin);
  });
}

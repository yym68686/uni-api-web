const channel = "uni-api-browser-login-v1";
export interface BrowserLoginInput {
  base: string;
  email: string;
  password: string;
  agreed: boolean;
  kind?: "sub2api" | "newapi";
}
export interface BrowserAuth {
  access_token: string;
  refresh_token: string;
  browser_kind?: "sub2api" | "newapi";
  browser_user_id?: number;
  browser_username?: string;
  browser_cookies?: Record<string, string>;
  browser_session_id?: string;
  browser_access_expires_at?: number;
}
export function browserHelperAvailable(
  kind: "sub2api" | "newapi" = "sub2api",
): Promise<boolean> {
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
        resolve(
          kind !== "newapi" ||
            event.data.capabilities?.includes("newapi") === true,
        );
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
      if (
        auth &&
        (input.kind === "newapi") !== (auth.browser_kind === "newapi")
      ) {
        reject(new Error("请更新登录助手到 1.2.0 或更新版本后重试"));
        return;
      }
      if (
        (typeof auth?.access_token === "string" && auth.access_token) ||
        (input.kind === "newapi" &&
          typeof auth?.browser_cookies?.session === "string" &&
          auth.browser_cookies.session)
      )
        resolve({
          access_token:
            typeof auth.access_token === "string" ? auth.access_token : "",
          refresh_token:
            typeof auth.refresh_token === "string" ? auth.refresh_token : "",
          browser_kind: auth.browser_kind === "newapi" ? "newapi" : undefined,
          browser_user_id:
            typeof auth.browser_user_id === "number"
              ? auth.browser_user_id
              : undefined,
          browser_username:
            typeof auth.browser_username === "string"
              ? auth.browser_username
              : undefined,
          browser_cookies:
            input.kind === "newapi"
              ? Object.fromEntries(
                  ["session", "new_api_refresh"].flatMap((name) =>
                    typeof auth.browser_cookies?.[name] === "string"
                      ? [[name, auth.browser_cookies[name]]]
                      : [],
                  ),
                )
              : undefined,
          browser_session_id:
            typeof auth.browser_session_id === "string"
              ? auth.browser_session_id
              : undefined,
          browser_access_expires_at:
            typeof auth.browser_access_expires_at === "number"
              ? auth.browser_access_expires_at
              : undefined,
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

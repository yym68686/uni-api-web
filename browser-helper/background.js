import { loginPageStep } from "./login-page.js";
import { newAPILoginPageStep } from "./newapi-login-page.js";
import { newAPISessionCookies } from "./newapi-cookies.js";
import { extensionAPI } from "./webextension-api.js";
const api = extensionAPI();
const consoleOrigin = "https://uni-api-console.fugue.pro";
let active = null;
api.runtime.onMessage.addListener((message, sender, reply) => {
  // Firefox may omit MessageSender.origin. Use the browser-provided URL only
  // as a fallback, never a URL or tab ID supplied by the page's message.
  let senderOrigin = sender.origin;
  try {
    if (sender.url) {
      const urlOrigin = new URL(sender.url).origin;
      if (senderOrigin && senderOrigin !== urlOrigin) return;
      senderOrigin = urlOrigin;
    }
  } catch {
    return;
  }
  if (
    senderOrigin !== consoleOrigin ||
    (sender.frameId !== undefined && sender.frameId !== 0) ||
    (!Number.isInteger(sender.tab?.id) && typeof sender.url !== "string") ||
    !message ||
    typeof message.id !== "string" ||
    message.id.length > 100
  )
    return;
  if (message.type === "cancel") {
    const owner = sender.tab?.id ?? sender.documentId ?? sender.url;
    if (active?.id === message.id && active.owner === owner) {
      active.cancelled = true;
      if (active.tabId) void api.tabs.remove(active.tabId).catch(() => {});
    }
    return;
  }
  if (message.type !== "login") return;
  if (active) {
    reply({ error: "已有浏览器登录进行中" });
    return;
  }
  const input = message.input;
  let site;
  try {
    if (!input || typeof input.base !== "string") throw new Error();
    site = new URL(input.base);
  } catch {
    reply({ error: "站点地址无效" });
    return;
  }
  if (
    site.protocol !== "https:" ||
    site.username ||
    site.password ||
    site.search ||
    site.hash ||
    typeof input.email !== "string" ||
    !input.email ||
    input.email.length > 320 ||
    input.base.length > 2048 ||
    typeof input.password !== "string" ||
    (!input.password && input.kind !== "newapi") ||
    input.password.length > 1024 ||
    (input.kind !== undefined &&
      input.kind !== "sub2api" &&
      input.kind !== "newapi")
  ) {
    reply({ error: "站点登录信息无效" });
    return;
  }
  const job = {
    id: message.id,
    owner: sender.tab?.id ?? sender.documentId ?? sender.url,
    cancelled: false,
    tabId: null,
  };
  active = job;
  (async () => {
    let tab;
    try {
      if (job.cancelled) throw new Error("登录已取消");
      if (
        input.kind === "newapi" &&
        !(await api.permissions.contains({ permissions: ["cookies"] }))
      )
        throw new Error(
          "请点击登录助手扩展图标，授权 New API 会话读取后重试。",
        );
      tab = await api.tabs.create({
        url:
          input.base.replace(/\/$/, "") +
          (input.kind === "newapi" ? "/sign-in" : "/login"),
        active: true,
        ...(Number.isInteger(sender.tab?.windowId)
          ? { windowId: sender.tab.windowId }
          : {}),
        // New API has explicitly granted cookies permission, which Firefox
        // also requires for tabs.create({cookieStoreId}). Sub2api must keep
        // working without that optional permission.
        ...(input.kind === "newapi" && sender.tab?.cookieStoreId
          ? { cookieStoreId: sender.tab.cookieStoreId }
          : {}),
      });
      job.tabId = tab.id;
      const deadline = Date.now() + 180000;
      while (Date.now() < deadline && !job.cancelled) {
        let results;
        try {
          // Firefox can return about:blank from tabs.create before navigation.
          // Always re-read the tab; never keep polling its initial URL snapshot.
          const current = await api.tabs.get(tab.id);
          const loading =
            !current.url ||
            (current.url === "about:blank" &&
              (!current.pendingUrl ||
                new URL(current.pendingUrl).origin === site.origin));
          if (!loading && new URL(current.url).origin !== site.origin)
            throw new Error("站点跳转到其他域名，已停止登录");
          if (loading) {
            results = [];
          } else {
            const cookies =
              input.kind === "newapi"
                ? await newAPISessionCookies(
                    input.base.replace(/\/$/, ""),
                    current,
                  )
                : {};
            results = await api.scripting.executeScript({
              target: { tabId: tab.id },
              func:
                input.kind === "newapi" ? newAPILoginPageStep : loginPageStep,
              args: [
                {
                  origin: site.origin,
                  base: input.base.replace(/\/$/, ""),
                  email: input.email,
                  password: input.password,
                  agreed: true,
                  hasSession: !!cookies.session,
                  hasRefresh: !!cookies.new_api_refresh,
                },
              ],
            });
          }
        } catch {
          const current = await api.tabs.get(tab.id);
          if (
            current.url &&
            current.url !== "about:blank" &&
            new URL(current.url).origin !== site.origin
          )
            throw new Error("站点跳转到其他域名，已停止登录");
        }
        if (job.cancelled) throw new Error("登录已取消");
        const state = results?.[0]?.result;
        if (state?.filled) input.password = "";
        if (state?.error) throw new Error(state.error);
        if (
          state?.login_path === "/login" &&
          input.kind === "newapi" &&
          !job.legacyLogin
        ) {
          job.legacyLogin = true;
          await api.tabs.update(tab.id, {
            url: input.base.replace(/\/$/, "") + "/login",
          });
        }
        if (state?.auth) {
          if (input.kind === "newapi") {
            const current = await api.tabs.get(tab.id);
            state.auth.browser_cookies = await newAPISessionCookies(
              input.base.replace(/\/$/, ""),
              current,
            );
            if (!state.auth.access_token && !state.auth.browser_cookies.session)
              throw new Error("原站登录会话已变化，请重新连接");
          }
          if (job.cancelled) throw new Error("登录已取消");
          input.password = "";
          reply({ auth: state.auth });
          await api.tabs.remove(tab.id);
          if (Number.isInteger(sender.tab?.id))
            await api.tabs.update(sender.tab.id, { active: true });
          return;
        }
        await new Promise((resolve) => setTimeout(resolve, 1000));
      }
      throw new Error(
        job.cancelled ? "登录已取消" : "登录等待超时，请完成原站验证后重新连接",
      );
    } catch (error) {
      reply({
        error: error instanceof Error ? error.message : "浏览器登录失败",
      });
      // Leave the original page visible when verification needs user attention.
    } finally {
      input.password = "";
      active = null;
    }
  })();
  return true;
});

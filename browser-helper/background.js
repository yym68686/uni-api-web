import { loginPageStep } from "./login-page.js";
import { newAPILoginPageStep } from "./newapi-login-page.js";
import { newAPISessionCookies } from "./newapi-cookies.js";
const consoleOrigin = "https://uni-api-console.fugue.pro";
let active = null;
chrome.runtime.onMessage.addListener((message, sender, reply) => {
  if (sender.origin !== consoleOrigin || sender.frameId !== 0) return;
  if (message.type === "cancel") {
    if (active?.id === message.id && active.owner === sender.tab.id) {
      active.cancelled = true;
      if (active.tabId) void chrome.tabs.remove(active.tabId).catch(() => {});
    }
    return;
  }
  if (message.type !== "login" || typeof message.id !== "string") return;
  if (active) {
    reply({ error: "已有浏览器登录进行中" });
    return;
  }
  const input = message.input;
  let site;
  try {
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
    owner: sender.tab.id,
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
        !(await chrome.permissions.contains({ permissions: ["cookies"] }))
      )
        throw new Error(
          "请点击登录助手扩展图标，授权 New API 会话读取后重试。",
        );
      tab = await chrome.tabs.create({
        url:
          input.base.replace(/\/$/, "") +
          (input.kind === "newapi" ? "/sign-in" : "/login"),
        active: true,
      });
      job.tabId = tab.id;
      const deadline = Date.now() + 180000;
      while (Date.now() < deadline && !job.cancelled) {
        let results;
        try {
          const current =
            input.kind === "newapi" ? await chrome.tabs.get(tab.id) : tab;
          const loading =
            !current.url ||
            (current.url === "about:blank" &&
              current.pendingUrl &&
              new URL(current.pendingUrl).origin === site.origin);
          if (!loading && new URL(current.url).origin !== site.origin)
            throw new Error("站点跳转到其他域名，已停止登录");
          if (loading && input.kind === "newapi") {
            results = [];
          } else {
            const cookies =
              input.kind === "newapi"
                ? await newAPISessionCookies(
                    input.base.replace(/\/$/, ""),
                    current,
                  )
                : {};
            results = await chrome.scripting.executeScript({
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
          const current = await chrome.tabs.get(tab.id);
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
          await chrome.tabs.update(tab.id, {
            url: input.base.replace(/\/$/, "") + "/login",
          });
        }
        if (state?.auth) {
          if (input.kind === "newapi") {
            const current = await chrome.tabs.get(tab.id);
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
          await chrome.tabs.remove(tab.id);
          await chrome.tabs.update(sender.tab.id, { active: true });
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

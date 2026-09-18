import { loginPageStep } from "./login-page.js";
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
    !input.password ||
    input.password.length > 1024
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
      tab = await chrome.tabs.create({
        url: input.base.replace(/\/$/, "") + "/login",
        active: true,
      });
      job.tabId = tab.id;
      const deadline = Date.now() + 180000;
      while (Date.now() < deadline && !job.cancelled) {
        let results;
        try {
          results = await chrome.scripting.executeScript({
            target: { tabId: tab.id },
            func: loginPageStep,
            args: [
              {
                origin: site.origin,
                base: input.base.replace(/\/$/, ""),
                email: input.email,
                password: input.password,
                agreed: true,
              },
            ],
          });
        } catch {
          const current = await chrome.tabs.get(tab.id);
          if (current.url && new URL(current.url).origin !== site.origin)
            throw new Error("站点跳转到其他域名，已停止登录");
        }
        if (job.cancelled) throw new Error("登录已取消");
        const state = results?.[0]?.result;
        if (state?.filled) input.password = "";
        if (state?.error) throw new Error(state.error);
        if (state?.auth) {
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
chrome.action.onClicked.addListener(() =>
  chrome.tabs.create({ url: consoleOrigin }),
);

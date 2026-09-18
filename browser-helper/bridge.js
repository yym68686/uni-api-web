(() => {
  const channel = "uni-api-browser-login-v1";
  window.addEventListener("message", async (event) => {
    if (
      event.source !== window ||
      event.origin !== location.origin ||
      event.data?.channel !== channel
    )
      return;
    const { id, type, input } = event.data;
    if (typeof id !== "string" || id.length > 100) return;
    if (type === "ping") {
      window.postMessage({ channel, id, type: "ready" }, location.origin);
      return;
    }
    if (type === "cancel") {
      void chrome.runtime.sendMessage({ type: "cancel", id });
      return;
    }
    if (type !== "login") return;
    try {
      const result = await chrome.runtime.sendMessage({
        type: "login",
        id,
        input,
      });
      window.postMessage(
        { channel, id, type: "result", ...result },
        location.origin,
      );
    } catch {
      window.postMessage(
        {
          channel,
          id,
          type: "result",
          error: "登录助手已断开，请重新加载控制台后重试",
        },
        location.origin,
      );
    }
  });
})();

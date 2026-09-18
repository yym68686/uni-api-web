const args = new URLSearchParams(location.search);
const origin = args.get("origin");
const id = args.get("id");
document.getElementById("site").textContent = origin;
document.getElementById("allow").addEventListener("click", async () => {
  try {
    const url = new URL(origin);
    if (url.protocol !== "https:" || url.origin !== origin)
      throw new Error("站点地址无效");
    const granted = await chrome.permissions.request({
      origins: [origin + "/*"],
    });
    if (!granted) throw new Error("尚未授予站点权限");
    await chrome.runtime.sendMessage({ type: "permission-granted", id });
  } catch (e) {
    document.getElementById("error").textContent = e.message || "授权失败";
  }
});

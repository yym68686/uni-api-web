import { extensionAPI } from "./webextension-api.js";

const api = extensionAPI();
document.querySelector("#enable").addEventListener("click", async () => {
  try {
    const granted = await api.permissions.request({
      permissions: ["cookies"],
    });
    document.querySelector("#status").textContent = granted
      ? "已授权。请回到控制台点击使用浏览器登录。"
      : "尚未授权，New API 浏览器登录不可用。";
  } catch {
    document.querySelector("#status").textContent =
      "授权失败，请在浏览器扩展管理页检查权限后重试。";
  }
});
document
  .querySelector("#console")
  .addEventListener("click", () =>
    api.tabs.create({ url: "https://uni-api-console.fugue.pro" }),
  );

document.querySelector("#enable").addEventListener("click", async () => {
  const granted = await chrome.permissions.request({
    permissions: ["cookies"],
  });
  document.querySelector("#status").textContent = granted
    ? "已授权。请回到控制台点击使用浏览器登录。"
    : "尚未授权，New API 浏览器登录不可用。";
});
document
  .querySelector("#console")
  .addEventListener("click", () =>
    chrome.tabs.create({ url: "https://uni-api-console.fugue.pro" }),
  );

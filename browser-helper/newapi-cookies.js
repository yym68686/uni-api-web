export async function newAPISessionCookies(base, tab) {
  const site = new URL(base);
  if (new URL(tab.url).origin !== site.origin)
    throw Error("站点跳转到其他域名，已停止登录");
  const stores = await chrome.cookies.getAllCookieStores();
  const store = stores.find((s) => s.tabIds.includes(tab.id));
  if (!store) throw Error("无法定位原站登录会话，请重试");
  const cookies = {};
  for (const [name, path] of [
    ["session", "/api/user/self"],
    ["new_api_refresh", "/api/user/auth/refresh"],
  ]) {
    const cookie = await chrome.cookies.get({
      name,
      url: base + path,
      storeId: store.id,
    });
    // Never collect WAF clearance, other cookies, or cookies belonging to a
    // parent/foreign domain. A browser partition must not escape its scope.
    if (
      !cookie ||
      cookie.partitionKey ||
      cookie.domain.replace(/^\./, "").toLowerCase() !==
        site.hostname.toLowerCase()
    )
      continue;
    if (cookie.value && cookie.value.length <= 8192)
      cookies[name] = cookie.value;
  }
  return cookies;
}

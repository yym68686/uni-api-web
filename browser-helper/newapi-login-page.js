// This function is serialized by chrome.scripting: all helpers must be nested.
// It never reads, solves, or forwards a Turnstile token. The native page owns
// verification and submission. Only the completed site's session is exported.
export async function newAPILoginPageStep(input) {
  if (location.origin !== input.origin)
    return { error: "站点跳转到其他域名，已停止登录" };
  const matches = (user) =>
    user &&
    [user.username, user.email].some(
      (value) =>
        typeof value === "string" &&
        value.toLowerCase() === input.email.toLowerCase(),
    );
  let legacy;
  try {
    legacy = JSON.parse(localStorage.getItem("user") || "null");
  } catch {
    /* no legacy profile */
  }
  if (input.hasSession && legacy?.id && !matches(legacy))
    return { error: "原站已登录其他账号，请先退出该账号后重试" };
  async function profile(access, userID, sid) {
    const headers = { Accept: "application/json" };
    if (access) headers.Authorization = "Bearer " + access;
    if (userID) headers["New-Api-User"] = String(userID);
    if (sid) headers["X-Auth-Session"] = sid;
    const response = await fetch(input.base + "/api/user/self", {
      credentials: "include",
      headers,
      cache: "no-store",
      redirect: "error",
      signal: AbortSignal.timeout(8000),
    });
    if (!response.ok) return null;
    const payload = await response.json();
    return payload.success && payload.data?.id > 0 ? payload.data : null;
  }
  if (input.hasSession || input.hasRefresh) {
    try {
      let access = "",
        sid = "",
        expires = 0,
        user;
      if (input.hasSession) user = await profile("", legacy?.id, "");
      if (!user && input.hasRefresh) {
        const response = await fetch(input.base + "/api/user/auth/refresh", {
          method: "POST",
          credentials: "include",
          headers: { Accept: "application/json" },
          cache: "no-store",
          redirect: "error",
          signal: AbortSignal.timeout(8000),
        });
        const payload = response.ok ? await response.json() : null;
        const data = payload?.success && payload.data;
        if (data?.access_token && data.user?.id > 0) {
          if (!matches(data.user))
            return { error: "原站已登录其他账号，请先退出该账号后重试" };
          access = data.access_token;
          sid = data.session?.sid || "";
          expires = data.access_expires_at || 0;
          user = await profile(access, data.user.id, sid);
          if (user && user.id !== data.user.id)
            return { error: "原站会话身份不一致" };
        }
      }
      if (user) {
        if (!matches(user)) return { error: "原站登录账号与用户名不一致" };
        return {
          auth: {
            access_token: access,
            refresh_token: "",
            browser_kind: "newapi",
            browser_user_id: user.id,
            browser_username: user.username,
            browser_session_id: sid,
            browser_access_expires_at: expires,
          },
        };
      }
    } catch {
      /* original page may still be completing login */
    }
  }
  const username = document.querySelector(
    'input[name="username"],input#username,input[name="email"],input#email',
  );
  const form = username?.closest("form");
  const password = form?.querySelector(
    'input[name="password"],input#password,input[type="password"]',
  );
  if (!username || !password || !form) {
    // Older New API builds expose /login, newer builds expose /sign-in.
    const link = [...document.querySelectorAll("a[href]")].find(
      (a) =>
        a.href === input.base + "/login" &&
        location.pathname !== new URL(a.href).pathname,
    );
    if (link) return { login_path: "/login" };
    return { waiting: true };
  }
  if (username.disabled || password.disabled) return { waiting: true };
  if (!form.dataset.uniConsoleFilled && input.password) {
    const setValue = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    ).set;
    setValue.call(username, input.email);
    username.dispatchEvent(new Event("input", { bubbles: true }));
    setValue.call(password, input.password);
    password.dispatchEvent(new Event("input", { bubbles: true }));
    form.dataset.uniConsoleFilled = "true";
    return { filled: true };
  }
  // User clicks the site's login button after completing any CAPTCHA or legal
  // consent. Do not submit prematurely while an invisible widget initializes.
  return { waiting: true, filled: !!form.dataset.uniConsoleFilled };
}

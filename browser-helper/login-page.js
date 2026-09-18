// Runs in the isolated extension world, only on the exact authorized origin.
// It uses the site's form and reads only that sub2api account's session fields.
export async function loginPageStep(input) {
  if (location.origin !== input.origin)
    return { error: "站点跳转到其他域名，已停止登录" };
  const access = localStorage.getItem("auth_token");
  if (access) {
    let user;
    try {
      user = JSON.parse(localStorage.getItem("auth_user") || "null");
    } catch {
      /* wait for login */
    }
    if (user?.email && user.email.toLowerCase() !== input.email.toLowerCase())
      return { error: "原站已登录其他账号，请先退出该账号后重试" };
    if (user?.email) {
      try {
        const response = await fetch(input.base + "/api/v1/auth/me", {
          headers: { Authorization: "Bearer " + access },
          cache: "no-store",
          redirect: "error",
          signal: AbortSignal.timeout(8000),
        });
        const profile = await response.json();
        if (
          response.ok &&
          profile.data?.email?.toLowerCase() === input.email.toLowerCase()
        )
          return {
            auth: {
              access_token: access,
              refresh_token: localStorage.getItem("refresh_token") || "",
            },
          };
        if (response.ok && profile.data?.email)
          return { error: "原站登录账号与邮箱不一致" };
      } catch {
        /* Allow the original page to refresh its own session. */
      }
    }
  }
  const email = document.querySelector("input#email");
  const password = document.querySelector("input#password");
  const form = email?.closest("form");
  if (!email || !password || !form) return { waiting: true };
  const agreement = document.querySelector("#login-agreement-consent");
  if (agreement && !agreement.checked) {
    if (!input.agreed) return { error: "请在控制台确认该站点登录协议" };
    agreement.click();
  }
  if (email.disabled || password.disabled) return { waiting: true };
  if (!form.dataset.uniConsoleFilled && input.password) {
    const setValue = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    ).set;
    setValue.call(email, input.email);
    email.dispatchEvent(new Event("input", { bubbles: true }));
    setValue.call(password, input.password);
    password.dispatchEvent(new Event("input", { bubbles: true }));
    form.dataset.uniConsoleFilled = "true";
    return { filled: true };
  }
  const submit = form.querySelector('button[type="submit"]');
  if (
    form.dataset.uniConsoleFilled &&
    !form.dataset.uniConsoleSubmitted &&
    submit &&
    !submit.disabled
  ) {
    form.dataset.uniConsoleSubmitted = "true";
    submit.click();
  }
  return { waiting: true, filled: !!form.dataset.uniConsoleFilled };
}

import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Globe2, Plus, X } from "lucide-react";
import { controlRequest } from "./api";
import { browserHelperAvailable, loginWithBrowser } from "./sub2apiBrowser";
import { Spinner } from "./ui";
import type { SubAccount } from "./Sub2apiChecks";

export function AccountForm({
  initial,
  close,
  saved,
  sessionOnly = false,
}: {
  initial: SubAccount | null;
  close: () => void;
  saved: () => void;
  sessionOnly?: boolean;
}) {
  const endpoint = initial
    ? `/v1/sub2api/accounts/${encodeURIComponent(initial.id)}/login`
    : "/v1/sub2api/accounts";
  const [name, setName] = useState(initial?.name || "");
  const [base, setBase] = useState(initial?.base || "");
  const [email, setEmail] = useState(initial?.login_name || initial?.email || "");
  const [password, setPassword] = useState("");
  const hasSavedPassword = initial?.has_saved_password === true;
  const [browserNeeded, setBrowserNeeded] = useState(false);
  const [helperReady, setHelperReady] = useState(false);
  const [browserBusy, setBrowserBusy] = useState(false);
  const [challenge, setChallenge] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [loginSaved, setLoginSaved] = useState(false);
  const [error, setError] = useState("");
  const browserAbort = useRef<AbortController | null>(null);
  const opener = useRef(document.activeElement);
  useEffect(() => {
    let active = true;
    void browserHelperAvailable().then((ready) => {
      if (active) setHelperReady(ready);
    });
    return () => {
      active = false;
      browserAbort.current?.abort();
    };
  }, []);
  function dismiss() {
    if (busy) return;
    browserAbort.current?.abort();
    close();
  }
  async function finishLogin() {
    setPassword("");
    setChallenge("");
    setCode("");
    setLoginSaved(true);
    if (initial && !sessionOnly) {
      try {
        await controlRequest(`/v1/sub2api/accounts/${encodeURIComponent(initial.id)}/sync`, {
          method: "POST", body: "{}",
        });
      } catch (e) {
        throw new Error(`登录已保存，同步未启动：${e instanceof Error ? e.message : "请稍后点击同步并检测"}`);
      }
    }
    saved();
  }
  async function browserLogin() {
    if (busy || browserBusy) return;
    if (!helperReady) {
      setError("登录助手未连接，请重新加载扩展和控制台后重试。");
      return;
    }
    setBrowserBusy(true);
    setError("");
    const abort = new AbortController();
    browserAbort.current = abort;
    try {
      const auth = await loginWithBrowser(
        { base, email, password, agreed: true },
        abort.signal,
      );
      await controlRequest(endpoint, {
        method: "POST",
        // Keep the operator's password through the browser verification step
        // so the server can encrypt it for future automatic sign-ins.
        body: JSON.stringify({ name, base, email, ...(password ? { password } : {}), ...auth }),
        signal: abort.signal,
      });
      await finishLogin();
    } catch (e) {
      if (!abort.signal.aborted)
        setError(e instanceof Error ? e.message : "浏览器登录失败");
    } finally {
      setBrowserBusy(false);
      browserAbort.current = null;
    }
  }
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (busy || browserBusy) return;
    if (browserNeeded && !challenge && !loginSaved) {
      void browserLogin();
      return;
    }
    setBusy(true);
    setError("");
    try {
      if (loginSaved) {
        await finishLogin();
        return;
      }
      const result = await controlRequest<{
        requires_2fa?: boolean;
        challenge?: string;
        requires_browser?: boolean;
        browser_base?: string;
      }>(endpoint, {
        method: "POST",
        body: JSON.stringify(
          challenge
            ? { challenge, totp_code: code }
            : { name, base, email, ...(password ? { password } : {}) },
        ),
      });
      if (result.requires_browser) {
        setBrowserNeeded(true);
        if (result.browser_base) setBase(result.browser_base);
        setHelperReady(await browserHelperAvailable());
      } else if (result.requires_2fa && result.challenge) {
        setChallenge(result.challenge);
        setPassword("");
      } else {
        await finishLogin();
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "连接失败");
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open) dismiss();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className={`dialog-overlay${sessionOnly ? " site-reauth-overlay" : ""}`} />
        <Dialog.Content
          className={`guide-dialog sub-account-dialog${sessionOnly ? " site-reauth-dialog" : ""}`}
          onPointerDownOutside={(event) => {
            if (busy || browserBusy) event.preventDefault();
          }}
          onEscapeKeyDown={(event) => {
            if (busy) event.preventDefault();
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            if (
              opener.current instanceof HTMLElement &&
              opener.current.isConnected
            )
              opener.current.focus();
          }}
        >
          <Dialog.Title>
            {challenge
              ? "完成双因素验证"
              : initial
                ? "重新登录站点账号"
                : "添加站点账号"}
          </Dialog.Title>
          <Dialog.Description>
            {sessionOnly
              ? "更新此站点的登录会话。已选模型、重命名和路由位置会保留，登录后可继续添加。"
              : "自动识别站点类型，连接后同步可用分组并检测，专用测试 key 不设置额度上限，检测费用由站点账号承担。"}
          </Dialog.Description>
          <button
            type="button"
            className="icon-button detail-close"
            aria-label="关闭账号窗口"
            disabled={busy}
            onClick={dismiss}
          >
            <X size={18} />
          </button>
          <form className="source-form sub-account-form" onSubmit={submit}>
            <fieldset
              className="sub-login-fields"
              disabled={busy || browserBusy || loginSaved}
            >
              {challenge ? (
                <label>
                  六位验证码
                  <input
                    autoFocus
                    aria-label="六位验证码"
                    autoComplete="one-time-code"
                    inputMode="numeric"
                    pattern="[0-9]{6}"
                    maxLength={6}
                    value={code}
                    onChange={(event) => setCode(event.target.value)}
                    required
                  />
                </label>
              ) : (
                <>
                  <label>
                    站点名称
                    <input
                      placeholder="例如：我的上游"
                      maxLength={120}
                      value={name}
                      readOnly={!!initial}
                      onChange={(event) => setName(event.target.value)}
                    />
                  </label>
                  <label>
                    站点地址
                    <input
                      type="url"
                      placeholder="https://api.example.com"
                      value={base}
                      onChange={(event) => {
                        setBase(event.target.value);
                        setBrowserNeeded(false);
                        setError("");
                      }}
                      readOnly={!!initial}
                      required
                    />
                  </label>
                  <label>
                    用户名 / 邮箱
                    <input
                      type="text"
                      autoComplete="username"
                      value={email}
                      onChange={(event) => setEmail(event.target.value)}
                      readOnly={!!initial}
                      required
                    />
                  </label>
                  <label>
                    账号密码
                    <input
                      type="password"
                      aria-label="账号密码"
                      aria-describedby="site-password-note"
                      placeholder={hasSavedPassword ? "已保存密码，留空继续使用" : "输入账号密码"}
                      autoFocus={sessionOnly}
                      autoComplete="current-password"
                      value={password}
                      onChange={(event) => setPassword(event.target.value)}
                      required={!hasSavedPassword && !browserNeeded}
                    />
                  </label>
                  <p className="settings-note" id="site-password-note">
                    {hasSavedPassword
                      ? "密码已加密保存。留空使用已保存密码，填写新密码可更新；会话失效后自动重新登录。"
                      : initial
                        ? "此账号尚未保存登录密码，请补填一次。验证成功后加密保存，会话失效后自动重新登录。"
                        : "验证成功后，密码将加密保存在服务端，用于会话失效时自动重新登录。"}
                  </p>
                </>
              )}
            </fieldset>
            {browserBusy && (
              <p className="sub-login-status" role="status">
                <Spinner small />
                {sessionOnly ? "正在等待原站登录…" : "正在等待原站登录，成功后自动同步…"}
              </p>
            )}
            {error && (
              <div role="alert" className="error-banner">
                {error}
              </div>
            )}
            <div className="sub-account-dialog-actions">
              <button
                type="button"
                className="button small"
                disabled={busy}
                onClick={dismiss}
              >
                取消
              </button>
              <button
                type="submit"
                className="button primary small"
                disabled={busy || browserBusy}
              >
                {busy || browserBusy ? (
                  <Spinner small />
                ) : browserNeeded && !challenge ? (
                  <Globe2 size={14} />
                ) : (
                  <Plus size={14} />
                )}
                {loginSaved
                  ? "重试同步并检测"
                  : challenge
                  ? sessionOnly ? "验证并返回" : "验证并检测"
                  : browserNeeded
                    ? "使用浏览器登录"
                    : sessionOnly ? "登录并返回" : "连接并检测"}
              </button>
            </div>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

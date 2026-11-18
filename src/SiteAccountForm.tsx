import { useEffect, useId, useRef, useState } from "react";
import type { FormEvent } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Globe2 } from "lucide-react";
import { controlRequest } from "./api";
import { CompactDialogHeader } from "./CompactDialog";
import { browserHelperAvailable, loginWithBrowser } from "./sub2apiBrowser";
import { Spinner } from "./ui";
import type { SubAccount } from "./Sub2apiChecks";

export function AccountForm({
  initial,
  close,
  saved,
  sessionOnly = false,
  detectionModels,
}: {
  initial: SubAccount | null;
  close: () => void;
  saved: () => void;
  sessionOnly?: boolean;
  detectionModels?: string[];
}) {
  const endpoint = initial
    ? `/v1/sub2api/accounts/${encodeURIComponent(initial.id)}/login`
    : "/v1/sub2api/accounts/scoped";
  const [detectOnAdd, setDetectOnAdd] = useState(!!detectionModels?.length);
  const initialScope = useRef<string[]>([]);
  const [name, setName] = useState(initial?.name || "");
  const [base, setBase] = useState(initial?.base || "");
  const addressChanged = !!initial && base.trim() !== initial.base;
  const addressUpdate = addressChanged ? { previous_base: initial.base } : {};
  const [email, setEmail] = useState(
    initial?.login_name || initial?.email || "",
  );
  const [password, setPassword] = useState("");
  const hasSavedPassword = initial?.has_saved_password === true;
  const accountID = initial?.id;
  const [credentialsLoading, setCredentialsLoading] =
    useState(hasSavedPassword);
  const [credentialsError, setCredentialsError] = useState("");
  const [browserNeeded, setBrowserNeeded] = useState(false);
  const [browserKind, setBrowserKind] = useState<"sub2api" | "newapi">(
    "sub2api",
  );
  const [helperReady, setHelperReady] = useState(false);
  const [browserBusy, setBrowserBusy] = useState(false);
  const [challenge, setChallenge] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [loginSaved, setLoginSaved] = useState(false);
  const [error, setError] = useState("");
  const noteId = useId();
  const browserAbort = useRef<AbortController | null>(null);
  const opener = useRef(document.activeElement);
  useEffect(() => {
    if (!accountID || !hasSavedPassword) return;
    const abort = new AbortController();
    let active = true;
    setCredentialsLoading(true);
    setCredentialsError("");
    void controlRequest<{ password: string }>(
      `/v1/sub2api/accounts/${encodeURIComponent(accountID)}/credentials`,
      { signal: abort.signal },
    )
      .then((credentials) => {
        if (typeof credentials.password !== "string")
          throw new Error("账号密码返回无效");
        if (active) setPassword(credentials.password);
      })
      .catch(() => {
        if (active)
          setCredentialsError(
            "已保存密码暂时无法显示。留空沿用。",
          );
      })
      .finally(() => {
        if (active) setCredentialsLoading(false);
      });
    return () => {
      active = false;
      abort.abort();
    };
  }, [accountID, hasSavedPassword]);
  useEffect(() => {
    let active = true;
    void browserHelperAvailable(browserKind).then((ready) => {
      if (active) setHelperReady(ready);
    });
    return () => {
      active = false;
      browserAbort.current?.abort();
    };
  }, [browserKind]);
  function dismiss() {
    if (busy) return;
    browserAbort.current?.abort();
    close();
  }
  function confirmAddress(result: { base?: string }) {
    if (addressChanged && !result.base)
      throw new Error(
        "后端尚未确认新站点地址，请待服务更新后重试。尚未启动同步检测。",
      );
  }
  async function finishLogin() {
    setPassword("");
    setChallenge("");
    setCode("");
    setLoginSaved(true);
    if (initial && !sessionOnly) {
      try {
        if (!detectionModels?.length) throw new Error("请在检测模型设置中选择模型");
        await controlRequest(
          `/v1/sub2api/accounts/${encodeURIComponent(initial.id)}/sync-selected`,
          {
            method: "POST",
            body: JSON.stringify({models: detectionModels}),
          },
        );
      } catch (e) {
        throw new Error(
          `登录已保存，同步未启动：${e instanceof Error ? e.message : "请稍后点击同步并检测"}`,
        );
      }
    }
    saved();
  }
  async function browserLogin() {
    if (busy || browserBusy || credentialsLoading) return;
    setBrowserBusy(true);
    setError("");
    const abort = new AbortController();
    browserAbort.current = abort;
    try {
      const available = await browserHelperAvailable(browserKind);
      setHelperReady(available);
      if (!available)
        throw new Error(
          browserKind === "newapi"
            ? "请安装或更新登录助手到 1.3.0 或更新版本，并重新加载扩展和控制台。"
            : "登录助手未连接，请重新加载扩展和控制台后重试。",
        );
      abort.signal.throwIfAborted();
      const auth = await loginWithBrowser(
        { base, email, password, agreed: true, kind: browserKind },
        abort.signal,
      );
      const result = await controlRequest<{ base?: string }>(endpoint, {
        method: "POST",
        // Keep the operator's password through the browser verification step
        // so the server can encrypt it for future automatic sign-ins.
        body: JSON.stringify({
          name,
          base,
          email,
          ...addressUpdate,
          ...(password ? { password } : {}),
          ...auth,
          ...(!initial ? {models: initialScope.current} : {}),
        }),
        signal: abort.signal,
      });
      confirmAddress(result);
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
    if (busy || browserBusy || credentialsLoading) return;
    if (browserNeeded && !challenge && !loginSaved) {
      void browserLogin();
      return;
    }
    setBusy(true);
    setError("");
    try {
      if (!initial && !challenge) {
        if (detectOnAdd && !detectionModels?.length) throw new Error("请在检测模型设置中选择模型");
        initialScope.current = detectOnAdd ? [...(detectionModels ?? [])] : [];
      }
      if (loginSaved) {
        await finishLogin();
        return;
      }
      const result = await controlRequest<{
        base?: string;
        requires_2fa?: boolean;
        challenge?: string;
        requires_browser?: boolean;
        browser_base?: string;
        browser_kind?: "sub2api" | "newapi";
      }>(endpoint, {
        method: "POST",
        body: JSON.stringify(
          challenge
            ? { challenge, totp_code: code }
            : {
                name,
                base,
                email,
                ...addressUpdate,
                ...(password ? { password } : {}),
                ...(!initial ? {models: initialScope.current} : {}),
              },
        ),
      });
      if (result.requires_browser) {
        setBrowserNeeded(true);
        if (result.browser_base) setBase(result.browser_base);
        setBrowserKind(result.browser_kind === "newapi" ? "newapi" : "sub2api");
        setHelperReady(
          await browserHelperAvailable(
            result.browser_kind === "newapi" ? "newapi" : "sub2api",
          ),
        );
      } else if (result.requires_2fa && result.challenge) {
        setChallenge(result.challenge);
        setPassword("");
      } else {
        confirmAddress(result);
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
        <Dialog.Overlay
          className={`dialog-overlay${sessionOnly ? " site-reauth-overlay" : ""}`}
        />
        <Dialog.Content
          className={`guide-dialog compact-dialog${sessionOnly ? " site-reauth-dialog" : ""}`}
          aria-describedby={undefined}
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
          <CompactDialogHeader closeLabel="关闭账号窗口" busy={busy}>
            {challenge
              ? "完成双因素验证"
              : initial
                ? "重新登录站点账号"
                : "添加站点账号"}
          </CompactDialogHeader>
          <form className="compact-dialog-form" onSubmit={submit}>
            <div className="compact-dialog-scroll">
            <fieldset
              className="dialog-field-grid"
              disabled={busy || browserBusy || credentialsLoading || loginSaved}
            >
              {challenge ? (
                <label className="dialog-field dialog-field-full">
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
                  <label className="dialog-field dialog-field-full">
                    {initial ? "站点名称" : "站点名称（选填）"}
                    <input
                      maxLength={120}
                      value={name}
                      readOnly={!!initial}
                      onChange={(event) => setName(event.target.value)}
                    />
                  </label>
                  <label className="dialog-field dialog-field-full">
                    站点地址
                    <input
                      type="url"
                      placeholder="https://api.example.com"
                      value={base}
                      onChange={(event) => {
                        setBase(event.target.value);
                        setBrowserNeeded(false);
                        setBrowserKind("sub2api");
                        setError("");
                      }}
                      readOnly={!!initial && sessionOnly}
                      aria-describedby={addressChanged ? `${noteId}-address` : undefined}
                      required
                    />
                  </label>
                  {addressChanged && (
                    <p className="dialog-inline-note dialog-field-full" role="note" id={`${noteId}-address`}>
                      请核对域名：密码将发送到新地址，已有网关路由不会自动更改。
                    </p>
                  )}
                  <label className="dialog-field">
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
                  <label className="dialog-field">
                    账号密码
                    <input
                      type="text"
                      aria-label="账号密码"
                      aria-describedby={`${noteId}-password`}
                      placeholder={hasSavedPassword ? "已保存密码，留空继续使用" : undefined}
                      autoFocus={sessionOnly}
                      autoComplete="current-password"
                      spellCheck={false}
                      autoCapitalize="none"
                      value={password}
                      onChange={(event) => setPassword(event.target.value)}
                      required={
                        (!hasSavedPassword || addressChanged) && !browserNeeded
                      }
                    />
                  </label>
                  <p className="dialog-inline-note dialog-field-full" id={`${noteId}-password`} role={credentialsError ? "status" : undefined}>
                    {credentialsLoading
                      ? "正在读取已保存密码…"
                      : credentialsError || (initial && !hasSavedPassword
                        ? "此账号尚未保存登录密码，请补填一次。"
                        : "密码加密保存，用于自动重新登录。")}
                  </p>
                </>
              )}
            </fieldset>
            {!initial && !challenge && !browserNeeded && (
              <div className="compact-dialog-options">
                <label className="dialog-check-row">
                  <input type="checkbox" checked={detectOnAdd} disabled={busy || !detectionModels?.length}
                    aria-label="同步并检测已选模型" aria-describedby={detectOnAdd ? `${noteId}-cost` : undefined}
                    onChange={event => setDetectOnAdd(event.target.checked)} />
                  <span>同步后检测模型</span>
                  <small id={`${noteId}-cost`}>{detectOnAdd ? `${detectionModels?.length ?? 0} 个 / 分组 · 可能计费` : "仅同步"}</small>
                </label>
              </div>
            )}
            {browserBusy && (
              <p className="sub-login-status" role="status">
                <Spinner small />
                等待原站登录…
              </p>
            )}
            {browserNeeded && browserKind === "newapi" && helperReady && (
              <p className="dialog-inline-note">
                首次登录需在扩展中授权读取 New API 会话。
              </p>
            )}
            {browserNeeded && !helperReady && (
              <div className="compact-dialog-options">
                <p className="dialog-inline-note">
                <a href="/uni-api-browser-helper-chrome.zip" download>
                  下载 Chrome 登录助手
                </a>
                {" · "}
                <a href="/uni-api-browser-helper-firefox.zip" download>
                  下载 Firefox 登录助手
                </a>
                </p>
                <details className="dialog-inline-note">
                  <summary>安装方法</summary>
                  Chrome 121+：解压后在扩展管理页加载。Firefox 140+：在 about:debugging 临时加载 manifest.json，重启后需重新加载。
                </details>
              </div>
            )}
            {error && (
              <div role="alert" className="error-banner">
                {error}
              </div>
            )}
            </div>
            <div className="compact-dialog-footer">
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
                disabled={busy || browserBusy || credentialsLoading}
              >
                {busy || browserBusy ? (
                  <Spinner small />
                ) : browserNeeded && !challenge ? (
                  <Globe2 size={14} />
                ) : null}
                {loginSaved
                  ? "重试同步并检测"
                  : challenge
                    ? sessionOnly
                      ? "验证并返回"
                      : !initial && !initialScope.current.length ? "验证并同步" : "验证并检测"
                    : browserNeeded
                      ? "使用浏览器登录"
                      : sessionOnly
                        ? "登录并返回"
                        : !initial && !detectOnAdd ? "连接并同步" : "连接并检测"}
              </button>
            </div>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

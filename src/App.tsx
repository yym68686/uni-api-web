import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { ArrowRight, Eye, EyeOff, Globe2, KeyRound, ShieldCheck, TriangleAlert } from "lucide-react";
import { ApiError, cleanBase, controlRequest } from "./api";
import { readKeys, type Keys } from "./requestFilters";
import { Spinner } from "./ui";
import { ConsoleBrand } from "./ConsoleChrome";
import { readAccountSession, type AccountSession } from "./accountSession";
import { clearDashboardSnapshots, restoreDashboardSnapshot } from "./dashboardSnapshot";
import { StartupScreen } from "./StartupScreen";
import { clearSummaryCache } from "./summaryCache";
import { loadConnection, saveConnection, clearConnection } from "./session";
import type { Connection } from "./types";
// During a rolling deploy, an old entry may arrive just before its hashed
// Dashboard asset is retired. Recover once, before any workspace can mount
// or contain edits. Other lazy sections keep their explicit error boundary.
const Dashboard = lazy(() => import("./Dashboard").catch((error: unknown) => {
  const message = error instanceof Error ? error.message : "";
  if (/dynamically imported module|importing a module script failed|error loading dynamically imported module/i.test(message)) {
    try {
      const key = "uni-console:dashboard-module-reload";
      const now = Date.now();
      if (now - Number(sessionStorage.getItem(key) || 0) >= 60_000) {
        sessionStorage.setItem(key, String(now));
        location.reload();
        return new Promise<never>(() => {});
      }
    } catch { /* Still show the explicit recovery action. */ }
  }
  throw error;
}));
function AccountForm({ onConnect, shortSession }: { onConnect: () => void; shortSession: boolean }) {
  const [username, setUsername] = useState(""),
    [password, setPassword] = useState(""),
    [remember, setRemember] = useState(false),
    [pending, setPending] = useState(false),
    [error, setError] = useState("");
  async function submit(e: FormEvent) {
    e.preventDefault();
    setPending(true);
    setError("");
    try {
      await controlRequest("/v1/auth/login", {
        method: "POST",
        body: JSON.stringify({ username, password, browser_session: shortSession && !remember }),
      });
      setPassword("");
      onConnect();
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 401
          ? "用户名或密码错误。"
          : e instanceof Error
            ? e.message
            : "登录失败",
      );
    } finally {
      setPending(false);
    }
  }
  return (
    <form className="connection-form" onSubmit={submit}>
      <label htmlFor="console-username">用户名</label>
      <div className="input-wrap">
        <KeyRound size={18} />
        <input
          id="console-username"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          autoComplete="username"
          required
          disabled={pending}
        />
      </div>
      <label htmlFor="console-password">密码</label>
      <div className="input-wrap">
        <ShieldCheck size={18} />
        <input
          id="console-password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="current-password"
          required
          disabled={pending}
        />
      </div>
      {error && (
        <div role="alert" className="error-banner">
          {error}
        </div>
      )}
      <button className="button primary connect-button" disabled={pending}>
        {pending ? (
          <Spinner small />
        ) : (
          <>
            登录 <ArrowRight size={17} />
          </>
        )}
      </button>
      {shortSession && <label className="session-option" title="公共设备不要保留登录"><input type="checkbox" checked={remember} disabled={pending} onChange={e => setRemember(e.target.checked)} /> 在此设备保留登录 30 天</label>}
    </form>
  );
}
function ConnectionForm({
  onConnect,
  initialBase = "",
  compact = false,
  initialError = "",
}: {
  onConnect: (connection: Connection, keys: Keys) => void;
  initialBase?: string;
  compact?: boolean;
  initialError?: string;
}) {
  const [base, setBase] = useState(initialBase),
    [key, setKey] = useState(""),
    [visible, setVisible] = useState(false);
  const [pending, setPending] = useState(false),
    [error, setError] = useState(initialError);
  const attempt = useRef<AbortController | null>(null);
  useEffect(() => () => attempt.current?.abort(), []);
  async function connect(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    setError("");
    setPending(true);
    const run = new AbortController();
    attempt.current = run;
    try {
      const connection = {
        base: cleanBase(base),
        key: key.trim(),
        session: crypto.randomUUID(),
      };
      if (!connection.key) throw new Error("请输入平台访问密钥。");
      const keys = await readKeys(connection, run.signal);
      if (run.signal.aborted) return;
      onConnect(connection, keys);
      setKey("");
    } catch (e) {
      if (!run.signal.aborted)
        setError(e instanceof Error ? e.message : "连接失败，请重试。");
    } finally {
      if (!run.signal.aborted) setPending(false);
    }
  }
  return (
    <form
      className={`connection-form ${compact ? "compact" : ""}`}
      onSubmit={connect}
    >
      <label htmlFor="service-address">
        服务地址
      </label>
      <div className="input-wrap">
        <Globe2 size={18} />
        <input
          id="service-address"
          type="url"
          placeholder="https://api.example.com"
          value={base}
          onChange={(e) => setBase(e.target.value)}
          autoComplete="url"
          required
          disabled={pending}
          spellCheck={false}
        />
      </div>
      <label htmlFor="access-key">
        访问密钥
      </label>
      <div className="input-wrap">
        <KeyRound size={18} />
        <input
          id="access-key"
          type={visible ? "text" : "password"}
          placeholder="配置中的第一个密钥或管理员密钥"
          value={key}
          onChange={(e) => setKey(e.target.value)}
          autoComplete="off"
          required
          disabled={pending}
          spellCheck={false}
        />
        <button
          type="button"
          className="icon-button"
          onClick={() => setVisible(!visible)}
          aria-label={visible ? "隐藏密钥" : "显示密钥"}
        >
          {visible ? <EyeOff size={17} /> : <Eye size={17} />}
        </button>
      </div>
      {error && (
        <div role="alert" className="error-banner">
          <TriangleAlert size={17} />
          <span>{error}</span>
        </div>
      )}
      <button className="button primary connect-button" disabled={pending}>
        {pending ? (
          <>
            <Spinner small /> 正在验证连接
          </>
        ) : (
          <>
            进入控制台 <ArrowRight size={17} />
          </>
        )}
      </button>
    </form>
  );
}

function Welcome({
  onConnect,
  error,
  legacyForm,
  shortSession = false,
}: {
  onConnect: () => void;
  error: string;
  legacyForm?: ReactNode;
  shortSession?: boolean;
}) {
  return (
    <div className="welcome observatory-welcome">
      <main className="observatory-login">
        <section className="connect-card">
          <ConsoleBrand />
          <h1>{legacyForm ? "连接服务" : "登录控制台"}</h1>
          {legacyForm || <AccountForm onConnect={onConnect} shortSession={shortSession} />}
          {error && (
            <p role="alert" className="error-banner">
              {error}
            </p>
          )}
        </section>
      </main>
    </div>
  );
}

export function LegacyConsole() {
  const [connection, setConnection] = useState<Connection | null>(
    loadConnection,
  );
  const [error, setError] = useState("");
  const [change, setChange] = useState(false);
  const client = useQueryClient();
  const connected = (next: Connection, keys: Keys) => {
    if (connection && connection.base !== next.base) history.replaceState(null, "", location.pathname + location.search);
    client.clear();
    client.setQueryData(["keys", next.session], keys);
    saveConnection(next);
    setConnection(next);
    setError("");
    setChange(false);
  };
  const disconnect = useCallback(
    (reason = "") => {
      history.replaceState(null, "", location.pathname + location.search);
      client.clear();
      clearConnection();
      setConnection(null);
      setError(reason);
      setChange(false);
      document.documentElement.dataset.theme = "light";
    },
    [client],
  );
  return (
    <>
      {connection ? (
        <Suspense fallback={<StartupScreen />}>
<Dashboard
          connection={connection}
          disconnect={disconnect}
          changeConnection={() => setChange(true)}
        />
      </Suspense>
      ) : (
        <Welcome
          onConnect={() => {}}
          error={error}
          legacyForm={<ConnectionForm onConnect={connected} />}
        />
      )}
      <Dialog.Root open={change} onOpenChange={setChange}>
        <Dialog.Portal>
          <Dialog.Overlay className="dialog-overlay" />
          <Dialog.Content className="connection-dialog" aria-describedby={undefined}>
            <Dialog.Title>切换服务连接</Dialog.Title>
            <ConnectionForm
              compact
              initialBase={connection?.base}
              onConnect={connected}
            />
            <Dialog.Close className="button">取消</Dialog.Close>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </>
  );
}

export default function App() {
  const client = useQueryClient();
  const [error, setError] = useState("");
  const auth = useQuery({
    queryKey: ["account"],
    queryFn: async () => {
      const session = await readAccountSession();
      if (session.enabled && session.authenticated) restoreDashboardSnapshot(client, session.username);
      else clearDashboardSnapshots(client);
      return session;
    },
    retry: false,
    staleTime: 30_000,
  });
  const connection = useMemo<Connection>(
    () => ({
      base: globalThis.location.origin,
      key: "",
      session: auth.data?.username || "account",
      account: true,
    }),
    [auth.data?.username],
  );
  const disconnect = useCallback(
    async (reason = "") => {
      try {
        await controlRequest("/v1/auth/logout", { method: "POST", body: "{}" });
      } catch (e) {
        if (!(e instanceof ApiError && e.status === 401)) {
          setError("退出失败，请重试。服务端会话尚未撤销。");
          return;
        }
      }
      await client.cancelQueries();
      history.replaceState(null, "", location.pathname + location.search);
      await clearSummaryCache();
      clearDashboardSnapshots(client);
      client.removeQueries({
        predicate: (query) => query.queryKey[0] !== "account",
      });
      client.setQueryData<AccountSession>(["account"], previous => ({
        ...previous,
        enabled: true,
        authenticated: false,
        username: "",
      }));
      setError(reason);
    },
    [client],
  );
  useEffect(() => {
    if (auth.data?.enabled) clearConnection();
    if (auth.data?.enabled && !auth.data.authenticated) void clearSummaryCache();
  }, [auth.data?.enabled, auth.data?.authenticated]);
  if (auth.isPending) return <StartupScreen />;
  if (auth.isError)
    return <StartupScreen onRetry={() => void auth.refetch()} />;
  if (!auth.data.enabled) return <LegacyConsole />;
  if (!auth.data.authenticated)
    return (
      <Welcome
        onConnect={() => {
          setError("");
          void auth.refetch();
        }}
        error={error}
        shortSession={auth.data.browser_session_supported === true}
      />
    );
  return (
    <>
      {error && (
        <div role="alert" className="error-banner">
          {error}
          <button onClick={() => void disconnect()}>重试退出</button>
        </div>
      )}
      <Suspense fallback={<StartupScreen />}>
<Dashboard
        key={connection.session}
        connection={connection}
        disconnect={(reason) => void disconnect(reason)}
        changeConnection={() => {}}
      />
      </Suspense>
    </>
  );
}

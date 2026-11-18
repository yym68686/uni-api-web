import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { m as motion } from "motion/react";
import * as Dialog from "@radix-ui/react-dialog";
import { ArrowRight, ArrowUpRight, Check, Command, Eye, EyeOff, Gauge, Globe2, KeyRound, Layers3, Link2, ShieldCheck, TriangleAlert, Wallet } from "lucide-react";
import { ApiError, cleanBase, controlRequest } from "./api";
import { readKeys, type Keys } from "./requestFilters";
import { Brand, Spinner } from "./ui";
import { readAccountSession, type AccountSession } from "./accountSession";
import { clearDashboardSnapshots, restoreDashboardSnapshot } from "./dashboardSnapshot";
import { StartupScreen } from "./StartupScreen";
import { clearSummaryCache } from "./summaryCache";
import { loadConnection, saveConnection, clearConnection } from "./session";
import type { Connection } from "./types";
const Dashboard = lazy(() => import("./Dashboard"));
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
      {shortSession && <label className="session-option"><input type="checkbox" checked={remember} disabled={pending} onChange={e => setRemember(e.target.checked)} /> 在此设备保留登录 30 天</label>}
      <p className="connection-help">{shortSession ? "默认仅本次浏览器会话，最长 8 小时。" : "当前服务使用 30 天会话。"}公共设备使用完请主动退出。</p>
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
        服务地址 <span>UNI-API ENDPOINT</span>
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
        访问密钥 <span>PLATFORM KEY</span>
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
      <p className="field-note">
        <ShieldCheck size={14} />
        密钥保存在当前标签页会话中，断开连接时清除。
      </p>
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
      <p className="connection-help">使用普通业务密钥无法查看平台数据。</p>
    </form>
  );
}

const reveal = {
  initial: { opacity: 0, y: 12 },
  animate: { opacity: 1, y: 0 },
  transition: { duration: 0.32 },
};

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
    <div className="welcome">
      <header className="welcome-header">
        <Brand />
        <a
          className="text-link"
          href="https://github.com/yym68686/uni-api-web"
          target="_blank"
          rel="noreferrer"
        >
          GitHub <ArrowUpRight size={16} />
        </a>
      </header>
      <main className="welcome-main">
        <motion.section {...reveal} className="welcome-intro">
          <span className="eyebrow">
            <span className="dot" /> YOUR MODELS. ONE CLEAR VIEW.
          </span>
          <h1>
            看清每一条路由。
            <br />
            <span>掌控每一次请求。</span>
          </h1>
          <p className="welcome-description">
            将模型、渠道与请求表现连在一起。
            <br />
            用一个清晰的工作台，发现等待、定位异常、了解余额。
          </p>
          <div className="route-visual" aria-hidden="true">
            <div className="route-line line-one" />
            <div className="route-line line-two" />
            <div className="visual-label">A CLEARER PATH FOR EVERY REQUEST</div>
            <div className="route-node origin">
              <Command size={22} />
              <span>应用请求</span>
            </div>
            <div className="route-node gateway">
              <Brand compact />
              <span>uni-api</span>
              <small>路由观测</small>
            </div>
            <div className="route-destinations">
              {["首字延迟", "请求前等待", "渠道余额"].map((label, i) => (
                <div className="route-node destination" key={label}>
                  <span className={`node-dot tone-${i}`} />
                  {label}
                  <Check size={14} />
                </div>
              ))}
            </div>
            <div className="visual-footer">
              <Layers3 size={14} /> 多模型 · 多渠道 · 一个视图
            </div>
          </div>
          <div className="welcome-features">
            <span>
              <Gauge size={17} />
              清晰的性能指标
            </span>
            <span>
              <Wallet size={17} />
              独立余额查询
            </span>
            <span>
              <ShieldCheck size={17} />
              安全连接你的服务
            </span>
          </div>
        </motion.section>
        <motion.section
          {...reveal}
          transition={{ delay: 0.09, duration: 0.35 }}
          className="connect-card"
        >
          <div className="connect-icon">
            <Link2 size={23} />
          </div>
          <h2>{legacyForm ? "直连 uni-api 服务" : "登录 uni-api console"}</h2>
          <p>{legacyForm ? "输入服务地址和平台密钥，查看此服务的数据。" : "使用控制台账号管理多个 uni-api 来源。"}</p>
          {legacyForm || <AccountForm onConnect={onConnect} shortSession={shortSession} />}
          {error && (
            <p role="alert" className="error-banner">
              {error}
            </p>
          )}
          <div className="connect-card-footer">
            <span className="tiny-dot" /> {legacyForm ? "单一服务 · 平台密钥直连" : "多来源分析 · 控制台账户"}
          </div>
        </motion.section>
      </main>
      <footer className="welcome-footer">
        <span>
          uni-api console <span className="muted">/</span>{" "}
          为每一次模型调用带来清晰视野
        </span>
        <span>OPEN SOURCE · BUILT FOR CLARITY</span>
      </footer>
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
          <Dialog.Content className="connection-dialog">
            <Dialog.Title>切换服务连接</Dialog.Title>
            <Dialog.Description>验证成功后切换连接。</Dialog.Description>
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

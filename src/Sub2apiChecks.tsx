import { useMemo, useState } from "react";
import type { FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Check,
  ChevronDown,
  CircleHelp,
  Globe2,
  Plus,
  RefreshCw,
  ScanLine,
  Search,
  Square,
  Trash2,
  X,
} from "lucide-react";
import { controlRequest } from "./api";
import { LatencyBadge } from "./LatencyBadge";
import { Empty, Spinner, Tip } from "./ui";
import { time } from "./format";

interface Probe {
  status: string;
  text: string;
  message?: string;
  ttft_ms: number | null;
  duration_ms: number;
  http_status?: number;
}
interface Result {
  model: string;
  checked_at: number;
  availability: Probe;
  quality: Probe;
  verdict: string;
}
export interface SubTarget {
  group_id: number;
  name: string;
  platform: string;
  channel: string;
  rate: number;
  billing?: { rate: number | null; source: string; checked_at: number } | null;
  key_id: number;
  active: boolean;
  state: string;
  message: string;
  result: Result | null;
}
export interface SubAccount {
  id: string;
  name: string;
  base: string;
  email: string;
  state: string;
  message: string;
  synced_at: number;
  targets: SubTarget[];
}
const pending = (state: string) => state === "queued" || state === "running";
const stateLabel: Record<string, string> = {
  idle: "已同步",
  queued: "等待检测",
  running: "检测中",
  stopped: "已停止",
  interrupted: "已中断",
  error: "同步失败",
};
const latency = (value: number | null) =>
  value === null
    ? "—"
    : value < 1000
      ? `${value} ms`
      : `${(value / 1000).toFixed(2)} s`;

function AccountForm({
  initial,
  close,
  saved,
}: {
  initial: SubAccount | null;
  close: () => void;
  saved: () => void;
}) {
  const [name, setName] = useState(initial?.name || "");
  const [base, setBase] = useState(initial?.base || "");
  const [email, setEmail] = useState(initial?.email || "");
  const [password, setPassword] = useState("");
  const [advanced, setAdvanced] = useState(false);
  const [access, setAccess] = useState("");
  const [refresh, setRefresh] = useState("");
  const [challenge, setChallenge] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      const result = await controlRequest<{
        requires_2fa?: boolean;
        challenge?: string;
      }>("/v1/sub2api/accounts", {
        method: "POST",
        body: JSON.stringify(
          challenge
            ? { challenge, totp_code: code }
            : {
                name,
                base,
                email,
                ...(advanced
                  ? { access_token: access, refresh_token: refresh }
                  : { password }),
              },
        ),
      });
      if (result.requires_2fa && result.challenge) {
        setChallenge(result.challenge);
        setPassword("");
        setAccess("");
        setRefresh("");
      } else {
        setPassword("");
        setAccess("");
        setRefresh("");
        saved();
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "连接失败");
    } finally {
      setBusy(false);
    }
  }
  return (
    <form className="source-form sub-account-form" onSubmit={submit}>
      <h3>
        {challenge
          ? "完成双因素验证"
          : initial
            ? "重新登录站点账号"
            : "添加 sub2api 账号"}
      </h3>
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
            onChange={(e) => setCode(e.target.value)}
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
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <label>
            站点地址
            <input
              type="url"
              placeholder="https://api.example.com"
              value={base}
              onChange={(e) => setBase(e.target.value)}
              readOnly={!!initial}
              required
            />
          </label>
          <label>
            账号邮箱
            <input
              type="email"
              autoComplete="username"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              readOnly={!!initial}
              required
            />
          </label>
          {!advanced && (
            <label>
              账号密码
              <input
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </label>
          )}
          <details onToggle={(e) => setAdvanced(e.currentTarget.open)}>
            <summary>站点要求验证码？使用已登录会话接入</summary>
            <p>
              在 sub2api 站点完成登录后，填入该账号的
              access_token；refresh_token 可选。这里需要登录会话令牌，不是模型
              API key。
            </p>
            <div className="source-storage-fields">
              <label>
                访问令牌
                <input
                  type="password"
                  autoComplete="off"
                  value={access}
                  onChange={(e) => setAccess(e.target.value)}
                  required={advanced}
                />
              </label>
              <label>
                刷新令牌（可选）
                <input
                  type="password"
                  autoComplete="off"
                  value={refresh}
                  onChange={(e) => setRefresh(e.target.value)}
                />
              </label>
            </div>
          </details>
        </>
      )}
      {error && (
        <div role="alert" className="error-banner">
          {error}
        </div>
      )}
      <div className="sub-form-footer">
        <span>
          连接后为所有可用分组创建或复用专用 key，并自动检测。每个新 key
          的累计额度为 $1。
        </span>
        <div>
          <button
            className="button small"
            type="button"
            onClick={close}
            disabled={busy}
          >
            取消
          </button>
          <button className="button primary small" disabled={busy}>
            {busy ? <Spinner small /> : <Plus size={14} />}
            {challenge ? "验证并检测" : "连接并检测"}
          </button>
        </div>
      </div>
    </form>
  );
}

function Verdict({ result }: { result: Result | null }) {
  if (!result) return <span className="muted">未检测</span>;
  const pass = result.verdict === "pass",
    fail = result.verdict === "fail";
  return (
    <span
      className={`check-status ${pass ? "pass" : fail ? "fail" : "inconclusive"}`}
    >
      {pass ? (
        <Check size={16} />
      ) : fail ? (
        <X size={16} />
      ) : (
        <CircleHelp size={16} />
      )}
      {pass
        ? "不降智"
        : fail
          ? "降智"
          : result.quality.status === "skipped"
            ? "未检测"
            : result.quality.status === "error"
              ? "检测失败"
              : "无法判定"}
    </span>
  );
}

export function Sub2apiChecks() {
  const client = useQueryClient();
  const query = useQuery({
    queryKey: ["sub2api"],
    queryFn: ({ signal }) =>
      controlRequest<{ data: SubAccount[] }>("/v1/sub2api/accounts", {
        signal,
      }),
    retry: false,
    refetchInterval: (q) =>
      q.state.data?.data.some((a) => pending(a.state)) ? 1500 : 15000,
  });
  const accounts = query.data?.data || [];
  const [form, setForm] = useState<{ account: SubAccount | null } | null>(null);
  const [search, setSearch] = useState("");
  const [accountId, setAccountId] = useState("");
  const [availability, setAvailability] = useState("");
  const [quality, setQuality] = useState("");
  const [error, setError] = useState("");
  const [action, setAction] = useState("");
  const [removing, setRemoving] = useState<string | null>(null);
  const [page, setPage] = useState(0);
  const rows = useMemo(
    () =>
      accounts
        .flatMap((account) =>
          account.targets.map((target) => ({ account, target })),
        )
        .filter(
          ({ account, target }) =>
            (!accountId || account.id === accountId) &&
            `${account.name} ${account.email} ${target.name} ${target.channel} ${target.platform}`
              .toLowerCase()
              .includes(search.toLowerCase()) &&
            (!availability ||
              (availability === "untested"
                ? !target.result
                : target.result?.availability.status === availability)) &&
            (!quality || target.result?.verdict === quality),
        ),
    [accounts, search, accountId, availability, quality],
  );
  const currentPage = Math.min(
    page,
    Math.max(0, Math.ceil(rows.length / 25) - 1),
  );
  const eligible = rows.filter(
    ({ account, target }) =>
      target.active &&
      target.key_id > 0 &&
      !pending(account.state) &&
      target.state !== "error",
  );
  const busyAccounts = accounts.filter((a) => pending(a.state));
  async function mutate(
    key: string,
    path: string,
    body?: unknown,
    method = "POST",
  ) {
    if (action) return;
    setAction(key);
    setError("");
    try {
      await controlRequest(path, {
        method,
        ...(body ? { body: JSON.stringify(body) } : {}),
      });
      await client.invalidateQueries({ queryKey: ["sub2api"] });
      setRemoving(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "操作失败");
    } finally {
      setAction("");
    }
  }
  const check = (selection: typeof rows) =>
    mutate("check", "/v1/sub2api/checks", {
      targets: selection.map(({ account, target }) => ({
        account_id: account.id,
        group_id: target.group_id,
      })),
    });
  return (
    <div className="sub2api-page">
      <section className="data-panel sub-accounts">
        <div className="data-heading">
          <div className="data-title">
            <Globe2 size={19} />
            <h2>站点账号</h2>
            <span className="count-badge">{accounts.length}</span>
          </div>
          <button
            className="button primary small"
            onClick={() => setForm({ account: null })}
          >
            <Plus size={15} />
            添加账号
          </button>
        </div>
        {form && (
          <AccountForm
            key={form.account?.id || "new"}
            initial={form.account}
            close={() => setForm(null)}
            saved={() => {
              setForm(null);
              void client.invalidateQueries({ queryKey: ["sub2api"] });
            }}
          />
        )}
        {query.error && (
          <div role="alert" className="error-banner">
            {query.error.message}
            <button
              className="button small"
              onClick={() => void query.refetch()}
            >
              重试
            </button>
          </div>
        )}
        {query.isPending ? (
          <div className="sub-loading">
            <Spinner />
            正在读取账号
          </div>
        ) : accounts.length === 0 ? (
          <Empty title="添加第一个 sub2api 账号" icon={<Globe2 size={25} />}>
            填入站点地址与账号密码，自动发现可用分组并检测 gpt-6-astra。
          </Empty>
        ) : (
          <div className="sub-account-list">
            {accounts.map((a) => {
              const completed = a.targets.filter(
                  (t) => t.active && t.state === "done",
                ).length,
                total = a.targets.filter((t) => t.active).length;
              return (
                <article className="sub-account" key={a.id}>
                  <div className="sub-account-identity">
                    <strong>{a.name}</strong>
                    <small>
                      {a.email} · {new URL(a.base).host}
                    </small>
                    <span
                      className="sub-account-progress"
                      role={pending(a.state) ? "status" : undefined}
                    >
                      {pending(a.state) && <Spinner small />}
                      {stateLabel[a.state] || a.state}
                      {total > 0 && ` · ${completed}/${total} 个分组`}
                      {a.synced_at > 0 &&
                        !pending(a.state) &&
                        ` · ${time(a.synced_at)}`}
                    </span>
                    {a.message && (
                      <small className="sub-account-message">{a.message}</small>
                    )}
                  </div>
                  <div className="sub-account-actions">
                    {pending(a.state) ? (
                      <button
                        className="button small"
                        disabled={!!action}
                        onClick={() =>
                          void mutate(a.id, `/v1/sub2api/accounts/${a.id}/stop`)
                        }
                      >
                        <Square size={13} />
                        停止
                      </button>
                    ) : (
                      <>
                        <button
                          className="button small"
                          disabled={!!action}
                          onClick={() =>
                            void mutate(
                              a.id,
                              `/v1/sub2api/accounts/${a.id}/sync`,
                            )
                          }
                        >
                          <RefreshCw size={13} />
                          同步并检测
                        </button>
                        <button
                          className="button ghost small"
                          onClick={() => setForm({ account: a })}
                        >
                          重新登录
                        </button>
                        <button
                          className="icon-button"
                          aria-label={`移除账号 ${a.name}`}
                          disabled={!!action}
                          onClick={() => setRemoving(a.id)}
                        >
                          <Trash2 size={15} />
                        </button>
                      </>
                    )}
                  </div>
                  {removing === a.id && (
                    <div className="sub-remove" role="alert">
                      移除后删除本地凭据和检测记录；上游测试 key
                      保留，可在站点密钥页面撤销。
                      <button
                        className="button small"
                        disabled={!!action}
                        onClick={() =>
                          void mutate(
                            a.id,
                            `/v1/sub2api/accounts/${a.id}`,
                            undefined,
                            "DELETE",
                          )
                        }
                      >
                        确认移除
                      </button>
                      <button
                        className="button small"
                        onClick={() => setRemoving(null)}
                      >
                        取消
                      </button>
                    </div>
                  )}
                </article>
              );
            })}
          </div>
        )}
      </section>
      {error && (
        <div role="alert" className="error-banner">
          {error}
        </div>
      )}
      <section className="data-panel">
        <div className="data-heading">
          <div className="data-title">
            <ScanLine size={19} />
            <h2>分组检测</h2>
            <span className="count-badge">{rows.length}</span>
          </div>
          <div className="data-actions">
            <label className="search-field">
              <Search size={16} />
              <input
                aria-label="搜索 sub2api 分组"
                placeholder="搜索站点、分组、渠道…"
                value={search}
                onChange={(e) => {
                  setSearch(e.target.value);
                  setPage(0);
                }}
              />
            </label>
            <button
              className="button small"
              aria-label="刷新 sub2api 检测"
              onClick={() => void query.refetch()}
            >
              <RefreshCw size={15} />
            </button>
            <button
              className="button primary small"
              disabled={!eligible.length || !!action || eligible.length > 500}
              onClick={() => void check(eligible)}
            >
              <ScanLine size={15} />
              一键检测 · {eligible.length}
            </button>
          </div>
        </div>
        <div className="filters sub-filters">
          <label className="select-field">
            <Globe2 size={15} />
            <select
              aria-label="sub2api 账号筛选"
              value={accountId}
              onChange={(e) => {
                setAccountId(e.target.value);
                setPage(0);
              }}
            >
              <option value="">全部账号</option>
              {accounts.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name} · {a.email}
                </option>
              ))}
            </select>
            <ChevronDown size={13} />
          </label>
          <label className="select-field">
            <select
              aria-label="可用性筛选"
              value={availability}
              onChange={(e) => {
                setAvailability(e.target.value);
                setPage(0);
              }}
            >
              <option value="">全部可用性</option>
              <option value="success">可用</option>
              <option value="error">检测失败</option>
              <option value="untested">未检测</option>
            </select>
            <ChevronDown size={13} />
          </label>
          <label className="select-field">
            <select
              aria-label="降智筛选"
              value={quality}
              onChange={(e) => {
                setQuality(e.target.value);
                setPage(0);
              }}
            >
              <option value="">全部降智结果</option>
              <option value="pass">不降智</option>
              <option value="fail">降智</option>
              <option value="inconclusive">无法判定</option>
              <option value="error">检测失败</option>
            </select>
            <ChevronDown size={13} />
          </label>
          <span className="sub-model">gpt-6-astra · Responses</span>
        </div>
        <div className="check-explanation">
          <p>
            每个分组独立测试：流式「say
            test」测可用性与首字延迟，成功后再测知识截止时间。首字延迟从检测服务发出请求计至首个文本增量。检测会消耗上游额度。
          </p>
          <p>
            回复包含「未知」显示绿勾，包含「2024-06」显示红叉；同时包含或均不包含则无法判定。此规则仅供参考，结果代表分组，不代表内部全部上游账号。
            {busyAccounts.length > 0 &&
              " 任务在后台运行，刷新或切换页面后可继续查看。"}
          </p>
        </div>
        {rows.length === 0 ? (
          <Empty title={accounts.length ? "没有匹配的分组" : "尚无检测结果"}>
            添加账号或调整筛选后查看。未同步出分组时，可在账号卡片查看原因。
          </Empty>
        ) : (
          <>
            <div className="table-scroll">
              <table className="channel-table check-table sub-table">
                <thead>
                  <tr>
                    <th>站点 / 分组</th>
                    <th>平台 / key</th>
                    <th>倍率</th>
                    <th>可用性</th>
                    <th>首字延迟</th>
                    <th>降智</th>
                    <th>回复 / 诊断</th>
                    <th>最近检测</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {rows
                    .slice(currentPage * 25, (currentPage + 1) * 25)
                    .map(({ account, target: t }) => (
                      <tr key={`${account.id}:${t.group_id}`}>
                        <td>
                          <strong>{t.name}</strong>
                          <small className="check-source">
                            {account.name}
                            {t.channel ? ` · ${t.channel}` : ""}
                          </small>
                          <small className="check-source">
                            分组 #{t.group_id}
                            {!t.active && " · 已不可用"}
                          </small>
                        </td>
                        <td>
                          {t.platform}
                          <small className="check-source">
                            {t.key_id ? `key #${t.key_id}` : "尚未创建"}
                          </small>
                        </td>
                        <td
                          className="mono"
                          title={
                            t.billing?.checked_at
                              ? `倍率采集于 ${time(t.billing.checked_at)}`
                              : "上游未提供可确认的有效倍率"
                          }
                        >
                          {t.billing?.rate != null ? t.billing.rate : "—"}
                        </td>
                        <td>
                          {pending(t.state) ? (
                            <span className="check-status">
                              <Spinner small />
                              {t.state === "queued" ? "排队中" : "检测中"}
                            </span>
                          ) : t.state === "error" ||
                            t.state === "interrupted" ? (
                            <span className="check-status error">
                              {t.state === "error" ? "同步失败" : "已中断"}
                            </span>
                          ) : t.result ? (
                            <span
                              className={`check-status ${t.result.availability.status === "success" ? "pass" : "fail"}`}
                            >
                              {t.result.availability.status === "success" ? (
                                <Check size={15} />
                              ) : (
                                <X size={15} />
                              )}
                              {t.result.availability.status === "success"
                                ? "可用"
                                : "检测失败"}
                            </span>
                          ) : (
                            <span className="muted">未检测</span>
                          )}
                        </td>
                        <td className="mono">
                          <LatencyBadge
                            value={t.result?.availability.ttft_ms}
                          />
                        </td>
                        <td>
                          <Verdict result={t.result} />
                        </td>
                        <td className="check-answer">
                          {t.message ||
                            t.result?.availability.message ||
                            t.result?.quality.message ||
                            t.result?.quality.text ||
                            "—"}
                          {t.result && (
                            <details>
                              <summary>检测详情</summary>
                              <div>
                                可用性回复：{t.result.availability.text || "—"}
                              </div>
                              <div>
                                降智回复：{t.result.quality.text || "—"}
                              </div>
                              <div>
                                可用性耗时：
                                {latency(t.result.availability.duration_ms)}
                              </div>
                              <div>
                                降智检测耗时：
                                {latency(t.result.quality.duration_ms)}
                              </div>
                              {t.result.quality.message && (
                                <div>{t.result.quality.message}</div>
                              )}
                            </details>
                          )}
                        </td>
                        <td className="mono">
                          {t.result ? time(t.result.checked_at) : "—"}
                          {pending(t.state) && t.result && (
                            <small className="check-source">上次结果</small>
                          )}
                        </td>
                        <td>
                          <button
                            className="button small"
                            aria-label={`检测 ${account.name} ${t.name}`}
                            disabled={
                              !!action ||
                              pending(account.state) ||
                              !t.active ||
                              !t.key_id ||
                              t.state === "error"
                            }
                            onClick={() => void check([{ account, target: t }])}
                          >
                            <ScanLine size={13} />
                            重新检测
                          </button>
                        </td>
                      </tr>
                    ))}
                </tbody>
              </table>
            </div>
            <div className="table-footer">
              <span>
                共 {rows.length} 个分组 · {eligible.length} 个可发起检测
              </span>
              <div className="sub-pagination">
                <button
                  className="button small"
                  disabled={currentPage === 0}
                  onClick={() => setPage(currentPage - 1)}
                >
                  上一页
                </button>
                <span>
                  {currentPage + 1} / {Math.max(1, Math.ceil(rows.length / 25))}
                </span>
                <button
                  className="button small"
                  disabled={(currentPage + 1) * 25 >= rows.length}
                  onClick={() => setPage(currentPage + 1)}
                >
                  下一页
                </button>
              </div>
            </div>
          </>
        )}
      </section>
      <p className="settings-note">
        密码仅用于本次登录；登录会话和测试 key 加密保存。重新同步复用专用
        key，不修改已有业务 key。
        <Tip text="专用 key 的 $1 是站点记账单位的累计消费上限。达到上限后需在上游调整额度，再重新同步；不会自动补充额度。">
          <span>
            测试额度说明 <CircleHelp size={12} />
          </span>
        </Tip>
      </p>
    </div>
  );
}

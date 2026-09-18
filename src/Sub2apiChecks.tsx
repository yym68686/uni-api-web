import { useEffect, useMemo, useState } from "react";
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
import { ResponseLatency } from "./LatencyBadge";
import { Empty, Spinner, Tip } from "./ui";
import { loadSubFilters, saveSubFilters } from "./sub2apiPreferences";
import { useSubImports } from "./sub2apiImports";
import { Sub2apiImport } from "./Sub2apiImport";
import { SUB_MODELS } from "./sub2apiModels";
import {
  modelChecks,
  availabilityCounts,
  modelMatchStatus,
  modelMatchLabels,
} from "./sub2apiResults";
import type { SubModelCheck } from "./sub2apiResults";
import { time } from "./format";

interface Probe {
  requested_model?: string;
  response_model?: string;
  model_match?: "match" | "mismatch" | "missing" | "invalid" | "unavailable";
  status: string;
  text: string;
  message?: string;
  ttft_ms: number | null;
  response_created_ms?: number | null;
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
  models?: {
    model: string;
    state: string;
    message: string;
    result: Result | null;
  }[];
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
  if (result?.verdict === "not_applicable")
    return <span className="muted">不适用</span>;
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

function AvailabilityStatus({ check }: { check: SubModelCheck }) {
  if (pending(check.state))
    return (
      <span className="check-status">
        <Spinner small />
        {check.state === "queued" ? "排队中" : "检测中"}
      </span>
    );
  if (check.state === "interrupted" || check.state === "error")
    return (
      <span className="check-status error">
        {check.state === "interrupted" ? "已中断" : "检测失败"}
      </span>
    );
  if (!check.result) return <span className="muted">未检测</span>;
  const success = check.result.availability.status === "success";
  return (
    <span className={`check-status ${success ? "pass" : "fail"}`}>
      {success ? <Check size={15} /> : <X size={15} />}
      {success ? "可用" : "检测失败"}
    </span>
  );
}

function GroupAvailability({ checks }: { checks: SubModelCheck[] }) {
  const counts = availabilityCounts(checks);
  return (
    <div className="sub-availability-summary">
      <span
        className={`check-status ${counts.success ? "pass" : "inconclusive"}`}
      >
        {counts.success}/{checks.length} 可用
      </span>
      <small className="check-source">
        {[
          counts.failed ? `${counts.failed} 个失败` : "",
          counts.untested ? `${counts.untested} 个未检测` : "",
        ]
          .filter(Boolean)
          .join(" · ")}
      </small>
      {counts.pending > 0 && (
        <small className="check-status">
          <Spinner small />
          {counts.pending} 个排队 / 检测中
        </small>
      )}
      {counts.interrupted > 0 && (
        <small className="check-source">{counts.interrupted} 个中断</small>
      )}
    </div>
  );
}

function ModelMatch({ check }: { check: SubModelCheck }) {
  const status = modelMatchStatus(check);
  const label = modelMatchLabels[status];
  const probe = check.result?.availability;
  return (
    <span
      className={`check-status ${status === "match" ? "pass" : status === "mismatch" ? "fail" : "inconclusive"}`}
      title={`请求模型：${probe?.requested_model || check.model}\n返回模型：${probe?.response_model || "—"}`}
    >
      {status === "match" ? (
        <Check size={15} />
      ) : status === "mismatch" ? (
        <X size={15} />
      ) : (
        <CircleHelp size={15} />
      )}
      {label}
    </span>
  );
}

function GroupModelMatch({ checks }: { checks: SubModelCheck[] }) {
  const statuses = checks.map(modelMatchStatus);
  const matches = statuses.filter((s) => s === "match").length;
  return (
    <div className="sub-model-match-summary">
      <span className={`check-status ${matches ? "pass" : "inconclusive"}`}>
        {matches}/{checks.length} 匹配
      </span>
      {(
        [
          "mismatch",
          "missing",
          "invalid",
          "unavailable",
          "legacy",
          "untested",
        ] as const
      ).map((status) => {
        const count = statuses.filter((s) => s === status).length;
        return count ? (
          <small
            className={`check-source ${status === "mismatch" ? "negative" : ""}`}
            key={status}
          >
            {count} 个{modelMatchLabels[status]}
          </small>
        ) : null;
      })}
    </div>
  );
}

function ProbeDetails({ check }: { check: SubModelCheck }) {
  return (
    <>
      {check.message && <div>{check.message}</div>}
      {check.result && (
        <>
          <div>
            请求模型：{check.result.availability.requested_model || check.model}
          </div>
          <div>返回模型：{check.result.availability.response_model || "—"}</div>
          <div>
            模型匹配：
            <ModelMatch check={check} />
          </div>
          <div>可用性回复：{check.result.availability.text || "—"}</div>
          {check.result.availability.message && (
            <div>{check.result.availability.message}</div>
          )}
          <div>
            可用性耗时：{latency(check.result.availability.duration_ms)}
          </div>
          {check.model === "gpt-6-astra" && (
            <>
              <div>降智回复：{check.result.quality.text || "—"}</div>
              <div>
                降智检测耗时：{latency(check.result.quality.duration_ms)}
              </div>
              {check.result.quality.message && (
                <div>{check.result.quality.message}</div>
              )}
            </>
          )}
        </>
      )}
    </>
  );
}

function ModelResults({ checks }: { checks: SubModelCheck[] }) {
  return (
    <details className="sub-model-results">
      <summary>各模型结果</summary>
      {checks.map((check) => (
        <div className="sub-model-result" key={check.model}>
          <strong>{check.model}</strong>
          <div className="sub-model-result-status">
            <AvailabilityStatus check={check} />
            <ModelMatch check={check} />
            <ResponseLatency
              created={check.result?.availability.response_created_ms}
              text={check.result?.availability.ttft_ms}
            />
          </div>
          <ProbeDetails check={check} />
          {check.result && (
            <small>
              最近检测 {time(check.result.checked_at)}
              {pending(check.state) && " · 上次结果"}
            </small>
          )}
        </div>
      ))}
    </details>
  );
}

export function Sub2apiChecks({ user = "account" }: { user?: string }) {
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
  const [filters, setFilters] = useState(() => loadSubFilters(user));
  const {
    search,
    accountId,
    model,
    maxRate,
    sort,
    availability,
    quality,
    platform,
  } = filters;
  useEffect(() => saveSubFilters(user, filters), [user, filters]);
  const setSearch = (search: string) => setFilters((v) => ({ ...v, search }));
  const setAccountId = (accountId: string) =>
    setFilters((v) => ({ ...v, accountId }));
  const setModel = (model: string) => setFilters((v) => ({ ...v, model }));
  const setMaxRate = (maxRate: string) =>
    setFilters((v) => ({ ...v, maxRate }));
  const setSort = (sort: string) => setFilters((v) => ({ ...v, sort }));
  const setAvailability = (availability: string) =>
    setFilters((v) => ({ ...v, availability }));
  const setQuality = (quality: string) =>
    setFilters((v) => ({ ...v, quality }));
  const setPlatform = (platform: string) =>
    setFilters((v) => ({ ...v, platform }));
  const imports = useSubImports();
  const [importing, setImporting] = useState<{
    account: SubAccount;
    target: SubTarget;
  } | null>(null);
  const [error, setError] = useState("");
  const [action, setAction] = useState("");
  const [removing, setRemoving] = useState<string | null>(null);
  const [page, setPage] = useState(0);
  const candidateRows = useMemo(
    () =>
      accounts
        .flatMap((account) =>
          account.targets.map((target) => {
            const checks = modelChecks(target);
            return {
              account,
              target,
              checks,
              selected: checks.find((c) => c.model === model),
              astra: checks[0],
            };
          }),
        )
        .filter(
          ({ account, target, checks, selected, astra }) =>
            (!accountId || account.id === accountId) &&
            `${account.name} ${account.email} ${target.name} ${target.channel} ${target.platform}`
              .toLowerCase()
              .includes(search.toLowerCase()) &&
            (!availability ||
              (selected ? [selected] : checks).some((check) =>
                availability === "untested"
                  ? !check.result
                  : check.result?.availability.status === availability,
              )) &&
            (!quality || astra.result?.verdict === quality),
        ),
    [accounts, search, accountId, availability, quality, model],
  );
  const rates = [
    ...new Set(
      candidateRows
        .filter(({ target }) => !platform || target.platform === platform)
        .map(({ target }) => target.billing?.rate)
        .filter((v): v is number => v != null && Number.isFinite(v)),
    ),
  ].sort((a, b) => a - b);
  const effectiveMaxRate = maxRate;
  if (maxRate && !rates.includes(Number(maxRate))) rates.push(Number(maxRate));
  rates.sort((a, b) => a - b);
  const withinRate = ({ target }: (typeof candidateRows)[number]) =>
    !effectiveMaxRate ||
    (target.billing?.rate != null &&
      target.billing.rate <= Number(effectiveMaxRate));
  const platforms = [
    ...new Set(
      candidateRows
        .filter(withinRate)
        .map(({ target }) => target.platform)
        .filter(Boolean),
    ),
  ].sort();
  const rows = candidateRows.filter(
    (row) => withinRate(row) && (!platform || row.target.platform === platform),
  );
  if (sort)
    rows.sort((a, b) => {
      const x = a.target.billing?.rate,
        y = b.target.billing?.rate;
      if (x == null) return y == null ? 0 : 1;
      if (y == null) return -1;
      return sort === "asc" ? x - y : y - x;
    });
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
  const check = (selection: typeof rows) => {
    // Filters select groups; only the explicit model selector chooses the probe
    // scope. Historical success/quality must not exclude untested sibling models.
    return mutate("check", "/v1/sub2api/checks", {
      targets: selection.map(({ account, target }) => ({
        account_id: account.id,
        group_id: target.group_id,
        models: model ? [model] : [...SUB_MODELS],
      })),
    });
  };
  const checkQuality = () =>
    mutate("quality-check", "/v1/sub2api/quality-checks", {
      targets: eligible.map(({ account, target }) => ({
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
            填入站点地址与账号密码，自动发现可用分组并检测六个模型。
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
            <h2>模型检测</h2>
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
              aria-label={`降智检测 · ${eligible.length} 个渠道`}
              disabled={!eligible.length || !!action || eligible.length > 500}
              onClick={() => void checkQuality()}
            >
              {action === "quality-check" ? (
                <Spinner small />
              ) : (
                <ScanLine size={15} />
              )}
              降智检测 · {eligible.length} 个渠道
            </button>
            <button
              className="button small"
              aria-label="刷新 sub2api 检测"
              onClick={() => void query.refetch()}
              disabled={query.isFetching}
            >
              <RefreshCw size={15} className={query.isFetching ? "spin" : ""} />
              刷新数据
            </button>
            <button
              className="button primary small"
              disabled={!eligible.length || !!action || eligible.length > 500}
              onClick={() => void check(eligible)}
            >
              <ScanLine size={15} />
              {model ? "检测所选模型" : "检测全部模型"} · {eligible.length}{" "}
              个渠道
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
              {accountId && !accounts.some((a) => a.id === accountId) && (
                <option value={accountId}>已选账号（当前不可用）</option>
              )}
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
              aria-label="检测模型筛选"
              value={model}
              onChange={(e) => {
                setModel(e.target.value);
                setPage(0);
              }}
            >
              <option value="">全部模型</option>
              {SUB_MODELS.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </select>
            <ChevronDown size={13} />
          </label>
          <label className="select-field">
            <select
              aria-label="平台筛选"
              value={platform}
              onChange={(e) => {
                setPlatform(e.target.value);
                setPage(0);
              }}
            >
              <option value="">全部平台</option>
              {platform && !platforms.includes(platform) && (
                <option value={platform} disabled>
                  {platform}（当前无匹配）
                </option>
              )}
              {platforms.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
            <ChevronDown size={13} />
          </label>
          <label className="select-field">
            <select
              aria-label="倍率上限筛选"
              value={effectiveMaxRate}
              onChange={(e) => {
                setMaxRate(e.target.value);
                setPage(0);
              }}
            >
              <option value="">全部倍率</option>
              {rates.map((rate) => (
                <option key={rate} value={rate}>
                  ≤ {rate}
                </option>
              ))}
            </select>
            <ChevronDown size={13} />
          </label>
          <label className="select-field">
            <select
              aria-label="倍率排序"
              value={sort}
              onChange={(e) => {
                setSort(e.target.value);
                setPage(0);
              }}
            >
              <option value="">默认顺序</option>
              <option value="asc">倍率从低到高</option>
              <option value="desc">倍率从高到低</option>
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
              <option value="success">
                {model ? "可用" : "至少一个模型可用"}
              </option>
              <option value="error">
                {model ? "检测失败" : "有模型检测失败"}
              </option>
              <option value="untested">
                {model ? "未检测" : "有模型未检测"}
              </option>
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
              <option value="">全部 Astra 降智结果</option>
              <option value="pass">不降智</option>
              <option value="fail">降智</option>
              <option value="inconclusive">无法判定</option>
              <option value="error">检测失败</option>
            </select>
            <ChevronDown size={13} />
          </label>
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
                    {model && <th>模型</th>}
                    <th>平台 / key</th>
                    <th>倍率</th>
                    <th>可用性</th>
                    <th>首字延迟</th>
                    <th>
                      <Tip text="比较检测请求的模型名与流式完成事件中的 response.model，完全一致才算匹配。独立降智检测同时更新 Astra 可用性和模型匹配，不额外发起请求。">
                        模型匹配 <CircleHelp size={12} />
                      </Tip>
                    </th>
                    <th>Astra 降智</th>
                    <th>回复 / 诊断</th>
                    <th>最近检测</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {rows
                    .slice(currentPage * 25, (currentPage + 1) * 25)
                    .map(({ account, target: t, checks, selected, astra }) => (
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
                        {model && <td className="mono">{model}</td>}
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
                          {t.state === "error" ? (
                            <span className="check-status error">同步失败</span>
                          ) : selected ? (
                            <AvailabilityStatus check={selected} />
                          ) : (
                            <GroupAvailability checks={checks} />
                          )}
                        </td>
                        <td className="mono">
                          <ResponseLatency
                            created={
                              selected?.result?.availability.response_created_ms
                            }
                            text={selected?.result?.availability.ttft_ms}
                          />
                        </td>
                        <td>
                          {selected ? (
                            <ModelMatch check={selected} />
                          ) : (
                            <GroupModelMatch checks={checks} />
                          )}
                        </td>
                        <td>
                          <Verdict result={astra.result} />
                        </td>
                        <td className="check-answer">
                          {selected ? (
                            <>
                              {t.message ||
                                selected.message ||
                                selected.result?.availability.message ||
                                (model === "gpt-6-astra"
                                  ? selected.result?.quality.message ||
                                    selected.result?.quality.text
                                  : selected.result?.availability.text) ||
                                "—"}
                              {selected.result && (
                                <details>
                                  <summary>检测详情</summary>
                                  <ProbeDetails check={selected} />
                                </details>
                              )}
                            </>
                          ) : (
                            <>
                              {t.message && <div>{t.message}</div>}
                              <ModelResults checks={checks} />
                            </>
                          )}
                        </td>
                        <td className="mono">
                          {(() => {
                            const at =
                              selected?.result?.checked_at ??
                              (selected
                                ? 0
                                : Math.max(
                                    0,
                                    ...checks.map(
                                      (c) => c.result?.checked_at || 0,
                                    ),
                                  ));
                            return at ? time(at) : "—";
                          })()}
                          {selected &&
                            pending(selected.state) &&
                            selected.result && (
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
                            onClick={() =>
                              void check([
                                { account, target: t, checks, selected, astra },
                              ])
                            }
                          >
                            <ScanLine size={13} />
                            {model ? "检测此模型" : "检测全部模型"}
                          </button>
                          <button
                            className="button small"
                            onClick={() =>
                              setImporting({
                                account,
                                target: t,
                              })
                            }
                          >
                            <Plus size={13} />
                            {(() => {
                              const count =
                                imports.data?.data.filter(
                                  (i) =>
                                    i.account_id === account.id &&
                                    i.group_id === t.group_id,
                                ).length || 0;
                              return count
                                ? `已添加 · ${count} 个 key`
                                : "添加到渠道";
                            })()}
                          </button>
                        </td>
                      </tr>
                    ))}
                </tbody>
              </table>
            </div>
            <div className="table-footer">
              <span>
                共 {rows.length} 个渠道 · {eligible.length} 个可发起检测
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
      {importing && (
        <Sub2apiImport
          imports={imports}
          account={importing.account}
          target={importing.target}
          close={() => setImporting(null)}
        />
      )}
    </div>
  );
}

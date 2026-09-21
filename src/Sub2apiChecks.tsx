import { useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import * as Dialog from "@radix-ui/react-dialog";
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
import { controlRequest, makeLimiter } from "./api";
import { BalanceAmount } from "./BalanceAmount";
import { browserHelperAvailable, loginWithBrowser } from "./sub2apiBrowser";
import { ResponseLatency } from "./LatencyBadge";
import { Empty, Spinner, Tip } from "./ui";
import { loadSubFilters, saveSubFilters } from "./sub2apiPreferences";
import { useSubImports } from "./sub2apiImports";
import { useSubAccounts } from "./sub2apiAccounts";
import { Sub2apiImport } from "./Sub2apiImport";
import { SUB_MODELS, loadSubModels, saveSubModels } from "./sub2apiModels";
import { SubModelSettings } from "./SubModelSettings";
import {
  modelChecks,
  availabilityCounts,
  modelMatchStatus,
  modelMatchLabels,
} from "./sub2apiResults";
import type { SubModelCheck } from "./sub2apiResults";
import { time } from "./format";
import { QualityHistory, QualityProbability, qualityTooltip } from "./QualityHistory";
import type { QualitySummary } from "./QualityHistory";
import { SiteLink } from "./ChannelSite";
import { useSubPrices, PriceStatus, GroupPriceStatus, UsageDetailRows } from "./Sub2apiPricing";
import type { SubUsage } from "./sub2apiPriceCheck";
import { priceFilterStatus } from "./sub2apiPriceCheck";
import type { ModelPrice } from "./types";

export interface Probe {
  id?: string;
  started_at?: number;
  request_ids?: string[];
  usage?: SubUsage;
  requested_model?: string;
  response_model?: string;
  model_match?: "match" | "mismatch" | "missing" | "invalid" | "unavailable";
  status: string;
  text: string;
  message?: string;
  ttft_ms: number | null;
  response_created_ms?: number | null;
  first_response_ms?: number | null;
  protocol?: "responses" | "gemini" | "messages";
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
  quality_check?: import("./ChannelChecks").ChannelCheck & { quality_probe?: Probe };
  history?: QualitySummary;
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
// Shared quality is separate from model availability and its billing details.
export function groupQualityResult(target: SubTarget): Result | null {
  const saved = modelChecks(target).find(c => c.model === "gpt-6-astra")?.result || null;
  const check = target.quality_check;
  if (!check) return saved;
  // Receipt lookups enrich the saved native probe after history was recorded.
  // Keep that newer billing detail when both refer to the same native check.
  if (check.quality_probe && saved?.checked_at === check.checked_at &&
      saved.quality.id === check.quality_probe.id) return saved;
  const unavailable: Probe = { status: "skipped", text: "", ttft_ms: null, duration_ms: 0 };
  return { model: "gpt-6-astra", checked_at: check.checked_at, verdict: check.verdict,
    availability: saved?.availability || unavailable,
    quality: check.quality_probe || { status: check.verdict === "error" ? "error" : "success", text: check.text, message: check.message, ttft_ms: null, duration_ms: check.duration_ms },
  };
}
export interface SubAccount {
  id: string;
  name: string;
  base: string;
  email: string;
  state: string;
  message: string;
  synced_at: number;
  balance?: SubAccountBalance | null;
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

interface SubAccountBalance {
  amount: number | null;
  checked_at: number;
  status: string;
}
const limitAccountBalance = makeLimiter(3);
function AccountBalance({ account }: { account: SubAccount }) {
  const query = useQuery({
    queryKey: ["sub2api-balance", account.id, account.synced_at],
    queryFn: ({ signal }) =>
      limitAccountBalance(
        () =>
          controlRequest<SubAccountBalance>(
            `/v1/sub2api/accounts/${account.id}/balance`,
            { signal },
          ),
        signal,
      ),
    staleTime: 60_000,
    refetchInterval: 60_000,
    retry: false,
  });
  const balance = query.data || account.balance;
  return (
    <div
      className="sub-account-balance"
      title={
        balance?.checked_at
          ? `余额查询于 ${time(balance.checked_at)}${query.isError || balance.status === "error" ? " · 更新失败，上次结果" : ""}`
          : "尚无余额数据"
      }
    >
      <span>余额</span>
      <BalanceAmount value={balance?.amount} />
      {query.isPending && !balance && <Spinner small />}
      {(query.isError ||
        (balance &&
          balance.status !== "ok" &&
          balance.status !== "missing")) && (
        <small>更新失败{balance?.amount != null && " · 上次结果"}</small>
      )}
    </div>
  );
}

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
  const [browserNeeded, setBrowserNeeded] = useState(false);
  const [helperReady, setHelperReady] = useState(false);
  const [browserBusy, setBrowserBusy] = useState(false);
  const [challenge, setChallenge] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
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
      setPassword("");
      await controlRequest("/v1/sub2api/accounts", {
        method: "POST",
        body: JSON.stringify({ name, base, email, ...auth }),
        signal: abort.signal,
      });
      saved();
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
    if (browserNeeded && !challenge) {
      void browserLogin();
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await controlRequest<{
        requires_2fa?: boolean;
        challenge?: string;
        requires_browser?: boolean;
        browser_base?: string;
      }>("/v1/sub2api/accounts", {
        method: "POST",
        body: JSON.stringify(
          challenge
            ? { challenge, totp_code: code }
            : { name, base, email, password },
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
        setPassword("");
        saved();
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
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content
          className="guide-dialog sub-account-dialog"
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
                : "添加 sub2api 账号"}
          </Dialog.Title>
          <Dialog.Description>
            连接后自动同步可用分组并检测，每个新测试 key 的累计额度为 $1。
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
              disabled={busy || browserBusy}
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
                    账号邮箱
                    <input
                      type="email"
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
                      autoComplete="current-password"
                      value={password}
                      onChange={(event) => setPassword(event.target.value)}
                      required
                    />
                  </label>
                </>
              )}
            </fieldset>
            {browserBusy && (
              <p className="sub-login-status" role="status">
                <Spinner small />
                正在等待原站登录，成功后自动同步…
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
                {challenge
                  ? "验证并检测"
                  : browserNeeded
                    ? "使用浏览器登录"
                    : "连接并检测"}
              </button>
            </div>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function Verdict({ result, history }: { result: Result | null; history?: QualitySummary }) {
  if (result?.verdict === "not_applicable")
    return <span className="muted">不适用</span>;
  if (!result) return <span className="muted">未检测</span>;
  const pass = result.verdict === "pass",
    fail = result.verdict === "fail";
  const status = (
    <span className={`check-status ${pass ? "pass" : fail ? "fail" : "inconclusive"}`}>
      {pass ? <Check size={16} /> : fail ? <X size={16} /> : <CircleHelp size={16} />}
      {pass ? "不降智" : fail ? "降智" : result.quality.status === "skipped" ? "未检测" : result.quality.status === "error" ? "检测失败" : "无法判定"}
    </span>
  );
  return history && history.total > 0 ? <Tip text={qualityTooltip(history, result.checked_at)}>{status}</Tip> : status;
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

function DetailRow({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <tr>
      <th scope="row">{label}</th>
      <td>{children}</td>
    </tr>
  );
}

function CheckDetails({
  account,
  target,
  checks,
  selected,
  prices,
}: {
  account: SubAccount;
  target: SubTarget;
  checks: SubModelCheck[];
  selected?: SubModelCheck;
  prices?: ModelPrice[];
}) {
  const [detailModel, setDetailModel] = useState(checks[0].model);
  const check =
    selected || checks.find((item) => item.model === detailModel) || checks[0];
  const result = check.result;
  const probe = result?.availability;
  const qualityResult = groupQualityResult(target);
  return (
    <Dialog.Root>
      <Dialog.Trigger asChild>
        <button
          className="button small"
          aria-label={`查看 ${account.name} ${target.name} 的回复与诊断`}
        >
          查看详情
        </button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="guide-dialog sub-check-dialog">
          <Dialog.Title>回复 / 诊断</Dialog.Title>
          <Dialog.Description>
            {account.name} / {target.name} · 分组 #{target.group_id}
          </Dialog.Description>
          <Dialog.Close asChild>
            <button
              className="icon-button detail-close"
              aria-label="关闭检测详情"
            >
              <X size={18} />
            </button>
          </Dialog.Close>
          {!selected && (
            <label className="sub-import-field">
              查看模型
              <select
                value={detailModel}
                onChange={(e) => setDetailModel(e.target.value)}
              >
                {checks.map((item) => (
                  <option key={item.model} value={item.model}>
                    {item.model}
                  </option>
                ))}
              </select>
            </label>
          )}
          <table
            className="sub-check-details-table"
            aria-label={`${check.model} 检测详情`}
          >
            <thead>
              <tr>
                <th scope="col">字段</th>
                <th scope="col">检测结果</th>
              </tr>
            </thead>
            <tbody>
              <DetailRow label="请求模型">
                <span className="mono">
                  {probe?.requested_model || check.model}
                </span>
              </DetailRow>
              <DetailRow label="返回模型">
                <span className="mono">{probe?.response_model || "—"}</span>
              </DetailRow>
              <DetailRow label="可用性">
                <AvailabilityStatus check={check} />
              </DetailRow>
              <DetailRow label="模型匹配">
                <ModelMatch check={check} />
              </DetailRow>
              <DetailRow label="单价核验">
                <PriceStatus check={check} prices={prices} />
              </DetailRow>
              <UsageDetailRows probe={probe} label="可用性" prices={prices} model={check.model} />
              <DetailRow label="最近检测">
                {result ? time(result.checked_at) : "未检测"}
                {result && pending(check.state) && " · 上次结果"}
              </DetailRow>
              <DetailRow label="首字延迟">
                <ResponseLatency
                  created={probe?.response_created_ms}
                  text={probe?.ttft_ms}
                  protocol={probe?.protocol}
                  firstResponse={probe?.first_response_ms}
                />
              </DetailRow>
              <DetailRow label="首个文本延迟">
                {latency(probe?.ttft_ms ?? null)}
                <small className="check-source">
                  {probe?.protocol === "gemini" ? "首个 Gemini 非思考文本片段" : probe?.protocol === "messages" ? "首个 Messages 文本片段" : "首个 response.output_text.delta"}
                </small>
              </DetailRow>
              <DetailRow label="可用性耗时">
                {latency(probe?.duration_ms ?? null)}
              </DetailRow>
              <DetailRow label="HTTP 状态">
                {probe?.http_status || "—"}
              </DetailRow>
              <DetailRow label="可用性回复">
                <div className="sub-check-reply">{probe?.text || "—"}</div>
              </DetailRow>
              {probe?.message && (
                <DetailRow label="可用性诊断">
                  <div className="sub-check-reply">{probe.message}</div>
                </DetailRow>
              )}
              {check.model === "gpt-6-astra" && (
                <>
                  <DetailRow label="Astra 降智">
                    <div className="quality-status-stack"><Verdict result={qualityResult} history={target.history} /><QualityProbability history={target.history} checkedAt={qualityResult?.checked_at} /></div>
                  </DetailRow>
                  {qualityResult?.quality.id && qualityResult.quality.id === probe?.id ? (
                    <DetailRow label="降智扣费">与可用性为同一次请求，扣费见上方</DetailRow>
                  ) : <UsageDetailRows probe={qualityResult?.quality} label="降智" prices={prices} model={check.model} />}
                  <DetailRow label="降智检测耗时">
                    {latency(qualityResult?.quality.duration_ms ?? null)}
                  </DetailRow>
                  <DetailRow label="降智 HTTP 状态">
                    {qualityResult?.quality.http_status || "—"}
                  </DetailRow>
                  <DetailRow label="降智回复">
                    <div className="sub-check-reply">
                      {qualityResult?.quality.text || "—"}
                    </div>
                  </DetailRow>
                  {qualityResult?.quality.message && (
                    <DetailRow label="降智诊断">
                      <div className="sub-check-reply">
                        {qualityResult.quality.message}
                      </div>
                    </DetailRow>
                  )}
                </>
              )}
              {check.message && (
                <DetailRow label="检测诊断">
                  <div className="sub-check-reply">{check.message}</div>
                </DetailRow>
              )}
              {target.message && (
                <DetailRow label="同步诊断">
                  <div className="sub-check-reply">{target.message}</div>
                </DetailRow>
              )}
            </tbody>
          </table>
          {check.model === "gpt-6-astra" && <QualityHistory path={`/v1/sub2api/accounts/${encodeURIComponent(account.id)}/groups/${target.group_id}/quality-history`} revision={`${qualityResult?.checked_at}:${target.history?.total}:${target.history?.successful}`} />}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

export function Sub2apiChecks({ user = "account" }: { user?: string }) {
  const client = useQueryClient();
  const query = useSubAccounts();
  const prices = useSubPrices(user);
  const accounts = query.data?.data || [];
  const [form, setForm] = useState<{ account: SubAccount | null } | null>(null);
  const [filters, setFilters] = useState(() => loadSubFilters(user));
  const [detectionModels, setDetectionModels] = useState(() => loadSubModels(user));
  const {
    search,
    accountId,
    model,
    maxRate,
    sort,
    availability,
    priceStatus,
    quality,
    minQuality,
    platform,
  } = filters;
  const modelsToCheck = detectionModels.filter((item) => !model || item === model);
  const allModelsSelected = detectionModels.length === SUB_MODELS.length;
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
  const [syncSubmitted, setSyncSubmitted] = useState<number | null>(null);
  const syncableAccounts = accounts.filter(
    (account) => !pending(account.state),
  );
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
          ({ account, target, checks, selected }) =>
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
            (!priceStatus ||
              priceFilterStatus(selected ? [selected] : checks, prices.data?.data) === priceStatus) &&
            (!quality || groupQualityResult(target)?.verdict === quality) &&
            (minQuality === "" ||
              (!!target.history?.successful &&
                target.history.passed * 100 >= Number(minQuality) * target.history.successful)),
        ),
    [accounts, search, accountId, availability, priceStatus, prices.data, quality, minQuality, model],
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
  async function syncAll() {
    if (action || !syncableAccounts.length) return;
    setAction("sync-all");
    setError("");
    setSyncSubmitted(null);
    try {
      const result = await controlRequest<{ queued: number }>(
        "/v1/sub2api/accounts/sync",
        { method: "POST" },
      );
      setSyncSubmitted(result.queued);
      await client.invalidateQueries({ queryKey: ["sub2api"] });
    } catch (e) {
      setError(e instanceof Error ? e.message : "批量同步失败");
    } finally {
      setAction("");
    }
  }
  const check = (selection: typeof rows) => {
    if (!modelsToCheck.length) return;
    // Result filters select groups; settings and the explicit model selector
    // choose probes, including untested/failed models within that scope.
    return mutate("check", "/v1/sub2api/checks", {
      targets: selection.map(({ account, target }) => ({
        account_id: account.id,
        group_id: target.group_id,
        models: modelsToCheck,
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
          <div className="sub-account-actions">
            <button
              className="button small"
              aria-label="一键同步并检测"
              aria-busy={action === "sync-all"}
              disabled={
                !syncableAccounts.length ||
                !!action ||
                query.isPending ||
                query.isError
              }
              onClick={() => void syncAll()}
            >
              {action === "sync-all" ? (
                <Spinner small />
              ) : (
                <RefreshCw size={15} />
              )}
              {action === "sync-all"
                ? "正在提交…"
                : `一键同步并检测 · ${syncableAccounts.length} 个账号`}
            </button>
            <button
              className="button primary small"
              onClick={() => setForm({ account: null })}
            >
              <Plus size={15} />
              添加账号
            </button>
          </div>
        </div>
        {syncSubmitted !== null && (
          <p className="settings-note" role="status">
            {syncSubmitted
              ? `已提交 ${syncSubmitted} 个账号，正在后台同步并检测。`
              : "暂无可同步账号，已有任务会继续执行。"}
          </p>
        )}
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
            填入站点地址与账号密码，自动发现可用分组并检测模型。
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
                      {a.email} · <SiteLink base={a.base}>{new URL(a.base).host}</SiteLink>
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
                    <AccountBalance account={a} />
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
              disabled={!eligible.length || !!action || eligible.length > 500 || !modelsToCheck.length}
              title={!modelsToCheck.length ? "当前筛选模型未勾选，请在设置中启用" : undefined}
              onClick={() => void check(eligible)}
            >
              <ScanLine size={15} />
              {model ? "检测所选模型" : allModelsSelected ? "检测全部模型" : `检测已选 ${detectionModels.length} 个模型`} · {eligible.length}{" "}
              个渠道
            </button>
            <SubModelSettings models={detectionModels} onSave={(models) => {
              setDetectionModels(models);
              saveSubModels(user, models);
            }} />
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
              aria-label="单价是否异常筛选"
              title={model ? "按所选模型的倍率前单价与价格设置比较" : "任一模型异常则为异常；全部模型确认正常才为正常；其余为未确认"}
              value={priceStatus}
              onChange={(e) => {
                setFilters((v) => ({ ...v, priceStatus: e.target.value }));
                setPage(0);
              }}
            >
              <option value="">全部单价</option>
              <option value="abnormal">单价异常</option>
              <option value="normal">单价正常</option>
              <option value="unconfirmed">单价未确认</option>
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
          <label className="select-field">
            <select
              aria-label="不降智概率筛选"
              value={minQuality}
              onChange={(e) => {
                setFilters((v) => ({ ...v, minQuality: e.target.value }));
                setPage(0);
              }}
            >
              <option value="">全部不降智概率</option>
              {Array.from({ length: 11 }, (_, i) => i * 10).map((percent) => (
                <option key={percent} value={percent}>不降智 ≥ {percent}%</option>
              ))}
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
                    <th><Tip text="按站点请求账单的倍率前输入／输出单价与价格设置比较；单位为美元／百万 token。缺少账单或 token 样本时不判为正常。">单价异常 <CircleHelp size={12} /></Tip></th>
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
                            protocol={selected?.result?.availability.protocol}
                            firstResponse={selected?.result?.availability.first_response_ms}
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
                        <td>{selected ? <PriceStatus check={selected} prices={prices.data?.data} /> : <GroupPriceStatus checks={checks} prices={prices.data?.data} />}</td>
                        <td>
                          <div className="quality-status-stack"><Verdict result={groupQualityResult(t)} history={t.history} /><QualityProbability history={t.history} checkedAt={groupQualityResult(t)?.checked_at} /></div>
                        </td>
                        <td>
                          <CheckDetails
                            key={model || "all"}
                            account={account}
                            target={t}
                            checks={checks}
                            selected={selected}
                            prices={prices.data?.data}
                          />
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
                              !modelsToCheck.length ||
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
                            {model ? "检测此模型" : allModelsSelected ? "检测全部模型" : `检测已选 ${detectionModels.length} 个模型`}
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
                                    i.kind !== "configured" &&
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
          target={accounts.find(a => a.id === importing.account.id)?.targets.find(t => t.group_id === importing.target.group_id) || importing.target}
          prices={prices.data?.data}
          close={() => setImporting(null)}
        />
      )}
    </div>
  );
}

import { useCallback, useEffect, useMemo, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import {
  Bot,
  History,
  Pause,
  Play,
  Plus,
  RefreshCw,
  Save,
  Trash2,
  X,
} from "lucide-react";
import { controlRequest } from "./api";
import type { ConsoleSource } from "./SourceSettings";
import { Spinner } from "./ui";

type Policy = {
  metrics: string[];
  min_success_samples: number;
  min_cache_samples: number;
  min_latency_samples: number;
  min_quality_samples: number;
  confidence_level: number;
  latency_quantile: number;
  latency_min_percent: number;
  latency_min_ms: number;
  success_min_pp: number;
  cache_min_pp: number;
  quality_min_pp: number;
  require_consecutive: number;
  cooldown_seconds: number;
  max_moves: number;
  action: "suggest" | "apply";
  require_all_higher: boolean;
  quality_check: boolean;
  endpoint: string;
  stream: string;
  max_age_seconds: number;
  quality_concurrency: number;
  latency_tolerance_percent: number;
  success_tolerance_pp: number;
  cache_tolerance_pp: number;
  quality_tolerance_pp: number;
};
type Task = {
  id: string;
  revision: number;
  name: string;
  kind: "order" | "quality";
  enabled: boolean;
  source_id: string;
  key_id: string;
  model: string;
  interval_seconds: number;
  range: string;
  policy: Policy;
  last_run?: string;
  next_run?: string;
};
type Metric = {
  Provider: string;
  Upstream: string;
  Latency: number;
  Success: number;
  Cache: number;
  Quality: number;
  LatencyN: number;
  SuccessN: number;
  CacheN: number;
  QualityN: number;
  LatencyCI: number[];
  SuccessCI: number[];
  CacheCI: number[];
  QualityCI: number[];
  Reason: string;
};
type Audit = {
  id: number;
  task_id: string;
  task_name: string;
  run_at: string;
  status: string;
  reason: string;
  before_order: string[];
  after_order: string[];
  changes: { provider: string; over: string }[];
  policy: Policy;
  metrics: {
    channels?: Record<string, Metric>;
    comparisons?: string[];
    checks?: Record<string, string>;
    confirmations?: number;
    task_revision?: number;
  };
};
type Key = { key_id: string; prefix: string; position: number };
const defaults: Policy = {
  metrics: ["latency", "success", "cache", "quality"],
  min_success_samples: 100,
  min_cache_samples: 50,
  min_latency_samples: 50,
  min_quality_samples: 20,
  confidence_level: 0.95,
  latency_quantile: 0.5,
  latency_min_percent: 10,
  latency_min_ms: 100,
  success_min_pp: 2,
  cache_min_pp: 5,
  quality_min_pp: 5,
  require_consecutive: 3,
  cooldown_seconds: 1800,
  max_moves: 1,
  action: "suggest",
  require_all_higher: false,
  quality_check: false,
  endpoint: "/v1/responses",
  stream: "true",
  max_age_seconds: 300,
  quality_concurrency: 4,
  latency_tolerance_percent: 0,
  success_tolerance_pp: 0,
  cache_tolerance_pp: 0,
  quality_tolerance_pp: 0,
};
const empty = (source = ""): Task => ({
  id: "",
  revision: 0,
  name: "",
  kind: "order",
  enabled: false,
  source_id: source,
  key_id: "",
  model: "gpt-6-astra",
  interval_seconds: 300,
  range: "1h",
  policy: { ...defaults, metrics: [...defaults.metrics] },
});
const metricLabels: Record<string, string> = {
  latency: "首字延迟",
  success: "成功率",
  cache: "缓存率",
  quality: "不降智概率（仅 gpt-6-astra）",
};
const statusLabels: Record<string, string> = {
  configured: "配置已保存",
  archived: "任务已删除",
  reviewed: "已核对并暂停",
  running: "执行中",
  completed: "执行已结束",
  interrupted: "上次执行中断",
  checked: "检测已完成",
  applied: "已应用",
  suggested: "生成建议",
  waiting: "等待连续确认",
  no_new_data: "等待新数据",
  cooldown: "冷却中",
  unchanged: "无需调整",
  error: "执行失败",
  skipped: "本次跳过",
  conflict: "配置冲突",
  prepared: "等待提交确认",
  uncertain: "提交待核对",
  rolled_back: "已恢复原顺序",
};
const ranges = [
  "5m",
  "15m",
  ...Array.from({ length: 24 }, (_, i) => `${i + 1}h`),
  "7d",
  "30d",
];
const number = (value: string) =>
  Number.isFinite(Number(value)) ? Number(value) : 0;
const time = (value?: string) =>
  value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "—";

export function Automation({
  sources,
  models = [],
}: {
  sources: ConsoleSource[];
  models?: string[];
}) {
  const [tasks, setTasks] = useState<Task[]>([]),
    [selected, setSelected] = useState(""),
    [draft, setDraft] = useState<Task | null>(null),
    [audit, setAudit] = useState<Audit[]>([]),
    [detail, setDetail] = useState<Audit | null>(null),
    [keys, setKeys] = useState<Key[]>([]),
    [loading, setLoading] = useState(true),
    [saving, setSaving] = useState(false),
    [error, setError] = useState(""),
    [generation, setGeneration] = useState(0),
    [keyError, setKeyError] = useState("");
  const sourceNames = useMemo(
    () => new Map(sources.map((s) => [s.id, s.name])),
    [sources],
  );
  const reload = useCallback(() => setGeneration((v) => v + 1), []);
  useEffect(() => {
    let live = true;
    setLoading(true);
    void controlRequest<{ data: Task[] }>("/v1/automations")
      .then((r) => {
        if (live) setTasks(r.data || []);
      })
      .catch((e) => {
        if (live) setError(String(e.message || e));
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [generation]);
  useEffect(() => {
    let live = true;
    setAudit([]);
    void controlRequest<{ data: Audit[] }>(
      selected
        ? `/v1/automations/${encodeURIComponent(selected)}`
        : "/v1/automations/audit",
    )
      .then((r) => {
        if (live) setAudit(r.data || []);
      })
      .catch((e) => {
        if (live) setError(String(e.message || e));
      });
    return () => {
      live = false;
    };
  }, [selected, generation]);
  useEffect(() => {
    let live = true;
    setKeys([]);
    setKeyError("");
    if (!draft?.source_id) return;
    void controlRequest<{ data: Key[] }>(
      `/v1/sources/${encodeURIComponent(draft.source_id)}/proxy/v1/api-keys`,
    )
      .then((r) => {
        if (live)
          setKeys(
            (r.data || []).map((k) => ({
              ...k,
              key_id: k.key_id.split("::").pop() || k.key_id,
            })),
          );
      })
      .catch(() => {
        if (live) setKeyError("API key 列表读取失败，请刷新后重试");
      });
    return () => {
      live = false;
    };
  }, [draft?.source_id, generation]);
  function edit(t: Task) {
    setSelected(t.id);
    setDraft({
      ...t,
      policy: { ...defaults, ...t.policy, metrics: [...t.policy.metrics] },
    });
    setError("");
  }
  function newTask() {
    setSelected("");
    setDraft(empty(sources[0]?.id));
    setError("");
  }
  function patch(value: Partial<Task>) {
    setDraft((t) => (t ? { ...t, ...value } : t));
  }
  function policy(value: Partial<Policy>) {
    setDraft((t) => (t ? { ...t, policy: { ...t.policy, ...value } } : t));
  }
  async function save(task = draft) {
    if (!task || saving) return;
    setSaving(true);
    setError("");
    try {
      const r = await controlRequest<{ task: Task }>(
        task.id
          ? `/v1/automations/${encodeURIComponent(task.id)}`
          : "/v1/automations",
        { method: task.id ? "PUT" : "POST", body: JSON.stringify(task) },
      );
      edit(r.task);
      reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }
  async function remove(t: Task) {
    if (!confirm(`删除任务“${t.name}”？历史审计会保留。`)) return;
    try {
      await controlRequest(`/v1/automations/${encodeURIComponent(t.id)}`, {
        method: "DELETE",
      });
      if (selected === t.id) {
        setSelected("");
        setDraft(null);
      }
      reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : "删除失败");
    }
  }
  async function run(t: Task) {
    try {
      await controlRequest(`/v1/automations/${encodeURIComponent(t.id)}/run`, {
        method: "POST",
      });
      reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : "任务排队失败");
    }
  }
  async function older() {
    if (!audit.length) return;
    try {
      const r = await controlRequest<{ data: Audit[] }>(
        (selected
          ? `/v1/automations/${encodeURIComponent(selected)}`
          : "/v1/automations/audit") + `?before=${audit[audit.length - 1].id}`,
      );
      setAudit((a) => [...a, ...r.data]);
    } catch (e) {
      setError(e instanceof Error ? e.message : "审计读取失败");
    }
  }
  const numeric = (fields: [keyof Policy, string, number, number, number?][]) =>
    fields.map(([key, label, min, max, step]) => (
      <label key={key}>
        {label}
        <input
          type="number"
          min={min}
          max={max}
          step={step || 1}
          value={draft?.policy[key] as number}
          onChange={(e) => policy({ [key]: number(e.target.value) })}
        />
      </label>
    ));
  async function review(a: Audit) {
    try {
      await controlRequest(`/v1/automations/audit/${a.id}/review`, {
        method: "POST",
      });
      setDetail(null);
      reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : "核对失败");
    }
  }
  async function approve(a: Audit) {
    try {
      await controlRequest(`/v1/automations/audit/${a.id}/apply`, {
        method: "POST",
      });
      setDetail(null);
      reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : "建议应用失败");
    }
  }
  async function rollback(a: Audit) {
    if (
      !confirm("恢复此次调整前的顺序并暂停任务？若配置已有变化，将取消恢复。")
    )
      return;
    try {
      await controlRequest(`/v1/automations/audit/${a.id}/rollback`, {
        method: "POST",
      });
      setDetail(null);
      reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : "恢复失败");
    }
  }
  const order = draft?.kind !== "quality";
  return (
    <section className="automation-page">
      <div className="data-panel automation-intro">
        <div className="data-heading">
          <div className="data-title">
            <Bot size={19} />
            <h2>自动化</h2>
            <span className="count-badge">{tasks.length}</span>
          </div>
          <div className="data-actions">
            <button className="button small" onClick={reload}>
              <RefreshCw size={15} />
              刷新数据
            </button>
            <button className="button small" onClick={newTask}>
              <Plus size={15} />
              新建任务
            </button>
          </div>
        </div>
        <p className="field-note">
          定时检测降智，或根据最近的渠道表现生成调序建议、自动调整优先级。每次执行和配置变更均保留审计。
        </p>
      </div>
      {error && (
        <p role="alert" className="error-banner">
          {error}
        </p>
      )}
      <div className="automation-layout">
        <div className="data-panel automation-list">
          <div className="data-heading">
            <h3>任务</h3>
            {loading && <Spinner small />}
          </div>
          {tasks.map((t) => (
            <article
              className={`automation-card ${selected === t.id ? "selected" : ""}`}
              key={t.id}
            >
              <button className="automation-select" onClick={() => edit(t)}>
                <strong>{t.name}</strong>
                <small>
                  {sourceNames.get(t.source_id) || t.source_id} ·{" "}
                  {t.kind === "quality" ? "定时降智检测" : "渠道调序"}
                </small>
                <small>
                  {t.model} · 每 {t.interval_seconds} 秒
                </small>
                <small>上次运行：{time(t.last_run)}</small>
              </button>
              <div className="automation-card-actions">
                <span className={`automation-state ${t.enabled ? "on" : ""}`}>
                  {t.enabled ? "已启用" : "已暂停"}
                </span>
                <button
                  disabled={saving}
                  className="icon-button"
                  aria-label={`${t.enabled ? "暂停" : "启用"} ${t.name}`}
                  onClick={() => void save({ ...t, enabled: !t.enabled })}
                >
                  {t.enabled ? <Pause size={15} /> : <Play size={15} />}
                </button>
                <button
                  className="icon-button"
                  aria-label={`删除 ${t.name}`}
                  onClick={() => void remove(t)}
                >
                  <Trash2 size={15} />
                </button>
              </div>
            </article>
          ))}
          {!tasks.length && !loading && (
            <p className="muted">还没有自动化任务。</p>
          )}
        </div>
        <div className="data-panel automation-editor">
          {draft ? (
            <>
              <div className="data-heading">
                <h3>{draft.id ? "编辑自动化任务" : "新建自动化任务"}</h3>
                <button
                  className="icon-button"
                  aria-label="关闭编辑"
                  onClick={() => setDraft(null)}
                >
                  <X size={17} />
                </button>
              </div>
              <div className="automation-form">
                <label>
                  任务名称
                  <input
                    value={draft.name}
                    onChange={(e) => patch({ name: e.target.value })}
                    maxLength={120}
                  />
                </label>
                <label>
                  任务类型
                  <select
                    aria-label="任务类型"
                    value={draft.kind}
                    onChange={(e) =>
                      patch({
                        kind: e.target.value as Task["kind"],
                        ...(e.target.value === "quality"
                          ? { model: "gpt-6-astra" }
                          : {}),
                      })
                    }
                  >
                    <option value="order">渠道调序</option>
                    <option value="quality">定时降智检测</option>
                  </select>
                </label>
                <label>
                  来源
                  <select
                    value={draft.source_id}
                    onChange={(e) =>
                      patch({ source_id: e.target.value, key_id: "" })
                    }
                  >
                    <option value="">选择来源</option>
                    {sources.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  API key
                  <select
                    aria-label="API key"
                    value={draft.key_id}
                    onChange={(e) => patch({ key_id: e.target.value })}
                  >
                    <option value="">选择 API key</option>
                    {draft.key_id &&
                      !keys.some((k) => k.key_id === draft.key_id) && (
                        <option value={draft.key_id}>
                          当前 key（等待验证）
                        </option>
                      )}
                    {keys.map((k) => (
                      <option key={k.key_id} value={k.key_id}>
                        Key {k.position} · {k.prefix}
                      </option>
                    ))}
                  </select>
                  {keyError && <small role="alert">{keyError}</small>}
                </label>
                <label>
                  模型
                  <input
                    list="automation-models"
                    value={draft.model}
                    disabled={!order}
                    onChange={(e) => patch({ model: e.target.value })}
                  />
                  <datalist id="automation-models">
                    {[...new Set(["gpt-6-astra", ...models])].map((m) => (
                      <option key={m} value={m} />
                    ))}
                  </datalist>
                </label>
                <label>
                  执行间隔（秒）
                  <input
                    type="number"
                    min={10}
                    max={86400}
                    value={draft.interval_seconds}
                    onChange={(e) =>
                      patch({ interval_seconds: number(e.target.value) })
                    }
                  />
                </label>
              </div>
              {!order ? (
                <>
                  <p className="field-note">
                    对所选 API key 下支持 gpt-6-astra
                    的可用渠道发起降智检测，保存最新结果和每次检测历史。该任务不调整渠道顺序。
                  </p>
                  <div className="automation-form">
                    {numeric([["quality_concurrency", "检测并发数", 1, 16]])}
                  </div>
                </>
              ) : (
                <>
                  <h4>可比较范围</h4>
                  <div className="automation-form">
                    <label>
                      统计时间范围
                      <select
                        value={draft.range}
                        onChange={(e) => patch({ range: e.target.value })}
                      >
                        {ranges.map((r) => (
                          <option key={r}>{r}</option>
                        ))}
                      </select>
                    </label>
                    <label>
                      请求端点
                      <select
                        value={draft.policy.endpoint}
                        onChange={(e) => policy({ endpoint: e.target.value })}
                      >
                        {[
                          "/v1/responses",
                          "/v1/messages",
                          "/v1/chat/completions",
                          "/v1beta/models",
                        ].map((e) => (
                          <option key={e}>{e}</option>
                        ))}
                      </select>
                    </label>
                    <label>
                      流式状态
                      <select
                        value={draft.policy.stream}
                        onChange={(e) => policy({ stream: e.target.value })}
                      >
                        <option value="true">流式</option>
                        <option value="false">非流式</option>
                      </select>
                    </label>
                    {numeric([
                      ["max_age_seconds", "数据最大年龄（秒）", 10, 2592000],
                    ])}
                  </div>
                  <h4>参与比较的指标</h4>
                  <div className="automation-metrics">
                    {Object.entries(metricLabels).map(([key, label]) => (
                      <label key={key}>
                        <input
                          type="checkbox"
                          disabled={
                            key === "quality" && draft.model !== "gpt-6-astra"
                          }
                          checked={draft.policy.metrics.includes(key)}
                          onChange={() =>
                            policy({
                              metrics: draft.policy.metrics.includes(key)
                                ? draft.policy.metrics.filter((m) => m !== key)
                                : [...draft.policy.metrics, key],
                            })
                          }
                        />
                        {label}
                      </label>
                    ))}
                  </div>
                  <p className="field-note">
                    只上移全面不劣且至少一项达到改善门槛的渠道。每次跨过一个渠道都重新判定；缺少数据的渠道不会被跨过。
                  </p>
                  <h4>样本与置信度</h4>
                  <div className="automation-form automation-grid">
                    {numeric([
                      ["min_success_samples", "成功率最小样本", 1, 1000000],
                      ["min_cache_samples", "缓存率最小样本", 1, 1000000],
                      ["min_latency_samples", "首字最小样本", 1, 1000000],
                      ["min_quality_samples", "降智最小样本", 1, 1000000],
                    ])}
                    <label>
                      置信水平（%）
                      <input
                        type="number"
                        min={80}
                        max={99.9}
                        step={0.1}
                        value={Number(
                          (draft.policy.confidence_level * 100).toFixed(3),
                        )}
                        onChange={(e) =>
                          policy({
                            confidence_level: number(e.target.value) / 100,
                          })
                        }
                      />
                    </label>
                    <label>
                      首字分位数（%）
                      <input
                        type="number"
                        min={1}
                        max={99}
                        step={1}
                        value={Number(
                          (draft.policy.latency_quantile * 100).toFixed(3),
                        )}
                        onChange={(e) =>
                          policy({
                            latency_quantile: number(e.target.value) / 100,
                          })
                        }
                      />
                    </label>
                  </div>
                  <h4>实际改善幅度</h4>
                  <div className="automation-form automation-grid">
                    {numeric([
                      ["latency_min_percent", "首字至少快（%）", 0, 100, 0.1],
                      ["latency_min_ms", "首字至少快（ms）", 0, 3600000],
                      ["success_min_pp", "成功率至少高（百分点）", 0, 100, 0.1],
                      ["cache_min_pp", "缓存率至少高（百分点）", 0, 100, 0.1],
                      ["quality_min_pp", "不降智至少高（百分点）", 0, 100, 0.1],
                    ])}
                  </div>
                  <p className="field-note">
                    首字改善满足百分比或毫秒门槛之一即可。成功率和不降智概率使用
                    Wilson 区间，缓存率按分钟重采样，首字按 response.created
                    的分位数区间判定。
                  </p>
                  <details className="automation-tolerance">
                    <summary>统计不确定性容差</summary>
                    <p className="field-note">
                      默认
                      0，要求区间也能确认全面不劣。即使观测值相同，区间重叠也可能阻止调整。允许容差时，所有观测值仍必须不变差；至少一项改善仍需通过置信区间确认。
                    </p>
                    <div className="automation-form automation-grid">
                      {numeric([
                        [
                          "latency_tolerance_percent",
                          "首字区间容差（%）",
                          0,
                          100,
                          0.1,
                        ],
                        [
                          "success_tolerance_pp",
                          "成功率区间容差（百分点）",
                          0,
                          100,
                          0.1,
                        ],
                        [
                          "cache_tolerance_pp",
                          "缓存率区间容差（百分点）",
                          0,
                          100,
                          0.1,
                        ],
                        [
                          "quality_tolerance_pp",
                          "不降智区间容差（百分点）",
                          0,
                          100,
                          0.1,
                        ],
                      ])}
                    </div>
                  </details>
                  <h4>确认与执行</h4>
                  <div className="automation-form automation-grid">
                    {numeric([
                      ["require_consecutive", "连续确认次数", 1, 100],
                      ["cooldown_seconds", "冷却时间（秒）", 0, 2592000],
                      ["max_moves", "每次最多上移步数", 1, 100],
                    ])}
                    <label>
                      执行方式
                      <select
                        value={draft.policy.action}
                        onChange={(e) =>
                          policy({ action: e.target.value as Policy["action"] })
                        }
                      >
                        <option value="suggest">仅生成建议</option>
                        <option value="apply">自动应用临时顺序</option>
                      </select>
                    </label>
                    <label className="automation-check">
                      <input
                        type="checkbox"
                        checked={draft.policy.require_all_higher}
                        onChange={(e) =>
                          policy({ require_all_higher: e.target.checked })
                        }
                      />
                      额外要求领先所有更高优先级渠道
                    </label>
                    {draft.model === "gpt-6-astra" && (
                      <label className="automation-check">
                        <input
                          type="checkbox"
                          checked={draft.policy.quality_check}
                          onChange={(e) =>
                            policy({ quality_check: e.target.checked })
                          }
                        />
                        调序前先检测降智
                      </label>
                    )}
                    {draft.policy.quality_check &&
                      numeric([["quality_concurrency", "检测并发数", 1, 16]])}
                  </div>
                </>
              )}
              <label className="automation-check">
                <input
                  type="checkbox"
                  checked={draft.enabled}
                  onChange={(e) => patch({ enabled: e.target.checked })}
                />
                启用任务
              </label>
              <div className="automation-editor-actions">
                <button
                  className="button primary"
                  disabled={
                    saving ||
                    !draft.name ||
                    !draft.source_id ||
                    !draft.key_id ||
                    !draft.model
                  }
                  onClick={() => void save()}
                >
                  {saving ? <Spinner small /> : <Save size={15} />}保存任务
                </button>
                {draft.id && draft.enabled && (
                  <button className="button" onClick={() => void run(draft)}>
                    <Play size={15} />
                    执行已保存任务
                  </button>
                )}
              </div>
            </>
          ) : (
            <div className="automation-empty">
              <Bot size={32} />
              <h3>配置一个自动化任务</h3>
              <p>自定义检测周期、样本门槛与渠道支配规则。</p>
              <button className="button" onClick={newTask}>
                <Plus size={15} />
                新建任务
              </button>
            </div>
          )}
        </div>
      </div>
      <section className="data-panel automation-audit">
        <div className="data-heading">
          <div className="data-title">
            <History size={17} />
            <h3>历史与变动审计</h3>
          </div>
          {selected && (
            <button className="button small" onClick={() => setSelected("")}>
              全部任务历史
            </button>
          )}
        </div>
        {audit.length ? (
          <>
            <div className="automation-audit-list">
              {audit.map((a) => (
                <article key={a.id}>
                  <div>
                    <strong>
                      {statusLabels[a.status] || a.status} · {a.task_name}
                    </strong>
                    <span>{time(a.run_at)}</span>
                  </div>
                  <p>{a.reason}</p>
                  <button className="button small" onClick={() => setDetail(a)}>
                    查看详情
                  </button>
                </article>
              ))}
            </div>
            {audit.length % 100 === 0 && (
              <button className="button" onClick={() => void older()}>
                加载更早记录
              </button>
            )}
          </>
        ) : (
          <p className="muted">
            暂无记录。已删除任务的审计也会保留在全部任务历史中。
          </p>
        )}
      </section>
      <Dialog.Root
        open={!!detail}
        onOpenChange={(open) => {
          if (!open) setDetail(null);
        }}
      >
        <Dialog.Portal>
          <Dialog.Overlay className="dialog-overlay" />
          <Dialog.Content className="connection-dialog automation-detail">
            <Dialog.Title>自动化运行详情</Dialog.Title>
            <Dialog.Description>
              {detail?.task_name} · {time(detail?.run_at)}
            </Dialog.Description>
            <Dialog.Close
              className="icon-button dialog-close"
              aria-label="关闭详情"
            >
              <X size={18} />
            </Dialog.Close>
            {detail && (
              <>
                <p>{detail.reason}</p>
                {["prepared", "uncertain"].includes(detail.status) && (
                  <button
                    className="button"
                    onClick={() => void review(detail)}
                  >
                    核对当前配置并暂停任务
                  </button>
                )}
                {detail.status === "suggested" && (
                  <button
                    className="button"
                    onClick={() => void approve(detail)}
                  >
                    按最新数据复核并应用
                  </button>
                )}
                {detail.status === "applied" && (
                  <button
                    className="button"
                    onClick={() => void rollback(detail)}
                  >
                    恢复此前顺序并暂停任务
                  </button>
                )}
                <div className="automation-table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th>字段</th>
                        <th>结果</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <th>运行状态</th>
                        <td>{statusLabels[detail.status] || detail.status}</td>
                      </tr>
                      <tr>
                        <th>调整前顺序</th>
                        <td>{detail.before_order?.join(" → ") || "—"}</td>
                      </tr>
                      <tr>
                        <th>调整后顺序</th>
                        <td>{detail.after_order?.join(" → ") || "—"}</td>
                      </tr>
                      <tr>
                        <th>配置版本 / 连续确认</th>
                        <td>
                          {detail.metrics?.task_revision || "—"} /{" "}
                          {detail.metrics?.confirmations || "—"}
                        </td>
                      </tr>
                      {detail.metrics?.checks &&
                        Object.entries(detail.metrics.checks).map(
                          ([p, result]) => (
                            <tr key={p}>
                              <th>{p}</th>
                              <td>{result}</td>
                            </tr>
                          ),
                        )}
                    </tbody>
                  </table>
                </div>
                {detail.metrics?.channels && (
                  <div className="automation-table-scroll">
                    <table>
                      <thead>
                        <tr>
                          <th>渠道 / 指标</th>
                          <th>观测值</th>
                          <th>样本数</th>
                          <th>置信区间</th>
                        </tr>
                      </thead>
                      <tbody>
                        {Object.values(detail.metrics.channels).flatMap((m) =>
                          (
                            [
                              [
                                "首字",
                                m.Latency,
                                m.LatencyN,
                                m.LatencyCI,
                                true,
                              ],
                              [
                                "成功率",
                                m.Success,
                                m.SuccessN,
                                m.SuccessCI,
                                false,
                              ],
                              ["缓存率", m.Cache, m.CacheN, m.CacheCI, false],
                              [
                                "不降智",
                                m.Quality,
                                m.QualityN,
                                m.QualityCI,
                                false,
                              ],
                            ] as [string, number, number, number[], boolean][]
                          ).map(([label, value, n, ci, ms]) => (
                            <tr key={m.Provider + label}>
                              <th>
                                {m.Provider}
                                <small>{label}</small>
                              </th>
                              <td>
                                {n
                                  ? ms
                                    ? `${value.toFixed(1)} ms`
                                    : `${(value * 100).toFixed(2)}%`
                                  : "—"}
                              </td>
                              <td>{n}</td>
                              <td>
                                {n && ci
                                  ? ci
                                      .map((v) =>
                                        ms
                                          ? `${v.toFixed(1)} ms`
                                          : `${(v * 100).toFixed(2)}%`,
                                      )
                                      .join(" – ")
                                  : "—"}
                                {m.Reason && <small>{m.Reason}</small>}
                              </td>
                            </tr>
                          )),
                        )}
                      </tbody>
                    </table>
                  </div>
                )}
                {detail.metrics?.comparisons?.map((reason, i) => (
                  <p className="field-note" key={i}>
                    {reason}
                  </p>
                ))}
                <details>
                  <summary>本次执行策略</summary>
                  <div className="automation-table-scroll">
                    <table>
                      <tbody>
                        {Object.entries(detail.policy || {}).map(
                          ([key, value]) => (
                            <tr key={key}>
                              <th>{key}</th>
                              <td>
                                {Array.isArray(value)
                                  ? value.join("、")
                                  : String(value)}
                              </td>
                            </tr>
                          ),
                        )}
                      </tbody>
                    </table>
                  </div>
                </details>
              </>
            )}
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </section>
  );
}

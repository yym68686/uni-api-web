import { DetailLoading } from "./PageLoading";
import { LoadingIndicator } from "./LoadingIndicator";
import { useEffect, useId, useState, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Eye,
  EyeOff,
  Copy,
  Check,
  Settings2,
  Plus,
  Trash2,
  RotateCcw,
  CornerUpLeft,
  SlidersHorizontal,
  KeyRound,
  Timer,
  Filter,
  Braces,
  Globe2,
  FileCode2,
  Layers,
  History,
  ArrowUp,
  ArrowDown,
  ShieldCheck,
  CircleCheck,
  ChevronDown,
} from "./icons";
import { parse, stringify } from "yaml";
import { controlRequest } from "./api";
import { TimeoutPolicyField, HedgingField } from "./TimeoutPolicyField";
import {
  removesImageGeneration,
  setImageGenerationRemoval,
} from "./imageGenerationRemoval";
import { GLOBAL_SETTINGS_SCOPE } from "./ChannelSettings";
import { ChannelModelCatalog } from "./ChannelModelCatalog";
import type { ManagedChannel } from "./channelManagement";
import type { ConfiguredChannelAddDraft, SubmitChannelAdd } from "./channelAdd";
import { Spinner } from "./ui";
import type { Channel } from "./types";
import { serviceTierFilterLabels, serviceTierName, serviceTiers, type ServiceTier } from "./serviceTierStatus";
import { siteTierStatus } from "./serviceTierTable";
import type { InstalledChannel } from "./sub2apiImports";
import { WorkbenchNotice } from "./Workbench";

type Document = Record<string, unknown>;
const upstream400RetriesPath = "/preferences/unknown_upstream_400_retries";
type TierRoutingView = {
  scope: string;
  routing?: InstalledChannel["service_tier_routing"];
  group_results?: NonNullable<InstalledChannel["group_tier_results"]>[string];
  group_results_unavailable?: boolean;
};
interface Field {
  path: string;
  group: string;
  type: string;
  minimum?: number;
  maximum?: number;
  note?: string;
}
interface SettingsView {
  provider: string;
  revision: string;
  kind: string;
  conflict?: string;
  base: Document;
  effective: Document;
  override_paths: string[];
  affected_keys: { key_id: string; models: string[] }[];
  api_key_id?: string;
  available_keys: { key_id: string; position: number }[];
  global_preferences: Document;
  resolved_fields?: Record<
    string,
    {
      value: unknown;
      source: string;
      inherited_value: unknown;
      inherited_source: string;
      can_inherit?: boolean;
      read_only?: boolean;
      note?: string;
    }
  >;
  schema: {
    all_upstream_400_retries?: boolean;
    version: number;
    fields: Field[];
    engines: string[];
    key_algorithms: string[];
  };
}
interface Change {
  provider: string;
  set: Record<string, unknown>;
  remove: string[];
  reset?: boolean;
  copy_to_key?: string;
}
interface Result {
  status: string;
  operation_id: string;
  revision: string;
  message?: string;
  previews?: {
    provider: string;
    before: Document;
    after: Document;
    affected_keys: SettingsView["affected_keys"];
    previous_keys?: SettingsView["affected_keys"];
    sample: unknown;
  }[];
}
const labels: Record<string, string> = {
  base_url: "上游地址",
  engine: "引擎",
  model: "模型映射",
  api: "上游 API key",
  api_key_schedule_algorithm: "多密钥调度",
  api_key_rate_limit: "密钥限额（默认或按模型）",
  model_timeout: "模型超时（秒）",
  hedging: "Hedging 并行请求",
  unknown_upstream_400_retries: "所有上游400换渠道次数",
  service_tier_routing: "Fast / Ultrafast 路由",
  timeout_policy: "条件超时策略",
  keepalive_interval: "心跳间隔（秒）",
  cooldown_period: "渠道冷却（秒）",
  api_key_cooldown_period: "密钥冷却（秒）",
  api_key_rate_limit_cooldown_period: "限流冷却（秒）",
  api_key_quota_cooldown_period: "额度冷却（秒）",
  AUTO_RETRY: "渠道自动重试",
  exclude_endpoints: "排除端点",
  only_request_types: "仅允许请求类型",
  exclude_request_types: "排除请求类型",
  compaction_ultrafast_only: "仅对 Ultrafast 请求开启远程压缩",
  exclude_request_rules: "条件排除规则",
  explicit_cache_guard: "显式缓存前缀重写回避",
  max_request_body_bytes: "请求体大小上限",
  headers: "自定义请求头",
  post_body_parameter_overrides: "请求体覆盖与删除",
  normalize_responses_custom_tool_call_ids: "工具调用 ID 归一化",
  upstream_stream_mode: "上游流式模式（Responses）",
  tools: "工具调用",
  image: "图片能力",
  proxy: "出口代理",
  balance_query: "查询上游余额",
  project_id: "项目 ID",
  region: "区域",
  client_email: "服务账号邮箱",
  private_key: "服务账号私钥",
  aws_access_key: "AWS access key",
  aws_secret_key: "AWS secret key",
  aws_session_token: "AWS session token",
  cf_account_id: "Cloudflare 账号 ID",
};
const categories: Record<string, typeof Settings2> = {
  基本与模型: SlidersHorizontal,
  模型与接入: Layers,
  密钥: KeyRound,
  并行请求: Layers,
  重试保护: ShieldCheck,
  超时与冷却: Timer,
  请求规则: Filter,
  请求改写: Braces,
  网络与适配: Globe2,
  高级配置: FileCode2,
  模板与批量: Layers,
  变更记录: History,
};
const keyOf = (path: string) => path.split("/").at(-1)!;
const ultrafastCompactionPath = "/preferences/compaction_ultrafast_only";
const onlyRequestTypesPath = "/only_request_types";
const excludeRequestTypesPath = "/exclude_request_types";
const compactionControls = [
  { path: onlyRequestTypesPath, label: "仅允许压缩请求", description: "此渠道不接收普通请求。" },
  { path: excludeRequestTypesPath, label: "排除压缩请求", description: "此渠道不接收远程压缩请求。" },
  { path: ultrafastCompactionPath, label: labels.compaction_ultrafast_only, description: "压缩不能降为普通档重试。普通请求仍按其他渠道规则转发。" },
];
const requestTypes = (value: unknown): string[] =>
  Array.isArray(value)
    ? value.filter((v): v is string => typeof v === "string")
    : typeof value === "string"
      ? [value]
      : [];
const get = (doc: Document, path: string): unknown =>
  path
    .slice(1)
    .split("/")
    .reduce<unknown>(
      (v, k) => (v && typeof v === "object" ? (v as Document)[k] : undefined),
      doc,
    );
function set(doc: Document, path: string, value: unknown) {
  const next = structuredClone(doc);
  let node = next;
  const parts = path.slice(1).split("/");
  for (const key of parts.slice(0, -1)) {
    if (!node[key] || typeof node[key] !== "object") node[key] = {};
    node = node[key] as Document;
  }
  if (value === undefined) delete node[parts.at(-1)!];
  else node[parts.at(-1)!] = value;
  return next;
}
const canonical = (value: unknown): unknown =>
  Array.isArray(value)
    ? value.map(canonical)
    : value && typeof value === "object"
      ? Object.fromEntries(
          Object.entries(value)
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([k, v]) => [k, canonical(v)]),
        )
      : value;
const equal = (a: unknown, b: unknown) =>
  JSON.stringify(canonical(a)) === JSON.stringify(canonical(b));

const isCompaction = (type: string) => type.trim().toLowerCase() === "compaction";

function CompactionControls({ view, draft, fields, busy, onChange }: {
  view: SettingsView;
  draft: Document;
  fields: Field[];
  busy: boolean;
  onChange: (next: Document) => void;
}) {
  const id = useId();
  const supported = compactionControls.filter(({ path }) => fields.some((field) => field.path === path));
  const listFields = supported.filter(({ path }) => path !== ultrafastCompactionPath);
  const active = (path: string) => path === ultrafastCompactionPath
    ? get(draft, path) === true
    : requestTypes(get(draft, path)).some(isCompaction);
  const withoutCompaction = (doc: Document, path: string) => {
    const types = requestTypes(get(doc, path)).filter((type) => !isCompaction(type));
    const saved = get(view.effective, path);
    // Restore the original representation on a round trip, including a missing field.
    return set(doc, path, equal(types, requestTypes(saved)) ? saved : types);
  };
  const disableUltrafast = (doc: Document) => {
    const saved = get(view.effective, ultrafastCompactionPath);
    if (saved === undefined && view.effective.preferences === undefined &&
      equal(doc.preferences, { compaction_ultrafast_only: true }))
      return set(doc, "/preferences", undefined);
    return set(doc, ultrafastCompactionPath, saved === undefined ? undefined : false);
  };
  function toggle(path: string) {
    const enabled = !active(path);
    let next = path === ultrafastCompactionPath
      ? enabled ? set(draft, path, true) : disableUltrafast(draft)
      : enabled ? set(draft, path, [...requestTypes(get(draft, path)), "compaction"])
        : withoutCompaction(draft, path);
    if (enabled) {
      const conflicts = path === excludeRequestTypesPath
        ? [onlyRequestTypesPath, ultrafastCompactionPath]
        : [excludeRequestTypesPath];
      for (const other of conflicts) {
        if (supported.some((field) => field.path === other) && active(other))
          next = other === ultrafastCompactionPath ? disableUltrafast(next) : withoutCompaction(next, other);
      }
    }
    onChange(next);
  }

  if (!supported.length) return null;
  return <div className="settings-compaction-controls" role="group" aria-label="远程压缩规则">
    {supported.map(({ path, label, description }, index) => {
      const labelId = `${id}-${index}`;
      const baseValue = get(view.base, path);
      return <div className="settings-tier-row settings-compaction-row" key={path}>
        <div className="settings-compaction-copy">
          <span id={labelId}>{label}</span>
          <small id={`${labelId}-hint`} className="muted">{description}</small>
        </div>
        <button type="button" className="settings-icon-button settings-reset-field"
          hidden={equal(get(draft, path), baseValue)} disabled={busy}
          title="恢复基础值" aria-label={`${labels[keyOf(path)]}：恢复基础值`}
          onClick={() => onChange(set(draft, path, baseValue))}><RotateCcw size={15} /></button>
        <button type="button" className="channel-detection-switch" role="switch"
          aria-labelledby={labelId} aria-describedby={`${labelId}-hint`} aria-checked={active(path)}
          disabled={busy} onClick={() => toggle(path)}><span /></button>
      </div>;
    })}
    {listFields.length > 0 && <details className="settings-compaction-advanced">
      <summary>编辑请求类型列表（高级）</summary>
      {listFields.map(({ path }) => <div key={path}>
        <span>{labels[keyOf(path)]}</span>
        <JSONField label={labels[keyOf(path)]} value={get(draft, path)}
          onChange={(value) => onChange(set(draft, path, value))} />
      </div>)}
    </details>}
  </div>;
}
export function settingsDiff(view: SettingsView, draft: Document): Change {
  const change: Change = { provider: view.provider, set: {}, remove: [] };
  for (const field of view.schema.fields) {
    const before = get(view.effective, field.path),
      after = get(draft, field.path);
    if (!equal(before, after)) {
      if (after === undefined) change.remove.push(field.path);
      else change.set[field.path] = after;
    }
  }
  return change;
}
function NumberField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  const serialized = value === undefined ? "" : String(value);
  const [text, setText] = useState(serialized);
  const focused = useRef(false);
  useEffect(() => {
    if (!focused.current) setText(serialized);
  }, [serialized]);
  return (
    <label>
      <span className="sr-only">{label}</span>
      <input
        aria-label={label}
        type="number"
        min={0}
        step="any"
        value={text}
        onFocus={() => {
          focused.current = true;
        }}
        onBlur={() => {
          focused.current = false;
          setText(serialized);
        }}
        onChange={(e) => {
          setText(e.target.value);
          onChange(e.target.value === "" ? undefined : Number(e.target.value));
        }}
      />
    </label>
  );
}
function JSONField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  const serialized = value === undefined ? "" : JSON.stringify(value, null, 2);
  const [text, setText] = useState(serialized);
  const [error, setError] = useState("");
  const focused = useRef(false);
  useEffect(() => {
    if (!focused.current) {
      setText(serialized);
      setError("");
    }
  }, [serialized]);
  return (
    <label className="setting-json">
      <span className="sr-only">{label}</span>
      <textarea
        aria-label={label}
        aria-invalid={!!error}
        value={text}
        rows={5}
        onFocus={() => {
          focused.current = true;
        }}
        onBlur={() => {
          focused.current = false;
          if (!error) setText(serialized);
        }}
        onChange={(e) => {
          const value = e.target.value;
          setText(value);
          try {
            const parsed = value.trim() ? JSON.parse(value) : undefined;
            onChange(parsed);
            setError("");
          } catch {
            setError("JSON 格式无效，修正后才会进入草稿");
          }
        }}
      />
      {error && (
        <span role="alert" className="negative">
          {error}
        </span>
      )}
    </label>
  );
}
function Keys({
  value,
  onChange,
  secretsPath,
}: {
  value: unknown;
  onChange: (v: unknown) => void;
  secretsPath: string;
}) {
  const [revealed, setRevealed] = useState<Record<string, string> | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState<number>();
  const request = useRef<AbortController | null>(null);
  useEffect(
    () => () => {
      request.current?.abort();
    },
    [],
  );
  const reference = (value: unknown) =>
    value && typeof value === "object" && "$secret" in value
      ? String(value.$secret)
      : undefined;
  const text = (value: unknown) =>
    typeof value === "string"
      ? value
      : revealed?.[reference(value) || ""] || "";
  async function reveal() {
    if (revealed !== null) {
      setRevealed(null);
      setCopied(undefined);
      setError("");
      return;
    }
    setLoading(true);
    setError("");
    const abort = new AbortController();
    request.current = abort;
    try {
      const result = await controlRequest<{ keys: Record<string, string> }>(
        secretsPath,
        { signal: abort.signal, cache: "no-store" },
      );
      if (!abort.signal.aborted) setRevealed(result.keys);
    } catch (e) {
      if (!abort.signal.aborted)
        setError(e instanceof Error ? e.message : "密钥读取失败");
    } finally {
      if (!abort.signal.aborted) setLoading(false);
    }
  }
  async function copy(value: unknown, index: number) {
    try {
      await navigator.clipboard.writeText(text(value));
      setCopied(index);
      setError("");
    } catch {
      setError("复制失败，请选中密钥后复制");
    }
  }

  const rows = Array.isArray(value)
    ? value
    : value === undefined
      ? []
      : [value];
  const save = (next: unknown[]) => {
    setCopied(undefined);
    onChange(!Array.isArray(value) && next.length === 1 ? next[0] : next);
  };
  return (
    <div className="settings-keys">
      <div className="settings-key-toolbar">
        <button
          className="button small ghost"
          disabled={loading}
          onClick={() => void reveal()}
        >
          {loading ? (
            <Spinner small />
          ) : revealed !== null ? (
            <EyeOff size={15} />
          ) : (
            <Eye size={15} />
          )}{" "}
          {loading ? "正在读取…" : revealed !== null ? "隐藏密钥" : "显示密钥"}
        </button>
      </div>
      {error && (
        <p className="settings-key-error negative" role="alert">
          {error}
        </p>
      )}
      {rows.map((key, i) => (
        <div className="settings-key-row" key={i}>
          <label>
            密钥 {i + 1}
            <input
              aria-label={`上游密钥 ${i + 1}`}
              type={revealed !== null ? "text" : "password"}
              autoComplete="new-password"
              spellCheck={false}
              placeholder={
                typeof key === "object" ? "已保存 · 填写以替换" : "粘贴密钥"
              }
              value={text(key)}
              onChange={(e) =>
                save(rows.map((v, n) => (n === i ? e.target.value : v)))
              }
            />
          </label>
          {revealed !== null && (
            <button
              className="settings-icon-button settings-copy-key"
              title={copied === i ? "已复制" : "复制密钥"}
              aria-label={`复制密钥 ${i + 1}`}
              disabled={!text(key)}
              onClick={() => void copy(key, i)}
            >
              {copied === i ? <Check size={15} /> : <Copy size={15} />}
            </button>
          )}
          <button
            className="settings-icon-button"
            title="上移"
            aria-label={`上移密钥 ${i + 1}`}
            disabled={i === 0}
            onClick={() => {
              const next = [...rows];
              [next[i - 1], next[i]] = [next[i], next[i - 1]];
              save(next);
            }}
          >
            <ArrowUp size={15} />
          </button>
          <button
            className="settings-icon-button"
            title="下移"
            aria-label={`下移密钥 ${i + 1}`}
            disabled={i === rows.length - 1}
            onClick={() => {
              const next = [...rows];
              [next[i + 1], next[i]] = [next[i], next[i + 1]];
              save(next);
            }}
          >
            <ArrowDown size={15} />
          </button>
          <button
            className="settings-icon-button is-destructive"
            title="删除密钥"
            aria-label={`删除密钥 ${i + 1}`}
            onClick={() => save(rows.filter((_, n) => n !== i))}
          >
            <Trash2 size={14} />
          </button>
        </div>
      ))}
      <button className="button small" onClick={() => save([...rows, ""])}>
        <Plus size={14} />
        添加密钥
      </button>
    </div>
  );
}
import type { SettingsChannel } from "./ChannelSettings";
export function Editor({
  row,
  onClose,
  member,
  prices,
  initialModelTab,
  submitAdd,
}: {
  row: SettingsChannel;
  onClose: () => void;
  member?: ManagedChannel;
  prices?: import("./types").ModelPrice[];
  initialModelTab?: boolean;
  submitAdd?: SubmitChannelAdd<ConfiguredChannelAddDraft>;
}) {
  const client = useQueryClient();
  const path = `/v1/sources/${encodeURIComponent(row.source_id!)}/channel-settings`;
  const query = useQuery({
    queryKey: ["channel-settings", row.source_id, row.provider],
    queryFn: ({ signal }) =>
      controlRequest<SettingsView>(
        path + "?" + new URLSearchParams({ provider: row.provider }),
        { signal },
      ),
    retry: false,
    staleTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  const [draft, setDraft] = useState<Document>();
  const [resetOverride, setResetOverride] = useState(false);
  const isGlobal = row.provider === GLOBAL_SETTINGS_SCOPE;
  const [tab, setTab] = useState(isGlobal ? "超时与冷却" : initialModelTab ? "模型与接入" : "基本与模型");
  const [tierDraft, setTierDraft] = useState<Partial<Record<ServiceTier, boolean>>>({});
  const [tierScope, setTierScope] = useState("");
  const hasTierDraft = Object.keys(tierDraft).length > 0;
  const tierRouting = useQuery({
    queryKey: ["service-tier-routing", row.source_id, row.provider, row.model],
    queryFn: ({ signal }) => controlRequest<TierRoutingView>(
      `/v1/sources/${encodeURIComponent(row.source_id!)}/service-tier-routing?` + new URLSearchParams({provider: row.provider, model: row.model}), { signal }),
    enabled: !isGlobal && !!row.source_id && tab === "请求规则" && !hasTierDraft,
    retry: false, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false,
  });
  const [modelVisited, setModelVisited] = useState(!isGlobal && !!initialModelTab);
  const bodyRef = useRef<HTMLDivElement>(null);
  const previewRef = useRef<HTMLElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [preview, setPreview] = useState<Result>();
  const [operation, setOperation] = useState("");
  const [format, setFormat] = useState<"json" | "yaml">("json");
  const [raw, setRaw] = useState("");
  const [templateName, setTemplateName] = useState("");
  const [batch, setBatch] = useState<string[]>([]);
  const [copyToKey, setCopyToKey] = useState("");
  useEffect(() => {
    if (bodyRef.current) bodyRef.current.scrollTop = 0;
  }, [tab]);
  useEffect(() => {
    if (preview?.status === "validated") previewRef.current?.scrollIntoView?.({ block: "nearest" });
  }, [preview]);
  const [sample, setSample] = useState<unknown>({
    model: row.model,
    endpoint: "/v1/responses",
    stream: true,
    body: { model: row.model, input: "示例请求" },
  });
  const templates = useQuery({
    queryKey: ["channel-setting-templates"],
    queryFn: () =>
      controlRequest<{
        data: {
          id: string;
          name: string;
          patch: { set: Record<string, unknown>; remove: string[] };
        }[];
      }>("/v1/channel-setting-templates"),
    retry: false,
  });
  const audit = useQuery({
    queryKey: ["channel-settings-audit", row.source_id],
    queryFn: () =>
      controlRequest<{
        data: {
          id: string;
          status: string;
          created_at: number;
          result: Result;
        }[];
      }>(path + "/operations"),
    enabled: tab === "变更记录",
    retry: false,
  });
  const catalog = useQuery({
    queryKey: ["settings-channel-catalog", row.source_id],
    queryFn: () =>
      controlRequest<{ data: Channel[] }>(
        `/v1/sources/${encodeURIComponent(row.source_id!)}/proxy/v1/model-channels?endpoint=all&stream=all`,
      ),
    enabled: !isGlobal && (tab === "模板与批量" || tab === "基本与模型"),
    retry: false,
  });
  const [editingView, setEditingView] = useState<SettingsView>();
  const view = editingView || query.data;
  useEffect(() => {
    if (view && !draft) {
      setEditingView(view);
      setDraft(structuredClone(view.effective));
    }
  }, [view, draft]);
  const edit = (next: Document) => {
    setDraft(next);
    setPreview(undefined);
    setOperation("");
    setError("");
    setNotice("");
  };
  async function refreshed() {
    const result = await query.refetch();
    if (result.data) {
      setEditingView(result.data);
      setDraft(structuredClone(result.data.effective));
      setRaw(
        format === "json"
          ? JSON.stringify(result.data.effective, null, 2)
          : stringify(result.data.effective),
      );
    }
    if (hasTierDraft || (!isGlobal && tab === "请求规则")) await tierRouting.refetch();
    setResetOverride(false);
    setTierDraft({});
    setTierScope("");
    setCopyToKey("");
    setBatch([]);
    await Promise.all([
      audit.refetch(),
      ...[
        "catalog",
        "control-catalog",
        "channel-controls",
        "channel-sites",
        "sub2api-imports",
        "metrics",
        "channel-info",
        "channel-management",
        "channel-routes",
        "settings-channel-catalog",
        "channel-model-catalog",
        "service-tier-routing",
        "service-tier-members",
        "service-tier-table",
      ].map((name) => client.invalidateQueries({ queryKey: [name] })),
    ]);
  }
  function readDraft() {
    if (tab === "高级配置") {
      const value = format === "json" ? JSON.parse(raw) : parse(raw);
      if (!value || typeof value !== "object" || Array.isArray(value))
        throw Error("高级配置必须是单渠道对象");
      if (!isGlobal && value.provider !== view?.provider)
        throw Error("渠道标识不能更改");
      if (!isGlobal && !equal(get(value, "/model"), get(view!.effective, "/model")))
        throw Error("实际路由模型只能在「模型与接入」中修改");
      const full = structuredClone(view!.effective);
      for (const f of view!.schema.fields) {
        const patched = set(full, f.path, get(value, f.path));
        Object.keys(full).forEach((k) => delete full[k]);
        Object.assign(full, patched);
      }
      if (!equal(full, value))
        throw Error("仅允许修改本来源支持的字段，其他字段请保持原值");
      return value as Document;
    }
    return draft!;
  }
  async function run(apply: boolean, reset = false) {
    if (!view || !draft || busy) return;
    if (
      bodyRef.current?.querySelector('[aria-invalid="true"]')
    ) {
      setError("请先修正无效的 JSON 字段");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const tierChoices = reset ? [] : serviceTiers.filter(tier => tierDraft[tier] !== undefined)
        .map(tier => ({provider: row.provider, scope: tierScope, tier, enabled: tierDraft[tier]}));
      if (tierChoices.length && (resetOverride || copyToKey || batch.length || tierRouting.isError || !tierScope || tierScope !== tierRouting.data?.scope))
        throw Error("请先单独保存档位开关，或撤销修改并重新读取档位配置。");
      const changes: Change[] = [
        reset
          ? { ...settingsDiff({ ...view, effective: view.base }, resetDocument(view)), reset: true }
          : resetOverride
            ? {
                ...settingsDiff({ ...view, effective: view.base }, readDraft()),
                reset: true,
              }
            : settingsDiff(view, readDraft()),
      ];
      if (copyToKey) changes[0].copy_to_key = copyToKey;
      for (const provider of batch)
        if (provider !== row.provider)
          changes.push({ ...changes[0], provider });
      const id = operation || crypto.randomUUID();
      const body = {
        revision: view.revision,
        operation_id: id,
        changes,
        sample,
        ...(tierChoices.length ? {service_tier_choices: tierChoices} : {}),
      };
      if (!apply) {
        const result = await controlRequest<Result>(path + "/validate", {
          method: "POST",
          body: JSON.stringify(body),
        });
        setPreview(result);
        setOperation(id);
        setNotice("校验通过");
      } else {
        setOperation(id);
        setPreview(undefined);
        const result = await controlRequest<Result>(path, {
          method: "PATCH",
          body: JSON.stringify(body),
        });
        setOperation(id);
        setPreview(result);
        if (result.status === "applied") {
          setNotice("设置已保存并应用");
          setOperation("");
          await refreshed();
        } else {
          setError(result.message || "应用结果待确认");
        }
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "操作失败");
    } finally {
      setBusy(false);
    }
  }
  function resetDocument(view: SettingsView) {
    return isGlobal ? structuredClone(view.base) : set(view.base, "/model", get(view.effective, "/model"));
  }
  async function reconcile(id = operation) {
    setBusy(true);
    setError("");
    try {
      const result = await controlRequest<Result>(
        path + "/operations/" + encodeURIComponent(id),
      );
      if (result.status === "applied") {
        setNotice("已确认保存并应用");
        setOperation("");
        await refreshed();
      } else {
        setError(result.status === "rejected" ? "修改被拒绝，请刷新设置后重试" : "应用待确认");
      }
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  async function rollback(id: string) {
    if (!view) return;
    setBusy(true);
    setError("");
    try {
      const result = await controlRequest<Result>(path + "/rollback", {
        method: "POST",
        body: JSON.stringify({
          rollback_id: id,
          revision: view.revision,
          operation_id: crypto.randomUUID(),
        }),
      });
      if (result.status === "applied") {
        setNotice("已回滚并保存");
        await refreshed();
      } else {
        setError(result.message || "回滚待确认");
      }
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  if (query.isPending || (query.isSuccess && !draft)) return <DetailLoading kind="form" label="渠道设置" />;
  if (!view || !draft)
    return (
      <div className="settings-loading">
        <p role="alert" className="negative">
          {query.error?.message || "设置读取失败"}
        </p>
        <button className="button" onClick={() => void query.refetch()}>
          重新读取
        </button>
        <button className="button" onClick={onClose}>
          关闭
        </button>
      </div>
    );
  const savedEngine = catalog.data?.data.find(
    (channel) => channel.provider === row.provider,
  );
  const autoEngineLabel = !equal(draft.base_url, view.effective.base_url)
    ? "自动识别"
    : view.effective.engine != null
      ? "自动识别"
      : savedEngine?.engine_mode === "auto" && savedEngine.engine
        ? `自动识别（实际：${savedEngine.engine}）`
        : "自动识别";
  const visibleFields = view.schema.fields.filter(
    (f) =>
      f.path !== "/model" &&
      (f.path !== upstream400RetriesPath ||
        view.schema.all_upstream_400_retries === true),
  );
  const inlineCompactionFields = isGlobal ? [] : visibleFields.filter(
    (field) => field.group === "请求规则" && compactionControls.some(({ path }) => path === field.path),
  );
  const retryField = visibleFields.find(f => f.path === upstream400RetriesPath);
  const retryMin = retryField?.minimum ?? 0;
  const retryMax = retryField?.maximum ?? 3;
  const retryCount = get(draft, upstream400RetriesPath);
  const invalidRetryCount = !!retryField && retryCount !== undefined &&
    (typeof retryCount !== "number" || !Number.isSafeInteger(retryCount) || retryCount < retryMin || retryCount > retryMax);
  const fieldGroups = [...new Set(visibleFields.map(f => f.group))];
  const groups = [
    ...(isGlobal ? fieldGroups : [
      ...fieldGroups.filter(group => group === "基本与模型"),
      "模型与接入",
      ...fieldGroups.filter(group => group !== "基本与模型"),
    ]),
    "高级配置",
    ...(!isGlobal ? ["模板与批量"] : []),
    "变更记录",
  ];
  const dirty =
    !equal(draft, view.effective) ||
    Object.keys(tierDraft).length > 0 ||
    resetOverride ||
    !!copyToKey ||
    batch.length > 0 ||
    (tab === "高级配置" &&
      raw !==
        (format === "json"
          ? JSON.stringify(draft, null, 2)
          : stringify(draft)));
  const modelTab = tab === "模型与接入";
  return (
    <>
      <div className="settings-workspace">
        <aside className="settings-sidebar">
          <nav className="settings-tabs" aria-label="渠道设置类别">
            {groups.map((group) => {
              const Icon = categories[group] || Settings2;
              return (
                <button
                  className={tab === group ? "active" : ""}
                  aria-pressed={tab === group}
                  key={group}
                  disabled={busy}
                  onClick={() => {
                    if (tab === group) return;
                    if (
                      bodyRef.current?.querySelector(
                        '[aria-invalid="true"]',
                      )
                    ) {
                      setError("请先修正无效的 JSON 字段");
                      return;
                    }
                    if (group === "模型与接入" && dirty) {
                      setError("请先保存其他设置，或撤销修改。");
                      return;
                    }
                    if (tab === "高级配置") {
                      try {
                        edit(readDraft());
                      } catch (e) {
                        setError(String(e));
                        return;
                      }
                    }
                    if (group === "高级配置")
                      setRaw(
                        format === "json"
                          ? JSON.stringify(draft, null, 2)
                          : stringify(draft),
                      );
                    if (group === "模型与接入") setModelVisited(true);
                    setTab(group);
                  }}
                >
                  <Icon size={16} />
                  <span>{group === "基本与模型" ? "基本设置" : group}</span>
                </button>
              );
            })}
          </nav>
        </aside>
        <div className="settings-main">
          {modelVisited && row.source_id && <div className="settings-model-panel" hidden={!modelTab}>
            <ChannelModelCatalog key={row.provider} source={row.source_id} provider={row.provider} revision={view.revision}
              member={member} prices={prices} ownerKey={view.api_key_id} close={onClose} onSettingsChanged={refreshed} submitAdd={submitAdd} />
          </div>}
          {!modelTab && <>
        <div className="settings-body" ref={bodyRef}>
          <div className="settings-section-heading">
            <h3>{tab === "基本与模型" ? "基本设置" : tab}</h3>
          </div>
          {view.conflict && (
            <p role="alert" className="settings-feedback is-error">
              基础配置与覆盖冲突，正在沿用上次有效设置：{view.conflict}
            </p>
          )}

          {!isGlobal && tab === "请求规则" && <section className="settings-tier-section" aria-label="服务档位与远程压缩">
            <div className="settings-tier-heading">
              <h4>{inlineCompactionFields.length ? "服务档位与远程压缩" : "服务档位"}</h4>
              <div className="settings-tier-tools">
              <WorkbenchNotice urgent={tierRouting.isError} label="档位配置读取失败" messages={[tierRouting.isError && tierRouting.error.message]} />
              <button type="button" className="settings-icon-button" title="重新读取档位配置"
                aria-label="重新读取档位配置" disabled={busy || tierRouting.isFetching || hasTierDraft}
                onClick={() => void tierRouting.refetch()}><RotateCcw size={18} /></button>
              </div>
            </div>
            {serviceTiers.map(tier => {
              const state = tierRouting.data?.routing?.[tier];
              const available = !!state && !!tierRouting.data?.scope && !tierRouting.isError;
              // With no bound receipt, the gateway cannot route this tier.
              const allowed = state === "enabled";
              const value = tierDraft[tier] ?? allowed;
              const groupStatus = siteTierStatus(tierRouting.data?.group_results?.[tier]);
              const verified = groupStatus === "supported" || state === "enabled" || state === "disabled_verified";
              const evidence = verified ? "已验证支持" : groupStatus ? serviceTierFilterLabels[groupStatus] :
                tierRouting.data?.group_results_unavailable ? "检测记录读取失败" : "未验证";
              const detail = tierRouting.isError ? "读取失败" : tierRouting.isPending ? "读取中…" :
                !available ? "来源不支持" : tierDraft[tier] !== undefined ? "待保存" :
                  allowed ? evidence : state === "unconfigured" ? (verified ? "未开启 · 已验证支持" : evidence) :
                    verified ? "已关闭 · 已验证" :
                    groupStatus ? "已关闭 · " + evidence : "已关闭";
              return <div className="settings-tier-row" key={tier}>
                <label id={`tier-switch-${tier}`} htmlFor={`tier-toggle-${tier}`}>允许 {serviceTierName[tier]} 请求</label>
                <span className={detail === "已验证支持" ? "positive" : detail === "未验证" ? "settings-tier-warning" : "muted"}>{detail}</span>
                <button id={`tier-toggle-${tier}`} type="button" className="channel-detection-switch"
                  role="switch" aria-labelledby={`tier-switch-${tier}`} aria-checked={value}
                  disabled={busy || !available || tierRouting.isFetching || resetOverride || !!copyToKey || batch.length > 0}
                  onClick={() => {
                    edit(draft);
                    setTierDraft(current => {
                      const next = {...current};
                      if ((!value) === allowed) delete next[tier]; else next[tier] = !value;
                      return next;
                    });
                    if (!hasTierDraft) setTierScope(tierRouting.data!.scope);
                  }}><span /></button>
              </div>;
            })}
            <CompactionControls view={view} draft={draft} fields={inlineCompactionFields} busy={busy} onChange={edit} />
          </section>}

          {visibleFields
            .filter((f) => f.group === tab && !inlineCompactionFields.includes(f))
            .map((f) => {
              const ownValue = get(draft, f.path),
                resolved = view.resolved_fields?.[f.path],
                value =
                  ownValue === undefined
                    ? (resolved?.inherited_value ?? resolved?.value)
                    : ownValue,
                label = labels[keyOf(f.path)] || keyOf(f.path);
              const inherited = ownValue === undefined;
              const inheritLabel = `${label}：${isGlobal ? "使用运行时默认值" : "继承全局设置"}`;
              return (
                <section
                  className={`settings-field ${["keys", "models", "json"].includes(f.type) || (value && typeof value === "object") ? "settings-field-wide" : ""}`}
                  key={f.path}
                >
                  <header className="settings-field-heading">
                    <h4>{label}</h4>
                    {resolved && <small className="settings-field-origin">{inherited ? (isGlobal ? "运行时默认" : "继承全局") : (isGlobal ? "全局配置" : "渠道覆盖")}</small>}
                    <div className="settings-field-actions">
                    {resolved?.can_inherit && (
                      <button
                        type="button"
                        className="settings-icon-button settings-reset-field"
                        hidden={inherited}
                        aria-label={inheritLabel}
                        title={inheritLabel}
                        onClick={() => edit(set(draft, f.path, undefined))}
                      >
                        <CornerUpLeft size={14} />
                      </button>
                    )}
                      <button
                        type="button"
                        className="settings-icon-button settings-reset-field"
                        hidden={equal(ownValue, get(view.base, f.path))}
                        title="恢复基础值"
                        aria-label={`${label}：恢复基础值`}
                        onClick={() =>
                          edit(set(draft, f.path, get(view.base, f.path)))
                        }
                      >
                        <RotateCcw size={15} />
                      </button>
                    </div>
                  </header>
                  <div className="settings-field-control">
                    {resolved?.read_only ? (
                      <p>{String(resolved.value)}</p>
                    ) : !isGlobal &&
                      f.path ===
                        "/preferences/post_body_parameter_overrides" ? (
                      <>
                        <label className="settings-check">
                          <input
                            type="checkbox"
                            checked={removesImageGeneration(value)}
                            onChange={(e) =>
                              edit(
                                set(
                                  draft,
                                  f.path,
                                  setImageGenerationRemoval(
                                    value,
                                    e.target.checked,
                                  ),
                                ),
                              )
                            }
                          />
                          移除生图参数
                        </label>
                        <details>
                          <summary>请求体覆盖与删除（高级 JSON）</summary>
                          <JSONField
                            label={label}
                            value={value}
                            onChange={(v) => edit(set(draft, f.path, v))}
                          />
                        </details>
                      </>
                    ) : f.path === "/preferences/upstream_stream_mode" ? (
                      <>
                        <select
                          aria-label={label}
                          value={
                            typeof value === "string"
                              ? value
                              : String(
                                  (value as Document | undefined)?.default ??
                                    "follow_client",
                                )
                          }
                          onChange={(e) =>
                            edit(
                              set(
                                draft,
                                f.path,
                                value && typeof value === "object"
                                  ? {
                                      ...(value as Document),
                                      default: e.target.value,
                                    }
                                  : e.target.value,
                              ),
                            )
                          }
                        >
                          <option value="follow_client">
                            跟随客户端（默认）
                          </option>
                          <option value="stream_only">
                            仅支持流式（非流式请求自动聚合）
                          </option>
                        </select>
                        <details>
                          <summary>按模型覆盖（高级 JSON）</summary>
                          <JSONField
                            label={label + "高级 JSON"}
                            value={value ?? "follow_client"}
                            onChange={(v) => edit(set(draft, f.path, v))}
                          />
                        </details>
                      </>
                    ) : f.path === "/preferences/timeout_policy" ? (
                      <>
                        <TimeoutPolicyField
                          value={value}
                          onChange={(v) => edit(set(draft, f.path, v))}
                        />
                        <details>
                          <summary>高级 JSON</summary>
                          <JSONField
                            label={label}
                            value={value}
                            onChange={(v) => edit(set(draft, f.path, v))}
                          />
                        </details>
                      </>
                    ) : f.path === "/preferences/hedging" ? (
                      <HedgingField
                        value={value}
                        onChange={(v) => edit(set(draft, f.path, v))}
                      />
                    ) : f.type === "keys" ? (
                      <Keys
                        key={view.revision}
                        secretsPath={
                          path +
                          "/secrets?" +
                          new URLSearchParams({
                            provider: row.provider,
                            revision: view.revision,
                          })
                        }
                        value={value}
                        onChange={(v) => edit(set(draft, f.path, v))}
                      />
                    ) : f.type === "json" ||
                      (value && typeof value === "object") ? (
                      <JSONField
                        label={label}
                        value={value}
                        onChange={(v) => edit(set(draft, f.path, v))}
                      />
                    ) : ["boolean", "engine", "algorithm"].includes(f.type) ? (
                      <label>
                        <span className="sr-only">{label}</span>
                        <select
                          aria-label={label}
                          value={value === undefined ? "" : String(value)}
                          onChange={(e) =>
                            edit(
                              set(
                                draft,
                                f.path,
                                e.target.value === ""
                                  ? undefined
                                  : f.type === "boolean"
                                    ? e.target.value === "true"
                                    : e.target.value,
                              ),
                            )
                          }
                        >
                          <option value="">
                            {f.type === "engine"
                              ? autoEngineLabel
                              : "继承 / 未设置"}
                          </option>
                          {(f.type === "boolean"
                            ? ["true", "false"]
                            : f.type === "engine"
                              ? view.schema.engines
                              : view.schema.key_algorithms
                          ).map((v) => (
                            <option key={v} value={v}>
                              {v === "true"
                                ? "开启"
                                : v === "false"
                                  ? "关闭"
                                  : v}
                            </option>
                          ))}
                        </select>
                      </label>
                    ) : f.path === upstream400RetriesPath && f.type === "integer" ? (
                      <label>
                        <span className="sr-only">{label}</span>
                        <input
                          aria-label={label}
                          aria-invalid={invalidRetryCount || undefined}
                          aria-describedby="settings-retry-count-hint"
                          inputMode="numeric"
                          placeholder="运行时默认值（0，关闭）"
                          value={ownValue === undefined ? "" : String(ownValue)}
                          onChange={({ target: { value: text } }) => {
                            const n = Number(text);
                            edit(set(draft, f.path, text === "" ? undefined :
                              /^\d+$/.test(text) && Number.isSafeInteger(n) ? n : text));
                          }}
                        />
                        <small id="settings-retry-count-hint" className={`settings-retry-count-hint ${invalidRetryCount ? "negative" : "muted"}`} aria-live="polite">
                          {invalidRetryCount ? `请输入 ${retryMin}–${retryMax} 的整数` : `${retryMin}–${retryMax} 次，0 为关闭`}
                        </small>
                      </label>
                    ) : f.type === "number" ? (
                      <NumberField
                        label={label}
                        value={value}
                        onChange={(v) => edit(set(draft, f.path, v))}
                      />
                    ) : (
                      <label>
                        <span className="sr-only">{label}</span>
                        <input
                          aria-label={label}
                          type={f.type === "secret" ? "password" : "text"}
                          value={value === undefined ? "" : String(value)}
                          min={0}
                          onChange={(e) =>
                            edit(
                              set(
                                draft,
                                f.path,
                                e.target.value === ""
                                  ? undefined
                                  : f.type === "number"
                                    ? Number(e.target.value)
                                    : e.target.value,
                              ),
                            )
                          }
                        />
                      </label>
                    )}
                  </div>
                </section>
              );
            })}
          {tab === "高级配置" && (
            <div className="settings-advanced">
              <label className="settings-format">
                格式
                <select
                  aria-label="高级配置格式"
                  value={format}
                  onChange={(e) => {
                    try {
                      const doc = readDraft();
                      const next = e.target.value as "json" | "yaml";
                      setFormat(next);
                      setRaw(
                        next === "json"
                          ? JSON.stringify(doc, null, 2)
                          : stringify(doc),
                      );
                    } catch (e) {
                      setError(String(e));
                    }
                  }}
                >
                  <option value="json">JSON</option>
                  <option value="yaml">YAML</option>
                </select>
              </label>
              <textarea
                className="settings-raw"
                aria-label="渠道高级配置"
                value={raw}
                onChange={(e) => {
                  setRaw(e.target.value);
                  setPreview(undefined);
                  setOperation("");
                }}
              />
            </div>
          )}
          {tab === "模板与批量" && (
            <div className="settings-utilities">
              <h4>复制为 API key 专用渠道</h4>
              <select
                aria-label="复制到 API key"
                value={copyToKey}
                onChange={(e) => {
                  setCopyToKey(e.target.value);
                  setBatch([]);
                  setPreview(undefined);
                  setOperation("");
                }}
              >
                <option value="">编辑原渠道</option>
                {view.available_keys?.map((k) => (
                  <option key={k.key_id} value={k.key_id}>
                    Key {k.position} · {k.key_id.slice(0, 14)}…
                  </option>
                ))}
              </select>
              <h4>模板</h4>
              <select
                aria-label="应用设置模板"
                defaultValue=""
                onChange={(e) => {
                  const t = templates.data?.data.find(
                    (t) => t.id === e.target.value,
                  );
                  if (!t) return;
                  let next = draft;
                  for (const [p, v] of Object.entries(t.patch.set || {}))
                    if (p !== "/model" && !p.startsWith("/model/")) next = set(next, p, v);
                  for (const p of t.patch.remove || [])
                    if (p !== "/model" && !p.startsWith("/model/")) next = set(next, p, undefined);
                  edit(next);
                }}
              >
                <option value="">选择模板</option>
                {templates.data?.data.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name}
                  </option>
                ))}
              </select>
              <input
                aria-label="模板名称"
                placeholder="模板名称"
                value={templateName}
                onChange={(e) => setTemplateName(e.target.value)}
              />
              <button
                className="button small"
                onClick={async () => {
                  try {
                    const change = settingsDiff(view, draft);
                    await controlRequest("/v1/channel-setting-templates", {
                      method: "POST",
                      body: JSON.stringify({
                        set: change.set,
                        remove: change.remove,
                        name: templateName,
                      }),
                    });
                    await templates.refetch();
                    setNotice("模板已保存");
                  } catch (e) {
                    setError(String(e));
                  }
                }}
              >
                将当前差异存为模板
              </button>
              <h4>同时应用到本来源其他渠道</h4>
              {[...new Set(catalog.data?.data.map((c) => c.provider) || [])]
                .filter((p) => p !== row.provider)
                .map((p) => (
                  <label className="settings-check" key={p}>
                    <input
                      type="checkbox"
                      disabled={!!copyToKey}
                      checked={batch.includes(p)}
                      onChange={(e) => {
                        setBatch((v) =>
                          e.target.checked
                            ? [...v, p]
                            : v.filter((x) => x !== p),
                        );
                        setPreview(undefined);
                        setOperation("");
                      }}
                    />
                    {p}
                  </label>
                ))}
            </div>
          )}
          {tab === "变更记录" && (
            <>
              {audit.isPending ? (
                <LoadingIndicator label="读取变更记录" />
              ) : audit.isError ? (
                <p role="alert">审计读取失败</p>
              ) : (
                audit.data?.data.map((a) => (
                  <section className="settings-audit" key={a.id}>
                    <strong>
                      {new Date(a.created_at * 1000).toLocaleString()} ·{" "}
                      {a.status}
                    </strong>
                    <details className="data-disclosure">
                      <summary>查看差异</summary>
                      <pre>{JSON.stringify(a.result.previews, null, 2)}</pre>
                    </details>
                    {["pending", "applied_unretained"].includes(a.status) && (
                      <button
                        className="button small"
                        disabled={busy}
                        onClick={() => void reconcile(a.id)}
                      >
                        核对操作状态
                      </button>
                    )}
                    {a.status === "applied" && (
                      <button
                        className="button small"
                        disabled={busy}
                        onClick={() => void rollback(a.id)}
                      >
                        回滚此操作
                      </button>
                    )}
                  </section>
                ))
              )}
            </>
          )}
          {tab !== "变更记录" && tab !== "模型与接入" && (
            <details className="settings-request-preview">
              <summary>
                <Braces size={16} />
                请求匹配与参数预览
                <ChevronDown size={15} />
              </summary>
              <JSONField
                label="预览请求"
                value={sample}
                onChange={(v) => {
                  setSample(v);
                  setPreview(undefined);
                  setOperation("");
                }}
              />
            </details>
          )}
          {preview && (
            <section className="settings-preview" ref={previewRef}>
              <h4>校验与影响范围</h4>
              {preview.previews?.map((p) => (
                <details key={p.provider} open>
                  <summary>
                    {p.provider} · 影响 {p.affected_keys?.length || 0} 个 API
                    key
                  </summary>
                  <p className="muted">
                    应用后引用：
                    {p.affected_keys
                      ?.map(
                        (k) =>
                          `Key ${view.available_keys?.find((v) => v.key_id === k.key_id)?.position || k.key_id.slice(0, 12)}（${k.models.join("、")}）`,
                      )
                      .join("；") || "无"}
                  </p>
                  {!!p.previous_keys?.length && (
                    <p className="muted">
                      修改前引用：
                      {p.previous_keys
                        .map(
                          (k) =>
                            `Key ${view.available_keys?.find((v) => v.key_id === k.key_id)?.position || k.key_id.slice(0, 12)}（${k.models.join("、")}）`,
                        )
                        .join("；")}
                    </p>
                  )}
                  <div className="settings-diff">
                    <div>
                      <strong>修改前</strong>
                      <pre>{JSON.stringify(p.before, null, 2)}</pre>
                    </div>
                    <div>
                      <strong>修改后</strong>
                      <pre>{JSON.stringify(p.after, null, 2)}</pre>
                    </div>
                  </div>
                  <details>
                    <summary>匹配、超时与请求参数覆盖预览</summary>
                    <pre>{JSON.stringify(p.sample, null, 2)}</pre>
                  </details>
                </details>
              ))}
            </section>
          )}
        </div>
      <div className="settings-feedback-slot">
        {error && <p role="alert" className="settings-feedback is-error">{error}</p>}
        {operation && preview?.status !== "validated" && (
          <button className="button small" disabled={busy} onClick={() => void reconcile()}>核对操作状态</button>
        )}
      </div>
      <span role="status" className="sr-only">{notice}</span>
      <footer className="settings-footer">
        <div className="settings-footer-secondary">
          <button
            className="settings-icon-button"
            aria-label="恢复基础配置"
            title="恢复基础配置"
            disabled={busy}
            onClick={() => {
              edit(resetDocument(view));
              setTierDraft({});
              setTierScope("");
              setResetOverride(true);
              setOperation("");
              void run(false, true);
            }}
          >
            <RotateCcw size={15} />
          </button>
        </div>
        <div className="settings-footer-primary">
          <button
            className="button ghost"
            disabled={busy || !dirty}
            onClick={() => {
              edit(structuredClone(view.effective));
              setTierDraft({});
              setTierScope("");
              setRaw(format === "json" ? JSON.stringify(view.effective, null, 2) : stringify(view.effective));
              setResetOverride(false);
              setCopyToKey("");
              setBatch([]);
            }}
          >
            撤销修改
          </button>
          <button
            className="button"
            disabled={busy || invalidRetryCount}
            onClick={() => void run(false)}
          >
            校验与预览
          </button>
          <button
            className="button primary"
            disabled={busy || invalidRetryCount || !preview || preview.status !== "validated"}
            onClick={() => void run(true)}
          >
            {busy ? <Spinner small /> : <CircleCheck size={16} />}保存并应用
          </button>
        </div>
      </footer>
      </>}
        </div>
      </div>
    </>
  );
}

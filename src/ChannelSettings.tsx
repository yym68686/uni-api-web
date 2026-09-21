import { useEffect, useState, useRef, type ReactNode } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Eye,
  EyeOff,
  Copy,
  Check,
  Settings2,
  Plus,
  Trash2,
  X,
  RotateCcw,
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
  Download,
  ShieldCheck,
  CircleCheck,
  ChevronDown,
} from "lucide-react";
import { parse, stringify } from "yaml";
import { controlRequest } from "./api";
import { Spinner } from "./ui";
import type { Channel } from "./types";

type Document = Record<string, unknown>;
interface Field {
  path: string;
  group: string;
  type: string;
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
  schema: {
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
  exclude_request_rules: "条件排除规则",
  max_request_body_bytes: "请求体大小上限",
  headers: "自定义请求头",
  post_body_parameter_overrides: "请求体覆盖与删除",
  normalize_responses_custom_tool_call_ids: "工具调用 ID 归一化",
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
const categories: Record<
  string,
  { icon: typeof Settings2; description: string }
> = {
  基本与模型: {
    icon: SlidersHorizontal,
    description: "设置上游连接与模型映射。",
  },
  密钥: { icon: KeyRound, description: "管理上游密钥、调用顺序与限额。" },
  超时与冷却: {
    icon: Timer,
    description: "调整请求等待时间与失败后的冷却策略。",
  },
  请求规则: { icon: Filter, description: "选择此渠道可接收的端点和请求类型。" },
  请求改写: { icon: Braces, description: "配置请求头与发送至上游的参数。" },
  网络与适配: { icon: Globe2, description: "管理渠道能力、代理与平台凭据。" },
  高级配置: {
    icon: FileCode2,
    description: "以 JSON 或 YAML 编辑当前渠道的完整配置。",
  },
  模板与批量: {
    icon: Layers,
    description: "复用设置，或创建 API key 专用渠道。",
  },
  变更记录: {
    icon: History,
    description: "查看历史修改、核对应用结果与回滚。",
  },
};
const keyOf = (path: string) => path.split("/").at(-1)!;
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
  useEffect(() => {
    setText(serialized);
    setError("");
  }, [serialized]);
  return (
    <label className="setting-json">
      <span className="sr-only">{label}</span>
      <textarea
        aria-label={label}
        aria-invalid={!!error}
        value={text}
        rows={5}
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
        <p className="muted">可查看、复制或替换密钥，箭头可调整调用顺序。</p>
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
function Models({
  value,
  onChange,
  discover,
}: {
  value: unknown;
  onChange: (v: unknown) => void;
  discover: ReactNode;
}) {
  const rows = Array.isArray(value)
    ? value.flatMap((v) =>
        typeof v === "string"
          ? [{ upstream: v, public: v }]
          : v && typeof v === "object"
            ? Object.entries(v).map(([upstream, alias]) => ({
                upstream,
                public: String(alias),
              }))
            : [],
      )
    : [];
  const save = (next: typeof rows) =>
    onChange(
      next.map((v) =>
        v.upstream === v.public ? v.public : { [v.upstream]: v.public },
      ),
    );
  return (
    <div className="settings-model-editor">
      <div className="settings-model-toolbar">
        <span className="settings-count">{rows.length} 个模型</span>
        <div className="settings-inline-actions">
          {discover}
          <button
            className="button small"
            onClick={() => save([...rows, { upstream: "", public: "" }])}
          >
            <Plus size={15} />
            添加模型
          </button>
        </div>
      </div>
      <div className="settings-model-table">
        <table className="setting-models">
          <thead>
            <tr>
              <th>公开模型名</th>
              <th>上游模型名</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={i}>
                <td>
                  <input
                    aria-label={`公开模型 ${i + 1}`}
                    value={r.public}
                    onChange={(e) =>
                      save(
                        rows.map((v, n) =>
                          n === i ? { ...v, public: e.target.value } : v,
                        ),
                      )
                    }
                  />
                </td>
                <td>
                  <input
                    aria-label={`上游模型 ${i + 1}`}
                    value={r.upstream}
                    onChange={(e) =>
                      save(
                        rows.map((v, n) =>
                          n === i ? { ...v, upstream: e.target.value } : v,
                        ),
                      )
                    }
                  />
                </td>
                <td>
                  <button
                    className="settings-icon-button is-destructive"
                    title="删除模型"
                    aria-label={`删除模型 ${i + 1}`}
                    onClick={() => save(rows.filter((_, n) => n !== i))}
                  >
                    <Trash2 size={14} />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="settings-help">
        左侧是调用时使用的模型名，右侧是上游模型名。
      </p>
    </div>
  );
}
export function ChannelSettings({ row }: { row: Channel }) {
  const [open, setOpen] = useState(false);
  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger asChild>
        <button className="button channel-settings-trigger">
          <Settings2 size={16} />
          渠道设置
        </button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay channel-settings-overlay" />
        <Dialog.Content className="channel-settings-dialog">
          <header className="settings-dialog-header">
            <div className="settings-title-icon">
              <SlidersHorizontal size={21} />
            </div>
            <div className="settings-title-copy">
              <Dialog.Title>渠道设置</Dialog.Title>
              <Dialog.Description title={row.provider}>
                <strong>{row.provider_name || row.provider}</strong>
                <span>·</span>
                {row.source_name || row.source_id}
              </Dialog.Description>
            </div>
            <Dialog.Close
              className="settings-icon-button settings-close"
              aria-label="关闭渠道设置"
            >
              <X size={20} />
            </Dialog.Close>
          </header>
          {open && <Editor row={row} onClose={() => setOpen(false)} />}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
function Editor({ row, onClose }: { row: Channel; onClose: () => void }) {
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
  const [tab, setTab] = useState("基本与模型");
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
    const body = document.querySelector(
      ".channel-settings-dialog .settings-body",
    );
    if (body) body.scrollTop = 0;
  }, [tab]);
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
    enabled: tab === "模板与批量",
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
    setResetOverride(false);
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
      ].map((name) => client.invalidateQueries({ queryKey: [name] })),
    ]);
  }
  function readDraft() {
    if (tab === "高级配置") {
      const value = format === "json" ? JSON.parse(raw) : parse(raw);
      if (!value || typeof value !== "object" || Array.isArray(value))
        throw Error("高级配置必须是单渠道对象");
      if (value.provider !== view?.provider) throw Error("渠道标识不能更改");
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
      document.querySelector('.channel-settings-dialog [aria-invalid="true"]')
    ) {
      setError("请先修正无效的 JSON 字段");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const changes: Change[] = [
        reset
          ? { provider: row.provider, set: {}, remove: [], reset: true }
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
      };
      if (!apply) {
        const result = await controlRequest<Result>(path + "/validate", {
          method: "POST",
          body: JSON.stringify(body),
        });
        setPreview(result);
        setOperation(id);
        setNotice("校验通过。请核对下方影响范围和差异。");
      } else {
        setOperation(id);
        setPreview(undefined);
        const result = await controlRequest<Result>(path, {
          method: "PATCH",
          body: JSON.stringify(body),
        });
        setOperation(id);
        setPreview(result);
        setNotice(
          result.status === "applied"
            ? "设置已保存并应用"
            : result.message || "应用结果待确认，请核对操作状态",
        );
        if (result.status === "applied") {
          setOperation("");
          await refreshed();
        }
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "操作失败");
    } finally {
      setBusy(false);
    }
  }
  async function reconcile(id = operation) {
    setBusy(true);
    try {
      const result = await controlRequest<Result>(
        path + "/operations/" + encodeURIComponent(id),
      );
      setNotice(
        result.status === "applied"
          ? "已确认保存并应用"
          : result.status === "rejected"
            ? "修改被拒绝，请刷新设置后重试"
            : "应用待确认，保留原恢复版本",
      );
      if (result.status === "applied") {
        setOperation("");
        await refreshed();
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
    try {
      const result = await controlRequest<Result>(path + "/rollback", {
        method: "POST",
        body: JSON.stringify({
          rollback_id: id,
          revision: view.revision,
          operation_id: crypto.randomUUID(),
        }),
      });
      setNotice(
        result.status === "applied"
          ? "已回滚并保存"
          : result.message || "回滚待确认",
      );
      if (result.status === "applied") await refreshed();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  if (query.isPending)
    return (
      <div className="settings-loading">
        <Spinner />
        正在读取渠道设置…
      </div>
    );
  if (query.isError || !view || !draft)
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
  const groups = [
    ...new Set(view.schema.fields.map((f) => f.group)),
    "高级配置",
    "模板与批量",
    "变更记录",
  ];
  const dirty =
    !equal(draft, view.effective) ||
    resetOverride ||
    !!copyToKey ||
    batch.length > 0 ||
    (tab === "高级配置" &&
      raw !==
        (format === "json"
          ? JSON.stringify(draft, null, 2)
          : stringify(draft)));
  const ownerKey = view.available_keys?.find(
    (k) => k.key_id === view.api_key_id,
  );
  return (
    <>
      <div className="settings-workspace">
        <aside className="settings-sidebar">
          <nav className="settings-tabs" aria-label="渠道设置类别">
            {groups.map((group) => {
              const Icon = categories[group]?.icon || Settings2;
              return (
                <button
                  className={tab === group ? "active" : ""}
                  aria-pressed={tab === group}
                  key={group}
                  onClick={() => {
                    if (
                      document.querySelector(
                        '.channel-settings-dialog [aria-invalid="true"]',
                      )
                    ) {
                      setError("请先修正无效的 JSON 字段");
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
                    setTab(group);
                  }}
                >
                  <Icon size={16} />
                  <span>{group}</span>
                </button>
              );
            })}
          </nav>
          <div
            className="settings-scope"
            title={`渠道标识：${view.provider}\n配置版本：${view.revision}`}
          >
            <ShieldCheck size={17} />
            <strong>
              {view.kind === "imported"
                ? "此 API key 专用渠道"
                : "来源共享渠道"}
            </strong>
            <span>
              {view.api_key_id
                ? `仅作用于 ${ownerKey ? `Key ${ownerKey.position}` : "绑定的 API key"}`
                : `影响 ${view.affected_keys.length} 个调用 API key`}
            </span>
          </div>
        </aside>
        <div className="settings-body">
          <div className="settings-section-heading">
            <h3>{tab}</h3>
            <p>{categories[tab]?.description}</p>
          </div>
          {view.conflict && (
            <p role="alert" className="settings-feedback is-error">
              基础配置与覆盖冲突，正在沿用上次有效设置：{view.conflict}
            </p>
          )}

          {view.schema.fields
            .filter((f) => f.group === tab)
            .map((f) => {
              const value = get(draft, f.path),
                label = labels[keyOf(f.path)] || keyOf(f.path);
              return (
                <section
                  className={`settings-field ${["keys", "models", "json"].includes(f.type) || (value && typeof value === "object") ? "settings-field-wide" : ""}`}
                  key={f.path}
                >
                  <header className="settings-field-heading">
                    <div>
                      <h4 title={f.path}>{label}</h4>
                      <small>
                        {value === undefined
                          ? "使用默认值"
                          : view.override_paths.some(
                                (p) =>
                                  p === f.path || p.startsWith(f.path + "/"),
                              )
                            ? "已自定义"
                            : "基础配置"}
                      </small>
                    </div>
                    {!equal(value, get(view.base, f.path)) && (
                      <button
                        className="settings-icon-button settings-reset-field"
                        title="恢复基础值"
                        aria-label={`${label}：恢复基础值`}
                        onClick={() =>
                          edit(set(draft, f.path, get(view.base, f.path)))
                        }
                      >
                        <RotateCcw size={15} />
                      </button>
                    )}
                  </header>
                  <div className="settings-field-control">
                    {f.type === "keys" ? (
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
                    ) : f.type === "models" ? (
                      <Models
                        value={value}
                        onChange={(v) => edit(set(draft, f.path, v))}
                        discover={
                          <button
                            className="button small ghost"
                            disabled={busy}
                            onClick={async () => {
                              setBusy(true);
                              try {
                                const result = await controlRequest<{
                                  models: string[];
                                }>(path + "/discover", {
                                  method: "POST",
                                  body: JSON.stringify({
                                    revision: view.revision,
                                    operation_id: crypto.randomUUID(),
                                    changes: [settingsDiff(view, draft)],
                                  }),
                                });
                                edit(set(draft, "/model", result.models));
                                setNotice("已读取上游模型列表，保存后生效");
                              } catch (e) {
                                setError(String(e));
                              } finally {
                                setBusy(false);
                              }
                            }}
                          >
                            <Download size={15} />
                            从上游读取模型
                          </button>
                        }
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
                          <option value="">继承 / 未设置</option>
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
                    ) : (
                      <label>
                        <span className="sr-only">{label}</span>
                        <input
                          aria-label={label}
                          type={
                            f.type === "number"
                              ? "number"
                              : f.type === "secret"
                                ? "password"
                                : "text"
                          }
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
              <p className="muted">
                密钥引用保持原值；填写新值可替换。只有支持字段的修改会应用，其他字段原样保留。
              </p>
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
                    next = set(next, p, v);
                  for (const p of t.patch.remove || [])
                    next = set(next, p, undefined);
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
                    setNotice(
                      "模板已保存（模型、超时和规则）；凭据和自定义请求体不进入模板",
                    );
                  } catch (e) {
                    setError(String(e));
                  }
                }}
              >
                将当前差异存为模板
              </button>
              <h4>同时应用到本来源其他渠道</h4>
              <p className="muted">
                只应用当前修改字段。每个来源单独原子提交，其他来源不受影响。
              </p>
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
                <Spinner />
              ) : audit.isError ? (
                <p role="alert">审计读取失败</p>
              ) : (
                audit.data?.data.map((a) => (
                  <section className="settings-audit" key={a.id}>
                    <strong>
                      {new Date(a.created_at * 1000).toLocaleString()} ·{" "}
                      {a.status}
                    </strong>
                    <details>
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
          {tab !== "变更记录" && (
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
            <section className="settings-preview">
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
      </div>
      {error && (
        <p role="alert" className="settings-feedback is-error">
          {error}
        </p>
      )}
      {notice && (
        <p role="status" className="settings-feedback">
          <CircleCheck size={16} />
          {notice}
        </p>
      )}
      <footer className="settings-footer">
        <div className="settings-footer-secondary">
          <button
            className="button ghost"
            disabled={busy}
            onClick={() => {
              edit(structuredClone(view.base));
              setResetOverride(true);
              setOperation("");
              void run(false, true);
            }}
          >
            <RotateCcw size={15} />
            恢复基础配置
          </button>
          <span className="settings-draft-state">
            {dirty ? "有未保存的修改" : "已与来源同步"}
          </span>
        </div>
        <div className="settings-footer-primary">
          <button
            className="button ghost"
            disabled={busy}
            onClick={() => {
              setDraft(structuredClone(view.effective));
              setPreview(undefined);
              setOperation("");
              onClose();
            }}
          >
            {dirty ? "放弃修改" : "关闭"}
          </button>
          <button
            className="button"
            disabled={busy}
            onClick={() => void run(false)}
          >
            校验与预览
          </button>
          <button
            className="button primary"
            disabled={busy || !preview || preview.status !== "validated"}
            onClick={() => void run(true)}
          >
            {busy ? <Spinner small /> : <CircleCheck size={16} />}保存并应用
          </button>
          {operation && preview?.status !== "validated" && (
            <button
              className="button"
              disabled={busy}
              onClick={() => void reconcile()}
            >
              核对操作状态
            </button>
          )}
        </div>
      </footer>
    </>
  );
}

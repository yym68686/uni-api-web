import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Plus, X, SlidersHorizontal, Braces } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { parse, stringify } from "yaml";
import { ApiError, controlRequest } from "./api";
import type { ConsoleSource } from "./SourceSettings";
import type { KeyInfo } from "./types";

type Document = Record<string, unknown>;
interface Schema {
  create_provider?: boolean;
  engines: string[];
  fields: { path: string; type: string }[];
}
interface Result {
  status: string;
  operation_id: string;
  message?: string;
}
const engineNames: Record<string, string> = {
  gpt: "OpenAI / 兼容接口",
  codex: "Codex",
  claude: "Claude",
  gemini: "Gemini",
  typesafe: "TypeSafe / Jev",
  aws: "AWS Bedrock",
  vertex: "Vertex AI",
  "vertex-gemini": "Vertex Gemini",
  "vertex-claude": "Vertex Claude",
  azure: "Azure OpenAI",
};
const initialDocument = (): Document => ({
  provider: "",
  engine: "gpt",
  base_url: "",
  model: [],
});
const platformFields: Record<string, string> = {
  project_id: "项目 ID",
  region: "区域",
  client_email: "服务账号邮箱",
  private_key: "服务账号私钥",
  aws_access_key: "AWS access key",
  aws_secret_key: "AWS secret key",
  aws_session_token: "AWS session token",
  cf_account_id: "Cloudflare 账号 ID",
};
const isObject = (value: unknown): value is Document =>
  !!value && typeof value === "object" && !Array.isArray(value);

// Reject unknown fields rather than silently dropping operator configuration.
export function creationSettings(document: Document, schema: Schema): Document {
  if (
    typeof document.provider !== "string" ||
    !document.provider ||
    new TextEncoder().encode(document.provider).length > 100 ||
    !/^[\p{L}\p{N}_.-]+$/u.test(document.provider)
  )
    throw Error(
      "渠道名称需为 1–100 字节，可使用文字、数字、点、下划线或连字符。",
    );
  if (!schema.engines.includes(String(document.engine)))
    throw Error("请选择当前来源支持的引擎。");
  let url: URL;
  try {
    url = new URL(String(document.base_url));
  } catch {
    throw Error("请输入完整的上游 HTTP(S) 地址。");
  }
  if (
    !["http:", "https:"].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.hash
  )
    throw Error("上游地址需为 HTTP(S)，不能包含用户名、密码或片段。");
  if (!Array.isArray(document.model) || !document.model.length)
    throw Error("请至少添加一个模型。");
  const settings: Document = {};
  const visit = (value: unknown, path: string) => {
    if (schema.fields.some((f) => f.path === path)) {
      settings[path] = value;
      return;
    }
    if (
      isObject(value) &&
      Object.keys(value).length &&
      schema.fields.some((f) => f.path.startsWith(path + "/"))
    ) {
      for (const [key, child] of Object.entries(value))
        visit(child, `${path}/${key.replace(/~/g, "~0").replace(/\//g, "~1")}`);
      return;
    }
    if (
      path === "/preferences" &&
      isObject(value) &&
      !Object.keys(value).length
    )
      return;
    throw Error(`当前来源不支持配置字段 ${path}`);
  };
  for (const [key, value] of Object.entries(document))
    if (key !== "provider") visit(value, "/" + key);
  return settings;
}

export function CreateChannel({
  sources,
  keys,
  sourceId,
  keyId,
}: {
  sources: ConsoleSource[];
  keys: KeyInfo[];
  sourceId: string;
  keyId: string;
}) {
  const cache = useQueryClient();
  const [open, setOpen] = useState(false);
  const [source, setSource] = useState("");
  const [key, setKey] = useState("");
  const [draft, setDraft] = useState<Document>(initialDocument);
  const [modelText, setModelText] = useState("");
  const [keyText, setKeyText] = useState("");
  const [advanced, setAdvanced] = useState(false);
  const [raw, setRaw] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [pending, setPending] = useState<{ source: string; id: string } | null>(
    null,
  );
  const path = `/v1/sources/${encodeURIComponent(source)}/channel-settings`;
  const schema = useQuery({
    queryKey: ["create-channel-schema", source],
    queryFn: ({ signal }) =>
      controlRequest<Schema>(`${path}/schema`, { signal }),
    enabled: open && !!source,
    staleTime: 0,
    retry: false,
  });
  const available = keys.filter(
    (k) => k.source_id === source || (!k.source_id && sources.length === 1),
  );
  const edit = (field: string, value: unknown) => {
    setDraft((old) => ({ ...old, [field]: value }));
    setMessage("");
  };
  function basicDocument() {
    const models = modelText
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean)
      .map((line) => {
        const index = line.indexOf("=");
        if (index < 0) return line;
        const alias = line.slice(0, index).trim(),
          upstream = line.slice(index + 1).trim();
        if (!alias || !upstream)
          throw Error("模型映射请填写：公开模型名 = 上游模型名。");
        return { [upstream]: alias };
      });
    const secrets = keyText
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean);
    const next: Document = { ...draft, model: models };
    if (secrets.length) next.api = secrets.length === 1 ? secrets[0] : secrets;
    else delete next.api;
    return next;
  }
  function readDocument() {
    if (!advanced) return basicDocument();
    const result: unknown = parse(raw);
    if (!isObject(result)) throw Error("请输入单个渠道的 JSON 或 YAML 对象。");
    return result;
  }
  function switchMode(next: boolean) {
    try {
      const doc = readDocument();
      if (next) setRaw(stringify(doc));
      else {
        if (!schema.data) return;
        creationSettings(doc, schema.data);
        setDraft(doc);
        setModelText(
          (doc.model as unknown[])
            .flatMap((m) =>
              typeof m === "string"
                ? [m]
                : isObject(m)
                  ? Object.entries(m).map(([up, alias]) => `${alias} = ${up}`)
                  : [],
            )
            .join("\n"),
        );
        setKeyText(
          Array.isArray(doc.api)
            ? doc.api.join("\n")
            : typeof doc.api === "string"
              ? doc.api
              : "",
        );
        setRaw("");
      }
      setAdvanced(next);
      setMessage("");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "配置格式无效");
    }
  }
  function clearDraft() {
    setDraft(initialDocument());
    setRaw("");
    setKeyText("");
    setModelText("");
    setAdvanced(false);
  }
  function finish(result: Result) {
    if (result.status === "applied") {
      setPending(null);
      clearDraft();
      setOpen(false);
      setMessage("");
      for (const name of [
        "catalog",
        "control-catalog",
        "keys",
        "channel-controls",
        "metrics",
        "imported-channels",
        "channel-sites",
        "channel-info",
        "channel-settings-audit",
      ])
        void cache.invalidateQueries({ queryKey: [name] });
    } else if (result.status === "rejected") {
      setPending(null);
      setMessage(result.message || "配置未应用，请核对后重试。");
    } else
      setMessage(result.message || "保存结果待确认，请点击“核对保存结果”。");
  }
  async function save() {
    if (busy) return;
    setBusy(true);
    setMessage("");
    let dispatched = false;
    try {
      if (pending) {
        finish(
          await controlRequest<Result>(
            `/v1/sources/${encodeURIComponent(pending.source)}/channel-settings/operations/${encodeURIComponent(pending.id)}`,
          ),
        );
        return;
      }
      if (!available.some((k) => k.key_id === key))
        throw Error("请选择当前来源下的 API key。");
      // Recheck capabilities at submission in case the gateway changed while editing.
      const current = await controlRequest<Schema>(`${path}/schema`);
      if (!current.create_provider)
        throw Error("该来源尚不支持通用渠道创建，请先更新 uni-api。");
      const doc = readDocument(),
        settings = creationSettings(doc, current);
      const controls = await controlRequest<{ revision: string }>(
        `/v1/sources/${encodeURIComponent(source)}/channel-controls`,
      );
      const mutation = {
        revision: controls.revision,
        operation_id: crypto.randomUUID(),
        changes: [
          {
            provider: doc.provider,
            create_to_key: key.includes("::")
              ? key.split("::").slice(1).join("::")
              : key,
            set: settings,
            remove: [],
          },
        ],
      };
      await controlRequest(`${path}/validate`, {
        method: "POST",
        body: JSON.stringify(mutation),
      });
      setPending({ source, id: mutation.operation_id });
      dispatched = true;
      finish(
        await controlRequest<Result>(path, {
          method: "PATCH",
          body: JSON.stringify(mutation),
        }),
      );
    } catch (error) {
      // Never discard an uncertain operation just because a reconciliation read failed.
      if (
        !pending &&
        dispatched &&
        error instanceof ApiError &&
        [400, 401, 403, 404, 409].includes(error.status)
      )
        setPending(null);
      setMessage(error instanceof Error ? error.message : "添加失败");
    } finally {
      setBusy(false);
    }
  }
  function changeOpen(next: boolean) {
    if (busy) return;
    setOpen(next);
    if (!next) {
      clearDraft();
      return;
    }
    if (!pending) {
      const initial = sourceId || keyId.split("::")[0];
      setSource(
        sources.some((s) => s.id === initial) ? initial : sources[0]?.id || "",
      );
      setKey(keyId);
      setMessage("");
    }
  }
  const engine = String(draft.engine);
  const cloudFields =
    engine === "aws"
      ? ["region", "aws_access_key", "aws_secret_key", "aws_session_token"]
      : engine.startsWith("vertex")
        ? ["project_id", "region", "client_email", "private_key"]
        : engine === "cloudflare"
          ? ["cf_account_id"]
          : [];
  const supported = schema.data?.create_provider;
  return (
    <Dialog.Root open={open} onOpenChange={changeOpen}>
      <Dialog.Trigger asChild>
        <button className="button small ghost">
          <Plus size={15} />
          添加渠道
        </button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay channel-settings-overlay" />
        <Dialog.Content className="create-channel-dialog">
          <header className="settings-dialog-header">
            <span className="settings-title-icon">
              <Plus size={23} />
            </span>
            <div className="settings-title-copy">
              <Dialog.Title>添加渠道</Dialog.Title>
              <Dialog.Description>
                连接上游服务，渠道仅供所选调用 API key 使用。
              </Dialog.Description>
            </div>
            <Dialog.Close asChild>
              <button
                className="settings-icon-button settings-close"
                aria-label="关闭"
                disabled={busy}
              >
                <X size={20} />
              </button>
            </Dialog.Close>
          </header>
          <div className="create-channel-body">
            <fieldset disabled={busy || !!pending}>
              <div className="create-channel-grid">
                <label>
                  uni-api 来源
                  <select
                    value={source}
                    onChange={(e) => {
                      setSource(e.target.value);
                      setKey("");
                      setMessage("");
                    }}
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
                  调用 API key
                  <select value={key} onChange={(e) => setKey(e.target.value)}>
                    <option value="">选择 API key</option>
                    {available.map((k) => (
                      <option key={k.key_id} value={k.key_id}>
                        #{k.position} · {k.prefix}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              {schema.isFetching && (
                <p className="settings-help">正在读取来源支持的渠道类型…</p>
              )}
              {schema.isError ? (
                <p role="alert" className="negative">
                  {schema.error.message}
                </p>
              ) : (
                schema.data &&
                !supported && (
                  <p role="alert" className="negative">
                    该来源尚不支持通用渠道创建，请先更新 uni-api。
                  </p>
                )
              )}
              {schema.isError && (
                <button
                  className="button small"
                  onClick={() => void schema.refetch()}
                >
                  重新读取
                </button>
              )}
              <div className="create-channel-mode" aria-label="渠道填写方式">
                <button
                  aria-pressed={!advanced}
                  onClick={() => switchMode(false)}
                >
                  <SlidersHorizontal size={15} />
                  基本配置
                </button>
                <button
                  aria-pressed={advanced}
                  onClick={() => switchMode(true)}
                >
                  <Braces size={15} />
                  高级配置
                </button>
              </div>
              {advanced ? (
                <label>
                  渠道配置 · JSON / YAML
                  <textarea
                    aria-label="渠道配置 · JSON / YAML"
                    className="create-channel-raw"
                    spellCheck={false}
                    value={raw}
                    onChange={(e) => setRaw(e.target.value)}
                  />
                  <small>
                    填写一个 provider
                    对象，可设置密钥、模型映射、请求改写及其他来源支持的字段。
                  </small>
                </label>
              ) : (
                <>
                  <div className="create-channel-grid">
                    <label>
                      渠道名称
                      <input
                        value={String(draft.provider)}
                        placeholder="例如 my-channel"
                        onChange={(e) => edit("provider", e.target.value)}
                      />
                    </label>
                    <label>
                      渠道引擎
                      <select
                        value={engine}
                        onChange={(e) => edit("engine", e.target.value)}
                        disabled={!supported}
                      >
                        {!schema.data?.engines?.includes(engine) && (
                          <option value={engine}>
                            {engineNames[engine] || engine}
                          </option>
                        )}
                        {schema.data?.engines?.map((value) => (
                          <option key={value} value={value}>
                            {engineNames[value] || value}
                          </option>
                        ))}
                      </select>
                    </label>
                  </div>
                  <label>
                    上游地址
                    <input
                      value={String(draft.base_url)}
                      placeholder="https://api.example.com/v1/responses"
                      onChange={(e) => edit("base_url", e.target.value)}
                      spellCheck={false}
                    />
                  </label>
                  <label>
                    上游 API key
                    <textarea
                      value={keyText}
                      onChange={(e) => setKeyText(e.target.value)}
                      autoComplete="off"
                      spellCheck={false}
                      rows={2}
                      placeholder="每行一个密钥；AWS / Vertex 可使用下方平台凭据"
                    />
                  </label>
                  {!!cloudFields.length && (
                    <div className="create-channel-grid">
                      {cloudFields.map((field) => (
                        <label key={field}>
                          {platformFields[field]}
                          {field === "private_key" ? (
                            <textarea
                              rows={4}
                              value={String(draft[field] || "")}
                              onChange={(e) => edit(field, e.target.value)}
                              spellCheck={false}
                              autoComplete="off"
                            />
                          ) : (
                            <input
                              value={String(draft[field] || "")}
                              onChange={(e) => edit(field, e.target.value)}
                              type={
                                schema.data?.fields.find(
                                  (f) => f.path === "/" + field,
                                )?.type === "secret"
                                  ? "password"
                                  : "text"
                              }
                              autoComplete="off"
                            />
                          )}
                        </label>
                      ))}
                    </div>
                  )}
                  <label>
                    模型与映射
                    <textarea
                      aria-label="模型与映射"
                      value={modelText}
                      onChange={(e) => setModelText(e.target.value)}
                      spellCheck={false}
                      rows={4}
                      placeholder={"每行一个模型\n公开模型名 = 上游模型名"}
                    />
                    <small>
                      名称相同时只填模型名；映射时左侧为调用名称，右侧为上游模型名称。
                    </small>
                  </label>
                </>
              )}
            </fieldset>
          </div>
          {message && (
            <p role="status" className="settings-feedback is-error">
              {message}
            </p>
          )}
          <footer className="settings-footer">
            <span className="settings-help">配置校验不会发送模型请求。</span>
            <div className="settings-footer-primary">
              <Dialog.Close asChild>
                <button className="button ghost" disabled={busy}>
                  取消
                </button>
              </Dialog.Close>
              <button
                className="button primary"
                disabled={
                  busy ||
                  (!pending &&
                    (!source || !key || !supported || schema.isFetching))
                }
                onClick={() => void save()}
              >
                {busy ? "处理中…" : pending ? "核对保存结果" : "校验并添加"}
              </button>
            </div>
          </footer>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

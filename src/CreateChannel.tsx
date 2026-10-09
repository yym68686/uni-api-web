import { useEffect, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Plus, X, SlidersHorizontal, Braces, Download } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { parse, stringify } from "yaml";
import { ApiError, controlRequest } from "./api";
import type { ConsoleSource } from "./SourceSettings";
import type { KeyInfo } from "./types";

import { CreateChannelModels } from "./CreateChannelModels";
import {
  readCreationModels,
  writeCreationModels,
} from "./creationModelMapping";
import type { ModelAlias } from "./ModelAliases";
import { Spinner } from "./ui";
import { automaticChannelDocument } from "./channelProtocol";

type Document = Record<string, unknown>;
interface Schema {
  create_provider?: boolean;
  automatic_engine?: boolean;
  engines: string[];
  fields: { path: string; type: string }[];
}
interface Result {
  status: string;
  operation_id: string;
  message?: string;
}
const engineNames: Record<string, string> = {
  auto: "自动识别（由 uni-api 按上游地址判断）",
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
  engine: "auto",
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
  if (document.engine === undefined && !schema.automatic_engine)
    throw Error(
      "该来源尚不支持按地址自动识别引擎，请更新 uni-api 或明确选择引擎。",
    );
  if (
    typeof document.provider !== "string" ||
    !document.provider ||
    new TextEncoder().encode(document.provider).length > 100 ||
    !/^[\p{L}\p{N}_.-]+$/u.test(document.provider)
  )
    throw Error(
      "渠道名称需为 1–100 字节，可使用文字、数字、点、下划线或连字符。",
    );
  if (
    document.engine !== undefined &&
    !schema.engines.includes(String(document.engine))
  )
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
  keys = [],
  sourceId = "",
  keyId = "",
}: {
  sources: ConsoleSource[];
  keys?: KeyInfo[];
  sourceId?: string;
  keyId?: string;
}) {
  const cache = useQueryClient();
  const [open, setOpen] = useState(false);
  const [source, setSource] = useState("");
  const [key, setKey] = useState("");
  const [draft, setDraft] = useState<Document>(initialDocument);
  const [knownModels, setKnownModels] = useState<string[]>([]);
  const [selectedModels, setSelectedModels] = useState<string[]>([]);
  const [aliases, setAliases] = useState<ModelAlias[]>([]);
  const [discovering, setDiscovering] = useState(false);
  const [discoveryError, setDiscoveryError] = useState("");
  const [discoveryNote, setDiscoveryNote] = useState("");
  const discovery = useRef<AbortController | null>(null);
  useEffect(() => () => discovery.current?.abort(), []);
  const modelOptions = [
    ...new Set([
      ...knownModels,
      ...selectedModels,
      ...aliases.map((a) => a.upstream),
    ]),
  ];
  function cancelDiscovery() {
    discovery.current?.abort();
    discovery.current = null;
    setDiscovering(false);
    setDiscoveryError("");
    setDiscoveryNote("");
  }
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
  const remoteKeys = useQuery({
    queryKey: ["create-channel-keys", source],
    queryFn: ({ signal }) =>
      controlRequest<{ data: KeyInfo[] }>(
        `/v1/sources/${encodeURIComponent(source)}/proxy/v1/api-keys`,
        { signal },
      ),
    enabled: open && !!source && !keys.length,
    retry: false,
  });
  const available = keys.length
    ? keys.filter(
        (k) => k.source_id === source || (!k.source_id && sources.length === 1),
      )
    : remoteKeys.data?.data || [];
  const edit = (field: string, value: unknown) => {
    if (field === "base_url" || field === "engine") cancelDiscovery();
    setDraft((old) => ({ ...old, [field]: value }));
    setMessage("");
  };
  function basicDocument() {
    const models = writeCreationModels(selectedModels, aliases);
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
    cancelDiscovery();
    try {
      const doc = readDocument();
      if (next) setRaw(stringify(doc));
      else {
        if (!schema.data) return;
        creationSettings(automaticChannelDocument(doc), schema.data);
        setDraft(doc);
        const models = readCreationModels(doc.model);
        setSelectedModels(models.originals);
        setAliases(models.aliases);
        setKnownModels((old) => [
          ...new Set([
            ...old,
            ...models.originals,
            ...models.aliases.map((a) => a.upstream),
          ]),
        ]);
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
    setKnownModels([]);
    setSelectedModels([]);
    setAliases([]);
    cancelDiscovery();
    setAdvanced(false);
  }
  async function fetchModels() {
    if (discovery.current || !source || !String(draft.base_url).trim()) return;
    const abort = new AbortController();
    discovery.current = abort;
    setDiscovering(true);
    setDiscoveryError("");
    setDiscoveryNote("");
    try {
      const result = await controlRequest<{
        models: string[];
        endpoint: string;
      }>(`${path}/discover-draft`, {
        method: "POST",
        signal: AbortSignal.any([abort.signal, AbortSignal.timeout(30000)]),
        body: JSON.stringify({
          base_url: String(draft.base_url).trim(),
          api:
            keyText
              .split("\n")
              .map((k) => k.trim())
              .find(Boolean) || "",
          engine: draft.engine === "auto" ? undefined : String(draft.engine),
        }),
      });
      if (abort.signal.aborted) return;
      if (
        !Array.isArray(result.models) ||
        result.models.some((m) => typeof m !== "string" || !m.trim())
      )
        throw Error("上游模型列表格式无效");
      const found = [...new Set(result.models)];
      setKnownModels([
        ...new Set([
          ...found,
          ...selectedModels,
          ...aliases.map((a) => a.upstream),
        ]),
      ]);
      // Refreshing the catalog must preserve deliberate deselections and aliases.
      if (!modelOptions.length && !selectedModels.length && !aliases.length)
        setSelectedModels(found);
      setDiscoveryNote(
        found.length
          ? `已获取 ${found.length} 个模型`
          : "上游返回空模型列表，可手动添加",
      );
    } catch (error) {
      if (!abort.signal.aborted)
        setDiscoveryError(
          error instanceof ApiError && error.status === 404
            ? "当前服务暂未提供模型获取功能，请稍后刷新重试"
            : error instanceof Error
              ? error.message
              : "获取模型失败",
        );
    } finally {
      if (discovery.current === abort) {
        discovery.current = null;
        setDiscovering(false);
      }
    }
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
        "channel-management",
        "channel-routes",
        "sub2api-imports",
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
    if (busy || discovering) return;
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
      const documents = [automaticChannelDocument(readDocument())];
      const changes = documents.map((doc) => ({
        provider: doc.provider,
        create_to_key: key.includes("::")
          ? key.split("::").slice(1).join("::")
          : key,
        set: creationSettings(doc, current),
        remove: [],
      }));
      const controls = await controlRequest<{ revision: string }>(
        `/v1/sources/${encodeURIComponent(source)}/channel-controls`,
      );
      const mutation = {
        revision: controls.revision,
        operation_id: crypto.randomUUID(),
        changes,
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
  const engine = String(draft.engine ?? "auto");
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
                      cancelDiscovery();
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
                  <select
                    value={key}
                    disabled={remoteKeys.isFetching}
                    onChange={(e) => setKey(e.target.value)}
                  >
                    <option value="">选择 API key</option>
                    {available.map((k) => (
                      <option key={k.key_id} value={k.key_id}>
                        #{k.position} · {k.prefix}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              {remoteKeys.error && (
                <p role="alert" className="negative">
                  {remoteKeys.error.message}
                  <button
                    type="button"
                    className="button small"
                    onClick={() => void remoteKeys.refetch()}
                  >
                    重新读取 API key
                  </button>
                </p>
              )}
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
                        <option value="auto">{engineNames.auto}</option>
                        {engine !== "auto" &&
                          !schema.data?.engines?.includes(engine) && (
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
                  {engine === "auto" && (
                    <p role="note">
                      不指定引擎，保留完整上游地址，由 uni-api
                      统一识别。模型名称不参与判断；同一地址下的所有模型共用网关识别的引擎。
                    </p>
                  )}
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
                      onChange={(e) => {
                        cancelDiscovery();
                        setKeyText(e.target.value);
                      }}
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
                  <CreateChannelModels
                    models={modelOptions}
                    selected={selectedModels}
                    aliases={aliases}
                    disabled={busy || discovering || !!pending}
                    onSelect={setSelectedModels}
                    onAliases={setAliases}
                    onAdd={(model) => {
                      setKnownModels((v) => [...new Set([...v, model])]);
                      setSelectedModels((v) => [...new Set([...v, model])]);
                    }}
                    discovery={
                      <button
                        type="button"
                        className="button small"
                        disabled={
                          discovering ||
                          !source ||
                          !String(draft.base_url).trim()
                        }
                        onClick={() => void fetchModels()}
                      >
                        {discovering ? (
                          <Spinner small />
                        ) : (
                          <Download size={14} />
                        )}{" "}
                        {discovering ? "获取中…" : "获取模型"}
                      </button>
                    }
                  />
                  {discoveryError && (
                    <p role="alert" className="create-models-feedback negative">
                      {discoveryError}
                    </p>
                  )}
                  {discoveryNote && (
                    <p role="status" className="create-models-feedback muted">
                      {discoveryNote}
                    </p>
                  )}
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
                  discovering ||
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

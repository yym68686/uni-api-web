import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowDown,
  ArrowUp,
  Check,
  RotateCcw,
  RefreshCw,
  SlidersHorizontal,
} from "lucide-react";
import { controlRequest, request, channelParams } from "./api";
import type { Catalog, Connection, KeyInfo } from "./types";
import type { ConsoleSource } from "./SourceSettings";
import { Spinner } from "./ui";

export interface ControlRule {
  api_key_id: string;
  model: string;
  order: string[];
  disabled: string[];
}
export interface ControlState {
  revision: string;
  instance_id: string;
  config_revision: string;
  rules: ControlRule[];
  reset_on_restart: boolean;
}
export function orderedProviders(providers: string[], order: string[]) {
  return [
    ...order.filter((p) => providers.includes(p)),
    ...providers.filter((p) => !order.includes(p)),
  ];
}
export function inheritedDisabled(
  rules: ControlRule[],
  key: string,
  model: string,
  provider: string,
) {
  return rules.some(
    (rule) =>
      (rule.api_key_id === "" || rule.api_key_id === key) &&
      (rule.model === "" || rule.model === model) &&
      !(rule.api_key_id === key && rule.model === model) &&
      rule.disabled.includes(provider),
  );
}
export function ChannelControls({
  connection,
  sources,
  initialKey,
  initialModel,
  onApplied,
}: {
  connection: Connection;
  sources: ConsoleSource[];
  initialKey: string;
  initialModel: string;
  onApplied: () => void;
}) {
  const [sourceId, setSourceId] = useState(connection.sourceId || "");
  const [key, setKey] = useState(initialKey),
    [model, setModel] = useState(initialModel);
  const [editorVersion, setEditorVersion] = useState(0);
  const client = useQueryClient();
  const source = sources.find((s) => s.id === sourceId);
  const conn = { ...connection, sourceId };
  const keys = useQuery({
    queryKey: ["control-keys", connection.session, sourceId],
    enabled: !!source,
    queryFn: ({ signal }) =>
      request<{ data: KeyInfo[] }>(conn, "/v1/api-keys", signal),
  });
  const catalog = useQuery({
    queryKey: ["control-catalog", connection.session, sourceId, key],
    enabled: !!source && !!keys.data,
    queryFn: ({ signal }) =>
      request<Catalog>(
        conn,
        "/v1/model-channels?" + channelParams(key, "15m", "", "all", "all"),
        signal,
      ),
  });
  const stateKey = ["channel-controls", connection.session, sourceId];
  const state = useQuery({
    queryKey: stateKey,
    enabled: !!source,
    queryFn: ({ signal }) =>
      controlRequest<ControlState>(
        `/v1/sources/${encodeURIComponent(sourceId)}/channel-controls`,
        { signal },
      ),
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: 5000,
  });
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [notice, setNotice] = useState("");
  const rawKey = key.includes("::") ? key.split("::")[1] : key;
  const models = [...new Set(catalog.data?.data.map((row) => row.model) || [])];
  const providers = [
    ...new Set(
      (catalog.data?.data || [])
        .filter((row) => !model || row.model === model)
        .map((row) => row.provider),
    ),
  ];
  async function apply(action: string, rule: ControlRule, revision: string) {
    if (busy || !source) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const result = await controlRequest<ControlState>(
        `/v1/sources/${encodeURIComponent(sourceId)}/channel-controls`,
        { method: "POST", body: JSON.stringify({ ...rule, action, revision }) },
      );
      client.setQueryData(stateKey, result);
      setEditorVersion((v) => v + 1);
      onApplied();
      await catalog.refetch();
      setNotice(
        action === "set"
          ? "临时修改已生效，持续至手动恢复或 uni-api 重启。"
          : "已移除此范围的临时规则，继续遵循其余规则和配置。",
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "修改失败，请刷新核对");
    } finally {
      setBusy(false);
    }
  }
  async function refresh() {
    setError("");
    setNotice("");
    await Promise.all([state.refetch(), catalog.refetch()]);
    setEditorVersion((v) => v + 1);
  }
  return (
    <section className="data-panel control-panel">
      <div className="data-heading">
        <div className="data-title">
          <SlidersHorizontal size={19} />
          <h2>临时路由控制</h2>
        </div>
        <button
          className="button small"
          onClick={() => void refresh()}
          disabled={!source || busy}
        >
          <RefreshCw size={14} />
          载入最新规则
        </button>
      </div>
      <div className="control-intro">
        仅保存在所选 uni-api
        的运行内存中，无到期时间。重启或重新部署后清空，恢复配置文件；只影响此后进入的新请求。来源有多个运行实例时，规则不会自动跨实例同步。
      </div>
      <div className="control-selectors">
        <label>
          uni-api 来源
          <select
            aria-label="控制来源"
            value={sourceId}
            disabled={busy}
            onChange={(e) => {
              setSourceId(e.target.value);
              setKey("");
              setModel("");
              setError("");
              setNotice("");
            }}
          >
            <option value="">请选择一个来源</option>
            {sources.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          API key 范围
          <select
            aria-label="控制 API key"
            value={key}
            disabled={!source || busy}
            onChange={(e) => {
              setKey(e.target.value);
              setModel("");
              setError("");
              setNotice("");
            }}
          >
            <option value="">全部 API key</option>
            {keys.data?.data.map((k) => (
              <option key={k.key_id} value={k.key_id}>
                Key {k.position} · {k.prefix}
              </option>
            ))}
          </select>
        </label>
        <label>
          模型范围
          <select
            aria-label="控制模型"
            value={model}
            disabled={!source || busy}
            onChange={(e) => {
              setModel(e.target.value);
              setError("");
              setNotice("");
            }}
          >
            <option value="">全部模型</option>
            {models.map((m) => (
              <option key={m}>{m}</option>
            ))}
          </select>
        </label>
      </div>
      {notice && (
        <p className="control-notice" role="status">
          <Check size={16} />
          {notice}
        </p>
      )}
      {(error || state.error || catalog.error || keys.error) && (
        <div className="error-banner" role="alert">
          {error ||
            state.error?.message ||
            catalog.error?.message ||
            keys.error?.message}
        </div>
      )}
      {!source ? (
        <p className="control-intro">请选择来源后查看和编辑临时规则。</p>
      ) : state.isLoading || keys.isLoading || catalog.isLoading ? (
        <p className="control-intro">
          <Spinner small />
          正在读取当前规则…
        </p>
      ) : state.data && catalog.data && !catalog.error ? (
        <ControlEditor
          key={`${sourceId}:${key}:${model}:${editorVersion}`}
          state={state.data}
          keyId={rawKey}
          model={model}
          providers={providers}
          busy={busy || state.isError || catalog.isError}
          onApply={apply}
        />
      ) : null}
      {!!state.data?.rules.length && (
        <div className="control-rules">
          <h3>此来源正在生效的临时规则</h3>
          {state.data.rules.map((rule) => (
            <div
              className="control-rule"
              key={JSON.stringify([rule.api_key_id, rule.model])}
            >
              <div>
                <strong>
                  {rule.api_key_id
                    ? keys.data?.data.find((k) =>
                        k.key_id.endsWith("::" + rule.api_key_id),
                      )?.prefix || "指定 API key"
                    : "全部 API key"}{" "}
                  · {rule.model || "全部模型"}
                </strong>
                <small>
                  {rule.order.length
                    ? `自定义 ${rule.order.length} 个渠道的优先顺序`
                    : "沿用配置顺序"}{" "}
                  ·{" "}
                  {rule.disabled.length
                    ? `停用：${rule.disabled.join("、")}`
                    : "无临时停用"}
                </small>
              </div>
              <button
                className="button small"
                disabled={busy}
                onClick={() => void apply("reset", rule, state.data!.revision)}
              >
                <RotateCcw size={13} />
                恢复此规则
              </button>
            </div>
          ))}
          <p className="muted">
            顺序优先级：指定 key＋模型 → 指定 key → 指定模型 →
            全局。停用规则叠加生效，局部规则不能解除全局停用。
          </p>
        </div>
      )}
    </section>
  );
}
function ControlEditor({
  state,
  keyId,
  model,
  providers,
  busy,
  onApply,
}: {
  state: ControlState;
  keyId: string;
  model: string;
  providers: string[];
  busy: boolean;
  onApply: (
    action: string,
    rule: ControlRule,
    revision: string,
  ) => Promise<void>;
}) {
  const existing = state.rules.find(
    (r) => r.api_key_id === keyId && r.model === model,
  );
  const [revision] = useState(state.revision);
  const [order, setOrder] = useState(() =>
    orderedProviders(providers, existing?.order || []),
  );
  const [customOrder, setCustomOrder] = useState(!!existing?.order.length);
  const [disabled, setDisabled] = useState(
    () => new Set(existing?.disabled || []),
  );
  const [dirty, setDirty] = useState(false);
  const stale = revision !== state.revision;
  const current = orderedProviders(providers, order);
  function move(index: number, direction: number) {
    const next = [...current];
    [next[index], next[index + direction]] = [
      next[index + direction],
      next[index],
    ];
    setOrder(next);
    setCustomOrder(true);
    setDirty(true);
  }
  const rule = {
    api_key_id: keyId,
    model,
    order: customOrder ? current : [],
    disabled: [...disabled],
  };
  return (
    <div className="control-editor">
      <div className="control-editor-heading">
        <label>
          <input
            type="checkbox"
            checked={customOrder}
            disabled={busy}
            onChange={(e) => {
              setCustomOrder(e.target.checked);
              setDirty(true);
            }}
          />
          自定义优先顺序
        </label>
        <span className="muted">
          {providers.length} 个渠道 ·{" "}
          {dirty ? "有未应用修改" : "已与当前规则同步"}
        </span>
      </div>
      {stale && (
        <p className="error-banner" role="alert">
          当前规则已更新，请载入最新规则后重新编辑。
        </p>
      )}
      <div className="control-channel-list">
        {current.map((provider, index) => {
          const inherited = inheritedDisabled(
            state.rules,
            keyId,
            model,
            provider,
          );
          return (
            <div
              className={`control-channel ${disabled.has(provider) || inherited ? "disabled" : ""}`}
              key={provider}
            >
              <span className="mono muted">
                {String(index + 1).padStart(2, "0")}
              </span>
              <strong>{provider}</strong>
              <label>
                <input
                  type="checkbox"
                  aria-label={`临时停用 ${provider}`}
                  checked={disabled.has(provider) || inherited}
                  disabled={busy || inherited}
                  onChange={(e) => {
                    const next = new Set(disabled);
                    e.target.checked
                      ? next.add(provider)
                      : next.delete(provider);
                    setDisabled(next);
                    setDirty(true);
                  }}
                />
                {inherited ? "继承停用" : "临时停用"}
              </label>
              <button
                className="icon-button"
                aria-label={`上移 ${provider}`}
                disabled={busy || index === 0}
                onClick={() => move(index, -1)}
              >
                <ArrowUp size={16} />
              </button>
              <button
                className="icon-button"
                aria-label={`下移 ${provider}`}
                disabled={busy || index === current.length - 1}
                onClick={() => move(index, 1)}
              >
                <ArrowDown size={16} />
              </button>
            </div>
          );
        })}
      </div>
      {!providers.length && <p className="control-intro">当前范围没有渠道。</p>}
      {!!providers.length &&
        current.every(
          (p) =>
            disabled.has(p) || inheritedDisabled(state.rules, keyId, model, p),
        ) && (
          <p className="error-banner">
            此范围的全部渠道将停用，新请求会返回无可用渠道。
          </p>
        )}
      <div className="control-editor-footer">
        <span className="muted">
          {customOrder
            ? "按列表从上到下优先请求；不会增加该 key 原本无权使用的渠道。"
            : "使用配置文件原有的调度方式。"}
        </span>
        <button
          className="button primary"
          disabled={busy || stale || !dirty || !providers.length}
          onClick={() => void onApply("set", rule, revision)}
        >
          {busy ? <Spinner small /> : <Check size={15} />}应用临时修改
        </button>
      </div>
    </div>
  );
}

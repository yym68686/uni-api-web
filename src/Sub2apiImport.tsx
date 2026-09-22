import { useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { Plus, Pencil, Trash2, X } from "lucide-react";
import { controlRequest } from "./api";
import { Spinner } from "./ui";
import type { ConsoleSourcesQuery } from "./consoleSources";
import { CompactionStatus } from "./SubCompaction";
import { assessPrice } from "./sub2apiPriceCheck";
import type { KeyInfo, ModelPrice } from "./types";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import type { InstalledChannel, SubImportsQuery } from "./sub2apiImports";
import { boundGroups } from "./sub2apiImports";
import { ChannelRoutes } from "./ChannelRoutes";
import { ChannelSettings } from "./ChannelSettings";
import type { ManagedChannel } from "./channelManagement";
import {
  modelChecks,
  availableModelChecks,
  importModelLabel,
} from "./sub2apiResults";
interface Options {
  provider?: string;
  supported: boolean;
  manageable: boolean;
  revision: string;
  keys: KeyInfo[];
  channels: { provider: string; model: string }[];
}
export function Sub2apiImport({
  account,
  target,
  imports,
  sources,
  prices,
  configured,
  close,
}: {
  account: SubAccount;
  target: SubTarget;
  imports: SubImportsQuery;
  sources: ConsoleSourcesQuery;
  prices?: ModelPrice[];
  configured?: ManagedChannel;
  close: () => void;
}) {
  const client = useQueryClient();
  const checks = useMemo(() => modelChecks(target), [target]);
  const available = availableModelChecks(target).map((check) => check.model);
  const installed = (imports.data?.data || []).filter(
    (i) => i.kind !== "configured" && i.account_id === account.id && i.group_id === target.group_id,
  );
  const configuredChannels = (imports.data?.data || []).filter(i => i.kind === "configured" &&
    boundGroups(i).some(g => g.account_id === account.id && g.group_id === target.group_id));
  if (configured && !configuredChannels.some(i => i.source_id === configured.source_id && i.provider === configured.provider)) configuredChannels.push(configured);
  const configuredSources = [...new Set(configuredChannels.map(i => i.source_id))];
  const [editing, setEditing] = useState<InstalledChannel | null>(null);
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<InstalledChannel | null>(null);
  const [modelChoices, setModelChoices] = useState<Record<string, boolean>>({});
  const models = checks.filter(check => modelChoices[check.model] ?? (available.includes(check.model) && assessPrice(check, prices).status !== "abnormal")).map(check => check.model);
  const [source, setSource] = useState("");
  const [key, setKey] = useState("");
  const [position, setPosition] = useState(1);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [compactionChoice, setCompactionChoice] = useState<boolean | null>(null);
  const compactionEnabled = compactionChoice ?? target.compaction?.status === "supported";
  const options = useQuery({
    queryKey: ["sub-import-options", source, key, account.id, target.group_id],
    queryFn: ({ signal }) =>
      controlRequest<Options>(
        "/v1/sub2api/channel-options?" +
          new URLSearchParams({
            source_id: source,
            api_key_id: key,
            account_id: account.id,
            group_id: String(target.group_id),
          }),
        { signal },
      ),
    enabled: !!source,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const positions = models.length
    ? Math.min(
        ...models.map(
          (model) =>
            new Set(
              options.data?.channels
                .filter(
                  (c) =>
                    c.model === model && c.provider !== options.data?.provider,
                )
                .map((c) => c.provider) || [],
            ).size + 1,
        ),
      )
    : 1;
  const validPosition = Math.min(position, positions);
  const duplicate = installed.find(
    (i) => i.source_id === source && i.api_key_id === key,
  );
  const stale =
    !!editing && !!options.data && editing.revision !== options.data.revision;
  function edit(item: InstalledChannel) {
    setEditing(item);
    setAdding(false);
    setRemoving(null);
    setSource(item.source_id);
    setKey(item.api_key_id);
    setModelChoices(Object.fromEntries(checks.map(check => [check.model, item.models.includes(check.model)])));
    setPosition(Math.min(...item.models.map((m) => item.positions[m] || 1)));
    setError("");
    setSuccess("");
    void client.invalidateQueries({ queryKey: ["sub-import-options"] });
  }
  function add() {
    setCompactionChoice(null);
    setEditing(null);
    setAdding(true);
    setRemoving(null);
    setSource("");
    setKey("");
    setModelChoices({});
    setPosition(1);
    setError("");
    setSuccess("");
  }
  async function refresh() {
    await Promise.all(
      [
        "sub2api-imports",
        "catalog",
        "channel-controls",
        "control-catalog",
        "sub2api",
        "sub-import-options",
        "channel-routes",
        "channel-management",
      ].map((name) => client.invalidateQueries({ queryKey: [name] })),
    );
  }
  async function mutate(
    action: "add" | "replace" | "delete",
    item?: InstalledChannel,
  ) {
    if (busy) return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      const result = await controlRequest<{ message: string }>(
        "/v1/sub2api/channels",
        {
          method: action === "add" ? "POST" : "PATCH",
          signal: AbortSignal.timeout(60000),
          body: JSON.stringify({
            ...(action !== "add" ? { action } : {}),
            account_id: account.id,
            group_id: target.group_id,
            source_id: item?.source_id || source,
            api_key_id: item?.api_key_id || key,
            ...(action !== "delete" ? { models, position: validPosition } : {}),
            ...(action === "add" ? { compaction_enabled: compactionEnabled } : {}),
            revision: item?.revision || options.data?.revision,
          }),
        },
      );
      setSuccess(result.message);
      setEditing(null);
      setAdding(false);
      setRemoving(null);
      setSource("");
      setKey("");
      await refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : "操作失败");
      await refresh();
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open && !busy) close();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content
          className="guide-dialog sub-import-dialog"
          onEscapeKeyDown={(e) => {
            if (busy) e.preventDefault();
          }}
          onPointerDownOutside={(e) => {
            if (busy) e.preventDefault();
          }}
        >
          <Dialog.Title>添加到渠道</Dialog.Title>
          <Dialog.Description>
            {account.name} / {target.name}
          </Dialog.Description>
          <button
            className="icon-button detail-close"
            aria-label="关闭添加渠道"
            disabled={busy}
            onClick={close}
          >
            <X size={18} />
          </button>
          {imports.isPending && <p role="status">正在读取已添加渠道…</p>}
          {(imports.error || !!imports.data?.unavailable_sources?.length) && (
            <div role="alert" className="error-banner">
              {imports.error?.message ||
                `${imports.data?.unavailable_sources.join("、")} 暂时无法读取，已添加记录可能不完整。`}
              <button
                className="button small"
                disabled={busy}
                onClick={() => void imports.refetch()}
              >
                重新读取
              </button>
            </div>
          )}
          {installed.length > 0 && (
            <section className="sub-installed">
              <h3>已添加到 {installed.length} 个 API key</h3>
              {installed.map((item) => (
                <article key={item.source_id + item.provider}>
                  <strong>{item.name}</strong>
                  <div>
                    {item.source_name} · Key {item.key_position} ·{" "}
                    {item.key_prefix}
                  </div>
                  <ul>
                    {item.models.map((m) => (
                      <li key={m}>
                        {m}{" "}
                        <small>
                          {item.positions[m]
                            ? `第 ${item.positions[m]} 位`
                            : "默认顺序"}
                        </small>
                      </li>
                    ))}
                  </ul>
                  <div className="sub-installed-actions">
                    <button
                      className="button small"
                      disabled={busy || !item.manageable}
                      onClick={() => edit(item)}
                    >
                      <Pencil size={13} />
                      编辑
                    </button>
                    <button
                      className="button small"
                      disabled={busy || !item.manageable}
                      onClick={() => {
                        setRemoving(item);
                        setError("");
                      }}
                    >
                      <Trash2 size={13} />
                      删除
                    </button>
                  </div>
                  {!item.manageable && (
                    <small>此来源需要更新 uni-api 后才能编辑和删除。</small>
                  )}
                  {removing?.provider === item.provider &&
                    removing?.source_id === item.source_id && (
                      <div className="sub-remove" role="alert">
                        从此 API key 移除此临时渠道？
                        <button
                          className="button small"
                          disabled={busy}
                          onClick={() => void mutate("delete", removing)}
                        >
                          确认删除
                        </button>
                        <button
                          className="button small"
                          disabled={busy}
                          onClick={() => setRemoving(null)}
                        >
                          取消
                        </button>
                      </div>
                    )}
                </article>
              ))}
              <button className="button small" disabled={busy} onClick={add}>
                <Plus size={14} />
                添加到其他 API key
              </button>
            </section>
          )}
          {configuredSources.map(source => <section className="sub-installed" key={source}>
            <h3>已有渠道 · {configuredChannels.find(i => i.source_id === source)?.source_name}</h3>
            <ChannelRoutes sourceId={source} providers={configuredChannels.filter(i => i.source_id === source).map(i => i.provider)} />
            {configuredChannels.filter(i => i.source_id === source).map(item => <div className="sub-installed-actions" key={item.provider}><span>{item.name}</span><ChannelSettings row={{provider:item.provider, provider_name:item.name, source_id:item.source_id, source_name:item.source_name, model:item.models[0] || target.result?.model || ""}} /></div>)}
          </section>)}
          {success && (
            <p role="status" className="sub-import-success">
              {success}
            </p>
          )}
          {(error || sources.error || options.error) && (
            <div role="alert" className="error-banner">
              {error || sources.error?.message || options.error?.message}
              {sources.isError && (
                <button type="button" className="button small" disabled={busy || sources.isFetching} onClick={() => void sources.refetch()}>
                  重新读取来源
                </button>
              )}
            </div>
          )}
          {(editing || adding || (!installed.length && !imports.isPending)) && (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                if (!stale && !duplicate && !editing) void mutate("add");
                else if (editing && !stale) void mutate("replace", editing);
              }}
            >
              <h3>{editing ? "编辑临时渠道" : "添加到新的 API key"}</h3>
              <fieldset disabled={busy}>
                <legend>模型</legend>
                <div className="sub-model-options">
                  {checks.map((check) => (
                    <label key={check.model}>
                      <input
                        type="checkbox"
                        checked={models.includes(check.model)}
                        disabled={
                          !available.includes(check.model) &&
                          !editing?.models.includes(check.model)
                        }
                        onChange={(e) =>
                          setModelChoices((old) => ({ ...old, [check.model]: e.target.checked }))
                        }
                      />
                      <span>{check.model}</span>
                      {assessPrice(check, prices).status === "abnormal" && <small className="negative">单价异常</small>}
                      {!available.includes(check.model) && (
                        <small>
                          {editing?.models.includes(check.model)
                            ? "已添加"
                            : importModelLabel(check)}
                        </small>
                      )}
                    </label>
                  ))}
                </div>
              </fieldset>
              {!editing && <div className="sub-import-field">
                <label className="settings-check"><input type="checkbox" checked={compactionEnabled} disabled={busy} onChange={e => setCompactionChoice(e.target.checked)} />开启远程压缩</label>
                <CompactionStatus target={target} />
                <small className="muted">关闭后此渠道跳过 compaction 请求，普通请求照常转发。{target.compaction?.model && ` 已验证模型：${target.compaction.model}。`}</small>
              </div>}
              <label className="sub-import-field">
                uni-api 来源
                <select
                  aria-label="添加到 uni-api 来源"
                  required
                  value={source}
                  disabled={busy || !!editing || !sources.data?.data.length}
                  onChange={(e) => {
                    setSource(e.target.value);
                    setKey("");
                    setPosition(1);
                    setError("");
                  }}
                >
                  <option value="">
                    {sources.data?.data.length ? "选择来源" : sources.isPending ? "正在读取来源…" : sources.isError ? "来源加载失败" : "暂无 uni-api 来源"}
                  </option>
                  {sources.data?.data.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
                </select>
              </label>
              {sources.isPending && <p role="status"><Spinner small /> 正在读取 uni-api 来源…</p>}
              <label className="sub-import-field">
                API key
                <select
                  aria-label="添加到 API key"
                  required
                  value={key}
                  disabled={
                    !source ||
                    options.isFetching ||
                    busy ||
                    !!editing ||
                    !options.data?.supported
                  }
                  onChange={(e) => {
                    setKey(e.target.value);
                    setPosition(1);
                  }}
                >
                  <option value="">选择 API key</option>
                  {options.data?.keys.map((k) => (
                    <option key={k.key_id} value={k.key_id}>
                      Key {k.position} · {k.prefix}
                    </option>
                  ))}
                </select>
              </label>
              <label className="sub-import-field">
                {editing ? "调整位置" : "添加位置"}
                <select
                  aria-label="渠道添加位置"
                  value={validPosition}
                  disabled={!key || options.isFetching || busy}
                  onChange={(e) => setPosition(Number(e.target.value))}
                >
                  {Array.from({ length: positions }, (_, i) => (
                    <option key={i} value={i + 1}>
                      第 {i + 1} 位{i === 0 ? " · 优先请求" : ""}
                    </option>
                  ))}
                </select>
              </label>
              <p className="sub-import-note">
                仅对所选 key
                和模型生效。开启“保留临时配置”时，来源重启后自动恢复。
              </p>
              {source && options.data && !options.data.supported && (
                <div role="alert" className="error-banner">
                  来源尚不支持临时添加渠道，请更新 uni-api。
                </div>
              )}
              {stale && (
                <div role="alert" className="error-banner">
                  渠道配置已变化，请重新读取后点击编辑。
                  <button
                    type="button"
                    className="button small"
                    disabled={busy}
                    onClick={() => {
                      setEditing(null);
                      setAdding(false);
                      void imports.refetch();
                    }}
                  >
                    重新读取
                  </button>
                </div>
              )}
              {!editing && duplicate && (
                <div role="status">
                  已添加到此 API key。
                  <button
                    type="button"
                    className="button small"
                    disabled={busy || !duplicate.manageable}
                    onClick={() => edit(duplicate)}
                  >
                    编辑现有渠道
                  </button>
                </div>
              )}
              <div className="sub-import-actions">
                <button
                  className="button"
                  type="button"
                  disabled={busy}
                  onClick={() => {
                    if (installed.length) {
                      setEditing(null);
                      setAdding(false);
                    } else close();
                  }}
                >
                  取消
                </button>
                <button
                  className="button primary"
                  disabled={
                    busy ||
                    !source ||
                    !key ||
                    !models.length ||
                    !options.data?.supported ||
                    options.isFetching ||
                    options.isError ||
                    stale ||
                    (!editing && !!duplicate) ||
                    (!!editing && !options.data?.manageable)
                  }
                >
                  {busy ? <Spinner small /> : <Plus size={15} />}
                  {editing ? "保存更改" : "添加到渠道"}
                </button>
              </div>
            </form>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

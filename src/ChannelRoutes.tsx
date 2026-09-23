import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { Plus, X } from "lucide-react";
import { controlRequest } from "./api";
import { Spinner } from "./ui";
import { ChannelBatchApply } from "./ChannelBatchApply";
import type { BatchPart, BatchDraft, BatchScope } from "./channelBatch";
import {
  ChannelBindingActions,
  RemoveConfiguredBinding,
} from "./ChannelBindingActions";
import type { ManagedChannel } from "./channelManagement";
import { channelMembers,configuredToolUseResult,useConfiguredChecks,useChannelManagement } from "./channelManagement";
import { ChannelModelSelection, splitChannelModels } from "./ChannelModelSelection";
import { SUB_MODELS } from "./sub2apiModels";
import {useSubAccounts} from "./sub2apiAccounts";
import {toolUseFailed,modelToolUse,toolUseLabels} from "./toolUse";
import {
  managedRouteCount,
  providerRoutes,
  useAllChannelRoutes,
  useChannelRoutes,
} from "./channelRouteData";
import { ModelAliases, aliasMappings } from "./ModelAliases";
import type { ModelAlias } from "./ModelAliases";
import type { KeyInfo } from "./types";
import type { ChannelRoute } from "./channelRouteData";
import {
  ModelPositions,
  selectedModelPositions,
  modelPositionLimit,
} from "./ModelPositions";

export function ChannelRoutes({
  sourceId,
  sourceName,
  providers,
  onEditModels,
  sourcePicker,
  initialKey = "",
}: {
  sourceId: string;
  sourceName?: string;
  providers: string[];
  onEditModels: (rows: ChannelRoute[]) => void;
  sourcePicker?: ReactNode;
  initialKey?: string;
}) {
  const query = useChannelRoutes(sourceId);
  const rows = providerRoutes(query.data?.data || [], providers);
  const keys = [...new Set(rows.map((row) => row.api_key_id))];
  const [chosenKey, setChosenKey] = useState(initialKey);
  const selectedKey = keys.includes(chosenKey) ? chosenKey : keys[0];
  return (
    <>
      {query.isPending && (
        <p role="status">
          <Spinner small /> 正在读取路由位置…
        </p>
      )}
      {(query.error || !!query.data?.unavailable_keys?.length) && (
        <div className="error-banner" role="alert">
          {query.error?.message ||
            "部分 API key 路由暂不可用，当前列表不完整。"}
          <button
            className="button small"
            onClick={() => void query.refetch()}
            disabled={query.isFetching}
          >
            重新读取路由
          </button>
        </div>
      )}
      <div className="route-destination-grid route-browse-selectors">
        {sourcePicker}
        <label className="sub-import-field">
          API key
          <select
            aria-label="查看 API key"
            value={selectedKey || ""}
            disabled={!keys.length}
            onChange={(e) => setChosenKey(e.target.value)}
          >
            {!keys.length && <option value="">暂无已接入的 API key</option>}
            {keys.map((key) => {
              const row = rows.find((r) => r.api_key_id === key)!;
              return (
                <option key={key} value={key}>
                  Key {row.key_position} · {row.key_prefix}
                </option>
              );
            })}
          </select>
        </label>
      </div>
      {rows.length > 0 && (
        <section className="sub-installed route-selected-binding">
          {keys
            .filter((key) => key === selectedKey)
            .map((key) => {
              const models = rows.filter((row) => row.api_key_id === key);
              const first = models[0];
              return (
                <article key={key}>
                  <div className="route-binding-heading">
                    <div>
                      <strong>
                        {sourceName && `${sourceName} · `}Key{" "}
                        {first.key_position}
                      </strong>
                      <small>{models.length} 个模型</small>
                    </div>
                    <div className="route-key-actions">
                      {[...new Set(models.map((r) => r.provider))].map(
                        (provider) => {
                          const providerRows = models.filter(
                            (r) => r.provider === provider,
                          );
                          const target = {
                            source_id: sourceId,
                            source_name: sourceName,
                            provider,
                            name: provider,
                            model: providerRows[0].model,
                          };
                          return (
                            <ChannelBindingActions
                              key={provider}
                              target={target}
                              editLabel={
                                new Set(models.map((r) => r.provider)).size ===
                                1
                                  ? `编辑 Key ${first.key_position}`
                                  : `编辑 Key ${first.key_position} 的模型 ${provider}`
                              }
                              onEdit={() => onEditModels(providerRows)}
                              remove={
                                <RemoveConfiguredBinding
                                  target={target}
                                  keyId={key}
                                  keyPosition={first.key_position}
                                />
                              }
                            />
                          );
                        },
                      )}
                    </div>
                  </div>
                  <RouteModelTable rows={models} />
                </article>
              );
            })}
        </section>
      )}
      {query.isSuccess &&
        !query.data.unavailable_keys?.length &&
        !rows.length && <p className="muted">尚未配置到任何 API key。</p>}
    </>
  );
}

export function RouteModelTable({
  rows,
  showChannel = false,
}: {
  rows: {
    model: string;
    upstream_model?: string;
    position?: number;
    channel?: string;
  }[];
  showChannel?: boolean;
}) {
  return (
    <div className="route-model-table">
      <table aria-label="当前模型路由">
        <thead>
          <tr>
            {showChannel && <th>渠道</th>}
            <th>原来的名字</th>
            <th>重命名后的名字</th>
            <th>路由位置</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr key={`${row.model}:${i}`}>
              {showChannel && <td>{row.channel}</td>}
              <td>{row.upstream_model || row.model}</td>
              <td>{row.model}</td>
              <td>{row.position ? `第 ${row.position} 位` : "默认顺序"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function ConfiguredChannelDialog({
  item: group,
  close,
  initialEdit,
  batchScope,
}: {
  item: ManagedChannel;
  close: () => void;
  initialEdit?: ChannelRoute[];
  batchScope?: BatchScope;
}) {
  const client = useQueryClient();
  const members = channelMembers(group);
  const toolChecks = useConfiguredChecks();
  const accounts = useSubAccounts();
  const inventory = useChannelManagement();
  const [memberId, setMemberId] = useState(members[0].source_id);
  const member = members.find((m) => m.source_id === memberId) || members[0];
  // A site binding carries identity only and may have an empty model list.
  // Hydrate from the already-loaded live inventory instead of treating it as
  // the channel definition or allowing it to hide the saved routes.
  const item = inventory.data?.data.find(m=>m.source_id===member.source_id&&m.provider===member.provider) || member;
  const sources = members.map((m) => m.source_id);
  const routes = useAllChannelRoutes(sources);
  const counts = managedRouteCount(group, sources, routes);
  const [adding, setAdding] = useState(false);
  const modeChosen = useRef(false);
  useEffect(() => {
    if (!modeChosen.current && counts && !counts.partial) {
      setAdding(counts.count === 0);
      modeChosen.current = true;
    }
  }, [counts]);
  const [editingProvider, setEditingProvider] = useState("");
  const toolDefaultsPending=!editingProvider && (toolChecks.isPending || accounts.isPending);
  const toolDefaultsError=!editingProvider && (toolChecks.isError || accounts.isError);
  const initializedEdit = useRef("");
  const toolTarget={tool_use:configuredToolUseResult(item,accounts.data?.data||[],toolChecks.data?.data||[])};
  const [selectedOverride, setSelected] = useState<string[]|null>(null);
  const selected=selectedOverride ?? item.models.filter(model=>!toolUseFailed(toolTarget,model));
  const [aliases, setAliases] = useState<ModelAlias[]>([]);
  const mapping = aliasMappings(aliases, selected);
  const modelOptions = [
    ...new Set([...item.models, ...selected, ...aliases.map(a=>a.upstream), ...SUB_MODELS,
      ...providerRoutes(routes[sources.indexOf(item.source_id)]?.data?.data||[],[item.provider]).flatMap(r=>[r.model,r.upstream_model||r.model]),
      ...(toolChecks.data?.data||[]).filter(c=>c.source_id===item.source_id&&c.provider===item.provider&&c.kind==="model").map(c=>c.model)]),
  ];
  const [key, setKey] = useState("");
  const [position, setPosition] = useState(1);
  const [perModel, setPerModel] = useState(false);
  const [modelPositions, setModelPositions] = useState<Record<string, number>>(
    {},
  );
  const activePositions = perModel ? modelPositions : {};
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  function beginEdit(member: ManagedChannel, rows: ChannelRoute[]) {
    modeChosen.current = true;
    setMemberId(member.source_id);
    initializedEdit.current = "";
    setEditingProvider(rows[0].provider);
    setAdding(true);
    setKey(rows[0].api_key_id);
    setPosition(1);
    setPerModel(true);
    setModelPositions(
      Object.fromEntries(rows.map((r) => [r.model, r.position])),
    );
    const {originals,aliases:renamed}=splitChannelModels(rows,member);
    setSelected(originals);
    setAliases(renamed);
    setError("");
    setSuccess("");
    void client.invalidateQueries({
      queryKey: ["configured-import-options", member.source_id],
    });
  }
  useEffect(() => {
    if (initialEdit?.length) beginEdit(item, initialEdit);
    // A supplied edit is the initial state of this dialog, never replayed on refetch.
  }, []);
  const options = useQuery({
    queryKey: ["configured-import-options", item.source_id, key],
    queryFn: ({ signal }) =>
      controlRequest<{
        revision: string;
        keys: KeyInfo[];
        channels: {
          provider: string;
          model: string;
          upstream_model?: string;
        }[];
      }>(
        "/v1/sub2api/channel-options?" +
          new URLSearchParams({ source_id: item.source_id, api_key_id: key }),
        { signal },
      ),
    enabled: adding,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  useEffect(() => {
    if (
      !editingProvider ||
      inventory.isPending ||
      !options.data ||
      options.isFetching ||
      initializedEdit.current === `${item.source_id}:${key}:${editingProvider}`
    )
      return;
    const positions: Record<string, number> = {},
      counts: Record<string, number> = {};
    for (const row of options.data.channels) {
      counts[row.model] = (counts[row.model] || 0) + 1;
      if (row.provider !== editingProvider) continue;
      positions[row.model] = counts[row.model];
    }
    const {originals,aliases:renamed}=splitChannelModels(options.data.channels.filter(r=>r.provider===editingProvider),item);
    if (!originals.length && !renamed.length) {
      setError("此 API key 的渠道已变化，请重新打开编辑。");
      return;
    }
    setSelected(originals);
    setAliases(renamed);
    setModelPositions(positions);
    initializedEdit.current = `${item.source_id}:${key}:${editingProvider}`;
  }, [editingProvider, options.data, options.isFetching, item, key, inventory.isPending]);
  const positions = mapping.models.length
    ? Math.min(
        ...mapping.models.map(
          (model) =>
            new Set(
              (options.data?.channels || [])
                .filter(
                  (c) => c.model === model && c.provider !== editingProvider,
                )
                .map((c) => c.provider),
            ).size + 1,
        ),
      )
    : 1;
  function batchDraft(part:BatchPart):BatchDraft {
    const resolve=(m:string)=>item.model_mappings?.[m]||m;
    const originals=Object.fromEntries(selected.map(m=>[m,resolve(m)]));
    const renamed=Object.fromEntries(Object.entries(mapping.mappings).map(([name,m])=>[name,resolve(m)]));
    return {part,name:group.name,allowUnverifiedModels:true,scope:batchScope||{kind:"configured",source:item.source_id,provider:item.provider},originals,aliases:renamed,models:{...originals,...renamed},positions:selectedModelPositions(mapping.models,activePositions,Math.min(position,positions)),anchor:{source:item.source_id,key,provider:editingProvider,revision:options.data?.revision||""}};
  }
  function batchButton(part:BatchPart,section?:string) {
    if(!editingProvider)return undefined;
    return <ChannelBatchApply section={section} draft={()=>batchDraft(part)}
      disabled={busy||inventory.isPending||options.isFetching||options.isError||!options.data||!key||(part!=="models"&&!!mapping.error)||((part==="all"||part==="positions")&&!mapping.models.length)}
      onApplied={()=>{setSuccess("批量操作结果已更新，请核对各来源接入状态。");setAdding(false);if(initialEdit)close();}}/>;
  }
  async function save() {
    if (
      busy ||
      inventory.isPending ||
      toolDefaultsPending || toolDefaultsError ||
      mapping.error ||
      !key ||
      !mapping.models.length ||
      !options.data
    )
      return;
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      const result = await controlRequest<{ message: string }>(
        "/v1/channel-management",
        {
          method: "POST",
          signal: AbortSignal.timeout(60000),
          body: JSON.stringify({
            source_id: item.source_id,
            provider: item.provider,
            ...(editingProvider ? { edit_provider: editingProvider, allow_unverified_models: true } : {}),
            api_key_id: key,
            revision: options.data.revision,
            models: selected,
            model_mappings: mapping.mappings,
            position: Math.min(position, positions),
            positions: selectedModelPositions(
              mapping.models,
              activePositions,
              Math.min(position, positions),
            ),
          }),
        },
      );
      setSuccess(result.message);
      setAdding(false);
      await Promise.all(
        [
          "channel-management",
          "channel-routes",
          "catalog",
          "control-catalog",
          "sub2api-imports",
          "channel-controls",
        ].map((name) => client.invalidateQueries({ queryKey: [name] })),
      );
      if (initialEdit) close();
    } catch (e) {
      setError(e instanceof Error ? e.message : "添加失败");
      // Keep the failed revision until the user explicitly reloads, so retrying
      // cannot silently overwrite a concurrent configuration change.
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
        <Dialog.Overlay
          className={`dialog-overlay${initialEdit ? " route-edit-overlay" : ""}`}
        />
        <Dialog.Content
          className={`guide-dialog sub-import-dialog route-workspace${initialEdit ? " route-edit-dialog" : ""}`}
          onEscapeKeyDown={(e) => {
            if (busy) e.preventDefault();
          }}
          onPointerDownOutside={(e) => {
            if (busy) e.preventDefault();
          }}
        >
          <header className="route-dialog-header">
            <Dialog.Title>添加到渠道</Dialog.Title>
            <Dialog.Description>
              {group.name} · {group.source_name}
            </Dialog.Description>
            <Dialog.Close
              className="icon-button detail-close"
              aria-label="关闭添加渠道"
              disabled={busy}
            >
              <X size={18} />
            </Dialog.Close>
          </header>
          <nav className="route-mode-tabs" aria-label="渠道接入视图">
            <button
              type="button"
              aria-pressed={!adding}
              disabled={busy}
              onClick={() => {
                modeChosen.current = true;
                setAdding(false);
                setError("");
              }}
            >
              已接入
              {counts
                ? ` · ${counts.partial ? "至少 " : ""}${counts.count} 个 Key`
                : ""}
            </button>
            <button
              type="button"
              aria-pressed={adding}
              disabled={busy}
              onClick={() => {
                if (adding) return;
                modeChosen.current = true;
                setAdding(true);
                setEditingProvider("");
                setPerModel(false);
                setModelPositions({});
                setSelected(null);
                setAliases([]);
                setKey("");
                setPosition(1);
                setError("");
                setSuccess("");
              }}
            >
              <Plus size={14} />
              {editingProvider && adding ? "编辑接入" : "新增接入"}
            </button>
          </nav>
          <div className="route-dialog-body">
            {!adding && (
              <section
                className="configured-source"
                aria-label={`${item.source_name} 接入情况`}
              >
                <div className="route-section-heading">
                  <h3>
                    {counts
                      ? `已添加到 ${counts.partial ? "至少 " : ""}${counts.count} 个 API key`
                      : "正在读取接入状态…"}
                  </h3>
                  <ChannelBatchApply remove draft={()=>batchDraft("delete")} disabled={busy||!counts?.count||counts.partial}
                    onApplied={()=>{setSuccess("批量删除结果已更新，请核对各来源接入状态。");if(initialEdit)close();}}/>
                </div>
                <ChannelRoutes
                  key={item.source_id}
                  sourceId={item.source_id}
                  sourceName={item.source_name}
                  providers={[item.provider]}
                  initialKey={key}
                  onEditModels={(rows) => beginEdit(item, rows)}
                  sourcePicker={
                    <label className="sub-import-field">
                      uni-api 来源
                      <select
                        aria-label="查看 uni-api 来源"
                        value={item.source_id}
                        onChange={(e) => setMemberId(e.target.value)}
                      >
                        {members.map((m) => (
                          <option key={m.source_id} value={m.source_id}>
                            {m.source_name}
                          </option>
                        ))}
                      </select>
                    </label>
                  }
                />
              </section>
            )}
            {success && (
              <p role="status" className="sub-import-success">
                {success}
              </p>
            )}
            {(error || options.error) && (
              <p role="alert" className="negative">
                {error || options.error?.message}
                <button
                  className="button small"
                  disabled={busy || options.isFetching || inventory.isPending}
                  onClick={() => {
                    initializedEdit.current = "";
                    setError("");
                    void options.refetch();
                  }}
                >
                  重新读取配置
                </button>
              </p>
            )}
            {adding && (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  void save();
                }}
              >
                <h3>
                  {editingProvider
                    ? "编辑此 API key 的模型"
                    : "添加模型到 API key"}
                </h3>
                {toolDefaultsPending && <p role="status"><Spinner small /> 正在读取各模型的 Tool use 检测结果…</p>}
                {toolDefaultsError && <div className="error-banner" role="alert">Tool use 检测记录读取失败。<button type="button" className="button small" onClick={()=>{void toolChecks.refetch();void accounts.refetch();}}>重试</button></div>}
                <div className="route-destination-grid">
                  <label className="sub-import-field">
                    uni-api 来源
                    <select
                      aria-label="添加到 uni-api 来源"
                      value={item.source_id}
                      disabled={busy || !!editingProvider}
                      onChange={(e) => {
                        const next = members.find(
                          (m) => m.source_id === e.target.value,
                        )!;
                        setMemberId(next.source_id);
                        setSelected(null);
                        setAliases([]);
                        setKey("");
                        setPosition(1);
                        setPerModel(false);
                        setModelPositions({});
                        setError("");
                      }}
                    >
                      {members.map((m) => (
                        <option key={m.source_id} value={m.source_id}>
                          {m.source_name}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="sub-import-field">
                    API key
                    <select
                      aria-label="添加到 API key"
                      value={key}
                      disabled={busy || options.isFetching}
                      onChange={(e) => {
                        if (editingProvider) {
                          const candidates = providerRoutes(
                            routes[sources.indexOf(item.source_id)]?.data
                              ?.data || [],
                            [item.provider],
                          ).filter((r) => r.api_key_id === e.target.value);
                          const provider =
                            candidates.find((r) => r.origin_provider)
                              ?.provider || candidates[0]?.provider;
                          if (provider)
                            beginEdit(
                              item,
                              candidates.filter((r) => r.provider === provider),
                            );
                        } else {
                          setKey(e.target.value);
                          setPosition(1);
                          setPerModel(false);
                          setModelPositions({});
                        }
                      }}
                    >
                      {!editingProvider && (
                        <option value="">选择 API key</option>
                      )}
                      {options.data?.keys
                        .filter(
                          (k) =>
                            !editingProvider ||
                            providerRoutes(
                              routes[sources.indexOf(item.source_id)]?.data
                                ?.data || [],
                              [item.provider],
                            ).some((r) => r.api_key_id === k.key_id),
                        )
                        .map((k) => (
                          <option key={k.key_id} value={k.key_id}>
                            Key {k.position} · {k.prefix}
                          </option>
                        ))}
                    </select>
                  </label>
                  <label className="sub-import-field">
                    调整位置
                    <select
                      aria-label="渠道添加位置"
                      value={
                        perModel ? "per-model" : Math.min(position, positions)
                      }
                      onChange={(e) => {
                        if (e.target.value === "per-model") {
                          setPerModel(true);
                        } else {
                          setPosition(Number(e.target.value));
                          setPerModel(false);
                          setModelPositions({});
                        }
                      }}
                      disabled={busy || options.isFetching || !key}
                    >
                      <option value="per-model">逐模型微调</option>
                      {Array.from({ length: positions }, (_, i) => (
                        <option key={i} value={i + 1}>
                          第 {i + 1} 位
                        </option>
                      ))}
                    </select>
                  </label>
                </div>
                <ChannelModelSelection
                  models={editingProvider?modelOptions:item.models}
                  selected={selected} onChange={setSelected} editing={!!editingProvider}
                  disabled={busy || options.isFetching || inventory.isPending || toolDefaultsPending || toolDefaultsError}
                  actions={batchButton("models","模型勾选")}
                  status={model=>toolUseFailed(toolTarget,model)?<small className="negative">Tool use · {toolUseLabels[modelToolUse(toolTarget,model)!.status]}</small>:undefined}
                />
                <ModelAliases
                  actions={batchButton("aliases","模型重命名")}
                  models={modelOptions}
                  aliases={aliases}
                  onChange={setAliases}
                  disabled={busy || options.isFetching}
                />
                {mapping.error && (
                  <p role="alert" className="negative">
                    {mapping.error}
                  </p>
                )}
                <ModelPositions
                  actions={batchButton("positions","路由位置")}
                  models={mapping.models}
                  channels={options.data?.channels || []}
                  provider={editingProvider}
                  positions={activePositions}
                  defaultPosition={Math.min(position, positions)}
                  onChange={setModelPositions}
                  disabled={busy || options.isFetching || !key || !perModel}
                  uniform={!perModel}
                />
                <p className="muted">
                  {editingProvider
                    ? "保存更改仅更新当前 API key；各项“应用此项于所有”只同步对应设置，底部按钮可同步全部设置。"
                    : "只对所选 API key 开放勾选的原模型和填写的对外模型名。"}
                </p>
                <div className="sub-import-actions">
                  {batchButton("all")}
                  <button
                    type="button"
                    className="button"
                    disabled={busy}
                    onClick={() => (initialEdit ? close() : setAdding(false))}
                  >
                    取消
                  </button>
                  <button
                    className="button primary"
                    disabled={
                      busy ||
                      inventory.isPending ||
                      toolDefaultsPending || toolDefaultsError ||
                      options.isFetching ||
                      options.isError ||
                      mapping.models.some(
                        (m) =>
                          (activePositions[m] ??
                            Math.min(position, positions)) >
                          modelPositionLimit(
                            m,
                            options.data?.channels || [],
                            editingProvider,
                          ),
                      ) ||
                      !!mapping.error ||
                      !key ||
                      !mapping.models.length
                    }
                  >
                    {busy ? <Spinner small /> : <Plus size={14} />}
                    {editingProvider ? "保存更改" : "添加到渠道"}
                  </button>
                </div>
              </form>
            )}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

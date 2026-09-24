import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { Plus, Trash2, X } from "lucide-react";
import { controlRequest } from "./api";
import { Spinner } from "./ui";
import { ChannelBatchApply } from "./ChannelBatchApply";
import type { BatchPart, BatchDraft } from "./channelBatch";
import type { ConsoleSourcesQuery } from "./consoleSources";
import {toolUseFailed,modelToolUse,toolUseLabels} from "./toolUse";
import { CompactionStatus } from "./SubCompaction";
import { assessPrice } from "./sub2apiPriceCheck";
import type { KeyInfo, ModelPrice } from "./types";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import type { InstalledChannel, SubImportsQuery } from "./sub2apiImports";
import { boundGroups } from "./sub2apiImports";
import { ConfiguredChannelDialog, RouteModelTable } from "./ChannelRoutes";
import {
  ChannelBindingActions,
  BindingRemoveDialog,
  RemoveConfiguredBinding,
} from "./ChannelBindingActions";
import { channelBindingView } from "./channelBindingView";
import type { ChannelRoute } from "./channelRouteData";
import { providerRoutes, useAllChannelRoutes } from "./channelRouteData";
import { ModelAliases, aliasMappings } from "./ModelAliases";
import { ChannelModelSelection, splitChannelModels, unavailableModelChanges } from "./ChannelModelSelection";
import type { ModelAlias } from "./ModelAliases";
import {
  ModelPositions,
  selectedModelPositions,
  modelPositionLimit,
} from "./ModelPositions";
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
    (i) =>
      i.kind !== "configured" &&
      i.account_id === account.id &&
      i.group_id === target.group_id,
  );
  const configuredChannels = (imports.data?.data || []).filter(
    (i) =>
      i.kind === "configured" &&
      boundGroups(i).some(
        (g) => g.account_id === account.id && g.group_id === target.group_id,
      ),
  );
  if (
    configured &&
    !configuredChannels.some(
      (i) =>
        i.source_id === configured.source_id &&
        i.provider === configured.provider,
    )
  )
    configuredChannels.push(configured);
  const configuredSources = [
    ...new Set(configuredChannels.map((i) => i.source_id)),
  ];
  const configuredRoutes = useAllChannelRoutes(configuredSources);
  const boundKeys = new Set(
    installed.map((i) => `${i.source_id}:${i.api_key_id}`),
  );
  let partialBindings =
    imports.isPending ||
    imports.isError ||
    !!imports.data?.unavailable_sources?.length;
  configuredSources.forEach((id, index) => {
    const query = configuredRoutes[index];
    partialBindings ||=
      !query.data || query.isError || !!query.data.unavailable_keys?.length;
    for (const row of providerRoutes(
      query.data?.data || [],
      configuredChannels
        .filter((i) => i.source_id === id)
        .map((i) => i.provider),
    )) {
      boundKeys.add(`${id}:${row.api_key_id}`);
    }
  });
  const [editing, setEditing] = useState<InstalledChannel | null>(null);
  const [view, setView] = useState<"new" | "existing" | null>(null);
  const [viewSource, setViewSource] = useState("");
  const [viewKey, setViewKey] = useState("");
  const viewSources = [
    ...new Map(
      [...installed, ...configuredChannels].map((i) => [
        i.source_id,
        i.source_name,
      ]),
    ).entries(),
  ];
  const activeSource = viewSources.some(([id]) => id === viewSource)
    ? viewSource
    : viewSources[0]?.[0] || "";
  const sourceBindings = installed.filter((i) => i.source_id === activeSource);
  const sourceRoutes =
    configuredRoutes[configuredSources.indexOf(activeSource)];
  const nativeRows = providerRoutes(
    sourceRoutes?.data?.data || [],
    configuredChannels
      .filter((i) => i.source_id === activeSource)
      .map((i) => i.provider),
  );
  const keyOptions = [
    ...new Map(
      [...sourceBindings, ...nativeRows].map((item) => [item.api_key_id, item]),
    ).values(),
  ].sort((a, b) => a.key_position - b.key_position);
  const activeKey = keyOptions.some((i) => i.api_key_id === viewKey)
    ? viewKey
    : keyOptions[0]?.api_key_id || "";
  const selectedKey = keyOptions.find((i) => i.api_key_id === activeKey);
  const bindings = channelBindingView(
    activeSource,
    activeKey,
    installed,
    configuredChannels,
    nativeRows,
  );
  const [editingNative, setEditingNative] = useState<{
    item: ManagedChannel;
    rows: ChannelRoute[];
  } | null>(null);
  const showForm =
    !!editing ||
    view === "new" ||
    (view === null &&
      !installed.length &&
      !configuredChannels.length &&
      !imports.isPending);
  const [removing, setRemoving] = useState<InstalledChannel | null>(null);
  const isRemoving = (item: InstalledChannel) =>
    !!removing &&
    removing.source_id === item.source_id &&
    removing.api_key_id === item.api_key_id &&
    removing.provider === item.provider;
  const [modelChoices, setModelChoices] = useState<Record<string, boolean>>({});
  const editChecks = [...checks];
  for(const model of [...installed.flatMap(i=>[...i.models,...Object.values(i.model_mappings||{})]),...(editing?.models||[]),...Object.values(editing?.model_mappings||{})]) {
    if(!editChecks.some(c=>c.model===model))editChecks.push({model,state:"idle",message:"",result:null});
  }
  const originals = editChecks
    .filter(
      (check) =>
        modelChoices[check.model] ??
        (editing ? editing.models.includes(check.model)&&(!editing.model_mappings?.[check.model]||editing.model_mappings[check.model]===check.model) : available.includes(check.model) &&
          assessPrice(check, prices).status !== "abnormal" && !toolUseFailed(target,check.model)),
    )
    .map((check) => check.model);
  const [aliases, setAliases] = useState<ModelAlias[]>([]);
  const mapping = aliasMappings(aliases, originals);
  const models = mapping.models;
  const invalidModels = unavailableModelChanges(
    {...Object.fromEntries(originals.map(m=>[m,m])),...mapping.mappings},
    Object.fromEntries((editing?.models||[]).map(m=>[m,editing?.model_mappings?.[m]||m])),
    model=>available.includes(model),
  );
  const [source, setSource] = useState("");
  const [key, setKey] = useState("");
  const [position, setPosition] = useState(1);
  const [perModel, setPerModel] = useState(false);
  const [modelPositions, setModelPositions] = useState<Record<string, number>>(
    {},
  );
  const activePositions = perModel ? modelPositions : {};
  const initializedPositions = useRef("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [compactionChoice, setCompactionChoice] = useState<boolean | null>(
    null,
  );
  const compactionEnabled =
    compactionChoice ?? target.compaction?.status === "supported";
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
    staleTime: 0,
    refetchOnWindowFocus: false,
  });
  useEffect(() => {
    if (
      !editing ||
      !options.data ||
      options.isFetching ||
      editing.revision !== options.data.revision
    )
      return;
    const id = `${editing.provider}:${editing.revision}`;
    if (initializedPositions.current === id) return;
    const counts: Record<string, number> = {},
      current: Record<string, number> = { ...editing.positions };
    for (const c of options.data.channels) {
      counts[c.model] = (counts[c.model] || 0) + 1;
      if (c.provider === editing.provider) current[c.model] = counts[c.model];
    }
    setModelPositions(current);
    initializedPositions.current = id;
  }, [editing, options.data, options.isFetching]);
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
  function batchDraft(part:BatchPart):BatchDraft {
    const originalModels=Object.fromEntries(originals.map(m=>[m,m]));
    return {part,name:`${account.name} / ${target.name}`,scope:{kind:"site",account:account.id,group:target.group_id},originals:originalModels,aliases:mapping.mappings,models:{...originalModels,...mapping.mappings},positions:selectedModelPositions(models,activePositions,validPosition),anchor:{source:editing?.source_id||activeSource,key:editing?.api_key_id||activeKey,provider:editing?.provider||"",revision:editing?.revision||""}};
  }
  function batchButton(part:BatchPart,section?:string) {
    if(!editing)return undefined;
    return <ChannelBatchApply section={section} draft={()=>batchDraft(part)}
      disabled={busy||invalidModels.length>0||stale||options.isFetching||options.isError||!options.data?.manageable||!source||!key||(part!=="models"&&!!mapping.error)||((part==="all"||part==="positions")&&!models.length)}
      onApplied={()=>{setEditing(null);setView("existing");setSuccess("批量操作结果已更新，请核对各来源接入状态。");}}/>;
  }
  function edit(item: InstalledChannel) {
    initializedPositions.current = "";
    setEditing(item);
    setView("existing");
    setRemoving(null);
    setSource(item.source_id);
    setKey(item.api_key_id);
    const saved=splitChannelModels(item.models.map(model=>({model,upstream_model:item.model_mappings?.[model]})));
    setModelChoices(Object.fromEntries([...new Set([...checks.map(c=>c.model),...item.models])].map(model=>[model,saved.originals.includes(model)])));
    setAliases(saved.aliases);
    setPosition(1);
    setPerModel(true);
    setModelPositions({ ...item.positions });
    setError("");
    setSuccess("");
    void client.invalidateQueries({ queryKey: ["sub-import-options"] });
  }
  function add() {
    setCompactionChoice(null);
    setEditing(null);
    setView("new");
    setRemoving(null);
    setSource("");
    setKey("");
    setModelChoices({});
    setAliases([]);
    setPosition(1);
    setPerModel(false);
    setModelPositions({});
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
    if (busy || (action !== "delete" && (mapping.error || invalidModels.length))) return;
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
            ...(action !== "delete"
              ? {
                  models: originals,
                  ...(aliases.length || action === "replace"
                    ? { model_mappings: mapping.mappings }
                    : {}),
                  position: validPosition,
                  positions: selectedModelPositions(
                    models,
                    activePositions,
                    validPosition,
                  ),
                }
              : {}),
            ...(action === "add"
              ? { compaction_enabled: compactionEnabled }
              : {}),
            revision: item?.revision || options.data?.revision,
          }),
        },
      );
      setSuccess(result.message);
      setViewSource(item?.source_id || source);
      setViewKey(item?.api_key_id || key);
      setEditing(null);
      setView("existing");
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
          className="guide-dialog sub-import-dialog route-workspace"
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
          </header>
          <nav className="route-mode-tabs" aria-label="渠道接入视图">
            <button
              type="button"
              aria-pressed={!showForm}
              disabled={busy}
              onClick={() => {
                setView("existing");
                setEditing(null);
                setRemoving(null);
                setError("");
              }}
            >
              已接入 · {partialBindings ? "至少 " : ""}
              {boundKeys.size} 个 Key
            </button>
            <button
              type="button"
              aria-pressed={showForm}
              disabled={busy}
              onClick={() => {
                if (!showForm) add();
              }}
            >
              <Plus size={14} />
              {editing ? "编辑接入" : "新增接入"}
            </button>
          </nav>
          <div className="route-dialog-body">
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
            {!showForm && (
              <>
                <div className="route-section-heading">
                  <h3>已保存接入</h3>
                  <ChannelBatchApply remove onPreview={()=>setView("existing")} draft={()=>batchDraft("delete")} disabled={busy||partialBindings||!boundKeys.size}
                    onApplied={()=>{setEditing(null);setView("existing");setSuccess("批量删除结果已更新，请核对各来源接入状态。");}}/>
                </div>
                <div className="route-destination-grid route-browse-selectors">
                  <label className="sub-import-field">
                    uni-api 来源
                    <select
                      aria-label="查看 uni-api 来源"
                      value={activeSource}
                      disabled={!viewSources.length}
                      onChange={(e) => {
                        setViewSource(e.target.value);
                        setViewKey("");
                        setRemoving(null);
                      }}
                    >
                      {!viewSources.length && (
                        <option value="">暂无已接入来源</option>
                      )}
                      {viewSources.map(([id, name]) => (
                        <option key={id} value={id}>
                          {name}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="sub-import-field">
                    API key
                    <select
                      aria-label="查看 API key"
                      value={activeKey}
                      disabled={!keyOptions.length}
                      onChange={(e) => {
                        setViewKey(e.target.value);
                        setRemoving(null);
                      }}
                    >
                      {!keyOptions.length && (
                        <option value="">
                          {sourceRoutes?.isPending
                            ? "正在读取路由…"
                            : "暂无已接入的 API key"}
                        </option>
                      )}
                      {keyOptions.map((item) => (
                        <option key={item.api_key_id} value={item.api_key_id}>
                          Key {item.key_position} · {item.key_prefix}
                        </option>
                      ))}
                    </select>
                  </label>
                </div>
                {sourceRoutes?.isPending && (
                  <p role="status">
                    <Spinner small />
                    正在读取路由位置…
                  </p>
                )}
                {(sourceRoutes?.error ||
                  !!sourceRoutes?.data?.unavailable_keys?.length) && (
                  <div role="alert" className="error-banner">
                    {sourceRoutes.error?.message ||
                      "部分 API key 路由暂不可用，当前列表不完整。"}
                    <button
                      type="button"
                      className="button small"
                      disabled={sourceRoutes.isFetching}
                      onClick={() => void sourceRoutes.refetch()}
                    >
                      重新读取路由
                    </button>
                  </div>
                )}
                {!!bindings.length && selectedKey && (
                  <section className="sub-installed route-selected-binding">
                    <article>
                      <div className="route-binding-heading">
                        <div>
                          <strong>
                            {
                              viewSources.find(
                                ([id]) => id === activeSource,
                              )?.[1]
                            }{" "}
                            · Key {selectedKey.key_position}
                          </strong>
                          <small>
                            {bindings.length} 个渠道 ·{" "}
                            {bindings.reduce((n, b) => n + b.rows.length, 0)}{" "}
                            条模型路由
                          </small>
                        </div>
                      </div>
                      <div className="route-binding-providers">
                        {bindings.map((binding) => (
                          <div
                            className="route-binding-provider"
                            key={binding.provider}
                          >
                            <div>
                              <strong>{binding.name}</strong>
                              <small>
                                {binding.installed ? "站点接入" : "配置渠道"} ·{" "}
                                {binding.rows.length} 个模型
                              </small>
                            </div>
                            <ChannelBindingActions
                              target={{
                                provider: binding.provider,
                                name: binding.name,
                                source_id: activeSource,
                                source_name: viewSources.find(
                                  ([id]) => id === activeSource,
                                )?.[1],
                                model: binding.rows[0]?.model || "",
                              }}
                              disabled={
                                busy ||
                                (!!binding.installed &&
                                  !binding.installed.manageable)
                              }
                              onEdit={() => {
                                if (binding.installed) edit(binding.installed);
                                else if (binding.configured)
                                  setEditingNative({
                                    item: {
                                      ...binding.configured,
                                      engine: "",
                                      account_ids: [account.id],
                                    },
                                    rows: binding.rows,
                                  });
                              }}
                              remove={
                                binding.installed ? (
                                  <Dialog.Root
                                    open={isRemoving(binding.installed)}
                                    onOpenChange={(open) => {
                                      if (busy) return;
                                      setRemoving(open ? binding.installed! : null);
                                      setError("");
                                    }}
                                  >
                                    <Dialog.Trigger asChild>
                                      <button
                                        type="button"
                                        className="button small"
                                        disabled={busy || !binding.installed.manageable}
                                      >
                                        <Trash2 size={13} />
                                        删除
                                      </button>
                                    </Dialog.Trigger>
                                    {isRemoving(binding.installed) && (
                                      <BindingRemoveDialog
                                        target={binding.installed}
                                        keyPosition={binding.installed.key_position}
                                        busy={busy}
                                        close={() => setRemoving(null)}
                                        onConfirm={() => void mutate("delete", removing!)}
                                      >
                                        {error && (
                                          <div role="alert" className="error-banner">{error}</div>
                                        )}
                                      </BindingRemoveDialog>
                                    )}
                                  </Dialog.Root>
                                ) : (
                                  <RemoveConfiguredBinding
                                    target={{
                                      provider: binding.provider,
                                      name: binding.name,
                                      source_id: activeSource,
                                      source_name: viewSources.find(
                                        ([id]) => id === activeSource,
                                      )?.[1],
                                      model: binding.rows[0]?.model || "",
                                    }}
                                    keyId={activeKey}
                                    keyPosition={selectedKey.key_position}
                                  />
                                )
                              }
                            />
                            {binding.installed &&
                              !binding.installed.manageable && (
                                <small className="route-binding-notice">
                                  此来源需要更新 uni-api 后才能编辑和删除。
                                </small>
                              )}
                          </div>
                        ))}
                      </div>
                      <RouteModelTable
                        showChannel={bindings.length > 1}
                        rows={bindings
                          .flatMap((binding) =>
                            binding.rows.map((row) => ({
                              ...row,
                              channel: binding.name,
                            })),
                          )
                          .sort(
                            (a, b) =>
                              a.model.localeCompare(b.model) ||
                              (a.position || 0) - (b.position || 0),
                          )}
                      />
                    </article>
                  </section>
                )}
                {!bindings.length &&
                  !!viewSources.length &&
                  !sourceRoutes?.isPending &&
                  !sourceRoutes?.error && (
                    <p className="route-empty">此来源暂无已接入的路由。</p>
                  )}
                {!installed.length &&
                  !configuredChannels.length &&
                  !imports.isPending && (
                    <p className="route-empty">
                      尚未接入任何 API key，选择“新增接入”开始配置。
                    </p>
                  )}
              </>
            )}
            {success && (
              <p role="status" className="sub-import-success">
                {success}
              </p>
            )}
            {(error || sources.error || options.error) && (
              <div role="alert" className="error-banner">
                {error || sources.error?.message || options.error?.message}
                {sources.isError && (
                  <button
                    type="button"
                    className="button small"
                    disabled={busy || sources.isFetching}
                    onClick={() => void sources.refetch()}
                  >
                    重新读取来源
                  </button>
                )}
              </div>
            )}
            {showForm && (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  if (!stale && !duplicate && !editing) void mutate("add");
                  else if (editing && !stale) void mutate("replace", editing);
                }}
              >
                <h3>{editing ? "编辑临时渠道" : "添加到新的 API key"}</h3>
                <div className="route-destination-grid">
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
                        setPerModel(false);
                        setModelPositions({});
                        setError("");
                      }}
                    >
                      <option value="">
                        {sources.data?.data.length
                          ? "选择来源"
                          : sources.isPending
                            ? "正在读取来源…"
                            : sources.isError
                              ? "来源加载失败"
                              : "暂无 uni-api 来源"}
                      </option>
                      {sources.data?.data.map((s) => (
                        <option key={s.id} value={s.id}>
                          {s.name}
                        </option>
                      ))}
                    </select>
                  </label>
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
                        setPerModel(false);
                        setModelPositions({});
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
                      value={perModel ? "per-model" : validPosition}
                      disabled={!key || options.isFetching || busy}
                      onChange={(e) => {
                        if (e.target.value === "per-model") {
                          setPerModel(true);
                        } else {
                          setPosition(Number(e.target.value));
                          setPerModel(false);
                          setModelPositions({});
                        }
                      }}
                    >
                      <option value="per-model">逐模型微调</option>
                      {Array.from({ length: positions }, (_, i) => (
                        <option key={i} value={i + 1}>
                          第 {i + 1} 位{i === 0 ? " · 优先请求" : ""}
                        </option>
                      ))}
                    </select>
                  </label>
                </div>
                {sources.isPending && (
                  <p role="status">
                    <Spinner small /> 正在读取 uni-api 来源…
                  </p>
                )}
                <ChannelModelSelection
                  models={editChecks.map(c=>c.model)} selected={originals} editing={!!editing}
                  disabled={busy} actions={batchButton("models","模型勾选")}
                  canSelect={model=>available.includes(model)}
                  onChange={selected=>setModelChoices(Object.fromEntries(editChecks.map(c=>[c.model,selected.includes(c.model)])))}
                  status={model=>{
                    const check=editChecks.find(c=>c.model===model)!;
                    return <>
                      {toolUseFailed(target,model) && <small className="negative">Tool use · {toolUseLabels[modelToolUse(target,model)!.status]}</small>}
                      {assessPrice(check,prices).status==="abnormal" && <small className="negative">单价异常</small>}
                      {!available.includes(model) && <small>{importModelLabel(check)}</small>}
                    </>;
                  }}
                />
                <ModelAliases
                  actions={batchButton("aliases","模型重命名")}
                  canSelect={model=>available.includes(model)}
                  models={[
                    ...new Set([
                      ...(editing?editChecks.map(c=>c.model):available),
                      ...Object.values(editing?.model_mappings || {}),
                    ]),
                  ]}
                  aliases={aliases}
                  onChange={setAliases}
                  disabled={busy}
                />
                {!!invalidModels.length && <p role="alert" className="negative">新增模型须检测可用：{invalidModels.join("、")}</p>}
                {mapping.error && (
                  <p role="alert" className="negative">
                    {mapping.error}
                  </p>
                )}
                {!editing && (
                  <div className="sub-import-field">
                    <label className="settings-check">
                      <input
                        type="checkbox"
                        checked={compactionEnabled}
                        disabled={busy}
                        onChange={(e) => setCompactionChoice(e.target.checked)}
                      />
                      开启远程压缩
                    </label>
                    <CompactionStatus target={target} />
                    <small className="muted">
                      关闭后此渠道跳过 compaction 请求，普通请求照常转发。
                      {target.compaction?.model &&
                        ` 已验证模型：${target.compaction.model}。`}
                    </small>
                  </div>
                )}
                <ModelPositions
                  actions={batchButton("positions","路由位置")}
                  models={models}
                  channels={options.data?.channels || []}
                  provider={options.data?.provider}
                  positions={activePositions}
                  defaultPosition={validPosition}
                  onChange={setModelPositions}
                  disabled={!key || options.isFetching || busy || !perModel}
                  uniform={!perModel}
                />
                <p className="sub-import-note">
                  {editing ? "保存更改仅更新当前 API key；分项按钮只同步对应设置，底部按钮可同步全部设置。" : "仅对所选 key 和模型生效。"}开启“保留临时配置”时，来源重启后自动恢复。
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
                        setView("existing");
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
                  {batchButton("all")}
                  <button
                    className="button"
                    type="button"
                    disabled={busy}
                    onClick={() => {
                      if (installed.length) {
                        setEditing(null);
                        setView("existing");
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
                      !!mapping.error || invalidModels.length>0 ||
                      !models.length ||
                      !options.data?.supported ||
                      options.isFetching ||
                      options.isError ||
                      models.some(
                        (m) =>
                          (activePositions[m] ?? validPosition) >
                          modelPositionLimit(
                            m,
                            options.data?.channels || [],
                            options.data?.provider,
                          ),
                      ) ||
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
          </div>
          {editingNative && (
            <ConfiguredChannelDialog
              item={editingNative.item}
              batchScope={{kind:"site",account:account.id,group:target.group_id}}
              initialEdit={editingNative.rows}
              close={() => setEditingNative(null)}
            />
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

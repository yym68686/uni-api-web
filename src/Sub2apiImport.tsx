import { useEffect, useMemo, useRef, useState } from "react";
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
import { ChannelRoutes, RouteModelTable } from "./ChannelRoutes";
import { providerRoutes, useAllChannelRoutes } from "./channelRouteData";
import { ChannelSettings } from "./ChannelSettings";
import { ModelAliases, aliasMappings } from "./ModelAliases";
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
  const activeKey = sourceBindings.some((i) => i.api_key_id === viewKey)
    ? viewKey
    : sourceBindings[0]?.api_key_id || "";
  const showForm =
    !!editing ||
    view === "new" ||
    (view === null &&
      !installed.length &&
      !configuredChannels.length &&
      !imports.isPending);
  const [removing, setRemoving] = useState<InstalledChannel | null>(null);
  const [modelChoices, setModelChoices] = useState<Record<string, boolean>>({});
  const originals = checks
    .filter(
      (check) =>
        modelChoices[check.model] ??
        (available.includes(check.model) &&
          assessPrice(check, prices).status !== "abnormal"),
    )
    .map((check) => check.model);
  const [aliases, setAliases] = useState<ModelAlias[]>([]);
  const mapping = aliasMappings(aliases, originals);
  const models = mapping.models;
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
  function edit(item: InstalledChannel) {
    initializedPositions.current = "";
    setEditing(item);
    setView("existing");
    setRemoving(null);
    setSource(item.source_id);
    setKey(item.api_key_id);
    setModelChoices(
      Object.fromEntries(
        checks.map((check) => [
          check.model,
          item.models.includes(check.model) &&
            !item.model_mappings?.[check.model],
        ]),
      ),
    );
    setAliases(
      Object.entries(item.model_mappings || {}).map(
        ([publicName, upstream]) => ({ public: publicName, upstream }),
      ),
    );
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
    if (busy || (action !== "delete" && mapping.error)) return;
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
                  {!!sourceBindings.length && (
                    <label className="sub-import-field">
                      API key
                      <select
                        aria-label="查看 API key"
                        value={activeKey}
                        onChange={(e) => {
                          setViewKey(e.target.value);
                          setRemoving(null);
                        }}
                      >
                        {sourceBindings.map((item) => (
                          <option key={item.api_key_id} value={item.api_key_id}>
                            Key {item.key_position} · {item.key_prefix}
                          </option>
                        ))}
                      </select>
                    </label>
                  )}
                </div>
                {installed.length > 0 && (
                  <section className="sub-installed route-selected-binding">
                    {sourceBindings
                      .filter((item) => item.api_key_id === activeKey)
                      .map((item) => (
                        <article key={item.source_id + item.provider}>
                          <div className="route-binding-heading">
                            <div>
                              <strong>
                                {item.source_name} · Key {item.key_position}
                              </strong>
                              <small>{item.models.length} 个模型</small>
                            </div>
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
                          </div>
                          <RouteModelTable
                            rows={item.models.map((model) => ({
                              model,
                              upstream_model: item.model_mappings?.[model],
                              position: item.positions[model],
                            }))}
                          />
                          {!item.manageable && (
                            <small>
                              此来源需要更新 uni-api 后才能编辑和删除。
                            </small>
                          )}
                          {removing?.provider === item.provider &&
                            removing?.source_id === item.source_id && (
                              <div className="sub-remove" role="alert">
                                从此 API key 移除此临时渠道？
                                <button
                                  className="button small"
                                  disabled={busy}
                                  onClick={() =>
                                    void mutate("delete", removing)
                                  }
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
                  </section>
                )}
                {configuredSources
                  .filter((source) => source === activeSource)
                  .map((source) => (
                    <section className="sub-installed" key={source}>
                      <h3>
                        已有渠道 ·{" "}
                        {
                          configuredChannels.find((i) => i.source_id === source)
                            ?.source_name
                        }
                      </h3>
                      <ChannelRoutes
                        sourceId={source}
                        providers={configuredChannels
                          .filter((i) => i.source_id === source)
                          .map((i) => i.provider)}
                      />
                      {configuredChannels
                        .filter((i) => i.source_id === source)
                        .map((item) => (
                          <div
                            className="sub-installed-actions"
                            key={item.provider}
                          >
                            <span>{item.name}</span>
                            <ChannelSettings
                              row={{
                                provider: item.provider,
                                provider_name: item.name,
                                source_id: item.source_id,
                                source_name: item.source_name,
                                model:
                                  item.models[0] || target.result?.model || "",
                              }}
                            />
                          </div>
                        ))}
                    </section>
                  ))}
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
                <fieldset disabled={busy}>
                  <legend>模型</legend>
                  <div className="sub-model-options">
                    {checks.map((check) => (
                      <label key={check.model}>
                        <input
                          type="checkbox"
                          checked={originals.includes(check.model)}
                          disabled={
                            !available.includes(check.model) &&
                            !editing?.models.includes(check.model)
                          }
                          onChange={(e) =>
                            setModelChoices((old) => ({
                              ...old,
                              [check.model]: e.target.checked,
                            }))
                          }
                        />
                        <span>{check.model}</span>
                        {assessPrice(check, prices).status === "abnormal" && (
                          <small className="negative">单价异常</small>
                        )}
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
                <ModelAliases
                  models={[
                    ...new Set([
                      ...available,
                      ...Object.values(editing?.model_mappings || {}),
                    ]),
                  ]}
                  aliases={aliases}
                  onChange={setAliases}
                  disabled={busy}
                />
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
                      !!mapping.error ||
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
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

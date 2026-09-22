import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { Plus, Pencil, X } from "lucide-react";
import { controlRequest } from "./api";
import { Spinner } from "./ui";
import { ChannelSettings } from "./ChannelSettings";
import type { ManagedChannel } from "./channelManagement";
import { channelMembers } from "./channelManagement";
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
import { ChannelRouteEditor } from "./ChannelRouteEditor";
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
}: {
  sourceId: string;
  sourceName?: string;
  providers: string[];
  onEditModels?: (rows: ChannelRoute[]) => void;
}) {
  const query = useChannelRoutes(sourceId);
  const [editingRoutes, setEditingRoutes] = useState<ChannelRoute[] | null>(
    null,
  );
  const rows = providerRoutes(query.data?.data || [], providers);
  const keys = [...new Set(rows.map((row) => row.api_key_id))];
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
      {rows.length > 0 && (
        <section className="sub-installed">
          <h3>
            {sourceName
              ? `${sourceName} · ${keys.length} 个 API key`
              : `已添加到 ${keys.length} 个 API key`}
          </h3>
          {keys.map((key) => {
            const models = rows.filter((row) => row.api_key_id === key);
            const first = models[0];
            return (
              <article key={key}>
                <strong>Key {first.key_position}</strong>
                <div>{first.key_prefix}</div>
                <ul>
                  {models.map((row) => (
                    <li key={`${row.provider}:${row.model}`}>
                      <span>
                        {row.upstream_model &&
                        row.upstream_model !== row.model ? (
                          <>
                            {row.upstream_model}
                            <small> → </small>
                            {row.model}
                          </>
                        ) : (
                          row.model
                        )}
                      </span>
                      <small>第 {row.position} 位</small>
                    </li>
                  ))}
                </ul>
                <div className="sub-installed-actions">
                  {onEditModels ? (
                    [...new Set(models.map((r) => r.provider))].map(
                      (provider, i, providers) => (
                        <button
                          key={provider}
                          className="button small"
                          aria-label={
                            providers.length === 1
                              ? `编辑 Key ${first.key_position}`
                              : `编辑 Key ${first.key_position} 的模型 ${provider}`
                          }
                          onClick={() =>
                            onEditModels(
                              models.filter((r) => r.provider === provider),
                            )
                          }
                        >
                          <Pencil size={13} />
                          {providers.length === 1
                            ? "编辑"
                            : `编辑配置 ${i + 1}`}
                        </button>
                      ),
                    )
                  ) : (
                    <button
                      className="button small"
                      aria-label={`编辑 Key ${first.key_position}`}
                      onClick={() => setEditingRoutes(models)}
                    >
                      <Pencil size={13} />
                      编辑
                    </button>
                  )}
                </div>
              </article>
            );
          })}
        </section>
      )}
      {query.isSuccess &&
        !query.data.unavailable_keys?.length &&
        !rows.length && <p className="muted">尚未配置到任何 API key。</p>}
      {editingRoutes && (
        <ChannelRouteEditor
          sourceId={sourceId}
          sourceName={sourceName}
          rows={editingRoutes}
          close={() => setEditingRoutes(null)}
        />
      )}
    </>
  );
}

export function ConfiguredChannelDialog({
  item: group,
  close,
}: {
  item: ManagedChannel;
  close: () => void;
}) {
  const client = useQueryClient();
  const members = channelMembers(group);
  const [memberId, setMemberId] = useState(members[0].source_id);
  const item = members.find((m) => m.source_id === memberId) || members[0];
  const sources = members.map((m) => m.source_id);
  const routes = useAllChannelRoutes(sources);
  const counts = managedRouteCount(group, sources, routes);
  const [adding, setAdding] = useState(false);
  const [editingProvider, setEditingProvider] = useState("");
  const initializedEdit = useRef("");
  const [selected, setSelected] = useState<string[]>(item.models);
  const [aliases, setAliases] = useState<ModelAlias[]>([]);
  const mapping = aliasMappings(aliases, selected);
  const modelOptions = [
    ...new Set([...item.models, ...aliases.map((a) => a.upstream)]),
  ];
  const [key, setKey] = useState("");
  const [position, setPosition] = useState(1);
  const [uniformApplied, setUniformApplied] = useState(true);
  const [modelPositions, setModelPositions] = useState<Record<string, number>>(
    {},
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  function beginEdit(member: ManagedChannel, rows: ChannelRoute[]) {
    setMemberId(member.source_id);
    initializedEdit.current = "";
    setEditingProvider(rows[0].provider);
    setAdding(true);
    setKey(rows[0].api_key_id);
    setPosition(1);
    setUniformApplied(false);
    setModelPositions(
      Object.fromEntries(rows.map((r) => [r.model, r.position])),
    );
    const originals: string[] = [],
      renamed: ModelAlias[] = [];
    for (const row of rows) {
      const upstream = row.upstream_model || row.model;
      if (
        member.models.includes(row.model) &&
        (member.model_mappings?.[row.model] || row.model) === upstream
      )
        originals.push(row.model);
      else
        renamed.push({
          public: row.model,
          upstream:
            member.models.find(
              (m) => (member.model_mappings?.[m] || m) === upstream,
            ) || upstream,
        });
    }
    setSelected(originals);
    setAliases(renamed);
    setError("");
    setSuccess("");
    void client.invalidateQueries({
      queryKey: ["configured-import-options", member.source_id],
    });
  }
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
      !options.data ||
      options.isFetching ||
      initializedEdit.current === `${item.source_id}:${key}:${editingProvider}`
    )
      return;
    const originals: string[] = [],
      renamed: ModelAlias[] = [],
      positions: Record<string, number> = {},
      counts: Record<string, number> = {};
    for (const row of options.data.channels) {
      counts[row.model] = (counts[row.model] || 0) + 1;
      if (row.provider !== editingProvider) continue;
      positions[row.model] = counts[row.model];
      const upstream =
        row.upstream_model || item.model_mappings?.[row.model] || row.model;
      if (
        item.models.includes(row.model) &&
        (item.model_mappings?.[row.model] || row.model) === upstream
      )
        originals.push(row.model);
      else
        renamed.push({
          public: row.model,
          upstream:
            item.models.find(
              (m) => (item.model_mappings?.[m] || m) === upstream,
            ) || upstream,
        });
    }
    if (!originals.length && !renamed.length) {
      setError("此 API key 的渠道已变化，请重新打开编辑。");
      return;
    }
    setSelected(originals);
    setAliases(renamed);
    setModelPositions(positions);
    initializedEdit.current = `${item.source_id}:${key}:${editingProvider}`;
  }, [editingProvider, options.data, options.isFetching, item, key]);
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
  async function save() {
    if (
      busy ||
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
            ...(editingProvider ? { edit_provider: editingProvider } : {}),
            api_key_id: key,
            revision: options.data.revision,
            models: selected,
            model_mappings: mapping.mappings,
            position: Math.min(position, positions),
            positions: selectedModelPositions(
              mapping.models,
              modelPositions,
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
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="guide-dialog sub-import-dialog">
          <Dialog.Title>添加到渠道</Dialog.Title>
          <Dialog.Description>
            {group.name} · {group.source_name}
          </Dialog.Description>
          <Dialog.Close
            className="icon-button detail-close"
            aria-label="关闭添加渠道"
          >
            <X size={18} />
          </Dialog.Close>

          {!adding && (
            <>
              {members.length > 1 && (
                <h3 className="configured-route-summary">
                  {counts
                    ? `已添加到 ${counts.partial ? "至少 " : ""}${counts.count} 个 API key`
                    : "正在读取接入状态…"}
                  <small>{members.length} 个 uni-api 来源</small>
                </h3>
              )}
              {members.map((member) => (
                <section
                  key={member.source_id}
                  className="configured-source"
                  aria-label={`${member.source_name} 接入情况`}
                >
                  {members.length > 1 &&
                    new Set(members.map((m) => m.base)).size > 1 && (
                      <p className="configured-source-address">
                        {member.source_name} · {member.base || "未提供上游地址"}
                      </p>
                    )}
                  <ChannelRoutes
                    sourceId={member.source_id}
                    sourceName={
                      members.length > 1 ? member.source_name : undefined
                    }
                    providers={[member.provider]}
                    onEditModels={(rows) => beginEdit(member, rows)}
                  />
                  <div className="configured-source-settings">
                    <span>{member.source_name}</span>
                    <ChannelSettings
                      row={{
                        provider: member.provider,
                        provider_name: member.name,
                        source_id: member.source_id,
                        source_name: member.source_name,
                        model: member.models[0] || "",
                      }}
                    />
                  </div>
                </section>
              ))}
            </>
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
                disabled={busy || options.isFetching}
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
          {!adding && (
            <button
              className="button small"
              onClick={() => {
                setAdding(true);
                setEditingProvider("");
                setUniformApplied(true);
                setModelPositions({});
                setSelected(item.models);
                setAliases([]);
                setKey("");
                setError("");
                setSuccess("");
              }}
            >
              <Plus size={14} />
              添加到 API key / 模型重命名
            </button>
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
                    setSelected(next.models);
                    setAliases([]);
                    setKey("");
                    setPosition(1);
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
              <fieldset disabled={busy || options.isFetching}>
                <legend>原模型</legend>
                <div className="sub-model-options">
                  {item.models.map((model) => (
                    <label key={model}>
                      <input
                        type="checkbox"
                        checked={selected.includes(model)}
                        onChange={(e) =>
                          setSelected((old) =>
                            e.target.checked
                              ? [...old, model]
                              : old.filter((m) => m !== model),
                          )
                        }
                      />
                      <span>{model}</span>
                    </label>
                  ))}
                </div>
              </fieldset>
              <ModelAliases
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
              <label className="sub-import-field">
                API key
                <select
                  aria-label="添加到 API key"
                  value={key}
                  disabled={busy || options.isFetching}
                  onChange={(e) => {
                    if (editingProvider) {
                      const candidates = providerRoutes(
                        routes[sources.indexOf(item.source_id)]?.data?.data ||
                          [],
                        [item.provider],
                      ).filter((r) => r.api_key_id === e.target.value);
                      const provider =
                        candidates.find((r) => r.origin_provider)?.provider ||
                        candidates[0]?.provider;
                      if (provider)
                        beginEdit(
                          item,
                          candidates.filter((r) => r.provider === provider),
                        );
                    } else {
                      setKey(e.target.value);
                      setPosition(1);
                      setModelPositions({});
                    }
                  }}
                >
                  {!editingProvider && <option value="">选择 API key</option>}
                  {options.data?.keys
                    .filter(
                      (k) =>
                        !editingProvider ||
                        providerRoutes(
                          routes[sources.indexOf(item.source_id)]?.data?.data ||
                            [],
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
                统一位置
                <select
                  aria-label="渠道添加位置"
                  value={
                    editingProvider && !uniformApplied
                      ? ""
                      : Math.min(position, positions)
                  }
                  onChange={(e) => {
                    setPosition(Number(e.target.value));
                    setUniformApplied(true);
                    setModelPositions({});
                  }}
                  disabled={busy || options.isFetching || !key}
                >
                  {editingProvider && !uniformApplied && (
                    <option value="">保持各模型当前位置</option>
                  )}
                  {Array.from({ length: positions }, (_, i) => (
                    <option key={i} value={i + 1}>
                      第 {i + 1} 位
                    </option>
                  ))}
                </select>
              </label>
              <ModelPositions
                models={mapping.models}
                channels={options.data?.channels || []}
                provider={editingProvider}
                positions={modelPositions}
                defaultPosition={Math.min(position, positions)}
                onChange={setModelPositions}
                disabled={busy || options.isFetching || !key}
              />
              <p className="muted">
                {editingProvider
                  ? "只修改当前选中的 API key；切换 API key 会载入该 Key 已保存的模型和位置。"
                  : "只对所选 API key 开放勾选的原模型和填写的对外模型名。"}
              </p>
              <div className="dialog-actions">
                <button
                  type="button"
                  className="button"
                  disabled={busy}
                  onClick={() => setAdding(false)}
                >
                  取消
                </button>
                <button
                  className="button primary"
                  disabled={
                    busy ||
                    options.isFetching ||
                    options.isError ||
                    mapping.models.some(
                      (m) =>
                        (modelPositions[m] ?? Math.min(position, positions)) >
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
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

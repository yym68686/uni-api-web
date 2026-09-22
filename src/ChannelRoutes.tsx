import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { Plus, X } from "lucide-react";
import { controlRequest } from "./api";
import { Spinner } from "./ui";
import { ChannelSettings } from "./ChannelSettings";
import type { ManagedChannel } from "./channelManagement";
import { providerRoutes, useChannelRoutes } from "./channelRouteData";
import { ModelAliases, aliasMappings } from "./ModelAliases";
import type { ModelAlias } from "./ModelAliases";
import type { KeyInfo } from "./types";

export function ChannelRoutes({
  sourceId,
  providers,
}: {
  sourceId: string;
  providers: string[];
}) {
  const query = useChannelRoutes(sourceId);
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
          <h3>已添加到 {keys.length} 个 API key</h3>
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

export function ConfiguredChannelDialog({
  item,
  close,
}: {
  item: ManagedChannel;
  close: () => void;
}) {
  const client = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [selected, setSelected] = useState<string[]>(item.models);
  const [aliases, setAliases] = useState<ModelAlias[]>([]);
  const mapping = aliasMappings(aliases, selected);
  const [key, setKey] = useState("");
  const [position, setPosition] = useState(1);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const options = useQuery({
    queryKey: ["configured-import-options", item.source_id, key],
    queryFn: ({ signal }) =>
      controlRequest<{
        revision: string;
        keys: KeyInfo[];
        channels: { provider: string; model: string }[];
      }>(
        "/v1/sub2api/channel-options?" +
          new URLSearchParams({ source_id: item.source_id, api_key_id: key }),
        { signal },
      ),
    enabled: adding,
    retry: false,
  });
  const positions = mapping.models.length
    ? Math.min(
        ...mapping.models.map(
          (model) =>
            new Set(
              (options.data?.channels || [])
                .filter((c) => c.model === model)
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
            api_key_id: key,
            revision: options.data.revision,
            models: selected,
            model_mappings: mapping.mappings,
            position: Math.min(position, positions),
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
      void options.refetch();
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
            {item.name} · {item.source_name}
          </Dialog.Description>
          <Dialog.Close
            className="icon-button detail-close"
            aria-label="关闭添加渠道"
          >
            <X size={18} />
          </Dialog.Close>

          <ChannelRoutes
            sourceId={item.source_id}
            providers={[item.provider]}
          />
          {success && (
            <p role="status" className="sub-import-success">
              {success}
            </p>
          )}
          {(error || options.error) && (
            <p role="alert" className="negative">
              {error || options.error?.message}
            </p>
          )}
          {!adding && (
            <button
              className="button small"
              onClick={() => {
                setAdding(true);
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
              <h3>添加模型到 API key</h3>
              <fieldset disabled={busy}>
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
                models={item.models}
                aliases={aliases}
                onChange={setAliases}
                disabled={busy}
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
                添加位置
                <select
                  aria-label="渠道添加位置"
                  value={Math.min(position, positions)}
                  onChange={(e) => setPosition(Number(e.target.value))}
                  disabled={busy || options.isFetching || !key}
                >
                  {Array.from({ length: positions }, (_, i) => (
                    <option key={i} value={i + 1}>
                      第 {i + 1} 位
                    </option>
                  ))}
                </select>
              </label>
              <p className="muted">
                保存为此 API key
                专用渠道；只开放勾选的原模型和填写的对外模型名。已有基础渠道路由保持独立。
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
                    !!mapping.error ||
                    !key ||
                    !mapping.models.length
                  }
                >
                  {busy ? <Spinner small /> : <Plus size={14} />}添加到渠道
                </button>
              </div>
            </form>
          )}
          <div className="sub-installed-actions">
            <ChannelSettings
              row={{
                provider: item.provider,
                provider_name: item.name,
                source_id: item.source_id,
                source_name: item.source_name,
                model: item.models[0] || "",
              }}
            />
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

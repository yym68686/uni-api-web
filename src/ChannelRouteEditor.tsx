import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { controlRequest } from "./api";
import { Spinner } from "./ui";
import type { ChannelRoute } from "./channelRouteData";

interface CatalogRow {
  provider: string;
  model: string;
  upstream_model?: string;
}
export function ChannelRouteEditor({
  sourceId,
  sourceName,
  rows,
  close,
}: {
  sourceId: string;
  sourceName?: string;
  rows: ChannelRoute[];
  close: () => void;
}) {
  const client = useQueryClient(),
    key = rows[0].api_key_id;
  const [overrides, setOverrides] = useState<Record<string, number>>({});
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const query = useQuery({
    queryKey: ["route-edit-options", sourceId, key],
    queryFn: ({ signal }) =>
      controlRequest<{ revision: string; channels: CatalogRow[] }>(
        "/v1/sub2api/channel-options?" +
          new URLSearchParams({ source_id: sourceId, api_key_id: key }),
        { signal },
      ),
    staleTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  const ids = new Set(rows.map((r) => `${r.provider}\n${r.model}`));
  const ranks = new Map<string, number>(),
    seen = new Set<string>();
  const current = (query.data?.channels || []).flatMap((r) => {
    const id = `${r.provider}\n${r.model}`;
    if (seen.has(id)) return [];
    seen.add(id);
    const position = (ranks.get(r.model) || 0) + 1;
    ranks.set(r.model, position);
    return ids.has(id) ? [{ ...r, id, position }] : [];
  });
  const invalid =
    current.some((r) => {
      const p = overrides[r.id] ?? r.position;
      return p < 1 || p > (ranks.get(r.model) || 0);
    }) ||
    current.some((r, i) =>
      current.some(
        (other, j) =>
          i !== j &&
          r.model === other.model &&
          overrides[r.id] != null &&
          overrides[r.id] !== r.position &&
          overrides[other.id] != null &&
          overrides[other.id] !== other.position &&
          overrides[r.id] === overrides[other.id],
      ),
    );
  const changed = current.some(
    (r) => overrides[r.id] != null && overrides[r.id] !== r.position,
  );
  async function save() {
    if (busy || invalid || !changed || !query.data) return;
    setBusy(true);
    setError("");
    try {
      await controlRequest(
        `/v1/sources/${encodeURIComponent(sourceId)}/channel-routes`,
        {
          method: "PATCH",
          signal: AbortSignal.timeout(60000),
          body: JSON.stringify({
            api_key_id: key,
            revision: query.data.revision,
            moves: current
              .filter(
                (r) =>
                  overrides[r.id] != null && overrides[r.id] !== r.position,
              )
              .map((r) => ({
                provider: r.provider,
                model: r.model,
                position: overrides[r.id],
              })),
          }),
        },
      );
      await Promise.all(
        [
          "channel-routes",
          "channel-controls",
          "control-catalog",
          "catalog",
          "sub2api-imports",
          "channel-management",
        ].map((name) => client.invalidateQueries({ queryKey: [name] })),
      );
      close();
    } catch (e) {
      setError(e instanceof Error ? e.message : "保存失败");
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
        <Dialog.Overlay className="dialog-overlay route-edit-overlay" />
        <Dialog.Content className="guide-dialog sub-import-dialog route-edit-dialog">
          <Dialog.Title>编辑 API key · Key {rows[0].key_position}</Dialog.Title>
          <Dialog.Description>
            {sourceName && `${sourceName} · `}
            {rows[0].key_prefix} · 分别调整每个模型的路由位置
          </Dialog.Description>
          <button
            className="icon-button detail-close"
            aria-label="关闭路由编辑"
            onClick={close}
            disabled={busy}
          >
            <X size={18} />
          </button>
          {query.isFetching && (
            <p role="status">
              <Spinner small />
              正在读取当前路由…
            </p>
          )}
          {(error || query.error) && (
            <div className="error-banner" role="alert">
              {error || query.error?.message}
              <button
                className="button small"
                disabled={busy || query.isFetching}
                onClick={() => {
                  setOverrides({});
                  setError("");
                  void query.refetch();
                }}
              >
                重新读取路由
              </button>
            </div>
          )}
          {query.isSuccess && current.length !== ids.size && (
            <p role="alert" className="negative">
              部分渠道已不在此 API key 中，已从编辑列表移除。
            </p>
          )}
          <div className="model-position-list">
            {current.map((r) => (
              <label key={r.id}>
                <span>
                  {r.upstream_model && r.upstream_model !== r.model
                    ? `${r.upstream_model} → ${r.model}`
                    : r.model}
                  <small className="check-source">{r.provider}</small>
                </span>
                <select
                  aria-label={`${r.provider} ${r.model} 的路由位置`}
                  value={overrides[r.id] ?? r.position}
                  disabled={busy || query.isFetching}
                  onChange={(e) =>
                    setOverrides((old) => ({
                      ...old,
                      [r.id]: Number(e.target.value),
                    }))
                  }
                >
                  {Array.from({ length: ranks.get(r.model) || 0 }, (_, i) => (
                    <option key={i} value={i + 1}>
                      第 {i + 1} 位
                    </option>
                  ))}
                </select>
              </label>
            ))}
          </div>
          {invalid && (
            <p role="alert" className="negative">
              同一模型的渠道位置不能重复，请分别选择位置。
            </p>
          )}
          <div className="sub-import-actions">
            <button className="button" disabled={busy} onClick={close}>
              取消
            </button>
            <button
              className="button primary"
              disabled={
                busy || query.isFetching || query.isError || invalid || !changed
              }
              onClick={() => void save()}
            >
              {busy && <Spinner small />}保存更改
            </button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

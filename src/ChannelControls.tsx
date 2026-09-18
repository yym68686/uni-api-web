import { useEffect, useRef, useState } from "react";
import { useQueries, useQueryClient } from "@tanstack/react-query";
import {
  ArrowDown,
  ArrowUp,
  Check,
  ChevronDown,
  RotateCcw,
  X,
} from "lucide-react";
import { channelParams, controlRequest, request } from "./api";
import type { Catalog, Channel, Connection, KeyInfo } from "./types";
import type { ConsoleSource } from "./SourceSettings";
import { channelName } from "./format";
import { Spinner, Tip } from "./ui";

export interface ControlRule {
  api_key_id: string;
  model: string;
  order: string[];
  disabled: string[];
}
export interface ControlState {
  revision: string;
  instance_id: string;
  rules: ControlRule[];
}
type ControlTarget = Pick<Channel, "source_id" | "provider">;
interface Draft {
  source: string;
  revision: string;
  rule: ControlRule;
}
const STALE_DRAFT = "规则已变化，请放弃草稿后重新编辑。";
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
    (r) =>
      (!r.api_key_id || r.api_key_id === key) &&
      (!r.model || r.model === model) &&
      !(r.api_key_id === key && r.model === model) &&
      r.disabled.includes(provider),
  );
}
export function useChannelControls({
  connection,
  rows,
  sourceIds: selectedSourceIds,
  keyId,
  model,
  enabled,
}: {
  connection: Connection;
  rows: Channel[];
  sourceIds?: string[];
  keyId: string;
  model: string;
  enabled: boolean;
}) {
  const client = useQueryClient();
  const [drafts, setDrafts] = useState<Map<string, Draft>>(new Map());
  const [errors, setErrors] = useState<Map<string, string>>(new Map());
  const [pending, setPending] = useState(false);
  const locked = useRef(false);
  const sourceIds = selectedSourceIds || [
    ...new Set(rows.map((r) => r.source_id).filter((id): id is string => !!id)),
  ];
  const controlSourceIds = [
    ...new Set([...sourceIds, ...[...drafts.values()].map((d) => d.source)]),
  ];
  const rawKey = keyId.includes("::") ? keyId.split("::")[1] : keyId;
  const scope = (source: string) => JSON.stringify([source, rawKey, model]);
  const stateKey = (source: string) => [
    "channel-controls",
    connection.session,
    source,
  ];
  const states = useQueries({
    queries: controlSourceIds.map((source) => ({
      queryKey: stateKey(source),
      enabled: enabled && !pending,
      queryFn: async ({ signal }: { signal: AbortSignal }) => {
        const data = await controlRequest<ControlState>(
          `/v1/sources/${encodeURIComponent(source)}/channel-controls`,
          { signal },
        );
        if (!data.revision || !Array.isArray(data.rules))
          throw Error("来源未提供临时控制状态");
        return data;
      },
      retry: false,
      staleTime: 5000,
      refetchInterval: enabled && !pending ? 5000 : false,
      refetchIntervalInBackground: false,
    })),
  });
  // Complete key/model membership is needed for safe reordering. Search, status,
  // endpoint and page filters never remove hidden channels from saved rules.
  const catalogs = useQueries({
    queries: sourceIds.map((source) => ({
      queryKey: ["control-catalog", connection.session, source, keyId],
      enabled,
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        request<Catalog>(
          { ...connection, sourceId: source },
          "/v1/model-channels?" + channelParams(keyId, "15m", "", "all", "all"),
          signal,
        ),
      retry: false,
      staleTime: 30000,
    })),
  });
  function info(row: ControlTarget) {
    const source = row.source_id || "",
      id = scope(source),
      index = sourceIds.indexOf(source);
    const state = states[controlSourceIds.indexOf(source)],
      catalog = catalogs[index];
    const current = state?.data?.rules.find(
      (r) => r.api_key_id === rawKey && r.model === model,
    );
    const providers = [
      ...new Set(
        (catalog?.data?.data || [])
          .filter((r) => !model || r.model === model)
          .map((r) => r.provider),
      ),
    ];
    const draft = drafts.get(id);
    const rule = draft?.rule ||
      current || { api_key_id: rawKey, model, order: [], disabled: [] };
    const order = orderedProviders(providers, rule.order);
    return {
      source,
      id,
      state,
      catalog,
      current,
      draft,
      rule,
      order,
      providers,
      inherited: inheritedDisabled(
        state?.data?.rules || [],
        rawKey,
        model,
        row.provider,
      ),
      stale: !!draft && draft.revision !== state?.data?.revision,
      ready:
        !!source &&
        !!state?.data &&
        !!catalog?.data &&
        !state.isError &&
        !catalog.isError &&
        providers.includes(row.provider),
      error: errors.get(id) || state?.error?.message || catalog?.error?.message,
      pending,
    };
  }
  function edit(
    row: Channel,
    update: (rule: ControlRule, order: string[]) => ControlRule,
  ) {
    const i = info(row);
    if (!i.ready || i.stale || locked.current) return;
    const next = update(
      { ...i.rule, order: [...i.rule.order], disabled: [...i.rule.disabled] },
      i.order,
    );
    setDrafts((old) =>
      new Map(old).set(i.id, {
        source: i.source,
        revision: i.draft?.revision || i.state.data!.revision,
        rule: next,
      }),
    );
    setErrors((old) => {
      const next = new Map(old);
      next.delete(i.id);
      return next;
    });
  }
  function toggle(row: Channel, disabled: boolean) {
    if (info(row).inherited) return;
    edit(row, (rule) => ({
      ...rule,
      disabled: disabled
        ? [...new Set([...rule.disabled, row.provider])]
        : rule.disabled.filter((p) => p !== row.provider),
    }));
  }
  function neighbor(row: Channel, direction: number, visible: Channel[]) {
    const i = info(row),
      names = new Set(
        visible
          .filter((r) => r.source_id === row.source_id)
          .map((r) => r.provider),
      );
    const order = i.order.filter((p) => names.has(p));
    return order[order.indexOf(row.provider) + direction];
  }
  function move(row: Channel, direction: number, visible: Channel[]) {
    const target = neighbor(row, direction, visible);
    if (!target) return;
    edit(row, (rule, order) => {
      const next = [...order],
        a = next.indexOf(row.provider),
        b = next.indexOf(target);
      [next[a], next[b]] = [next[b], next[a]];
      return { ...rule, order: next };
    });
  }
  function discardAll() {
    if (locked.current) return;
    setDrafts(new Map());
    setErrors(new Map());
  }
  async function commit(changes: [string, Draft][], action: "set" | "reset") {
    if (locked.current || !changes.length) return;
    locked.current = true;
    setPending(true);
    const groups = new Map<string, [string, Draft][]>();
    for (const entry of changes) {
      const group = groups.get(entry[1].source) || [];
      group.push(entry);
      groups.set(entry[1].source, group);
    }
    try {
      await Promise.all(
        [...groups].map(async ([source, entries]) => {
          const remaining = new Set(entries.map(([id]) => id));
          try {
            // An older poll must not overwrite the state acknowledged by a write.
            await client.cancelQueries({ queryKey: stateKey(source) });
            let revision = client.getQueryData<ControlState>(
              stateKey(source),
            )?.revision;
            if (
              !revision ||
              entries.some(([, draft]) => draft.revision !== revision)
            )
              throw new Error(STALE_DRAFT);
            for (const [id, draft] of entries) {
              const result: ControlState = await controlRequest<ControlState>(
                `/v1/sources/${encodeURIComponent(source)}/channel-controls`,
                {
                  method: "POST",
                  body: JSON.stringify({ ...draft.rule, action, revision }),
                },
              );
              if (!result.revision || !Array.isArray(result.rules))
                throw new Error("来源返回无效状态，请刷新后核对");
              const previousRevision = revision;
              revision = result.revision;
              client.setQueryData(stateKey(source), result);
              setDrafts((old) => {
                const next = new Map(old);
                next.delete(id);
                // Only advance drafts based on our own acknowledged change.
                // External revisions continue to require an explicit new edit.
                for (const [otherId, other] of next) {
                  if (
                    other.source === source &&
                    other.revision === previousRevision
                  )
                    next.set(otherId, { ...other, revision: result.revision });
                }
                return next;
              });
              setErrors((old) => {
                const next = new Map(old);
                next.delete(id);
                return next;
              });
              remaining.delete(id);
            }
          } catch (e) {
            setErrors((old) => {
              const next = new Map(old);
              for (const id of remaining)
                next.set(
                  id,
                  e instanceof Error ? e.message : "修改失败，请刷新核对",
                );
              return next;
            });
          }
        }),
      );
    } finally {
      locked.current = false;
      setPending(false);
      void Promise.all([
        client.invalidateQueries({ queryKey: ["catalog"] }),
        client.invalidateQueries({ queryKey: ["live-metrics"] }),
        client.invalidateQueries({ queryKey: ["control-catalog"] }),
        client.invalidateQueries({ queryKey: ["control-persistence"] }),
      ]);
    }
  }
  function applyAll() {
    return commit([...drafts], "set");
  }
  function reset(row: ControlTarget) {
    const i = info(row);
    if (
      !i.state?.data ||
      i.state.isError ||
      !i.current ||
      [...drafts.values()].some((draft) => draft.source === i.source)
    )
      return;
    return commit(
      [
        [
          i.id,
          {
            source: i.source,
            revision: i.state.data.revision,
            rule: i.current,
          },
        ],
      ],
      "reset",
    );
  }
  function arrange(input: Channel[]) {
    const groups = new Map<string, Channel[]>();
    for (const row of input) {
      const src = row.source_id || "";
      const group = groups.get(src) || [];
      group.push(row);
      groups.set(src, group);
    }
    for (const group of groups.values()) {
      const i = info(group[0]);
      if (!i.rule.order.length) continue;
      const ranks = new Map(i.order.map((p, n) => [p, n]));
      group.sort(
        (a, b) =>
          (ranks.get(a.provider) ?? Infinity) -
          (ranks.get(b.provider) ?? Infinity),
      );
    }
    const positions = new Map<string, number>();
    return input.map((row) => {
      const src = row.source_id || "",
        index = positions.get(src) || 0;
      positions.set(src, index + 1);
      return groups.get(src)![index];
    });
  }
  const scopes = sourceIds.map((source) =>
    info({ source_id: source, provider: "" }),
  );
  const draftIssues = [...drafts].flatMap(([id, draft]) => {
    const state = states[controlSourceIds.indexOf(draft.source)];
    const message =
      errors.get(id) ||
      state?.error?.message ||
      (state?.data?.revision !== draft.revision ? STALE_DRAFT : "");
    return message ? [{ id, source: draft.source, message }] : [];
  });
  return {
    info,
    toggle,
    neighbor,
    move,
    discardAll,
    applyAll,
    reset,
    arrange,
    scopes,
    drafts,
    draftIssues,
    pending,
  };
}
export type ChannelControls = ReturnType<typeof useChannelControls>;
export function ChannelControlCell({
  row,
  controls,
  visible,
  configOrder,
}: {
  row: Channel;
  controls: ChannelControls;
  visible: Channel[];
  configOrder: boolean;
}) {
  const i = controls.info(row),
    label = `${row.source_name || row.source_id || ""} ${channelName(row)} ${row.model}`;
  if (!i.ready)
    return (
      <td className="control-actions-cell">
        {i.error ? (
          <span className="negative">{i.error}</span>
        ) : (
          <span className="muted">
            <Spinner small />
            读取控制状态…
          </span>
        )}
      </td>
    );
  return (
    <td className="control-actions-cell">
      <div className="control-row-actions">
        <label>
          <input
            type="checkbox"
            aria-label={`临时停用 ${label}`}
            checked={i.rule.disabled.includes(row.provider) || i.inherited}
            disabled={i.pending || i.stale || i.inherited}
            onChange={(e) => controls.toggle(row, e.target.checked)}
          />
          {i.inherited ? "继承停用" : "临时停用"}
        </label>
        <Tip
          text={
            configOrder
              ? "只调整此来源内的渠道顺序，未显示的渠道仍保留。"
              : "选择 Provider / API key 顺序后可调整。"
          }
        >
          <span className="control-order-actions">
            <button
              className="icon-button"
              aria-label={`上移 ${label}`}
              disabled={
                i.pending ||
                i.stale ||
                !configOrder ||
                !controls.neighbor(row, -1, visible)
              }
              onClick={() => controls.move(row, -1, visible)}
            >
              <ArrowUp size={15} />
            </button>
            <button
              className="icon-button"
              aria-label={`下移 ${label}`}
              disabled={
                i.pending ||
                i.stale ||
                !configOrder ||
                !controls.neighbor(row, 1, visible)
              }
              onClick={() => controls.move(row, 1, visible)}
            >
              <ArrowDown size={15} />
            </button>
          </span>
        </Tip>
      </div>
      {i.stale && <small className="negative">{STALE_DRAFT}</small>}
      {i.error && (
        <small className="negative" role="alert">
          {i.error}
        </small>
      )}
    </td>
  );
}

export const RESET_SCOPE_LABEL = "撤销更改";
export function ChannelControlActions(props: {
  controls: ChannelControls;
  sources: ConsoleSource[];
  keys: KeyInfo[];
}) {
  const { controls, sources } = props;
  return (
    <div className="control-toolbar-actions">
      <ChannelControlReset {...props} />
      {controls.drafts.size > 0 && (
        <>
          <Tip text="应用全部未保存修改，包括筛选隐藏的来源、API key 和模型范围。">
            <button
              className="button small primary"
              aria-label="应用全部临时修改"
              disabled={controls.pending}
              onClick={() => void controls.applyAll()}
            >
              {controls.pending ? <Spinner small /> : <Check size={13} />}
              {controls.pending ? "应用中…" : "应用"}
            </button>
          </Tip>
          <Tip text="放弃全部未保存修改，保留已生效规则。">
            <button
              className="button small"
              aria-label="放弃全部临时修改"
              disabled={controls.pending}
              onClick={controls.discardAll}
            >
              <X size={13} />
              放弃
            </button>
          </Tip>
        </>
      )}
      {!controls.pending && controls.draftIssues.length > 0 && (
        <small className="control-draft-error negative" role="alert">
          未应用的修改已保留：
          {[
            ...new Set(
              controls.draftIssues.map(
                (issue) =>
                  `${sources.find((source) => source.id === issue.source)?.name || issue.source}：${issue.message}`,
              ),
            ),
          ].join("；")}
        </small>
      )}
    </div>
  );
}
export function ChannelControlReset({
  controls,
  sources,
  keys,
}: {
  controls: ChannelControls;
  sources: ConsoleSource[];
  keys: KeyInfo[];
}) {
  const menu = useRef<HTMLDetailsElement>(null);
  useEffect(() => {
    const close = (event: PointerEvent) => {
      if (menu.current && !menu.current.contains(event.target as Node))
        menu.current.open = false;
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape" && menu.current?.open) {
        menu.current.open = false;
        menu.current.querySelector("summary")?.focus();
      }
    };
    document.addEventListener("pointerdown", close);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("pointerdown", close);
      document.removeEventListener("keydown", escape);
    };
  }, []);
  const scopes = controls.scopes
    .filter((scope) => !!scope.current)
    .map((scope) => {
      const rule = scope.current!;
      const sourceName =
        sources.find((source) => source.id === scope.source)?.name ||
        scope.source;
      const key = keys.find(
        (key) =>
          key.key_id === `${scope.source}::${rule.api_key_id}` ||
          (key.source_id === scope.source && key.key_id === rule.api_key_id),
      );
      const keyName = rule.api_key_id
        ? key
          ? `Key ${key.position} · ${key.prefix}`
          : "指定 API key（已不在目录）"
        : "全部 API key";
      const label = `${sourceName} / ${keyName} / ${rule.model || "全部模型"}`;
      const hasDraft = [...controls.drafts.values()].some(
        (draft) => draft.source === scope.source,
      );
      const disabled = scope.pending || hasDraft || scope.state.isError;
      const hint = hasDraft
        ? "有未应用修改，请先应用或放弃草稿。"
        : `撤销 ${label} 的渠道顺序和临时停用，包含被表格筛选隐藏的渠道。其他范围规则保留。`;
      return { scope, label, disabled, hint };
    });
  if (!scopes.length) return null;
  const reset = (source: string) => {
    if (menu.current) menu.current.open = false;
    void controls.reset({ source_id: source, provider: "" });
  };
  if (scopes.length === 1) {
    const { scope, label, disabled, hint } = scopes[0];
    return (
      <div className="control-reset-action">
        <Tip text={hint}>
          <button
            className="button small"
            aria-label={RESET_SCOPE_LABEL}
            disabled={disabled}
            onClick={() => reset(scope.source)}
            data-reset-scope={label}
          >
            {scope.pending ? <Spinner small /> : <RotateCcw size={14} />}
            <span>{RESET_SCOPE_LABEL}</span>
          </button>
        </Tip>
      </div>
    );
  }
  return (
    <div className="control-reset-action">
      <details ref={menu}>
        <summary className="button small" role="button">
          <RotateCcw size={14} />
          <span>{RESET_SCOPE_LABEL}</span>
          <ChevronDown size={12} />
        </summary>
        <div
          className="control-reset-menu"
          role="menu"
          aria-label="选择要撤销的范围"
        >
          {scopes.map(({ scope, label, disabled, hint }) => (
            <Tip key={scope.id} text={hint}>
              <button
                role="menuitem"
                disabled={disabled}
                onClick={() => reset(scope.source)}
              >
                {label}
              </button>
            </Tip>
          ))}
        </div>
      </details>
    </div>
  );
}

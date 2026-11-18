import { useEffect, useRef, useState } from "react";
import { useQueries, useQueryClient } from "@tanstack/react-query";
import {
  ArrowUpToLine,
  ChevronDown,
  RotateCcw,
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
  const present = new Set(providers);
  const arranged = new Set(order);
  return [
    ...order.filter((p) => present.has(p)),
    ...providers.filter((p) => !arranged.has(p)),
  ];
}

export function moveVisibleProvider(order: string[], visible: ReadonlySet<string>, provider: string, target: string, after: boolean) {
  if (provider === target || !visible.has(provider) || !visible.has(target)) return order;
  const current = order.filter(name => visible.has(name));
  if (!current.includes(provider) || !current.includes(target)) return order;
  const next = current.filter(name => name !== provider);
  next.splice(next.indexOf(target) + Number(after), 0, provider);
  if (next.every((name, index) => name === current[index])) return order;
  let index = 0;
  return order.map(name => visible.has(name) ? next[index++] : name);
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
    if (!i.ready || i.stale || locked.current) return false;
    const next = update(
      { ...i.rule, order: [...i.rule.order], disabled: [...i.rule.disabled] },
      i.order,
    );
    if (next.order.every((provider, index) => provider === i.rule.order[index]) && next.order.length === i.rule.order.length &&
        next.disabled.every((provider, index) => provider === i.rule.disabled[index]) && next.disabled.length === i.rule.disabled.length) return false;
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
    return true;
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
    const index = order.indexOf(row.provider);
    return index < 0 ? undefined : order[index + direction];
  }
  function moveRelative(row: Channel, target: ControlTarget, after: boolean, visible: Channel[]) {
    if (row.source_id !== target.source_id) return false;
    const names = new Set(visible.filter(r => r.source_id === row.source_id).map(r => r.provider));
    return edit(row, (rule, order) => {
      const next = moveVisibleProvider(order, names, row.provider, target.provider, after);
      return next === order ? rule : { ...rule, order: next };
    });
  }
  function move(row: Channel, direction: number, visible: Channel[]) {
    const target = neighbor(row, direction, visible);
    return target ? moveRelative(row, {source_id:row.source_id, provider:target}, direction > 0, visible) : false;
  }
  function moveToTop(row: Channel) {
    const i=info(row);
    if (!i.ready || i.pending || i.stale || i.order.indexOf(row.provider)<=0) return;
    edit(row, rule=>({...rule,order:[row.provider,...i.order.filter(provider=>provider!==row.provider)]}));
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
  function arrange(input: Channel[], appliedOnly = false) {
    const groups = new Map<string, Channel[]>();
    for (const row of input) {
      const src = row.source_id || "";
      const group = groups.get(src) || [];
      group.push(row);
      groups.set(src, group);
    }
    for (const group of groups.values()) {
      const i = info(group[0]);
      const rule = appliedOnly ? i.current : i.rule;
      if (!rule?.order.length) continue;
      const ranks = new Map(rule.order.map((p, n) => [p, n]));
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
    moveRelative,
    moveToTop,
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
export function ChannelRowActions({ row, controls, state, disabled }: {
  row: Channel;
  controls: ChannelControls;
  state: ReturnType<ChannelControls["info"]>;
  disabled: boolean;
}) {
  const label = `${row.source_name || row.source_id || ""} ${channelName(row)} ${row.model}`.trim();
  const stopped = state.rule.disabled.includes(row.provider) || state.inherited;
  return <div className="route-row-actions">
    <button type="button" className="icon-button route-pin" aria-label={`置顶 ${label}`} title="置顶到此来源的第一位"
      disabled={disabled || state.order[0] === row.provider} onClick={() => controls.moveToTop(row)}>
      <ArrowUpToLine size={14} aria-hidden="true" />
    </button>
    <label className="route-disable" title={state.inherited ? "从上级路由规则继承停用" : "临时停用"}>
      <input type="checkbox" aria-label={`临时停用 ${label}`} checked={stopped} disabled={disabled || state.inherited}
        onChange={event => controls.toggle(row, event.target.checked)} />
    </label>
  </div>;
}

export const RESET_SCOPE_LABEL = "撤销更改";
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

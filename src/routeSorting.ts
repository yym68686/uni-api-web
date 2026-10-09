import { useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ApiError, controlRequest } from "./api";
import type { Channel } from "./types";
import { channelName } from "./format";

export interface RouteSortChange {
  api_key_id: string;
  key_position: number;
  model: string;
  before: string[];
  after: string[];
  upstreams: Record<string, string>;
}
export interface RouteSortSource {
  source: string;
  name: string;
  receipt: string;
  changes: RouteSortChange[];
  revision: string;
}
export interface RouteSortPlan {
  sources: RouteSortSource[];
  labels: Record<string, string>;
  errors: string[];
}
type SortRecord = {
  sources: (RouteSortSource & {
    status: "pending" | "applied" | "undone" | "error" | "conflict";
    message?: string;
  })[];
  createdAt: number;
};
const storageKey = (scope: string) => `uni-console-route-sort-undo:v1:${scope}`;
const path = (source: string) =>
  `/v1/sources/${encodeURIComponent(source)}/channel-sort`;
const post = <T>(source: string, body: unknown, signal?: AbortSignal) =>
  controlRequest<T>(path(source), {
    method: "POST",
    signal: signal
      ? AbortSignal.any([signal, AbortSignal.timeout(120000)])
      : AbortSignal.timeout(120000),
    body: JSON.stringify(body),
  });

export function routeSortSelections(rows: Channel[]) {
  const sources = new Map<string, { model: string; providers: string[] }[]>();
  for (const row of rows) {
    if (!row.source_id) continue;
    const models = sources.get(row.source_id) || [];
    let model = models.find((m) => m.model === row.model);
    if (!model) {
      model = { model: row.model, providers: [] };
      models.push(model);
    }
    if (!model.providers.includes(row.provider))
      model.providers.push(row.provider);
    sources.set(row.source_id, models);
  }
  return sources;
}
export async function prepareRouteSorting(
  rows: Channel[],
  key: string,
  signal?: AbortSignal,
  labelRows: Channel[] = rows,
): Promise<RouteSortPlan> {
  const sources: RouteSortSource[] = [],
    errors: string[] = [];
  await Promise.all(
    [...routeSortSelections(rows)].map(async ([source, models]) => {
      const name =
        rows.find((r) => r.source_id === source)?.source_name || source;
      try {
        const response = await post<Omit<RouteSortSource, "source" | "name">>(
          source,
          { action: "prepare", api_key_id: key, models },
          signal,
        );
        if (
          !response.receipt ||
          !response.revision ||
          !Array.isArray(response.changes)
        )
          throw Error("来源未提供有效排序预览");
        sources.push({ ...response, source, name });
      } catch (e) {
        errors.push(`${name}：${e instanceof Error ? e.message : "预览失败"}`);
      }
    }),
  );
  sources.sort((a, b) => a.name.localeCompare(b.name));
  return {
    sources,
    errors,
    labels: Object.fromEntries(
      labelRows.map((r) => [
        JSON.stringify([r.source_id, r.provider]),
        channelName(r),
      ]),
    ),
  };
}
function loadRecord(scope: string): SortRecord | null {
  try {
    const value = JSON.parse(localStorage.getItem(storageKey(scope)) || "null");
    if (
      value &&
      Array.isArray(value.sources) &&
      value.sources.every(
        (s: RouteSortSource) =>
          typeof s.source === "string" &&
          typeof s.receipt === "string" &&
          Array.isArray(s.changes),
      )
    )
      return value;
  } catch {
    /* A missing receipt never authorizes a reverse write. */
  }
  return null;
}
export function useRouteSorting(scope: string) {
  const client = useQueryClient();
  const [record, setRecord] = useState<SortRecord | null>(() =>
    loadRecord(scope),
  );
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const locked = useRef(false);
  const pending = !!record?.sources.some(
    (s) => s.status !== "undone" && s.status !== "conflict",
  );
  const unresolved = !!record?.sources.some(
    (s) => s.status === "pending" || s.status === "error",
  );
  function persist(next: SortRecord) {
    localStorage.setItem(storageKey(scope), JSON.stringify(next));
    setRecord(next);
  }
  async function confirmSource(
    source: SortRecord["sources"][number],
    signal?: AbortSignal,
  ) {
    try {
      const result = await post<{ status: string; revision: string }>(
        source.source,
        {
          action: "status",
          receipt: source.receipt,
        },
        signal
          ? AbortSignal.any([signal, AbortSignal.timeout(15000)])
          : AbortSignal.timeout(15000),
      );
      if (
        !result.revision ||
        !["applied", "before", "conflict"].includes(result.status)
      )
        throw Error("未取得有效排序记录状态");
      if (result.status === "conflict") {
        source.status = "conflict";
        source.message = "当前路由已变化，请重新预览后应用。";
      } else {
        source.status = result.status === "applied" ? "applied" : "undone";
        source.message = undefined;
      }
    } catch (e) {
      // A rejected receipt cannot authorize undo. Network failures still need
      // confirmation and must not be discarded or replaced by another write.
      source.status =
        e instanceof ApiError && [400, 404].includes(e.status)
          ? "conflict"
          : "error";
      source.message = e instanceof Error ? e.message : "排序结果尚未确认";
    }
  }
  async function refresh(signal?: AbortSignal) {
    if (locked.current) return false;
    if (!record || record.sources.every((s) => s.status === "undone"))
      return true;
    const next = structuredClone(record);
    locked.current = true;
    setBusy(true);
    setError("");
    try {
      await Promise.all(
        next.sources
          .filter((s) => s.status !== "undone")
          .map((s) => confirmSource(s, signal)),
      );
      persist(next);
      return !next.sources.some(
        (s) => s.status === "error" || s.status === "pending",
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "排序记录核对失败");
      return false;
    } finally {
      locked.current = false;
      setBusy(false);
    }
  }
  async function execute(action: "apply" | "undo", plan?: RouteSortPlan) {
    if (locked.current) return;
    if (action === "apply" && (!plan || plan.errors.length || unresolved))
      return;
    const next: SortRecord =
      action === "apply"
        ? {
            createdAt: Date.now(),
            sources: plan!.sources
              .filter((s) => s.changes.length)
              .map((s) => ({ ...s, status: "pending" })),
          }
        : structuredClone(record!);
    if (!next?.sources.length) return;
    locked.current = true;
    setBusy(true);
    setError("");
    try {
      // Persist the server-authenticated recovery receipt before sending writes.
      try {
        persist(next);
      } catch {
        throw Error("无法保存撤回记录，请检查浏览器存储后重试；尚未提交修改。");
      }
      await Promise.all(
        next.sources.map(async (source) => {
          if (source.status === "undone" || source.status === "conflict")
            return;
          try {
            const result = await post<{ status: string; revision: string }>(
              source.source,
              { action, receipt: source.receipt },
            );
            if (
              !result.revision ||
              result.status !== (action === "apply" ? "applied" : "before")
            )
              throw Error("未取得有效应用结果");
            source.status = action === "apply" ? "applied" : "undone";
            source.message = undefined;
          } catch (e) {
            source.status = "error";
            source.message = e instanceof Error ? e.message : "操作结果未确认";
            // Read-only recovery after an ambiguous response; never retry a write.
            await confirmSource(source);
          }
          try {
            persist(structuredClone(next));
          } catch {
            setRecord(structuredClone(next));
          }
        }),
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "排序操作失败");
    } finally {
      locked.current = false;
      setBusy(false);
      await Promise.all(
        [
          "catalog",
          "control-catalog",
          "channel-controls",
          "channel-routes",
          "channel-management",
          "sub2api-imports",
          "control-persistence",
        ].map((name) => client.invalidateQueries({ queryKey: [name] })),
      );
    }
  }
  function dismiss() {
    if (busy) return;
    try {
      localStorage.removeItem(storageKey(scope));
    } catch {
      setError("无法清除本机撤回记录，请检查浏览器存储。");
      return;
    }
    setRecord(null);
    setError("");
  }
  return {
    record,
    busy,
    error,
    pending,
    unresolved,
    refresh,
    apply: (plan: RouteSortPlan) => execute("apply", plan),
    undo: () => (record ? execute("undo") : Promise.resolve()),
    dismiss,
  };
}
export type RouteSorting = ReturnType<typeof useRouteSorting>;

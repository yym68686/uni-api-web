import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, X, CircleHelp, Play, ScanLine } from "lucide-react";
import { controlRequest } from "./api";
import { providerId, time, channelName } from "./format";
import type { Channel } from "./types";
import { Spinner, Tip } from "./ui";

export interface ChannelCheck {
  source_id: string;
  provider: string;
  model: string;
  verdict: "pass" | "fail" | "inconclusive" | "error";
  text: string;
  message?: string;
  checked_at: number;
  duration_ms: number;
}
export function checkTargets(rows: Channel[]) {
  return [...new Map(rows.map((row) => [providerId(row), row])).values()];
}
export function useChannelChecks(session: string, enabled: boolean) {
  const client = useQueryClient();
  const queryKey = ["channel-checks", session];
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) =>
      controlRequest<{ data: ChannelCheck[] }>(
        "/v1/sources/all/channel-checks",
        { signal },
      ),
    enabled,
    staleTime: 30_000,
    retry: false,
  });
  const [pending, setPending] = useState<Set<string>>(new Set());
  const [errors, setErrors] = useState<Map<string, ChannelCheck>>(new Map());
  const [batch, setBatch] = useState<{ done: number; total: number } | null>(
    null,
  );
  const active = useRef(new Map<string, AbortController>());
  const controller = useRef(new AbortController());
  const batchController = useRef<AbortController | null>(null);
  useEffect(() => {
    const abort = new AbortController();
    controller.current = abort;
    return () => abort.abort();
  }, [session]);
  async function run(row: Channel, batchSignal?: AbortSignal) {
    const id = providerId(row),
      abort = new AbortController(),
      signal = AbortSignal.any([
        controller.current.signal,
        abort.signal,
        ...(batchSignal ? [batchSignal] : []),
      ]);
    if (active.current.has(id) || !row.source_id || !enabled || signal.aborted)
      return;
    active.current.set(id, abort);
    setPending(new Set(active.current.keys()));
    setErrors((old) => {
      const next = new Map(old);
      next.delete(id);
      return next;
    });
    try {
      await client.cancelQueries({ queryKey });
      signal.throwIfAborted();
      const result = await controlRequest<ChannelCheck>(
        `/v1/sources/${encodeURIComponent(row.source_id!)}/channel-checks`,
        {
          method: "POST",
          body: JSON.stringify({ provider: row.provider }),
          signal: AbortSignal.any([signal, AbortSignal.timeout(60_000)]),
        },
      );
      signal.throwIfAborted();
      client.setQueryData<{ data: ChannelCheck[] }>(queryKey, (previous) => ({
        data: [
          ...(previous?.data || []).filter((item) => providerId(item) !== id),
          result,
        ],
      }));
    } catch (error) {
      if (!signal.aborted)
        setErrors((old) =>
          new Map(old).set(id, {
            source_id: row.source_id!,
            provider: row.provider,
            model: "gpt-6-astra",
            verdict: "error",
            text: "",
            message: error instanceof Error ? error.message : "检测失败",
            checked_at: Date.now() / 1000,
            duration_ms: 0,
          }),
        );
    } finally {
      // A canceled request may settle after another check of this channel starts.
      if (active.current.get(id) === abort) {
        active.current.delete(id);
        if (!controller.current.signal.aborted)
          setPending(new Set(active.current.keys()));
      }
    }
  }
  async function runAll(rows: Channel[]) {
    if (batchController.current || active.current.size || !enabled) return;
    const queue = checkTargets(rows);
    const abort = new AbortController();
    batchController.current = abort;
    let done = 0;
    setBatch({ done, total: queue.length });
    try {
      await Promise.all(
        queue.map(async (row) => {
          await run(row, abort.signal);
          done++;
          if (
            batchController.current === abort &&
            !abort.signal.aborted &&
            !controller.current.signal.aborted
          )
            setBatch({ done, total: queue.length });
        }),
      );
    } finally {
      if (batchController.current === abort) {
        batchController.current = null;
        if (!controller.current.signal.aborted) setBatch(null);
      }
    }
  }
  const results = new Map(
    (query.data?.data || []).map((item) => [providerId(item), item]),
  );
  for (const [id, item] of errors) {
    if (item.checked_at > (results.get(id)?.checked_at || 0))
      results.set(id, item);
  }
  return {
    results,
    pending,
    batch,
    run,
    runAll,
    stop: () => {
      batchController.current?.abort();
      batchController.current = null;
      for (const abort of active.current.values()) abort.abort();
      active.current.clear();
      setPending(new Set());
      setBatch(null);
    },
    error: query.error,
    loading: query.isPending,
    refetch: query.refetch,
  };
}
export type Checks = ReturnType<typeof useChannelChecks>;
export function CheckVerdict({ result }: { result: ChannelCheck }) {
  return (
    <span className={`check-status ${result.verdict}`}>
      {result.verdict === "pass" ? (
        <Check size={18} />
      ) : result.verdict === "fail" ? (
        <X size={18} />
      ) : (
        <CircleHelp size={17} />
      )}
      {
        {
          pass: "不降智",
          fail: "降智",
          inconclusive: "无法判定",
          error: "检测失败",
        }[result.verdict]
      }
    </span>
  );
}
export function LatestChannelCheck({
  row,
  checks,
}: {
  row: Channel;
  checks: Checks;
}) {
  const id = providerId(row),
    result = checks.results.get(id),
    pending = checks.pending.has(id);
  return (
    <td className="latest-channel-check">
      {result ? (
        <>
          <Tip
            text={
              <>
                <div>
                  最近检测：
                  {new Date(result.checked_at * 1000).toLocaleString("zh-CN", {
                    hour12: false,
                  })}
                </div>
                <div>检测模型：{result.model}</div>
                <div>{result.text || result.message || "未返回有效回复"}</div>
              </>
            }
          >
            <CheckVerdict result={result} />
          </Tip>
          <small className="muted">{time(result.checked_at)}</small>
        </>
      ) : (
        !pending && (
          <span className="muted">
            {checks.loading ? "读取中…" : checks.error ? "读取失败" : "未检测"}
          </span>
        )
      )}
      {pending && (
        <small className="check-status muted">
          <Spinner small />
          检测中{result ? " · 上次结果" : ""}
        </small>
      )}
      {result && checks.error && (
        <small className="negative">更新失败 · 上次结果</small>
      )}
    </td>
  );
}
export function CheckActions({
  checks,
  rows,
  disabled,
  expanded,
  onExpandedChange,
}: {
  checks: Checks;
  rows: Channel[];
  disabled: boolean;
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
}) {
  if (!expanded)
    return (
      <button className="button small" onClick={() => onExpandedChange(true)}>
        <ScanLine size={15} />
        降智检测
      </button>
    );
  return (
    <div className="check-toolbar-actions">
      {checks.batch && (
        <span className="muted" role="status">
          {checks.batch.done} / {checks.batch.total}
        </span>
      )}
      <button
        className="button primary small"
        aria-label={`一键检测 · ${rows.length}`}
        aria-busy={!!checks.batch}
        disabled={
          disabled || !!checks.batch || checks.pending.size > 0 || !rows.length
        }
        onClick={() => void checks.runAll(rows)}
      >
        {checks.batch ? <Spinner small /> : <ScanLine size={16} />}
        一键检测 · {rows.length}
      </button>
      <button
        className="button small"
        onClick={() => {
          checks.stop();
          onExpandedChange(false);
        }}
      >
        <X size={14} />
        取消检测
      </button>
    </div>
  );
}
export function ChannelCheckAction({
  row,
  checks,
}: {
  row: Channel;
  checks: Checks;
}) {
  const pending = checks.pending.has(providerId(row));
  return (
    <td>
      <button
        className="button small"
        disabled={!row.source_id || pending || !!checks.batch}
        onClick={() => void checks.run(row)}
        aria-label={`重新检测 ${row.source_name || row.source_id || ""} ${channelName(row)} ${row.model}`}
      >
        {pending ? <Spinner small /> : <Play size={13} />}
        {pending ? "检测中…" : "重新检测"}
      </button>
    </td>
  );
}

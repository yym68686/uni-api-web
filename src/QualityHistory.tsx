import { useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
import { rate, ms } from "./format";
import { Spinner, Tip } from "./ui";

export interface QualitySummary {
  total: number;
  successful: number;
  passed: number;
}
export function QualityProbability({
  history,
  checkedAt,
  origin,
}: {
  history?: QualitySummary;
  checkedAt?: number;
  origin?: string;
}) {
  if (!history || history.total === 0) return null;
  const checked = checkedAt
    ? new Date(checkedAt * 1000).toLocaleString("zh-CN", { hour12: false })
    : "";
  return (
    <Tip
      text={`不降智次数 ÷ 成功检测次数：${history.passed} / ${history.successful}。累计 ${history.total} 次记录；失败、超时、取消及未执行的检测不计入分母。${checked ? ` 最近检测：${checked}${origin ? ` · ${origin}` : ""}` : ""}`}
    >
      <small className="quality-probability">
        {rate(history.successful ? history.passed / history.successful : null)}
      </small>
    </Tip>
  );
}
interface HistoryEntry {
  id: string;
  model: string;
  rule: string;
  checked_at: number;
  verdict: string;
  successful: boolean;
  result: {
    text?: string;
    message?: string;
    duration_ms?: number;
    quality?: { text?: string; message?: string; duration_ms?: number; response_model?: string; http_status?: number };
  };
}
interface HistoryPage { data: HistoryEntry[]; summary: QualitySummary; next: string }
const labels: Record<string, string> = {
  pass: "不降智", fail: "降智", inconclusive: "无法判定", error: "检测失败",
  running: "检测中", interrupted: "已中断", skipped: "未执行", cancelled: "已取消",
};
export function QualityHistory({ path, revision, title = "降智检测历史" }: { path: string; revision?: string | number; title?: string }) {
  const [expanded, setExpanded] = useState(false);
  const query = useInfiniteQuery({
    queryKey: ["quality-history", path, revision],
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) => controlRequest<HistoryPage>(path + (path.includes("?") ? "&" : "?") + new URLSearchParams({ before: pageParam }), { signal }),
    getNextPageParam: (page) => page.next || undefined,
    enabled: expanded,
    retry: false,
    staleTime: 5000,
    refetchInterval: expanded ? 15000 : false,
  });
  const entries = query.data?.pages.flatMap((page) => page.data) || [];
  return (
    <section className="quality-history">
      <button className="button small" aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>{expanded ? "收起" : "查看"}{title}</button>
      {expanded && <>
        {query.data && <QualityProbability history={query.data.pages[0].summary} />}
        {query.isPending ? <p className="muted"><Spinner small /> 正在读取历史…</p> : query.isError ? <p role="alert">历史读取失败 <button className="button small" onClick={() => void query.refetch()}>重试</button></p> : !entries.length ? <p className="muted">暂无检测记录。</p> : (
          <div className="quality-history-scroll">
            <table className="quality-history-table" aria-label={title}>
              <thead><tr><th>检测时间</th><th>结果</th><th>回复 / 诊断</th></tr></thead>
              <tbody>{entries.map((entry) => {
                const probe = entry.result.quality || entry.result;
                return <tr key={entry.id}>
                  <td><time>{new Date(entry.checked_at * 1000).toLocaleString("zh-CN", { hour12: false })}</time>{entry.rule === "legacy" && <small className="muted">更新前最后一次</small>}</td>
                  <td><span className={`check-status ${entry.verdict}`}>{labels[entry.verdict] || "检测失败"}</span><small className="muted">{ms(probe.duration_ms)}</small></td>
                  <td>{probe.text || probe.message ? <details><summary>查看内容</summary><dl><dt>检测模型</dt><dd>{entry.model}</dd>{entry.result.quality?.response_model && <><dt>响应模型</dt><dd>{entry.result.quality.response_model}</dd></>}<dt>回复 / 诊断</dt><dd className="sub-check-reply">{probe.text || probe.message}</dd></dl></details> : "—"}</td>
                </tr>;
              })}</tbody>
            </table>
          </div>
        )}
        {query.hasNextPage && <button className="button small" disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>{query.isFetchingNextPage ? "读取中…" : "加载更早记录"}</button>}
      </>}
    </section>
  );
}

import type { RouteSorting } from "./routeSorting";

export function RouteSortReceipt({ routing }: { routing: RouteSorting }) {
  return (
    <>
      {routing.error && (
        <p role="alert" className="negative">
          {routing.error}
        </p>
      )}
      {routing.record && (
        <section className="route-sort-receipt" aria-label="真实排序应用结果">
          {routing.record.sources.map((source) => (
            <p
              key={source.source}
              role={source.status === "error" ? "alert" : "status"}
            >
              {source.name}：
              {source.status === "applied"
                ? "已应用到真实路由"
                : source.status === "undone"
                  ? "已恢复应用前顺序"
                  : source.status === "pending"
                    ? "结果待确认"
                    : "未确认或配置已变化"}
              {source.message && ` · ${source.message}`}
            </p>
          ))}
          <button
            className="button small"
            disabled={routing.busy || !routing.pending}
            onClick={() => void routing.undo()}
          >
            撤回排序
          </button>
          <button
            className="button small ghost"
            disabled={routing.busy}
            onClick={routing.dismiss}
            title="仅清除此浏览器中的撤回记录，不修改路由"
          >
            放弃撤回记录
          </button>
        </section>
      )}
    </>
  );
}

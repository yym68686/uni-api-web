import { Check, RefreshCw, RotateCcw, X } from "lucide-react";
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
          <div className="route-sort-receipt-status">
            {routing.record.sources.map((source) => (
              <p
                key={source.source}
                role={source.status === "error" ? "alert" : "status"}
              >
                {source.status === "applied" && <Check size={13} />}
                {source.name}：
                {source.status === "applied"
                  ? "已应用到真实路由"
                  : source.status === "undone"
                    ? "已恢复应用前顺序"
                    : source.status === "pending"
                      ? "结果待确认"
                      : source.status === "conflict"
                        ? "旧排序记录已失效"
                        : "排序结果尚未确认"}
                {source.message && ` · ${source.message}`}
              </p>
            ))}
          </div>
          <div className="route-sort-receipt-actions">
            <button
              className="icon-button"
              aria-label="核对排序记录"
              title="核对排序记录"
              disabled={routing.busy}
              onClick={() => void routing.refresh()}
            >
              <RefreshCw
                size={14}
                className={routing.busy ? "spin" : undefined}
              />
            </button>
            <button
              className="button small ghost"
              disabled={routing.busy || !routing.pending}
              onClick={() => void routing.undo()}
            >
              <RotateCcw size={13} />
              撤回排序
            </button>
            <button
              className="icon-button"
              aria-label="放弃撤回记录"
              disabled={routing.busy}
              onClick={routing.dismiss}
              title="仅清除此浏览器中的撤回记录，不修改路由"
            >
              <X size={14} />
            </button>
          </div>
        </section>
      )}
    </>
  );
}

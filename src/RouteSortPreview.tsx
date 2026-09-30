import type { Channel } from "./types";
import type { RouteSortPlan, RouteSorting } from "./routeSorting";
import { channelName } from "./format";

export function RouteSortPreview({
  plan,
  rows,
}: {
  plan: RouteSortPlan | null;
  rows: Channel[] | null;
}) {
  if (!rows) return null;
  return (
    <section className="route-sort-preview" aria-label="请求顺序预览">
      <h3>请求顺序预览</h3>
      {plan?.errors.map((error) => (
        <p role="alert" className="negative" key={error}>
          {error}
        </p>
      ))}
      {plan ? (
        plan.sources.map((source) => (
          <div key={source.source}>
            <h4>
              {source.name} · {source.changes.length} 个 API key / 模型范围
            </h4>
            {source.changes.map((change) => (
              <details
                key={`${change.api_key_id}:${change.model}`}
                open={plan.sources.length === 1 && source.changes.length === 1}
              >
                <summary>
                  Key {change.key_position} · {change.model} ·{" "}
                  {change.after.filter((p, i) => p !== change.before[i]).length}{" "}
                  个位置变更
                </summary>
                <ol>
                  {change.after.map((provider, index) => (
                    <li key={provider}>
                      <span>
                        {plan.labels[
                          JSON.stringify([source.source, provider])
                        ] || provider}
                      </span>
                      <small>
                        {change.before.indexOf(provider) + 1} → {index + 1}
                      </small>
                    </li>
                  ))}
                </ol>
              </details>
            ))}
          </div>
        ))
      ) : (
        <ol>
          {rows.map((row, i) => (
            <li key={`${row.source_id}:${row.provider}:${row.model}:${i}`}>
              <span>
                {channelName(row)} · {row.model}
              </span>
              <small>{i + 1}</small>
            </li>
          ))}
        </ol>
      )}
      {plan &&
        !plan.errors.length &&
        !plan.sources.some((s) => s.changes.length) && (
          <p>实际请求顺序已与此方案一致，无需应用。</p>
        )}
    </section>
  );
}
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

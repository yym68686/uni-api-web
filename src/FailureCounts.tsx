import type { Channel } from "./types";
import { count } from "./format";

const labels: Record<string, string> = {
  responses_max_output_tokens: "输出达到上限",
  responses_content_filter: "内容过滤",
  responses_incomplete: "响应未完成",
  missing_response_completed: "缺少完成事件",
  protocol_error: "响应协议错误",
  transport_error: "上游传输错误",
  upstream_response_failed: "上游返回失败事件",
  upstream_http_error: "上游 HTTP 错误",
  downstream_disconnected: "下游连接中断",
  no_successful_channel: "无成功渠道",
  other: "其他失败",
  unknown: "历史失败／原因未记录",
};

export function FailureCounts({
  channels,
  summary = false,
}: {
  channels: Channel[];
  summary?: boolean;
}) {
  if (channels.some((channel) => !channel.stats)) {
    return <span>失败统计暂不可用</span>;
  }
  const totals: Record<string, number> = {};
  for (const channel of channels) {
    const reasons = channel.stats?.failure_reasons || {};
    let classified = 0;
    for (const [reason, n] of Object.entries(reasons)) {
      if (!Number.isFinite(n) || n <= 0) continue;
      totals[reason] = (totals[reason] || 0) + n;
      classified += n;
    }
    const missing = Math.max(0, (channel.stats?.failed || 0) - classified);
    if (missing) totals.unknown = (totals.unknown || 0) + missing;
  }
  const entries = Object.entries(totals).sort(
    (a, b) => b[1] - a[1] || a[0].localeCompare(b[0]),
  );
  return (
    <section className="failure-counts" aria-label="失败原因次数">
      {summary && (
        <dl className="attempt-counts">
          <div>
            <dt>成功</dt>
            <dd>
              {count(channels.reduce((n, c) => n + (c.stats.success || 0), 0))}{" "}
              次
            </dd>
          </div>
          <div>
            <dt>失败</dt>
            <dd>
              {count(channels.reduce((n, c) => n + (c.stats.failed || 0), 0))}{" "}
              次
            </dd>
          </div>
        </dl>
      )}
      <h4>
        失败原因{" "}
        <span className="muted">
          {count(entries.reduce((n, [, value]) => n + value, 0))} 次
        </span>
      </h4>
      {entries.length ? (
        <dl>
          {entries.map(([reason, n]) => (
            <div key={reason}>
              <dt>
                {labels[reason] ||
                  (/^upstream_http_[45]\d\d$/.test(reason)
                    ? `上游 HTTP ${reason.slice(-3)}`
                    : "其他失败")}
              </dt>
              <dd>{count(n)} 次</dd>
            </div>
          ))}
        </dl>
      ) : (
        <p className="muted">当前范围暂无失败记录</p>
      )}
    </section>
  );
}

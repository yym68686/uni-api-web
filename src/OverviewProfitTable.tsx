import { channelObservationName } from "./channelBilling";
import { overviewProfitAmount, type OverviewChannelProfit } from "./overviewProfit";
import type { Channel } from "./types";

export function OverviewProfitTable({ title, entries, empty, showSource, onChannel }: {
  title: string;
  entries: OverviewChannelProfit[];
  empty: string;
  showSource: boolean;
  onChannel: (row: Channel) => void;
}) {
  return (
    <section className="overview-panel" aria-label={title}>
      <header className="overview-panel-header"><h3>{title}</h3></header>
      <table className="overview-table overview-profit-table">
        <thead><tr><th scope="col">渠道 / 模型</th><th scope="col">利润参考</th></tr></thead>
        <tbody>{entries.length ? entries.map(entry => {
          const name = channelObservationName(entry.row);
          const model = entry.row.model;
          const source = showSource ? entry.row.source_name || entry.row.source_id : "";
          const label = [name, model, source].filter(Boolean).join(" · ");
          return <tr key={entry.id}>
            <th scope="row">
              <button type="button" className="overview-link overview-profit-link"
                aria-label={`在渠道观测筛选 ${label}`} title={label} onClick={() => onChannel(entry.row)}>
                <span>{name}</span>
                <small>{[model, source].filter(Boolean).join(" · ")}</small>
              </button>
            </th>
            <td className={entry.amount < 0 ? "negative" : entry.provisional ? "profit-provisional" : "profit-positive"}>
              {overviewProfitAmount(entry.amount)}
              {entry.provisional && <small className="overview-profit-basis">暂计</small>}
            </td>
          </tr>;
        }) : <tr><td colSpan={2} className="overview-empty">{empty}</td></tr>}</tbody>
      </table>
    </section>
  );
}

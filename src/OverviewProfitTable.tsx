import { channelObservationName } from "./channelBilling";
import { overviewProfitAmount, type OverviewChannelProfit } from "./overviewProfit";
import type { Channel } from "./types";

export function OverviewProfitTable({ title, entries, empty, showSource, current, onChannel }: {
  title: string;
  entries: OverviewChannelProfit[];
  empty: string;
  showSource: boolean;
  current: ReadonlyMap<string, Channel>;
  onChannel: (row: Channel) => void;
}) {
  return (
    <section className="overview-panel" aria-label={title}>
      <header className="overview-panel-header"><h3>{title}</h3></header>
      <table className="overview-table overview-profit-table">
        <thead><tr><th scope="col">渠道</th><th scope="col">利润参考</th></tr></thead>
        <tbody>{entries.length ? entries.map(entry => {
          const target = current.get(entry.id);
          const name = channelObservationName(entry.row);
          return <tr key={entry.id}>
            <th scope="row">
              {target
                ? <button type="button" className="overview-link" title={name} onClick={() => onChannel(target)}>{name}</button>
                : <span className="overview-link" title={name}>{name}</span>}
              {showSource && <small className="overview-profit-source" title={entry.row.source_name}>{entry.row.source_name || entry.row.source_id}</small>}
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

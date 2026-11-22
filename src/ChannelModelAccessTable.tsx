import { useState } from "react";
import type { ReactNode } from "react";
import type { CatalogModel } from "./ChannelModelCatalog";

type ModelAccessRow = {
  upstream: string;
  catalog?: CatalogModel;
  names: string[];
  canEnable: boolean;
  canRename: boolean;
  status: ReactNode;
  checking: boolean;
};
const pageSize = 40;

export function ChannelModelAccessTable({ rows, disabled, routePending, protocol, position, onToggle, onRename, onAlias, onRemoveAlias, onProbe }: {
  rows: ModelAccessRow[];
  disabled: boolean;
  routePending: boolean;
  protocol?: (upstream: string) => ReactNode;
  position: (name: string) => ReactNode;
  onToggle: (row: ModelAccessRow, checked: boolean) => void;
  onRename: (upstream: string, index: number, name: string) => void;
  onAlias: (upstream: string) => void;
  onRemoveAlias: (upstream: string, index: number) => void;
  onProbe: (model: string) => void;
}) {
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState("all");
  const [page, setPage] = useState(1);
  const filtered = rows.filter(row => {
    if (!routePending && ((filter === "attached" && !row.names.length) || (filter === "detached" && row.names.length))) return false;
    const term = search.trim().toLowerCase();
    return !term || row.upstream.toLowerCase().includes(term) || row.names.some(name => name.toLowerCase().includes(term));
  });
  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  const currentPage = Math.min(page, pageCount);
  const attached = rows.filter(row => row.names.length).length;
  return <section className="channel-model-access" aria-label="模型目录与接入">
    <div className="channel-model-access-filters">
      <input aria-label="搜索上游模型或公开名" placeholder="搜索模型…" value={search}
        onChange={e => { setSearch(e.target.value); setPage(1); }} />
      <select aria-label="按接入状态筛选" value={filter} disabled={routePending}
        onChange={e => { setFilter(e.target.value); setPage(1); }}>
        <option value="all">全部 ({rows.length})</option>
        <option value="attached">已勾选 ({attached})</option>
        <option value="detached">未勾选 ({rows.length - attached})</option>
      </select>
    </div>
    <div className="channel-model-access-scroll">
      <table>
        <thead><tr><th scope="col">上游模型</th><th scope="col">接入本 Key</th><th scope="col">公开模型名</th><th scope="col">路由位置</th><th scope="col">上游端点</th></tr></thead>
        <tbody>
          {filtered.slice((currentPage - 1) * pageSize, currentPage * pageSize).map(row => <tr key={row.upstream}>
            <th scope="row">
              <code>{row.upstream}</code>
              <small className="muted">
                {row.catalog?.manual ? "手工添加" : row.catalog?.observed ? "上游返回" : row.catalog ? "本次未返回" : "现有模型"}
                {row.catalog?.manual && row.catalog.observed ? " · 上游返回" : ""}
                {" · "}{row.status}
              </small>
              <button type="button" className="button small" aria-label={`检测模型 ${row.upstream}`}
                onClick={() => onProbe(row.upstream)} disabled={disabled || row.checking}>
                {row.checking ? "检测中…" : "检测模型"}
              </button>
            </th>
            <td><input type="checkbox" aria-label={`接入 ${row.upstream}`} checked={row.names.length > 0}
              disabled={disabled || routePending || (!row.names.length && !row.canEnable)}
              onChange={e => onToggle(row, e.target.checked)} /></td>
            <td>{row.names.length ? <>
              {row.names.map((name, index) => <div className="channel-model-public-name" key={index}>
                <input aria-label={`${row.upstream} 公开名 ${index + 1}`} maxLength={256} value={name}
                  disabled={disabled || routePending || !row.canRename}
                  onChange={event => onRename(row.upstream, index, event.target.value)} />
                {row.names.length > 1 && <button type="button" className="button small ghost"
                  aria-label={`移除 ${row.upstream} 的公开名 ${index + 1}`}
                  disabled={disabled || routePending} onClick={() => onRemoveAlias(row.upstream, index)}>移除</button>}
              </div>)}
              <button type="button" className="button small ghost" disabled={disabled || routePending || !row.canRename}
                onClick={() => onAlias(row.upstream)}>添加别名</button>
            </> : "—"}</td>
            <td>{row.names.length ? row.names.map((name, i) => <div className="channel-model-position" key={i}>{position(name)}</div>) : "—"}</td>
            <td>{row.names.length ? protocol?.(row.upstream) || "沿用渠道默认端点" : "—"}</td>
          </tr>)}
          {!filtered.length && <tr><td colSpan={5} className="muted">没有匹配的模型</td></tr>}
        </tbody>
      </table>
    </div>
    {pageCount > 1 && <div className="channel-model-access-filters" aria-label="模型目录分页">
      <button type="button" className="button small" disabled={currentPage === 1} onClick={() => setPage(currentPage - 1)}>上一页</button>
      <span>{currentPage} / {pageCount} 页</span>
      <button type="button" className="button small" disabled={currentPage === pageCount} onClick={() => setPage(currentPage + 1)}>下一页</button>
    </div>}
  </section>;
}

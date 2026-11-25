import { useId, useRef, useState } from "react";
import type { ReactNode } from "react";
import { ChevronDown, ChevronLeft, ChevronRight, FlaskConical, Info, Plus, Search, X } from "./icons";
import * as Tooltip from "@radix-ui/react-tooltip";
import { Spinner } from "./ui";

type ModelAccessRow = {
  upstream: string;
  names: string[];
  savedNames: string[];
  canEnable: boolean;
  canRename: boolean;
  status: string;
  failureReason?: string;
  tone: "idle" | "success" | "error";
  diagnostics: ReactNode;
  checking: boolean;
};
const pageSize = 40;

export function ChannelModelAccessTable({ rows, disabled, routePending, loading, toolbar, feedback, protocol, position, onToggle, onRename, onAlias, onRemoveAlias, onProbe }: {
  rows: ModelAccessRow[];
  disabled: boolean;
  routePending: boolean;
  loading: boolean;
  toolbar?: ReactNode;
  feedback?: ReactNode;
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
  const [expanded, setExpanded] = useState<string>();
  const scroll = useRef<HTMLDivElement>(null);
  const id = useId();
  function goToPage(page: number) {
    setPage(page);
    if (scroll.current) scroll.current.scrollTop = 0;
  }
  const term = search.trim().toLowerCase();
  const filtered = rows.filter(row => {
    if (filter === "saved" && !row.savedNames.length) return false;
    if ((filter === "attached" && !row.names.length) || (filter === "detached" && row.names.length)) return false;
    const names = filter === "saved" ? row.savedNames : row.names;
    return !term || row.upstream.toLowerCase().includes(term) || names.some(name => name.toLowerCase().includes(term));
  });
  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  const currentPage = Math.min(page, pageCount);
  const savedCount = rows.filter(row => row.savedNames.length).length;
  const checkedCount = rows.filter(row => row.names.length).length;
  return <section className="channel-model-access" aria-label="模型目录与接入">
    {toolbar}
    <div className="channel-model-filters">
      <label className="channel-model-search"><Search size={15} aria-hidden="true" />
        <input type="search" aria-label="搜索上游模型或公开名" placeholder="搜索模型" value={search}
          onChange={e => { setSearch(e.target.value); goToPage(1); }} />
      </label>
      <select aria-label="按接入状态筛选" value={filter}
        onChange={e => { setFilter(e.target.value); goToPage(1); }}>
        <option value="all">全部 ({rows.length})</option>
        <option value="saved">已接入 ({savedCount})</option>
        <option value="attached">已勾选 ({checkedCount})</option>
        <option value="detached">未勾选 ({rows.length - checkedCount})</option>
      </select>
    </div>
    <div className="channel-model-list" aria-busy={loading}>
      <div className="channel-model-access-scroll" ref={scroll}>
        <div className="channel-model-columns" aria-hidden="true"><span>上游模型</span><span>检测</span><span>接入</span><span /></div>
        <div className="channel-model-list-feedback">{feedback}</div>
        <ul className="channel-model-rows">
          {filtered.slice((currentPage - 1) * pageSize, currentPage * pageSize).map((row, rowIndex) => {
            const open = expanded === row.upstream;
            const attached = row.names.length > 0;
            const detailsId = `${id}-${rowIndex}`;
            return <li key={row.upstream} className={open ? "is-expanded" : undefined}>
              <div className="channel-model-row">
                <button type="button" className="channel-model-name" title={row.upstream}
                  aria-label={`配置 ${row.upstream}`} aria-expanded={open} aria-controls={open ? detailsId : undefined}
                  onClick={() => setExpanded(open ? undefined : row.upstream)}>
                  <code>{row.upstream}</code>
                  <ChevronDown size={16} aria-hidden="true" />
                </button>
                <button type="button" className={`settings-icon-button channel-model-probe is-${row.tone}`}
                  aria-label={`检测模型 ${row.upstream}`} aria-describedby={`${detailsId}-status`}
                  title={row.checking ? "检测中" : row.status}
                  onClick={() => onProbe(row.upstream)} disabled={disabled || row.checking}>
                  {row.checking ? <Spinner small /> : <FlaskConical size={16} />}
                </button>
                <span id={`${detailsId}-status`} className="sr-only">{row.checking ? "检测中" : row.status}</span>
                <label className="channel-model-toggle">
                  <input type="checkbox" aria-label={`接入 ${row.upstream}`} checked={attached}
                    disabled={disabled || routePending || (!attached && !row.canEnable)}
                    onChange={e => onToggle(row, e.target.checked)} />
                </label>
              </div>
              {open && <div id={detailsId} className="channel-model-detail">
                <div className="channel-model-result"><span className={`channel-model-dot is-${row.tone}`} />
                  <span>{row.checking ? "检测中" : row.status}</span>
                  {!row.checking && row.failureReason && <Tooltip.Root>
                    <Tooltip.Trigger asChild>
                      <button type="button" className="channel-model-failure-info" aria-label="查看检测失败原因">
                        <Info size={13} aria-hidden="true" />
                      </button>
                    </Tooltip.Trigger>
                    <Tooltip.Portal>
                      <Tooltip.Content className="tooltip" sideOffset={6} collisionPadding={12}>
                        {row.failureReason}
                        <Tooltip.Arrow className="tooltip-arrow" />
                      </Tooltip.Content>
                    </Tooltip.Portal>
                  </Tooltip.Root>}
                  {row.diagnostics}</div>
                {attached && <>
                  <div className="channel-model-mapping-labels"><span>公开模型名</span><span>路由位置</span><span /></div>
                  {row.names.map((name, index) => <div className="channel-model-mapping" key={index}>
                    <input aria-label={`${row.upstream} 公开名 ${index + 1}`} maxLength={256} value={name}
                      disabled={disabled || routePending || !row.canRename}
                      onChange={event => onRename(row.upstream, index, event.target.value)} />
                    <div>{position(name)}</div>
                    {row.names.length > 1 && <button type="button" className="settings-icon-button is-destructive"
                      aria-label={`移除 ${row.upstream} 的公开名 ${index + 1}`}
                      disabled={disabled || routePending} onClick={() => onRemoveAlias(row.upstream, index)}><X size={14} /></button>}
                  </div>)}
                  <button type="button" className="button small ghost channel-model-alias" disabled={disabled || routePending || !row.canRename}
                    onClick={() => onAlias(row.upstream)}><Plus size={14} />添加别名</button>
                  <div className="channel-model-endpoint"><span>上游端点</span>{protocol?.(row.upstream) || <span>渠道默认</span>}</div>
                </>}
              </div>}
            </li>;
          })}
        </ul>
        {!filtered.length && <div className="channel-model-empty">{loading ? <Spinner /> : term ? "没有匹配的模型" : filter === "saved" ? "暂无已接入模型" : "暂无模型"}</div>}
      </div>
    </div>
    <div className="channel-model-pagination" aria-label="模型目录分页">
      <span>{filtered.length} 个模型</span>
      <div hidden={pageCount < 2}>
        <button type="button" className="settings-icon-button" aria-label="上一页" disabled={currentPage === 1} onClick={() => goToPage(currentPage - 1)}><ChevronLeft size={16} /></button>
        <span>{currentPage} / {pageCount}</span>
        <button type="button" className="settings-icon-button" aria-label="下一页" disabled={currentPage === pageCount} onClick={() => goToPage(currentPage + 1)}><ChevronRight size={16} /></button>
      </div>
    </div>
  </section>;
}

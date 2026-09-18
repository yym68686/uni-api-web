import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { Plus, Save, SlidersHorizontal } from "lucide-react";
import { analyticsRequest } from "./api";
import type { Connection, ModelPrice } from "./types";
import { Spinner } from "./ui";

const fields = [
  ["input", "输入"], ["output", "输出"], ["cache_read", "缓存读取"],
  ["cache_write", "缓存写入 · 5 分钟"], ["cache_write_1h", "缓存写入 · 1 小时"],
] as const;

function PriceRow({ price, connection, onSaved }: { price: ModelPrice; connection: Connection; onSaved: () => void }) {
  const [draft, setDraft] = useState(price);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const change = (patch: Partial<ModelPrice>) => { setDraft(value => ({ ...value, ...patch })); setSaved(false); };
  async function save() {
    if (pending) return;
    setPending(true); setError(""); setSaved(false);
    try {
      const next = { ...draft, source: "manual" };
      await analyticsRequest(connection, `/analytics/v1/prices/${encodeURIComponent(price.model)}`, undefined, {
        method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(next),
      });
      setSaved(true); onSaved();
    } catch (error) { setError(error instanceof Error ? error.message : "保存失败"); }
    finally { setPending(false); }
  }
  return <article className="price-editor">
    <header><strong>{price.model}</strong><span className="muted">{price.source === "fact-discovered" ? "等待设置价格" : price.source || "自定义"}</span></header>
    <div className="price-inputs">{fields.map(([field, label]) => <label key={field}>{label}<input aria-label={`${price.model} ${label}价格`} type="number" min="0" max="1000000000" step="any" required value={draft[field]} disabled={pending} onChange={event => change({ [field]: event.target.valueAsNumber })} /></label>)}</div>
    <footer><label className="price-verified"><input type="checkbox" checked={!!draft.verified} disabled={pending} onChange={event => change({ verified: event.target.checked })} />确认价格并用于估算</label><button className="button small" disabled={pending || fields.some(([field]) => !Number.isFinite(draft[field]) || draft[field] < 0)} onClick={() => void save()}>{pending ? <Spinner small /> : <Save size={14} />} {saved ? "已保存" : "保存价格"}</button></footer>
    {error && <p role="alert" className="negative">{error}</p>}
  </article>;
}

export function PriceSettings({ prices, loading, error, connection, onSaved, refreshAction }: { prices: ModelPrice[]; loading: boolean; error?: string; connection: Connection; onSaved: () => void; refreshAction?: ReactNode }) {
  const [added, setAdded] = useState<ModelPrice[]>([]);
  const [name, setName] = useState("");
  const [filter, setFilter] = useState("");
  useEffect(() => setAdded(items => items.filter(item => !prices.some(price => price.model === item.model))), [prices]);
  const all = [...prices, ...added];
  const trimmed = name.trim();
  function add() { if (!trimmed || all.some(price => price.model === trimmed)) return; setAdded(items => [...items, {model: trimmed,input:0,output:0,cache_read:0,cache_write:0,cache_write_1h:0,source:"manual",verified:false}]); setName(""); }
  return <section className="data-panel prices-panel">
    <div className="data-heading"><div className="data-title"><SlidersHorizontal size={19} /><h2>模型价格</h2></div>{loading && <Spinner small />}</div>
    <p className="field-note">美元 / 百万 token。确认价格后用于当前价格估算，实际渠道扣费以渠道账单为准。未定价模型显示暂无费用。</p>
    <form className="price-add" onSubmit={event => { event.preventDefault(); add(); }}><input aria-label="新增模型名称" placeholder="输入模型名称" value={name} maxLength={512} onChange={event => setName(event.target.value)} /><button className="button small" disabled={!trimmed || all.some(price => price.model === trimmed)}><Plus size={14} />新增模型</button></form>
    <div className="data-actions price-toolbar"><input className="price-search" aria-label="搜索模型价格" placeholder="搜索模型价格…" value={filter} onChange={event => setFilter(event.target.value)} />{refreshAction}</div>
    {error && <p role="alert" className="negative">{error}</p>}
    <div className="price-list">{all.filter(price => price.model.toLowerCase().includes(filter.toLowerCase())).map(price => <PriceRow key={price.model} price={price} connection={connection} onSaved={onSaved} />)}</div>
    {!loading && !error && !all.length && <p className="muted">还没有模型价格，可以新增模型，或等待请求事实导入后自动发现模型。</p>}
  </section>;
}

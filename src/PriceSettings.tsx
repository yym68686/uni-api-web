import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { ExternalLink, Save, SlidersHorizontal } from "lucide-react";
import { analyticsRequest } from "./api";
import type { Connection, ModelPrice } from "./types";
import { Spinner } from "./ui";
import { displayedModelPrices, MODEL_PRICE_CATALOG } from "./modelPrices";

const fields = [
  ["input", "输入"], ["output", "输出"], ["cache_read", "缓存读取"],
  ["cache_write", "缓存写入 · 5 分钟"], ["cache_write_1h", "缓存写入 · 1 小时"],
] as const;

function PriceRow({ price, connection, onSaved }: { price: ModelPrice; connection: Connection; onSaved: () => void }) {
  const reference = MODEL_PRICE_CATALOG.find(entry => entry.model === price.model)!;
  const [draft, setDraft] = useState(price);
  const [dirty, setDirty] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  useEffect(() => { if (!dirty) setDraft(price); }, [price, dirty]);
  const change = (patch: Partial<ModelPrice>) => {
    setDraft(value => ({ ...value, ...patch })); setDirty(true); setSaved(false);
  };
  const visibleFields = fields.filter(([field]) => reference.cache_write_kind === "ttl" ||
    field !== "cache_write_1h" && (reference.cache_write_kind === "flat" || field !== "cache_write"));
  const unpriced = !draft.verified && !reference.verified && !dirty;
  async function save() {
    if (pending) return;
    setPending(true); setError(""); setSaved(false);
    try {
      const next = { ...draft, source: "manual" };
      if (reference.cache_write_kind === "input") next.cache_write = next.cache_write_1h = next.input;
      if (reference.cache_write_kind === "flat") next.cache_write_1h = next.cache_write;
      await analyticsRequest(connection, `/analytics/v1/prices/${encodeURIComponent(price.model)}`, undefined, {
        method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(next),
      });
      setDraft(next); setSaved(true); onSaved();
    } catch (error) { setError(error instanceof Error ? error.message : "保存失败"); }
    finally { setPending(false); }
  }
  return <article className="price-editor">
    <header role="generic"><strong>{price.model}</strong><span className="price-source muted">{price.source === "manual" ? "自定义价格" : reference.verified ? "官方参考价" : "未定价"}{reference.verified && <a href={reference.source} target="_blank" rel="noreferrer">官方来源 <ExternalLink size={12} /></a>}</span></header>
    <p className="field-note price-basis">{reference.note}</p>
    <div className="price-inputs">{visibleFields.map(([field, label]) => <label key={field}>{field === "cache_write" && reference.cache_write_kind === "flat" ? "缓存写入" : label}<input aria-label={`${price.model} ${label}价格`} type="number" min="0" max="1000000000" step="any" required value={unpriced || !Number.isFinite(draft[field]) ? "" : draft[field]} placeholder={unpriced ? "未公布" : ""} disabled={pending} onChange={event => change({ [field]: event.target.valueAsNumber })} /></label>)}</div>
    <footer><label className="price-verified"><input type="checkbox" checked={!!draft.verified} disabled={pending} onChange={event => change({ verified: event.target.checked })} />确认价格并用于估算</label><button className="button small" disabled={pending || unpriced || visibleFields.some(([field]) => !Number.isFinite(draft[field]) || draft[field] < 0)} onClick={() => void save()}>{pending ? <Spinner small /> : <Save size={14} />} {saved ? "已保存" : "保存价格"}</button></footer>
    {error && <p role="alert" className="negative">{error}</p>}
  </article>;
}

export function PriceSettings({ prices, loading, error, connection, onSaved, refreshAction }: { prices: ModelPrice[]; loading: boolean; error?: string; connection: Connection; onSaved: () => void; refreshAction?: ReactNode }) {
  const [filter, setFilter] = useState("");
  const all = displayedModelPrices(prices);
  const visible = all.filter(price => price.model.toLowerCase().includes(filter.toLowerCase()));
  return <section className="data-panel prices-panel">
    <div className="data-heading"><div className="data-title"><SlidersHorizontal size={19} /><h2>模型价格</h2><span className="count-badge">{all.length}</span></div>{loading && <Spinner small />}</div>
    <p className="field-note">美元 / 百万 token · 官方参考价核对于 2026-09-19。模型列表与检测设置一致；带后缀的模型自动沿用基础模型价格，例如 gemini-3.1-pro-search → gemini-3.1-pro。</p>
    <p className="field-note">按标准文本请求估算，未单列的缓存写入按普通输入计价；不含搜索、缓存存储等额外费用。长上下文及峰谷价见各模型说明，实际扣费以站点账单为准。</p>
    <div className="data-actions price-toolbar"><input className="price-search" aria-label="搜索模型价格" placeholder="搜索模型价格…" value={filter} onChange={event => setFilter(event.target.value)} />{refreshAction}</div>
    {error && <p role="alert" className="negative">{error}</p>}
    <div className="price-list">{visible.map(price => <PriceRow key={price.model} price={price} connection={connection} onSaved={onSaved} />)}</div>
    {!visible.length && <p className="muted">没有匹配的检测模型。</p>}
  </section>;
}

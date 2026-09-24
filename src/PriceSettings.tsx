import { Fragment, useEffect, useState } from "react";
import type { ReactNode } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import {
  Check,
  ExternalLink,
  Info,
  Save,
  Search,
  SlidersHorizontal,
  X,
} from "lucide-react";
import { analyticsRequest } from "./api";
import type { Connection, ModelPrice } from "./types";
import { Spinner } from "./ui";
import {
  chargesCacheWrite,
  salePercent,
  displayedModelPrices,
  MODEL_PRICE_CATALOG,
} from "./modelPrices";

const fields = [
  ["input", "输入"],
  ["output", "输出"],
  ["cache_read", "缓存读取"],
  ["cache_write", "缓存写入 · 5 分钟"],
  ["cache_write_1h", "缓存写入 · 1 小时"],
] as const;

function PriceDetails({ model }: { model: string }) {
  const reference = MODEL_PRICE_CATALOG.find((entry) => entry.model === model)!;
  return (
    <Dialog.Root>
      <Dialog.Trigger asChild>
        <button
          className="icon-button price-info"
          aria-label={`查看 ${model} 价格说明`}
          title="价格说明与来源"
        >
          <Info size={14} />
        </button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="guide-dialog price-details">
          <Dialog.Title>{model}</Dialog.Title>
          <Dialog.Description>价格说明 · 美元 / 百万 token</Dialog.Description>
          <p className="price-details-note">{reference.note}</p>
          <p>
            {reference.cache_write_kind === "input"
              ? "缓存写入按输入价格计算。修改输入价格后，两个缓存写入价格会同步更新。"
              : reference.cache_write_kind === "flat"
                ? "缓存写入使用统一价格，不按缓存时长分档。"
                : "缓存写入分别使用 5 分钟与 1 小时价格。"}
            关闭写入计费后不计该项费用，价格设置仍保留。
          </p>
          {reference.verified && (
            <a
              className="price-reference-link"
              href={reference.source}
              target="_blank"
              rel="noreferrer"
            >
              查看官方价格来源 <ExternalLink size={14} />
            </a>
          )}
          <Dialog.Close asChild>
            <button
              className="icon-button detail-close"
              aria-label="关闭价格说明"
            >
              <X size={18} />
            </button>
          </Dialog.Close>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function PriceRow({
  price,
  connection,
  onSaved,
}: {
  price: ModelPrice;
  connection: Connection;
  onSaved: () => void;
}) {
  const reference = MODEL_PRICE_CATALOG.find(
    (entry) => entry.model === price.model,
  )!;
  const [draft, setDraft] = useState(price);
  const [dirty, setDirty] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  useEffect(() => {
    if (!dirty) setDraft(price);
  }, [price, dirty]);
  const change = (patch: Partial<ModelPrice>) => {
    setDraft((value) => ({ ...value, ...patch }));
    setDirty(true);
    setSaved(false);
  };
  const visibleFields = fields.filter(
    ([field]) =>
      reference.cache_write_kind === "ttl" ||
      (field !== "cache_write_1h" &&
        (reference.cache_write_kind === "flat" || field !== "cache_write")),
  );
  const unpriced = !draft.verified && !reference.verified && !dirty;
  async function save() {
    if (pending) return;
    setPending(true);
    setError("");
    setSaved(false);
    try {
      const next = {
        ...draft,
        source: "manual",
        charge_cache_write: chargesCacheWrite(draft),
        sale_percent: salePercent(draft),
      };
      if (reference.cache_write_kind === "input")
        next.cache_write = next.cache_write_1h = next.input;
      if (reference.cache_write_kind === "flat")
        next.cache_write_1h = next.cache_write;
      const result = await analyticsRequest<{ price: ModelPrice }>(
        connection,
        `/analytics/v1/prices/${encodeURIComponent(price.model)}`,
        undefined,
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(next),
        },
      );
      if (result.price?.sale_percent !== next.sale_percent) {
        throw new Error("服务尚未确认售卖比例，请刷新后重新保存。");
      }
      setDraft(next);
      setSaved(true);
      onSaved();
    } catch (error) {
      setError(error instanceof Error ? error.message : "保存失败");
    } finally {
      setPending(false);
    }
  }
  return (
    <Fragment>
      <tr
        className="price-table-row"
        data-dirty={(dirty && !saved) || undefined}
        aria-busy={pending || undefined}
      >
        <th scope="row" className="price-model-cell">
          <div className="price-model-name">
            <strong title={price.model}>{price.model}</strong>
            <PriceDetails model={price.model} />
          </div>
          <div className="price-model-meta">
            <span>
              {price.source === "manual" || saved
                ? "自定义价格"
                : reference.verified
                  ? "官方参考价"
                  : "未定价"}
            </span>
            {dirty && !saved && <span className="price-unsaved">未保存</span>}
          </div>
        </th>
        {fields.map(([field, label]) => (
          <td key={field} className="price-number-cell">
            {visibleFields.some(([visible]) => visible === field) ? (
              <input
                className="price-number-input"
                aria-label={`${price.model} ${label}价格`}
                type="number"
                min="0"
                max="1000000000"
                step="any"
                required
                value={
                  unpriced || !Number.isFinite(draft[field]) ? "" : draft[field]
                }
                placeholder={unpriced ? "未公布" : ""}
                disabled={pending}
                onChange={(event) =>
                  change({ [field]: event.target.valueAsNumber })
                }
              />
            ) : (
              <span
                className="price-derived"
                title={
                  reference.cache_write_kind === "input"
                    ? "随输入价格自动更新"
                    : "与默认缓存写入价格一致"
                }
              >
                {reference.cache_write_kind === "input" ? "同输入" : "同默认"}
              </span>
            )}
          </td>
        ))}
        <td className="price-number-cell">
          <input
            className="price-number-input"
            aria-label={`${price.model} 售卖价格百分比`}
            type="number"
            min="0"
            max="1000000"
            step="any"
            required
            value={
              Number.isFinite(salePercent(draft)) ? salePercent(draft) : ""
            }
            disabled={pending}
            onChange={(event) =>
              change({ sale_percent: event.target.valueAsNumber })
            }
          />
        </td>
        <td className="price-toggle-cell">
          <label className="price-table-toggle">
            <input
              type="checkbox"
              aria-label={`${price.model} 计算缓存写入费用`}
              checked={chargesCacheWrite(draft)}
              disabled={pending}
              onChange={(event) =>
                change({ charge_cache_write: event.target.checked })
              }
            />
            <span aria-hidden="true" />
          </label>
        </td>
        <td className="price-toggle-cell">
          <label className="price-table-toggle">
            <input
              type="checkbox"
              aria-label={`${price.model} 确认价格并用于估算`}
              checked={!!draft.verified}
              disabled={pending}
              onChange={(event) => change({ verified: event.target.checked })}
            />
            <span aria-hidden="true" />
          </label>
        </td>
        <td className="price-save-cell">
          <button
            className="button small"
            disabled={
              pending ||
              unpriced ||
              !Number.isFinite(salePercent(draft)) ||
              salePercent(draft) < 0 ||
              salePercent(draft) > 1000000 ||
              visibleFields.some(
                ([field]) => !Number.isFinite(draft[field]) || draft[field] < 0,
              )
            }
            onClick={() => void save()}
          >
            {pending ? (
              <Spinner small />
            ) : saved ? (
              <Check size={14} />
            ) : (
              <Save size={14} />
            )}{" "}
            {pending ? "保存中" : saved ? "已保存" : "保存价格"}
          </button>
        </td>
      </tr>
      {error && (
        <tr className="price-error-row">
          <td colSpan={10}>
            <p role="alert">
              {price.model}：{error}
            </p>
          </td>
        </tr>
      )}
    </Fragment>
  );
}

export function PriceSettings({
  prices,
  loading,
  error,
  connection,
  onSaved,
  refreshAction,
}: {
  prices: ModelPrice[];
  loading: boolean;
  error?: string;
  connection: Connection;
  onSaved: () => void;
  refreshAction?: ReactNode;
}) {
  const [filter, setFilter] = useState("");
  const all = displayedModelPrices(prices);
  const visible = all.filter((price) =>
    price.model.toLowerCase().includes(filter.toLowerCase()),
  );
  return (
    <section className="data-panel prices-panel">
      <div className="data-heading price-heading">
        <div className="data-title">
          <SlidersHorizontal size={19} />
          <h2>模型价格</h2>
          <span className="count-badge">{all.length}</span>
        </div>
        <div className="data-actions price-toolbar">
          {loading && <Spinner small />}
          <label className="price-search-field">
            <Search size={15} aria-hidden="true" />
            <input
              className="price-search"
              aria-label="搜索模型价格"
              placeholder="搜索模型价格…"
              value={filter}
              onChange={(event) => setFilter(event.target.value)}
            />
          </label>
          {refreshAction}
        </div>
      </div>
      <div className="price-table-intro">
        <p>
          美元 / 百万 token
          <span>每行独立保存 · 模型旁 ⓘ 查看来源与适用条件</span>
        </p>
        <details className="price-help">
          <summary>计费说明</summary>
          <div>
            <p>
              模型列表与检测设置一致；带后缀的模型自动沿用基础模型价格，例如
              gemini-3.1-pro-search → gemini-3.1-pro。
            </p>
            <p>
              GPT
              默认不计缓存写入费用，其他模型默认计费，可逐项修改。“同输入”随输入价格自动更新，“同默认”使用默认缓存写入价格。关闭写入计费后保留原有价格。价格确认后才参与估算，未公布价格不表示免费。
            </p>
            <p>
              售卖比例为原价的百分比，填 2.5 表示按原价的 2.5% 售卖。GPT
              及其他模型默认 2.5%，Claude / Gemini 默认 15%。利润 = 估算消费 ×
              售卖百分比 ÷ 100 × 6.9 − 渠道实际消费。
            </p>
            <p>不含搜索、缓存存储等额外费用，实际扣费以站点账单为准。</p>
          </div>
        </details>
      </div>
      {error && (
        <p role="alert" className="price-load-error negative">
          {error}
        </p>
      )}
      <div
        className="price-table-scroll"
        role="region"
        aria-label="模型价格表，可横向滚动"
        tabIndex={0}
      >
        <table className="price-table" aria-label="模型价格">
          <colgroup>
            <col className="price-model-col" />
            {fields.map(([field]) => (
              <col className="price-number-col" key={field} />
            ))}
            <col className="price-number-col" />
            <col className="price-toggle-col" />
            <col className="price-toggle-col" />
            <col className="price-save-col" />
          </colgroup>
          <thead>
            <tr>
              <th scope="col" className="price-model-cell">
                模型
              </th>
              <th scope="col">输入</th>
              <th scope="col">输出</th>
              <th scope="col">缓存读取</th>
              <th scope="col">
                缓存写入<small>默认 / 5 分钟</small>
              </th>
              <th scope="col">
                缓存写入<small>1 小时</small>
              </th>
              <th scope="col">
                售卖比例<small>原价 %</small>
              </th>
              <th scope="col">写入计费</th>
              <th scope="col">参与估算</th>
              <th scope="col">操作</th>
            </tr>
          </thead>
          <tbody>
            {visible.map((price) => (
              <PriceRow
                key={price.model}
                price={price}
                connection={connection}
                onSaved={onSaved}
              />
            ))}
            {!visible.length && (
              <tr>
                <td colSpan={10} className="price-empty">
                  没有匹配的检测模型。
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <div className="price-table-footer">
        <span>
          {filter
            ? `显示 ${visible.length} / ${all.length} 个模型`
            : `共 ${all.length} 个模型`}
        </span>
        <span>窄屏可横向滚动，模型列固定</span>
      </div>
    </section>
  );
}

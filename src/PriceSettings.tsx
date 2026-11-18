import { TableLoading } from "./PageLoading";
import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode, Dispatch, SetStateAction } from "react";
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
import { WorkbenchDisclosure, WorkbenchNotice } from "./Workbench";
import {
  chargesCacheWrite,
  salePercent,
  displayedModelPrices,
  MODEL_PRICE_CATALOG,
  promptPriceTier,
  priceForPrompt,
} from "./modelPrices";
import "./priceSettings.css";

type PriceEdit = { draft: ModelPrice; baseline: ModelPrice };
export type PriceDrafts = Map<string, PriceEdit>;

const fields = [
  ["input", "输入"],
  ["output", "输出"],
  ["cache_read", "缓存读取"],
  ["cache_write", "缓存写入 · 5 分钟"],
  ["cache_write_1h", "缓存写入 · 1 小时"],
] as const;
const references = new Map(MODEL_PRICE_CATALOG.map(entry => [entry.model, entry] as const));
const MAX_PRICE = 1_000_000_000;
const MAX_SALE = 1_000_000;

function PriceDetails({ model }: { model: string }) {
  const reference = references.get(model)!;
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
          {reference.verified && reference.source.startsWith("https://") && (
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
  hidden,
  readOnly,
  edit,
  onEdit,
  pending,
  onPending,
}: {
  price: ModelPrice;
  connection: Connection;
  onSaved: (price: ModelPrice) => void;
  hidden?: boolean;
  readOnly?: boolean;
  edit?: PriceEdit;
  onEdit: (model: string, edit?: PriceEdit) => void;
  pending: boolean;
  onPending: (model: string, pending: boolean) => void;
}) {
  const reference = references.get(price.model)!;
  const [receipt, setReceipt] = useState<{ value: ModelPrice; against: ModelPrice }>();
  const current = receipt?.against === price ? receipt.value : price;
  const draft = edit?.draft || current;
  const dirty = !!edit;
  const [error, setError] = useState("");
  const saving = useRef(false);
  const saved = !!receipt && !dirty;
  const conflicted = !!edit && JSON.stringify(edit.baseline) !== JSON.stringify(current);
  const change = (patch: Partial<ModelPrice>) => {
    onEdit(price.model, {draft:{...draft,...patch},baseline:edit?.baseline || current});
    setError("");
  };
  function discard() {
    onEdit(price.model);
    setError("");
  }
  const visibleFields = fields.filter(
    ([field]) =>
      reference.cache_write_kind === "ttl" ||
      (field !== "cache_write_1h" &&
        (reference.cache_write_kind === "flat" || field !== "cache_write")),
  );
  const fieldLabel = (field: (typeof fields)[number][0], label: string) =>
    field === "cache_write" && reference.cache_write_kind === "flat"
      ? "缓存写入 · 统一单价"
      : label;
  const tier = promptPriceTier(price.model);
  const highPrice = tier ? priceForPrompt(draft, tier.threshold_tokens + 1) : undefined;
  const tierLimit = tier ? `${tier.threshold_tokens / 1000}k` : "";
  const unpriced = !draft.verified && !reference.verified && !dirty;
  const canAdopt = !dirty && current.source !== "manual" && reference.verified;
  const invalid = visibleFields.some(([field]) => !Number.isFinite(draft[field]) || draft[field] < 0 || draft[field] > MAX_PRICE) ||
    !Number.isFinite(salePercent(draft)) || salePercent(draft) < 0 || salePercent(draft) > MAX_SALE;
  const blocked = !!readOnly || pending || invalid || conflicted || unpriced || (!dirty && !canAdopt);
  async function save() {
    if (saving.current || blocked) return;
    saving.current = true;
    onPending(price.model, true);
    setError("");
    try {
      const next = {
        ...draft,
        source: "manual",
        prompt_price_tier: tier,
        charge_cache_write: chargesCacheWrite(draft),
        long_context_premium: tier ? false : draft.long_context_premium ?? false,
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
      if (result.price?.long_context_premium !== next.long_context_premium) {
        throw new Error("服务尚未确认长上下文加价设置，请刷新后重新保存。");
      }
      if (tier && (
        result.price?.prompt_price_tier?.threshold_tokens !== tier.threshold_tokens ||
        result.price.prompt_price_tier.input_multiplier !== tier.input_multiplier ||
        result.price.prompt_price_tier.output_multiplier !== tier.output_multiplier ||
        result.price.prompt_price_tier.cache_multiplier !== tier.cache_multiplier
      )) {
        throw new Error("服务尚未确认 100k 分档规则，请等待后台更新后重新保存。");
      }
      if (result.price.model !== next.model ||
          fields.some(([field]) => result.price[field] !== next[field]) ||
          chargesCacheWrite(result.price) !== next.charge_cache_write ||
          !!result.price.verified !== !!next.verified) {
        throw new Error("服务返回的价格与提交内容不一致，请刷新核对。");
      }
      setReceipt({value: result.price, against: price});
      discard();
      onSaved(result.price);
    } catch (error) {
      setError(error instanceof Error ? error.message : "保存失败");
    } finally {
      saving.current = false;
      onPending(price.model, false);
    }
  }
  return (
    <Fragment>
      <tr
        className="price-table-row"
        hidden={hidden}
        data-dirty={dirty || undefined}
        aria-busy={pending || undefined}
      >
        <th scope="row" className="price-model-cell">
          <div className="price-model-name">
            <strong title={price.model}>{price.model}</strong>
            <PriceDetails model={price.model} />
          </div>
          <div className="price-model-meta">
            <span>
              {current.source === "manual"
                ? "自定义价格"
                : price.source?.startsWith("inherited:")
                  ? "沿用参考价"
                : reference.verified
                  ? "官方参考价"
                  : "未定价"}
            </span>
            {dirty && <span className="price-unsaved">未保存</span>}
          </div>
        </th>
        {fields.map(([field, label]) => (
          <td key={field} className="price-number-cell" data-label={label}>
            {tier && <small className="price-tier-label">≤{tierLimit}</small>}
            {visibleFields.some(([visible]) => visible === field) ? (
              <input
                className="price-number-input"
                aria-label={`${price.model} ${fieldLabel(field, label)}价格`}
                type="number"
                min="0"
                max={MAX_PRICE}
                step="any"
                required
                value={
                  unpriced || !Number.isFinite(draft[field]) ? "" : draft[field]
                }
                placeholder={unpriced ? "未公布" : ""}
                disabled={pending || readOnly}
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
            {highPrice && (
              <small className="price-tier-rate" aria-label={`${price.model} 超过${tierLimit} ${label}价格`}>
                <span>&gt;{tierLimit}</span>
                <span>{Number.isFinite(highPrice[field]) ? `$${Number(highPrice[field].toPrecision(12))}` : "—"}</span>
              </small>
            )}
          </td>
        ))}
        <td className="price-number-cell" data-label="售卖比例（%）">
          <input
            className="price-number-input"
            aria-label={`${price.model} 售卖价格百分比`}
            type="number"
            min="0"
            max={MAX_SALE}
            step="any"
            required
            value={
              Number.isFinite(salePercent(draft)) ? salePercent(draft) : ""
            }
            disabled={pending || readOnly}
            onChange={(event) =>
              change({ sale_percent: event.target.valueAsNumber })
            }
          />
        </td>
        <td className="price-toggle-cell" data-label="分开计费">
          <label className="price-table-toggle">
            <input
              type="checkbox"
              aria-label={`${price.model} 计算缓存写入费用`}
              checked={chargesCacheWrite(draft)}
              disabled={pending || readOnly}
              onChange={(event) =>
                change({ charge_cache_write: event.target.checked })
              }
            />
            <span aria-hidden="true" />
          </label>
        </td>
        <td className="price-toggle-cell" data-label="长上下文">
          {tier ? (
            <span className="price-tier-policy" title={`总输入（含缓存）超过 ${tier.threshold_tokens.toLocaleString("en-US")} token 时，整次请求输入、输出及缓存单价均 ×${tier.input_multiplier}；不叠加 272k 加价。`}>
              <strong>&gt;{tierLimit} ×{tier.input_multiplier}</strong>
              <small>自动分档</small>
            </span>
          ) : <label className="price-table-toggle" title="单次请求总输入（含缓存）超过 272,000 token：输入、缓存读取和缓存写入单价 ×2，输出单价 ×1.5">
            <input
              type="checkbox"
              aria-label={`${price.model} 超过272k加价`}
              checked={draft.long_context_premium ?? false}
              disabled={pending || readOnly}
              onChange={(event) => change({ long_context_premium: event.target.checked })}
            />
            <span aria-hidden="true" />
          </label>}
        </td>
        <td className="price-toggle-cell" data-label="启用价格">
          <label className="price-table-toggle">
            <input
              type="checkbox"
              aria-label={`${price.model} 确认价格并用于估算`}
              checked={!!draft.verified}
              disabled={pending || readOnly}
              onChange={(event) => change({ verified: event.target.checked })}
            />
            <span aria-hidden="true" />
          </label>
        </td>
        <td className="price-save-cell">
          <button
            className="button small"
            disabled={blocked}
            onClick={() => void save()}
          >
            {pending ? (
              <Spinner small />
            ) : saved ? (
              <Check size={14} />
            ) : (
              <Save size={14} />
            )}{" "}
            {pending ? "保存中" : saved ? "已保存" : canAdopt ? "采用参考价" : "保存价格"}
          </button>
          {dirty && <button className="button small" type="button" disabled={pending} onClick={discard}>放弃草稿</button>}
          {dirty && !invalid && <small className="price-impact">
            {draft.verified
              ? `美元 / 百万 token：输入 ${current.input} → ${draft.input}，输出 ${current.output} → ${draft.output}。`
              : "保存后不使用此价格估算。"}
            {draft.long_context_premium !== current.long_context_premium && (draft.long_context_premium ? " 开启 272k 加价。" : " 关闭 272k 加价。")}
            按新设置重算历史估算，不修改实际扣费。
          </small>}
          {dirty && invalid && <small className="negative">请核对价格和售卖比例的范围。</small>}
          {conflicted && <small role="alert">服务端价格已变化，请先核对最新价格，再重新编辑。</small>}
        </td>
      </tr>
      {error && (
        <tr className="price-error-row" hidden={hidden}>
          <td colSpan={11}>
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
  drafts: externalDrafts,
  onDraftChange,
  pendingModels: externalPending,
  onPendingChange,
}: {
  prices: ModelPrice[];
  loading: boolean;
  error?: string;
  connection: Connection;
  onSaved: (price: ModelPrice) => void;
  refreshAction?: ReactNode;
  drafts?: PriceDrafts;
  onDraftChange?: Dispatch<SetStateAction<PriceDrafts>>;
  pendingModels?: Set<string>;
  onPendingChange?: Dispatch<SetStateAction<Set<string>>>;
}) {
  const [filter, setFilter] = useState("");
  const [editing, setEditing] = useState("");
  const editorOpener = useRef<HTMLElement | null>(null);
  const [localDrafts, setLocalDrafts] = useState<PriceDrafts>(() => new Map());
  const drafts = externalDrafts ?? localDrafts;
  const setDrafts = onDraftChange ?? setLocalDrafts;
  const [localPending, setLocalPending] = useState(() => new Set<string>());
  const pendingModels = externalPending ?? localPending;
  const setPendingModels = onPendingChange ?? setLocalPending;
  const all = useMemo(() => displayedModelPrices(prices), [prices]);
  const visible = all.filter((price) =>
    price.model.toLowerCase().includes(filter.trim().toLowerCase()),
  );
  const editedPrice = all.find(price => price.model === editing);
  const dirtyCount = drafts.size;
  useEffect(() => {
    if (!dirtyCount || externalDrafts) return;
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ""; };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirtyCount, !!externalDrafts]);
  return (
    <section className="data-panel prices-panel">
      <div className="data-heading price-heading workbench-heading">
        <div className="data-title">
          <SlidersHorizontal size={19} />
          <h2>模型价格</h2>
          <WorkbenchNotice urgent label="价格读取失败" messages={[error && `${error} · 当前配置未核实，参考价只读。`]} />
        </div>
        <div className="workbench-tools">
          <WorkbenchDisclosure label={filter ? "筛选 1" : "筛选"} icon={<Search size={14} aria-hidden="true" />}>
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
          </WorkbenchDisclosure>
          {refreshAction}
        </div>
      </div>
      {loading ? <TableLoading kind="prices" rows={8} /> : <>
      <div className="price-overview-wrap">
        <table className="price-overview-table" aria-label="模型价格">
          <thead>
            <tr>
              <th scope="col">模型</th>
              <th scope="col">输入</th>
              <th scope="col">输出</th>
              <th scope="col" className="price-reference-extra">缓存读取</th>
            </tr>
          </thead>
          <tbody>
            {visible.map(price => {
              const published = price.verified || references.get(price.model)?.verified;
              return <tr key={price.model}>
                <th scope="row"><button type="button" className="price-model-open" aria-label={`编辑 ${price.model}`} title={price.model} onClick={event => {
                  editorOpener.current = event.currentTarget;
                  setEditing(price.model);
                }}>{price.model}</button><small>{drafts.has(price.model) ? "未保存" : price.verified ? "已确认" : published ? "待确认" : "未定价"}</small></th>
                {(["input","output","cache_read"] as const).map(field =>
                  <td key={field} className={field === "cache_read" ? "price-reference-extra" : undefined}>{published ? `$${price[field]}` : "—"}</td>)}
              </tr>;
            })}
            {!visible.length && (
              <tr>
                <td colSpan={4} className="price-empty">
                  没有匹配的模型
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
        <span>美元 / 百万 token{dirtyCount > 0 && ` · ${dirtyCount} 项未保存`}</span>
      </div>
      </>}
      <Dialog.Root open={!!editedPrice} onOpenChange={open => { if (!open && !pendingModels.has(editing)) setEditing(""); }}>
        {editedPrice && <Dialog.Portal>
          <Dialog.Overlay className="dialog-overlay" />
          <Dialog.Content className="connection-dialog price-edit-dialog" onCloseAutoFocus={event => {
            event.preventDefault();
            editorOpener.current?.focus({preventScroll:true});
          }}>
            <div className="locator-heading">
              <Dialog.Title>{editedPrice.model}</Dialog.Title>
              <Dialog.Close className="icon-button" aria-label="关闭价格编辑" disabled={pendingModels.has(editing)}><X size={18}/></Dialog.Close>
            </div>
            <Dialog.Description className="price-field-unit">美元 / 百万 token</Dialog.Description>
            <table className="price-table price-edit-fields"><tbody>
              <PriceRow key={editedPrice.model} price={editedPrice} connection={connection} onSaved={onSaved} readOnly={!!error}
                edit={drafts.get(editedPrice.model)} pending={pendingModels.has(editedPrice.model)}
                onPending={(model, pending) => setPendingModels(previous => {
                  const next = new Set(previous);
                  if (pending) next.add(model); else next.delete(model);
                  return next;
                })}
                onEdit={(model, edit) => setDrafts(previous => {
                  const next = new Map(previous);
                  if (edit) next.set(model,edit); else next.delete(model);
                  return next;
                })} />
            </tbody></table>
          </Dialog.Content>
        </Dialog.Portal>}
      </Dialog.Root>
    </section>
  );
}

import { useEffect, useRef, useState } from "react";
import { controlRequest } from "./api";
import type { SubAccount } from "./Sub2apiChecks";
import "./siteRecharge.css";

interface Config {
  enabled: boolean;
  shop_url: string;
  products: string[];
  contact: string;
  max_cny: string;
  target_base: string;
  target_email: string;
}
interface Product {
  key: string;
  name: string;
  price_cny: string;
}
interface Settings {
  config: Config;
  supported: boolean;
  payment_configured: boolean;
  identity_current: boolean;
}
interface Quote {
  id: string;
  product: string;
  name: string;
  merchant: string;
  target: string;
  total_cny: string;
  fee_cny: string;
  expires_at: number;
}
interface Job {
  id: string;
  state: string;
  message: string;
  quote: Quote;
  merchant_order?: string;
  action_url?: string;
  credited_amount?: number;
}
interface Wallet {
  status: string;
  message: string;
  action_url?: string;
}
interface Recovery {
  merchant_order: string;
  query_password: string;
  code: string;
  payment_url: string;
}

const labels: Record<string, string> = {
  queued: "等待下单",
  creating: "正在下单",
  paying: "正在提交付款",
  awaiting_payment: "核对付款 / 等待发货",
  redeeming: "正在兑换",
  verifying: "核对到账",
  done: "已到账",
  failed: "未下单",
  closed: "已关闭 / 退款",
  review: "需核对原订单",
};
const active = (job: Job) => !["done", "closed", "failed"].includes(job.state);
function officialLink(raw?: string) {
  if (!raw) return false;
  try {
    const u = new URL(raw);
    return (
      u.protocol === "https:" &&
      !u.username &&
      !u.password &&
      !u.port &&
      !u.hash &&
      (u.hostname === "aipay.alipay.com" ||
        u.hostname === "qr.alipay.com" ||
        u.hostname === "excashier.alipay.com" ||
        /^cashier[a-z0-9-]*\.alipay\.com$/.test(u.hostname))
    );
  } catch {
    return false;
  }
}
function paymentLink(raw?: string) {
  if (officialLink(raw)) return true;
  try {
    const u = new URL(raw!);
    return (
      u.protocol === "https:" &&
      !u.username &&
      !u.password &&
      !u.port &&
      !u.hash &&
      ["wzyp.cn", "pay.ldxp.cn"].includes(u.hostname)
    );
  } catch {
    return false;
  }
}

export function SiteRecharge({ account }: { account: SubAccount }) {
  const base = `/v1/sub2api/accounts/${encodeURIComponent(account.id)}/recharge`;
  const [settings, setSettings] = useState<Settings>();
  const [config, setConfig] = useState<Config>();
  const [products, setProducts] = useState<Product[]>([]);
  const [selected, setSelected] = useState("");
  const [quote, setQuote] = useState<Quote>();
  const [confirmed, setConfirmed] = useState(false);
  const [identityConfirmed, setIdentityConfirmed] = useState(false);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [wallet, setWallet] = useState<Wallet>();
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState<Recovery>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [pollError, setPollError] = useState("");
  const submitKey = useRef("");
  const inFlight = useRef(false);
  async function read(signal?: AbortSignal) {
    const [s, j] = await Promise.all([
      controlRequest<Settings>(`${base}/settings`, { signal }),
      controlRequest<{ jobs: Job[] }>(`${base}/jobs`, { signal }),
    ]);
    if (signal?.aborted) return;
    setSettings(s);
    setConfig((c) => c || s.config);
    setJobs(j.jobs);
    setPollError("");
  }
  useEffect(() => {
    const controller = new AbortController();
    void read(controller.signal).catch((e) => {
      if (!controller.signal.aborted)
        setError(e instanceof Error ? e.message : "充值设置读取失败");
    });
    const timer = window.setInterval(() => {
      void controlRequest<{ jobs: Job[] }>(`${base}/jobs`, {
        signal: controller.signal,
      })
        .then((j) => {
          if (!controller.signal.aborted) {
            setJobs(j.jobs);
            setPollError("");
          }
        })
        .catch(() => {
          if (!controller.signal.aborted)
            setPollError(
              "充值状态刷新失败，当前显示上次记录；请核对原订单，勿重复付款。",
            );
        });
    }, 15000);
    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, [base]);
  async function run(fn: () => Promise<void>) {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setError("");
    setNote("");
    try {
      await fn();
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "操作未确认，请先刷新原充值状态",
      );
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }
  const update = (patch: Partial<Config>) => {
    setConfig((c) => (c ? { ...c, ...patch } : c));
    setQuote(undefined);
    setConfirmed(false);
  };
  async function catalog() {
    const data = await controlRequest<{
      shop_url: string;
      products: Product[];
    }>(`${base}/catalog`, {
      method: "POST",
      body: JSON.stringify({ shop_url: config?.shop_url }),
    });
    setProducts(data.products);
    update({ shop_url: data.shop_url });
  }
  async function walletAction(action: string) {
    const status = await controlRequest<Wallet>(
      `${base}/wallet`,
      action === "status"
        ? {}
        : {
            method: "POST",
            body: JSON.stringify({
              action,
              code: action === "bind" ? code : undefined,
            }),
          },
    );
    setWallet(status);
    if (action === "bind") setCode("");
  }
  if (!config || !settings)
    return (
      <section className="recharge-panel">
        <h3>充值</h3>
        <p role={error ? "alert" : "status"}>{error || "读取充值设置…"}</p>
        <button
          className="button small"
          onClick={() => void run(() => read())}
          disabled={busy}
        >
          重新读取充值设置
        </button>
      </section>
    );
  if (!settings.supported)
    return (
      <section className="recharge-panel">
        <h3>充值</h3>
        <p>当前仅适配 sub2api + 链动。此站点不会使用其他站点的充值流程。</p>
      </section>
    );
  return (
    <section className="recharge-panel" aria-label="站点充值">
      <h3>实时充值</h3>
      <p className="settings-note">
        实时购买兑换码，支付后自动取码并兑换到当前账号。首次使用需完成支付宝官方授权，每笔确认含手续费的金额。
      </p>
      {error && (
        <p className="error-banner" role="alert">
          {error}
        </p>
      )}
      {note && (
        <p className="settings-note" role="status">
          {note}
        </p>
      )}
      <label>
        <input
          type="checkbox"
          checked={config.enabled}
          disabled={busy}
          onChange={(e) => update({ enabled: e.target.checked })}
        />
        启用此账号的实时充值
      </label>
      <label>
        链动店铺地址
        <input
          type="url"
          value={config.shop_url}
          placeholder="https://wzyp.cn/shop/…"
          disabled={busy}
          onChange={(e) => update({ shop_url: e.target.value })}
        />
      </label>
      <p className="settings-note">
        可粘贴完整店铺链接；保存时会去除登录令牌等查询参数。请选择明确用于本账号的充值商品。
      </p>
      <button
        type="button"
        className="button small"
        disabled={busy || !config.shop_url}
        onClick={() => void run(catalog)}
      >
        读取充值商品（不下单）
      </button>
      <div className="recharge-products">
        {products.map((p) => (
          <label key={p.key}>
            <input
              type="checkbox"
              checked={config.products.includes(p.key)}
              disabled={busy}
              onChange={(e) =>
                update({
                  products: e.target.checked
                    ? [...config.products, p.key]
                    : config.products.filter((k) => k !== p.key),
                })
              }
            />
            {p.name} · ¥{p.price_cny}
          </label>
        ))}
        {!products.length && config.products.length > 0 && (
          <p className="settings-note">
            已绑定 {config.products.length} 个充值商品；读取商品可调整。
          </p>
        )}
      </div>
      <label>
        订单联系方式
        <input
          value={config.contact}
          maxLength={200}
          disabled={busy}
          onChange={(e) => update({ contact: e.target.value })}
        />
      </label>
      <label>
        单次含手续费上限（元）
        <input
          inputMode="decimal"
          value={config.max_cny}
          disabled={busy}
          onChange={(e) => update({ max_cny: e.target.value })}
        />
      </label>
      <label>
        <input
          type="checkbox"
          checked={identityConfirmed}
          disabled={busy}
          onChange={(e) => setIdentityConfirmed(e.target.checked)}
        />
        确认这些商品可兑换到 {account.base} · {account.email}
        ，不是其他站点的兑换码
      </label>
      <button
        type="button"
        className="button small"
        disabled={busy || (config.enabled && !identityConfirmed)}
        onClick={() =>
          void run(async () => {
            const saved = {
              ...config,
              target_base: account.base,
              target_email: account.email,
            };
            await controlRequest(`${base}/settings`, {
              method: "PUT",
              body: JSON.stringify(saved),
            });
            setConfig(saved);
            await read();
            setNote("充值设置已保存；未创建订单或付款。");
          })
        }
      >
        保存充值设置
      </button>
      {!settings.identity_current && (
        <p className="error-banner">账号身份已变化，请重新确认充值设置。</p>
      )}
      <h4>支付宝授权</h4>
      {!settings.payment_configured && (
        <p className="settings-note">
          支付代理尚未配置。管理员需先接入支付宝官方 CLI
          和当前用户专用钱包；此时不会创建订单或付款。
        </p>
      )}
      <div className="recharge-actions">
        <button
          type="button"
          className="button small"
          disabled={busy || !settings.payment_configured}
          onClick={() => void run(() => walletAction("status"))}
        >
          查询钱包授权
        </button>
        <button
          type="button"
          className="button small"
          disabled={busy || !settings.payment_configured}
          onClick={() => void run(() => walletAction("apply"))}
        >
          开通支付宝授权
        </button>
      </div>
      {wallet && (
        <p role="status">
          {wallet.message}
          {officialLink(wallet.action_url) && (
            <>
              {" "}
              ·{" "}
              <a href={wallet.action_url} target="_blank" rel="noreferrer">
                打开支付宝官方授权
              </a>
            </>
          )}
        </p>
      )}
      <label>
        本次官方授权码
        <input
          autoComplete="off"
          type="password"
          value={code}
          disabled={busy}
          onChange={(e) => setCode(e.target.value)}
        />
      </label>
      <button
        type="button"
        className="button small"
        disabled={busy || !code || !settings.payment_configured}
        onClick={() => void run(() => walletAction("bind"))}
      >
        提交授权码
      </button>
      <h4>购买并自动兑换</h4>
      <label>
        充值商品
        <select
          value={selected}
          disabled={busy}
          onChange={(e) => {
            setSelected(e.target.value);
            setQuote(undefined);
            setConfirmed(false);
          }}
        >
          <option value="">请选择</option>
          {settings.config.products.map((k) => (
            <option value={k} key={k}>
              {products.find((p) => p.key === k)?.name || k}
            </option>
          ))}
        </select>
      </label>
      <button
        type="button"
        className="button small"
        disabled={
          busy ||
          !settings.config.enabled ||
          !selected ||
          !settings.payment_configured ||
          jobs.some(active)
        }
        onClick={() =>
          void run(async () => {
            const q = await controlRequest<Quote>(`${base}/quotes`, {
              method: "POST",
              body: JSON.stringify({ product: selected }),
            });
            submitKey.current = crypto.randomUUID();
            setQuote(q);
            setConfirmed(false);
          })
        }
      >
        获取实时含手续费报价（不下单）
      </button>
      {quote && (
        <div className="recharge-quote">
          <p>
            {quote.merchant} · {quote.name}（1 件）
          </p>
          <p>
            实付 ¥{quote.total_cny}，含手续费 ¥{quote.fee_cny}
          </p>
          <p>兑换到：{quote.target}</p>
          <label>
            <input
              type="checkbox"
              checked={confirmed}
              disabled={busy}
              onChange={(e) => setConfirmed(e.target.checked)}
            />
            确认本次商品、目标账号及实付金额，授权下单付款并自动兑换
          </label>
          <button
            type="button"
            className="button primary"
            disabled={busy || !confirmed || jobs.some(active)}
            onClick={() =>
              void run(async () => {
                await controlRequest(`${base}/jobs`, {
                  method: "POST",
                  body: JSON.stringify({
                    quote_id: quote.id,
                    total_cny: quote.total_cny,
                    confirmed: true,
                    idempotency_key: submitKey.current,
                  }),
                });
                setQuote(undefined);
                setConfirmed(false);
                await read();
                setNote(
                  "已提交一笔实时充值，请在下方查看原订单状态，勿重复付款。",
                );
              })
            }
          >
            确认支付 ¥{quote.total_cny} 并自动兑换
          </button>
        </div>
      )}
      <h4>充值记录</h4>
      {pollError && (
        <p role="alert" className="error-banner">
          {pollError}
        </p>
      )}
      <button
        type="button"
        className="button small"
        disabled={busy}
        onClick={() => void run(() => read())}
      >
        刷新充值状态（不重新付款）
      </button>
      {jobs.length === 0 && <p className="settings-note">暂无充值记录。</p>}
      {jobs.map((j) => (
        <article className="recharge-job" key={j.id}>
          <strong>
            {labels[j.state] || "状态待确认"} · ¥{j.quote.total_cny} ·{" "}
            {j.quote.name}
          </strong>
          <p>
            {j.message}
            {j.credited_amount !== undefined &&
              ` · 到账额度 ${j.credited_amount}`}
          </p>
          {j.merchant_order && <small>原订单：{j.merchant_order}</small>}
          {officialLink(j.action_url) && (
            <p>
              <a href={j.action_url} target="_blank" rel="noreferrer">
                支付宝官方确认入口
              </a>
            </p>
          )}
          <div className="recharge-actions">
            {["review", "awaiting_payment", "verifying"].includes(j.state) && (
              <button
                type="button"
                className="button small"
                disabled={busy}
                onClick={() =>
                  void run(async () => {
                    await controlRequest(
                      `${base}/jobs/${encodeURIComponent(j.id)}/reconcile`,
                      { method: "POST", body: "{}" },
                    );
                    await read();
                  })
                }
              >
                核对原订单并继续交付
              </button>
            )}
            <button
              type="button"
              className="button small"
              disabled={busy}
              onClick={() =>
                void run(async () =>
                  setRecovery(
                    await controlRequest<Recovery>(
                      `${base}/jobs/${encodeURIComponent(j.id)}/recovery`,
                    ),
                  ),
                )
              }
            >
              查看原订单恢复资料
            </button>
          </div>
        </article>
      ))}
      {recovery && (
        <div className="recharge-quote">
          <strong>仅用于核对原订单，请勿泄露</strong>
          <p>订单：{recovery.merchant_order || "尚未取得"}</p>
          <p>查询密码：{recovery.query_password}</p>
          {recovery.code && <p>已交付兑换码：{recovery.code}</p>}
          {paymentLink(recovery.payment_url) && (
            <p>
              <a href={recovery.payment_url} target="_blank" rel="noreferrer">
                打开原订单收银台（勿重复支付）
              </a>
            </p>
          )}
          <button
            type="button"
            className="button small"
            onClick={() => setRecovery(undefined)}
          >
            隐藏恢复资料
          </button>
        </div>
      )}
    </section>
  );
}

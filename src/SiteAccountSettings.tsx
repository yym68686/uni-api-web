import { useState } from "react";
import type { FormEvent } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { ApiError, controlRequest } from "./api";
import type { SubAccount } from "./Sub2apiChecks";
import { siteRateCoefficient } from "./billingRates";
import { SiteRecharge } from "./SiteRecharge";

export function SiteAccountSettings({
  account,
  close,
  saved,
  initialSection = "rate",
}: {
  account: SubAccount;
  close: () => void;
  saved: () => void;
  initialSection?: "rate" | "recharge";
}) {
  const [value, setValue] = useState(
    String(siteRateCoefficient(account.rate_coefficient)),
  );
  const [busy, setBusy] = useState(false);
  const [rechargeBusy, setRechargeBusy] = useState(false);
  const dialogBusy = busy || rechargeBusy;
  const [error, setError] = useState("");
  const [section, setSection] = useState<"rate" | "recharge">(initialSection);
  const coefficient = Number(value);
  const valid =
    value.trim() !== "" &&
    Number.isFinite(coefficient) &&
    coefficient > 0 &&
    coefficient <= 1000000;
  const current = siteRateCoefficient(account.rate_coefficient);
  const representative = account.targets?.find(target => Number.isFinite(target.rate) && target.rate > 0);
  const precision = new Intl.NumberFormat("zh-CN", {maximumSignificantDigits:6});
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (busy || !valid) return;
    setBusy(true);
    setError("");
    try {
      await controlRequest(
        `/v1/sub2api/accounts/${encodeURIComponent(account.id)}/settings`,
        {
          method: "PATCH",
          body: JSON.stringify({ rate_coefficient: coefficient }),
        },
      );
      saved();
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 404
          ? "站点设置服务尚未更新或账号已移除，请刷新后重试。"
          : e instanceof Error
            ? e.message
            : "设置保存失败，请重试。",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open && !dialogBusy) close();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content
          className="guide-dialog sub-account-dialog"
          onPointerDownOutside={(e) => {
            if (dialogBusy) e.preventDefault();
          }}
          onEscapeKeyDown={(e) => {
            if (dialogBusy) e.preventDefault();
          }}
        >
          <Dialog.Title>站点账号设置 · {account.name}</Dialog.Title>
          <Dialog.Description>
            设置仅作用于此站点账号下的所有渠道。
          </Dialog.Description>
          <button
            type="button"
            className="icon-button detail-close"
            aria-label="关闭站点设置"
            disabled={dialogBusy}
            onClick={close}
          >
            <X size={18} />
          </button>
          <div
            className="recharge-actions"
            role="group"
            aria-label="站点设置分类"
          >
            <button
              type="button"
              className="button small"
              aria-pressed={section === "rate"}
              disabled={dialogBusy}
              onClick={() => setSection("rate")}
            >
              倍率设置
            </button>
            <button
              type="button"
              className="button small"
              aria-pressed={section === "recharge"}
              disabled={dialogBusy}
              onClick={() => setSection("recharge")}
            >
              充值设置
            </button>
          </div>
          {section === "recharge" ? (
            <SiteRecharge key={account.id} account={account} onBusyChange={setRechargeBusy} />
          ) : (
            <form className="source-form sub-account-form" onSubmit={submit}>
              <label>
                倍率系数
                <input
                  type="number"
                  step="any"
                  min="0"
                  max="1000000"
                  required
                  value={value}
                  disabled={busy}
                  onChange={(e) => setValue(e.target.value)}
                />
              </label>
              <p className="settings-note">
                默认 1。例如设为 0.1，渠道显示倍率 0.2 将换算为
                0.02；渠道实际消费 $10 将换算为
                $1，利润随之重新计算。历史消费也按当前系数展示，原始账单和站点余额保持原值。
              </p>
              {valid && <div className="site-rate-preview" role="status">
                <p>本账号预览 · 当前系数 {precision.format(current)} → {precision.format(coefficient)} · 调整后为原展示 {precision.format(coefficient/current)} 倍</p>
                {representative && <p>「{representative.name}」原始倍率 {precision.format(representative.rate)}：展示 {precision.format(representative.rate*current)} → {precision.format(representative.rate*coefficient)} 。</p>}
              </div>}
              {!valid && (
                <p className="settings-note">
                  请输入大于 0 且不超过 1000000 的系数。
                </p>
              )}
              {error && (
                <p className="error-banner" role="alert">
                  {error}
                </p>
              )}
              <div className="sub-account-dialog-actions">
                <button
                  type="button"
                  className="button"
                  disabled={busy}
                  onClick={close}
                >
                  取消
                </button>
                <button
                  type="submit"
                  className="button primary"
                  disabled={busy || !valid}
                >
                  {busy ? "保存中…" : "保存设置"}
                </button>
              </div>
            </form>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

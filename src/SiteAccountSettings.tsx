import { useState } from "react";
import type { FormEvent } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { ApiError, controlRequest } from "./api";
import type { SubAccount } from "./Sub2apiChecks";
import { siteRateCoefficient } from "./billingRates";

export function SiteAccountSettings({
  account,
  close,
  saved,
}: {
  account: SubAccount;
  close: () => void;
  saved: () => void;
}) {
  const [value, setValue] = useState(
    String(siteRateCoefficient(account.rate_coefficient)),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const coefficient = Number(value);
  const valid =
    value.trim() !== "" &&
    Number.isFinite(coefficient) &&
    coefficient > 0 &&
    coefficient <= 1000000;
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
        if (!open && !busy) close();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content
          className="guide-dialog sub-account-dialog"
          onPointerDownOutside={(e) => {
            if (busy) e.preventDefault();
          }}
          onEscapeKeyDown={(e) => {
            if (busy) e.preventDefault();
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
            disabled={busy}
            onClick={close}
          >
            <X size={18} />
          </button>
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
              默认 1。例如设为 0.1，渠道显示倍率 0.2 将换算为 0.02；渠道实际消费
              $10 将换算为
              $1，利润随之重新计算。历史消费也按当前系数展示，原始账单和站点余额保持原值。
            </p>
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
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

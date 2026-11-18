import { useRef, useState } from "react";
import type { FormEvent } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { ApiError, controlRequest } from "./api";
import type { SubAccount } from "./Sub2apiChecks";
import { siteRateCoefficient } from "./billingRates";
import { SiteRecharge } from "./SiteRecharge";
import { CompactDialogHeader } from "./CompactDialog";

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
  const opener = useRef(document.activeElement);
  const [rechargeBusy, setRechargeBusy] = useState(false);
  const dialogBusy = busy || rechargeBusy;
  const [error, setError] = useState("");
  const [section, setSection] = useState<"rate" | "recharge" | "probe">(initialSection);
  const [probeRPM, setProbeRPM] = useState(String(account.probe_limits?.rpm_limit ?? ""));
  const [probeConcurrency, setProbeConcurrency] = useState(String(account.probe_limits?.concurrency ?? ""));
  const validLimit = (input: string, max: number) => input === "" || (Number.isInteger(Number(input)) && Number(input) >= 1 && Number(input) <= max);
  const validProbe = validLimit(probeRPM,480) && validLimit(probeConcurrency,8);
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
    const probe = section === "probe";
    if (busy || !(probe ? validProbe : valid)) return;
    setBusy(true);
    setError("");
    try {
      await controlRequest(
        `/v1/sub2api/accounts/${encodeURIComponent(account.id)}/${probe ? "probe-limits" : "settings"}`,
        {
          method: probe ? "PUT" : "PATCH",
          body: JSON.stringify(probe ? {rpm_limit:probeRPM ? Number(probeRPM) : null, concurrency:probeConcurrency ? Number(probeConcurrency) : null} : { rate_coefficient: coefficient }),
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
          className="guide-dialog compact-dialog site-settings-dialog"
          onCloseAutoFocus={event => {
            event.preventDefault();
            if (opener.current instanceof HTMLElement && opener.current.isConnected) opener.current.focus();
          }}
          onPointerDownOutside={(e) => {
            if (dialogBusy) e.preventDefault();
          }}
          onEscapeKeyDown={(e) => {
            if (dialogBusy) e.preventDefault();
          }}
        >
          <CompactDialogHeader context={account.name} closeLabel="关闭站点设置" busy={dialogBusy}>站点账号设置</CompactDialogHeader>
          <div
            className="dialog-subtabs"
            role="group"
            aria-label="站点设置分类"
          >
            <button
              type="button"
              className="button small"
              aria-pressed={section === "rate"}
              disabled={dialogBusy}
              onClick={() => { setError(""); setSection("rate"); }}
            >
              倍率设置
            </button>
            <button
              type="button"
              className="button small"
              aria-pressed={section === "recharge"}
              disabled={dialogBusy}
              onClick={() => { setError(""); setSection("recharge"); }}
            >
              充值设置
            </button>
            <button type="button" className="button small" aria-pressed={section === "probe"} disabled={dialogBusy}
              onClick={() => {setError("");setSection("probe");}}>
              检测速率
            </button>
          </div>
          {section === "recharge" ? (
            <div className="compact-dialog-scroll"><SiteRecharge key={account.id} account={account} onBusyChange={setRechargeBusy} /></div>
          ) : (
            <form className="compact-dialog-form" onSubmit={submit}>
              <div className="compact-dialog-scroll">
                {section === "probe" ? <>
                  <div className="dialog-field-grid">
                    <label className="dialog-field">每分钟检测请求上限（RPM）
                      <input type="number" min="1" max="480" step="1" value={probeRPM} disabled={busy}
                        placeholder="自动" onChange={e => setProbeRPM(e.target.value)} />
                    </label>
                    <label className="dialog-field">同时在途请求上限
                      <input type="number" min="1" max="8" step="1" value={probeConcurrency} disabled={busy}
                        placeholder="自动" onChange={e => setProbeConcurrency(e.target.value)} />
                    </label>
                  </div>
                </> : <>
                  <label className="dialog-field">
                    倍率系数
                    <input type="number" step="any" min="0" max="1000000" required value={value}
                      disabled={busy} aria-invalid={!valid} onChange={e => setValue(e.target.value)} />
                  </label>
                  {!valid && <p className="dialog-inline-note" role="alert">系数须大于 0，且不超过 1,000,000。</p>}
                  {valid && coefficient !== current && <dl className="dialog-key-values" aria-label="倍率预览">
                    <dt title="历史消费按新系数展示；不修改账单和余额。">系数</dt><dd>{precision.format(current)} → {precision.format(coefficient)}</dd>
                    {representative && <>
                      <dt title={representative.name}>{representative.name}</dt>
                      <dd>{precision.format(representative.rate * current)} → {precision.format(representative.rate * coefficient)}</dd>
                    </>}
                  </dl>}
                </>}
                {error && <p className="error-banner" role="alert">{error}</p>}
              </div>
              <div className="compact-dialog-footer">
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
                  disabled={busy || !(section === "probe" ? validProbe : valid)}
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

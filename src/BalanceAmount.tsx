import { balanceLabel, balanceTone } from "./format";

export function BalanceAmount({
  value,
  currency = "USD",
}: {
  value?: number | null;
  currency?: string;
}) {
  const known = typeof value === "number" && Number.isFinite(value);
  return (
    <span className={`amount ${balanceTone(value)}`}>
      {known
        ? balanceLabel({ position: 1, status: "ok", amount: value, currency })
        : "—"}
    </span>
  );
}

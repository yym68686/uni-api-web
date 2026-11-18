import { formatBalance, balanceTone } from "./format";

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
        ? formatBalance(value, currency)
        : "—"}
    </span>
  );
}

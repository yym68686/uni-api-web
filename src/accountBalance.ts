import { controlRequest } from "./api";
import { balanceSite, scheduleBalance } from "./balanceRequests";

export interface AccountBalance {
  amount: number | null;
  status: string;
  checked_at: number;
}

// A wallet belongs to an account, not a channel, model, source or time filter.
// Every view uses the same query so a refresh updates all references together.
export function accountBalanceQuery(user: string, id: string, base?: string) {
  return {
    queryKey: ["site-account-balance", user, id],
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      scheduleBalance(
        balanceSite(base, `account:${id}`),
        () => controlRequest<AccountBalance>(
          `/v1/sub2api/accounts/${encodeURIComponent(id)}/balance`,
          { signal },
        ),
        signal,
      ),
    retry: false,
    staleTime: 60_000,
  };
}

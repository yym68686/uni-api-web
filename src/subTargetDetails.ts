import { readSummary } from "./summaryFeed";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";

// Only a fresh, complete, owned group may supply all-model results or import
// defaults. Never treat a negotiated page projection as a complete response.
export async function readCompleteSubTarget(accountId: string, groupId: number, signal: AbortSignal): Promise<SubTarget> {
  const response = await readSummary<{ account_id?: string; target?: SubTarget }, { data: SubAccount[] }>(
    `/v1/sub2api/accounts/${encodeURIComponent(accountId)}/groups/${groupId}/details?include_rates=0`,
    undefined, signal,
  );
  const targets = response.summary
    ? response.summary.rows.flatMap(row => row.value.account_id === accountId && row.value.target ? [row.value.target] : [])
    : (response.legacy?.data || []).filter(row => row.id === accountId).flatMap(row => row.targets);
  if (targets.length !== 1 || targets[0].group_id !== groupId || !Array.isArray(targets[0].models) || targets[0].receipt_model || targets[0].summary_model)
    throw Error("渠道完整模型数据不完整，请重新读取");
  return targets[0];
}

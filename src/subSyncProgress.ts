import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { controlRequest } from "./api";
import type { SubAccount } from "./Sub2apiChecks";
import type { ManagementPage } from "./managementQuery";

export interface GroupProgress {
  group_id: number;
  total: number;
  completed: number;
  queued: number;
  running: number;
  interrupted: number;
  rejected?: number;
  skipped?: number;
  waiting?: number;
  state?: string;
  compaction_state?: string;
  tool_use_state?: string;
  tier_version?: number;
  tool_version?: number;
  model_version?: number;
}
export interface SyncProgress extends Omit<GroupProgress, "group_id"> {
  keys_ready?: number;
  keys_total?: number;
  key_errors?: number;
  running_models: string[];
  groups: GroupProgress[];
}
const active = (accounts: SubAccount[]) => accounts.some(a => a.state === "queued" || a.state === "running");
const isRowCheck = (account: SubAccount) => ["check", "quality", "compaction", "tool_use"].includes(account.job_kind || "") ||
  !account.job_kind && !!account.progress?.groups.some(g => g.waiting);
export interface ProgressResponse { data: SubAccount[]; read_at_ms: number }

export function modelProgressDetails(progress: Omit<GroupProgress, "group_id">) {
  return [
    `${progress.running} 检测中`,
    `${progress.queued + (progress.waiting || 0)} 排队`,
    progress.rejected ? `${progress.rejected} 上游拒绝` : "",
    progress.skipped ? `${progress.skipped} 已跳过` : "",
    progress.interrupted ? `${progress.interrupted} 已中断` : "",
  ].filter(Boolean).join(" · ");
}

// Polling updates account headers, not the result snapshot used by the table.
// Once a job settles, read that snapshot once so results, filters, counts and
// operation eligibility all come from the same newly verified page. Never
// turn an old queued check into a success using progress counters alone.
export function useSubSyncResultRefresh(user: string, page: ManagementPage | undefined, progress: ProgressResponse | undefined) {
  const client = useQueryClient();
  const observed = useRef<{ owner: string; response?: ProgressResponse; finishedAt: number; requestedAt: number }>({ owner: user, finishedAt: 0, requestedAt: 0 });
  useEffect(() => {
    if (observed.current.owner !== user) observed.current = { owner: user, finishedAt: 0, requestedAt: 0 };
    const state = observed.current;
    if (!progress) return;
    const previous = new Map(state.response?.data.map(a => [a.id, a]));
    const current = new Map(progress.data.map(a => [a.id, a]));
    const settled = progress.data.some(a => !active([a]) && previous.has(a.id) && active([previous.get(a.id)!]) && !isRowCheck(previous.get(a.id)!));
    // Also recover a page opened while the job was ending, before the first
    // poll observed its active state (or a retained pre-completion page).
    const stalePage = page && (page.accounts.some(a => !isRowCheck(a) && active([a]) && current.has(a.id) && !active([current.get(a.id)!]))
      || page.rows.some(row => {
        const account = current.get(row.account.id);
        const group = account?.progress?.groups?.find(g => g.group_id === row.target.group_id);
        return account && !isRowCheck(account) && !active([account]) && group && group.total > 0 && group.queued === 0 && group.running === 0 && !group.waiting
          && ([row.target.state, row.target.compaction_state, row.target.tool_use_state].some(s => s === "queued" || s === "running")
            || row.checks.some(c => c.state === "queued" || c.state === "running"));
      }));
    if (settled || stalePage) state.finishedAt = Math.max(state.finishedAt, progress.read_at_ms);
    state.response = progress;
    // Keep a completion seen during a filter/page read, then refresh only if
    // the completed page predates it. Do not cancel an in-flight long read.
    if (!page || page.read_at_ms >= state.finishedAt || state.requestedAt >= state.finishedAt) return;
    state.requestedAt = state.finishedAt;
    void client.invalidateQueries({ queryKey: ["management-query", user] }, { cancelRefetch: false });
  }, [client, user, page, progress]);
}

// An active job needs live progress, not repeated replacement of the channel
// table. Scope each poll stream to its freshly read page and stop on completion.
export function useSubSyncProgress(user: string, accounts: SubAccount[], readAt: number | undefined, submission = 0, visibleAccounts?: string[], verifying = false) {
  const client=useQueryClient();
  const scope = new Set(visibleAccounts ?? accounts.map(a => a.id));
  const queryKey=["sub-sync-progress", user, JSON.stringify([...scope].sort()), submission];
  type Response = ProgressResponse;
  return useQuery({
    queryKey,
    enabled: !!readAt && accounts.length>0,
    queryFn: async ({ signal }) => {
      const previous=client.getQueryData<Response>(queryKey);
      // Keep polling each visible group, including a job that ended before
      // the first poll. The accounts view can reuse unchanged idle summaries.
      const watched=visibleAccounts ? [...scope] : (previous?.data || accounts)
        .filter(a=>scope.has(a.id) && (!previous || !a.progress || active([a]))).map(a=>a.id);
      // The API caps the watch list and the proxy also bounds URL length.
      // Channel rows watch only the displayed accounts, not the full catalog.
      const batches: string[][] = [[]];
      for (const id of watched) {
        const batch = batches[batches.length - 1];
        if (batch.length && (batch.length >= 128 || encodeURIComponent(JSON.stringify([...batch, id])).length > 5500)) batches.push([id]);
        else batch.push(id);
      }
      let response: Response = {data: [], read_at_ms: 0};
      const summaries = new Map<string, SubAccount>();
      for (const batch of batches) {
        response = await controlRequest<Response>("/v1/sub2api/accounts/progress?accounts="+encodeURIComponent(JSON.stringify(batch)), { signal, cache: "no-store" });
        if (!Array.isArray(response.data) || !(response.read_at_ms > 0)) throw Error("任务进度不完整");
        for (const a of response.data) if (a.progress) summaries.set(a.id, a);
      }
      const old=new Map(previous?.data.map(a=>[a.id,a]));
      return {...response,data:response.data.map(a=>{
        const before=summaries.get(a.id) ?? old.get(a.id);
        if(a.progress || !before?.progress || active([a]) || a.state!==before.state || a.synced_at!==before.synced_at || a.job_kind!==before.job_kind || a.sync_only!==before.sync_only || JSON.stringify(a.sync_models)!==JSON.stringify(before.sync_models))return a;
        return {...a,progress:before.progress,active_groups:before.active_groups,completed_groups:before.completed_groups};
      })};
    },
    refetchInterval: query => !verifying && query.state.data && !active(query.state.data.data) && !query.state.data.data.some(a=>scope.has(a.id) && !a.progress) ? false : 3000,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    retry: false,
  });
}

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { controlRequest } from "./api";
import type { SubAccount } from "./Sub2apiChecks";

export interface GroupProgress {
  group_id: number;
  total: number;
  completed: number;
  queued: number;
  running: number;
  interrupted: number;
}
export interface SyncProgress extends Omit<GroupProgress, "group_id"> {
  running_models: string[];
  groups: GroupProgress[];
}
const active = (accounts: SubAccount[]) => accounts.some(a => a.state === "queued" || a.state === "running");

// An active job needs live progress, not repeated replacement of the channel
// table. Scope each poll stream to its freshly read page and stop on completion.
export function useSubSyncProgress(user: string, accounts: SubAccount[], readAt: number | undefined, submission = 0) {
  const client=useQueryClient();
  const queryKey=["sub-sync-progress", user, readAt, submission];
  type Response = {data:SubAccount[];read_at_ms:number};
  return useQuery({
    queryKey,
    enabled: !!readAt && accounts.length>0,
    queryFn: async ({ signal }) => {
      const previous=client.getQueryData<Response>(queryKey);
      // First read includes completed accounts too. Subsequent reads only
      // aggregate active jobs, missing summaries or changed job identities.
      const watched=(previous?.data || accounts).filter(a=>!previous || !a.progress || active([a])).map(a=>a.id);
      const response = await controlRequest<Response>("/v1/sub2api/accounts/progress?accounts="+encodeURIComponent(JSON.stringify(watched)), { signal, cache: "no-store" });
      if (!Array.isArray(response.data) || !(response.read_at_ms > 0)) throw Error("任务进度不完整");
      const old=new Map(previous?.data.map(a=>[a.id,a]));
      return {...response,data:response.data.map(a=>{
        const before=old.get(a.id);
        if(a.progress || !before?.progress || active([a]) || a.state!==before.state || a.synced_at!==before.synced_at || a.job_kind!==before.job_kind || a.sync_only!==before.sync_only || JSON.stringify(a.sync_models)!==JSON.stringify(before.sync_models))return a;
        return {...a,progress:before.progress,active_groups:before.active_groups,completed_groups:before.completed_groups};
      })};
    },
    refetchInterval: query => query.state.data && !active(query.state.data.data) && !query.state.data.data.some(a=>!a.progress) ? false : 3000,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    retry: false,
  });
}

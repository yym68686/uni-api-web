import { useQuery } from "@tanstack/react-query";
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
export function useSubSyncProgress(user: string, accounts: SubAccount[], readAt: number | undefined) {
  const watched=accounts.filter(a=>a.state === "queued" || a.state === "running").map(a=>a.id);
  return useQuery({
    queryKey: ["sub-sync-progress", user, readAt],
    enabled: !!readAt && active(accounts),
    queryFn: async ({ signal }) => {
      const response = await controlRequest<{ data: SubAccount[]; read_at_ms: number }>("/v1/sub2api/accounts/progress?accounts="+encodeURIComponent(JSON.stringify(watched)), { signal, cache: "no-store" });
      if (!Array.isArray(response.data) || !(response.read_at_ms > 0)) throw Error("任务进度不完整");
      return response;
    },
    refetchInterval: query => query.state.data && !active(query.state.data.data) ? false : 3000,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    retry: false,
  });
}

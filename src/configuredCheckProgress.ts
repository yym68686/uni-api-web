import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ApiError, controlRequest } from "./api";
import { channelMembers } from "./channelManagement";
import type { ManagementPage } from "./managementQuery";

type ActiveCheck = {
  source_id: string;
  provider: string;
  kind: "model" | "availability" | "compaction" | "tool-use";
  model: string;
  fingerprint: string;
};
const pending = (state?: string) => state === "queued" || state === "running";
const interval = 3000;
const memberKey = ({ source_id, provider }: Pick<ActiveCheck, "source_id" | "provider">) =>
  JSON.stringify([source_id, provider]);
const workKey = (kind: string, model = "") => JSON.stringify([kind, model]);

function watchPage(page: ManagementPage) {
  return page.rows.flatMap(({ account, configured, checks, target }) => {
    if (account.id || !configured) return [];
    const jobs = checks.filter(check => pending(check.state)).map(check => workKey("model", check.model));
    if (pending(target.compaction_state)) jobs.push(workKey("compaction"));
    if (pending(target.tool_use_state)) jobs.push(workKey("tool-use"));
    return jobs.length ? [{ jobs, members: channelMembers(configured) }] : [];
  });
}

// A merged row can have multiple real source/provider pairs. Bound each GET
// by its encoded size so even a page of long labels fits proxy URL limits.
function progressPaths(watched: ReturnType<typeof watchPage>) {
  const unique = new Map(watched.flatMap(row => row.members.map(member => [memberKey(member), member] as const)));
  const paths: string[] = [];
  let batch: Pick<ActiveCheck, "source_id" | "provider">[] = [];
  const path = (targets: typeof batch) => "/v1/channel-management/checks/progress?" +
    new URLSearchParams({ targets: JSON.stringify(targets) });
  for (const { source_id, provider } of unique.values()) {
    const scope = { source_id, provider };
    if (batch.length && (batch.length === 128 || path([...batch, scope]).length > 6000)) {
      paths.push(path(batch));
      batch = [];
    }
    batch.push(scope);
  }
  if (batch.length) paths.push(path(batch));
  return paths;
}

export function useConfiguredCheckResultRefresh(user: string, page?: ManagementPage) {
  const client = useQueryClient();
  useEffect(() => {
    if (!page) return;
    const watched = watchPage(page);
    const paths = progressPaths(watched);
    if (!paths.length) return;

    const controller = new AbortController();
    const { signal } = controller;
    let timer: ReturnType<typeof setTimeout>;
    const refreshPage = () => void client.invalidateQueries({
      queryKey: ["management-query", user],
      predicate: query => query.state.data === page,
    }, { cancelRefetch: false });
    async function poll() {
      if (signal.aborted) return;
      if (document.visibilityState !== "hidden") {
        try {
          const responses = await Promise.all(paths.map(path => controlRequest<{ data: ActiveCheck[] }>(path, { signal })));
          if (signal.aborted) return;
          if (responses.every(response => Array.isArray(response.data))) {
            const active = new Map<string, ActiveCheck[]>();
            for (const check of responses.flatMap(response => response.data)) {
              const key = memberKey(check);
              const list = active.get(key);
              if (list) list.push(check);
              else active.set(key, [check]);
            }
            const settled = watched.some(({ jobs, members }) => {
              const remaining = new Set(members.flatMap(member => (active.get(memberKey(member)) || []).flatMap(check => {
                if (check.fingerprint && member.probe_fingerprint && check.fingerprint !== member.probe_fingerprint) return [];
                if (check.kind === "model" || check.kind === "availability") return [workKey("model", check.model)];
                return [workKey(check.kind)];
              })));
              return jobs.some(job => !remaining.has(job));
            });
            if (settled) {
              // Use the authoritative page for results, filters and button
              // eligibility. Progress isn't evidence of a successful check.
              refreshPage();
              return;
            }
          }
        } catch (error) {
          if (signal.aborted) return;
          if (error instanceof ApiError && [401, 403].includes(error.status)) {
            refreshPage();
            return;
          }
        }
      }
      timer = setTimeout(() => void poll(), interval);
    }
    timer = setTimeout(() => void poll(), interval);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [client, user, page]);
}

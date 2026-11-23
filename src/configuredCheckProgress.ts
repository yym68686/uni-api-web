import { useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ApiError, controlRequest } from "./api";
import { channelMembers, managementRows } from "./channelManagement";
import type { ConfiguredCheck, ManagedChannel, ManagementRow } from "./channelManagement";
import type { ManagementPage } from "./managementQuery";
import { managementCandidateRows } from "./managementQueryFilters";
import type { SubFilters } from "./sub2apiPreferences";
import { readSummary } from "./summaryFeed";

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

function watchRows(rows: ManagementRow[]) {
  return rows.flatMap(row => {
    const { account, configured, checks, target } = row;
    if (account.id || !configured) return [];
    const jobs = checks.filter(check => pending(check.state)).map(check => workKey("model", check.model));
    if (pending(target.compaction_state)) jobs.push(workKey("compaction"));
    if (pending(target.tool_use_state)) jobs.push(workKey("tool-use"));
    return jobs.length ? [{ row, jobs, members: channelMembers(configured) }] : [];
  });
}

// A merged row can have multiple real source/provider pairs. Bound each GET
// by its encoded size so even a page of long labels fits proxy URL limits.
function progressPaths(watched: ReturnType<typeof watchRows>) {
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

async function readRowChecks(rows: ManagementRow[], model: string, signal: AbortSignal) {
  const members = new Map(rows.flatMap(row => channelMembers(row.configured!).map(member => [memberKey(member), member] as const)));
  const results = await Promise.all([...members].map(async ([key, { source_id, provider }]) => {
    const path = "/v1/channel-management/checks?" + new URLSearchParams({ source_id, provider });
    const { summary, legacy } = await readSummary<ConfiguredCheck, { data: ConfiguredCheck[] }>(path, undefined, signal);
    const checks = summary?.rows.map(({ value }) => ({ ...value, details_omitted: true })) ?? legacy?.data;
    if (!Array.isArray(checks) || checks.some(check => memberKey(check) !== key)) throw Error("渠道检测结果范围不一致");
    return [key, checks] as const;
  }));
  signal.throwIfAborted();
  const byMember = new Map(results);
  return rows.map(row => {
    const configured = row.configured!;
    const evidence = channelMembers(configured).flatMap(member => byMember.get(memberKey(member)) || []);
    const updated = managementRows([], [configured], [], model, "", evidence).find(candidate => candidate.configured);
    if (!updated) throw Error("渠道检测结果暂不可用");
    const { models, details_omitted, history, quality_check, check_source_id, compaction, compaction_state, tool_use, tool_use_state } = updated.target;
    return { ...row, checks: updated.checks, selected: updated.selected, target: {
      ...row.target, models, details_omitted, history, quality_check, check_source_id, compaction, compaction_state, tool_use, tool_use_state,
    } };
  });
}

type RowChanges = {
  base: ManagementPage;
  patches: Map<string, ManagementRow>;
  requested: Set<string>;
};
const noRows: ManagementRow[] = [];

export function useConfiguredCheckResultRefresh(user: string, filters: SubFilters, page?: ManagementPage) {
  const client = useQueryClient();
  const [changes, setChanges] = useState<RowChanges>();
  const current = useRef(page);
  current.current = page;
  const patches = changes?.base === page ? changes?.patches : undefined;
  const requested = changes?.base === page ? changes?.requested : undefined;
  const rows = useMemo(() => patches && page ? page.rows.map(row => patches.get(row.id) || row) : page?.rows ?? noRows, [page, patches]);

  const markStale = () => void client.invalidateQueries({ queryKey: ["management-query", user], refetchType: "none" });
  function afterSubmission(targets: Pick<ActiveCheck, "source_id" | "provider">[]) {
    markStale();
    const base = current.current;
    if (!base || !targets.length) return;
    const members = new Set(targets.map(memberKey));
    const ids = base.rows.filter(row => !row.account.id && row.configured &&
      channelMembers(row.configured).some(member => members.has(memberKey(member)))).map(row => row.id);
    if (!ids.length) return;
    setChanges(previous => ({
      base,
      patches: previous?.base === base ? previous.patches : new Map(),
      requested: new Set([...(previous?.base === base ? previous.requested : []), ...ids]),
    }));
  }

  useEffect(() => {
    if (!page) return;
    const watched = watchRows(rows);
    const paths = progressPaths(watched);
    const submitted = rows.filter(row => requested?.has(row.id));
    if (!paths.length && !submitted.length) return;

    const controller = new AbortController();
    const { signal } = controller;
    let timer: ReturnType<typeof setTimeout>;
    const reconcilePage = () => void client.invalidateQueries({
      queryKey: ["management-query", user],
      predicate: query => query.state.data === page,
    }, { cancelRefetch: false });
    const updateRows = async (affected: ManagementRow[]) => {
      const refreshed = await readRowChecks(affected, filters.model, signal);
      if (signal.aborted || current.current !== page) return;
      // Native results do not change advertised rates or stable ordering. If a
      // result actually removes a row from the filter, fetch the replacement
      // and updated counts from the server rather than leave a hole in the page.
      if (managementCandidateRows(refreshed, filters, page.context.prices).length !== refreshed.length) {
        reconcilePage();
        return;
      }
      void client.invalidateQueries({ queryKey: ["management-query", user], refetchType: "none" });
      setChanges(previous => ({
        base: page,
        patches: new Map([...(previous?.base === page ? previous.patches : []), ...refreshed.map(row => [row.id, row] as const)]),
        requested: new Set([...(previous?.base === page ? previous.requested : [])].filter(id => !refreshed.some(row => row.id === id))),
      }));
    };
    async function poll() {
      if (signal.aborted) return;
      if (document.visibilityState !== "hidden") {
        try {
          if (submitted.length) {
            await updateRows(submitted);
            return;
          }
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
            const settled = watched.filter(({ jobs, members }) => {
              const remaining = new Set(members.flatMap(member => (active.get(memberKey(member)) || []).flatMap(check => {
                if (check.fingerprint && member.probe_fingerprint && check.fingerprint !== member.probe_fingerprint) return [];
                if (check.kind === "model" || check.kind === "availability") return [workKey("model", check.model)];
                return [workKey(check.kind)];
              })));
              return jobs.some(job => !remaining.has(job));
            });
            if (settled.length) {
              // Progress is only a hint to fetch these rows, never evidence
              // of a successful check.
              await updateRows(settled.map(({ row }) => row));
              return;
            }
          }
        } catch (error) {
          if (signal.aborted) return;
          if (error instanceof ApiError && [401, 403].includes(error.status)) {
            reconcilePage();
            return;
          }
        }
      }
      timer = setTimeout(() => void poll(), interval);
    }
    timer = setTimeout(() => void poll(), submitted.length ? 0 : interval);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [client, user, page, rows, requested, filters]);

  return {
    afterSubmission,
    awaiting: (configured?: ManagedChannel) => !!configured && !!requested &&
      rows.some(row => row.configured === configured && requested.has(row.id)),
    displayRows: (shown?: ManagementPage) => shown === page ? rows :
      shown && changes?.base === shown ? shown.rows.map(row => changes.patches.get(row.id) || row) : shown?.rows ?? noRows,
  };
}

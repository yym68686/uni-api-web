import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { ManagementRow } from "./channelManagement";
import type { ManagementPage } from "./managementQuery";
import { managementCandidateRows } from "./managementQueryFilters";
import { modelChecks } from "./sub2apiResults";
import { readCompleteSubTarget } from "./subTargetDetails";
import type { ProgressResponse } from "./subSyncProgress";
import type { SubFilters } from "./sub2apiPreferences";

const pending = (state?: string) => state === "queued" || state === "running";
type Work = {row: ManagementRow; version: string; submittedKey?: string; submittedAt?: number};

// Progress is a hint, never evidence of a result. Read changed rows with a
// small pool, independently of account completion. A new poll can replace
// queued work without canceling slow reads already in flight.
export function useSiteCheckProgress(owner: string, filters: SubFilters, page: ManagementPage | undefined, progress: ProgressResponse | undefined, submissions: Map<string, number>) {
  const client = useQueryClient();
  const [patch, setPatch] = useState<{ page: ManagementPage; rows: Map<string, ManagementRow> }>();
  const scope = useRef<{ page: ManagementPage; abort: AbortController; versions: Map<string, string>;
    requested: Map<string, Work>; active: Set<string> }>(undefined);
  useEffect(() => {
    if (!page) return;
    const abort = new AbortController();
    scope.current = { page, abort, versions: new Map(), requested: new Map(), active: new Set() };
    return () => abort.abort();
  }, [owner, page]);
  useEffect(() => {
    const state = scope.current;
    if (!state || !page || state.page !== page || !progress) return;
    const { signal } = state.abort;
    const accounts = new Map(progress.data.map(account => [
      account.id, new Map(account.progress?.groups.map(group => [group.group_id, group])),
    ]));
    for (const row of page.rows) {
      const group = accounts.get(row.account.id)?.get(row.target.group_id);
      // Older backends lack row state; retain the account-settled fallback.
      if (!group?.state) continue;
      const version = JSON.stringify(group);
      const before = state.versions.get(row.id);
      const submittedKey = JSON.stringify([owner, row.account.id, row.target.group_id]);
      const submittedAt = submissions.get(submittedKey);
      const submitted = submittedAt !== undefined && submittedAt < progress.read_at_ms;
      if (before === version && !submitted) continue;
      if (!submitted && before === undefined && (progress.read_at_ms <= page.read_at_ms ||
        ![row.target.state, row.target.compaction_state, row.target.tool_use_state, group.state,
          group.compaction_state, group.tool_use_state].some(pending) && !row.checks.some(check => pending(check.state)))) {
        state.versions.set(row.id, version);
        continue;
      }
      state.requested.set(row.id, { row, version, submittedKey, submittedAt });
    }
    function pump() {
      if (signal.aborted || !state || !page) return;
      for (const [id, { row, version, submittedKey, submittedAt }] of state.requested) {
        if (state.active.size >= 4) break;
        if (state.active.has(id)) continue;
        state.requested.delete(id);
        const submitted = submittedKey && submittedAt !== undefined && submissions.get(submittedKey) === submittedAt;
        if (state.versions.get(id) === version && !submitted) continue;
        state.active.add(id);
        void readCompleteSubTarget(row.account.id, row.target.group_id, signal).then(result => {
          if (signal.aborted) return;
          const target = { ...row.target, ...result, list_overview: undefined, receipt_model: undefined, summary_model: undefined,
            observed_rates: row.target.observed_rates };
          const checks = modelChecks(target);
          const updated = { ...row, target, checks, selected: checks.find(check => check.model === filters.model) };
          state.versions.set(id, version);
          if (submittedKey && submitted && submissions.get(submittedKey) === submittedAt) submissions.delete(submittedKey);
          if (!managementCandidateRows([updated], filters, page.context.prices).length) {
            void client.invalidateQueries({queryKey: ["management-query", owner], predicate: query => query.state.data === page}, {cancelRefetch: false});
            return;
          }
          setPatch(old => ({ page, rows: new Map([...(old?.page === page ? old.rows : []), [id, updated]]) }));
        }).catch(() => {
          // Keep previous evidence and retry on the next progress read.
        }).finally(() => {
          state.active.delete(id);
          pump();
        });
      }
    }
    pump();
  }, [client, owner, filters, page, progress, submissions]);
  return (row: ManagementRow, displayed?: ManagementPage) => displayed && patch?.page === displayed ? patch.rows.get(row.id) || row : row;
}

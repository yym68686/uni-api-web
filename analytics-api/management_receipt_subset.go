package main

import (
	"context"
	"encoding/json"
	"log"
)

// Entries are immutable after admission. Snapshot only the at-most-eight
// pointers under the existing lock; decode outside it into request-local data.
func (c *managementReceiptSets) currentEntries(version managementReceiptSetVersion) []*managementReceiptSet {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*managementReceiptSet
	for i := len(c.entries) - 1; i >= 0; i-- {
		entry := c.entries[i]
		v := entry.version
		if v.database == version.database && v.owner == version.owner && v.stamp == version.stamp {
			out = append(out, entry)
		}
	}
	return out
}

// Reuse valid receipts and certified projection absences under the exact current
// source/ownership proof, just as an exact selected-ID cache hit already does.
// Invalid rows still require SQL and strict fallback. An absent projection stays
// absent from the result map, so it NEVER bypasses the later raw receipt decoder.
// Both encoded rows and absent IDs share the existing 8-entry/8MiB budget;
// selected sets over 4096 IDs retain their original owner scan.
func managementReceiptSubset(ctx context.Context, db controlQueryDB, owner string, ids []string) (map[string]managementStreamReceipt, []string, error) {
	scope := managementReceiptSetScopeFor(ctx, db, owner, ids)
	if scope == nil {
		return nil, ids, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	entries := scope.store.currentEntries(scope.version)
	if len(entries) == 0 {
		return nil, ids, nil
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	out := map[string]managementStreamReceipt{}
	reusedAbsent := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		var rows []managementCachedReceipt
		if json.Unmarshal(entry.body, &rows) != nil {
			continue
		}
		var absent []string
		if len(entry.absent) != 0 && json.Unmarshal(entry.absent, &absent) != nil {
			continue
		}
		for _, row := range rows {
			if row.Valid && wanted[row.ID] {
				out[row.ID] = managementStreamReceipt{valid: true, evidence: row.Evidence, usage: row.Usage}
				delete(wanted, row.ID)
			}
		}
		for _, id := range absent {
			if wanted[id] {
				delete(wanted, id)
				reusedAbsent++
			}
		}
		if len(wanted) == 0 {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if len(out) == 0 && reusedAbsent == 0 {
		return nil, ids, nil
	}
	missing := make([]string, 0, len(wanted))
	for _, id := range ids {
		if wanted[id] {
			missing = append(missing, id)
		}
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_receipt_subset_cache reused=%d raw_ids=%d absent=%d request_id=%s", len(out), len(missing), reusedAbsent, consoleRequestID(ctx))
	}
	return out, missing, nil
}

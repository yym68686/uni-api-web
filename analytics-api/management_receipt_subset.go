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

// Reuse only PRESENT, VALID receipts with the exact current source/ownership
// proof. Valid typed rows are identical in strict and non-strict reads; invalid
// or absent rows NEVER bypass SQL, its strict fallback, or the raw decoder.
// No new evidence/cache/TTL is retained. The existing 8-entry/8MiB budget bounds
// decoding; selected sets over 4096 IDs retain their original owner scan.
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
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		var rows []managementCachedReceipt
		if json.Unmarshal(entry.body, &rows) != nil {
			continue
		}
		for _, row := range rows {
			if row.Valid && wanted[row.ID] {
				out[row.ID] = managementStreamReceipt{valid: true, evidence: row.Evidence, usage: row.Usage}
				delete(wanted, row.ID)
			}
		}
		if len(wanted) == 0 {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if len(out) == 0 {
		return nil, ids, nil
	}
	missing := make([]string, 0, len(wanted))
	for _, id := range ids {
		if wanted[id] {
			missing = append(missing, id)
		}
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_receipt_subset_cache reused=%d raw_ids=%d request_id=%s", len(out), len(missing), consoleRequestID(ctx))
	}
	return out, missing, nil
}

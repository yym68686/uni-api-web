package main

import (
	"context"
	"encoding/json"
	"log"
	"slices"
	"strings"
	"unsafe"
)

// A projection absence cannot resolve a raw receipt. Only a successful raw SQL
// read may certify absence, and only management's complete current RR proof may
// reuse it. The proof covers raw receipt inserts/deletes/reassignment and account
// ownership. Ordinary/detail/selection readers retain their original SQL.
// Certificates share the 32-entry/8MiB receipt budget but have a
// separate kind, so neither projection rows nor projection absences enter here.
func readManagementRawReceiptAbsences(ctx context.Context, db controlQueryDB, owner string, ids []string, current map[string]*subUsage, read func([]string) error) error {
	scope := managementReceiptSetScopeFor(ctx, db, owner, ids)
	if scope == nil {
		return read(ids)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	version := scope.version
	version.rawAbsence = true
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	reused := 0
	for _, entry := range scope.store.currentEntries(version) {
		if err := ctx.Err(); err != nil {
			return err
		}
		var absent []string
		if json.Unmarshal(entry.absent, &absent) != nil {
			continue
		}
		for _, id := range absent {
			if wanted[id] {
				delete(wanted, id)
				reused++
			}
		}
		if len(wanted) == 0 {
			break
		}
	}
	rawIDs := ids
	if reused > 0 {
		rawIDs = make([]string, 0, len(wanted))
		for _, id := range ids {
			if wanted[id] {
				rawIDs = append(rawIDs, id)
			}
		}
	}
	if len(rawIDs) > 0 {
		if err := read(rawIDs); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// The callback has completed the original typed raw decoder. A returned row
	// (even an unsupported/empty one) is never stored as absent. Embedded probe
	// usage remains unchanged for missing rows, just as on the original path.
	var absent []string
	for _, id := range rawIDs {
		if _, found := current[id]; !found {
			absent = append(absent, id)
		}
	}
	stored := false
	if len(absent) > 0 {
		slices.Sort(absent)
		absent = slices.Compact(absent)
		body, err := json.Marshal(absent)
		version.ids = tokenHash(string(body))
		n := int(unsafe.Sizeof(managementReceiptSet{})) + len(version.owner) + len(version.stamp) + len(version.ids) + len(body)
		stored = err == nil && n <= managementReceiptSetBudget
		if stored {
			version.owner, version.stamp = strings.Clone(version.owner), strings.Clone(version.stamp)
			stored = storeManagementReceiptSet(ctx, scope.store, &managementReceiptSet{version: version, absent: append(make([]byte, 0, len(body)), body...), bytes: n})
		}
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_raw_receipt_absence reused=%d raw_ids=%d stored=%t phase=%s request_id=%s", reused, len(rawIDs), stored, managementReceiptPhase(ctx), consoleRequestID(ctx))
	}
	return nil
}

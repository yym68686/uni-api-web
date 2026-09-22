package main

import (
	"fmt"
	duckdb "github.com/duckdb/duckdb-go/v2"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseBudgetAndSafeFailureCategories(t *testing.T) {
	for _, mb := range []int{512, 1024} {
		e, err := OpenEngine(filepath.Join(t.TempDir(), "test.duckdb"), Config{Timezone: "UTC", DatabaseMemoryLimitMB: mb})
		if err != nil {
			t.Fatal(err)
		}
		// current_setting returns a human-readable budget; query the normalized bytes.
		var setting string
		err = e.DB.QueryRow("SELECT current_setting('memory_limit')").Scan(&setting)
		if err != nil || (mb == 1024 && setting != "1.0 GiB") || (mb == 512 && setting != "512.0 MiB") {
			t.Fatal(mb, setting, err)
		}
		e.Close()
	}
	wrapped := fmt.Errorf("apply: %w", &duckdb.Error{Type: duckdb.ErrorTypeTransaction, Msg: "TransactionContext Error: Failed to commit: failed to pin block of size 256.0 KiB (488.2 MiB/488.2 MiB used) secret"})
	if d := checkpointFailure(checkpointStageError("apply", wrapped)); d.Class != "database_out_of_memory" || strings.Contains(fmt.Sprint(d), "secret") {
		t.Fatal(d)
	}
	if importErrorClass(wrapped) != "database_out_of_memory" {
		t.Fatal(importErrorClass(wrapped))
	}
}

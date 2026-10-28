package main

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

type managementCatalogQueryRecorder struct {
	controlQueryDB
	predicateReads int
}

func (r *managementCatalogQueryRecorder) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.HasPrefix(query, managementModelStreamSQL) {
		r.predicateReads++
	}
	return r.controlQueryDB.QueryContext(ctx, query, args...)
}

func TestManagementCatalogPreservesDirectoryAndLegacyEvidenceRead(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	for _, compact := range []bool{false, true} {
		if compact {
			prepareManagementRevisionsForTest(t, s.control.db)
		}
		recorder := &managementCatalogQueryRecorder{controlQueryDB: s.control.db}
		revision, got, err := readManagementRevisionCatalogFrom(ctx, recorder, "owner", true)
		if err != nil {
			t.Fatal(err)
		}
		wantRevision, want, err := readManagementJoinedRevisionCatalogFrom(ctx, s.control.db, "owner", true)
		if err != nil {
			t.Fatal(err)
		}
		if compact {
			var ready bool
			wantRevision, ready, err = readManagementCompactRevision(ctx, s.control.db, "owner")
			if err != nil || !ready {
				t.Fatal("compact fixture is not ready", err)
			}
		}
		if revision != wantRevision || !reflect.DeepEqual(got, want) {
			t.Fatal("catalog-only directory/revision changed", compact, len(got), len(want))
		}
		wantReads := 1
		if compact {
			wantReads = 0
		}
		if recorder.predicateReads != wantReads {
			t.Fatal("catalog lost legacy evidence read or repeated prepared evidence", compact, recorder.predicateReads)
		}
	}
}

package main

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

type managementQualityReadRecorder struct {
	controlQueryDB
	targetQueries []string
}

func (d *managementQualityReadRecorder) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.HasPrefix(q, "SELECT t.account_id,t.group_id,t.name,t.platform") {
		d.targetQueries = append(d.targetQueries, q)
	}
	return d.controlQueryDB.QueryContext(ctx, q, args...)
}

func TestManagementReusesCompleteQualityTotalsOnlyWithinScope(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	db := s.control.db
	if _, err := db.Exec(`INSERT INTO console_quality_history(check_id,account_id,group_id,checked_at,verdict,successful,result) SELECT 'fixture-'||g,'a',1,g,'pass',true,'{"quality":{"status":"success"}}' FROM generate_series(1,5)g`); err != nil {
		t.Fatal(err)
	}
	shared, err := readSharedQualityProjectionFrom(ctx, db, "owner", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	read := func(q sharedQuality) ([]subAccount, string) {
		t.Helper()
		accounts, err := readSubAccountHeaders(ctx, db, "owner", "")
		if err != nil {
			t.Fatal(err)
		}
		recorder := &managementQualityReadRecorder{controlQueryDB: db}
		if err = s.readSubAccountDetailsWithModelsFrom(ctx, recorder, "owner", "", 0, "", true, true, accounts, nil, q, nil, nil, true); err != nil {
			t.Fatal(err)
		}
		if len(recorder.targetQueries) != 1 {
			t.Fatal("unexpected target queries")
		}
		return accounts, recorder.targetQueries[0]
	}
	optimized, query := read(shared)
	if strings.Contains(query, "console_quality_totals") {
		t.Fatal("complete totals scanned twice")
	}
	prior := shared
	prior.complete = false
	original, query := read(prior)
	if !strings.Contains(query, "console_quality_totals") || !reflect.DeepEqual(optimized, original) {
		t.Fatal("quality totals changed")
	}
	if optimized[0].Targets[0].History.Total != 5 || optimized[0].Targets[1].History.Total != 0 {
		t.Fatal("history scope changed")
	}
	for _, wrong := range []sharedQuality{{complete: true, owner: "foreign", Groups: map[string]sharedQualityProjection{}}, {complete: true, owner: "owner", account: "another", Groups: map[string]sharedQualityProjection{}}} {
		_, query := read(wrong)
		if !strings.Contains(query, "console_quality_totals") {
			t.Fatal("incomplete scope bypassed current totals")
		}
	}
}

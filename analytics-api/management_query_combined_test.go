package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestManagementCombinedReadPreservesFullSelectionRevision(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	db := s.control.db
	compare := func() string {
		t.Helper()
		wantRows, err := readCurrentManagementModelSignals(ctx, db, "owner")
		if err != nil {
			t.Fatal(err)
		}
		wantRevision, err := readManagementModelRevisionFrom(ctx, db, "owner")
		if err != nil {
			t.Fatal(err)
		}
		rows, revision, err := readCurrentManagementSignalsAndRevision(ctx, db, "owner")
		if err != nil || revision != wantRevision || !reflect.DeepEqual(rows, wantRows) {
			t.Fatal("combined page read diverged from full-selection evidence", err)
		}
		return revision
	}
	before := compare()
	for _, q := range []string{
		`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','234') WHERE group_id=61`,
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{input_price}','42') WHERE id='q-61-gpt-5.5'`,
		`UPDATE console_sub_models SET message='new current diagnostic' WHERE group_id=61`,
		`INSERT INTO console_sub_check_queue(account_id,group_id,kind,model) VALUES('a',1,'model','gpt-5.5')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
		after := compare()
		if after == before {
			t.Fatal("omitted display evidence failed to change revision")
		}
		before = after
	}
	rows, revision, err := readCurrentManagementSignalsAndRevision(ctx, db, "foreign")
	foreignRevision, foreignErr := readManagementModelRevisionFrom(ctx, db, "foreign")
	if err != nil || foreignErr != nil || len(rows) != 0 || revision != foreignRevision {
		t.Fatal("foreign owner scope changed", err, foreignErr)
	}
	for _, q := range []string{
		`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','"unsupported"') WHERE group_id=61`,
		`DELETE FROM console_management_model_signal WHERE group_id=61`,
		`UPDATE console_management_projection_state SET phase='models',complete=false WHERE singleton`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
		rows, revision, err = readCurrentManagementSignalsAndRevision(ctx, db, "owner")
		if !errors.Is(err, errManagementPredicateFallback) || rows != nil || revision != "" {
			t.Fatal("unsafe partial projection was returned", err)
		}
		if _, err := db.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','234') WHERE group_id=61`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE console_management_projection_state SET phase='complete',complete=true WHERE singleton`); err != nil {
			t.Fatal(err)
		}
		compare()
	}
}

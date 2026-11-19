package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestManagementAvailabilityUsesGroupSignalsWithoutChangingPageOrSelection(t *testing.T) {
	s := managementHydrationFixture(t)
	db := s.control.db
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE console_sub_models SET result=NULL WHERE group_id=2;
		UPDATE console_sub_models SET result=jsonb_set(result,'{availability,status}','"error"') WHERE group_id=1 AND model='gpt-5.5';
		UPDATE console_sub_targets SET result='{"availability":{"status":"error"}}' WHERE group_id IN (2,3);
		DELETE FROM console_sub_models WHERE group_id=2 AND model='gpt-6-astra';
		INSERT INTO console_sub_check_queue(account_id,group_id,kind,model) VALUES ('a',3,'check','gpt-6-astra')`); err != nil {
		t.Fatal(err)
	}
	prepareManagementRevisionsForTest(t, db)
	light := context.WithValue(ctx, managementListViewKey{}, true)
	for _, status := range []string{"success", "error"} {
		f := managementQueryFilters{GroupStatus: "all", Availability: status}
		full, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", true)
		if err != nil {
			t.Fatal(err)
		}
		page, err := s.readManagementQuerySnapshot(light, "owner", f, "", false)
		if err != nil || page.Evidence.siteAvailability == nil {
			t.Fatal("eligible API list did not use scoped availability", err)
		}
		page.Page.ReadAtMS = full.Page.ReadAtMS
		managementListRows(&full.Page)
		if mustJSON(page.Page) != mustJSON(full.Page) || len(page.Filtered.Rows) != len(full.Filtered.Rows) {
			t.Fatal("page or selection revision changed", status)
		}
		for i, r := range page.Filtered.Rows {
			if r.ID != full.Filtered.Rows[i].ID {
				t.Fatal("membership/order changed", status, i)
			}
		}
	}
}

func TestManagementAvailabilityUsesWinningAliasNotAnyMember(t *testing.T) {
	s := managementHydrationFixture(t)
	db := s.control.db
	if _, err := db.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,status}','"error"') WHERE group_id=2 AND model='extra-model'`); err != nil {
		t.Fatal(err)
	}
	prepareManagementRevisionsForTest(t, db)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	compact, err := s.readManagementDatabaseModeFrom(ctx, tx, "owner", nil, true, "", true)
	if err != nil {
		t.Fatal(err)
	}
	full, err := s.readManagementDatabaseModeFrom(ctx, tx, "owner", nil, true, "", false)
	if err != nil {
		t.Fatal(err)
	}
	member := func(id string, group int64, upstream string) managementChannel {
		return managementChannel{subInstalledChannel: subInstalledChannel{SourceID: id, Provider: "provider", Kind: "configured", AccountID: "a", GroupID: group, Models: []string{"public"}, ModelMappings: map[string]string{"public": upstream}}, Engine: "gpt", AccountIDs: []string{"a"}}
	}
	channels := []managementChannel{member("a", 1, "gpt-5.5"), member("b", 2, "extra-model")}
	compact.Input.Channels, full.Input.Channels = channels, channels
	for _, status := range []string{"success", "error"} {
		matching, err := attachManagementAvailability(ctx, tx, "owner", &compact.Input, status)
		if err != nil {
			t.Fatal(err)
		}
		filter := func(in managementQueryInput, evidence managementFilterEvidence) []string {
			rows, err := buildManagementQueryRows(in, "", "")
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, r := range filterManagementQueryRows(rows, managementQueryFilters{GroupStatus: "all", Availability: status}, evidence).Rows {
				ids = append(ids, r.ID)
			}
			return ids
		}
		if got, want := strings.Join(filter(compact.Input, managementFilterEvidence{siteAvailability: matching}), " "), strings.Join(filter(full.Input, managementFilterEvidence{}), " "); got != want ||
			strings.Contains(got, "configured:a:provider") != (status == "error") {
			t.Fatal("alias availability ignored its latest member", status, got, want)
		}
	}
}

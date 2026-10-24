package main

import (
	"context"
	"testing"
)

func TestManagementCurrentExportDiscardsOnlyUnusedRetainedEpoch(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	if _, err := s.control.db.Exec(`INSERT INTO console_sources(id,name,base,encrypted_key,enabled) VALUES('source','fixture','https://fixture.test','',false)`); err != nil {
		t.Fatal(err)
	}
	// No gateway sources in this fixture: neither its page nor aliases consume
	// saved controls. Install the same proof as the real current-export path.
	f := managementQueryFilters{GroupStatus: "all"}
	snapshot, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Proof.CurrentExport {
		t.Fatal("unused saved state still required")
	}
	page := managementPageResponse{managementQueryPage: snapshot.Page, Accounts: managementAccountHeaders(snapshot.View.Input.Accounts), Context: managementContextForPage(snapshot.View, snapshot.Page, f.Model)}
	s.rememberManagementPage("independent", snapshot.Proof, page)
	legacy := *snapshot.Proof
	legacy.CurrentExport = false
	s.rememberManagementPage("legacy", &legacy, page)
	if _, err = s.control.db.Exec(`UPDATE console_management_view_epoch SET revision=revision+1 WHERE relation_name='console_control_snapshots'`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.currentComputedManagementPage(ctx, "owner", "independent"); err != nil || !ok {
		t.Fatal("unused retained revision forced full scan", err)
	}
	if _, ok, err := s.currentComputedManagementPage(ctx, "owner", "legacy"); err != nil || ok {
		t.Fatal("legacy fallback lost its fence", err)
	}
	// A real historical engine override must invalidate even when export is
	// complete. No snapshot update is necessary for this dependency.
	if _, err = s.control.db.Exec(`INSERT INTO console_channel_settings_operations(id,source_id,owner,request_hash,request_revision,status,encrypted_before,encrypted_intent,public_result) VALUES('fixture-operation','source','owner','','','completed','','','{"previews":[{"provider":"native","before":{"engine":"gpt"},"after":{"engine":"claude"}}]}')`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.currentComputedManagementPage(ctx, "owner", "independent"); err != nil || ok {
		t.Fatal("historical override was ignored", err)
	}
	before, _, err := s.managementReadProof(ctx, s.control.db, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.control.db.Exec(`UPDATE console_channel_settings_operations SET status='rejected' WHERE id='fixture-operation'; UPDATE console_channel_settings_operations SET status='completed' WHERE id='fixture-operation'`); err != nil {
		t.Fatal(err)
	}
	after, _, err := s.managementReadProof(ctx, s.control.db, "owner")
	if err != nil || before.ExportStamp == after.ExportStamp {
		t.Fatal("historical override ABA escaped proof", err)
	}
}

func TestManagementCurrentExportRequiresEveryTemporaryDefinition(t *testing.T) {
	f := managementGatewayFacts{Controls: map[string]any{"temporary_channels": []any{map[string]any{"provider": "p"}}}, Export: map[string]any{"temporary_definitions": map[string]any{"p": map[string]any{"engine": "gpt"}}}}
	if !managementHasCompleteCurrentExport([]managementGatewayFacts{f}) {
		t.Fatal("complete export rejected")
	}
	f.Export["temporary_definitions"] = map[string]any{}
	if managementHasCompleteCurrentExport([]managementGatewayFacts{f}) {
		t.Fatal("missing legacy definition ignored")
	}
	f.Export = nil
	if managementHasCompleteCurrentExport([]managementGatewayFacts{f}) {
		t.Fatal("absent export ignored")
	}
}

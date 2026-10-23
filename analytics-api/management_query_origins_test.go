package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func TestManagementEngineOriginsReadOnlyCurrentProofAndHistoricalOverrides(t *testing.T) {
	s := managementBindingTestService(t)
	ctx := context.Background()
	src := controlSource{sourceView: sourceView{ID: "source", Base: "https://gateway.test", Name: "Source"}, Key: "secret"}
	if _, err := s.control.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	ref := subChannelRef{Account: "account", Group: 7, Base: "https://site.test/tenant"}
	provider := subProviderName(ref.Account, ref.Group, "caller")
	base := ref.Base + "/v1/responses"
	channel := retainedChannel{Provider: provider, KeyID: "caller", Key: "upstream-private", Base: base, Models: []string{"m"}}
	definition := map[string]any{"provider": provider, "engine": "gpt", "base_url": base, "api": "upstream-private"}
	facts := managementGatewayFacts{Source: src, Revision: "current", Controls: map[string]any{"temporary_channels": []retainedChannel{channel}}, Export: map[string]any{"temporary_definitions": map[string]any{provider: definition}, "channel_settings": map[string]any{}}}
	read := func(f managementGatewayFacts) map[string]string {
		t.Helper()
		tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		got, err := s.managementEngineOriginsFrom(ctx, tx, f, []subChannelRef{ref})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(mustJSON(got), "private") {
			t.Fatal("private definition escaped")
		}
		return got
	}
	if got := read(facts)[provider]; got != engineOriginLegacy {
		t.Fatal("current export proof lost", got)
	}
	facts.Export["channel_settings"] = map[string]any{provider: map[string]any{"set": map[string]any{"/engine": "gpt"}}}
	if got := read(facts)[provider]; got != engineOriginUser {
		t.Fatal("manual override lost", got)
	}
	facts.Export["channel_settings"] = map[string]any{}
	facts.Export["temporary_definitions"] = map[string]any{}
	if got := read(facts)[provider]; got != "" {
		t.Fatal("missing evidence invented an origin", got)
	}
	var n int
	if err := s.control.db.QueryRow(`SELECT count(*) FROM console_control_snapshots`).Scan(&n); err != nil || n != 0 {
		t.Fatal("read created retained state", n, err)
	}
	sealed, err := s.control.encrypt(mustJSON(retainedSnapshot{Version: 2, Channels: []retainedChannel{channel}, Settings: map[string]json.RawMessage{}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.control.db.Exec(`INSERT INTO console_control_snapshots(source_id,encrypted_snapshot,target_hash,revision) VALUES($1,$2,$3,'old')`, src.ID, sealed, controlTarget(src)); err != nil {
		t.Fatal(err)
	}
	if got := read(facts)[provider]; got != "" {
		t.Fatal("stale retained evidence reused", got)
	}
	if _, err = s.control.db.Exec(`UPDATE console_control_snapshots SET revision='current'`); err != nil {
		t.Fatal(err)
	}
	if got := read(facts)[provider]; got != engineOriginLegacy {
		t.Fatal("matching retained evidence lost", got)
	}
	result := mustJSON(map[string]any{"previews": []any{map[string]any{"provider": provider, "before": map[string]any{"engine": "gpt"}, "after": map[string]any{"engine": "claude"}}}})
	if _, err = s.control.db.Exec(`INSERT INTO console_channel_settings_operations(id,source_id,owner,request_hash,request_revision,status,encrypted_before,encrypted_intent,public_result) VALUES('operation',$1,'owner','','','completed','','',$2)`, src.ID, result); err != nil {
		t.Fatal(err)
	}
	if got := read(facts)[provider]; got != engineOriginUser {
		t.Fatal("historical user intent lost", got)
	}
}

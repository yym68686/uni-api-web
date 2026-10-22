package main

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func managementBindingTestService(t *testing.T) *Service {
	t.Helper()
	db := migrationTestDB(t)
	if err := migrateControlSchema(context.Background(), db, controlMigrations()); err != nil {
		t.Fatal(err)
	}
	return &Service{control: &controlStore{db: db, key: []byte(strings.Repeat("b", 32))}}
}

func TestManagementBindingsUseFreshCredentialsAndTransactionalOwnedIndex(t *testing.T) {
	s := managementBindingTestService(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.control.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, account := range []string{"owned", "foreign"} {
		exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$1,$1,'https://site.test/tenant','fixture','')`, account)
		for _, group := range []int{7, 8, 9} {
			exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,billing) VALUES($1,$2,'group','openai','{"rate":0}')`, account, group)
		}
		exec(`INSERT INTO console_sub_key_index(account_id,key_hash,remote_key_id,group_id) VALUES($1,$2,42,7),($1,$3,43,8)`, account, tokenHash("old-secret"), tokenHash("new-secret"))
	}
	src := controlSource{sourceView: sourceView{ID: "source", Name: "Source", Base: "https://gateway.test"}, Key: "gateway-secret"}
	if _, err := s.control.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	old := configuredProvider{Provider: "named", Base: "https://site.test/tenant/v1", API: "old-secret"}
	if err := s.saveConfiguredInventory(ctx, src, []configuredProvider{old}); err != nil {
		t.Fatal(err)
	}
	cached, err := s.configuredBindings(ctx, "owned")
	if err != nil || len(cached) != 1 || cached[0].GroupID != 7 {
		t.Fatal("stale inventory fixture", err)
	}
	current := old
	current.API = "new-secret"
	facts := []managementGatewayFacts{{Source: src, Providers: []configuredProvider{current}}}
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	check := func(db controlQueryDB, owner string, group int64) {
		t.Helper()
		got, err := s.managementBindingsFrom(ctx, db, owner, facts)
		if err != nil || len(got) != 1 || got[0].AccountID != owner || got[0].GroupID != group || got[0].Name != "named" || got[0].BindingStatus != "matched" {
			t.Fatal("live owned binding incorrect", got, err)
		}
		if got[0].Fingerprint != managementSnapshotFromFacts(facts[0]).Providers["named"].Fingerprint {
			t.Fatal("live assembly and binding use different credential identity")
		}
		for _, secret := range []string{"new-secret", "old-secret", "gateway-secret"} {
			if strings.Contains(mustJSON(got), secret) {
				t.Fatal("credential escaped public binding")
			}
		}
	}
	check(tx, "owned", 8)
	// The foreign owner's equal key must not create ambiguity or leak ownership.
	check(tx, "foreign", 8)
	exec(`UPDATE console_sub_key_index SET group_id=9 WHERE account_id='owned' AND key_hash=$1`, tokenHash("new-secret"))
	check(tx, "owned", 8)
	check(s.control.db, "owned", 9)
	missing, err := s.managementBindingsFrom(ctx, tx, "absent", facts)
	if err != nil || len(missing) != 1 || missing[0].BindingStatus != "no_account" || missing[0].AccountID != "" || len(missing[0].BoundKeys) != 0 {
		t.Fatal("foreign account leaked", missing, err)
	}
}

func TestManagementLiveTemporaryBindingsRejectChangedOrForeignSite(t *testing.T) {
	refs := []subChannelRef{{Account: "a", Group: 7, Base: "https://site.test/tenant"}}
	name := subProviderName("a", 7, "caller")
	for _, test := range []struct {
		name, base              string
		controlChanged, changed bool
		temporary               bool
		want                    int
	}{
		{"same", "https://site.test/tenant/v1", false, false, true, 1},
		{"other tenant", "https://site.test/other/v1", false, false, true, 0},
		{"control changed", "https://site.test/tenant", true, false, true, 0},
		{"effective changed", "https://site.test/tenant", false, true, true, 0},
		{"no longer temporary", "https://site.test/tenant", false, false, false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := managementGatewayFacts{Source: controlSource{sourceView: sourceView{ID: "source"}}, Keys: []managementGatewayKey{{ID: "caller"}}, Controls: map[string]any{"temporary_channels": []any{map[string]any{"provider": name, "api_key_id": "caller", "identity_changed": test.controlChanged}}}, Providers: []configuredProvider{{Provider: name, Base: test.base, Temporary: test.temporary, IdentityChanged: test.changed}}}
			got, err := managementQualityBindings(nil, refs, []managementGatewayFacts{facts})
			if err != nil || len(got) != test.want {
				t.Fatal("unsafe quality binding", got, err)
			}
			imports, err := assembleManagementImports(facts, refs)
			if err != nil || len(imports) != test.want {
				t.Fatal("unsafe installed binding", imports, err)
			}
		})
	}
}

func TestManagementSourceFenceCoversMembershipAndCredentials(t *testing.T) {
	source := controlSource{sourceView: sourceView{ID: "a", Name: "A", Base: "https://gateway.test"}, Key: "read", ConfigKey: "config"}
	facts := []managementGatewayFacts{{Source: source}}
	if err := verifyManagementSources([]controlSource{source}, facts); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"ID", "Name", "Base", "Key", "ConfigKey"} {
		changed := source
		reflect.ValueOf(&changed).Elem().FieldByName(field).SetString("changed")
		if err := verifyManagementSources([]controlSource{changed}, facts); !errors.Is(err, errManagementSourceRevision) {
			t.Fatal("source change accepted", field, err)
		}
	}
	for _, changed := range [][]controlSource{nil, {source, source}} {
		if err := verifyManagementSources(changed, facts); !errors.Is(err, errManagementSourceRevision) {
			t.Fatal("source membership change accepted", err)
		}
	}
}

func TestManagementQualityReadCannotReattributeUnlinkedHistory(t *testing.T) {
	s := managementBindingTestService(t)
	ctx := context.Background()
	statements := []string{
		`INSERT INTO console_sources(id,name,base,encrypted_key) VALUES('source','fixture','https://gateway.test','')`,
		`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('a','owner','fixture','https://site.test','fixture',''),('foreign','foreign','fixture','https://site.test','fixture','')`,
		`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES('a',7,'old','openai'),('a',8,'current','openai'),('foreign',7,'foreign','openai')`,
		`INSERT INTO console_quality_history(check_id,source_id,provider,checked_at,verdict,successful,result) VALUES('linked','source','channel',1,'pass',true,'{"verdict":"pass","checked_at":1}'),('unlinked','source','channel',2,'fail',true,'{"verdict":"fail","checked_at":2}')`,
		`INSERT INTO console_quality_group_links(check_id,account_id,group_id) VALUES('linked','a',7)`,
	}
	for _, query := range statements {
		if _, err := s.control.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	bindings := []qualityBinding{{Source: "source", Provider: "channel", Account: "a", Group: 8}}
	got, err := readSharedQualityProjectionFrom(ctx, tx, "owner", "", bindings)
	if err != nil || got.Groups[qualityGroupID("a", 7)].History.Total != 1 || got.Groups[qualityGroupID("a", 8)].History.Total != 0 || got.channel("source", "channel") != nil {
		t.Fatal("history moved or acquired unknown provenance", got, err)
	}
	foreign, err := readSharedQualityProjectionFrom(ctx, tx, "foreign", "", nil)
	if err != nil || len(foreign.Groups) != 0 {
		t.Fatal("foreign history leaked", foreign, err)
	}
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM console_quality_group_links`).Scan(&count); err != nil || count != 1 {
		t.Fatal("reading changed historical attribution", count, err)
	}
}

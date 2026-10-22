package main

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSubObservedRateSurvivesReceiptFIFO(t *testing.T) {
	s, account, owner := attributionFixture(t)
	ctx := context.Background()
	for _, query := range []string{
		"CREATE INDEX IF NOT EXISTS " + receiptAccountTimeIndex + " ON console_sub_spend_logs(account_id,at_ms)",
	} {
		if _, err := s.control.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,billing) VALUES($1,7,'group','openai',42,'{"rate":0.3}')`, account); err != nil {
		t.Fatal(err)
	}
	floor := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := s.engine.DB.Exec("UPDATE storage_state SET evicted_before_ms=?", floor); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES($1,$2,7,42,'luna',$3,'[]','matched','{"status":"matched","total_cost":1,"actual_cost":3,"input_price":0.1}')`, account+"-receipt", account, floor/1000-60); err != nil {
		t.Fatal(err)
	}
	before, err := s.control.subObservedRates(ctx, owner)
	if err != nil || before[qualityGroupID(account, 7)]["luna"].Rate != 3 {
		t.Fatal(before, err)
	}
	if err := s.trimReceiptHistory(ctx); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := s.control.db.QueryRow("SELECT count(*) FROM console_sub_usage WHERE account_id=$1", account).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("expired request must still be evicted", remaining, err)
	}
	after, err := s.control.subObservedRates(ctx, owner)
	if err != nil || after[qualityGroupID(account, 7)]["luna"].Rate != 3 {
		t.Fatalf("FIFO erased current model billing metadata: before=%v after=%v err=%v", before, after, err)
	}
}

func TestObservedRateMetadataTracksCurrentKeyAndRollsBackAtomically(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	ctx := context.Background()
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id) VALUES($1,7,'group','openai',42)`, account); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES($1,$2,7,$3,'m',$4,'[]','matched',$5)`
	good := `{"status":"matched","total_cost":1,"actual_cost":0,"input_price":1,"message":"not durable metadata"}`
	if _, err := s.control.db.Exec(insert, account+"-first", account, 42, 100, good); err != nil {
		t.Fatal(err)
	}
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(insert, account+"-rollback", account, 42, 200, `{"status":"matched","total_cost":1,"actual_cost":9,"input_price":1}`); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	tx.Rollback()
	read := func() map[string]subObservedRate {
		t.Helper()
		v, e := s.control.subObservedRates(ctx, owner)
		if e != nil {
			t.Fatal(e)
		}
		return v[qualityGroupID(account, 7)]
	}
	if v, ok := read()["m"]; !ok || v.Rate != 0 {
		t.Fatal("zero/rollback", read())
	}
	var extra bool
	if err = s.control.db.QueryRow(`SELECT result ? 'message' FROM console_sub_observed_rates WHERE account_id=$1`, account).Scan(&extra); err != nil || extra {
		t.Fatal("metadata retained unrelated payload", extra, err)
	}
	if _, err = s.control.db.Exec(`UPDATE console_sub_targets SET remote_key_id=43 WHERE account_id=$1`, account); err != nil {
		t.Fatal(err)
	}
	if len(read()) != 0 {
		t.Fatal("old key rate applied to replacement key")
	}
	if _, err = s.control.db.Exec(insert, account+"-replacement", account, 43, 50, `{"status":"matched","total_cost":1,"actual_cost":2,"input_price":1}`); err != nil {
		t.Fatal(err)
	}
	// An older writer's DELETE trigger must not resurrect the replaced key.
	if _, err = s.control.db.Exec(`DELETE FROM console_sub_usage WHERE account_id=$1`, account); err != nil {
		t.Fatal(err)
	}
	if read()["m"].Rate != 2 {
		t.Fatal("replacement was lost", read())
	}
	if _, err = s.control.db.Exec(insert, account+"-invalid", account, 43, 60, `{"status":"matched","total_cost":0,"actual_cost":0,"input_price":1}`); err != nil {
		t.Fatal(err)
	}
	if len(read()) != 0 {
		t.Fatal("older valid rate hid newest unusable evidence")
	}
	if _, err = s.control.db.Exec(`DELETE FROM console_sub_usage WHERE account_id=$1`, account); err != nil {
		t.Fatal(err)
	}
	if len(read()) != 0 {
		t.Fatal("FIFO revived an older rate")
	}
}

func TestObservedRateRecoveryUsesExistingReceiptAndExactCurrentKey(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	ctx := context.Background()
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id) VALUES($1,7,'group','openai',42)`, account); err != nil {
		t.Fatal(err)
	}
	old := usageFixture(1, "receipt-one")
	old.CreatedAt = time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	latest := usageFixture(2, "receipt-two")
	latest.CreatedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	latest.ActualCost = usageFloat(.0024)
	foreign := usageFixture(3, "foreign")
	foreign.KeyID = 99
	foreign.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	foreignGroup := foreign
	foreignGroup.KeyID = 42
	foreignGroup.GroupID = usageInt(9)
	page := []subUsageLog{old, latest, foreign, foreignGroup}
	if n, err := s.control.retainObservedRatePage(ctx, account, 7, 42, page, false); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	got, err := s.control.subObservedRates(ctx, owner)
	if err != nil || len(got) != 0 {
		t.Fatal("preview wrote evidence", got, err)
	}
	if n, err := s.control.retainObservedRatePage(ctx, account, 7, 42, page, true); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	got, err = s.control.subObservedRates(ctx, owner)
	if err != nil || math.Abs(got[qualityGroupID(account, 7)][checkModel].Rate-3) > 1e-9 {
		t.Fatal(got, err)
	}
	if n, err := s.control.retainObservedRatePage(ctx, account, 7, 42, page, true); err != nil || n != 0 {
		t.Fatal("replay was not idempotent", n, err)
	}
	if _, err = s.control.db.Exec(`UPDATE console_sub_targets SET remote_key_id=99 WHERE account_id=$1`, account); err != nil {
		t.Fatal(err)
	}
	if _, err = s.control.retainObservedRatePage(ctx, account, 7, 42, page, true); err == nil {
		t.Fatal("changed current key accepted")
	}
}

func TestSubReceiptRateUsesActualChargeAndRejectsUnusableReceipts(t *testing.T) {
	input, total, actual, declared := .1, .0000537, .0001611, .3
	usage := subUsage{Status: "matched", InputPrice: &input, TotalCost: &total, ActualCost: &actual, Rate: &declared, CreatedAt: "2026-10-13T14:42:54+08:00"}
	got := subReceiptRate(usage, 1)
	if got == nil || math.Abs(got.Rate-3) > 1e-9 || got.ObservedAt != 1791873774 {
		t.Fatalf("actual receipt not preferred: %+v", got)
	}
	zero := 0.0
	usage.ActualCost = &zero
	if got := subReceiptRate(usage, 1); got == nil || got.Rate != 0 {
		t.Fatal("confirmed zero charge lost")
	}
	for _, change := range []func(*subUsage){
		func(value *subUsage) { value.Status = "pending" },
		func(value *subUsage) { value.TotalCost = &zero },
		func(value *subUsage) { value.TotalCost = nil },
		func(value *subUsage) { value.InputPrice = nil },
		func(value *subUsage) { negative := -1.0; value.ActualCost = &negative },
		func(value *subUsage) { invalid := math.NaN(); value.ActualCost = &invalid },
	} {
		invalid := usage
		change(&invalid)
		if subReceiptRate(invalid, 1) != nil {
			t.Fatal("unusable receipt treated as verified")
		}
	}
	actual, declared = .00016269, 1.7
	total = .0000957
	usage.ActualCost = &actual
	if got := subReceiptRate(usage, 1); got == nil || got.Rate != 1.7 {
		t.Fatal("receipt rounding changed the display multiplier")
	}
}

func TestSubChannelModelRatesRespectModelSourceAndEveryBoundGroup(t *testing.T) {
	refs := []subChannelRef{
		{Account: "a", AccountName: "codex5", Group: 33, ObservedRates: map[string]subObservedRate{"luna": {Rate: 3, ObservedAt: 100}, "astra": {Rate: .3, ObservedAt: 90}}},
		{Account: "a", AccountName: "codex5", Group: 27, ObservedRates: map[string]subObservedRate{"luna": {Rate: 1.7, ObservedAt: 101}}},
		{Account: "b", AccountName: "other", Group: 33, ObservedRates: map[string]subObservedRate{"luna": {Rate: 0, ObservedAt: 102}}},
	}
	bindings := []subBoundKey{{AccountID: "a", GroupID: 33}, {AccountID: "a", GroupID: 27}}
	channels := []subInstalledChannel{
		{SourceID: "s", Provider: "p", AccountID: "a", GroupID: 33, Models: []string{"alias", "astra", "unknown"}, ModelMappings: map[string]string{"alias": "luna"}},
		{SourceID: "other", Provider: "p", AccountID: "b", GroupID: 33, Models: []string{"luna"}},
		{SourceID: "s", Provider: "mixed", Kind: "configured", BindingStatus: "matched", BoundKeys: bindings, Models: []string{"luna", "astra"}},
		{SourceID: "s", Provider: "partial", Kind: "configured", BindingStatus: "partial", BoundKeys: bindings[:1], Models: []string{"luna"}},
	}
	got := subChannelModelRates(channels, refs)
	if got["s"]["p"]["luna"].Name != "codex5-3" || got["s"]["p"]["astra"].Name != "codex5-0.3" || got["s"]["p"]["unknown"].Name != "codex5-未知倍率" {
		t.Fatalf("model identity or display incorrect: %+v", got)
	}
	if got["other"]["p"]["luna"].Rate == nil || *got["other"]["p"]["luna"].Rate != 0 {
		t.Fatal("source identity or zero charge lost")
	}
	if got["s"]["mixed"]["luna"].Rate != nil || got["s"]["mixed"]["astra"].Rate != nil || got["s"]["partial"] != nil {
		t.Fatal("mixed or partial bindings fabricated a rate")
	}
	channels = append(channels, subInstalledChannel{SourceID: "s", Provider: "p", AccountID: "a", GroupID: 27, Models: []string{"luna"}})
	if subChannelModelRates(channels, refs)["s"]["p"]["luna"].Rate != nil {
		t.Fatal("conflicting provider bindings picked an arbitrary rate")
	}
}

func TestSubObservedRatesUseLatestMatchedReceiptAndCurrentKey(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("r", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := "observed-rate-" + randomID()
	account := owner + "-account"
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, account)
	if _, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$2,'codex5','https://fixture.test','fixture@test','')`, account, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,billing) VALUES($1,33,'group','openai',625,'{"rate":0.3}')`, account); err != nil {
		t.Fatal(err)
	}
	for _, receipt := range []struct {
		key, at               int64
		model, status, result string
	}{
		{625, 1, "luna", "matched", `{"status":"matched","total_cost":1,"actual_cost":0.3,"input_price":0.1}`},
		{625, 2, "luna", "matched", `{"status":"matched","total_cost":1,"actual_cost":3,"input_price":0.1}`},
		{625, 3, "luna", "pending", `{}`},
		{624, 4, "luna", "matched", `{"status":"matched","total_cost":1,"actual_cost":9,"input_price":0.1}`},
		{625, 5, "astra", "matched", `{"status":"matched","total_cost":1,"actual_cost":0.3,"input_price":10}`},
	} {
		if _, err = store.db.Exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES($1,$2,33,$3,$4,$5,'[]',$6,$7)`, randomID(), account, receipt.key, receipt.model, receipt.at, receipt.status, receipt.result); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.subObservedRates(context.Background(), owner)
	if err != nil || got[qualityGroupID(account, 33)]["luna"].Rate != 3 || got[qualityGroupID(account, 33)]["astra"].Rate != .3 {
		t.Fatalf("wrong latest receipt: %+v %v", got, err)
	}
	foreign, err := store.subObservedRates(context.Background(), owner+"-other")
	if err != nil || len(foreign) != 0 {
		t.Fatal("cross-owner billing evidence exposed", err)
	}
	refs, err := store.subChannelRefs(context.Background(), owner)
	if err != nil || len(refs) != 1 || refs[0].Name != "codex5-0.3" || refs[0].AccountName != "codex5" {
		t.Fatal("published and observed rates were not kept separate", err)
	}
}

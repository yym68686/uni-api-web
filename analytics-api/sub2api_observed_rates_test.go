package main

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"
)

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

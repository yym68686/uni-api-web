package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestSubBusinessRateProjectionUsesExactRecentReceipts(t *testing.T) {
	s, account, owner, request := spendTestService(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET billing='{"rate":0.12}' WHERE account_id=$1`, account); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_key_index(account_id,key_hash,remote_key_id,group_id) VALUES ($1,'external',43,7),($1,'probe',99,7)`, account); err != nil {
		t.Fatal(err)
	}
	request("7d", now)
	task, err := s.subClaimSpend(ctx)
	if err != nil {
		t.Fatal(err)
	}
	task.from, task.to = now.Add(-48*time.Hour).UnixMilli(), now.UnixMilli()
	makeLog := func(id, group, key int64, model, rate, total string, age time.Duration) subSpendLog {
		log := spendLog(id, now.Add(-age), "0.06")
		log.GroupID, log.KeyID, log.Model = usageInt(group), key, model
		log.Total, log.Rate = json.RawMessage(total), json.RawMessage(rate)
		return log
	}
	recent := time.Minute
	records := []subSpendLog{
		makeLog(10, 7, 42, "gpt-6-astra", "0.12", "0.5", recent),
		makeLog(11, 7, 42, "gpt-6.2-astra", "0.12", "0.5", recent),
		makeLog(12, 7, 43, "gpt-6-astra", "0.2", "0.3", recent),
		makeLog(20, 7, 99, "gpt-6-astra", "0.3", "0.2", recent),
		makeLog(21, 8, 42, "gpt-6-astra", "0.3", "0.2", recent),
		makeLog(22, 7, 42, "unconfirmed", "0.7", "0.5", recent),
		makeLog(23, 7, 42, "invalid-metadata", `"bad"`, "0.5", recent),
		makeLog(24, 7, 42, "stale", "0.12", "0.5", 25*time.Hour),
		makeLog(25, 7, 42, "gpt-6-astra", "0.3", "0.2", 2*recent),
	}
	if err := s.subStoreSpendReceipts(ctx, task, subSpendPage{Items: records}, false, false); err != nil {
		t.Fatal(err)
	}
	if err := s.subStoreSpendReceipts(ctx, task, subSpendPage{Items: records[8:]}, false, false); err != nil {
		t.Fatal(err)
	}
	var count int
	var amount float64
	if err := s.control.db.QueryRow(`SELECT count(*),sum(actual_cost) FROM console_sub_spend_logs WHERE account_id=$1`, account).Scan(&count, &amount); err != nil || count != len(records) || amount != .54 {
		t.Fatal("billing projection changed the underlying ledger", count, amount, err)
	}
	business, err := s.control.subBusinessRates(ctx, owner, now)
	if err != nil || len(business) != 2 || len(business[subBusinessRateKey{account, 7, 42}]) != 2 {
		t.Fatal("bad business projection", business, err)
	}
	if out, err := s.control.subBusinessRates(ctx, owner+"-other", now); err != nil || len(out) != 0 {
		t.Fatal("business evidence crossed owner boundary", out, err)
	}
	if out, err := s.control.subBusinessRates(ctx, owner, now.Add(25*time.Hour)); err != nil || len(out) != 0 {
		t.Fatal("expired evidence stayed current", out, err)
	}
	refs, err := s.control.subChannelRefs(ctx, owner)
	if err != nil || len(refs) != 1 {
		t.Fatal(refs, err)
	}
	refs[0].ObservedRates = map[string]subObservedRate{
		"gpt-6-astra":   {Rate: .15009311, ObservedAt: now.AddDate(0, 0, -14).Unix()},
		"gpt-6.2-astra": {Rate: .11983842, ObservedAt: now.AddDate(0, 0, -9).Unix()},
	}
	astra := "gpt-6-astra"
	channels := []subInstalledChannel{
		{SourceID: "s", Provider: "p", AccountID: account, GroupID: 7, Models: []string{astra, "gpt-6.2-astra"}},
		{SourceID: "s", Provider: "configured", Kind: "configured", BindingStatus: "matched", BoundKeys: []subBoundKey{{AccountID: account, GroupID: 7, RemoteKeyID: 43}}, Models: []string{"alias"}, ModelMappings: map[string]string{"alias": astra}},
		{SourceID: "s", Provider: "mixed", Kind: "configured", BindingStatus: "matched", BoundKeys: []subBoundKey{{AccountID: account, GroupID: 7, RemoteKeyID: 43}, {AccountID: account, GroupID: 7, RemoteKeyID: 42}}, Models: []string{astra}},
		{SourceID: "s", Provider: "unverified", Kind: "configured", BindingStatus: "matched", BoundKeys: []subBoundKey{{AccountID: account, GroupID: 7, RemoteKeyID: 44}}, Models: []string{astra}},
	}
	got := subChannelModelRates(channels, refs, business, now)["s"]
	for _, model := range channels[0].Models {
		r := got["p"][model]
		if r.Rate == nil || *r.Rate != .12 || r.RateSource != "business" || r.ObservedAt != now.Add(-recent).Unix() {
			t.Fatalf("%s took an old probe instead of its current receipt: %+v", model, r)
		}
	}
	if r := got["configured"][astra]; r.Rate == nil || *r.Rate != .2 || r.RateSource != "business" {
		t.Fatal("configured binding ignored exact key", r)
	}
	if got["mixed"][astra].Rate != nil || got["unverified"][astra].RateSource != "advertised" {
		t.Fatal("borrowed another key's evidence", got)
	}
	refs[0].RoutingKey = 45
	if got := subChannelModelRates(channels, refs, business, now)["s"]["p"][astra]; got.RateSource != "advertised" || got.Rate == nil || *got.Rate != .12 || got.ObservedAt != 0 {
		t.Fatal("used previous credential or stale probe after rotation", got)
	}
	refs[0].Rate = nil
	if got := subChannelModelRates(channels, refs, business, now)["s"]["p"][astra]; got.RateSource != "historical" || got.Rate == nil || *got.Rate != .15009311 {
		t.Fatal("historical fallback lacked provenance", got)
	}
	refs[0].RoutingKey, refs[0].RateCoefficient = 42, .1
	if got := subChannelModelRates(channels, refs, business, now)["s"]["p"][astra]; got.Rate == nil || *got.Rate != .012 {
		t.Fatal("did not apply coefficient once", got)
	}
}

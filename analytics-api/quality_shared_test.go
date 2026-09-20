package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSharedQualityBackfillsBothOriginsOnceAndSharesLatest(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	ctx := context.Background()
	source := "quality-source-" + randomID()[:8]
	_, err := s.control.saveSource(ctx, controlSource{sourceView: sourceView{ID: source, Name: "fixture", Base: "https://gateway.example"}, Key: "secret"}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.control.db.Exec(`DELETE FROM console_configured_channels WHERE source_id=$1`, source)
		s.control.db.Exec(`DELETE FROM console_sources WHERE id=$1`, source)
	})
	for _, g := range []int64{7, 8} {
		if _, err = s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES($1,$2,'fixture','openai')`, account, g); err != nil {
			t.Fatal(err)
		}
	}
	for i, hash := range []string{"hash-seven", "hash-eight"} {
		if _, err = s.control.db.Exec(`INSERT INTO console_sub_key_index(account_id,key_hash,remote_key_id,group_id) VALUES($1,$2,$3,$4)`, account, hash, 42+i, 7+i); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []struct{ name, hashes string }{{"configured", `["hash-seven"]`}, {"mixed", `["hash-seven","hash-eight"]`}, {"unknown", `["missing"]`}} {
		if _, err = s.control.db.Exec(`INSERT INTO console_configured_channels(source_id,provider,base,site,key_hashes,fingerprint,checked_at) VALUES($1,$2,'https://usage.example','https://usage.example',$3,'fixture',1)`, source, p.name, p.hashes); err != nil {
			t.Fatal(err)
		}
	}
	imported := subProviderName(account, 7, "caller-a")
	sibling := subProviderName(account, 7, "caller-b")
	snapshot := retainedSnapshot{Version: 1, Channels: []retainedChannel{{Provider: imported, KeyID: "caller-a", Base: "https://usage.example/v1/responses", Key: "route"}, {Provider: sibling, KeyID: "caller-b", Base: "https://usage.example/v1/responses", Key: "route"}}}
	encrypted, _ := s.control.encrypt(mustJSON(snapshot))
	if _, err = s.control.db.Exec(`INSERT INTO console_control_snapshots(source_id,encrypted_snapshot) VALUES($1,$2)`, source, encrypted); err != nil {
		t.Fatal(err)
	}
	// No remote/model access: old history rows exist before the shared projection.
	insert := func(id string, q qualityScope, verdict string, at int64, result any) {
		t.Helper()
		if err = s.control.beginQuality(ctx, source+id, q, time.Minute); err != nil {
			t.Fatal(err)
		}
		if err = s.control.finishQuality(source+id, verdict, verdict == "pass" || verdict == "fail", at, result); err != nil {
			t.Fatal(err)
		}
	}
	insert("sub", qualityScope{Account: account, Group: 7}, "pass", 100, subResult{Verdict: "pass", CheckedAt: 100, Quality: subProbe{Status: "success", Text: "21"}})
	insert("import", qualityScope{Source: source, Provider: imported}, "fail", 200, ChannelCheck{SourceID: source, Provider: imported, Verdict: "fail", CheckedAt: 200, Text: "20"})
	insert("config", qualityScope{Source: source, Provider: "configured"}, "pass", 300, ChannelCheck{SourceID: source, Provider: "configured", Verdict: "pass", CheckedAt: 300, Text: "21"})
	insert("mixed", qualityScope{Source: source, Provider: "mixed"}, "fail", 900, ChannelCheck{Verdict: "fail"})
	insert("unknown", qualityScope{Source: source, Provider: "unknown"}, "fail", 901, ChannelCheck{Verdict: "fail"})
	want := qualitySummary{Total: 3, Successful: 3, Passed: 2}
	for range 2 {
		shared, e := s.sharedQuality(ctx, owner)
		if e != nil {
			t.Fatal(e)
		}
		p := shared.Groups[qualityGroupID(account, 7)]
		if p.History != want || p.Check == nil || p.Check.Text != "21" || p.Check.CheckedAt != 300 {
			t.Fatal(p)
		}
		for _, provider := range []string{imported, sibling, "configured"} {
			c := shared.channel(source, provider)
			if c == nil || *c.History != want || c.HistoryScope != "account_group" {
				t.Fatal(provider, c)
			}
		}
		if shared.channel(source, "mixed") != nil || shared.channel(source, "unknown") != nil {
			t.Fatal("ambiguous/unbound channel merged")
		}
	}
	var count int
	s.control.db.QueryRow(`SELECT count(*) FROM console_quality_group_links WHERE account_id=$1`, account).Scan(&count)
	if count != 2 {
		t.Fatal("duplicated links", count)
	}
	foreign, e := s.sharedQuality(ctx, "foreign-"+owner)
	if e != nil || len(foreign.Groups) != 0 || len(foreign.Bindings) != 0 {
		t.Fatal("account leak", foreign, e)
	}
	// A new channel-origin result is shared automatically without rewriting either
	// original projection or overwriting model availability/billing.
	insert("new", qualityScope{Source: source, Provider: sibling}, "fail", 400, ChannelCheck{SourceID: source, Provider: sibling, Verdict: "fail", CheckedAt: 400, Text: "20"})
	expected := qualitySummary{Total: 4, Successful: 4, Passed: 2}
	token, _ := s.control.newSession(ctx, owner)
	call := func(path string) []byte {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var channels struct{ Data []ChannelCheck }
	json.Unmarshal(call("/v1/sources/all/channel-checks"), &channels)
	found := 0
	for _, c := range channels.Data {
		if c.SourceID == source {
			found++
			if c.CheckedAt != 400 || c.Verdict != "fail" || *c.History != expected {
				t.Fatal(c)
			}
		}
	}
	if found != 3 {
		t.Fatal("missing installed aliases", found)
	}
	var accounts struct{ Data []subAccount }
	json.Unmarshal(call("/v1/sub2api/accounts"), &accounts)
	target := accounts.Data[0].Targets[0]
	if target.History != expected || target.QualityCheck == nil || target.QualityCheck.CheckedAt != 400 || target.Result != nil || len(target.Models) != 0 {
		t.Fatal("quality changed model results", target)
	}
	for _, path := range []string{"/v1/sources/" + source + "/channel-checks/history?provider=" + url.QueryEscape(imported), "/v1/sub2api/accounts/" + account + "/groups/7/quality-history"} {
		var page struct {
			Summary qualitySummary
			Data    []struct{ ID string }
		}
		json.Unmarshal(call(path), &page)
		if page.Summary != expected || len(page.Data) != 4 {
			t.Fatal(path, page)
		}
		ids := map[string]bool{}
		for _, row := range page.Data {
			if ids[row.ID] {
				t.Fatal("duplicate history")
			}
			ids[row.ID] = true
		}
	}
	// Rebinding a configured provider cannot move already linked historical checks.
	s.control.db.Exec(`UPDATE console_configured_channels SET key_hashes='["hash-eight"]' WHERE source_id=$1 AND provider='configured'`, source)
	shared, e := s.sharedQuality(ctx, owner)
	if e != nil || shared.Groups[qualityGroupID(account, 7)].History != expected {
		t.Fatal("old history moved", e)
	}
	if p := shared.Groups[qualityGroupID(account, 8)]; p.History.Total != 0 {
		t.Fatal("old result re-attributed", p)
	}
	insert("native-new", qualityScope{Account: account, Group: 7}, "pass", 500, subResult{Verdict: "pass", CheckedAt: 500, Quality: subProbe{Status: "success", Text: "native 21"}})
	shared, e = s.sharedQuality(ctx, owner)
	if e != nil {
		t.Fatal(e)
	}
	for _, provider := range []string{imported, sibling} {
		c := shared.channel(source, provider)
		if c == nil || c.CheckedAt != 500 || c.Text != "native 21" || *c.History != (qualitySummary{Total: 5, Successful: 5, Passed: 3}) {
			t.Fatal("sub2api result not shared back", c)
		}
	}
}

func TestSharedQualityHistoryKeepsStablePagination(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	ctx := context.Background()
	s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES($1,7,'fixture','openai')`, account)
	for i := 0; i < 33; i++ {
		id := randomID()
		s.control.beginQuality(ctx, id, qualityScope{Account: account, Group: 7}, time.Minute)
		s.control.finishQuality(id, "pass", true, 123, subResult{Verdict: "pass", Quality: subProbe{Text: "21"}})
	}
	token, _ := s.control.newSession(ctx, owner)
	next := ""
	seen := map[string]bool{}
	for _, n := range []int{30, 3} {
		r := httptest.NewRequest("GET", "/v1/sub2api/accounts/"+account+"/groups/7/quality-history?before="+next, nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		var p struct {
			Data    []struct{ ID string }
			Next    string
			Summary qualitySummary
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil || len(p.Data) != n || p.Summary.Total != 33 {
			t.Fatal(w.Code, strings.TrimSpace(w.Body.String()))
		}
		for _, v := range p.Data {
			if seen[v.ID] {
				t.Fatal("duplicate page")
			}
			seen[v.ID] = true
		}
		next = p.Next
	}
	if next != "" || len(seen) != 33 {
		t.Fatal(next, len(seen))
	}
}

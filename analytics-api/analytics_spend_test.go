package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCombinedAnalyticsKeepsBillingCutoffAndOwner(t *testing.T) {
	s, account, owner := attributionFixture(t)
	f := attributedFact("combined", "caller", time.Now().Add(-time.Minute))
	f.Status = 200
	if err := s.engine.Import(context.Background(), "one", "1", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	storeAttributedLog(t, s, account, "combined", "0.25", 1)
	for _, user := range []string{owner, "foreign-combined-owner"} {
		cookie, err := s.control.newSession(context.Background(), user)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", "/v1/analytics?source_id=source&key_id=caller&spend_model="+checkModel, nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		w := httptest.NewRecorder()
		s.analyticsWithSpend(w, r, []string{"source"}, "1h")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var result QueryResult
		if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.ChannelSpend == nil || len(*result.ChannelSpend) != 1 {
			t.Fatal(result)
		}
		spend := (*result.ChannelSpend)[0]
		if spend.From != result.From || spend.To != result.To {
			t.Fatal("different scopes", result.From, result.To, spend)
		}
		if user == owner {
			if spend.Amount == nil || *spend.Amount != 0.25 {
				t.Fatal(spend)
			}
		} else if spend.Amount != nil {
			t.Fatal("foreign receipt leak", spend)
		}
		if w.Header().Get("Server-Timing") == "" {
			t.Fatal("missing stage timings")
		}
	}
}

func TestCombinedAnalyticsDoesNotHideBillingFailureOrCachePreviousAmount(t *testing.T) {
	s, _, owner := attributionFixture(t)
	f := attributedFact("combined-failure", "caller", time.Now().Add(-time.Minute))
	if err := s.engine.Import(context.Background(), "one", "1", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	cookie, err := s.control.newSession(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	// Break only the billing schema in this isolated engine. Metrics still work.
	if _, err = s.engine.DB.Exec(`CREATE TABLE history.previous_facts AS SELECT * EXCLUDE(upstream_key_hash) FROM facts; DROP TABLE facts; ALTER TABLE history.previous_facts RENAME TO facts`); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/v1/analytics?source_id=source&key_id=caller", nil)
	r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
	w := httptest.NewRecorder()
	s.analyticsWithSpend(w, r, []string{"source"}, "1h")
	var result QueryResult
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.ChannelSpend != nil || result.ChannelSpendError == "" {
		t.Fatal(w.Code, w.Body.String(), err)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestManagementSelectedReceiptsPreserveDisplaySelectionAndLegacyPages(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	session, err := s.control.newSession(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	f := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	call := func(cursor, selection, mode string) *httptest.ResponseRecorder {
		t.Helper()
		values := url.Values{"filters": {mustJSON(f)}}
		if mode != "" {
			values.Set("receipt_view", mode)
		}
		if cursor != "" {
			values.Set("cursor", cursor)
		}
		if selection != "" {
			values.Set("selection", selection)
		}
		r := httptest.NewRequest("GET", "/v1/channel-management/query?"+values.Encode(), nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	read := func(cursor, mode string) managementPageResponse {
		t.Helper()
		w := call(cursor, "", mode)
		var page managementPageResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		return page
	}
	// Alternate both response modes, including current-page and paging reuse.
	// Neither may populate the other's computed response or validator key.
	for _, cursor := range []string{"", read("", "").NextCursor} {
		full := read(cursor, "")
		for range 2 {
			selected := read(cursor, "selected")
			if selected.Revision != full.Revision || selected.Total != full.Total || selected.EligibleCount != full.EligibleCount || len(selected.Rows) != len(full.Rows) {
				t.Fatal("projection changed filtering, ordering or selection revision")
			}
			for i, row := range selected.Rows {
				want := full.Rows[i]
				if row.Target.ReceiptModel != f.Model || row.ID != want.ID || !reflect.DeepEqual(row.Selected, want.Selected) || len(row.Checks) != len(want.Checks) || len(row.Target.Models) != len(want.Target.Models) {
					t.Fatal("selected display or complete status directory changed")
				}
				for j, check := range row.Target.Models {
					if check.Model == f.Model || check.Model == checkModel {
						if !reflect.DeepEqual(check, want.Target.Models[j]) {
							t.Fatal("selected or Astra receipt changed")
						}
					} else if check.Result.Availability.Usage != nil || check.Result.Quality.Usage != nil || want.Target.Models[j].Result.Availability.Usage == nil {
						t.Fatal("partial view exposed unused or stale receipt")
					}
				}
			}
		}
		fresh := read(cursor, "")
		fresh.ReadAtMS = full.ReadAtMS
		if !reflect.DeepEqual(fresh, full) {
			t.Fatal("selected view polluted complete legacy page")
		}
		w := call("", full.SelectionToken, "")
		var selection managementSelectionResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &selection) != nil || len(selection.Rows) != 61 || selection.Rows[0].Target.Models[0].Result.Availability.Usage == nil {
			t.Fatal("complete selection lost receipts", w.Code)
		}
		if w = call("", full.SelectionToken, "selected"); w.Code != 400 {
			t.Fatal("partial selection accepted")
		}
	}
	if w := call("", "", "other"); w.Code != 400 {
		t.Fatal("unknown view accepted")
	}
	f.Model = ""
	if w := call("", "", "selected"); w.Code != 400 {
		t.Fatal("unfiltered projection accepted")
	}
}

func TestManagementSelectedReceiptReadVolume(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	f := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	counts := []int{}
	for _, selected := range []bool{false, true} {
		logs.Reset()
		ctx := context.WithValue(context.Background(), managementSelectedReceiptsKey{}, selected)
		before := time.Now()
		page, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
		if err != nil || len(page.Page.Rows) != 25 {
			t.Fatal("page read failed", err)
		}
		match := regexp.MustCompile(`management_page_receipt_signals requested=([0-9]+)`).FindStringSubmatch(logs.String())
		if len(match) != 2 {
			t.Fatal("missing receipt read cardinality")
		}
		count, _ := strconv.Atoi(match[1])
		counts = append(counts, count)
		t.Logf("local fixture selected=%t receipt_ids=%d elapsed=%s", selected, count, time.Since(before))
	}
	if !reflect.DeepEqual(counts, []int{150, 100}) {
		t.Fatal("display receipt scope did not remove nonselected model reads", counts)
	}
}

func TestManagementSelectedReceiptScopeKeepsEveryBoundMember(t *testing.T) {
	accounts := []subAccount{{ID: "a", Targets: []subTarget{}}}
	for group := int64(1); group <= 2; group++ {
		target := subTarget{GroupID: group}
		for _, name := range []string{"gpt-5.5", checkModel, "aliased-upstream"} {
			target.Models = append(target.Models, subModelResult{Model: name, Result: &subResult{Availability: subProbe{ID: name + string(rune(group+'0'))}}})
		}
		accounts[0].Targets = append(accounts[0].Targets, target)
	}
	projected := managementPageUsageAccounts(accounts, "gpt-5.5", map[string]bool{qualityGroupID("a", 2): true})
	probes := subUsageProbes(projected)
	if len(probes) != 5 || probes["aliased-upstream1"] != nil || probes["aliased-upstream2"] == nil || len(accounts[0].Targets[0].Models) != 3 {
		t.Fatal("receipt scope omitted a bound alias or changed model membership")
	}
	if managementPageComputedKey(context.WithValue(context.Background(), managementSelectedReceiptsKey{}, true), "a", managementQueryFilters{}, "") == managementComputedKey("a", managementQueryFilters{}, "") {
		t.Fatal("partial response shares a complete-page cache key")
	}
	// A complete group read used by imports still goes through the original
	// decoder, independently of the negotiated page response.
	if strings.Contains(mustJSON(accounts), "receipt_model") {
		t.Fatal("projection marked shared source metadata")
	}
}

func TestManagementSelectedReceiptsKeepCustomModelGroupFallback(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	f := managementQueryFilters{GroupStatus: "all", Model: "custom-model-absent-from-this-group"}
	full, err := s.readManagementQuerySnapshot(context.Background(), "owner", f, "", false)
	if err != nil || len(full.Page.Rows) != 25 || full.Page.Rows[0].Selected != nil {
		t.Fatal("fixture did not use legacy group display fallback", err)
	}
	selected, err := s.readManagementQuerySnapshot(context.WithValue(context.Background(), managementSelectedReceiptsKey{}, true), "owner", f, "", false)
	if err != nil || !reflect.DeepEqual(selected.Page.Rows, full.Page.Rows) || selected.Page.Revision != full.Page.Revision {
		t.Fatal("custom-model fallback lost complete group prices", err)
	}
}

package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestManagementPageCoversCompleteSelectionAndFencesEveryRead(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	in := managementQueryInput{Accounts: []subAccount{{ID: "account", Name: "site", State: "idle", Targets: []subTarget{}}}}
	for i := 1; i <= 61; i++ {
		in.Accounts[0].Targets = append(in.Accounts[0].Targets, subTarget{GroupID: int64(i), Name: fmt.Sprint(i), Active: true, KeyID: int64(i), State: "done", Billing: &subBilling{Rate: usageFloat(float64(i % 3))}})
	}
	in.Accounts[0].Targets[0].KeyID = 0
	in.Accounts[0].Targets[1].State = "error"
	filters := managementQueryFilters{GroupStatus: "available", Sort: "asc"}
	evidence := managementFilterEvidence{FingerprintReady: true, Prices: []Price{{Model: "gpt-5.5", Input: 1, Output: 2, Verified: true}}}
	readAt := time.Unix(123, 0)
	first, err := buildManagementQueryPage(in, filters, evidence, "owner", key, "", readAt)
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 61 || first.EligibleCount != 59 || len(first.Rows) != 25 || first.PageSize != 25 || first.Offset != 0 || first.NextCursor == "" || first.PreviousCursor != "" || first.ReadAtMS != 123000 {
		t.Fatalf("incomplete first page/count: %+v", first)
	}
	if !reflect.DeepEqual(first.Rates, []float64{0, 1, 2}) {
		t.Fatal("page-local facets", first.Rates)
	}
	seen := map[string]bool{}
	var order []string
	page := first
	for {
		if page.Total != 61 || page.EligibleCount != 59 || page.Revision != first.Revision {
			t.Fatal("counts or revision changed across pages")
		}
		for _, row := range page.Rows {
			if seen[row.ID] {
				t.Fatal("duplicate row", row.ID)
			}
			seen[row.ID] = true
			order = append(order, row.ID)
			if len(row.Account.Targets) != 0 {
				t.Fatal("page included hidden account result payload")
			}
		}
		if page.NextCursor == "" {
			break
		}
		page, err = buildManagementQueryPage(in, filters, evidence, "owner", key, page.NextCursor, readAt.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 61 || len(page.Rows) != 11 || page.Offset != 50 {
		t.Fatal("selection truncated", len(seen), page.Offset)
	}
	for rate := 0; rate < 3; rate++ {
		expected := []string{}
		for group := 1; group <= 61; group++ {
			if group%3 == rate {
				expected = append(expected, fmt.Sprintf("account:%d", group))
			}
		}
		if !reflect.DeepEqual(order[:len(expected)], expected) {
			t.Fatal("equal-rate ordering changed", rate)
		}
		order = order[len(expected):]
	}
	back, err := buildManagementQueryPage(in, filters, evidence, "owner", key, page.PreviousCursor, readAt)
	if err != nil || back.Offset != 25 || len(back.Rows) != 25 {
		t.Fatal("previous page", err, back.Offset)
	}
	for _, variant := range []struct {
		owner  string
		key    []byte
		cursor string
	}{
		{"other", key, first.NextCursor}, {"owner", []byte("different"), first.NextCursor}, {"owner", key, "A" + first.NextCursor[1:]},
	} {
		if _, err := buildManagementQueryPage(in, filters, evidence, variant.owner, variant.key, variant.cursor, readAt); !errors.Is(err, errManagementCursor) {
			t.Fatal("foreign/forged cursor accepted", err)
		}
	}
	changedFilter := filters
	changedFilter.Search = "site"
	if _, err := buildManagementQueryPage(in, changedFilter, evidence, "owner", key, first.NextCursor, readAt); !errors.Is(err, errManagementCursor) {
		t.Fatal("cursor reused for different filters", err)
	}
	changedEvidence := evidence
	changedEvidence.Prices = []Price{{Model: "gpt-5.5", Input: 3, Output: 2, Verified: true}}
	if _, err := buildManagementQueryPage(in, filters, changedEvidence, "owner", key, first.NextCursor, readAt); !errors.Is(err, errManagementRevision) {
		t.Fatal("price revision silently changed", err)
	}
	in.Accounts[0].Targets[30].State = "running"
	if _, err := buildManagementQueryPage(in, filters, evidence, "owner", key, first.NextCursor, readAt); !errors.Is(err, errManagementRevision) {
		t.Fatal("concurrent update mixed with old page", err)
	}
	fresh, err := buildManagementQueryPage(in, filters, evidence, "owner", key, "", readAt)
	if err != nil || fresh.Revision == first.Revision {
		t.Fatal("fresh read did not publish new revision", err)
	}
}

func TestManagementPageEmptyAndDuplicateBoundSelection(t *testing.T) {
	filters := managementQueryFilters{GroupStatus: "available"}
	page, err := buildManagementQueryPage(managementQueryInput{}, filters, managementFilterEvidence{}, "owner", []byte("key"), "", time.Now())
	if err != nil || page.Total != 0 || page.EligibleCount != 0 || len(page.Rows) != 0 || page.NextCursor != "" {
		t.Fatal(page, err)
	}
	rows := []managementQueryRow{
		{ID: "site", Account: subAccount{ID: "a"}, Target: subTarget{GroupID: 1, KeyID: 1, Active: true}},
		{ID: "bound-copy", Account: subAccount{ID: "a"}, Target: subTarget{GroupID: 1, KeyID: 1, Active: true}},
		{ID: "pending", Account: subAccount{ID: "b", State: "running"}, Target: subTarget{GroupID: 1, KeyID: 1, Active: true}},
		{ID: "native", Configured: &managementQueryChannel{managementChannel: managementChannel{subInstalledChannel: subInstalledChannel{Provider: "native"}, Engine: "gpt"}}},
		{ID: "other-engine", Configured: &managementQueryChannel{managementChannel: managementChannel{subInstalledChannel: subInstalledChannel{Provider: "native"}, Engine: "claude"}}},
	}
	selected := managementEligibleRows(rows)
	if len(selected) != 3 || selected[0].ID != "site" || selected[1].ID != "native" || selected[2].ID != "other-engine" {
		t.Fatal("batch count dedupe changed", selected)
	}
}

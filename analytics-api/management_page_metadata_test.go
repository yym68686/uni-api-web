package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagementMetadataTransportKeepsLivePageAndCompleteCache(t *testing.T) {
	page := managementWireFixture()
	page.Owner = "owner"
	s := &Service{}
	write := func(p managementPageResponse, client string) managementColumnPage {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/query", nil)
		r.Header.Set("Accept", managementPageV3MediaType)
		r.Header.Set(managementMetadataHeader, client)
		w := httptest.NewRecorder()
		if err := s.writeAndRememberManagementPage(w, r, p, "key", &managementFreshProof{Stamp: "exact"}); err != nil || w.Code != 200 {
			t.Fatalf("failed to deliver page: %s err=%v", w.Body.String(), err)
		}
		if !strings.Contains(strings.Join(w.Header().Values("Vary"), ","), managementMetadataHeader) {
			t.Fatal("missing transport variant")
		}
		var wire managementColumnPage
		if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		return wire
	}
	first := write(page, "1")
	if len(first.MetadataVersion) != 64 || len(first.Models) != len(page.Models) {
		t.Fatal("client without retained metadata did not receive the catalog")
	}
	second := write(page, first.MetadataVersion)
	if second.MetadataVersion != first.MetadataVersion || second.Models != nil || second.Context.Prices != nil ||
		mustJSON(second.Rows) != mustJSON(first.Rows) || mustJSON(second.Accounts) != mustJSON(first.Accounts) ||
		second.SelectionToken != first.SelectionToken || mustJSON(second.Context.Fingerprints) != mustJSON(first.Context.Fingerprints) {
		t.Fatal("projection omitted current page data or resent shared metadata")
	}
	stored, ok := s.managementComputed.get("key")
	if !ok || mustJSON(stored.Page.Models) != mustJSON(page.Models) ||
		mustJSON(stored.Page.Context.Prices) != mustJSON(page.Context.Prices) {
		t.Fatal("metadata delta entered the complete-page cache")
	}
	changed := page
	changed.Models = append(append([]string{}, page.Models...), "new-model")
	full := write(changed, first.MetadataVersion)
	if full.MetadataVersion == first.MetadataVersion || len(full.Models) != len(page.Models)+1 {
		t.Fatal("metadata change did not return a full catalog in one response")
	}
	changed.Owner = "other"
	if other := write(changed, full.MetadataVersion); other.MetadataVersion == full.MetadataVersion || other.Models == nil {
		t.Fatal("metadata was reused across owners")
	}
	if old := write(page, ""); old.MetadataVersion != "" || old.Models == nil {
		t.Fatal("legacy client negotiated incomplete metadata")
	}
}

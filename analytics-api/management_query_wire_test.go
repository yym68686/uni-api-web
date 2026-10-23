package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func managementWireFixture() managementPageResponse {
	result := &subResult{Model: "upstream-模型", CheckedAt: 123, Verdict: "pass", Availability: subProbe{Status: "success", RequestedModel: "alias", ResponseModel: "upstream-模型", ModelMatch: "match", Message: strings.Repeat("完整诊断🙂", 30), TTFT: usageInt(0), Usage: &subUsage{Status: "matched", ActualCost: usageFloat(0), TotalCost: usageFloat(1), InputPrice: usageFloat(2)}}, Quality: subProbe{Status: "success"}}
	same := *result
	different := same
	different.Availability.Status = "error"
	different.Verdict = "fail"
	check := managementQueryCheck{subModelResult: subModelResult{Model: "alias", State: "done", Message: "原始状态", Result: result}, SourceName: "Source"}
	row := managementQueryRow{ID: "a:1", Account: subAccount{ID: "a", Name: "synthetic", Base: "https://fixture.test", Targets: []subTarget{}}, AccountIDs: []string{"a"}, Target: subTarget{GroupID: 1, Name: "Group", Models: []subModelResult{{Model: "actual", State: "done", Result: result}, {Model: "equal-copy", State: "done", Result: &same}, {Model: "pending", State: "queued"}}, Result: result}, Checks: []managementQueryCheck{check, {subModelResult: subModelResult{Model: "other", State: "error", Result: &different}}}, Selected: &check}
	other := row
	other.ID = "a:2"
	other.Target.GroupID = 2
	other.Target.Result = nil
	other.Selected = nil
	empty := managementQueryRow{ID: "a:3", Account: row.Account, AccountIDs: []string{"a"}, Target: subTarget{GroupID: 3}, Checks: []managementQueryCheck{}}
	return managementPageResponse{managementQueryPage: managementQueryPage{Format: "management-page-v1", Revision: "same-current-revision", ReadAtMS: 123, Rows: []managementQueryRow{row, other, empty}, Total: 3, EligibleCount: 2, PageSize: 25, Rates: []float64{0, 1}, Platforms: []string{"openai"}, Models: []string{"alias", "actual", "other"}, DetectionModels: []string{"alias", "other"}}, SelectionToken: "signed-current-selection", Accounts: []subAccount{row.Account}, Context: managementPageContext{Prices: []Price{}, Imports: []subInstalledChannel{}, Routes: map[string]managementQueryRoutes{}, Fingerprints: map[string][]managementFingerprintEntry{"a:1": {}, "a:2": {}, "a:3": {}}, FingerprintModels: []string{}}}
}

func decodeManagementWireForTest(t *testing.T, wire managementWirePage) managementPageResponse {
	t.Helper()
	out := wire.managementPageResponse
	out.Format = "management-page-v1"
	ref := func(i *int) *subResult {
		if i == nil {
			return nil
		}
		if *i < 0 || *i >= len(wire.Results) {
			t.Fatal("invalid generated reference")
		}
		r := &subResult{}
		if err := json.Unmarshal(wire.Results[*i], r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	model := func(m managementWireModel) subModelResult {
		return subModelResult{Model: m.Model, State: m.State, Message: m.Message, Result: ref(m.ResultRef)}
	}
	check := func(c managementWireCheck) managementQueryCheck {
		return managementQueryCheck{subModelResult: model(c.managementWireModel), SourceName: c.SourceName}
	}
	if wire.Rows != nil {
		out.Rows = make([]managementQueryRow, len(wire.Rows))
	}
	for i, w := range wire.Rows {
		row := w.managementQueryRow
		row.Target = w.Target.subTarget
		row.Target.Result = ref(w.Target.ResultRef)
		if w.Target.Models != nil {
			row.Target.Models = make([]subModelResult, len(w.Target.Models))
		}
		for j, m := range w.Target.Models {
			row.Target.Models[j] = model(m)
		}
		if w.Checks != nil {
			row.Checks = make([]managementQueryCheck, len(w.Checks))
		}
		for j, c := range w.Checks {
			row.Checks[j] = check(c)
		}
		if w.Selected != nil {
			c := check(*w.Selected)
			row.Selected = &c
		}
		out.Rows[i] = row
	}
	return out
}

func TestManagementWirePreservesCompleteJSONAndSnapshot(t *testing.T) {
	page := managementWireFixture()
	before, _ := json.Marshal(page)
	encoded, err := encodeManagementPage(page)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var wire managementWirePage
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Results) != 2 {
		t.Fatal("identical results were not deduplicated", len(wire.Results))
	}
	got, _ := json.Marshal(decodeManagementWireForTest(t, wire))
	after, _ := json.Marshal(page)
	if !bytes.Equal(before, got) || !bytes.Equal(before, after) {
		t.Fatal("transport changed evidence or mutated input")
	}
	if len(raw) >= len(before) {
		t.Fatal("wire dictionary did not remove repeated payload")
	}
	if os.Getenv("UPDATE_MANAGEMENT_WIRE_FIXTURE") == "1" {
		fixture, _ := json.Marshal(map[string]any{"page": page, "wire": encoded})
		file, err := os.Create("testdata/management-page-wire.json.gz")
		if err != nil {
			t.Fatal(err)
		}
		writer := gzip.NewWriter(file)
		_, err = writer.Write(fixture)
		if err != nil {
			t.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	invalid := managementWireFixture()
	invalid.Rows[0].Checks[0].Result.Availability.Usage.InputPrice = usageFloat(math.NaN())
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept", managementPageV2MediaType)
	response := httptest.NewRecorder()
	if err := writeManagementPage(response, request, invalid); err == nil || response.Code != 500 {
		t.Fatal("invalid evidence published successfully")
	}
}

func TestManagementHTTPNegotiatesPageWithoutBreakingOldClients(t *testing.T) {
	s := managementHydrationFixture(t)
	token, err := s.control.newSession(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	var legacy managementPageResponse
	for _, accept := range []string{"application/json", managementPageV2MediaType} {
		r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/query", nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		r.Header.Set("Accept", accept)
		r.Header.Set("Accept-Encoding", "gzip")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(strings.Join(w.Header().Values("Vary"), ","), "Accept") {
			t.Fatal(w.Code, w.Header())
		}
		reader, err := gzip.NewReader(w.Body)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if accept == "application/json" {
			if err := json.Unmarshal(raw, &legacy); err != nil {
				t.Fatal(err)
			}
			if legacy.Format != "management-page-v1" {
				t.Fatal("old client unexpectedly upgraded")
			}
		} else {
			var wire managementWirePage
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			if wire.Format != "management-page-v2" || w.Header().Get("Content-Type") != managementPageV2MediaType {
				t.Fatal("negotiation failed")
			}
			page := decodeManagementWireForTest(t, wire)
			if page.Revision != legacy.Revision || page.SelectionToken != legacy.SelectionToken || !reflect.DeepEqual(page.Rows, legacy.Rows) || !reflect.DeepEqual(page.Context, legacy.Context) || page.Total != 61 || len(page.Rows) != 25 {
				t.Fatal("negotiated page changed current result")
			}
		}
	}
}

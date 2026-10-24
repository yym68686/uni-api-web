package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func decodeManagementColumnsForTest(t *testing.T, c managementResultColumns) []map[string]json.RawMessage {
	t.Helper()
	out := make([]map[string]json.RawMessage, c.Count)
	for i := range out {
		out[i] = map[string]json.RawMessage{}
	}
	for j, f := range c.Fields {
		for i, cell := range c.Columns[j] {
			if len(cell) == 1 {
				out[i][f] = cell[0]
			}
		}
	}
	return out
}
func TestManagementColumnsPreserveEveryValueAndLiveHeader(t *testing.T) {
	page := managementWireFixture()
	page.Owner = "owner"
	wire, err := encodeManagementColumnPage(page)
	if err != nil {
		t.Fatal(err)
	}

	if mustJSON(decodeManagementColumnPageForTest(t, wire)) != mustJSON(page) {
		t.Fatal("column representation changed evidence")
	}
	page.preparedColumns = &wire
	page.ReadAtMS = 999
	page.Accounts[0].Balance = &subAccountBalance{Amount: usageFloat(123), CheckedAt: 888, Status: "ok"}
	page.Rows[0].Account = page.Accounts[0]
	fresh, err := currentManagementColumnPage(page)
	if err != nil {
		t.Fatal(err)
	}
	if mustJSON(decodeManagementColumnPageForTest(t, fresh)) != mustJSON(page) {
		t.Fatal("prepared columns lost current headers")
	}
	if wire.ReadAtMS == 999 || wire.Rows[0].Account.Balance != nil {
		t.Fatal("retained columns mutated")
	}
	for _, accept := range []string{"application/json", managementPageV2MediaType, managementPageV3MediaType} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept", accept)
		w := httptest.NewRecorder()
		if err := writeManagementPage(w, r, page); err != nil || w.Code != 200 {
			t.Fatal(err, w.Code)
		}
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want := "management-page-v1"
		if accept == managementPageV2MediaType {
			want = "management-page-v2"
		}
		if accept == managementPageV3MediaType {
			want = "management-page-v3"
			if _, ok := got["results"]; ok {
				t.Fatal("duplicate full result table")
			}
		}
		if got["format"] != want || got["owner"] != "owner" {
			t.Fatal("wrong negotiation or owner")
		}
	}
}
func TestManagementColumnsDistinguishAbsentNullAndZero(t *testing.T) {
	rows := []map[string]json.RawMessage{{"a": json.RawMessage(`null`), "unicode": json.RawMessage(`"模型🙂"`)}, {"a": json.RawMessage(`0`), "boolean": json.RawMessage(`false`)}, {"empty": json.RawMessage(`[]`)}, {}}
	if mustJSON(decodeManagementColumnsForTest(t, managementColumns(rows))) != mustJSON(rows) {
		t.Fatal("column data changed")
	}
}

func decodeManagementColumnPageForTest(t *testing.T, wire managementColumnPage) managementPageResponse {

	meta, a, q := decodeManagementColumnsForTest(t, wire.ResultFields), decodeManagementColumnsForTest(t, wire.Availability), decodeManagementColumnsForTest(t, wire.Quality)
	old := wire.managementWirePage
	for i, m := range meta {
		m["availability"], _ = json.Marshal(a[i])
		m["quality"], _ = json.Marshal(q[i])
		raw, _ := json.Marshal(m)
		old.Results = append(old.Results, raw)
	}
	return decodeManagementWireForTest(t, old)

}

func TestManagementStoredColumnsDoNotRetainExpandedEvidence(t *testing.T) {
	s := &Service{}
	page := managementWireFixture()
	s.rememberManagementPage("first", &managementFreshProof{Stamp: "current"}, page)
	stored := s.managementComputed.pages["first"]
	if stored.Page.preparedColumns == nil {
		t.Fatal("canonical representation missing")
	}
	if stored.Page.Rows[0].Checks != nil || stored.Page.Rows[0].Target.Result != nil || stored.Page.Rows[0].Target.Models != nil || stored.Page.preparedColumns.managementPageResponse.Rows != nil {
		t.Fatal("shadowed expanded evidence still retained")
	}
	raw, err := json.Marshal(stored.Page.preparedColumns)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Bytes != len(raw) {
		t.Fatal("budget does not count canonical representation", stored.Bytes, len(raw))
	}
	expanded, err := expandManagementColumnPage(*stored.Page.preparedColumns)
	if err != nil || mustJSON(expanded) != mustJSON(page) {
		t.Fatal("canonical store changed complete data", err)
	}
	page.Rows[0].Checks[0].Result.Availability.Message = "mutated caller"
	again, err := expandManagementColumnPage(*stored.Page.preparedColumns)
	if err != nil || mustJSON(again) != mustJSON(expanded) {
		t.Fatal("caller mutated retained result", err)
	}
	// Independent expansion is required by legacy JSON clients too.
	again.Rows[0].Checks[0].Result.Availability.Message = "legacy edit"
	if again.Rows[1].Checks[0].Result.Availability.Message == "legacy edit" {
		t.Fatal("shared mutable legacy result")
	}
}

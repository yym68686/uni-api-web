package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Independent previous writer: the complete column JSON was marshaled once
// for HTTP, then marshaled a second time by rememberManagementColumnPage.
func previousManagementWriteAndMarshalStore(s *Service, w http.ResponseWriter, r *http.Request, page managementPageResponse, proof *managementFreshProof) error {
	w.Header().Add("Vary", "Accept")
	headerTime := managementColumnReadHeaders(w, r, page.ReadAtMS)
	columns, err := encodeManagementColumnPage(page)
	if err != nil {
		http.Error(w, "渠道响应编码失败", http.StatusInternalServerError)
		return err
	}
	delivered := columns
	if headerTime {
		delivered.ReadAtMS = 0
	}
	err = writeManagementJSONMedia(w, r, delivered, managementPageV3MediaType)
	if err == nil {
		s.rememberManagementColumnPage("page", proof, columns)
	}
	return err
}

func TestManagementResponseJSONReusePreservesHTTPAndStoreClock(t *testing.T) {
	for _, at := range []int64{0, 1, -1, 1793235760840, math.MinInt64, math.MaxInt64} {
		for _, validation := range []string{"", "1"} {
			for _, compression := range []string{"", "gzip", "br"} {
				for _, proof := range []*managementFreshProof{nil, {}, {Stamp: "current", CurrentExport: true, ExportStamp: "current-export", HeadersRevision: 9}} {
					page := managementWireFixture()
					page.ReadAtMS = at
					// Nested clock-like text must not be patched or interpreted.
					page.Rows[0].Target.Message = `read_at_ms":0,"read_at_ms":123`
					before := mustJSON(page)
					request := func(tag string) *http.Request {
						r := httptest.NewRequest("GET", "/", nil)
						r = r.WithContext(context.WithValue(r.Context(), consoleRequestIDKey{}, "reuse-json-123"))
						r.Header.Set("Accept", managementPageV3MediaType)
						r.Header.Set("Accept-Encoding", compression)
						r.Header.Set("X-Console-Current-Validation", validation)
						r.Header.Set("If-None-Match", tag)
						return r
					}
					tag := ""
					for attempt := 0; attempt < 2; attempt++ {
						oldService, newService := &Service{}, &Service{}
						old, got := httptest.NewRecorder(), httptest.NewRecorder()
						oldErr := previousManagementWriteAndMarshalStore(oldService, old, request(tag), page, proof)
						gotErr := newService.writeAndRememberManagementPage(got, request(tag), page, "page", proof)
						if fmt.Sprint(oldErr) != fmt.Sprint(gotErr) || old.Code != got.Code || !reflect.DeepEqual(old.Header(), got.Header()) || old.Body.String() != got.Body.String() {
							t.Fatal("HTTP encoding, validation or clock changed", at, validation, compression, attempt)
						}
						if mustJSON(oldService.managementComputed.pages["page"]) != mustJSON(newService.managementComputed.pages["page"]) || newService.managementComputed.bytes != oldService.managementComputed.bytes {
							t.Fatal("canonical representation, proof, clock or budget changed", at)
						}
						if proof != nil && proof.Stamp != "" {
							stored := newService.managementComputed.pages["page"]
							canonical, err := json.Marshal(stored.Page.preparedColumns)
							if err != nil || stored.Bytes != len(canonical) || stored.Page.ReadAtMS != at || stored.Page.preparedColumns.ReadAtMS != at {
								t.Fatal("retained time or exact canonical bytes lost", at, err)
							}
						}
						tag = old.Header().Get("ETag")
					}
					if mustJSON(page) != before {
						t.Fatal("caller graph was mutated")
					}
				}
			}
		}
	}
}

func TestManagementResponseJSONReusePreservesExactAdmissionLimit(t *testing.T) {
	for _, at := range []int64{math.MinInt64, 0, math.MaxInt64} {
		page := managementWireFixture()
		page.ReadAtMS = at
		columns, err := encodeManagementColumnPage(page)
		if err != nil {
			t.Fatal(err)
		}
		base, err := json.Marshal(columns)
		if err != nil {
			t.Fatal(err)
		}
		for _, delta := range []int{0, 1} {
			columns.Revision = page.Revision + strings.Repeat("x", (16<<20)-len(base)+delta)
			original, err := json.Marshal(columns)
			if err != nil || len(original) != (16<<20)+delta {
				t.Fatal("invalid boundary fixture", err, len(original))
			}
			delivered := columns
			delivered.ReadAtMS = 0
			body, err := json.Marshal(delivered)
			if err != nil {
				t.Fatal(err)
			}
			s := &Service{}
			s.rememberManagementColumnBytes("page", &managementFreshProof{Stamp: "current"}, body, 0, at)
			stored, admitted := s.managementComputed.pages["page"]
			if admitted != (delta == 0) || admitted && stored.Bytes != len(original) || s.managementComputed.bytes > 16<<20 {
				t.Fatal("HTTP zero clock changed exact admission boundary", at, delta, admitted, stored.Bytes)
			}
		}
	}
}

func TestManagementResponseJSONReuseKeepsIndependentClone(t *testing.T) {
	columns, err := encodeManagementColumnPage(managementWireFixture())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(columns)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{}
	proof := &managementFreshProof{Stamp: "current"}
	s.rememberManagementColumnBytes("page", proof, raw, columns.ReadAtMS, columns.ReadAtMS)
	before := mustJSON(s.managementComputed.pages["page"])
	for i := range raw {
		raw[i] = 'x'
	}
	columns.Rows[0].Target.Models[0].Model = "caller edited"
	columns.Availability.Columns[0][0][0][0] = 'x'
	if mustJSON(s.managementComputed.pages["page"]) != before {
		t.Fatal("HTTP bytes or live columns alias retained evidence")
	}
	for _, raw := range [][]byte{nil, []byte(`{`), []byte(`{"read_at_ms":1}`)} {
		s.rememberManagementColumnBytes("invalid", proof, raw, 0, 123)
		if _, exists := s.managementComputed.pages["invalid"]; exists {
			t.Fatal("invalid JSON or unexpected encoded clock admitted")
		}
	}
}

func BenchmarkManagementResponseJSONReuse(b *testing.B) {
	page := managementWireFixture()
	page.Rows = nil
	for g := 0; g < 25; g++ {
		row := managementQueryRow{ID: strconv.Itoa(g), Target: subTarget{GroupID: int64(g)}}
		for m := 0; m < 288; m++ {
			result := &subResult{Model: fmt.Sprint("model-", m), CheckedAt: int64(g*288 + m), Availability: subProbe{Status: "success", Message: "current complete diagnostic", Usage: &subUsage{Status: "matched", InputPrice: usageFloat(2)}}, Quality: subProbe{Status: "skipped"}}
			model := subModelResult{Model: result.Model, State: "done", Result: result}
			row.Target.Models = append(row.Target.Models, model)
			row.Checks = append(row.Checks, managementQueryCheck{subModelResult: model})
		}
		page.Rows = append(page.Rows, row)
	}
	proof := &managementFreshProof{Stamp: "current"}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept", managementPageV3MediaType)
	r.Header.Set("X-Console-Current-Validation", "1")
	r = r.WithContext(context.WithValue(r.Context(), consoleRequestIDKey{}, "reuse-json-123"))
	for _, name := range []string{"previous_two_marshals", "reuse_http_json"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				s := &Service{}
				w := httptest.NewRecorder()
				var err error
				if name == "previous_two_marshals" {
					err = previousManagementWriteAndMarshalStore(s, w, r, page, proof)
				} else {
					err = s.writeAndRememberManagementPage(w, r, page, "page", proof)
				}
				if err != nil || len(s.managementComputed.pages) != 1 {
					b.Fatal("incomplete response or store", err)
				}
			}
		})
	}
}

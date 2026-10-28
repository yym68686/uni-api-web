package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestManagementPagingEncodingPreservesHTTPAndRetainedProof(t *testing.T) {
	for _, shape := range []string{"full", "nil_rows", "empty_rows", "invalid_result"} {
		for _, media := range []string{"application/json", managementPageV2MediaType, managementPageV3MediaType} {
			for _, validation := range []string{"", "1"} {
				for _, compression := range []string{"", "gzip", "br"} {
					for proofIndex, proof := range []*managementFreshProof{nil, {}, {Stamp: "current", HeadersRevision: 7, CurrentExport: true, ExportStamp: "export"}} {
						t.Run(fmt.Sprintf("%s/%s/%s/%s/%d", shape, media, validation, compression, proofIndex), func(t *testing.T) {
							page := managementWireFixture()
							page.Owner = "owner"
							page.Offset = 25
							page.PreviousCursor = "signed-previous"
							if shape == "nil_rows" {
								page.Rows = nil
							} else if shape == "empty_rows" {
								page.Rows = []managementQueryRow{}
							} else if shape == "invalid_result" {
								page.Rows[0].Target.Result.Availability.Usage = &subUsage{InputPrice: usageFloat(math.NaN())}
							}
							before := mustJSON(page)
							request := func(tag string) *http.Request {
								r := httptest.NewRequest("GET", "/v1/channel-management/query", nil)
								r = r.WithContext(context.WithValue(r.Context(), consoleRequestIDKey{}, "paging-encode-123"))
								r.Header.Set("Accept", media)
								r.Header.Set("Accept-Encoding", compression)
								r.Header.Set("X-Console-Current-Validation", validation)
								r.Header.Set("If-None-Match", tag)
								return r
							}
							tag := ""
							for attempt := 0; attempt < 2; attempt++ {
								previous, current := &Service{}, &Service{}
								// The previous paging path retained before writing, but
								// discarded the columns and encoded them again for HTTP.
								previous.rememberManagementPage("page", proof, page)
								prepared := page
								prepared.preparedColumns = current.rememberManagementPage("page", proof, page)
								old, got := httptest.NewRecorder(), httptest.NewRecorder()
								oldErr := writeManagementPage(old, request(tag), page)
								gotErr := writeManagementPage(got, request(tag), prepared)
								if fmt.Sprint(oldErr) != fmt.Sprint(gotErr) || old.Code != got.Code || !reflect.DeepEqual(old.Header(), got.Header()) || old.Body.String() != got.Body.String() {
									t.Fatal("HTTP bytes, headers, status or error changed", oldErr, gotErr)
								}
								if mustJSON(previous.managementComputed.pages["page"]) != mustJSON(current.managementComputed.pages["page"]) || mustJSON(page) != before {
									t.Fatal("retained proof, columns, budget or caller input changed")
								}
								if proofIndex == 2 && shape != "invalid_result" && shape != "nil_rows" && prepared.preparedColumns == nil {
									t.Fatal("valid encoded page was discarded")
								}
								tag = old.Header().Get("ETag")
							}
						})
					}
				}
			}
		}
	}
}

func TestManagementPagingRequestColumnsCannotMutateRetainedState(t *testing.T) {
	s := &Service{}
	page := managementWireFixture()
	columns := s.rememberManagementPage("page", &managementFreshProof{Stamp: "current"}, page)
	stored := s.managementComputed.pages["page"]
	if columns == nil || columns == stored.Page.preparedColumns {
		t.Fatal("request columns alias retained representation")
	}
	before := mustJSON(stored)
	columns.Availability.Fields[0] = "changed"
	columns.Rows[0].Target.Models[0].Model = "changed"
	columns.ReadAtMS = -1
	page.Rows[0].Target.Result.Availability.Message = "caller changed"
	if mustJSON(s.managementComputed.pages["page"]) != before {
		t.Fatal("request or caller changed retained response")
	}
}

func TestManagementPagingColumnsRemainDeliverableAfterStoreAdmissionRejects(t *testing.T) {
	s := &Service{}
	page := managementPageResponse{managementQueryPage: managementQueryPage{Rows: []managementQueryRow{}, Revision: strings.Repeat("x", 17<<20)}}
	prepared := page
	prepared.preparedColumns = s.rememberManagementPage("oversized", &managementFreshProof{Stamp: "current"}, page)
	if prepared.preparedColumns == nil || len(s.managementComputed.pages) != 0 {
		t.Fatal("admission rejection lost request-local columns or retained oversized data")
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept", managementPageV3MediaType)
	old, got := httptest.NewRecorder(), httptest.NewRecorder()
	oldErr := writeManagementPage(old, r, page)
	gotErr := writeManagementPage(got, r, prepared)
	if fmt.Sprint(oldErr) != fmt.Sprint(gotErr) || old.Code != got.Code || !reflect.DeepEqual(old.Header(), got.Header()) || old.Body.String() != got.Body.String() {
		t.Fatal("admission changed the delivered response")
	}
}

package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
)

// Previous fresh-response writer, independent of the new shared encoding.
func previousWriteManagementPage(w http.ResponseWriter, r *http.Request, page managementPageResponse) error {
	w.Header().Add("Vary", "Accept")
	if r.Header.Get("Accept") == managementPageV3MediaType {
		if r.Header.Get("X-Console-Current-Validation") == "1" {
			w.Header().Set("X-Console-Read-At-Ms", strconv.FormatInt(page.ReadAtMS, 10))
			w.Header().Set("X-Console-Validated-Request-ID", consoleRequestID(r.Context()))
			page.ReadAtMS = 0
		}
		wire, err := currentManagementColumnPage(page)
		if err != nil {
			http.Error(w, "渠道响应编码失败", 500)
			return err
		}
		return writeManagementJSONMedia(w, r, wire, managementPageV3MediaType)
	}
	if r.Header.Get("Accept") != managementPageV2MediaType {
		return writeManagementJSON(w, r, page)
	}
	wire, err := encodeManagementPage(page)
	if err != nil {
		http.Error(w, "渠道响应编码失败", 500)
		return err
	}
	return writeManagementJSONMedia(w, r, wire, managementPageV2MediaType)
}

func TestManagementFreshEncodingPreservesHTTPAndCanonicalStore(t *testing.T) {
	for _, shape := range []string{"full", "nil_rows", "empty_rows", "invalid_result"} {
		for _, media := range []string{"application/json", managementPageV2MediaType, managementPageV3MediaType} {
			for _, validation := range []string{"", "1"} {
				for _, compression := range []string{"", "gzip", "br"} {
					t.Run(shape+"/"+media+"/"+validation+"/"+compression, func(t *testing.T) {
						page := managementWireFixture()
						page.Owner = "owner"
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
							r = r.WithContext(context.WithValue(r.Context(), consoleRequestIDKey{}, "encoding-once-123"))
							r.Header.Set("Accept", media)
							r.Header.Set("Accept-Encoding", compression)
							r.Header.Set("X-Console-Current-Validation", validation)
							r.Header.Set("If-None-Match", tag)
							return r
						}
						proof := &managementFreshProof{Stamp: "current"}
						tag := ""
						for attempt := 0; attempt < 2; attempt++ {
							previous, current := &Service{}, &Service{}
							old, got := httptest.NewRecorder(), httptest.NewRecorder()
							oldErr := previousWriteManagementPage(old, request(tag), page)
							if oldErr == nil {
								previous.rememberManagementPage("page", proof, page)
							}
							gotErr := current.writeAndRememberManagementPage(got, request(tag), page, "page", proof)
							if (oldErr != nil) != (gotErr != nil) || old.Code != got.Code || !reflect.DeepEqual(old.Header(), got.Header()) || old.Body.String() != got.Body.String() {
								t.Fatal("HTTP bytes, headers, status or error changed", attempt, old.Code, got.Code, oldErr, gotErr)
							}
							if mustJSON(previous.managementComputed.pages["page"]) != mustJSON(current.managementComputed.pages["page"]) {
								t.Fatal("retained proof, read time, canonical bytes or budget changed")
							}
							if shape != "invalid_result" {
								stored := current.managementComputed.pages["page"]
								if stored.Page.ReadAtMS != page.ReadAtMS {
									t.Fatal("HTTP validation clock leaked into retained metadata")
								}
								if attempt == 1 {
									want := http.StatusOK
									if media == managementPageV3MediaType && validation == "1" {
										want = http.StatusNotModified
									}
									if old.Code != want {
										t.Fatal("fixture did not exercise correct conditional response", old.Code, want)
									}
								}
							}
							tag = old.Header().Get("ETag")
						}
						if mustJSON(page) != before {
							t.Fatal("caller response mutated")
						}
					})
				}
			}
		}
	}
}

type managementEncodeFailedWriter struct{ header http.Header }

func (w *managementEncodeFailedWriter) Header() http.Header { return w.header }
func (*managementEncodeFailedWriter) WriteHeader(int)       {}
func (*managementEncodeFailedWriter) Write([]byte) (int, error) {
	return 0, errors.New("fixture response disconnected")
}

func TestManagementSharedEncodingDoesNotRetainFailedWritesOrCallerAliases(t *testing.T) {
	proof := &managementFreshProof{Stamp: "current"}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept", managementPageV3MediaType)
	s := &Service{}
	if err := s.writeAndRememberManagementPage(&managementEncodeFailedWriter{header: http.Header{}}, r, managementWireFixture(), "failed", proof); err == nil || len(s.managementComputed.pages) != 0 {
		t.Fatal("failed response retained")
	}
	page := managementWireFixture()
	if err := s.writeAndRememberManagementPage(httptest.NewRecorder(), r, page, "success", proof); err != nil {
		t.Fatal(err)
	}
	stored := mustJSON(s.managementComputed.pages["success"])
	page.Rows[0].Target.Result.Availability.Message = "mutated caller"
	page.Rows[0].Checks[0].Result.Quality.Message = "mutated caller"
	if mustJSON(s.managementComputed.pages["success"]) != stored {
		t.Fatal("caller still aliases retained data")
	}
	for _, unavailable := range []*managementFreshProof{nil, {}} {
		uncached := &Service{}
		if err := uncached.writeAndRememberManagementPage(httptest.NewRecorder(), r, page, "absent", unavailable); err != nil || len(uncached.managementComputed.pages) != 0 {
			t.Fatal("unproven page retained", err)
		}
	}
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	canceled := &Service{}
	if err := canceled.writeAndRememberManagementPage(httptest.NewRecorder(), r.WithContext(ctx), page, "canceled", proof); !errors.Is(err, context.Canceled) || len(canceled.managementComputed.pages) != 0 {
		t.Fatal("canceled write retained", err)
	}
}

func BenchmarkManagementFreshWriteAndStore(b *testing.B) {
	page := managementWireFixture()
	page.Rows = nil
	for g := 0; g < 25; g++ {
		row := managementQueryRow{ID: fmt.Sprint(g), Target: subTarget{GroupID: int64(g)}}
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
	for _, name := range []string{"previous_twice", "shared_once"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				s := &Service{}
				w := httptest.NewRecorder()
				var err error
				if name == "previous_twice" {
					err = previousWriteManagementPage(w, r, page)
					if err == nil {
						s.rememberManagementPage("page", proof, page)
					}
				} else {
					err = s.writeAndRememberManagementPage(w, r, page, "page", proof)
				}
				if err != nil || len(s.managementComputed.pages) != 1 {
					b.Fatal("invalid output", err)
				}
			}
		})
	}
}

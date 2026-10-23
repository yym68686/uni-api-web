package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"github.com/andybalholm/brotli"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestManagementResponseEncodingPreservesCompleteJSON(t *testing.T) {
	value := map[string]any{"rows": strings.Repeat("latest current model evidence", 4000), "revision": "same-current-revision"}
	want, _ := json.Marshal(value)
	for header, encoding := range map[string]string{
		"": "", "br, gzip": "br", "GZip; q=0.5": "gzip", "gzip;q=0": "",
		"gzip;q=0, *;q=1": "", "xgzip": "", "gzip;q=NaN": "",
		"gzip;q=2": "", "gzip;q=0.5;q=1": "", "gzip;q=bad": "",
		"gzip;q=1, gzip;q=0": "", "br;q=0,gzip": "gzip", "br;q=0": "", "br;q=NaN": "", "br;q=1,br;q=0": "",
		"br;q=0.2,gzip;q=1": "gzip", "br;q=1,gzip;q=0.2": "br",
	} {
		r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/query", nil)
		r.Header.Set("Accept-Encoding", header)
		w := httptest.NewRecorder()
		if err := writeManagementJSON(w, r, value); err != nil {
			t.Fatal(err)
		}
		if w.Header().Get("Content-Encoding") != encoding || w.Code != 200 || w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) || w.Header().Get("Vary") != "Accept-Encoding" {
			t.Fatal("incorrect response negotiation", header, w.Header())
		}
		body := w.Body.Bytes()
		if encoding == "br" {
			var err error
			body, err = io.ReadAll(brotli.NewReader(bytes.NewReader(body)))
			if err != nil {
				t.Fatal(err)
			}
		} else if encoding == "gzip" {
			reader, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			body, err = io.ReadAll(reader)
			_ = reader.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(body, want) {
			t.Fatal("compression changed data or completeness")
		}
	}
}

func TestManagementHTTPPreparedProjectionReturnsHydratedCompressedPageAndFullSelection(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	token, err := s.control.newSession(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	call := func(values url.Values, target any) int {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/query?"+values.Encode(), nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		r.Header.Set("Accept-Encoding", "gzip")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			return w.Code
		}
		if w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ETag") != "" {
			t.Fatal("page is uncompressed or cacheable", w.Header())
		}
		reader, err := gzip.NewReader(w.Body)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		if err := json.NewDecoder(reader).Decode(target); err != nil {
			t.Fatal(err)
		}
		return w.Code
	}
	filters := `{"model":"gpt-5.5"}`
	var page managementPageResponse
	if code := call(url.Values{"filters": {filters}}, &page); code != 200 || page.Total != 61 || len(page.Rows) != 25 || page.Rows[0].Selected == nil || *page.Rows[0].Selected.Result.Availability.TTFT != 123 {
		t.Fatal("missing current page evidence", code)
	}
	var selection managementSelectionResponse
	if code := call(url.Values{"filters": {filters}, "selection": {page.SelectionToken}}, &selection); code != 200 || len(selection.Rows) != 61 || selection.Revision != page.Revision {
		t.Fatal("prepared selection contract lost", code)
	}
}

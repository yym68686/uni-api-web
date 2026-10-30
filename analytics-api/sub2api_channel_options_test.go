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
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Retain the previous serial reader as an independent behavior oracle for the
// response, validation, error priority and exclusion order, not for scheduling.
func previousSubOptionsData(ctx context.Context, src controlSource, query url.Values) (map[string]any, int, error) {
	state, status, err := subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if err != nil {
		return nil, status, err
	}
	keys, status, err := fetchSource(ctx, src, "/v1/api-keys", nil)
	if err != nil {
		return nil, status, optionsTestError("无法读取来源 API key")
	}
	channels := []any{}
	if key := query.Get("api_key_id"); key != "" {
		key, err = subRawKey(src.ID, key)
		if err != nil {
			return nil, 400, err
		}
		catalog, code, err := fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
		if err != nil {
			return nil, code, optionsTestError("无法读取当前渠道顺序")
		}
		if data, ok := catalog["data"].([]any); ok {
			replaced := configuredExcluded(state, key)
			for _, row := range data {
				item, _ := row.(map[string]any)
				provider, _ := item["provider"].(string)
				if !replaced[provider] {
					channels = append(channels, row)
				}
			}
		}
	}
	return map[string]any{"batch_revisions": true, "revision": state["revision"], "supported": state["temporary_channel_import"] == true, "manageable": state["temporary_channel_management"] == true, "keys": keys["data"], "channels": channels, "provider": subProviderName(query.Get("account_id"), int64Param(query.Get("group_id")), strings.TrimPrefix(query.Get("api_key_id"), src.ID+"::"))}, 200, nil
}

type optionsTestError string

func (e optionsTestError) Error() string { return string(e) }

func TestSubOptionsPreservesSerialResultsAndErrors(t *testing.T) {
	key := "key-" + strings.Repeat("a", 64)
	controls := mustJSON(map[string]any{
		"revision": "boot:23", "temporary_channel_import": true, "temporary_channel_management": true,
		"rules": []retainedRule{{KeyID: key, Disabled: []string{"hidden"}}},
	})
	keys := `{"data":[{"key_id":"masked-id","prefix":"masked","position":2}]}`
	catalog := `{"data":[{"provider":"kept","model":"模型🙂"},{"provider":"hidden","model":"excluded"},null,{"provider":"kept","model":"duplicate"}]}`
	for _, tc := range []struct {
		name, key, controls, keys, catalog string
		controlCode, keyCode, catalogCode  int
	}{
		{name: "selected", key: key},
		{name: "source qualified", key: "source::" + key},
		{name: "no key"},
		{name: "invalid key", key: "invalid"},
		{name: "wrong source", key: "foreign::" + key},
		{name: "empty data", key: key, keys: `{}`, catalog: `{}`},
		{name: "null data", key: key, keys: `{"data":null}`, catalog: `{"data":null}`},
		{name: "non array catalog", key: key, catalog: `{"data":{}}`},
		{name: "strict capability booleans", key: key, controls: `{"revision":"r","temporary_channel_import":"true","temporary_channel_management":1}`},
		{name: "controls forbidden", key: key, controlCode: 403},
		{name: "controls not supported", key: key, controlCode: 404},
		{name: "controls json", key: key, controls: `{`},
		{name: "keys unavailable", key: key, keyCode: 503},
		{name: "keys json", key: key, keys: `{`},
		{name: "catalog unavailable", key: key, catalogCode: 503},
		{name: "catalog json", key: key, catalog: `{`},
		{name: "controls error first", key: "invalid", controlCode: 403, keyCode: 503, catalogCode: 503},
		{name: "keys error before key validation", key: "invalid", keyCode: 503},
		{name: "keys error before catalog", key: key, keyCode: 503, catalogCode: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var catalogCalls atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer fixture-admin" {
					t.Error("unexpected write or auth")
				}
				body, code := "", 0
				switch r.URL.Path {
				case "/v1/channel-controls":
					body, code = tc.controls, tc.controlCode
					if body == "" {
						body = controls
					}
				case "/v1/api-keys":
					body, code = tc.keys, tc.keyCode
					if body == "" {
						body = keys
					}
				case "/v1/model-channels":
					catalogCalls.Add(1)
					if !reflect.DeepEqual(r.URL.Query(), url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}}) {
						t.Error("catalog scope changed")
					}
					body, code = tc.catalog, tc.catalogCode
					if body == "" {
						body = catalog
					}
				default:
					t.Error("unexpected read", r.URL.Path)
				}
				if code == 0 {
					code = 200
				}
				w.WriteHeader(code)
				_, _ = w.Write([]byte(body))
			}))
			defer gateway.Close()
			src := controlSource{sourceView: sourceView{ID: "source", Base: gateway.URL}, Key: "fixture-admin"}
			q := url.Values{"api_key_id": {tc.key}, "account_id": {"fixture"}, "group_id": {"7"}}
			want, wantCode, wantErr := previousSubOptionsData(context.Background(), src, q)
			expectedCatalogCalls := catalogCalls.Swap(0)
			got, code, err := subChannelOptionsData(context.Background(), src, q)
			message := func(e error) string {
				if e == nil {
					return ""
				}
				return e.Error()
			}
			if code != wantCode || message(err) != message(wantErr) || !reflect.DeepEqual(got, want) {
				t.Fatal("serial contract changed", code, wantCode, err, wantErr, got, want)
			}
			if catalogCalls.Load() != expectedCatalogCalls {
				t.Fatal("catalog called before prerequisites passed")
			}
			if tc.name == "selected" {
				data := got["channels"].([]any)
				if len(data) != 3 || data[0].(map[string]any)["model"] != "模型🙂" || data[1] != nil || data[2].(map[string]any)["model"] != "duplicate" {
					t.Fatal("exclusions, nulls or catalog order changed", data)
				}
			}
		})
	}
}

func TestSubOptionsOverlapsKeysButKeepsCatalogAfterControls(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	defer close(release)
	var catalogCalls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected mutation")
		}
		if r.URL.Path == "/v1/model-channels" {
			catalogCalls.Add(1)
			writeJSON(w, 200, map[string]any{"data": []any{}})
			return
		}
		started <- r.URL.Path
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		writeJSON(w, 200, map[string]any{"revision": "current", "data": []any{}})
	}))
	defer gateway.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, _, err := subChannelOptionsData(ctx, controlSource{sourceView: sourceView{ID: "source", Base: gateway.URL}}, url.Values{"api_key_id": {"key-" + strings.Repeat("a", 64)}})
		finished <- err
	}()
	paths := map[string]bool{}
	for len(paths) < 2 {
		select {
		case path := <-started:
			paths[path] = true
		case <-ctx.Done():
			t.Fatal("independent reads did not overlap")
		}
	}
	if !paths["/v1/channel-controls"] || !paths["/v1/api-keys"] || catalogCalls.Load() != 0 {
		t.Fatal("catalog escaped revision prerequisite", paths, catalogCalls.Load())
	}
	// Release both prerequisite handlers without a scheduler-dependent deadline.
	release <- struct{}{}
	release <- struct{}{}
	if err := <-finished; err != nil || catalogCalls.Load() != 1 {
		t.Fatal("successful options did not finish", err, catalogCalls.Load())
	}
}

func TestSubOptionsEarlierFailureCancelsUnneededKeyRead(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/api-keys":
			close(started)
			<-r.Context().Done()
			close(canceled)
		case "/v1/channel-controls":
			<-started
			http.Error(w, "forbidden", 403)
		default:
			t.Error("later prerequisite was read", r.URL.Path)
		}
	}))
	defer gateway.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, status, err := subChannelOptionsData(ctx, controlSource{sourceView: sourceView{ID: "source", Base: gateway.URL}}, url.Values{"api_key_id": {"key-" + strings.Repeat("a", 64)}})
	if status != 403 || err == nil || err.Error() != "来源密钥无管理权限" {
		t.Fatal(status, err)
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("failed prerequisite retained unrelated HTTP read")
	}
}

func TestSubOptionsRespectsCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer gateway.Close()
	data, status, err := subChannelOptionsData(ctx, controlSource{sourceView: sourceView{Base: gateway.URL}}, url.Values{})
	if err == nil || status != 502 || data != nil || calls.Load() != 0 {
		t.Fatal("canceled read escaped", data, status, err, calls.Load())
	}
}

func TestSubOptionsHTTPAuthorizationAndTrace(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	s, account, src := bindingFixture(t, "https://site.test")
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" {
			t.Error("unexpected mutation")
		}
		writeJSON(w, 200, map[string]any{"revision": "live", "data": []any{}})
	}))
	defer gateway.Close()
	src.Base = gateway.URL
	if _, err := s.control.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	session, err := s.control.newSession(context.Background(), account.Owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range []string{"", session} {
		r := httptest.NewRequest("GET", "/v1/sub2api/channel-options?source_id="+src.ID, nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if cookie == "" {
			if w.Code != 401 || calls.Load() != 0 {
				t.Fatal("unauthenticated reads reached source", w.Code, calls.Load())
			}
		} else {
			var data map[string]any
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &data) != nil || data["revision"] != "live" || calls.Load() != 2 {
				t.Fatal(w.Code, w.Body.String(), calls.Load())
			}
		}
	}
	for _, expected := range []string{"stage=options_controls", "stage=options_keys", "stage=options_gateway", "read_kind=import_options"} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatal("missing options correlation", expected, logs.String())
		}
	}
	for _, private := range []string{src.ID, account.ID, src.Key, gateway.URL} {
		if strings.Contains(logs.String(), private) {
			t.Fatal("private options fields reached trace")
		}
	}
}

func BenchmarkSubOptionsPrerequisitesLatency(b *testing.B) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		writeJSON(w, 200, map[string]any{"revision": "r", "data": []any{}})
	}))
	defer gateway.Close()
	src := controlSource{sourceView: sourceView{ID: "source", Base: gateway.URL}}
	q := url.Values{"api_key_id": {"key-" + strings.Repeat("a", 64)}}
	for _, tc := range []struct {
		name string
		read func(context.Context, controlSource, url.Values) (map[string]any, int, error)
	}{{"serial", previousSubOptionsData}, {"parallel_prerequisites", subChannelOptionsData}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, status, err := tc.read(context.Background(), src, q); status != 200 || err != nil {
					b.Fatal(status, err)
				}
			}
		})
	}
}

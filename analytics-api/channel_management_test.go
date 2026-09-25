package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestChannelManagementUsesSavedCatalogWithoutGatewayRead(t *testing.T) {
	var gatewayReads atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gatewayReads.Add(1)
		http.Error(w, "slow gateway", 503)
	}))
	defer gateway.Close()
	s, account, src := bindingFixture(t, "https://account.test")
	src.Base = gateway.URL
	if _, err := s.control.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	snapshot := managementSnapshot{
		Rows:      []managementModel{{Provider: "native", Model: "gpt-6-sol", Engine: "gpt"}},
		Providers: map[string]managementProvider{},
		Copies:    map[string]bool{},
		CheckedAt: time.Now().Unix(),
		Identity:  controlTarget(src),
	}
	if err := s.saveManagementSnapshot(context.Background(), src.ID, snapshot); err != nil {
		t.Fatal(err)
	}
	token, _ := s.control.newSession(context.Background(), account.Owner)
	request := func() struct {
		Data        []managementChannel `json:"data"`
		Unavailable []string            `json:"unavailable_sources"`
	} {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/channel-management", nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var result struct {
			Data        []managementChannel `json:"data"`
			Unavailable []string            `json:"unavailable_sources"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	start := time.Now()
	result := request()
	if time.Since(start) > time.Second || len(result.Data) != 1 || result.Data[0].Models[0] != "gpt-6-sol" || gatewayReads.Load() != 0 {
		t.Fatal("saved catalog was not served directly", result, gatewayReads.Load())
	}
	snapshot.CheckedAt = time.Now().Add(-6 * time.Minute).Unix()
	if err := s.saveManagementSnapshot(context.Background(), src.ID, snapshot); err != nil {
		t.Fatal(err)
	}
	result = request()
	if len(result.Data) != 1 || len(result.Unavailable) != 1 || gatewayReads.Load() != 0 {
		t.Fatal("stale catalog should stay visible with a warning", result, gatewayReads.Load())
	}
	src.Key = "rotated-gateway-key"
	if _, err := s.control.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	result = request()
	if len(result.Data) != 0 || len(result.Unavailable) != 1 || gatewayReads.Load() == 0 {
		t.Fatal("changed source reused an old catalog", result, gatewayReads.Load())
	}
}

func TestChannelManagementUsesConfiguredInventoryAndEffectiveKeyRoutes(t *testing.T) {
	var failKey atomic.Bool
	var changedKey atomic.Bool
	var revisionChanges atomic.Bool
	var controlReads atomic.Int32
	providers := []configuredProvider{
		{Provider: "bound", Base: "https://account.test/v1/responses", API: "private-upstream-key"},
		{Provider: "site-only", Base: "https://account.test/v1/responses", API: "different-key"},
		{Provider: "unassigned", Base: "https://other.test/v1/messages", API: "other-key"},
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("management changed gateway", r.Method)
			http.Error(w, "read only", 405)
			return
		}
		switch r.URL.Path {
		case "/v1/channel-controls":
			n := controlReads.Add(1)
			revision := "r1"
			if revisionChanges.Load() && n%2 == 0 {
				revision = "r2"
			}
			writeJSON(w, 200, map[string]any{"rules": []any{}, "temporary_channels": []any{}, "revision": revision, "temporary_channel_management": true})
		case "/v1/channel-settings/providers":
			live := append([]configuredProvider{}, providers...)
			if changedKey.Load() {
				live[0].API = "replacement-key"
			}
			writeJSON(w, 200, map[string]any{"providers": live})
		case "/v1/api-keys":
			writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"key_id": "caller-one", "prefix": "masked-one", "position": 1}, map[string]any{"key_id": "caller-two", "prefix": "masked-two", "position": 2}}})
		case "/v1/model-channels":
			if r.URL.Query().Get("endpoint") != "all" || r.URL.Query().Get("stream") != "all" {
				t.Error("filtered configured catalog")
			}
			key := r.URL.Query().Get("api_key_id")
			if key == "caller-two" && failKey.Load() {
				http.Error(w, "unavailable", 503)
				return
			}
			row := func(p, m string) map[string]any { return map[string]any{"provider": p, "model": m, "engine": "gpt"} }
			rows := []any{row("bound", "gpt-6-astra"), row("site-only", "gpt-6-astra"), row("unassigned", "custom-model"), row("bound", "gpt-5.6-sol")}
			if key == "caller-one" {
				rows = []any{row("site-only", "gpt-6-astra"), row("bound", "gpt-6-astra"), row("bound", "gpt-6-astra"), row("bound", "gpt-5.6-sol")}
			}
			if key == "caller-two" {
				rows = []any{row("bound", "gpt-6-astra"), row("unassigned", "custom-model")}
			}
			writeJSON(w, 200, map[string]any{"data": rows})
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	s, a, src := bindingFixture(t, "https://account.test")
	src.Base = gateway.URL
	if _, err := s.control.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	if err := s.saveConfiguredInventory(context.Background(), src, providers); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_key_index(account_id,key_hash,remote_key_id,group_id) VALUES($1,$2,42,7)`, a.ID, tokenHash("private-upstream-key")); err != nil {
		t.Fatal(err)
	}
	token, _ := s.control.newSession(context.Background(), a.Owner)
	request := func(path, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request("/v1/channel-management", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := request("/v1/channel-management", token)
	var listing struct {
		Data []managementChannel `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listing) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(listing.Data) != 3 {
		t.Fatal(w.Body.String())
	}
	for _, item := range listing.Data {
		switch item.Provider {
		case "bound":
			if item.AccountID != a.ID || item.GroupID != 7 || len(item.Models) != 2 || len(item.AccountIDs) != 1 {
				t.Fatal(item)
			}
		case "site-only":
			if item.AccountID != "" || item.GroupID != 0 || len(item.AccountIDs) != 1 {
				t.Fatal("site match invented group", item)
			}
		case "unassigned":
			if len(item.AccountIDs) != 0 || item.Models[0] != "custom-model" {
				t.Fatal(item)
			}
		}
	}
	if strings.Contains(w.Body.String(), "private-upstream-key") || strings.Contains(w.Body.String(), tokenHash("private-upstream-key")) {
		t.Fatal("secret leaked")
	}
	changedKey.Store(true)
	w = request("/v1/channel-management", token)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listing) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, item := range listing.Data {
		if item.Provider == "bound" && (item.AccountID != "" || item.GroupID != 0) {
			t.Fatal("stale key inherited another group's results", item)
		}
	}
	path := "/v1/sources/" + src.ID + "/channel-routes"
	w = request(path, token)
	var routes struct {
		Data           []channelRoute `json:"data"`
		Unavailable    []string       `json:"unavailable_keys"`
		Revision       string         `json:"revision"`
		Consistent     bool           `json:"snapshot_consistent"`
		Manageable     bool           `json:"manageable"`
		BatchRevisions bool           `json:"batch_revisions"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &routes) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(routes.Data) != 5 {
		t.Fatal("duplicate or missing route", w.Body.String())
	}
	if !routes.Consistent || routes.Revision != "r1" || !routes.Manageable || !routes.BatchRevisions {
		t.Fatal("missing consistent preview metadata", w.Body.String())
	}
	for _, route := range routes.Data {
		if route.Provider == "bound" {
			want := 1
			if route.KeyID == "caller-one" && route.Model == "gpt-6-astra" {
				want = 2
			}
			if route.Position != want {
				t.Fatal("wrong per-model effective position", route)
			}
		}
	}
	failKey.Store(true)
	w = request(path, token)
	json.Unmarshal(w.Body.Bytes(), &routes)
	if len(routes.Unavailable) != 1 || len(routes.Data) != 3 {
		t.Fatal("partial fetch reported complete", w.Body.String())
	}
	failKey.Store(false)
	controlReads.Store(0)
	revisionChanges.Store(true)
	w = request(path, token)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &routes) != nil || routes.Consistent {
		t.Fatal("changing revision advertised a safe snapshot", w.Body.String())
	}
}

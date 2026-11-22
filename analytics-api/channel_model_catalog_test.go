package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestChannelCatalogRefreshAndManualEntryNeverWriteGatewayRoutes(t *testing.T) {
	store, err := newControlStore(isolatedControlTestDSN(t), strings.Repeat("c", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const provider = "openrouter"
	revision := "r1"
	base := "https://catalog.example/api/v1/systemone"
	current := []string{"gemini-4-carbon", "jev-latest"}
	discovered := []string{"google/gemini-4-carbon", "~typesafe/jev-latest", "google/gemini-4-carbon"}
	reads := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer settings-admin-fixture" {
			t.Error("wrong gateway auth scope")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/channel-settings":
			if r.URL.Query().Get("provider") != provider {
				t.Error("wrong provider", r.URL.Query())
			}
			writeJSON(w, 200, map[string]any{"provider": provider, "revision": revision, "api_key_id": "owner-fixture",
				"effective": map[string]any{"base_url": base, "model": current}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/channel-settings/discover":
			reads++
			var in channelSettingMutation
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Revision != revision || len(in.Changes) != 1 ||
				in.Changes[0].Provider != provider || len(in.Changes[0].Set) != 0 || in.Changes[0].Reset {
				t.Errorf("catalog discovery tried to change settings: %#v %v", in, err)
			}
			writeJSON(w, 200, map[string]any{"models": discovered})
		default:
			t.Errorf("unexpected gateway call: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	src := controlSource{sourceView: sourceView{ID: "fixture", Base: upstream.URL, Name: "Test"}, Key: "catalog-fixture", ConfigKey: "settings-admin-fixture"}
	if _, err = store.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	handler := (&Service{control: store}).controlHandler()
	call := func(method, body string, status int) channelModelCatalogView {
		t.Helper()
		req := httptest.NewRequest(method, "/v1/sources/fixture/channel-settings/model-catalog?provider=openrouter", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("HTTP %d expected %d: %s", rec.Code, status, rec.Body.String())
		}
		var result channelModelCatalogView
		if status == 200 && json.Unmarshal(rec.Body.Bytes(), &result) != nil {
			t.Fatalf("invalid catalog: %s", rec.Body.String())
		}
		return result
	}
	if initial := call("GET", "", 200); len(initial.Models) != 0 || initial.Models == nil {
		t.Fatalf("invalid initial catalog: %#v", initial)
	}
	after := call("POST", `{"action":"refresh","revision":"r1"}`, 200)
	if len(after.Models) != 2 || !after.Models[0].Observed || after.RefreshedAt == nil {
		t.Fatalf("invalid refresh: %#v", after)
	}
	if saved := call("GET", "", 200); !reflect.DeepEqual(after, saved) {
		t.Fatalf("refresh did not survive reopening: %#v", saved)
	}
	call("POST", `{"action":"add","revision":"r1","model":"manual/model"}`, 200)
	current = []string{"jev-latest"}
	revision = "r2" // Edits to attachment are independent of the catalog key.
	call("POST", `{"action":"refresh","revision":"r1"}`, 409)
	discovered = []string{"google/gemini-4-carbon"}
	call("POST", `{"action":"refresh","revision":"r2"}`, 200)
	saved := call("GET", "", 200)
	if len(saved.Models) != 3 || !saved.Models[0].Observed || !saved.Models[1].Manual || saved.Models[2].Observed || reads != 2 {
		t.Fatalf("expected retained missing/manual models, got %#v", saved)
	}
	base = "https://different.example/v1"
	if saved := call("GET", "", 200); len(saved.Models) != 0 {
		t.Fatalf("changed upstream saw previous catalog: %#v", saved)
	}
}

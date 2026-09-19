package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAutomationTaskCRUDAndAuditAuthorization(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" { t.Skip("PostgreSQL required") }
	store, err := newControlStore(dsn, strings.Repeat("a", 32))
	if err != nil { t.Fatal(err) }
	defer store.Close()
	id := "automation-" + randomID()[:10]
	if _, err = store.saveSource(context.Background(), controlSource{sourceView: sourceView{ID: id, Name: id, Base: "https://example.test"}, Key: "fixture"}, false); err != nil { t.Fatal(err) }
	defer store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, id)
	service, _ := NewService(stateTestEngine(t), Config{Upstream: "https://example.test"}); service.control = store
	token, err := store.newSession(context.Background(), "automation-owner"); if err != nil { t.Fatal(err) }
	body := `{"name":"调序任务","enabled":false,"source_id":"`+id+`","key_id":"key-1","model":"gpt-6-astra","interval_seconds":300,"range":"1h","policy":{"metrics":["latency"],"min_success_samples":100,"min_cache_samples":50,"min_latency_samples":50,"min_quality_samples":20,"confidence_level":0.95,"latency_quantile":0.5,"latency_min_percent":10,"latency_min_ms":100,"success_min_pp":2,"cache_min_pp":5,"quality_min_pp":5,"require_consecutive":3,"cooldown_seconds":1800,"max_moves":1,"action":"suggest","require_all_higher":true}}`
	request := func(method, path, raw string, auth bool) *httptest.ResponseRecorder { r:=httptest.NewRequest(method,path,strings.NewReader(raw)); r.Header.Set("Content-Type","application/json"); if auth { r.AddCookie(&http.Cookie{Name:"uni_console_session",Value:token}) }; w:=httptest.NewRecorder(); service.Handler().ServeHTTP(w,r); return w }
	if w:=request("GET","/v1/automations","",false); w.Code!=401 { t.Fatal(w.Code) }
	w:=request("POST","/v1/automations",body,true); if w.Code!=200 { t.Fatal(w.Code,w.Body.String()) }
	var saved struct{Task AutomationTask}; if json.Unmarshal(w.Body.Bytes(),&saved)!=nil || saved.Task.ID=="" { t.Fatal(w.Body.String()) }
	w=request("GET","/v1/automations","",true); if w.Code!=200 || !strings.Contains(w.Body.String(),"调序任务") { t.Fatal(w.Code,w.Body.String()) }
	w=request("GET","/v1/automations/"+saved.Task.ID,"",true); if w.Code!=200 || !strings.Contains(w.Body.String(),`"data":[]`) { t.Fatal(w.Code,w.Body.String()) }
	w=request("DELETE","/v1/automations/"+saved.Task.ID,"",true); if w.Code!=200 { t.Fatal(w.Code,w.Body.String()) }
}

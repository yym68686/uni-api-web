package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type automationFixture struct {
	service *Service
	store   *controlStore
	task    AutomationTask
	source  controlSource
	mu      sync.Mutex
	live    retainedLive
	posts   int
	cookie  string
}

func newAutomationFixture(t *testing.T) *automationFixture {
	t.Helper()
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, e := newControlStore(dsn, strings.Repeat("a", 32))
	if e != nil {
		t.Fatal(e)
	}
	id := "auto-" + randomID()[:10]
	f := &automationFixture{store: store}
	f.task = AutomationTask{ID: id, Name: "调序任务", Kind: "order", SourceID: id, KeyID: "key-" + tokenHash("fixture-business-key"), Model: "m", Enabled: true, Range: "1h", IntervalSeconds: 10, Policy: defaultAutomationPolicy(), Owner: id, Revision: 1}
	f.live = retainedLive{Instance: "boot", Revision: "boot:1", Rules: []retainedRule{{KeyID: f.task.KeyID, Model: "m", Order: []string{"a", "b", "disabled"}, Disabled: []string{"disabled"}}}}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/v1/model-channels":
			if r.URL.Query().Get("api_key_id") != f.task.KeyID {
				http.Error(w, "wrong key", 404)
				return
			}
			cs := []automationChannel{}
			for _, p := range f.live.Rules[0].Order {
				cs = append(cs, automationChannel{Provider: p, Model: "m", Upstream: "m", Eligible: p != "disabled"})
			}
			writeJSON(w, 200, map[string]any{"api_key_id": f.task.KeyID, "data": cs})
		case "/v1/channel-controls":
			if r.Method == "POST" {
				var in struct {
					Revision string `json:"revision"`
					retainedRule
				}
				if json.NewDecoder(r.Body).Decode(&in) != nil || in.Revision != f.live.Revision {
					http.Error(w, "conflict", 409)
					return
				}
				f.posts++
				f.live.Revision = fmt.Sprintf("boot:%d", f.posts+1)
				f.live.Rules = []retainedRule{in.retainedRule}
			}
			writeJSON(w, 200, f.live)
		case "/v1/api_config":
			writeJSON(w, 200, map[string]any{"api_config": map[string]any{"api_keys": []any{map[string]any{"api": "fixture-business-key", "preferences": map[string]any{"SCHEDULING_ALGORITHM": "fixed_priority"}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	f.source = controlSource{sourceView: sourceView{ID: id, Name: id, Base: up.URL}, Key: "fixture-admin"}
	if _, e = store.saveSource(context.Background(), f.source, false); e != nil {
		t.Fatal(e)
	}
	f.service, _ = NewService(stateTestEngine(t), Config{Upstream: up.URL})
	f.service.control = store
	f.cookie, e = store.newSession(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		up.Close()
		store.db.Exec(`DELETE FROM console_automation_audit WHERE task_id IN (SELECT id FROM console_automation_tasks WHERE source_id=$1)`, id)
		store.db.Exec(`DELETE FROM console_automation_tasks WHERE source_id=$1`, id)
		store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, id)
		store.Close()
	})
	return f
}
func (f *automationFixture) request(method, path string, v any, cookie string) *httptest.ResponseRecorder {
	var body string
	if v != nil {
		body = mustJSON(v)
	}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
	}
	w := httptest.NewRecorder()
	f.service.Handler().ServeHTTP(w, r)
	return w
}
func (f *automationFixture) save(t *testing.T) AutomationTask {
	t.Helper()
	w := f.request("POST", "/v1/automations", f.task, f.cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var r struct{ Task AutomationTask }
	json.Unmarshal(w.Body.Bytes(), &r)
	f.task.ID = r.Task.ID
	return r.Task
}
func TestAutomationTaskCRUDVersionsAndArchivedAudit(t *testing.T) {
	f := newAutomationFixture(t)
	w := f.request("GET", "/v1/automations", nil, "")
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	task := f.save(t)
	task.Name = "改名"
	w = f.request("PUT", "/v1/automations/"+task.ID, task, f.cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = f.request("PUT", "/v1/automations/"+task.ID, task, f.cookie); w.Code != 409 {
		t.Fatal("stale write", w.Code)
	}
	other, _ := f.store.newSession(context.Background(), "other-"+task.ID)
	w = f.request("GET", "/v1/automations/"+task.ID, nil, other)
	if w.Code != 200 || strings.Contains(w.Body.String(), "改名") {
		t.Fatal("foreign audit leaked")
	}
	if w = f.request("DELETE", "/v1/automations/"+task.ID, nil, f.cookie); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = f.request("GET", "/v1/automations", nil, f.cookie)
	if strings.Contains(w.Body.String(), task.ID) {
		t.Fatal("archived task remains active")
	}
	w = f.request("GET", "/v1/automations/audit", nil, f.cookie)
	if !strings.Contains(w.Body.String(), "archived") || !strings.Contains(w.Body.String(), "configured") {
		t.Fatal("audit history lost", w.Body.String())
	}
}
func TestAutomationLeasesSerializeAndPauseInvalidates(t *testing.T) {
	f := newAutomationFixture(t)
	task := f.save(t)
	var wg sync.WaitGroup
	results := make(chan []AutomationTask, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tasks, e := f.store.claimAutomations(context.Background())
			if e != nil {
				t.Error(e)
			}
			results <- tasks
		}()
	}
	wg.Wait()
	close(results)
	var claimed []AutomationTask
	for ts := range results {
		claimed = append(claimed, ts...)
	}
	if len(claimed) != 1 {
		t.Fatalf("duplicate claim: %d", len(claimed))
	}
	t1 := claimed[0]
	f.store.db.Exec(`UPDATE console_automation_tasks SET next_run=now()-interval '1 hour' WHERE id=$1`, task.ID)
	if ts, e := f.store.claimAutomations(context.Background()); e != nil || len(ts) != 0 {
		t.Fatal("live lease reclaimed", ts, e)
	}
	task.Enabled = false
	if w := f.request("PUT", "/v1/automations/"+task.ID, task, f.cookie); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	ctx, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()
	catalog, _ := f.service.automationCatalog(ctx, f.source, t1)
	err := f.service.applyAutomation(ctx, t1, f.source, f.live, catalog, []string{"b", "a", "disabled"}, nil, map[string]any{})
	if err == nil || f.posts != 0 {
		t.Fatal("paused task wrote rules", err, f.posts)
	}
}
func TestAutomationApplyPreservesDisabledPersistsAndRollback(t *testing.T) {
	f := newAutomationFixture(t)
	f.save(t)
	tasks, e := f.store.claimAutomations(context.Background())
	if e != nil || len(tasks) != 1 {
		t.Fatal(e, tasks)
	}
	task := tasks[0]
	ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
	defer c()
	catalog, _ := f.service.automationCatalog(ctx, f.source, task)
	before := f.live
	detail := map[string]any{"previous_rule": exactRule(before, task)}
	if e = f.service.applyAutomation(ctx, task, f.source, before, catalog, []string{"b", "a", "disabled"}, nil, detail); e != nil {
		t.Fatal(e)
	}
	if f.posts != 1 || fmt.Sprint(f.live.Rules[0].Disabled) != "[disabled]" {
		t.Fatal(f.posts, f.live)
	}
	record, e := f.store.retainedRecord(ctx, f.source.ID)
	if e != nil || record.Revision != f.live.Revision {
		t.Fatal("not retained", record, e)
	}
	var auditID int64
	if e = f.store.db.QueryRow(`SELECT id FROM console_automation_audit WHERE task_id=$1 AND status='applied'`, task.ID).Scan(&auditID); e != nil {
		t.Fatal(e)
	}
	w := f.request("POST", fmt.Sprintf("/v1/automations/audit/%d/rollback", auditID), nil, f.cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if fmt.Sprint(f.live.Rules[0].Order) != "[a b disabled]" || fmt.Sprint(f.live.Rules[0].Disabled) != "[disabled]" {
		t.Fatal("bad rollback", f.live)
	}
	saved, e := f.store.automationTask(ctx, f.task.Owner, task.ID)
	if e != nil || saved.Enabled {
		t.Fatal("rollback did not pause", e)
	}
	w = f.request("POST", fmt.Sprintf("/v1/automations/audit/%d/rollback", auditID), nil, f.cookie)
	if w.Code != 409 || f.posts != 2 {
		t.Fatal("stale rollback overwrote live state", w.Code)
	}
}
func TestAutomationApplyRejectsControlConflictsBeforeWriting(t *testing.T) {
	f := newAutomationFixture(t)
	f.save(t)
	tasks, _ := f.store.claimAutomations(context.Background())
	task := tasks[0]
	ctx := context.Background()
	catalog, _ := f.service.automationCatalog(ctx, f.source, task)
	before := f.live
	f.live.Revision = "changed"
	if e := f.service.applyAutomation(ctx, task, f.source, before, catalog, []string{"b", "a", "disabled"}, nil, map[string]any{}); e == nil || f.posts != 0 {
		t.Fatal("conflict overwritten", e)
	}
}
func TestAutomationStreakRequiresSamePlanRevisionAndNewFacts(t *testing.T) {
	f := newAutomationFixture(t)
	task := f.save(t)
	ctx := context.Background()
	if _, e := f.service.insertAutomationAudit(ctx, task, "waiting", "observe", nil, nil, nil, map[string]any{"proposal": "p", "watermark": 100, "task_revision": task.Revision}, "waiting"); e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct {
		proposal  string
		watermark int64
		count     int
		fresh     bool
	}{{"p", 100, 1, false}, {"p", 101, 1, true}, {"changed", 101, 0, true}} {
		n, fresh, e := f.service.automationStreak(ctx, task, c.proposal, c.watermark)
		if e != nil || n != c.count || fresh != c.fresh {
			t.Fatal(n, fresh, e)
		}
	}
}

func TestAutomationScheduledRunUsesActualOrderAndPersistsDecision(t *testing.T) {
	f := newAutomationFixture(t)
	f.task.Policy.Metrics = []string{"latency"}
	f.task.Policy.RequireConsecutive = 1
	f.task.Policy.Action = "apply"
	f.save(t)
	now := time.Now()
	facts := []Fact{}
	for _, p := range []string{"a", "b"} {
		for i := 0; i < 100; i++ {
			latency := 1500.0
			if p == "b" {
				latency = 1000
			}
			facts = append(facts, Fact{Schema: 1, SourceID: f.source.ID, EventID: fmt.Sprintf("%s-%d", p, i), Kind: "attempt", AtMS: now.Add(-time.Duration(i) * time.Second).UnixMilli(), KeyID: f.task.KeyID, Provider: p, Model: "m", UpstreamModel: "m", Endpoint: "/v1/responses", Stream: true, Outcome: "success", ResponseCreatedMS: &latency})
		}
	}
	if e := f.service.engine.Import(context.Background(), "automation-run", "v1", facts); e != nil {
		t.Fatal(e)
	}
	f.service.lastCollect.Store(now.UnixMilli())
	tasks, e := f.store.claimAutomations(context.Background())
	if e != nil || len(tasks) != 1 {
		t.Fatal(e, tasks)
	}
	f.service.runAutomation(context.Background(), tasks[0])
	if f.posts != 1 || fmt.Sprint(f.live.Rules[0].Order) != "[b a disabled]" {
		rows, _ := f.store.db.Query(`SELECT status,reason FROM console_automation_audit WHERE task_id=$1`, f.task.ID)
		defer rows.Close()
		for rows.Next() {
			var st, r string
			rows.Scan(&st, &r)
			t.Log(st, r)
		}
		t.Fatal("run failed", f.posts, f.live)
	}
	saved, e := f.store.automationTask(context.Background(), f.task.Owner, f.task.ID)
	if e != nil || saved.LeaseToken != "" {
		t.Fatal("lease not released", e)
	}
}
func TestAutomationQualityChecksPersistAndRespectDisabledAndBusyChannels(t *testing.T) {
	f := newAutomationFixture(t)
	var hits int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			writeJSON(w, 200, map[string]any{"capabilities": map[string]bool{"targeted_responses": true}})
			return
		}
		hits++
		if r.Header.Get("X-Uni-API-Provider") != "a" {
			t.Error("wrong provider")
		}
		writeJSON(w, 200, map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "21"}}}}})
	}))
	defer up.Close()
	src := f.source
	src.Base = up.URL
	f.store.db.Exec(`INSERT INTO console_channel_check_runs(source_id,provider,run_id,expires_at) VALUES($1,'busy','manual',now()+interval '1 minute')`, src.ID)
	channels := []automationChannel{{Provider: "a", Eligible: true}, {Provider: "disabled", Eligible: false}, {Provider: "busy", Eligible: true}}
	results := f.service.runAutomationQualityChecks(context.Background(), src, channels, 2)
	if hits != 1 || results["a"] != "pass" || len(results) != 3 {
		t.Fatal(hits, results)
	}
	var n int
	if e := f.store.db.QueryRow(`SELECT count(*) FROM console_quality_history WHERE source_id=$1 AND provider='a' AND successful AND verdict='pass'`, src.ID).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	f.store.db.Exec(`DELETE FROM console_channel_checks WHERE source_id=$1`, src.ID)
	f.store.db.Exec(`DELETE FROM console_channel_check_runs WHERE source_id=$1`, src.ID)
}

func TestAutomationReviewUncertainIsReadOnlyAndPauses(t *testing.T) {
	f := newAutomationFixture(t)
	task := f.save(t)
	previous := exactRule(f.live, task)
	id, e := f.service.insertAutomationAudit(context.Background(), task, "uncertain", "apply", previous.Order, []string{"b", "a", "disabled"}, nil, map[string]any{"previous_rule": previous}, "uncertain")
	if e != nil {
		t.Fatal(e)
	}
	w := f.request("POST", fmt.Sprintf("/v1/automations/audit/%d/review", id), nil, f.cookie)
	if w.Code != 200 || f.posts != 0 {
		t.Fatal(w.Code, w.Body.String(), f.posts)
	}
	saved, e := f.store.automationTask(context.Background(), f.task.Owner, task.ID)
	if e != nil || saved.Enabled {
		t.Fatal("review did not pause", e)
	}
	var status string
	f.store.db.QueryRow(`SELECT status FROM console_automation_audit WHERE id=$1`, id).Scan(&status)
	if status != "reviewed" {
		t.Fatal(status)
	}
}
func TestAutomationApprovalRechecksLatestEvidence(t *testing.T) {
	f := newAutomationFixture(t)
	task := f.save(t)
	f.service.lastCollect.Store(time.Now().UnixMilli())
	previous := exactRule(f.live, task)
	id, e := f.service.insertAutomationAudit(context.Background(), task, "suggested", "observe", previous.Order, []string{"b", "a", "disabled"}, nil, map[string]any{"task_revision": task.Revision, "control_revision": f.live.Revision, "previous_rule": previous}, "suggested")
	if e != nil {
		t.Fatal(e)
	}
	w := f.request("POST", fmt.Sprintf("/v1/automations/audit/%d/apply", id), nil, f.cookie)
	if w.Code != 409 || f.posts != 0 {
		t.Fatal("approved absent evidence", w.Code, w.Body.String(), f.posts)
	}
}

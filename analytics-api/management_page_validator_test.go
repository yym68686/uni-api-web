package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagementValidatorRevalidatesEvictedPageWithoutHydration(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	session, err := s.control.newSession(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"filters": {`{"groupStatus":"all","model":"gpt-5.5","sort":"asc"}`}}
	request := func(tag, encoding, accept string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/channel-management/query?"+values.Encode(), nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
		r.Header.Set("Accept", accept)
		r.Header.Set("Accept-Encoding", encoding)
		r.Header.Set("X-Console-Current-Validation", "1")
		r.Header.Set("If-None-Match", tag)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	evict := func() {
		s.managementComputed.mu.Lock()
		s.managementComputed.pages = nil
		s.managementComputed.order = nil
		s.managementComputed.bytes = 0
		s.managementComputed.mu.Unlock()
	}
	first := request("", "br", managementPageV3MediaType)
	if first.Code != 200 || first.Header().Get("ETag") == "" || len(s.pageValidators.entries) != 1 {
		t.Fatal("complete response did not seed validator", first.Code)
	}
	tag := first.Header().Get("ETag")
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	for _, evicted := range []bool{false, true} {
		if evicted {
			evict()
		}
		logs.Reset()
		same := request(`"another", `+tag, "br", managementPageV3MediaType)
		if same.Code != 304 || same.Body.Len() != 0 || same.Header().Get("ETag") != tag || same.Header().Get("X-Console-Read-At-Ms") == "" || same.Header().Get("X-Console-Validated-Request-ID") == "" {
			t.Fatal("verified response lost live validation", evicted, same.Code)
		}
		if !strings.Contains(logs.String(), "management_validator_response source=") || strings.Contains(logs.String(), "stage=hydrate_") || strings.Contains(logs.String(), "management_paging_current verified=true") {
			t.Fatal("validated response rehydrated its complete body", evicted, logs.String())
		}
		if same.Header().Get("Cache-Control") != first.Header().Get("Cache-Control") || strings.Join(same.Header().Values("Vary"), ",") != strings.Join(first.Header().Values("Vary"), ",") || same.Header().Get("Content-Type") != first.Header().Get("Content-Type") {
			t.Fatal("conditional response policy changed", same.Header())
		}
	}
	// The header epoch tracks private authorization rotation for older readers.
	// It is not proof that any byte in this owner's public response changed.
	if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET encrypted_auth='rotated-private-session' WHERE id='a'; UPDATE console_management_view_epoch SET revision=revision+1 WHERE relation_name='console_control_snapshots'`); err != nil {
		t.Fatal(err)
	}
	evict()
	logs.Reset()
	if got := request(tag, "br", managementPageV3MediaType); got.Code != 304 || !strings.Contains(logs.String(), "source=evicted_page") || strings.Contains(logs.String(), "stage=hydrate_") {
		t.Fatal("private rotation or unused snapshot repeated identical complete body", got.Code, logs.String())
	}
	// Header-only changes are outside the channel computation stamp but are
	// part of the exact response bytes. They must return a complete new body.
	for _, query := range []string{
		`UPDATE console_sub_accounts SET balance='{"amount":987.65,"status":"ok"}' WHERE id='a'`,
		`UPDATE console_sub_models SET message='changed off-page evidence' WHERE group_id=61`,
		`UPDATE console_sub_usage SET result='{"input_price":42}' WHERE id='a-1-gpt-5.5'`,
	} {
		if _, err := s.control.db.Exec(query); err != nil {
			t.Fatal(err)
		}
		evict()
		changed := request(tag, "br", managementPageV3MediaType)
		if changed.Code != 200 || changed.Header().Get("ETag") == tag {
			t.Fatal("changed complete response incorrectly confirmed", changed.Code, query)
		}
		tag = changed.Header().Get("ETag")
	}
	for _, variant := range [][3]string{{`W/` + tag, "br", managementPageV3MediaType}, {tag, "gzip", managementPageV3MediaType}, {tag, "br", managementPageV2MediaType}, {"*", "br", managementPageV3MediaType}} {
		evict()
		if got := request(variant[0], variant[1], variant[2]); got.Code != 200 {
			t.Fatal("different representation or unsupported condition reused tag", variant, got.Code)
		}
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result='{"input_tokens":"broken"}' WHERE id='a-1-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	evict()
	if got := request(tag, "br", managementPageV3MediaType); got.Code != 503 || got.Header().Get("ETag") != "" {
		t.Fatal("invalid current payload confirmed old complete result", got.Code)
	}
	if err := s.control.logout(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	evict()
	if got := request(tag, "br", managementPageV3MediaType); got.Code != 401 || got.Header().Get("ETag") != "" {
		t.Fatal("revoked owner session accepted validator", got.Code)
	}
}

func TestManagementValidatorGatesCursorDeadlineCancellationAndColdProcess(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	snapshot, err := s.readManagementQuerySnapshot(context.Background(), "owner", managementQueryFilters{GroupStatus: "all"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	proof := managementRetainedProof(*snapshot.Proof)
	entry := managementPageValidator{key: "page", owner: "owner", tag: `"complete"`, encoding: "br", revision: snapshot.Page.Revision, proof: proof, headers: managementValidatorHeaders(*snapshot.Proof), bytes: 1024}
	s.pageValidators.put(entry)
	request := func(ctx context.Context, owner, revision string) (bool, error, int) {
		r := httptest.NewRequest("GET", "/", nil).WithContext(context.WithValue(ctx, consoleRequestIDKey{}, "validator-request"))
		r.Header.Set("Accept", managementPageV3MediaType)
		r.Header.Set("Accept-Encoding", "br")
		r.Header.Set("X-Console-Current-Validation", "1")
		r.Header.Set("If-None-Match", entry.tag)
		w := httptest.NewRecorder()
		ok, err := s.currentManagementNotModified(r.Context(), w, r, owner, "page", revision, false)
		return ok, err, w.Code
	}
	if ok, err, status := request(context.Background(), "owner", ""); err != nil || !ok || status != 304 {
		t.Fatal("current proof did not validate", err, status)
	}
	if ok, err, _ := request(context.Background(), "owner", "different-cursor-revision"); err != nil || ok {
		t.Fatal("cursor revision was bypassed", err)
	}
	if ok, err, _ := request(context.Background(), "foreign", ""); err != nil || ok {
		t.Fatal("foreign snapshot confirmed", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ok, err, _ := request(ctx, "owner", ""); ok || err == nil {
		t.Fatal("canceled request confirmed")
	}
	entry.proof.Until = time.Now().Add(-time.Second)
	s.pageValidators.put(entry)
	if ok, err, _ := request(context.Background(), "owner", ""); ok || err != nil {
		t.Fatal("expired computation confirmed", err)
	}
	s.pageValidators = managementPageValidators{}
	if ok, err, _ := request(context.Background(), "owner", ""); ok || err != nil {
		t.Fatal("unknown tag on cold process confirmed", err)
	}
}

func TestManagementValidatorConcurrentMetadataBudget(t *testing.T) {
	var cache managementPageValidators
	var group sync.WaitGroup
	for n := 0; n < 8; n++ {
		group.Add(1)
		go func(n int) {
			defer group.Done()
			for i := 0; i < 80; i++ {
				entry := managementPageValidator{key: fmt.Sprint(n, ":", i), tag: `"tag"`, encoding: "br", bytes: 16384}
				cache.put(entry)
				_, _ = cache.get(entry.key, "br", entry.tag)
			}
		}(n)
	}
	group.Wait()
	if cache.bytes > managementValidatorBudget || len(cache.entries) > managementValidatorEntries {
		t.Fatal("metadata budget exceeded", cache.bytes, len(cache.entries))
	}
	count := len(cache.entries)
	cache.put(managementPageValidator{key: "oversized", bytes: managementValidatorBudget + 1})
	if len(cache.entries) != count {
		t.Fatal("oversized metadata admitted")
	}
}

func TestManagementValidatorCountsPrivateSourceFactsAndComparesAuthorization(t *testing.T) {
	proof := managementFreshProof{Stamp: "stamp", Facts: []managementGatewayFacts{{Source: controlSource{sourceView: sourceView{ID: "s"}, Key: "observer", ConfigKey: "admin"}, AtomicSnapshot: true}}}
	retained := managementRetainedProof(proof)
	if !managementValidatorProofEqual(proof, retained) {
		t.Fatal("same authorization proof differs")
	}
	for _, field := range []string{"key", "config_key", "atomic"} {
		changed := managementRetainedProof(proof)
		switch field {
		case "key":
			changed.Facts[0].Source.Key = "rotated"
		case "config_key":
			changed.Facts[0].Source.ConfigKey = "rotated"
		case "atomic":
			changed.Facts[0].AtomicSnapshot = false
		}
		if managementValidatorProofEqual(changed, retained) {
			t.Fatal("private authorization field ignored", field)
		}
	}
	proof.Facts[0].Source.Key = strings.Repeat("x", managementValidatorBudget+1)
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept", managementPageV3MediaType)
	r.Header.Set("X-Console-Current-Validation", "1")
	w := httptest.NewRecorder()
	w.Header().Set("X-Console-Validated-Request-ID", "request")
	w.Header().Set("ETag", `"tag"`)
	s := &Service{}
	s.rememberManagementValidator(w, r, managementPageResponse{Owner: "owner"}, "page", &proof)
	if len(s.pageValidators.entries) != 0 {
		t.Fatal("private source bytes bypassed metadata cap")
	}
}

func TestManagementValidatorStillReadsLiveGatewayPermissionAndRevision(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	var mode atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode.Load() == 2 {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		revision := "r1"
		if mode.Load() == 1 {
			revision = "r2"
		}
		writeJSON(w, 200, map[string]any{"format": "management-revision-v1", "revision": revision, "config_revision": "c1"})
	}))
	defer server.Close()
	source := controlSource{sourceView: sourceView{ID: "s", Name: "S", Base: server.URL}, Key: "observer", ConfigKey: "admin"}
	if _, err := s.control.saveSource(context.Background(), source, false); err != nil {
		t.Fatal(err)
	}
	proof, _, err := s.managementReadProof(context.Background(), s.control.db, "owner")
	if err != nil {
		t.Fatal(err)
	}
	proof.Facts = []managementGatewayFacts{{Source: source, AtomicSnapshot: true, Revision: "r1", ConfigRevision: "c1"}}
	s.pageValidators.put(managementPageValidator{key: "page", owner: "owner", tag: `"complete"`, proof: managementRetainedProof(proof), headers: managementValidatorHeaders(proof), bytes: 4096})
	call := func() (bool, error) {
		r := httptest.NewRequest("GET", "/", nil)
		r = r.WithContext(context.WithValue(r.Context(), consoleRequestIDKey{}, "live-validator"))
		r.Header.Set("Accept", managementPageV3MediaType)
		r.Header.Set("X-Console-Current-Validation", "1")
		r.Header.Set("If-None-Match", `"complete"`)
		return s.currentManagementNotModified(r.Context(), httptest.NewRecorder(), r, "owner", "page", "", false)
	}
	if ok, err := call(); err != nil || !ok {
		t.Fatal("current gateway refused", err)
	}
	mode.Store(1)
	if ok, err := call(); err != nil || ok {
		t.Fatal("changed gateway version confirmed", err)
	}
	mode.Store(2)
	if ok, err := call(); err == nil || ok {
		t.Fatal("revoked gateway permission confirmed", err)
	}
}

func BenchmarkManagementValidatorSkipsCompleteBodyEncoding(b *testing.B) {
	page := managementWireFixture()
	page.Owner = "owner"
	page.Rows = nil
	for g := 0; g < 25; g++ {
		row := managementQueryRow{ID: fmt.Sprint(g), Target: subTarget{GroupID: int64(g)}}
		for m := 0; m < 288; m++ {
			result := &subResult{Model: fmt.Sprint("model-", m), CheckedAt: int64(g*288 + m), Availability: subProbe{Status: "success", Message: "complete current diagnostic", Usage: &subUsage{Status: "matched", InputPrice: usageFloat(2)}}, Quality: subProbe{Status: "skipped"}}
			model := subModelResult{Model: result.Model, State: "done", Result: result}
			row.Target.Models = append(row.Target.Models, model)
			row.Checks = append(row.Checks, managementQueryCheck{subModelResult: model})
		}
		page.Rows = append(page.Rows, row)
	}
	s := &Service{}
	proof := &managementFreshProof{Stamp: "proof", HeadersRevision: 1}
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), consoleRequestIDKey{}, "benchmark-request"))
	r.Header.Set("Accept", managementPageV3MediaType)
	r.Header.Set("X-Console-Current-Validation", "1")
	w := httptest.NewRecorder()
	if err := s.writeAndRememberManagementPage(w, r, page, "page", proof); err != nil {
		b.Fatal(err)
	}
	r.Header.Set("If-None-Match", w.Header().Get("ETag"))
	page = s.managementComputed.pages["page"].Page
	page.currentProof = proof
	for _, name := range []string{"previous_marshal_complete_columns", "verified_tag_only"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				w := httptest.NewRecorder()
				var err error
				if name == "verified_tag_only" {
					err = s.writeAndRememberManagementValidator(w, r, page, "page")
				} else {
					err = writeManagementPage(w, r, page)
				}
				if err != nil || w.Code != 304 || w.Body.Len() != 0 {
					b.Fatal("complete validation changed", err, w.Code)
				}
			}
		})
	}
}

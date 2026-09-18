package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubProbeRecordsFirstCreatedSeparatelyFromFirstText(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{}}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(60 * time.Millisecond)
		// subSSE sends another created event; it must not replace the first clock.
		subSSE(w, "未知")
	}))
	defer upstream.Close()
	result := subRunQualityProbe(context.Background(), upstream.Client(), upstream.URL, "key")
	if result.Availability.ResponseCreatedMS == nil || result.Availability.TTFT == nil || *result.Availability.TTFT-*result.Availability.ResponseCreatedMS < 40 {
		t.Fatalf("created and first text clocks were conflated: %+v", result)
	}
}

func TestSubQualityProbeUsesOneResponseForAvailabilityAndModelMatch(t *testing.T) {
	for _, status := range []int{200, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var in struct {
					Model  string `json:"model"`
					Stream bool   `json:"stream"`
					Input  []struct {
						Content string `json:"content"`
					} `json:"input"`
				}
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					t.Error(err)
				}
				if !in.Stream || in.Model != checkModel || len(in.Input) != 1 || in.Input[0].Content != checkPrompt {
					t.Errorf("unexpected probe: %+v", in)
				}
				if status != 200 {
					http.Error(w, "unavailable", status)
					return
				}
				subSSE(w, "未知", "gpt-5.6-sol")
			}))
			defer upstream.Close()
			result := subRunQualityProbe(context.Background(), upstream.Client(), upstream.URL, "key")
			if calls != 1 || !reflect.DeepEqual(result.Availability, result.Quality) || result.CheckedAt == 0 {
				t.Fatalf("quality probe did not reuse the response: %+v (%d)", result, calls)
			}
			if status == 200 && (result.Verdict != "pass" || result.Availability.Status != "success" || result.Availability.TTFT == nil || result.Availability.ModelMatch != "mismatch" || result.Availability.ResponseModel != "gpt-5.6-sol") {
				t.Fatalf("wrong quality/model result: %+v", result)
			}
			if status != 200 && (result.Verdict != "error" || result.Availability.Status != "error" || result.Availability.ModelMatch != "unavailable") {
				t.Fatal(result)
			}

		})
	}
}

func TestSubQualityQueueScopesPersistsAndStops(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("local PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("q", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := "quality-" + randomID()[:8]
	account := "sub_" + randomID()[:16]
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE owner=$1`, owner)
	key, err := store.encrypt("quality-secret")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$2,'Quality','https://quality.example','quality@example.com','')`, account, owner)
	if err != nil {
		t.Fatal(err)
	}
	for group := 1; group <= 3; group++ {
		_, err = store.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,encrypted_key) VALUES($1,$2,'Group','openai',42,$3)`, account, group, key)
		if err != nil {
			t.Fatal(err)
		}
	}
	previous := subResult{Model: checkModel, CheckedAt: 123, Verdict: "fail", Availability: subProbe{Status: "success", Text: "test", RequestedModel: checkModel, ResponseModel: checkModel, ModelMatch: "match", Duration: 456}}
	raw, _ := json.Marshal(previous)
	for _, model := range []string{checkModel, "gpt-5.6-sol"} {
		_, err = store.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,1,$2,'done',$3)`, account, model, string(raw))
		if err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer quality-secret" {
			t.Error("unexpected request", r.URL.Path)
		}
		var in struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
			Input  []struct {
				Content string `json:"content"`
			} `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		if !in.Stream || in.Model != checkModel || len(in.Input) != 1 || in.Input[0].Content != checkPrompt {
			t.Errorf("extra probe: %+v", in)
		}
		subSSE(w, "未知", "gpt-5.6-sol")
	}))
	defer upstream.Close()
	previousHTTP := subHTTP
	u, _ := url.Parse(upstream.URL)
	subHTTP = &http.Client{Transport: subTestTransport{u, http.DefaultTransport}}
	defer func() { subHTTP = previousHTTP }()
	service := &Service{control: store}
	token, _ := store.newSession(context.Background(), owner)
	foreign, _ := store.newSession(context.Background(), owner+"-other")
	request := func(path, body, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, r)
		return w
	}
	body := fmt.Sprintf(`{"targets":[{"account_id":%q,"group_id":1,"models":["gpt-5.6-sol"]},{"account_id":%q,"group_id":2},{"account_id":%q,"group_id":1}]}`, account, account, account)
	for _, tc := range []struct {
		cookie string
		code   int
	}{{"", 401}, {foreign, 409}} {
		if w := request("/v1/sub2api/quality-checks", body, tc.cookie); w.Code != tc.code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	invalid := fmt.Sprintf(`{"targets":[{"account_id":%q,"group_id":1},{"account_id":%q,"group_id":999}]}`, account, account)
	if w := request("/v1/sub2api/quality-checks", invalid, token); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	var state string
	store.db.QueryRow(`SELECT state FROM console_sub_accounts WHERE id=$1`, account).Scan(&state)
	if state != "idle" {
		t.Fatal("invalid batch partially queued", state)
	}
	if w := request("/v1/sub2api/quality-checks", body, token); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("/v1/sub2api/quality-checks", body, token); w.Code != 409 {
		t.Fatal("duplicate queued", w.Code)
	}
	if !service.subWorkOne(context.Background()) || calls.Load() != 2 {
		t.Fatal("wrong probe count", calls.Load())
	}
	for group := 1; group <= 2; group++ {
		var stored []byte
		if err = store.db.QueryRow(`SELECT result FROM console_sub_models WHERE account_id=$1 AND group_id=$2 AND model=$3`, account, group, checkModel).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		var result subResult
		if err = json.Unmarshal(stored, &result); err != nil {
			t.Fatal(err)
		}
		if result.Verdict != "pass" || result.Availability.ModelMatch != "mismatch" || result.CheckedAt <= 123 || result.Availability.Status != "success" || result.Availability.TTFT == nil || !reflect.DeepEqual(result.Availability, result.Quality) {
			t.Fatalf("wrong persisted result: %+v", result)
		}

		var projection []byte
		store.db.QueryRow(`SELECT result FROM console_sub_targets WHERE account_id=$1 AND group_id=$2`, account, group).Scan(&projection)
		if string(stored) != string(projection) {
			t.Fatal("legacy result projection differs")
		}
	}
	var sibling []byte
	store.db.QueryRow(`SELECT result FROM console_sub_models WHERE account_id=$1 AND group_id=1 AND model='gpt-5.6-sol'`, account).Scan(&sibling)
	var siblingResult subResult
	json.Unmarshal(sibling, &siblingResult)
	if !reflect.DeepEqual(siblingResult, previous) {
		t.Fatal("sibling model changed")
	}
	var unselected int
	store.db.QueryRow(`SELECT count(*) FROM console_sub_models WHERE account_id=$1 AND group_id=3`, account).Scan(&unselected)
	if unselected != 0 {
		t.Fatal("unselected group was checked")
	}
	if w := request("/v1/sub2api/quality-checks", body, token); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	if w := request("/v1/sub2api/accounts/"+account+"/stop", "{}", token); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if service.subWorkOne(context.Background()) || calls.Load() != 2 {
		t.Fatal("stopped probes executed")
	}
}

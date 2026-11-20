package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNewAPIReclaimAtCapacity(t *testing.T) {
	// Route safety deliberately scans all saved sources, including disabled
	// ones. Other integration tests use different master keys for fixtures.
	t.Setenv("TEST_CONTROL_DATABASE_URL", isolatedControlTestDSN(t))
	for _, tc := range []struct {
		name, block              string
		deleteUnknown            bool
		expectedDeletes, creates int
	}{
		{"only oldest proven test key is retired", "", false, 1, 2},
		{"ambiguous failure is not capacity", "generic", false, 0, 1},
		{"old intents are not evidence", "legacy", false, 0, 1},
		{"one successful observation is insufficient", "recent", false, 0, 1},
		{"pending receipts veto cleanup", "receipt", false, 0, 1},
		{"inflight checks veto cleanup", "running", false, 0, 1},
		{"routing credentials are protected", "route", false, 0, 1},
		{"remote group recovery vetoes cleanup", "restored", false, 0, 1},
		{"unexpected active upstream usage vetoes cleanup", "used", false, 0, 1},
		{"recent upstream receipt vetoes cleanup", "upstream_log", false, 0, 1},
		{"secret identity mismatch vetoes cleanup", "secret", false, 0, 1},
		{"temporary channel vetoes cleanup", "live_route", false, 0, 1},
		{"managed draft vetoes cleanup", "draft", false, 0, 1},
		{"a prior rejection does not hide other eligible keys", "previous_reject", false, 1, 2},
		{"DELETE throttling ends the attempt and shares cooldown", "delete_429", false, 1, 1},
		{"incomplete token inventory vetoes cleanup", "incomplete", false, 0, 1},
		{"stolen job lease vetoes cleanup", "stopped", false, 0, 1},
		{"uncertain DELETE is never replayed", "", true, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, account, _ := newAPITestService(t)
			ctx := context.Background()
			const base, job = "https://usage.example", "cleanup-job"
			db := s.control.db
			t.Cleanup(func() {
				db.Exec(`DELETE FROM console_newapi_key_reclaims WHERE account_id=$1`, account)
			})
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := db.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec(`UPDATE console_sub_accounts SET state='running',job_id=$2,job_kind='import',lease_until=now()+interval '2 minutes' WHERE id=$1`, account, job)
			want, err := s.newAPIGroupID(ctx, account, "wanted")
			if err != nil {
				t.Fatal(err)
			}
			created := time.Now().Add(-30 * 24 * time.Hour).Unix()
			groups, tokens := map[string]any{"wanted": map[string]any{"ratio": 1}}, []newAPIToken{}
			add := func(remote string, id, accessed int64, owned bool) int64 {
				group, err := s.newAPIGroupID(ctx, account, remote)
				if err != nil {
					t.Fatal(err)
				}
				key, _ := s.control.encrypt("sk-fixture-" + remote)
				exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,active,state,remote_key_id,encrypted_key,
 newapi_missing_since,newapi_last_missing_at) VALUES($1,$2,$3,'openai',false,'done',$4,$5,now()-interval '9 days',now()-interval '1 day')`,
					account, group, remote, id, key)
				if owned {
					exec(`INSERT INTO console_site_key_creates(account_id,name,acknowledged_base,acknowledged_at,created_at,owned_key_id,owned_user_id,owned_created_at)
 VALUES($1,$2,$3,to_timestamp($5::bigint),to_timestamp($5::bigint),$4,7,$5)`,
						account, subKeyName(account, group), base, id, created)
				} else {
					exec(`INSERT INTO console_site_key_creates(account_id,name) VALUES($1,$2)`, account, subKeyName(account, group))
				}
				tokens = append(tokens, newAPIToken{ID: id, UserID: 7, Name: subKeyName(account, group), Group: remote, Status: 1, Key: "sk-****", Created: created, Accessed: accessed})
				return group
			}
			retired := add("old", 51, created+1, tc.block != "legacy")
			candidateID, candidateName, candidateGroup := int64(51), "old", retired
			// False look-alikes and business keys are never candidates even when
			// they have older last-used timestamps.
			add("manual", 50, created, false)
			if tc.block == "" && !tc.deleteUnknown {
				add("less-old", 52, created+2, true)
			}
			switch tc.block {
			case "recent":
				exec(`UPDATE console_sub_targets SET newapi_last_missing_at=newapi_missing_since WHERE account_id=$1 AND group_id=$2`, account, retired)
			case "receipt":
				exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids) VALUES($1,$2,$3,51,'fixture',$4,'[]')`, account+"-pending", account, retired, created)
			case "running":
				exec(`INSERT INTO console_sub_models(account_id,group_id,model,state) VALUES($1,$2,'fixture','running')`, account, retired)
			case "route":
				exec(`UPDATE console_sub_targets SET routing_key_id=51 WHERE account_id=$1 AND group_id=$2`, account, retired)
			case "restored":
				groups["old"] = map[string]any{"ratio": 1}
			case "used":
				tokens[0].Accessed = time.Now().Unix()
			case "previous_reject":
				exec(`INSERT INTO console_newapi_key_reclaims(account_id,base,attempt_scope,group_id,remote_key_id,state)
 VALUES($1,$2,'earlier-job',$3,51,'rejected')`, account, base, retired)
				candidateID, candidateName = 52, "next"
				candidateGroup = add(candidateName, candidateID, created+2, true)
			case "live_route", "draft":
				source := account + "-source"
				encrypted, _ := s.control.encrypt("fixture-gateway-administrator")
				exec(`INSERT INTO console_sources(id,name,base,encrypted_key) VALUES($1,'fixture','https://usage.example',$2)`, source, encrypted)
				t.Cleanup(func() {
					db.Exec(`DELETE FROM console_managed_config_state WHERE source_id=$1`, source)
					db.Exec(`DELETE FROM console_managed_config_versions WHERE source_id=$1`, source)
					db.Exec(`DELETE FROM console_sources WHERE id=$1`, source)
				})
				if tc.block == "draft" {
					draft, _ := s.control.encrypt(`{"providers":[{"api":"fixture-old"}]}`)
					exec(`WITH draft AS (INSERT INTO console_managed_config_versions(source_id,encrypted_bundle) VALUES($1,$2) RETURNING revision)
 INSERT INTO console_managed_config_state(source_id,draft_revision) SELECT $1,revision FROM draft`, source, draft)
				}
			}
			exec(`INSERT INTO console_sub_spend_logs(account_id,key_id,log_id,at_ms,actual_cost) VALUES($1,51,1,$2,0.1)`, account, (created+1)*1000)
			deletes, creates, readInventory := 0, 0, 0
			candidatePath := "/api/token/" + strconv.FormatInt(candidateID, 10)
			withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/user/self/groups":
					newAPIOK(w, groups)
				case r.URL.Path == "/api/token/" && r.Method == http.MethodGet:
					readInventory++
					total := len(tokens)
					if tc.block == "incomplete" {
						total++
					}
					items := tokens
					if r.URL.Query().Get("p") == "2" {
						items = []newAPIToken{}
					}
					newAPIOK(w, map[string]any{"items": items, "total": total})
				case r.URL.Path == "/api/token/" && r.Method == http.MethodPost:
					creates++
					if tc.block == "generic" {
						w.WriteHeader(400)
					} else if creates == 2 && deletes == 1 && !tc.deleteUnknown {
						newAPIOK(w, nil)
					} else {
						writeJSON(w, 200, map[string]any{"success": false, "message": "Maximum number of tokens reached (50)"})
					}
				case r.URL.Path == candidatePath && r.Method == http.MethodGet:
					if tc.block == "stopped" {
						exec(`UPDATE console_sub_accounts SET job_id='another-job' WHERE id=$1`, account)
					}
					for _, token := range tokens {
						if token.ID == candidateID {
							newAPIOK(w, token)
							return
						}
					}
					http.NotFound(w, r)
				case r.URL.Path == candidatePath+"/key" && r.Method == http.MethodPost:
					key := "fixture-" + candidateName
					if tc.block == "secret" {
						key = "not-the-saved-secret"
					}
					newAPIOK(w, map[string]string{"key": key})
				case r.URL.Path == "/api/log/self":
					if r.URL.Query().Get("token_name") != subKeyName(account, candidateGroup) || r.URL.Query().Get("start_timestamp") == "" {
						t.Error("recent usage must be scoped to the candidate and observation window")
					}
					items := []newAPILog{}
					if tc.block == "upstream_log" {
						items = append(items, newAPILog{ID: 1, Key: 51})
					}
					newAPIOK(w, map[string]any{"total": len(items), "items": items})
				case r.URL.Path == "/v1/channel-settings/providers":
					writeJSON(w, 200, map[string]any{"providers": []configuredProvider{{Provider: "manual-test", API: "sk-fixture-old", Temporary: true}}})
				case r.URL.Path == candidatePath && r.Method == http.MethodDelete:
					deletes++
					if tc.deleteUnknown {
						w.WriteHeader(502)
					} else if tc.block == "delete_429" {
						w.Header().Set("Retry-After", "120")
						w.WriteHeader(429)
					} else {
						for i, token := range tokens {
							if token.ID == candidateID {
								tokens = append(tokens[:i], tokens[i+1:]...)
								break
							}
						}
						newAPIOK(w, nil)
					}
				default:
					t.Errorf("unexpected upstream %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			oldSource := sourceHTTP
			sourceHTTP = subHTTP
			t.Cleanup(func() { sourceHTTP = oldSource })
			err = s.newAPICreateWithReclaim(ctx, account, base, job, "uni-console-route-"+account+"-"+strconv.FormatInt(want, 10), "wanted")
			if (err == nil) != (tc.creates == 2) || creates != tc.creates || deletes != tc.expectedDeletes {
				t.Fatalf("unexpected outcome: create=%d delete=%d err=%v", creates, deletes, err)
			}
			if tc.block == "generic" && readInventory != 0 {
				t.Fatal("a generic 400 started cleanup")
			}
			if tc.creates == 2 {
				var key, facts int64
				var state string
				if err := db.QueryRow(`SELECT t.remote_key_id,r.state,(SELECT count(*) FROM console_sub_spend_logs WHERE account_id=$1 AND key_id=51)
 FROM console_sub_targets t JOIN console_newapi_key_reclaims r USING(account_id) WHERE t.account_id=$1 AND t.group_id=$2 AND r.attempt_scope=$3`,
					account, candidateGroup, job).Scan(&key, &state, &facts); err != nil || key != 0 || state != "deleted" || facts != 1 {
					t.Fatalf("audit, credential or history: id=%d state=%s facts=%d err=%v", key, state, facts, err)
				}
				if err := s.newAPICreateWithReclaim(ctx, account, base, job, "uni-console-route-second", "wanted"); err == nil || deletes != 1 || creates != 3 {
					t.Fatalf("another deletion in the same job: %d %d %v", deletes, creates, err)
				}
			}
			if tc.block == "delete_429" {
				var paused bool
				if err := db.QueryRow(`SELECT retry_at>=now()+interval '115 seconds' FROM console_site_detection_limits WHERE scope=$1`, newAPIKeyCreateScope(base)).Scan(&paused); err != nil || !paused {
					t.Fatalf("DELETE Retry-After was not recorded: %t %v", paused, err)
				}
			}
			if tc.deleteUnknown {
				exec(`UPDATE console_sub_accounts SET job_id='another-job' WHERE id=$1`, account)
				err := s.newAPICreateWithReclaim(ctx, account, base, "another-job", "uni-console-route-second", "wanted")
				if err == nil || !strings.Contains(err.Error(), "尚未确认") || deletes != 1 || creates != 2 {
					t.Fatalf("unknown deletion was replayed: %d %d %v", deletes, creates, err)
				}
			}
		})
	}
}

func TestNewAPIMetadataSyncCreatesKeyOnlyWhenChecking(t *testing.T) {
	t.Setenv("TEST_CONTROL_DATABASE_URL", isolatedControlTestDSN(t))
	s, account, _ := newAPITestService(t)
	ctx := context.Background()
	db := s.control.db
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	var tokens []newAPIToken
	inventories, creates, probes := 0, 0, 0
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/user/self/groups":
			newAPIOK(w, map[string]any{"first": map[string]any{"ratio": 1}, "second": map[string]any{"ratio": 2}})
		case r.URL.Path == "/api/user/models":
			newAPIOK(w, []string{"fixture-model"})
		case r.URL.Path == "/api/user/self":
			newAPIOK(w, newAPIUser{ID: 7})
		case r.URL.Path == "/api/token/" && r.Method == "GET":
			inventories++
			items := append([]newAPIToken{}, tokens...)
			newAPIOK(w, map[string]any{"items": items, "total": len(items)})
		case r.URL.Path == "/api/token/" && r.Method == "POST":
			creates++
			var token newAPIToken
			want := []string{"first", "second"}[min(creates-1, 1)]
			if err := json.NewDecoder(r.Body).Decode(&token); err != nil || token.Group != want {
				t.Errorf("unexpected creation: group=%q err=%v", token.Group, err)
			}
			token.ID, token.UserID, token.Status, token.Key, token.Created = int64(100+creates), 7, 1, "sk-fixture-"+want, time.Now().Unix()
			tokens = append(tokens, token)
			newAPIOK(w, nil)
		case r.URL.Path == "/v1/responses":
			probes++
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"test\"}\n\n")
		default:
			t.Errorf("unexpected upstream %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	exec(`UPDATE console_sub_accounts SET state='queued',job_kind='sync',sync_models='[]',probe_limits='{"rpm_limit":480}' WHERE id=$1`, account)
	if !s.subWorkOne(ctx) || creates != 0 || inventories != 0 || probes != 0 {
		t.Fatalf("metadata-only sync used detection keys: %d %d %d", creates, inventories, probes)
	}
	var group int64
	if err := db.QueryRow(`SELECT group_id FROM console_sub_targets WHERE account_id=$1 AND name='first' AND state='done' AND sync_key_state='not_needed'`, account).Scan(&group); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if code, err := s.subEnqueueCheck(ctx, tx, subSelection{AccountID: account, GroupID: group}, "check", "fixture-model"); err != nil || code != 202 {
		t.Fatal(code, err)
	}
	if err := s.subPromoteAccountChecks(ctx, tx, account); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if !s.subWorkOne(ctx) || creates != 1 || probes != 1 {
		t.Fatalf("explicit check did not create and use one key: %d %d", creates, probes)
	}
	var owned int64
	if err := db.QueryRow(`SELECT owned_key_id FROM console_site_key_creates WHERE account_id=$1 AND name=$2`,
		account, subKeyName(account, group)).Scan(&owned); err != nil || owned != 101 {
		t.Fatalf("verified create was not bound: id=%d err=%v", owned, err)
	}
	var second int64
	if err := db.QueryRow(`SELECT group_id FROM console_sub_targets WHERE account_id=$1 AND name='second' AND encrypted_key=''`, account).Scan(&second); err != nil {
		t.Fatal(err)
	}
	plan, err := s.prepareFingerprint(ctx, fingerprintTarget{Account: account, Group: second, Model: checkModel})
	if err != nil || creates != 2 || probes != 1 || plan.config["key"] != "sk-fixture-second" {
		t.Fatalf("explicit fingerprint did not prepare one Key before probing: %d %d %v", creates, probes, err)
	}
	var state, job string
	if err := db.QueryRow(`SELECT state,job_id FROM console_sub_accounts WHERE id=$1`, account).Scan(&state, &job); err != nil || state != "idle" || job != "" {
		t.Fatalf("fingerprint preparation left the account claimed: %s %s %v", state, job, err)
	}
}

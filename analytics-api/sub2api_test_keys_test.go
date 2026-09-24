package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testQuota(v float64) *float64 { return &v }

func TestSubTestKeyStateDistinguishesQuotaExpiryAndDisabled(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	for _, tc := range []struct {
		name, kind, message string
		key                 subRemoteKey
	}{
		{"active", "", "", subRemoteKey{Status: "active"}},
		{"quota", "quota_exhausted", "已用 $1.022945 / 限额 $1.000000", subRemoteKey{Status: "quota_exhausted", Quota: testQuota(1), QuotaUsed: testQuota(1.02294516)}},
		{"quota without amounts", "quota_exhausted", "额度已耗尽", subRemoteKey{Status: "quota_exhausted"}},
		{"quota before status update", "quota_exhausted", "额度已耗尽", subRemoteKey{Status: "active", Quota: testQuota(5), QuotaUsed: testQuota(5)}},
		{"unlimited", "", "", subRemoteKey{Status: "active", Quota: testQuota(0), QuotaUsed: testQuota(100)}},
		{"expired", "expired", "已过期", subRemoteKey{Status: "expired"}},
		{"past expiry", "expired", "已过期", subRemoteKey{Status: "active", ExpiresAt: &past}},
		{"disabled", "disabled", "已停用", subRemoteKey{Status: "disabled", Quota: testQuota(1), QuotaUsed: testQuota(2)}},
		{"inactive", "disabled", "已停用", subRemoteKey{Status: "inactive"}},
		{"unrecognized", "unavailable", "状态不可用", subRemoteKey{Status: "unknown-private-secret"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := subTestKeyStateError(tc.key)
			if tc.kind == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || subTestKeyFailureKind(err) != tc.kind || !strings.Contains(err.Error(), tc.message) || strings.Contains(err.Error(), "private-secret") {
				t.Fatalf("wrong diagnostic: %v", err)
			}
		})
	}
	if got := subTestKeyFailureSummary(map[string]int{"quota_exhausted": 2, "disabled": 1}); got != "2 个分组测试 key 额度耗尽；1 个分组测试 key 已停用，详情见表格" {
		t.Fatal(got)
	}
}

func TestSubPrepareTestKeyScopesLegacyMigrationAndVerifiesOutcome(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	for _, mode := range []string{"active", "exhausted", "unlimited", "custom", "disabled", "expired", "different stored key", "different stored id", "wrong group", "wrong name", "changed on reread", "changed credential", "read failed", "write failed", "verify failed", "quota unchanged", "status unchanged"} {
		t.Run(mode, func(t *testing.T) {
			key := subRemoteKey{ID: 42, Key: "private-key", Name: subKeyName("account", 7), GroupID: 7, Status: "active", Quota: testQuota(1), QuotaUsed: testQuota(0.2)}
			storedID, storedKey := int64(42), key.Key
			switch mode {
			case "exhausted", "status unchanged":
				key.Status = "quota_exhausted"
				key.QuotaUsed = testQuota(1.02)
			case "unlimited":
				key.Quota = testQuota(0)
			case "custom":
				key.Quota = testQuota(10)
			case "disabled":
				key.Status = "inactive"
			case "expired":
				key.ExpiresAt = &past
			case "different stored key":
				storedKey = "another-key"
			case "different stored id":
				storedID = 43
			case "wrong group":
				key.GroupID = 8
			case "wrong name":
				key.Name = "business-key"
			}
			current := key
			reads, writes := 0, 0
			call := func(method, path string, body, out any, idem string) error {
				if path != "/api/v1/keys/42" {
					t.Fatalf("unexpected path %s", path)
				}
				if method == "GET" {
					reads++
					if mode == "read failed" || (mode == "verify failed" && reads == 2) {
						return errors.New("connection failed")
					}
					if mode == "changed on reread" {
						current.Status = "inactive"
					}
					if mode == "changed credential" {
						current.Key = "replacement-key"
					}
					*out.(*subRemoteKey) = current
					return nil
				}
				if method != "PUT" || !reflect.DeepEqual(body, map[string]any{"quota": 0}) {
					t.Fatalf("unexpected mutation %s %#v", method, body)
				}
				writes++
				if mode == "write failed" {
					return errors.New("update rejected")
				}
				if mode != "quota unchanged" {
					current.Quota = testQuota(0)
				}
				if mode != "status unchanged" {
					current.Status = "active"
				}
				return nil
			}
			got, err := subPrepareTestKey(call, "account", 7, key, true, storedID, storedKey)
			shouldWrite := mode == "active" || mode == "exhausted" || mode == "write failed" || mode == "verify failed" || mode == "quota unchanged" || mode == "status unchanged"
			if (writes == 1) != shouldWrite {
				t.Fatalf("writes=%d, err=%v", writes, err)
			}
			if mode == "active" || mode == "exhausted" {
				if err != nil || reads != 2 || got.Quota == nil || *got.Quota != 0 || got.QuotaUsed != key.QuotaUsed || got.Key != key.Key {
					t.Fatalf("migration: %+v, %v", got, err)
				}
			} else if mode != "unlimited" && mode != "custom" && mode != "different stored key" && mode != "different stored id" && err == nil {
				t.Fatal("unsafe/unconfirmed key was accepted")
			}
		})
	}
}

func TestSubSynchronizeUncappedKeysAndLegacyQuotaMigration(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("q", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := "key-quota-" + randomID()[:12]
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, id)
	auth, _ := store.encrypt(`{"access_token":"panel-secret"}`)
	if _, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth,job_id,state) VALUES($1,$1,'Quota fixture','https://site.example','me@example.com',$2,'job','running')`, id, auth); err != nil {
		t.Fatal(err)
	}
	keys := map[int64]subRemoteKey{}
	for group := int64(1); group <= 5; group++ {
		key := subRemoteKey{ID: group + 10, GroupID: group, Name: subKeyName(id, group), Key: "test-secret-" + strconv.FormatInt(group, 10), Status: "active", Quota: testQuota(1), QuotaUsed: testQuota(0.2)}
		if group == 1 {
			key.Status = "quota_exhausted"
			key.QuotaUsed = testQuota(1.02)
		}
		if group == 3 {
			key.Status = "inactive"
		}
		if group == 4 {
			key.Status = "quota_exhausted"
			key.Quota = testQuota(5)
			key.QuotaUsed = testQuota(5.1)
		}
		if group == 5 {
			key.Quota = testQuota(0)
		}
		keys[group] = key
		enc, _ := store.encrypt(key.Key)
		if _, err = store.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,encrypted_key,encrypted_routing_key,routing_key_id) VALUES($1,$2,'Fixture','openai',$3,$4,'business-secret-cipher',99)`, id, group, key.ID, enc); err != nil {
			t.Fatal(err)
		}
	}
	created, writes := 0, 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok := func(data any) { writeJSON(w, 200, map[string]any{"code": 0, "data": data}) }
		switch r.URL.Path {
		case "/api/v1/groups/available":
			groups := []subRemoteGroup{}
			for group := int64(1); group <= 6; group++ {
				groups = append(groups, subRemoteGroup{ID: group, Name: "Fixture", Platform: "openai"})
			}
			ok(groups)
		case "/api/v1/groups/rates":
			ok(map[string]float64{})
		case "/api/v1/channels/available":
			ok([]any{})
		case "/api/v1/keys":
			if r.Method == "GET" {
				items := []subRemoteKey{}
				for _, key := range keys {
					items = append(items, key)
				}
				ok(map[string]any{"items": items, "pages": 1})
			} else {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if !reflect.DeepEqual(body, map[string]any{"name": subKeyName(id, 6), "group_id": float64(6)}) || r.Header.Get("Idempotency-Key") == "" {
					t.Errorf("bad create request %#v", body)
				}
				created++
				keys[6] = subRemoteKey{ID: 16, GroupID: 6, Name: subKeyName(id, 6), Key: "new-secret", Status: "active", Quota: testQuota(0)}
				ok(keys[6])
			}
		case "/v1/sub2api/billing":
			writeJSON(w, 200, map[string]any{"object": "sub2api.key_billing", "billing_scope": "token", "effective_rate_multiplier": 1})
		default:
			var group int64
			for g, key := range keys {
				if r.URL.Path == "/api/v1/keys/"+strconv.FormatInt(key.ID, 10) {
					group = g
				}
			}
			if group == 0 || group > 2 {
				t.Error("unexpected upstream call", r.Method, r.URL.Path)
				http.Error(w, "unexpected", 400)
				return
			}
			key := keys[group]
			if r.Method == "PUT" {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if !reflect.DeepEqual(body, map[string]any{"quota": float64(0)}) {
					t.Errorf("bad quota update %#v", body)
				}
				writes++
				key.Quota = testQuota(0)
				key.Status = "active"
				keys[group] = key
			} else if r.Method != "GET" {
				t.Error("unexpected method", r.Method)
			}
			ok(key)
		}
	}))
	defer up.Close()
	oldHTTP := subHTTP
	u, _ := url.Parse(up.URL)
	subHTTP = &http.Client{Transport: subTestTransport{u, http.DefaultTransport}}
	defer func() { subHTTP = oldHTTP }()
	svc := &Service{control: store}
	for attempt := 0; attempt < 2; attempt++ {
		if err = svc.subSynchronize(context.Background(), id, "https://site.example", "job", auth); err != nil {
			t.Fatal(err)
		}
	}
	if created != 1 || writes != 2 {
		t.Fatalf("duplicate/unexpected mutation: created=%d writes=%d", created, writes)
	}
	var message, business string
	if err = store.db.QueryRow(`SELECT message FROM console_sub_accounts WHERE id=$1`, id).Scan(&message); err != nil {
		t.Fatal(err)
	}
	if message != "1 个分组测试 key 额度耗尽；1 个分组测试 key 已停用，详情见表格" {
		t.Fatal(message)
	}
	if err = store.db.QueryRow(`SELECT encrypted_routing_key FROM console_sub_targets WHERE account_id=$1 AND group_id=1`, id).Scan(&business); err != nil || business != "business-secret-cipher" {
		t.Fatal("business key changed", err)
	}
	if *keys[1].QuotaUsed != 1.02 || *keys[4].Quota != 5 || keys[3].Status != "inactive" {
		t.Fatal("usage, custom quota or disabled status changed")
	}
}

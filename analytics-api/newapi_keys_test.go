package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Reproduce the New API search contract: exact names, ten searches per window.
// Two synchronizations must recover/reuse all twenty groups without hitting
// that endpoint or modifying the account's unrelated business credential.
func TestNewAPISyncTwentyGroupsWithoutSearchRateLimit(t *testing.T) {
	s, account, _ := newAPITestService(t)
	ctx := context.Background()
	if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET state='running',job_id='sync-keys' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	groups := map[string]any{}
	tokens := []newAPIToken{{ID: 1, UserID: 7, Name: "business-key", Group: "group-00", Key: "sk-****", Status: 1, Unlimited: true}}
	// A previous interrupted sync already created seven keys, matching the
	// production failure. They must be adopted by prefix without being recreated.
	for i := 0; i < 20; i++ {
		remote := fmt.Sprintf("group-%02d", i)
		groups[remote] = map[string]any{"ratio": 1}
		id, err := s.newAPIGroupID(ctx, account, remote)
		if err != nil {
			t.Fatal(err)
		}
		if i < 7 {
			tokens = append(tokens, newAPIToken{ID: int64(i + 2), UserID: 7, Name: subKeyName(account, id), Group: remote, Key: "sk-****", Status: 1, Unlimited: true})
		}
	}
	var mu sync.Mutex
	searches, creates, reveals := 0, 0, 0
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/api/user/self/groups":
			newAPIOK(w, groups)
		case r.URL.Path == "/api/user/models":
			newAPIOK(w, []string{"gpt-6-sol"})
		case r.URL.Path == "/api/user/self":
			newAPIOK(w, map[string]any{"id": 7, "username": "fixture"})
		case r.URL.Path == "/api/token/search":
			searches++
			if searches > 10 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			found := []newAPIToken{}
			for _, token := range tokens {
				if token.Name == r.URL.Query().Get("keyword") {
					found = append(found, token)
				}
			}
			newAPIOK(w, map[string]any{"items": found, "total": len(found)})
		case r.URL.Path == "/api/token/" && r.Method == "GET":
			newAPIOK(w, map[string]any{"items": tokens, "total": len(tokens)})
		case r.URL.Path == "/api/token/" && r.Method == "POST":
			var token newAPIToken
			if err := json.NewDecoder(r.Body).Decode(&token); err != nil {
				t.Error(err)
			}
			for _, previous := range tokens {
				if previous.Name == token.Name {
					t.Error("duplicate key created", token.Name)
				}
			}
			creates++
			token.ID, token.UserID, token.Key, token.Status = int64(len(tokens)+1), 7, "sk-****", 1
			tokens = append(tokens, token)
			newAPIOK(w, nil)
		case strings.HasPrefix(r.URL.Path, "/api/token/") && strings.HasSuffix(r.URL.Path, "/key") && r.Method == "POST":
			id, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/token/"), "/key"))
			if id <= 1 || id > len(tokens) {
				t.Error("attempted to reveal an unrelated/unknown key", id)
			}
			reveals++
			newAPIOK(w, map[string]string{"key": fmt.Sprintf("fixture-%d", id)})
		default:
			t.Error("unexpected upstream operation", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	for pass := 0; pass < 2; pass++ {
		if err := s.subSynchronize(ctx, account, "https://usage.example", "sync-keys", ""); err != nil {
			t.Fatal(err)
		}
		var ready, failed int
		if err := s.control.db.QueryRow(`SELECT count(*) FILTER (WHERE remote_key_id>0 AND encrypted_key<>''),count(*) FILTER (WHERE state='error') FROM console_sub_targets WHERE account_id=$1`, account).Scan(&ready, &failed); err != nil {
			t.Fatal(err)
		}
		if ready != 20 || failed != 0 {
			t.Fatalf("pass %d: ready=%d failed=%d", pass, ready, failed)
		}
	}
	if searches != 0 || creates != 13 || reveals != 40 {
		t.Fatalf("searches=%d creates=%d reveals=%d", searches, creates, reveals)
	}
}

func TestNewAPIKeyInventoryFiltersBeforeRevealingAndPaginatesMatches(t *testing.T) {
	s, account, _ := newAPITestService(t)
	prefix := "uni-console-check-" + account + "-"
	tokens := make([]newAPIToken, 202)
	for i := range tokens {
		name := fmt.Sprintf("business-%d", i)
		if i >= 100 {
			name = prefix + strconv.Itoa(i)
		}
		tokens[i] = newAPIToken{ID: int64(i + 1), UserID: 7, Name: name, Group: "default", Key: "sk-****", Status: 1, Unlimited: true}
	}
	var mu sync.Mutex
	reads, reveals := 0, 0
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "GET" && r.URL.Path == "/api/token/" {
			reads++
			page, _ := strconv.Atoi(r.URL.Query().Get("p"))
			start := (page - 1) * 100
			newAPIOK(w, map[string]any{"items": tokens[start:min(start+100, len(tokens))], "total": len(tokens)})
			return
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/key") {
			id, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/token/"), "/key"))
			if id < 101 || id > 202 {
				t.Error("unrelated key revealed", id)
			}
			reveals++
			newAPIOK(w, map[string]string{"key": "fixture"})
			return
		}
		t.Error("unexpected operation", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	for page, want := range []int{100, 2, 0} {
		keys, total, err := s.newAPIKeys(context.Background(), account, "https://usage.example", prefix, page+1)
		if err != nil || total != 102 || len(keys) != want {
			t.Fatalf("page=%d count=%d total=%d err=%v", page+1, len(keys), total, err)
		}
		for i, key := range keys {
			if key.ID != int64(101+page*100+i) {
				t.Fatal("wrong filtered page identity", key.ID)
			}
		}
	}
	if reads != 9 || reveals != 102 {
		t.Fatalf("reads=%d reveals=%d", reads, reveals)
	}
}

func TestNewAPIEnsureKeyDoesNotCreateAfterIncompleteInventory(t *testing.T) {
	for _, failure := range []string{"repeated_page", "rate_limit", "invalid_identity"} {
		t.Run(failure, func(t *testing.T) {
			s, account, _ := newAPITestService(t)
			ctx := context.Background()
			group, err := s.newAPIGroupID(ctx, account, "default")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/api/token/" {
					t.Error("must not mutate or reveal on an incomplete inventory", r.Method, r.URL.Path)
				}
				if failure == "rate_limit" {
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				id := int64(1)
				if failure == "invalid_identity" {
					id = 0
				}
				newAPIOK(w, map[string]any{"items": []newAPIToken{{ID: id, Name: "business-key"}}, "total": 2})
			}))
			_, err = s.newAPIEnsureKey(ctx, account, "https://usage.example", subKeyName(account, group), group)
			if err == nil || calls > 2 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
			var intents int
			if err := s.control.db.QueryRow(`SELECT count(*) FROM console_site_key_creates WHERE account_id=$1`, account).Scan(&intents); err != nil || intents != 0 {
				t.Fatalf("partial inventory claimed creation: count=%d err=%v", intents, err)
			}
		})
	}
}

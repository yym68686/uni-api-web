package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestImportedObjectPageBoundsAndSourceIsolation(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	if _, err := e.DB.Exec("INSERT INTO imported_objects SELECT 'unrelated-'||i,'e',current_timestamp,0 FROM range(20000) t(i)"); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for i := 0; i < 1100; i++ {
		k := fmt.Sprintf("key-%d", i)
		keys = append(keys, k)
		if _, err := e.DB.Exec("INSERT INTO imported_objects(object_key,etag) VALUES(?,?)", k, "e"); err != nil {
			t.Fatal(err)
		}
	}
	keys = append(keys, "absent", "digitalocean::key-0")
	page, err := e.ImportedObjectPage(ctx, keys)
	if err != nil || len(page) != 1100 {
		t.Fatal(len(page), err)
	}
	if _, ok := page["digitalocean::key-0"]; ok {
		t.Fatal("cross-source match")
	}
	if p, err := e.ImportedObjectPage(ctx, nil); err != nil || len(p) != 0 {
		t.Fatal(p, err)
	}
}
func TestPageImportRetainsLateArrivalsAndRejectsChangedETags(t *testing.T) {
	var phase atomic.Int32
	var downloaded atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list-type") == "2" {
			if r.URL.Query().Get("continuation-token") == "next" {
				writeList(w, "facts/late.jsonl", "")
				return
			}
			if phase.Load() == 2 {
				w.Header().Set("Content-Type", "application/xml")
				fmt.Fprint(w, `<ListBucketResult><Contents><Key>facts/first.jsonl</Key><ETag>changed</ETag><Size>100</Size></Contents><IsTruncated>false</IsTruncated></ListBucketResult>`)
				return
			}
			token := ""
			if phase.Load() > 0 {
				token = "next"
			}
			writeList(w, "facts/first.jsonl", token)
			return
		}
		downloaded.Add(1)
		json.NewEncoder(w).Encode(Fact{Schema: 1, EventID: r.URL.Path, Kind: "request", AtMS: time.Now().Add(-time.Hour).UnixMilli(), Outcome: "success"})
	}))
	defer up.Close()
	s := importFixtureClient(t, up.URL)
	ctx := context.Background()
	if err := s.importSingleS3(ctx); err != nil {
		t.Fatal(err)
	}
	phase.Store(1)
	if err := s.importSingleS3(ctx); err != nil {
		t.Fatal(err)
	}
	if downloaded.Load() != 2 {
		t.Fatal("re-downloaded known objects", downloaded.Load())
	}
	phase.Store(2)
	if err := s.importSingleS3(ctx); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatal(err)
	}
	var n int
	s.engine.DB.QueryRow("SELECT count(*) FROM imported_objects").Scan(&n)
	if n != 2 {
		t.Fatal(n)
	}
}

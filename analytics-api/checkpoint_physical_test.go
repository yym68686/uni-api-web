package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPhysicalCheckpointPreservesConcurrentSettingsAndPools(t *testing.T) {
	store, objects := checkpointFixture(t)
	target := stateTestEngine(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	var conns []*sql.Conn
	for i := 0; i < 4; i++ {
		c, e := target.DB.Conn(ctx)
		if e != nil {
			t.Fatal(e)
		}
		conns = append(conns, c)
	}
	for _, c := range conns {
		c.Close()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 15; i++ {
			if err := target.SavePrice(ctx, Price{Model: "concurrent", Input: float64(i), Source: "manual", Verified: true}); err != nil {
				t.Error(err)
			}
		}
	}()
	if ok, err := store.restore(ctx, target); err != nil || !ok {
		t.Fatal(ok, err)
	}
	wg.Wait()
	var input float64
	if err := target.DB.QueryRow("SELECT input FROM prices WHERE model='concurrent'").Scan(&input); err != nil || input != 14 {
		t.Fatal("price reverted", input, err)
	}
	var n int
	target.DB.QueryRow("SELECT count(*) FROM information_schema.tables WHERE table_catalog='history'").Scan(&n)
	if n != 3 {
		t.Fatal("config leaked into history", n)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var n int
			if err := target.DB.QueryRow("SELECT count(*) FROM facts").Scan(&n); err != nil || n != 1 {
				t.Error(n, err)
			}
		}()
	}
	wg.Wait()
	// Restoring the physical file must preserve primary keys and replay idempotence.
	f := Fact{Schema: 1, EventID: "checkpoint-request", Kind: "request", AtMS: time.Now().UnixMilli(), Outcome: "success"}
	if err := target.Import(ctx, "different-object", "etag", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	target.DB.QueryRow("SELECT count(*) FROM facts").Scan(&n)
	if n != 1 {
		t.Fatal("duplicate facts", n)
	}
	if objects.metadata["manifest"] == "" {
		t.Fatal("missing manifest")
	}
}

func TestPhysicalCheckpointRejectsInvalidFilesWithoutReplacingCache(t *testing.T) {
	for _, kind := range []string{"row_count", "schema", "file_hash", "truncated", "extra_size"} {
		t.Run(kind, func(t *testing.T) {
			store, objects := checkpointFixture(t)
			target := stateTestEngine(t)
			ctx := context.Background()
			// Keep a valid local history sentinel with an empty replay index.
			if err := target.Import(ctx, "existing", "e", []Fact{{Schema: 1, EventID: "existing", Kind: "request", AtMS: time.Now().UnixMilli(), Outcome: "success"}}); err != nil {
				t.Fatal(err)
			}
			target.DB.Exec("DELETE FROM imported_objects")
			var m physicalManifest
			json.Unmarshal([]byte(objects.metadata["manifest"]), &m)
			switch kind {
			case "row_count":
				m.Rows["facts"]++
			case "schema":
				m.Schema = "wrong"
			case "file_hash":
				m.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
			case "truncated":
				objects.body = objects.body[:len(objects.body)/2]
			case "extra_size":
				m.Bytes = 4*maxCheckpointBytes + 1
			}
			raw, _ := json.Marshal(m)
			objects.metadata["manifest"] = string(raw)
			if ok, _, err := store.restorePhysical(ctx, target); err == nil || ok {
				t.Fatal("invalid accepted", ok, err)
			}
			var id string
			if err := target.DB.QueryRow("SELECT event_id FROM facts").Scan(&id); err != nil || id != "existing" {
				t.Fatal(id, err)
			}
		})
	}
}

func TestPhysicalCheckpointInstallCancellationKeepsOldDatabase(t *testing.T) {
	target := stateTestEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := target.installPhysical(ctx, "absent.duckdb"); err == nil {
		t.Fatal("canceled install accepted")
	}
	var n int
	if err := target.DB.QueryRow("SELECT count(*) FROM facts").Scan(&n); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyCombinedCacheMigratesPricesWithoutRevertingEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "combined.duckdb")
	db, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE prices(model VARCHAR PRIMARY KEY,input DOUBLE,output DOUBLE,cache_read DOUBLE,cache_write DOUBLE,cache_write_1h DOUBLE,source VARCHAR,verified BOOLEAN,effective_at TIMESTAMP);INSERT INTO prices VALUES('custom',17,2,0,0,0,'manual',true,current_timestamp);CREATE TABLE price_history(model VARCHAR,document VARCHAR,updated_at TIMESTAMP DEFAULT current_timestamp);CREATE TABLE meta(name VARCHAR PRIMARY KEY,value VARCHAR);INSERT INTO meta VALUES('operator','kept')`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	e, err := OpenEngine(path, Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.SavePrice(context.Background(), Price{Model: "custom", Input: 23, Source: "manual", Verified: true}); err != nil {
		t.Fatal(err)
	}
	e.Close()
	e, err = OpenEngine(path, Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	var input float64
	var historyTime bool
	e.DB.QueryRow("SELECT input FROM prices WHERE model='custom'").Scan(&input)
	e.DB.QueryRow("SELECT updated_at IS NOT NULL FROM price_history LIMIT 1").Scan(&historyTime)
	if input != 23 || !historyTime {
		t.Fatal(input, historyTime)
	}
}

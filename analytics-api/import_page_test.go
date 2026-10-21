package main

import (
	"context"
	"fmt"
	"testing"
)

func TestImportedObjectPageBoundsAndSourceIsolation(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	if _, err := e.DB.Exec("INSERT INTO imported_objects(object_key,etag,events) SELECT 'unrelated-'||i,'e',0 FROM generate_series(0,19999) t(i)"); err != nil {
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

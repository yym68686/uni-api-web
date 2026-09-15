package main

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func BenchmarkImportBatch(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		e, err := OpenEngine(filepath.Join(b.TempDir(), "import.duckdb"), Config{Timezone: "UTC"})
		if err != nil {
			b.Fatal(err)
		}
		objects := make([]FactObject, 32)
		at := time.Now().Add(-time.Minute).UnixMilli()
		for j := range objects {
			objects[j] = FactObject{Key: fmt.Sprintf("batch-%d.jsonl", j), ETag: fmt.Sprint(j)}
			for k := 0; k < 16; k++ {
				objects[j].Facts = append(objects[j].Facts, Fact{Schema: 1, EventID: fmt.Sprintf("event-%d-%d", j, k), Kind: "request", AtMS: at, Provider: "provider", Model: "model", Outcome: "success"})
			}
		}
		b.StartTimer()
		err = e.ImportBatch(context.Background(), objects)
		b.StopTimer()
		e.Close()
		if err != nil {
			b.Fatal(err)
		}
	}
}

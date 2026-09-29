package main

import (
	"context"
	"path/filepath"
	"testing"
)

func BenchmarkRequestTraceLookup(b *testing.B) {
	e, err := OpenEngine(filepath.Join(b.TempDir(), "trace.duckdb"), Config{Timezone: "UTC", DatabaseMemoryLimitMB: 512})
	if err != nil {
		b.Fatal(err)
	}
	defer e.Close()
	// One million facts, twenty facts per request, including trace-only lookup.
	_, err = e.DB.Exec(`INSERT INTO facts(event_id,kind,source_id,instance_id,request_id,trace_id,at_ms,model)
 SELECT 'event-'||i,'trace','primary','instance','request-'||(i//20),'trace-'||(i//20),1700000000000+i,'gpt-6-sol' FROM range(1000000) t(i)`)
	if err != nil {
		b.Fatal(err)
	}
	for _, id := range []string{"request-49999", "trace-49999", "not-found"} {
		b.Run(id, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				result, err := e.RequestTrace(context.Background(), id, []string{"primary"}, "primary")
				if err != nil {
					b.Fatal(err)
				}
				if id != "not-found" && (len(result.Data) != 1 || len(result.Data[0].Events) != 20) {
					b.Fatal("incomplete trace", result)
				}
			}
		})
	}
}

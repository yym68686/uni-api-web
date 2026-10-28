package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// Independent pre-batching implementation: keep absent, null and an empty
// RawMessage distinct, and compare the complete columns rather than a decode.
func previousManagementColumns(rows []map[string]json.RawMessage) managementResultColumns {
	out := managementResultColumns{Count: len(rows), Fields: []string{}, Columns: [][][]json.RawMessage{}}
	names := map[string]bool{}
	for _, row := range rows {
		for k := range row {
			names[k] = true
		}
	}
	for k := range names {
		out.Fields = append(out.Fields, k)
	}
	sort.Strings(out.Fields)
	for _, field := range out.Fields {
		column := make([][]json.RawMessage, len(rows))
		for i, row := range rows {
			column[i] = []json.RawMessage{}
			if value, ok := row[field]; ok {
				column[i] = []json.RawMessage{value}
			}
		}
		out.Columns = append(out.Columns, column)
	}
	return out
}

func TestManagementBatchedColumnCellsPreserveExactRepresentation(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	fields := []string{"", "model", "usage", "模型🙂", "<>&\u2028", "bad\xff"}
	values := []json.RawMessage{nil, {}, json.RawMessage(`null`), json.RawMessage(`0`), json.RawMessage(`false`), json.RawMessage(`""`), json.RawMessage(`[]`), json.RawMessage(`{}`), json.RawMessage(`"模型🙂"`), json.RawMessage(`{"x":[null,0]}`)}
	cases := [][]map[string]json.RawMessage{nil, {}, {nil}, {{}}, {{"null": json.RawMessage(`null`)}, {}, nil}}
	for n := 0; n < 250; n++ {
		rows := make([]map[string]json.RawMessage, rng.Intn(80))
		for i := range rows {
			if rng.Intn(5) == 0 {
				continue
			}
			rows[i] = map[string]json.RawMessage{}
			for _, field := range fields {
				if rng.Intn(3) == 0 {
					rows[i][field] = values[rng.Intn(len(values))]
				}
			}
		}
		cases = append(cases, rows)
	}
	for n, rows := range cases {
		old, got := previousManagementColumns(rows), managementColumns(rows)
		if !reflect.DeepEqual(old, got) {
			t.Fatalf("column representation differs for case %d", n)
		}
		wantJSON, oldErr := json.Marshal(old)
		gotJSON, err := json.Marshal(got)
		if string(wantJSON) != string(gotJSON) || fmt.Sprint(oldErr) != fmt.Sprint(err) {
			t.Fatalf("JSON bytes or encoding error differs for case %d", n)
		}
	}
}

func TestManagementBatchedColumnCellsRemainIndependent(t *testing.T) {
	rows := []map[string]json.RawMessage{{"a": json.RawMessage(`1`), "b": json.RawMessage(`2`)}, {"a": json.RawMessage(`3`)}, {"a": json.RawMessage(`4`)}}
	got := managementColumns(rows)
	for _, column := range got.Columns {
		for _, cell := range column {
			if len(cell) == 1 && cap(cell) != 1 {
				t.Fatal("cell can be resliced or appended over a neighboring value")
			}
		}
	}
	got.Columns[0][0][0] = json.RawMessage(`9`)
	got.Columns[0][1] = append(got.Columns[0][1], json.RawMessage(`8`))
	if string(got.Columns[0][2][0]) != `4` || string(got.Columns[1][0][0]) != `2` || string(rows[0]["a"]) != `1` {
		t.Fatal("editing/appending one cell overwrote another cell or input slot")
	}
	if got.Columns[1][1] == nil || len(got.Columns[1][1]) != 0 {
		t.Fatal("absent field became null rather than an empty cell")
	}
}

func BenchmarkManagementColumnCellAllocation(b *testing.B) {
	rows := make([]map[string]json.RawMessage, 25*288)
	for i := range rows {
		rows[i] = map[string]json.RawMessage{}
		for field := range 25 {
			if field%4 != 0 || i%5 == 0 {
				rows[i][fmt.Sprintf("field-%02d", field)] = json.RawMessage(`"complete evidence"`)
			}
		}
	}
	for _, entry := range []struct {
		name    string
		columns func([]map[string]json.RawMessage) managementResultColumns
	}{{"previous_per_cell", previousManagementColumns}, {"batched_per_column", managementColumns}} {
		b.Run(entry.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				out := entry.columns(rows)
				if out.Count != len(rows) || len(out.Fields) != 25 {
					b.Fatal("incomplete output")
				}
			}
		})
	}
}

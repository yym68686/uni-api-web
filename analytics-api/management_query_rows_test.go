package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"testing"
)

func TestManagementQueryRowsMatchBrowserContract(t *testing.T) {
	raw := managementContractFixture(t, "testdata/management-query-rows.json.gz")
	var cases []struct {
		Name     string               `json:"name"`
		Input    managementQueryInput `json:"input"`
		Model    string               `json:"model"`
		Account  string               `json:"account"`
		Expected []managementQueryRow `json:"expected"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			before := mustJSON(c.Input)
			rows, err := buildManagementQueryRows(c.Input, c.Model, c.Account)
			if err != nil {
				t.Fatal(err)
			}
			if mustJSON(c.Input) != before {
				t.Fatal("query mutated shared input")
			}
			if len(rows) != len(c.Expected) {
				t.Fatalf("rows=%d want=%d", len(rows), len(c.Expected))
			}
			for i, row := range rows {
				if !reflect.DeepEqual(row, c.Expected[i]) {
					t.Errorf("row %s differs\ngot %s\nwant %s", row.ID, mustJSON(row), mustJSON(c.Expected[i]))
				}
			}
		})
	}
}

// Compressed synthetic contracts are generated from the readable browser test
// factory. They keep the full field comparisons without megabytes of repeated
// idle-model placeholders in the repository.
func managementContractFixture(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

package main

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
)

func TestManagementSupplementalEvidenceMatchesBrowserContract(t *testing.T) {
	raw := managementContractFixture(t, "testdata/management-query-evidence.json.gz")
	var cases []struct {
		Input    managementQueryInput     `json:"input"`
		Model    string                   `json:"model"`
		Expected managementFilterEvidence `json:"expected"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			before := mustJSON(c.Input)
			rows, err := buildManagementQueryRows(c.Input, c.Model, "")
			if err != nil {
				t.Fatal(err)
			}
			got := managementQueryEvidence(c.Input, rows, c.Model, []Price{})
			if mustJSON(c.Input) != before {
				t.Fatal("projection modified source evidence")
			}
			if !reflect.DeepEqual(got, c.Expected) {
				t.Fatalf("got %s\nwant %s", mustJSON(got), mustJSON(c.Expected))
			}
		})
	}
}

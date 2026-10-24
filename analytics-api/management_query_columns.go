package main

import (
	"encoding/json"
	"sort"
)

const managementPageV3MediaType = "application/vnd.uni-api.management-page-v3+json"

// A cell has zero elements for an absent property, or one complete JSON value.
// Thus absent, null, zero, false and empty values remain distinct.
type managementResultColumns struct {
	Count   int                   `json:"count"`
	Fields  []string              `json:"fields"`
	Columns [][][]json.RawMessage `json:"columns"`
}
type managementColumnPage struct {
	managementWirePage
	Format       string                  `json:"format"`
	Results      []json.RawMessage       `json:"results,omitempty"`
	ResultFields managementResultColumns `json:"result_fields"`
	Availability managementResultColumns `json:"availability"`
	Quality      managementResultColumns `json:"quality"`
}

func managementColumns(rows []map[string]json.RawMessage) managementResultColumns {
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
func encodeManagementColumnPage(page managementPageResponse) (managementColumnPage, error) {
	wire, err := encodeManagementPage(page)
	if err != nil {
		return managementColumnPage{}, err
	}
	out := managementColumnPage{managementWirePage: wire, Format: "management-page-v3"}
	meta, availability, quality := make([]map[string]json.RawMessage, len(wire.Results)), make([]map[string]json.RawMessage, len(wire.Results)), make([]map[string]json.RawMessage, len(wire.Results))
	for i, raw := range wire.Results {
		if err = json.Unmarshal(raw, &meta[i]); err != nil {
			return out, err
		}
		if err = json.Unmarshal(meta[i]["availability"], &availability[i]); err != nil {
			return out, err
		}
		if err = json.Unmarshal(meta[i]["quality"], &quality[i]); err != nil {
			return out, err
		}
		delete(meta[i], "availability")
		delete(meta[i], "quality")
	}
	out.managementWirePage.Results = nil
	out.ResultFields, out.Availability, out.Quality = managementColumns(meta), managementColumns(availability), managementColumns(quality)
	return out, nil
}

// Only headers/read time change after the stored result is proven current.
// Keep the large immutable evidence columns prepared inside the bounded store.
func currentManagementColumnPage(page managementPageResponse) (managementColumnPage, error) {
	if page.preparedColumns == nil {
		return encodeManagementColumnPage(page)
	}
	wire := *page.preparedColumns
	wire.managementPageResponse = page
	wire.Rows = append([]managementWireRow{}, wire.Rows...)
	for i := range wire.Rows {
		wire.Rows[i].Account = page.Rows[i].Account
	}
	return wire, nil
}

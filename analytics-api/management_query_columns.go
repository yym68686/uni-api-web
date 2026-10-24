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

// Decode the canonical stored representation for older clients. v3 callers
// keep the compact evidence until serialization; no partial row crosses HTTP.
func expandManagementColumnPage(wire managementColumnPage) (managementPageResponse, error) {
	expand := func(c managementResultColumns) []map[string]json.RawMessage {
		rows := make([]map[string]json.RawMessage, c.Count)
		for i := range rows {
			rows[i] = map[string]json.RawMessage{}
		}
		for j, field := range c.Fields {
			for i, cell := range c.Columns[j] {
				if len(cell) == 1 {
					rows[i][field] = cell[0]
				}
			}
		}
		return rows
	}
	meta, a, q := expand(wire.ResultFields), expand(wire.Availability), expand(wire.Quality)
	results := make([]json.RawMessage, len(meta))
	for i, m := range meta {
		m["availability"], _ = json.Marshal(a[i])
		m["quality"], _ = json.Marshal(q[i])
		raw, err := json.Marshal(m)
		if err != nil {
			return managementPageResponse{}, err
		}
		results[i] = raw
	}
	out := wire.managementPageResponse
	out.Format = "management-page-v1"
	ref := func(i *int) (*subResult, error) {
		if i == nil {
			return nil, nil
		}
		r := &subResult{}
		if err := json.Unmarshal(results[*i], r); err != nil {
			return nil, err
		}
		return r, nil
	}
	model := func(m managementWireModel) (subModelResult, error) {
		r, err := ref(m.ResultRef)
		return subModelResult{Model: m.Model, State: m.State, Message: m.Message, Result: r}, err
	}
	check := func(c managementWireCheck) (managementQueryCheck, error) {
		m, err := model(c.managementWireModel)
		return managementQueryCheck{subModelResult: m, SourceName: c.SourceName}, err
	}
	if wire.Rows != nil {
		out.Rows = make([]managementQueryRow, len(wire.Rows))
	}
	for i, w := range wire.Rows {
		row := w.managementQueryRow
		row.Target = w.Target.subTarget
		var err error
		row.Target.Result, err = ref(w.Target.ResultRef)
		if err != nil {
			return out, err
		}
		if w.Target.Models != nil {
			row.Target.Models = make([]subModelResult, len(w.Target.Models))
		}
		for j, m := range w.Target.Models {
			row.Target.Models[j], err = model(m)
			if err != nil {
				return out, err
			}
		}
		if w.Checks != nil {
			row.Checks = make([]managementQueryCheck, len(w.Checks))
		}
		for j, c := range w.Checks {
			row.Checks[j], err = check(c)
			if err != nil {
				return out, err
			}
		}
		if w.Selected != nil {
			c, err := check(*w.Selected)
			if err != nil {
				return out, err
			}
			row.Selected = &c
		}
		out.Rows[i] = row
	}
	return out, nil
}

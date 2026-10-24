package main

import (
	"encoding/json"
	"net/http"
	"strconv"
)

const managementPageV2MediaType = "application/vnd.uni-api.management-page-v2+json"

type managementWireModel struct {
	Model     string `json:"model"`
	State     string `json:"state"`
	Message   string `json:"message"`
	ResultRef *int   `json:"result_ref"`
}
type managementWireCheck struct {
	managementWireModel
	SourceName string `json:"source_name,omitempty"`
}
type managementWireTarget struct {
	subTarget
	// Shadow embedded fields: their exact values are represented by references.
	Result    *subResult            `json:"result,omitempty"`
	ResultRef *int                  `json:"result_ref"`
	Models    []managementWireModel `json:"models"`
}
type managementWireRow struct {
	managementQueryRow
	Target   managementWireTarget  `json:"target"`
	Checks   []managementWireCheck `json:"checks"`
	Selected *managementWireCheck  `json:"selected,omitempty"`
}
type managementWirePage struct {
	managementPageResponse
	Format  string              `json:"format"`
	Rows    []managementWireRow `json:"rows"`
	Results []json.RawMessage   `json:"results"`
}

// Deduplicate only byte-identical complete typed results. No model, diagnostic,
// receipt, revision or filter value is omitted. The client expands references
// before the existing validation/rendering contract sees the page.
func encodeManagementPage(page managementPageResponse) (managementWirePage, error) {
	out := managementWirePage{managementPageResponse: page, Format: "management-page-v2", Results: []json.RawMessage{}}
	byPointer, byValue := map[*subResult]int{}, map[string]int{}
	resultRef := func(result *subResult) (*int, error) {
		if result == nil {
			return nil, nil
		}
		if i, ok := byPointer[result]; ok {
			return &i, nil
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		key := string(raw)
		i, ok := byValue[key]
		if !ok {
			i = len(out.Results)
			byValue[key] = i
			out.Results = append(out.Results, raw)
		}
		byPointer[result] = i
		return &i, nil
	}
	model := func(in subModelResult) (managementWireModel, error) {
		ref, err := resultRef(in.Result)
		return managementWireModel{Model: in.Model, State: in.State, Message: in.Message, ResultRef: ref}, err
	}
	check := func(in managementQueryCheck) (managementWireCheck, error) {
		m, err := model(in.subModelResult)
		return managementWireCheck{managementWireModel: m, SourceName: in.SourceName}, err
	}
	if page.Rows != nil {
		out.Rows = make([]managementWireRow, len(page.Rows))
	}
	for i, row := range page.Rows {
		wire := managementWireRow{managementQueryRow: row, Target: managementWireTarget{subTarget: row.Target}}
		var err error
		wire.Target.ResultRef, err = resultRef(row.Target.Result)
		if err != nil {
			return out, err
		}
		if row.Target.Models != nil {
			wire.Target.Models = make([]managementWireModel, len(row.Target.Models))
		}
		for j, m := range row.Target.Models {
			wire.Target.Models[j], err = model(m)
			if err != nil {
				return out, err
			}
		}
		if row.Checks != nil {
			wire.Checks = make([]managementWireCheck, len(row.Checks))
		}
		for j, c := range row.Checks {
			wire.Checks[j], err = check(c)
			if err != nil {
				return out, err
			}
		}
		if row.Selected != nil {
			selected, err := check(*row.Selected)
			if err != nil {
				return out, err
			}
			wire.Selected = &selected
		}
		out.Rows[i] = wire
	}
	return out, nil
}

func writeManagementPage(w http.ResponseWriter, r *http.Request, page managementPageResponse) error {
	w.Header().Add("Vary", "Accept")
	if r.Header.Get("Accept") == managementPageV3MediaType {
		if r.Header.Get("X-Console-Current-Validation") == "1" {
			// ReadAt is dynamic response metadata. The representation remains
			// byte-identical while every delivered header attests THIS read.
			w.Header().Set("X-Console-Read-At-Ms", strconv.FormatInt(page.ReadAtMS, 10))
			w.Header().Set("X-Console-Validated-Request-ID", consoleRequestID(r.Context()))
			page.ReadAtMS = 0
		}
		wire, err := currentManagementColumnPage(page)
		if err != nil {
			http.Error(w, "渠道响应编码失败", http.StatusInternalServerError)
			return err
		}
		return writeManagementJSONMedia(w, r, wire, managementPageV3MediaType)
	}
	if r.Header.Get("Accept") != managementPageV2MediaType {
		return writeManagementJSON(w, r, page)
	}
	wire, err := encodeManagementPage(page)
	if err != nil {
		http.Error(w, "渠道响应编码失败", http.StatusInternalServerError)
		return err
	}
	return writeManagementJSONMedia(w, r, wire, managementPageV2MediaType)
}

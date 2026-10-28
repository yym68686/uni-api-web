package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// Independent previous parser: first copy both complete probe JSON values into
// a top-level RawMessage map, then parse each probe for a second time.
func previousManagementColumnPageParser(page managementPageResponse) (managementColumnPage, error) {
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

func TestManagementColumnParserCoversEveryTopLevelResultField(t *testing.T) {
	fields := func(value any) map[string]string {
		out := map[string]string{}
		v := reflect.TypeOf(value)
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if field.Anonymous || field.PkgPath != "" {
				t.Fatal("column result schema needs review", field.Name)
			}
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				t.Fatal("column result schema needs review", field.Name)
			}
			out[name] = field.Tag.Get("json")
		}
		return out
	}
	if !reflect.DeepEqual(fields(subResult{}), fields(managementColumnResultParts{})) {
		t.Fatal("result schema changed: preserve every new field in the column parser before release")
	}
}

func TestManagementColumnParserPreservesRawValuesAndReferences(t *testing.T) {
	rng := rand.New(rand.NewSource(22))
	cases := []managementPageResponse{managementWireFixture(), {}, {managementQueryPage: managementQueryPage{Rows: []managementQueryRow{}}}}
	zero := managementWireFixture()
	zero.Rows = []managementQueryRow{{Target: subTarget{Result: &subResult{}, Models: []subModelResult{{Result: &subResult{}}}}}}
	cases = append(cases, zero)
	for i := 0; i < 150; i++ {
		page := managementWireFixture()
		page.Rows = nil
		for g := 0; g < 1+rng.Intn(4); g++ {
			row := managementQueryRow{ID: fmt.Sprint(g), Target: subTarget{GroupID: int64(g)}}
			for m := 0; m < 1+rng.Intn(8); m++ {
				r := &subResult{Model: fmt.Sprintf("模型🙂<>&\u2028-\xff-%d", m), CheckedAt: rng.Int63(), Verdict: "pass"}
				for _, probe := range []*subProbe{&r.Availability, &r.Quality} {
					*probe = subProbe{CurlToken: "opaque", ID: "fixture-id", StartedAt: -1, RequestIDs: []string{}, RequestedModel: "alias", ResponseModel: r.Model, ModelMatch: "missing", Status: "success", TerminalStatus: "missing", Text: "\x00\"\\模型", Message: "complete diagnostic", TTFT: usageInt(0), ResponseCreatedMS: usageInt(-1), FirstResponseMS: usageInt(1), Protocol: "responses", Duration: math.MaxInt64, HTTPStatus: 200}
					if rng.Intn(3) == 0 {
						probe.RequestIDs = nil
						probe.TTFT = nil
						probe.Text = ""
						probe.Message = ""
					}
					if rng.Intn(3) > 0 {
						probe.Usage = &subUsage{Status: "matched", Message: "原始账单", LogID: 1, RequestID: "receipt", CheckedAt: 2, CreatedAt: "2026-10-28T00:00:00Z", ActualCost: usageFloat(0), TotalCost: usageFloat(1.23456789), Rate: usageFloat(-1), InputTokens: usageInt(math.MaxInt64), OutputTokens: usageInt(-1), CacheReadTokens: usageInt(0), CacheWriteTokens: usageInt(3), InputPrice: usageFloat(1e-100), OutputPrice: usageFloat(1e100), CacheReadPrice: usageFloat(0), CacheWritePrice: usageFloat(3), PaidInputPrice: usageFloat(4), PaidOutputPrice: usageFloat(5), DurationMS: usageInt(6), FirstTokenMS: usageInt(7)}
					}
				}
				if rng.Intn(8) == 0 {
					r = nil
				}
				model := subModelResult{Model: fmt.Sprint(m), State: "done", Result: r}
				row.Target.Models = append(row.Target.Models, model)
				row.Checks = append(row.Checks, managementQueryCheck{subModelResult: model, SourceName: "fixture"})
				if r != nil && m%2 == 0 {
					same := *r
					model.Result = &same
					row.Checks = append(row.Checks, managementQueryCheck{subModelResult: model})
				}
			}
			row.Target.Result = row.Target.Models[0].Result
			row.Selected = &row.Checks[0]
			page.Rows = append(page.Rows, row)
		}
		cases = append(cases, page)
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		page := managementWireFixture()
		page.Rows[0].Target.Result.Availability.Usage.InputPrice = usageFloat(value)
		cases = append(cases, page)
	}
	for i, page := range cases {
		before := mustJSON(page)
		want, wantErr := previousManagementColumnPageParser(page)
		got, gotErr := encodeManagementColumnPage(page)
		if fmt.Sprint(wantErr) != fmt.Sprint(gotErr) || !reflect.DeepEqual(want, got) {
			t.Fatalf("result structure or error differs for case %d", i)
		}
		wantJSON, wantJSONErr := json.Marshal(want)
		gotJSON, gotJSONErr := json.Marshal(got)
		if string(wantJSON) != string(gotJSON) || fmt.Sprint(wantJSONErr) != fmt.Sprint(gotJSONErr) || mustJSON(page) != before {
			t.Fatalf("wire bytes, error or input differs for case %d", i)
		}
	}
}

func BenchmarkManagementColumnPageParsing(b *testing.B) {
	page := managementWireFixture()
	page.Rows = nil
	for g := 0; g < 25; g++ {
		row := managementQueryRow{ID: fmt.Sprint(g), Target: subTarget{GroupID: int64(g)}}
		for m := 0; m < 288; m++ {
			r := &subResult{Model: fmt.Sprint("model-", m), CheckedAt: int64(g*288 + m), Verdict: "pass", Availability: subProbe{Status: "success", Message: "current complete diagnostic", Usage: &subUsage{Status: "matched", InputPrice: usageFloat(2)}}, Quality: subProbe{Status: "skipped"}}
			model := subModelResult{Model: r.Model, State: "done", Result: r}
			row.Target.Models = append(row.Target.Models, model)
			row.Checks = append(row.Checks, managementQueryCheck{subModelResult: model})
		}
		page.Rows = append(page.Rows, row)
	}
	for _, entry := range []struct {
		name   string
		encode func(managementPageResponse) (managementColumnPage, error)
	}{{"previous_three_parses", previousManagementColumnPageParser}, {"single_parse", encodeManagementColumnPage}} {
		b.Run(entry.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				out, err := entry.encode(page)
				if err != nil || out.Availability.Count != 7200 {
					b.Fatal("incomplete result", err)
				}
			}
		})
	}
}

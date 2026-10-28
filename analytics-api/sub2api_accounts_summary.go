package main

import "unicode/utf8"

// Models dominate account summaries (over 100,000 rows for some owners).
// Project them directly into the existing wire shape instead of marshaling
// detail structs into maps and recursively walking those maps. Target metadata
// still uses the general compactor, including shared quality-cache objects.
func compactAccountTarget(target subTarget) any {
	models := target.Models
	target.Models = nil
	out := compactSummary(target).(map[string]any)
	if models == nil {
		return out
	}
	projected := make([]subModelSummary, len(models))
	for i, model := range models {
		projected[i] = subModelSummary{Model: model.Model, State: model.State, Message: summaryMessage(model.Message)}
		if r := model.Result; r != nil {
			projected[i].Result = &subResultSummary{Model: r.Model, CheckedAt: r.CheckedAt, Verdict: r.Verdict,
				Availability: summarizeSubProbe(r.Availability), Quality: summarizeSubProbe(r.Quality)}
		}
	}
	out["models"] = projected
	return out
}

func summaryMessage(message string) string {
	if len(message) <= 160 {
		return message
	}
	count := 0
	for offset := range message {
		if count == 160 {
			prefix := message[:offset]
			// Converting the original to []rune replaced invalid UTF-8 in a
			// truncated prefix. Keep that exact behavior, without traversing
			// or allocating storage for the discarded suffix.
			if !utf8.ValidString(prefix) {
				prefix = string([]rune(prefix))
			}
			return prefix + "…"
		}
		count++
	}
	return message
}

type subModelSummary struct {
	Model   string            `json:"model"`
	State   string            `json:"state"`
	Message string            `json:"message"`
	Result  *subResultSummary `json:"result"`
}

type subResultSummary struct {
	Model        string          `json:"model"`
	CheckedAt    int64           `json:"checked_at"`
	Availability subProbeSummary `json:"availability"`
	Quality      subProbeSummary `json:"quality"`
	Verdict      string          `json:"verdict"`
}

type subProbeSummary struct {
	Usage             *subUsageSummary `json:"usage,omitempty"`
	RequestedModel    string           `json:"requested_model,omitempty"`
	ResponseModel     string           `json:"response_model,omitempty"`
	ModelMatch        string           `json:"model_match,omitempty"`
	Status            string           `json:"status"`
	TerminalStatus    string           `json:"terminal_status,omitempty"`
	Message           string           `json:"message,omitempty"`
	TTFT              *int64           `json:"ttft_ms"`
	ResponseCreatedMS *int64           `json:"response_created_ms"`
	FirstResponseMS   *int64           `json:"first_response_ms,omitempty"`
	Protocol          string           `json:"protocol,omitempty"`
	Duration          int64            `json:"duration_ms"`
	HTTPStatus        int              `json:"http_status,omitempty"`
}

type subUsageSummary struct {
	Status       string   `json:"status"`
	InputPrice   *float64 `json:"input_price"`
	OutputPrice  *float64 `json:"output_price"`
	InputTokens  *int64   `json:"input_tokens"`
	OutputTokens *int64   `json:"output_tokens"`
}

func summarizeSubProbe(p subProbe) subProbeSummary {
	s := subProbeSummary{RequestedModel: p.RequestedModel, ResponseModel: p.ResponseModel, ModelMatch: p.ModelMatch,
		Status: p.Status, TerminalStatus: p.TerminalStatus, Message: summaryMessage(p.Message), TTFT: p.TTFT,
		ResponseCreatedMS: p.ResponseCreatedMS, FirstResponseMS: p.FirstResponseMS, Protocol: p.Protocol,
		Duration: p.Duration, HTTPStatus: p.HTTPStatus}
	if u := p.Usage; u != nil {
		s.Usage = &subUsageSummary{Status: u.Status, InputPrice: u.InputPrice, OutputPrice: u.OutputPrice, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens}
	}
	return s
}

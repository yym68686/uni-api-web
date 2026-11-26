package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

func subTierModel(model string) bool {
	return subCapabilityModel(model) && subModelProtocol(model) == "responses"
}

// Site probes describe the current test credential only. They can inform a
// channel import preference, but cannot verify a separate business key.
type subTierProbe struct {
	Status    string `json:"status"`
	Observed  string `json:"observed,omitempty"`
	CheckedAt int64  `json:"checked_at"`
}

func probeSiteTier(ctx context.Context, client *http.Client, base, key, model, tier string) subTierProbe {
	out := subTierProbe{Status: "inconclusive", CheckedAt: time.Now().Unix()}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	body := mustJSON(map[string]any{"model": model, "input": []map[string]string{{"role": "user", "content": "Reply OK."}},
		"instructions": "", "service_tier": tier, "store": false, "stream": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/responses", strings.NewReader(body))
	if err != nil {
		return out
	}
	req.GetBody = nil
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	req.Header.Set("X-Request-ID", "site-tier-"+randomID())
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirect.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	const budget = 256 << 10
	raw, err := io.ReadAll(io.LimitReader(resp.Body, budget+1))
	if err != nil || len(raw) > budget {
		return out
	}
	if resp.StatusCode == 400 || resp.StatusCode == 422 {
		var denial struct {
			Error struct{ Param, Code, Message string } `json:"error"`
		}
		if json.Unmarshal(raw, &denial) == nil && denial.Error.Param == "service_tier" {
			switch denial.Error.Code {
			case "unsupported_service_tier", "service_tier_not_supported", "service_tier_not_allowed":
				out.Status = "unsupported"
			default:
				if strings.Contains(denial.Error.Message, "The requested service tier is not allowed for this project.") {
					out.Status = "unsupported"
				}
			}
		}
		return out
	}
	if resp.StatusCode != 200 {
		return out
	}
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		raw = siteTierCompletedEvent(raw)
	}
	var completed struct {
		ID, Model, Status string
		Tier              string            `json:"service_tier"`
		Output            []json.RawMessage `json:"output"`
		Error             json.RawMessage   `json:"error"`
	}
	if json.Unmarshal(raw, &completed) != nil || completed.Status != "completed" || completed.Model != model ||
		completed.ID == "" || completed.Output == nil || len(completed.Error) > 0 && string(completed.Error) != "null" {
		return out
	}
	switch completed.Tier {
	case "fast", "priority", "ultrafast", "default", "flex", "scale":
		out.Observed, out.Status = completed.Tier, "not_honored"
		if completed.Tier == tier || tier == "fast" && completed.Tier == "priority" {
			out.Status = "supported"
		}
	}
	return out
}

func siteTierCompletedEvent(raw []byte) json.RawMessage {
	reader := bufio.NewScanner(bytes.NewReader(raw))
	reader.Buffer(make([]byte, 4096), 256<<10)
	var completed json.RawMessage
	var data strings.Builder
	eventName := ""
	flush := func() bool {
		var event struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
		}
		if data.Len() != 0 {
			_ = json.Unmarshal([]byte(data.String()), &event)
		}
		data.Reset()
		if event.Type == "response.failed" || event.Type == "response.incomplete" || event.Type == "error" ||
			eventName == "response.failed" || eventName == "response.incomplete" || eventName == "error" {
			return false
		}
		eventName = ""
		if event.Type == "response.completed" {
			if completed != nil {
				return false
			}
			completed = event.Response
		}
		return true
	}
	for reader.Scan() {
		line := reader.Text()
		if line == "" {
			if !flush() {
				return nil
			}
		} else if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(line[len("event:"):])
		} else if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimPrefix(line[len("data:"):], " "))
			data.WriteByte('\n')
		}
	}
	if reader.Err() != nil || !flush() {
		return nil
	}
	return completed
}

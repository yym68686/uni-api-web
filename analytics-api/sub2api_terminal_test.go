package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponsesTerminalWarnings(t *testing.T) {
	created := "data: {\"type\":\"response.created\",\"response\":{\"model\":\"gpt-6-luna\"}}\n\n"
	delta := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"test\"}\n\n"
	item := "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"test\"}]}}\n\n"
	emptyCompleted := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-luna\",\"output\":[]}}\n\n"
	for _, tc := range []struct{ name, body, status, terminal, text, match string }{
		{"clean EOF after text", created + delta, "success", "missing", "test", "match"},
		{"DONE after text", created + delta + "data: [DONE]\n\n", "success", "missing", "test", "match"},
		{"no model metadata", delta, "success", "missing", "test", "missing"},
		{"completed item without terminal", created + item, "success", "missing", "test", "match"},
		{"oaix empty terminal output", created + delta + item + emptyCompleted, "success", "missing_output", "test", "match"},
		{"empty terminal after deltas", created + delta + emptyCompleted, "success", "missing_output", "test", "match"},
		{"duplicate item", created + delta + item + item + emptyCompleted, "success", "missing_output", "test", "match"},
		{"ordered items", created + strings.ReplaceAll(strings.ReplaceAll(item, `"output_index":0`, `"output_index":1`), `"text":"test"`, `"text":" second"`) + item + emptyCompleted, "success", "missing_output", "test second", "match"},
		{"complete authoritative output", created + delta + strings.ReplaceAll(emptyCompleted, `"output":[]`, `"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"final"}]}]`), "success", "complete", "final", "match"},
		{"refusal is not valid text", created + delta + strings.ReplaceAll(emptyCompleted, `"output":[]`, `"output":[{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"no"}]}]`), "error", "", "test", "unavailable"},
		{"empty 200", "", "error", "", "", "unavailable"},
		{"created only", created, "error", "", "", "unavailable"},
		{"empty completed", created + emptyCompleted, "error", "missing_output", "", "unavailable"},
		{"explicit error", created + delta + "data: {\"type\":\"error\"}\n\n", "error", "", "test", "unavailable"},
		{"failed after text", created + delta + "data: {\"type\":\"response.failed\"}\n\n", "error", "", "test", "unavailable"},
		{"incomplete after text", created + delta + "data: {\"type\":\"response.incomplete\"}\n\n", "error", "", "test", "unavailable"},
		{"invalid completed status", created + delta + strings.ReplaceAll(emptyCompleted, `"status":"completed"`, `"status":"failed"`), "error", "", "test", "unavailable"},
		{"malformed event", created + delta + "data: nope\n\n", "error", "", "test", "unavailable"},
		{"oversized line", created + delta + "data: " + strings.Repeat("x", 600<<10), "error", "", "test", "unavailable"},
		{"body limit", created + delta + strings.Repeat(": heartbeat\n\n", 200000), "error", "", "test", "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			got := subProbeStream(context.Background(), server.Client(), server.URL, "fixture-key", "say test", "gpt-6-luna")
			if got.Status != tc.status || got.TerminalStatus != tc.terminal || got.Text != tc.text || got.ModelMatch != tc.match {
				t.Fatalf("got %+v", got)
			}
			if got.Status == "success" && got.TerminalStatus != "complete" && !strings.HasPrefix(got.Message, "成功，但") {
				t.Fatalf("warning lost: %+v", got)
			}
		})
	}
}

type terminalFailureTransport struct{ body io.Reader }

func (t terminalFailureTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(t.body)}, nil
}

type terminalErrorReader struct{}

func (terminalErrorReader) Read([]byte) (int, error) { return 0, errors.New("private upstream error") }
func TestResponsesReadErrorDoesNotBecomeSuccess(t *testing.T) {
	text := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"test\"}\n\n"
	client := &http.Client{Transport: terminalFailureTransport{io.MultiReader(strings.NewReader(text), terminalErrorReader{})}}
	got := subProbeStream(context.Background(), client, "https://fixture.test", "secret", "say test")
	if got.Status != "error" || got.TerminalStatus != "" || got.Text != "test" || strings.Contains(got.Message, "private") {
		t.Fatalf("got %+v", got)
	}
}
func TestMissingCompletionDoesNotProduceQualityVerdict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"21\"}\n\n")
	}))
	defer server.Close()
	got := subRunQualityProbe(context.Background(), server.Client(), server.URL, "fixture-key")
	if got.Availability.Status != "success" || got.Availability.TerminalStatus != "missing" || got.Quality.Status != "error" || got.Verdict != "error" {
		t.Fatalf("got %+v", got)
	}
}

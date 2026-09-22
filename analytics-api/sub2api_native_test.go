package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func subNativeSSE(w http.ResponseWriter, model, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	if strings.HasPrefix(model, "gemini-") {
		fmt.Fprintf(w, "data: {\"modelVersion\":%q,\"responseId\":\"native-response\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":%q}]},\"finishReason\":\"STOP\"}]}\n\n", model, text)
		return
	}
	fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"native-response\",\"role\":\"assistant\",\"model\":%q}}\n\n", model)
	fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
	fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", text)
	fmt.Fprint(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
}

func subFixtureProbeModel(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/v1beta/models/") {
		return strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1beta/models/"), ":streamGenerateContent")
	}
	var body struct {
		Model string `json:"model"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	return body.Model
}

func TestSubAllModelsUseRequestedProtocol(t *testing.T) {
	expected := []string{"gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5", "codex-auto-review", "glm-5.3", "glm-5.3-flash", "kimi-k3", "deepseek-4.1-flash", "deepseek-4-pro", "grok-4.6", "gemini-3.1-pro", "gemini-3.8-flash", "claude-fable-5", "claude-fable-5-1", "claude-opus-5-5", "claude-opus-5", "claude-sonnet-5", "claude-opus-4-8", "claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5-20251001"}
	if len(subModels) != len(expected) {
		t.Fatal("unexpected model count", len(subModels))
	}
	for _, model := range expected {
		t.Run(model, func(t *testing.T) {
			if !subModelAllowed(model) {
				t.Fatal("model rejected")
			}
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.Header.Get("X-Request-ID") == "" {
					t.Error("missing method/request correlation")
				}
				var body struct {
					Model     string `json:"model"`
					Stream    bool   `json:"stream"`
					MaxTokens int    `json:"max_tokens"`
					Input     []struct {
						Content string `json:"content"`
					} `json:"input"`
					Messages []struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					} `json:"messages"`
					Contents []struct {
						Role  string `json:"role"`
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"contents"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("X-Request-ID", "upstream-request")
				switch {
				case strings.HasPrefix(model, "gemini-"):
					if r.URL.Path != "/v1beta/models/"+model+":streamGenerateContent" || r.URL.Query().Get("alt") != "sse" || r.Header.Get("x-goog-api-key") != "private-key" || body.Model != "" || len(body.Contents) != 1 || len(body.Contents[0].Parts) != 1 || body.Contents[0].Parts[0].Text != "say test" {
						t.Error("wrong Gemini request", r.URL.Path)
					}
					subNativeSSE(w, model, "test")
				case strings.HasPrefix(model, "claude-"):
					if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "private-key" || r.Header.Get("anthropic-version") != "2023-06-01" || body.Model != model || !body.Stream || body.MaxTokens <= 0 || len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != "say test" {
						t.Error("wrong Messages request", r.URL.Path)
					}
					subNativeSSE(w, model, "test")
				default:
					if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer private-key" || body.Model != model || !body.Stream || len(body.Input) != 1 || (body.Input[0].Content != "say test" && !(model == checkModel && body.Input[0].Content == checkPrompt)) {
						t.Error("wrong Responses request", r.URL.Path)
					}
					subSSE(w, "21", model)
				}
			}))
			defer up.Close()
			result := subRunProbes(context.Background(), up.Client(), up.URL, "private-key", model)
			if result.Model != model || result.Availability.Status != "success" || result.Availability.ModelMatch != "match" || result.Availability.ResponseModel != model {
				t.Fatalf("bad result %+v", result)
			}
			expectedCalls := 1
			if model == checkModel {
				expectedCalls = 2
			} else if result.Quality.Status != "not_applicable" {
				t.Fatal("non Astra quality check", result)
			}
			if calls != expectedCalls {
				t.Fatal("unexpected paid request count", calls)
			}
			if !strings.Contains(strings.Join(result.Availability.RequestIDs, ","), "upstream-request") {
				t.Fatal("missing receipt correlation")
			}
		})
	}
}

func TestSubNativeCompletionAndTextTiming(t *testing.T) {
	for _, protocol := range []string{"gemini", "messages"} {
		for _, mode := range []string{"success", "truncated", "failure", "reasoning-only", "blocked", "invalid", "http", "json", "overlong", "missing-model", "wrong-model"} {
			t.Run(protocol+"/"+mode, func(t *testing.T) {
				model := "gemini-3.1-pro"
				if protocol == "messages" {
					model = "claude-opus-5"
				}
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if mode == "http" {
						http.Error(w, "private-key", 403)
						return
					}
					if mode == "json" {
						writeJSON(w, 200, map[string]string{"text": "test"})
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if mode == "invalid" {
						fmt.Fprint(w, "data: {\n\n")
						return
					}
					if mode == "blocked" {
						fmt.Fprint(w, "event: error\ndata: {\"error\":{\"message\":\"private-key\"}}\n\n")
						return
					}
					returned := model
					if mode == "missing-model" {
						returned = ""
					}
					if mode == "wrong-model" {
						returned = "different-model"
					}
					if protocol == "gemini" {
						fmt.Fprintf(w, "data: {\"modelVersion\":%q,\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"thinking\",\"thought\":true}]}}]}\n\n", returned)
					} else {
						fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"role\":\"assistant\",\"model\":%q}}\n\n", returned)
						fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"thinking\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
					}
					w.(http.Flusher).Flush()
					time.Sleep(35 * time.Millisecond)
					text := "test"
					if mode == "reasoning-only" {
						text = ""
					}
					if mode == "overlong" {
						text = strings.Repeat("x", 8193)
					}
					if protocol == "gemini" {
						finish := "STOP"
						if mode == "truncated" {
							finish = ""
						}
						if mode == "failure" {
							finish = "MAX_TOKENS"
						}
						fmt.Fprintf(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":%q}]},\"finishReason\":%q}]}\n\n", text, finish)
					} else {
						fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
						fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", text)
						fmt.Fprint(w, "data: {\"type\":\"content_block_stop\",\"index\":1}\n\n")
						reason := "end_turn"
						if mode == "failure" {
							reason = "max_tokens"
						}
						fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%q}}\n\n", reason)
						if mode != "truncated" {
							fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
						}
					}
				}))
				defer up.Close()
				result := subRunProbes(context.Background(), up.Client(), up.URL, "private-key", model).Availability
				success := mode == "success" || mode == "missing-model" || mode == "wrong-model"
				if (result.Status == "success") != success {
					t.Fatalf("unexpected result %+v", result)
				}
				if success && (result.FirstResponseMS == nil || result.TTFT == nil || *result.TTFT-*result.FirstResponseMS < 30 || result.ResponseCreatedMS != nil || result.Text != "test") {
					t.Fatalf("invalid native timing %+v", result)
				}
				if mode == "reasoning-only" && result.TTFT != nil {
					t.Fatal("thinking counted as text")
				}
				if mode == "missing-model" && result.ModelMatch != "missing" {
					t.Fatal(result.ModelMatch)
				}
				if mode == "wrong-model" && result.ModelMatch != "mismatch" {
					t.Fatal(result.ModelMatch)
				}
				if strings.Contains(result.Message, "private-key") {
					t.Fatal("key leaked")
				}
			})
		}
	}
}

package main

import (
	"strconv"
	"strings"
)

// Completion evidence can demote a reported success, never promote a failure.
// Legacy facts without termination evidence retain their recorded outcome:
// absence of a historical field does not prove absence of an upstream event.
func factMetricOutcome(f Fact) string {
	if f.Kind != "attempt" && f.Kind != "request" {
		return f.Outcome
	}
	switch f.Outcome {
	case "cancelled", "client_cancelled", "hedge_cancelled", "skipped":
		return f.Outcome
	}
	if f.Outcome == "incomplete" || f.TerminalKind == "incomplete" {
		return "failed"
	}
	if f.Stream && f.Endpoint == "/v1/responses" {
		if (f.ResponseCompleted != nil && !*f.ResponseCompleted) ||
			(f.TerminalKind != "" && f.TerminalKind != "completed") {
			return "failed"
		}
	}
	return f.Outcome
}

func knownFailureReason(reason string) bool {
	switch reason {
	case "responses_max_output_tokens", "responses_content_filter", "responses_incomplete", "missing_response_completed", "protocol_error", "transport_error", "upstream_response_failed", "upstream_http_error", "downstream_disconnected", "no_successful_channel", "other":
		return true
	}
	if strings.HasPrefix(reason, "upstream_http_") {
		n, err := strconv.Atoi(strings.TrimPrefix(reason, "upstream_http_"))
		return err == nil && n >= 400 && n <= 599
	}
	return false
}

func factFailureReason(f Fact) string {
	if f.Kind != "attempt" && f.Kind != "request" {
		return ""
	}
	switch factMetricOutcome(f) {
	case "success", "completed", "cancelled", "client_cancelled", "hedge_cancelled", "skipped":
		return ""
	}
	if f.Outcome == "incomplete" {
		return "responses_incomplete"
	}
	if knownFailureReason(f.FailureReason) {
		return f.FailureReason
	}
	switch f.TerminalKind {
	case "incomplete":
		return "responses_incomplete"
	case "protocol_error":
		return "protocol_error"
	case "transport_error":
		return "transport_error"
	case "semantic_failure", "semantic_error":
		return "upstream_response_failed"
	case "downstream_disconnected":
		return "downstream_disconnected"
	case "http_error":
		if f.Status >= 400 && f.Status <= 599 {
			return "upstream_http_" + strconv.Itoa(f.Status)
		}
		return "upstream_http_error"
	}
	if f.Stream && f.Endpoint == "/v1/responses" && f.ResponseCompleted != nil && !*f.ResponseCompleted {
		return "missing_response_completed"
	}
	return "" // Legacy records have no reason; do not invent one from token counts.
}

func failureReasonKey(outcome string) string {
	if outcome == "incomplete" {
		return "responses_incomplete"
	}
	if reason, ok := strings.CutPrefix(outcome, "failed/"); ok && knownFailureReason(reason) {
		return reason
	}
	return "unknown"
}

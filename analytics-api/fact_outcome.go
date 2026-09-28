package main

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

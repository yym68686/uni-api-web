package main

import (
	"bytes"
	"context"
	"log"
	"reflect"
	"strings"
	"testing"
)

func TestManagementStreamStagesPreserveReadAndDoNotExposeValues(t *testing.T) {
	s := managementHydrationFixture(t)
	for _, scope := range []managementStreamScope{
		{},
		{Model: "gpt-5.5", Names: []string{"gpt-5.5", "gpt-6-astra"}},
		{CatalogOnly: true},
	} {
		want, revision, err := readManagementScopedStreams(context.Background(), s.control.db, "owner", true, scope)
		if err != nil {
			t.Fatal(err)
		}
		var logs bytes.Buffer
		previous := log.Writer()
		func() {
			log.SetOutput(&logs)
			defer log.SetOutput(previous)
			ctx := context.WithValue(managementTraceContext(context.Background()), consoleRequestIDKey{}, "stream-trace-123")
			got, actual, err := readManagementScopedStreams(ctx, s.control.db, "owner", true, scope)
			if err != nil || actual != revision || !reflect.DeepEqual(got, want) {
				t.Fatal("timing changed current stream result", err)
			}
		}()
		for _, stage := range []string{"signal_model_directory", "signal_model_scope", "signal_model_order", "signal_model_expand"} {
			if !strings.Contains(logs.String(), "stage="+stage+" ") {
				t.Fatal("missing fixed stream stage", stage, logs.String())
			}
		}
		if !scope.CatalogOnly {
			for _, stage := range []string{"signal_model_predicates", "signal_model_verify"} {
				if !strings.Contains(logs.String(), "stage="+stage+" ") {
					t.Fatal("missing fixed predicate stage", stage, logs.String())
				}
			}
		}
		if !strings.Contains(logs.String(), "request_id=stream-trace-123") {
			t.Fatal("stream timing lost request correlation")
		}
		for _, private := range []string{"owner", "gpt-5.5", "gpt-6-astra", "extra-model", "model message", "current diagnostic", "a-1-", "q-1-", "a.test"} {
			if strings.Contains(logs.String(), private) {
				t.Fatal("stream timing exposed a private value", private)
			}
		}
	}
}

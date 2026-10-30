package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

type managementTraceBrokenBody struct{ read bool }

func (b *managementTraceBrokenBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, io.ErrUnexpectedEOF
	}
	b.read = true
	return copy(p, `{"private":"unfinished`), nil
}
func (*managementTraceBrokenBody) Close() error { return nil }

func TestManagementGatewayReadTracePreservesHTTPAndDecodeBehavior(t *testing.T) {
	previousLog, previousHTTP := log.Writer(), sourceHTTP
	t.Cleanup(func() { log.SetOutput(previousLog); sourceHTTP = previousHTTP })
	source := controlSource{sourceView: sourceView{Base: "https://private-host"}, Key: "private-key"}
	for _, test := range []struct {
		name, body, phase string
		status, code      int
		budget            string
		broken, canceled  bool
	}{
		{name: "object", body: `{"private":"value","number":1,"nested":[null,{}]}`, status: 200, code: 200, phase: "complete"},
		{name: "null", body: `null`, status: 200, code: 200, phase: "complete"},
		{name: "array", body: `[]`, status: 200, code: 502, phase: "decode"},
		{name: "malformed", body: `{"private":`, status: 200, code: 502, phase: "decode"},
		{name: "budget", body: `{"private":"too large"}`, budget: "5", status: 200, code: 502, phase: "body"},
		{name: "body-error", broken: true, status: 200, code: 502, phase: "body"},
		{name: "not-found", body: `private error`, status: 404, code: 404, phase: "status"},
		{name: "unauthorized", body: `private error`, status: 401, code: 403, phase: "status"},
		{name: "forbidden", body: `private error`, status: 403, code: 403, phase: "status"},
		{name: "conflict", body: `private error`, status: 409, code: 409, phase: "status"},
		{name: "bad-request", body: `{"error":{"message":"Invalid retained configuration"}}`, status: 400, code: 400, phase: "status"},
		{name: "unavailable", body: `private error`, status: 503, code: 502, phase: "status"},
		{name: "transport-error", code: 502, phase: "transport"},
		{name: "canceled", canceled: true, code: 502, phase: "transport"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CONFIGURATION_MAX_BYTES", test.budget)
			var expected map[string]any
			var expectedError string
			for _, traced := range []bool{false, true} {
				var output bytes.Buffer
				log.SetOutput(&output)
				ctx := context.Background()
				if traced {
					ctx = managementTraceContext(ctx)
				}
				ctx = context.WithValue(ctx, consoleRequestIDKey{}, "gateway-read-123")
				if test.canceled {
					canceled, cancel := context.WithCancel(ctx)
					cancel()
					ctx = canceled
				}
				calls, closes := 0, 0
				sourceHTTP = &http.Client{Transport: sourceDiagnosticRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Method != http.MethodGet || r.URL.Host != "private-host" || r.URL.Path != "/v1/channel-settings/management-snapshot" || r.Header.Get("Authorization") != "Bearer private-key" || r.Header.Get("Accept") != "application/json" {
						t.Error("request identity or method changed")
					}
					if test.status == 0 {
						if test.canceled {
							return nil, r.Context().Err()
						}
						return nil, errors.New("private transport error")
					}
					var reader io.Reader = strings.NewReader(test.body)
					if test.broken {
						reader = &managementTraceBrokenBody{}
					}
					return &http.Response{StatusCode: test.status, Body: &managementTraceCloseBody{Reader: reader, close: func() { closes++ }}, Header: make(http.Header)}, nil
				})}
				data, code, err := subGateway(ctx, source, http.MethodGet, "/v1/channel-settings/management-snapshot", nil)
				message := ""
				if err != nil {
					message = err.Error()
				}
				if code != test.code || calls != 1 || test.status != 0 && closes != 1 {
					t.Fatal("HTTP, retry or body-close behavior changed", code, calls, closes)
				}
				if !traced {
					expected, expectedError = data, message
					if output.Len() != 0 {
						t.Fatal("ordinary reader emitted management diagnostics")
					}
					continue
				}
				if !reflect.DeepEqual(data, expected) || message != expectedError || code != 200 && err == nil {
					t.Fatal("tracing changed the original result or public error")
				}
				logged := output.String()
				for _, marker := range []string{"management_gateway_read document=snapshot phase=" + test.phase, "transport_ms=", "body_ms=", "decode_ms=", "bytes=", "elapsed_ms=", "request_id=gateway-read-123"} {
					if !strings.Contains(logged, marker) {
						t.Fatal("missing safe phase metric", marker)
					}
				}
				if strings.Count(logged, "management_gateway_read ") != 1 || strings.Contains(logged, "private") || strings.Contains(logged, "https://") || strings.Contains(logged, "/v1/") {
					t.Fatal("duplicate diagnostic or private detail escaped")
				}
				if test.canceled && (!errors.Is(err, context.Canceled) || !strings.Contains(logged, "canceled=true")) {
					t.Fatal("original cancellation was lost")
				}
			}
		})
	}
}

type managementTraceCloseBody struct {
	io.Reader
	close func()
}

func TestSourceCatalogReadTracePreservesResultsErrorsAndSingleRequest(t *testing.T) {
	previousLog, previousHTTP := log.Writer(), sourceHTTP
	t.Cleanup(func() { log.SetOutput(previousLog); sourceHTTP = previousHTTP })
	for path, document := range map[string]string{"/v1/api-keys": "keys", "/v1/model-channels": "catalog"} {
		for _, test := range []struct {
			name, body, phase, budget string
			status                    int
			broken, canceled          bool
		}{
			{name: "object", body: `{"data":[{"private":"value"}],"number":1}`, status: 200, phase: "complete"},
			{name: "null", body: `null`, status: 200, phase: "complete"},
			{name: "array", body: `[]`, status: 200, phase: "decode"},
			{name: "json-error", body: `{`, status: 200, phase: "decode"},
			{name: "budget", body: `{"data":[]}`, status: 200, phase: "body", budget: "2"},
			{name: "body-error", broken: true, status: 200, phase: "body"},
			{name: "forbidden", body: "private error", status: 403, phase: "status"},
			{name: "not-found", body: "private error", status: 404, phase: "status"},
			{name: "unavailable", body: "private error", status: 503, phase: "status"},
			{name: "transport", phase: "transport"},
			{name: "canceled", phase: "transport", canceled: true},
		} {
			t.Run(document+"/"+test.name, func(t *testing.T) {
				t.Setenv("CONFIGURATION_MAX_BYTES", test.budget)
				var expected map[string]any
				var expectedMessage string
				var expectedCode int
				for _, traced := range []bool{false, true} {
					var output bytes.Buffer
					log.SetOutput(&output)
					ctx := context.WithValue(context.Background(), consoleRequestIDKey{}, "catalog-read-123")
					if traced {
						ctx = managementTraceContext(ctx)
					}
					if test.canceled {
						canceled, cancel := context.WithCancel(ctx)
						cancel()
						ctx = canceled
					}
					calls, closes := 0, 0
					sourceHTTP = &http.Client{Transport: sourceDiagnosticRoundTripFunc(func(r *http.Request) (*http.Response, error) {
						calls++
						if r.Method != http.MethodGet || r.URL.Host != "private-host" || r.URL.Path != path || r.URL.Query().Get("api_key_id") != "private-selector" || r.Header.Get("Authorization") != "Bearer private-key" || r.Header.Get("Accept") != "application/json" {
							t.Error("observation request identity changed")
						}
						if test.status == 0 {
							if test.canceled {
								return nil, r.Context().Err()
							}
							return nil, errors.New("private transport")
						}
						var reader io.Reader = strings.NewReader(test.body)
						if test.broken {
							reader = &managementTraceBrokenBody{}
						}
						return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: &managementTraceCloseBody{Reader: reader, close: func() { closes++ }}}, nil
					})}
					data, code, err := fetchSource(ctx, controlSource{sourceView: sourceView{Base: "https://private-host"}, Key: "private-key"}, path, url.Values{"api_key_id": {"private-selector"}})
					message := ""
					if err != nil {
						message = err.Error()
					}
					if calls != 1 || test.status != 0 && closes != 1 {
						t.Fatal("read retried or failed to close response", calls, closes)
					}
					if !traced {
						expected, expectedCode, expectedMessage = data, code, message
						if output.Len() != 0 {
							t.Fatal("ordinary observation became verbose")
						}
						continue
					}
					if !reflect.DeepEqual(data, expected) || code != expectedCode || message != expectedMessage || strings.Count(output.String(), "management_gateway_read ") != 1 || !strings.Contains(output.String(), "document="+document+" phase="+test.phase) || !strings.Contains(output.String(), "request_id=catalog-read-123") || strings.Contains(output.String(), "private") {
						t.Fatal("diagnostics changed behavior or leaked fields", output.String())
					}
					if test.canceled && !errors.Is(err, context.Canceled) {
						t.Fatal("original cancellation was lost")
					}
				}
			})
		}
	}
}

func (b *managementTraceCloseBody) Close() error { b.close(); return nil }

func TestManagementGatewayReadTraceLimitsScopeAndPrivateLabels(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	ctx := context.WithValue(managementTraceContext(context.Background()), consoleRequestIDKey{}, "private-id\ninjected=value")
	for path, document := range map[string]string{
		"/v1/channel-settings/management-snapshot": "snapshot",
		"/v1/channel-settings/management-revision": "snapshot_revision",
		"/v1/channel-controls":                     "controls",
		"/v1/channel-controls/revision":            "controls_revision",
		"/v1/api-keys":                             "keys",
		"/v1/model-channels":                       "catalog",
	} {
		output.Reset()
		trace := newManagementGatewayReadTrace(ctx, http.MethodGet, path)
		trace.headers(200)
		trace.read(37)
		trace.decoded(true)
		trace.finish()
		if !strings.Contains(output.String(), "document="+document+" phase=complete status=200") || !strings.Contains(output.String(), "bytes=37 canceled=false request_id=unavailable") || strings.Contains(output.String(), "private") {
			t.Fatal("fixed document label, byte count or safe request ID lost")
		}
	}
	output.Reset()
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodGet} {
		for _, path := range []string{"private-path", "/v1/channel-settings/management-snapshot?private-query", "/v1/channel-settings/export"} {
			if trace := newManagementGatewayReadTrace(ctx, method, path); trace != nil {
				t.Fatal("out-of-scope read/write acquired diagnostics")
			}
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		if trace := newManagementGatewayReadTrace(ctx, method, "/v1/channel-controls"); trace != nil {
			t.Fatal("write acquired read diagnostics")
		}
	}
	trace := newManagementGatewayReadTrace(context.Background(), http.MethodGet, "/v1/channel-controls")
	if trace != nil {
		t.Fatal("untraced request acquired diagnostics")
	}
	trace.headers(200)
	trace.read(1)
	trace.decoded(true)
	trace.finish()
	if output.Len() != 0 {
		t.Fatal("disabled trace emitted output")
	}
}

func TestManagementGatewayReadTraceSeparatesDocumentsDecodeAndValidation(t *testing.T) {
	previousLog, previousHTTP := log.Writer(), sourceHTTP
	t.Cleanup(func() { log.SetOutput(previousLog); sourceHTTP = previousHTTP })
	source := controlSource{sourceView: sourceView{Base: "https://private-host"}, Key: "private-key"}
	const complete = `{"format":"management-snapshot-v1","revision":"r1","config_revision":"c1","controls":{"revision":"r1","config_revision":"c1","unknown":null},"keys":[{"key_id":"k1"}],"catalog":[{"provider":"p1","model":"m1"}],"routes":{"k1":[{"provider":"p1","model":"m1"}]},"providers":[{"provider":"p1","api":"private-upstream"}],"export":{"revision":"r1","channel_settings":{},"temporary_definitions":{}}}`
	for _, test := range []struct {
		name, body string
		err        error
		validated  bool
	}{
		{"complete", complete, nil, true},
		{"typed-error", strings.Replace(complete, `"key_id":"k1"`, `"key_id":42`, 1), errManagementSourceContract, false},
		{"revision-error", strings.Replace(complete, `"unknown":null`, `"revision":"changed"`, 1), errManagementSourceRevision, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var before managementGatewayFacts
			for _, traced := range []bool{false, true} {
				var output bytes.Buffer
				log.SetOutput(&output)
				sourceHTTP = &http.Client{Transport: sourceDiagnosticRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.Method != http.MethodGet || r.URL.Path != "/v1/channel-settings/management-snapshot" || r.Header.Get("Authorization") != "Bearer private-key" {
						t.Error("snapshot request changed")
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(test.body)), Header: make(http.Header)}, nil
				})}
				ctx := context.Background()
				if traced {
					ctx = managementTraceContext(ctx)
				}
				ctx = context.WithValue(ctx, consoleRequestIDKey{}, "atomic-read-123")
				got, supported, err := readManagementGatewayAtomic(ctx, source)
				if !supported || !errors.Is(err, test.err) {
					t.Fatal("snapshot contract changed", supported, err)
				}
				if !traced {
					before = got
					if output.Len() != 0 {
						t.Fatal("ordinary atomic reader emitted diagnostics")
					}
					continue
				}
				if !reflect.DeepEqual(got, before) {
					t.Fatal("tracing changed snapshot facts or their partial error state")
				}
				for _, stage := range []string{"gateway_atomic", "gateway_atomic_documents", "gateway_atomic_decode"} {
					if strings.Count(output.String(), "stage="+stage+" ") != 1 {
						t.Fatal("missing or duplicate snapshot phase", stage)
					}
				}
				if strings.Contains(output.String(), "stage=gateway_atomic_validate ") != test.validated || !strings.Contains(output.String(), "request_id=atomic-read-123") || strings.Contains(output.String(), "private") {
					t.Fatal("validation phase or private diagnostic boundary changed")
				}
			}
		})
	}
}

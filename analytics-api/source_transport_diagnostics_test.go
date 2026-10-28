package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

func TestSourceTransportDiagnosticsClassifiesRetainedCauseOnly(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"canceled", context.Canceled, "source_canceled"},
		{"dns missing", &net.DNSError{Err: "private-detail", Name: "private-host", IsNotFound: true}, "source_dns_not_found"},
		{"dns timeout", &net.DNSError{Err: "private-detail", IsTimeout: true}, "source_dns_timeout"},
		{"dns other", &net.DNSError{Err: "private-detail"}, "source_dns_error"},
		{"certificate verification", &tls.CertificateVerificationError{Err: errors.New("private-detail")}, "source_tls_certificate"},
		{"certificate authority", x509.UnknownAuthorityError{}, "source_tls_certificate"},
		{"certificate hostname", x509.HostnameError{}, "source_tls_certificate"},
		{"certificate invalid", x509.CertificateInvalidError{}, "source_tls_certificate"},
		{"TLS record", tls.RecordHeaderError{}, "source_tls_protocol"},
		{"TLS alert", tls.AlertError(40), "source_tls_protocol"},
		{"refused", syscall.ECONNREFUSED, "source_connection_refused"},
		{"EOF", io.EOF, "source_connection_lost"},
		{"unexpected EOF", io.ErrUnexpectedEOF, "source_connection_lost"},
		{"reset", syscall.ECONNRESET, "source_connection_lost"},
		{"pipe", syscall.EPIPE, "source_connection_lost"},
		{"deadline", context.DeadlineExceeded, "source_timeout"},
		{"network timeout", &net.OpError{Op: "read", Err: syscall.ETIMEDOUT}, "source_timeout"},
		{"connect", &net.OpError{Op: "dial", Err: syscall.ENETUNREACH}, "source_connect_error"},
		{"unknown", errors.New("private-detail"), "source_transport_error"},
		// Text is deliberately misleading: labels come only from error types.
		{"misleading text", errors.New("DNS EOF timeout TLS connection refused"), "source_transport_error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cause := &url.Error{Op: "Get", URL: "https://private-host/private-query", Err: test.err}
			err := fmt.Errorf("private wrapper: %w", &sourceTransportError{message: "safe", cause: cause})
			if got := sourceTransportErrorClass(err); got != test.want {
				t.Fatalf("class=%q, want %q", got, test.want)
			}
		})
	}
	for _, err := range []error{nil, io.EOF, context.DeadlineExceeded, &net.DNSError{IsNotFound: true}, errors.New("source returned HTTP 403"), errors.New("invalid source JSON")} {
		if got := sourceTransportErrorClass(err); got != "" {
			t.Fatal("non-source failure acquired source classification", got)
		}
	}
	// An unrelated joined error must not override the retained HTTP cause.
	joined := errors.Join(&net.DNSError{IsNotFound: true}, &sourceTransportError{cause: io.EOF})
	if got := sourceTransportErrorClass(joined); got != "source_connection_lost" {
		t.Fatal("classified unrelated joined error", got)
	}
}

type sourceDiagnosticRoundTripFunc func(*http.Request) (*http.Response, error)

func (f sourceDiagnosticRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestSourceTransportDiagnosticsPreservesRequestsAndPublicErrors(t *testing.T) {
	var logs bytes.Buffer
	previousLog, previousHTTP := log.Writer(), sourceHTTP
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousLog); sourceHTTP = previousHTTP })
	cause := &net.DNSError{Err: "private-detail", Name: "private-host", IsNotFound: true}
	calls := 0
	sourceHTTP = &http.Client{Transport: sourceDiagnosticRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer private-key" || r.URL.Host != "private-host" {
			t.Error("request identity changed")
		}
		return nil, cause
	})}
	src := controlSource{sourceView: sourceView{Base: "https://private-host"}, Key: "private-key"}
	ctx := context.WithValue(managementTraceContext(context.Background()), consoleRequestIDKey{}, "transport-read-123")
	for _, test := range []struct {
		name, message, phase string
		call                 func() (map[string]any, int, error)
	}{
		{"observation", "source unavailable", "gateway_keys", func() (map[string]any, int, error) {
			return fetchSource(ctx, src, "/v1/api-keys", url.Values{"private-query": {"private-value"}})
		}},
		{"gateway", "无法确认来源结果，请刷新后核对，勿重复提交", "management_snapshot", func() (map[string]any, int, error) {
			return subGateway(ctx, src, http.MethodGet, "/v1/channel-settings/management-snapshot", nil)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			logs.Reset()
			before := calls
			data, code, err := test.call()
			if data != nil || code != 502 || err == nil || err.Error() != test.message || !errors.Is(err, cause) || calls != before+1 {
				t.Fatal("original response, cause or call count changed")
			}
			want := "management_source_transport_failed phase=" + test.phase + " class=source_dns_not_found canceled=false request_id=transport-read-123"
			if !strings.Contains(logs.String(), want) || strings.Count(logs.String(), "management_source_transport_failed") != 1 {
				t.Fatal("missing or duplicate correlated diagnostic")
			}
			if strings.Contains(logs.String(), "private") || strings.Contains(logs.String(), "https://") {
				t.Fatal("private transport context escaped diagnostic")
			}
		})
	}
}

func TestSourceTransportDiagnosticsLimitsLabelsAndTraceScope(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	ctx := managementTraceContext(context.Background())
	err := &sourceTransportError{cause: io.EOF}
	for path, phase := range map[string]string{
		"/v1/channel-settings/management-snapshot": "management_snapshot",
		"/v1/channel-settings/management-revision": "management_revision",
		"/v1/channel-controls":                     "gateway_controls",
		"/v1/channel-controls/revision":            "observation_revision",
		"/v1/channel-settings/export":              "gateway_export",
		"/v1/api-keys":                             "gateway_keys",
		"/v1/model-channels":                       "gateway_catalog",
	} {
		logs.Reset()
		managementSourceTransportFailure(ctx, http.MethodGet, path, err)
		if !strings.Contains(logs.String(), "phase="+phase+" class=source_connection_lost canceled=false request_id=unavailable") {
			t.Fatal("missing allowlisted phase", phase)
		}
	}
	logs.Reset()
	managementSourceTransportFailure(context.Background(), http.MethodGet, "/v1/api-keys", err)
	managementSourceTransportFailure(ctx, http.MethodPost, "/v1/api-keys", err)
	managementSourceTransportFailure(ctx, http.MethodDelete, "/v1/channel-controls", err)
	managementSourceTransportFailure(ctx, http.MethodGet, "/private-path?private-query", err)
	managementSourceTransportFailure(ctx, http.MethodGet, "/v1/api-keys", io.EOF)
	if logs.Len() != 0 {
		t.Fatal("out-of-scope operation logged")
	}
	unsafe := context.WithValue(ctx, consoleRequestIDKey{}, "private-id\ninjected=value")
	managementSourceTransportFailure(unsafe, http.MethodGet, "/v1/api-keys", err)
	if strings.Contains(logs.String(), "private") || !strings.Contains(logs.String(), "request_id=unavailable") {
		t.Fatal("unsafe request ID escaped")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	logs.Reset()
	managementSourceTransportFailure(canceled, http.MethodGet, "/v1/api-keys", &sourceTransportError{cause: context.Canceled})
	if !strings.Contains(logs.String(), "class=source_canceled canceled=true") {
		t.Fatal("caller cancellation lost")
	}
}

func TestSourceTransportDiagnosticsManagementFailureRemainsUnavailable(t *testing.T) {
	s := managementBindingTestService(t)
	ctx := context.Background()
	src := controlSource{sourceView: sourceView{ID: "transport-fixture", Base: "https://private-host"}, Key: "private-key"}
	if _, err := s.control.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	token, err := s.control.newSession(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	previousLog, previousHTTP := log.Writer(), sourceHTTP
	t.Cleanup(func() { log.SetOutput(previousLog); sourceHTTP = previousHTTP })
	handler := s.Handler()
	for _, test := range []struct {
		name, class string
		cause       error
	}{
		{"DNS", "source_dns_not_found", &net.DNSError{Name: "private-host", IsNotFound: true}},
		{"EOF", "source_connection_lost", io.EOF},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			log.SetOutput(&logs)
			var calls atomic.Int32
			sourceHTTP = &http.Client{Transport: sourceDiagnosticRoundTripFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return nil, test.cause
			})}
			r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/query", nil)
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
			r.Header.Set("X-Console-Request-ID", "transport-handler-123")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 503 || w.Body.String() != "渠道数据读取失败，请重新读取\n" || calls.Load() != 1 {
				t.Fatal("source failure changed public status, message or retry behavior", w.Code, calls.Load())
			}
			for _, marker := range []string{"management_source_transport_failed phase=management_snapshot class=" + test.class, "management_query read_ms=", "class=" + test.class + " canceled=false request_id=transport-handler-123"} {
				if !strings.Contains(logs.String(), marker) {
					t.Fatal("missing handler diagnostic", marker)
				}
			}
			if strings.Contains(logs.String(), "private") || strings.Contains(logs.String(), "database_connection_lost") || strings.Contains(w.Body.String(), "source_") {
				t.Fatal("failure leaked detail or was mislabeled as database")
			}
		})
	}
}

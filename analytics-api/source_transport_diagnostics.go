package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"syscall"
)

// Classify only a retained source HTTP transport cause. A database EOF/DNS
// error or an HTTP/JSON response error must not become a source network fault.
// Never inspect or log Error(): it can contain URLs, credentials and hostnames.
func sourceTransportErrorClass(err error) string {
	var source *sourceTransportError
	if !errors.As(err, &source) || source == nil {
		return ""
	}
	err = source.cause
	if errors.Is(err, context.Canceled) {
		return "source_canceled"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		if dns.IsNotFound {
			return "source_dns_not_found"
		}
		if dns.IsTimeout {
			return "source_dns_timeout"
		}
		return "source_dns_error"
	}
	var verify *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &verify) || errors.As(err, &authority) || errors.As(err, &hostname) || errors.As(err, &invalid) {
		return "source_tls_certificate"
	}
	var record tls.RecordHeaderError
	var alert tls.AlertError
	if errors.As(err, &record) || errors.As(err, &alert) {
		return "source_tls_protocol"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "source_connection_refused"
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return "source_connection_lost"
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return "source_timeout"
	}
	var operation *net.OpError
	if errors.As(err, &operation) && operation.Op == "dial" {
		return "source_connect_error"
	}
	return "source_transport_error"
}

// Only known management GETs are observed. Unknown paths and mutation methods
// never become log labels. This observes an existing failure, with no extra
// request, retry, resolution attempt or change to the original returned error.
func managementSourceTransportFailure(ctx context.Context, method, path string, err error) {
	if !managementTraceEnabled(ctx) || method != http.MethodGet {
		return
	}
	phase := ""
	switch path {
	case "/v1/channel-settings/management-snapshot":
		phase = "management_snapshot"
	case "/v1/channel-settings/management-revision":
		phase = "management_revision"
	case "/v1/channel-controls":
		phase = "gateway_controls"
	case "/v1/channel-controls/revision":
		phase = "observation_revision"
	case "/v1/channel-settings/export":
		phase = "gateway_export"
	case "/v1/api-keys":
		phase = "gateway_keys"
	case "/v1/model-channels":
		phase = "gateway_catalog"
	}
	class := sourceTransportErrorClass(err)
	if phase == "" || class == "" {
		return
	}
	id := consoleRequestID(ctx)
	if !consoleRequestIDPattern.MatchString(id) {
		id = "unavailable"
	}
	log.Printf("management_source_transport_failed phase=%s class=%s canceled=%t request_id=%s", phase, class, ctx.Err() != nil, id)
}

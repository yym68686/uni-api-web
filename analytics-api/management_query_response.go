package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/andybalholm/brotli"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
)

func managementAcceptsGzip(header string) bool {
	return managementAcceptsEncoding(header, "gzip")
}

func managementAcceptsEncoding(header, encoding string) bool {
	return managementEncodingQuality(header, encoding) > 0
}
func managementEncodingQuality(header, encoding string) float64 {
	found := false
	qualityValue := 1.0
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(item, ";")
		if !strings.EqualFold(strings.TrimSpace(parts[0]), encoding) {
			continue
		}
		found = true
		seenQuality := false
		for _, parameter := range parts[1:] {
			name, raw, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") || seenQuality {
				return 0
			}
			seenQuality = true
			quality, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
			if err != nil || !(quality > 0 && quality <= 1) {
				return 0
			}
			qualityValue = min(qualityValue, quality)
		}
	}
	if found {
		return qualityValue
	}
	return 0
}

// Compression changes only the transport encoding. The complete, current page
// and its revision are finalized before any success headers or bytes are sent.
func writeManagementJSON(w http.ResponseWriter, r *http.Request, value any) error {
	return writeManagementJSONMedia(w, r, value, "application/json")
}

// Hash the exact previous media/encoding/body byte sequence without two
// response-sized string copies. A bounded scratch block keeps large inputs
// cache-local while leaving the original JSON available for compression.
func managementResponseETag(mediaType, encoding string, body []byte) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, mediaType)
	_, _ = hash.Write([]byte{0})
	_, _ = io.WriteString(hash, encoding)
	_, _ = hash.Write([]byte{0})
	var buffer [32 << 10]byte
	for len(body) > 0 {
		n := copy(buffer[:], body)
		_, _ = hash.Write(buffer[:n])
		body = body[n:]
	}
	return `"` + hex.EncodeToString(hash.Sum(nil)) + `"`
}

func writeManagementJSONMedia(w http.ResponseWriter, r *http.Request, value any, mediaType string) error {
	if err := r.Context().Err(); err != nil {
		return err
	}
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "渠道响应编码失败", http.StatusInternalServerError)
		return err
	}
	return writeEncodedManagementJSONMedia(w, r, body, mediaType)
}

// body is the complete json.Marshal output, before transport compression.
// Keep it unchanged so fresh v3 writes can independently clone these same
// bytes for the bounded store without serializing every evidence cell again.
func writeEncodedManagementJSONMedia(w http.ResponseWriter, r *http.Request, body []byte, mediaType string) error {
	w.Header().Add("Vary", "Accept-Encoding")
	if mediaType == managementPageV3MediaType {
		w.Header().Add("Vary", "X-Console-Current-Validation")
		if r.Header.Get("X-Console-Current-Validation") == "1" && w.Header().Get("X-Console-Validated-Request-ID") != "" {
			// Revalidation runs only AFTER the same live permission, gateway
			// and database proof as a full 200 response. Never allow freshness
			// lifetimes, disconnected reuse or shared proxy storage.
			w.Header().Add("Vary", "Cookie")
			w.Header().Set("Cache-Control", "private, no-cache, must-revalidate")
			tag := managementResponseETag(mediaType, r.Header.Get("Accept-Encoding"), body)
			w.Header().Set("ETag", tag)
			w.Header().Set("Content-Type", mediaType)
			for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
				if strings.TrimSpace(candidate) == tag {
					w.WriteHeader(http.StatusNotModified)
					// This is a complete, freshly validated response with no
					// body. Do not leave its headers buffered until the caller
					// finishes retaining the independently cloned page. Keep
					// retention synchronous; no proof or lifetime is relaxed.
					endFlush := managementReadSpan(r.Context(), "response_flush")
					err := http.NewResponseController(w).Flush()
					endFlush()
					unsupported := errors.Is(err, http.ErrNotSupported)
					if managementTraceEnabled(r.Context()) {
						log.Printf("management_response_flush status=304 flushed=%t unsupported=%t request_id=%s", err == nil, unsupported, consoleRequestID(r.Context()))
					}
					if unsupported {
						// Custom writers without flushing retain their old path.
						return nil
					}
					return err
				}
			}
		}
	}
	brQuality, gzipQuality := managementEncodingQuality(r.Header.Get("Accept-Encoding"), "br"), managementEncodingQuality(r.Header.Get("Accept-Encoding"), "gzip")
	if len(body) > 1024 && brQuality > 0 && brQuality >= gzipQuality {
		var compressed bytes.Buffer
		writer := brotli.NewWriterLevel(&compressed, 5)
		if _, err := writer.Write(body); err != nil {
			return err
		}
		if err := writer.Close(); err != nil {
			return err
		}
		if compressed.Len() < len(body) {
			body = compressed.Bytes()
			w.Header().Set("Content-Encoding", "br")
		}
	} else if len(body) > 1024 && managementAcceptsGzip(r.Header.Get("Accept-Encoding")) {
		var compressed bytes.Buffer
		writer, _ := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
		_, _ = writer.Write(body)
		_ = writer.Close()
		if compressed.Len() < len(body) {
			body = compressed.Bytes()
			w.Header().Set("Content-Encoding", "gzip")
		}
	}
	if err := r.Context().Err(); err != nil {
		return err
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(body)
	return err
}

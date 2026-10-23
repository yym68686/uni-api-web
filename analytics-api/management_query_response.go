package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"github.com/andybalholm/brotli"
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

func writeManagementJSONMedia(w http.ResponseWriter, r *http.Request, value any, mediaType string) error {
	if err := r.Context().Err(); err != nil {
		return err
	}
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "渠道响应编码失败", http.StatusInternalServerError)
		return err
	}
	w.Header().Add("Vary", "Accept-Encoding")
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
	_, err = w.Write(body)
	return err
}

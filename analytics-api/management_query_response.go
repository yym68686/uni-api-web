package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func managementAcceptsGzip(header string) bool {
	found := false
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(item, ";")
		if !strings.EqualFold(strings.TrimSpace(parts[0]), "gzip") {
			continue
		}
		found = true
		seenQuality := false
		for _, parameter := range parts[1:] {
			name, raw, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") || seenQuality {
				return false
			}
			seenQuality = true
			quality, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
			if err != nil || !(quality > 0 && quality <= 1) {
				return false
			}
		}
	}
	return found
}

// Compression changes only the transport encoding. The complete, current page
// and its revision are finalized before any success headers or bytes are sent.
func writeManagementJSON(w http.ResponseWriter, r *http.Request, value any) error {
	if err := r.Context().Err(); err != nil {
		return err
	}
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "渠道响应编码失败", http.StatusInternalServerError)
		return err
	}
	w.Header().Add("Vary", "Accept-Encoding")
	if len(body) > 1024 && managementAcceptsGzip(r.Header.Get("Accept-Encoding")) {
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
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(body)
	return err
}

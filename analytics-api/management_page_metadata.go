package main

import (
	"encoding/json"
	"net/http"
)

const managementMetadataHeader = "X-Console-Management-Metadata"

// The request carries only a version for data already received in this tab.
// This version does not participate in a page proof or authorize reuse.
func managementMetadataRequest(r *http.Request) string {
	if r.Header.Get("Accept") != managementPageV3MediaType {
		return ""
	}
	raw := r.Header.Get(managementMetadataHeader)
	if raw == "1" {
		return raw
	}
	if len(raw) != 64 {
		return ""
	}
	for _, c := range raw {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return ""
		}
	}
	return raw
}

func managementMetadataValidatorKey(r *http.Request, key string) string {
	if version := managementMetadataRequest(r); version != "" {
		return key + "\x00metadata:" + version
	}
	return key
}

// Only stable, public, page-independent data can be reused. In particular,
// keep account/session information, rows, imports and routes on the wire.
// Modifying this value copy must not touch the retained complete columns.
func projectManagementPageMetadata(page *managementPageResponse, client string) {
	page.MetadataVersion = ""
	if client == "" || page.Owner == "" {
		return
	}
	raw, err := json.Marshal([]any{"management-metadata-v1", page.Owner, page.Rates,
		page.Platforms, page.Models, page.DetectionModels, page.Context.Prices, page.Context.FingerprintModels})
	if err != nil {
		return
	}
	page.MetadataVersion = tokenHash(string(raw))
	if client == page.MetadataVersion {
		page.Rates, page.Platforms, page.Models, page.DetectionModels = nil, nil, nil, nil
		page.Context.Prices, page.Context.FingerprintModels = nil, nil
	}
}

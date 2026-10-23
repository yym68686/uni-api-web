package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
)

const managementPageSize = 25

var errManagementCursor = errors.New("invalid management page cursor")
var errManagementRevision = errors.New("management results changed; reload the first page")

type managementPageCursor struct {
	Version  int    `json:"v"`
	Offset   int    `json:"offset"`
	Revision string `json:"revision"`
	Filter   string `json:"filter"`
}

type managementQueryPage struct {
	Format          string               `json:"format"`
	Revision        string               `json:"revision"`
	ReadAtMS        int64                `json:"read_at_ms"`
	Rows            []managementQueryRow `json:"rows"`
	Total           int                  `json:"total"`
	EligibleCount   int                  `json:"eligible_count"`
	Offset          int                  `json:"offset"`
	PageSize        int                  `json:"page_size"`
	NextCursor      string               `json:"next_cursor,omitempty"`
	PreviousCursor  string               `json:"previous_cursor,omitempty"`
	Rates           []float64            `json:"rates"`
	Platforms       []string             `json:"platforms"`
	Models          []string             `json:"models"`
	DetectionModels []string             `json:"detection_models"`
}

func managementCursorMAC(key []byte, owner, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("management-page-v1\x00"))
	mac.Write([]byte(owner))
	mac.Write([]byte{0})
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func encodeManagementCursor(key []byte, owner string, cursor managementPageCursor) string {
	raw, _ := json.Marshal(cursor)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + base64.RawURLEncoding.EncodeToString(managementCursorMAC(key, owner, payload))
}

func decodeManagementCursor(key []byte, owner, value string) (managementPageCursor, error) {
	var cursor managementPageCursor
	if len(value) > 1024 {
		return cursor, errManagementCursor
	}
	payload, encodedSignature, found := strings.Cut(value, ".")
	if !found {
		return cursor, errManagementCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(encodedSignature)
	if err != nil || !hmac.Equal(signature, managementCursorMAC(key, owner, payload)) {
		return cursor, errManagementCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Version != 1 || cursor.Offset < 0 || cursor.Offset%managementPageSize != 0 || cursor.Revision == "" || cursor.Filter == "" {
		return cursor, errManagementCursor
	}
	return cursor, nil
}

func managementEligibleRows(rows []managementQueryRow) []managementQueryRow {
	out, seen := []managementQueryRow{}, map[string]bool{}
	for _, row := range rows {
		eligible := row.Account.ID == "" && row.Configured != nil || row.Target.Active && row.Target.KeyID > 0 && !managementPending(row.Account.State) && row.Target.State != "error"
		if !eligible {
			continue
		}
		id := qualityGroupID(row.Account.ID, row.Target.GroupID)
		if row.Account.ID == "" && row.Configured != nil {
			id = "configured:" + row.Configured.Provider + ":" + row.Configured.Engine
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, row)
		}
	}
	return out
}

// Pagination happens only after the full authorized query has been evaluated.
// No snapshot is held between requests: a cursor is valid only against a newly
// read, identical revision. Batch operations must use the full result and the
// same revision fence rather than treating these 25 rows as a whole selection.
func buildManagementQueryPage(in managementQueryInput, filters managementQueryFilters, evidence managementFilterEvidence, owner string, key []byte, cursor string, readAt time.Time) (managementQueryPage, error) {
	rows, err := buildManagementQueryRows(in, filters.Model, filters.AccountID)
	if err != nil {
		return managementQueryPage{}, err
	}
	return buildManagementQueryPageFromRows(in, rows, filters, evidence, owner, key, cursor, readAt)
}

func buildManagementQueryPageFromRows(in managementQueryInput, rows []managementQueryRow, filters managementQueryFilters, evidence managementFilterEvidence, owner string, key []byte, cursor string, readAt time.Time) (managementQueryPage, error) {
	page := managementQueryPage{Format: "management-page-v1", PageSize: managementPageSize, ReadAtMS: readAt.UnixMilli(), Rows: []managementQueryRow{}}
	filtered := filterManagementQueryRows(rows, filters, evidence)
	page.Rates, page.Platforms = filtered.Rates, filtered.Platforms
	page.Total, page.EligibleCount = len(filtered.Rows), len(managementEligibleRows(filtered.Rows))
	models := append([]string{}, subModels...)
	for _, c := range in.Channels {
		models = append(models, c.Models...)
	}
	for _, c := range in.Checks {
		if c.Kind == "model" {
			models = append(models, c.Model)
		}
	}
	for _, a := range in.Accounts {
		for _, target := range a.Targets {
			for _, check := range target.Models {
				models = append(models, check.Model)
			}
		}
	}
	if filters.Model != "" {
		models = append(models, filters.Model)
	}
	page.Models = managementUnique(models)
	extra := []string{}
	for _, row := range filtered.Rows {
		for _, check := range row.Checks {
			if !slices.Contains(subModels, check.Model) {
				extra = append(extra, check.Model)
			}
		}
	}
	extra = managementUnique(extra)
	sort.Slice(extra, func(i, j int) bool {
		return slices.Compare(utf16.Encode([]rune(extra[i])), utf16.Encode([]rune(extra[j]))) < 0
	})
	page.DetectionModels = append(append([]string{}, subModels...), extra...)
	var err error
	if in.ModelsRevision != "" {
		page.Revision, err = managementProjectedRevision(in, page, evidence)
		if err != nil {
			return page, err
		}
	} else {
		revisionData, err := json.Marshal(struct {
			Rows                               []managementQueryRow
			Rates                              []float64
			Platforms, Models, DetectionModels []string
			Evidence                           managementFilterEvidence
			Sources                            map[string]string
		}{filtered.Rows, page.Rates, page.Platforms, page.Models, page.DetectionModels, evidence, in.SourceRevisions})
		if err != nil {
			return page, err
		}
		page.Revision = tokenHash(string(revisionData))
	}
	filterHash := tokenHash(mustJSON(filters))
	if cursor != "" {
		decoded, err := decodeManagementCursor(key, owner, cursor)
		if err != nil {
			return page, err
		}
		if decoded.Filter != filterHash {
			return page, errManagementCursor
		}
		if decoded.Revision != page.Revision {
			return page, errManagementRevision
		}
		page.Offset = decoded.Offset
		if page.Offset >= page.Total && page.Offset != 0 {
			return page, errManagementCursor
		}
	}
	end := min(page.Total, page.Offset+managementPageSize)
	page.Rows = append(page.Rows, filtered.Rows[page.Offset:end]...)
	encode := func(offset int) string {
		return encodeManagementCursor(key, owner, managementPageCursor{Version: 1, Offset: offset, Revision: page.Revision, Filter: filterHash})
	}
	if end < page.Total {
		page.NextCursor = encode(end)
	}
	if page.Offset > 0 {
		page.PreviousCursor = encode(max(0, page.Offset-managementPageSize))
	}
	return page, nil
}

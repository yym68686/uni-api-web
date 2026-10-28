package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

var errManagementFilters = errors.New("invalid management query filters")

type managementPageResponse struct {
	preparedColumns *managementColumnPage
	Owner           string `json:"owner,omitempty"`
	managementQueryPage
	SelectionToken string                `json:"selection_token"`
	Accounts       []subAccount          `json:"accounts"`
	Context        managementPageContext `json:"context"`
}

type managementSelectionResponse struct {
	Format        string               `json:"format"`
	Revision      string               `json:"revision"`
	ReadAtMS      int64                `json:"read_at_ms"`
	Total         int                  `json:"total"`
	EligibleCount int                  `json:"eligible_count"`
	Rows          []managementQueryRow `json:"rows"`
	Eligible      []managementQueryRow `json:"eligible"`
	Input         managementQueryInput `json:"input"`
	Prices        []Price              `json:"prices"`
}

func parseManagementQuery(values url.Values) (managementQueryFilters, error) {
	filters := managementQueryFilters{GroupStatus: "available"}
	for key, list := range values {
		if !slices.Contains([]string{"filters", "cursor", "selection", "refresh_cursor"}, key) || len(list) != 1 || len(list[0]) > 8192 {
			return filters, errManagementFilters
		}
	}
	if values.Has("refresh_cursor") && values.Get("refresh_cursor") != "1" {
		return filters, errManagementFilters
	}
	raw := values.Get("filters")
	if raw == "" {
		raw = "{}"
	}
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return filters, errManagementFilters
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&filters); err != nil {
		return filters, errManagementFilters
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return filters, errManagementFilters
	}
	for _, field := range []string{filters.Search, filters.AccountID, filters.Model, filters.Platform} {
		if len(field) > 2048 || strings.ContainsRune(field, '\x00') {
			return filters, errManagementFilters
		}
	}
	for _, constraint := range []struct {
		value   string
		allowed []string
	}{
		{filters.Sort, []string{"", "asc", "desc"}},
		{filters.GroupStatus, []string{"available", "all", "unavailable"}},
		{filters.Availability, []string{"", "success", "error", "untested"}},
		{filters.PriceStatus, []string{"", "normal", "abnormal", "unconfirmed", "unconfirmed_match", "unconfirmed_mismatch"}},
		{filters.Compaction, []string{"", "supported", "unsupported", "error", "untested", "not_applicable"}},
		{filters.ToolUse, []string{"", "supported", "unsupported", "error", "untested", "not_applicable"}},
		{filters.Quality, []string{"", "pass", "fail", "inconclusive", "error"}},
		{filters.Joined, []string{"", "joined", "unjoined"}},
		{filters.ModelMatch, []string{"", "match", "mismatch", "missing", "invalid", "unavailable", "legacy", "untested"}},
		{filters.Fingerprint, []string{"", "match", "mismatch", "insufficient", "error", "queued", "running", "interrupted", "untested", "not_applicable"}},
	} {
		if !slices.Contains(constraint.allowed, constraint.value) {
			return filters, errManagementFilters
		}
	}
	if filters.MaxRate != "" && filters.MaxRate != "mismatch" {
		rate, err := strconv.ParseFloat(filters.MaxRate, 64)
		if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
			return filters, errManagementFilters
		}
	}
	if filters.MinQuality != "" {
		minimum, err := strconv.Atoi(filters.MinQuality)
		if err != nil || minimum < 0 || minimum > 100 || minimum%10 != 0 {
			return filters, errManagementFilters
		}
	}
	return filters, nil
}

// The selection is a separate, fresh, owner-authorized read of all matching
// rows. Its signed token fences it to the page the user reviewed; no mutation
// endpoint may mistake the current 25-row page for the complete selection.
func (s *Service) managementQuery(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(managementTraceContext(r.Context()))
	w.Header().Set("Cache-Control", "no-store")
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "请重新登录", http.StatusUnauthorized)
		return
	}
	if len(r.URL.RawQuery) > 16384 {
		http.Error(w, "筛选参数无效", http.StatusBadRequest)
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "筛选参数无效", http.StatusBadRequest)
		return
	}
	filters, err := parseManagementQuery(values)
	if err != nil {
		http.Error(w, "筛选参数无效", http.StatusBadRequest)
		return
	}
	cursor, selection := values.Get("cursor"), values.Get("selection")
	rebase := values.Get("refresh_cursor") == "1"
	if cursor != "" && selection != "" || rebase && (cursor == "" || selection != "") {
		http.Error(w, "分页与完整选择不可同时读取", http.StatusBadRequest)
		return
	}
	token := cursor
	if selection != "" {
		token = selection
	}
	if token != "" {
		decoded, err := decodeManagementCursor(s.control.key, owner, token)
		if err != nil || decoded.Filter != tokenHash(mustJSON(filters)) || selection != "" && decoded.Offset != 0 {
			http.Error(w, "读取凭据已失效，请刷新渠道", http.StatusBadRequest)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	start := time.Now()
	cacheCursor := cursor
	if rebase {
		cacheCursor += "\x00refresh"
	}
	computedKey := managementComputedKey(owner, filters, cacheCursor)
	if selection == "" {
		page, reused, reuseErr := s.currentComputedManagementPageMode(ctx, owner, computedKey, r.Header.Get("Accept") == managementPageV3MediaType)
		if reuseErr != nil {
			http.Error(w, "渠道数据实时核验失败，请重新读取", http.StatusServiceUnavailable)
			return
		}
		if reused {
			page.Owner = owner
			err := writeManagementPage(w, r, page)
			log.Printf("management_current_computation verified=true elapsed_ms=%d rows=%d total=%d failed=%t request_id=%s", time.Since(start).Milliseconds(), len(page.Rows), page.Total, err != nil, consoleRequestID(r.Context()))
			return
		}
		page, reused, reuseErr = s.currentManagementPagingPage(ctx, owner, filters, cursor, rebase)
		if reuseErr == nil && reused {
			err := writeManagementPage(w, r, page)
			log.Printf("management_paging_current verified=true elapsed_ms=%d rows=%d total=%d offset=%d failed=%t request_id=%s", time.Since(start).Milliseconds(), len(page.Rows), page.Total, page.Offset, err != nil, consoleRequestID(r.Context()))
			return
		}
		if reuseErr != nil && !errors.Is(reuseErr, errManagementRevision) && !errors.Is(reuseErr, errManagementCursor) {
			http.Error(w, "渠道数据实时核验失败，请重新读取", http.StatusServiceUnavailable)
			return
		}
	}
	snapshot, err := s.readManagementQuerySnapshotMode(ctx, owner, filters, token, selection != "", rebase)
	readElapsed := time.Since(start)
	view, page, rows, evidence := snapshot.View, snapshot.Page, snapshot.Rows, snapshot.Evidence
	if err == nil {
		err = ctx.Err()
	}
	class := "none"
	if err != nil {
		class = databaseErrorClass(err)
		status, message := http.StatusServiceUnavailable, "渠道数据读取失败，请重新读取"
		if errors.Is(err, errManagementRevision) || errors.Is(err, errManagementSourceRevision) {
			status, message, class = http.StatusConflict, "渠道配置或检测结果已变化，请重新读取", "management_revision_changed"
		} else if errors.Is(err, errManagementCursor) {
			status, message, class = http.StatusBadRequest, "分页凭据已失效，请刷新渠道", "management_cursor_invalid"
		} else if errors.Is(err, errManagementSourceContract) {
			class = "management_source_incomplete"
		}
		http.Error(w, message, status)
	} else if selection != "" {
		filtered := filterManagementQueryRows(rows, filters, evidence).Rows
		err = writeManagementJSON(w, r, managementSelectionResponse{Format: "management-selection-v1", Revision: page.Revision, ReadAtMS: page.ReadAtMS, Total: page.Total, EligibleCount: page.EligibleCount, Rows: filtered, Eligible: managementEligibleRows(filtered), Input: view.Input, Prices: view.Prices})
	} else {
		headers := managementAccountHeaders(view.Input.Accounts)
		selectionToken := encodeManagementCursor(s.control.key, owner, managementPageCursor{Version: 1, Revision: page.Revision, Filter: tokenHash(mustJSON(filters))})
		response := managementPageResponse{Owner: owner, managementQueryPage: page, SelectionToken: selectionToken, Accounts: headers, Context: managementContextForPage(view, page, filters.Model)}
		err = s.writeAndRememberManagementPage(w, r, response, computedKey, snapshot.Proof)
		if err == nil {
			s.rememberManagementPagingPlan(owner, filters, snapshot)
		}
	}
	if err != nil && class == "none" {
		class = "management_response_write_failed"
	}
	log.Printf("management_query read_ms=%d elapsed_ms=%d rows=%d total=%d selection=%t class=%s canceled=%t request_id=%s", readElapsed.Milliseconds(), time.Since(start).Milliseconds(), len(page.Rows), page.Total, selection != "", class, ctx.Err() != nil, consoleRequestID(r.Context()))
}

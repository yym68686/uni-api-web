package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"time"
)

type managementOptimizationResponse struct {
	managementSelectionResponse
	Owner   string                 `json:"owner"`
	Filters managementQueryFilters `json:"filters"`
}

// This endpoint can only read a new preview. It issues no selection token and
// cannot submit a check or change a route. Applying the user's chosen changes
// still uses channel-batch's original configuration revision and validation.
func (s *Service) managementOptimizationPreview(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(managementTraceContext(r.Context()))
	w.Header().Set("Cache-Control", "no-store")
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "请重新登录", http.StatusUnauthorized)
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 16384 || len(values) != 1 || !values.Has("filters") {
		http.Error(w, "筛选参数无效", http.StatusBadRequest)
		return
	}
	filters, err := parseManagementQuery(values)
	if err != nil {
		http.Error(w, "筛选参数无效", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if filters.Model != "" {
		ctx = context.WithValue(ctx, managementSelectedReceiptsKey{}, true)
		ctx = context.WithValue(ctx, managementSelectedModelsKey{}, true)
	}
	start := time.Now()
	snapshot, err := s.readManagementQuerySnapshotWithPreview(ctx, owner, filters, "", false, false, true)
	if err == nil {
		err = ctx.Err()
	}
	class := "none"
	if err != nil {
		status, message := http.StatusServiceUnavailable, "优化预览读取失败，请重新读取"
		class = databaseErrorClass(err)
		if errors.Is(err, errManagementSourceRevision) || errors.Is(err, errManagementRevision) {
			status, message, class = http.StatusConflict, "读取期间渠道配置已变化，请重新打开优化预览", "management_source_changed"
		}
		http.Error(w, message, status)
	} else {
		page, view, rows := snapshot.Page, snapshot.View, snapshot.Rows
		eligible := managementEligibleRows(rows)
		err = writeManagementJSON(w, r, managementOptimizationResponse{
			managementSelectionResponse: managementSelectionResponse{
				Format: "management-optimization-preview-v1", Revision: page.Revision, ReadAtMS: page.ReadAtMS,
				Total: len(rows), EligibleCount: len(eligible), Rows: rows,
				Eligible: eligible, Input: view.Input, Prices: view.Prices,
			}, Owner: owner, Filters: filters,
		})
		if err != nil {
			class = "management_response_write_failed"
		}
	}
	log.Printf("management_optimization_preview elapsed_ms=%d rows=%d class=%s canceled=%t request_id=%s", time.Since(start).Milliseconds(), len(snapshot.Rows), class, ctx.Err() != nil, consoleRequestID(r.Context()))
}

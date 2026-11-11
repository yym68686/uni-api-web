package main

import (
	"database/sql"
	"errors"
	"math"
	"math/big"
	"net/http"
	"strconv"
)

// Zero is the legacy in-memory value when an older response or a synthetic
// unbound channel has no setting. Saved coefficients must be strictly positive.
func siteRateCoefficient(value float64) float64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 1
	}
	return value
}

// Multiply decimal values before converting back to a JSON number, so labels
// such as 0.3 × 0.1 stay 0.03 rather than showing binary float artifacts.
func adjustedSiteAmount(value, coefficient float64) float64 {
	coefficient = siteRateCoefficient(coefficient)
	if coefficient == 1 {
		return value
	}
	amount, ok := new(big.Rat).SetString(strconv.FormatFloat(value, 'f', -1, 64))
	if !ok {
		return value * coefficient
	}
	factor, _ := new(big.Rat).SetString(strconv.FormatFloat(coefficient, 'f', -1, 64))
	amount.Mul(amount, factor)
	result, _ := amount.Float64()
	return result
}

func (s *Service) subAccountSettings(w http.ResponseWriter, r *http.Request) {
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var input struct {
		Coefficient *float64 `json:"rate_coefficient"`
	}
	if !decodeConfiguration(w, r, &input) {
		return
	}
	if input.Coefficient == nil || *input.Coefficient <= 0 || *input.Coefficient > 1000000 || math.IsNaN(*input.Coefficient) || math.IsInf(*input.Coefficient, 0) {
		http.Error(w, "倍率系数必须大于 0 且不超过 1000000", http.StatusBadRequest)
		return
	}
	var coefficient float64
	err = s.control.db.QueryRowContext(r.Context(), `UPDATE console_sub_accounts SET rate_coefficient=$3 WHERE id=$1 AND owner=$2 RETURNING rate_coefficient`, r.PathValue("id"), owner, *input.Coefficient).Scan(&coefficient)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "站点账号不存在", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "站点设置保存失败", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rate_coefficient": coefficient})
}

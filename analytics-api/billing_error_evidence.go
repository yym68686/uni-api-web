package main

import (
	"context"
	"encoding/hex"
)

// Exact response bodies verified against sub2api api_key_auth.go and
// openai_gateway_handler.go's pre-forward CheckBillingEligibility. Deliberately
// not a substring match for "balance", nor a blanket exemption for HTTP 403.
const subBalanceAuthDigest = "7650844e093da022f530f60d448c6e401ca17d5efd97d38978acf34e43cdcb71"
const subBalanceEligibilityDigest = "89698b2e03311b2e3ab9b559b2f96f02aaf2caa7bf654e3a786cc360928bd9b7"

func validErrorDigest(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == value
}

func confirmedBalanceRejection(b billingFact) bool {
	return b.Status == 403 && (b.ErrorSHA256 == subBalanceAuthDigest || b.ErrorSHA256 == subBalanceEligibilityDigest)
}

func (s *Service) supplementBillingErrorEvidence(ctx context.Context, facts []billingFact) error {
	indices := map[string][]int{}
	for i, b := range facts {
		if b.ErrorSHA256 == "" && b.Status == 403 {
			indices[b.Event] = append(indices[b.Event], i)
		}
	}
	if len(indices) == 0 {
		return nil
	}
	ids := make([]string, 0, len(indices))
	for id := range indices {
		ids = append(ids, id)
	}
	rows, err := s.control.db.QueryContext(ctx, `SELECT source_id,event_id,request_id,attempt_id,provider,status,error_sha256 FROM console_billing_error_evidence WHERE event_id=ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var e billingFact
		if err = rows.Scan(&e.Source, &e.Event, &e.Request, &e.Attempt, &e.Provider, &e.Status, &e.ErrorSHA256); err != nil {
			return err
		}
		for _, i := range indices[e.Event] {
			b := &facts[i]
			if b.Source == e.Source && b.Request == e.Request && b.Attempt == e.Attempt && b.Provider == e.Provider && b.Status == e.Status {
				b.ErrorSHA256 = e.ErrorSHA256
			}
		}
	}
	return rows.Err()
}

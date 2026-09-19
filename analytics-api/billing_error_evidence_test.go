package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestBalanceErrorFingerprintsAreExactProtocolResponses(t *testing.T) {
	for body, expected := range map[string]string{
		`{"code":"INSUFFICIENT_BALANCE","message":"Insufficient account balance"}`: subBalanceAuthDigest,
		`{"error":{"message":"insufficient balance","type":"billing_error"}}`:      subBalanceEligibilityDigest,
	} {
		digest := sha256.Sum256([]byte(body))
		if hex.EncodeToString(digest[:]) != expected {
			t.Fatal("fingerprint mismatch")
		}
	}
}

func TestBalanceRejectedAttemptsReconcileAlongsidePaidRequests(t *testing.T) {
	s, account, owner := attributionFixture(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	facts := []Fact{attributedFact("paid", "caller", now.Add(-time.Minute)), attributedFact("auth", "caller", now.Add(-time.Minute)), attributedFact("eligibility", "caller", now.Add(-time.Minute))}
	for i, digest := range []string{subBalanceAuthDigest, subBalanceEligibilityDigest} {
		facts[i+1].Status = 403
		facts[i+1].UpstreamErrorSHA256 = digest
	}
	// Authentication can reject before the site's correlation middleware ran.
	facts[1].BillingRequestIDs = nil
	if err := s.engine.Import(ctx, "rejected", "1", facts); err != nil {
		t.Fatal(err)
	}
	storeAttributedLog(t, s, account, "paid", "1.25", 1)
	read := func() attributedSpend {
		t.Helper()
		rows, err := s.attributedChannelSpend(ctx, owner, QueryFilter{SourceID: "source"}, now.Add(-time.Hour).Unix(), now.Unix())
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		return rows[0]
	}
	if row := read(); row.Status != "complete" || row.Amount == nil || *row.Amount != 1.25 || row.Matched != 1 || row.ConfirmedUnbilled != 2 || row.Total != 3 || row.Pending != 0 {
		t.Fatal(row)
	}
	// Error evidence survives checkpoints without changing request metrics.
	cp := newCheckpointStore(&fakeStateObjects{}, Config{StateBucket: "state"})
	if err := cp.save(ctx, s.engine); err != nil {
		t.Fatal(err)
	}
	fresh := stateTestEngine(t)
	if ok, err := cp.restore(ctx, fresh); err != nil || !ok {
		t.Fatal(ok, err)
	}
	s.engine = fresh
	if row := read(); row.ConfirmedUnbilled != 2 {
		t.Fatal(row)
	}
	// A subsequently recorded bill takes precedence; never discard real charges.
	storeAttributedLog(t, s, account, "eligibility", "0.5", 2)
	if row := read(); row.Amount == nil || *row.Amount != 1.75 || row.Matched != 2 || row.ConfirmedUnbilled != 1 {
		t.Fatal(row)
	}
	rows, err := s.attributedChannelSpend(ctx, "foreign", QueryFilter{SourceID: "source"}, now.Add(-time.Hour).Unix(), now.Unix())
	if err != nil || rows[0].Amount != nil || rows[0].ConfirmedUnbilled != 0 {
		t.Fatal(rows, err)
	}
}

func TestUnprovenFailuresCannotBecomeZeroCost(t *testing.T) {
	for _, mode := range []string{"403_only", "wrong_body", "200_semantic_failure", "unbound", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			s, _, owner := attributionFixture(t)
			now := time.Now().Truncate(time.Second)
			f := attributedFact("rejected", "caller", now.Add(-time.Minute))
			f.Status = 403
			f.UpstreamErrorSHA256 = subBalanceAuthDigest
			switch mode {
			case "403_only":
				f.UpstreamErrorSHA256 = ""
			case "wrong_body":
				f.UpstreamErrorSHA256 = tokenHash("Insufficient account balance")
			case "200_semantic_failure":
				f.Status = 200
			case "unbound":
				f.UpstreamKeyHash = tokenHash("other-key")
			}
			facts := []Fact{f}
			if mode == "duplicate" {
				other := f
				other.EventID = "other"
				other.KeyID = "other-caller"
				facts = append(facts, other)
			}
			if err := s.engine.Import(context.Background(), "bad", "1", facts); err != nil {
				t.Fatal(err)
			}
			rows, err := s.attributedChannelSpend(context.Background(), owner, QueryFilter{SourceID: "source", KeyID: "caller"}, now.Add(-time.Hour).Unix(), now.Unix())
			if err != nil || len(rows) != 1 || rows[0].Amount != nil || rows[0].ConfirmedUnbilled != 0 {
				t.Fatal(rows, err)
			}
		})
	}
}

func TestHistoricalErrorSupplementRequiresExactAttemptAndSource(t *testing.T) {
	s, _, owner := attributionFixture(t)
	now := time.Now().Truncate(time.Second)
	f := attributedFact("historical"+randomID(), "caller", now.Add(-time.Minute))
	f.Status = 403
	if err := s.engine.Import(context.Background(), "old", "1", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	_, err := s.control.db.Exec(`INSERT INTO console_billing_error_evidence(source_id,event_id,request_id,attempt_id,provider,status,error_sha256,log_sha256,provenance) VALUES($1,$2,$3,$4,$5,403,$6,$6,'fixture')`, f.SourceID, f.EventID, f.RequestID, "wrong-attempt", f.Provider, subBalanceAuthDigest)
	if err != nil {
		t.Fatal(err)
	}
	defer s.control.db.Exec(`DELETE FROM console_billing_error_evidence WHERE event_id=$1`, f.EventID)
	read := func() attributedSpend {
		rows, err := s.attributedChannelSpend(context.Background(), owner, QueryFilter{SourceID: "source"}, now.Add(-time.Hour).Unix(), now.Unix())
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		return rows[0]
	}
	if row := read(); row.Amount != nil || row.ConfirmedUnbilled != 0 {
		t.Fatal(row)
	}
	_, err = s.control.db.Exec(`UPDATE console_billing_error_evidence SET attempt_id=$2,source_id='wrong-source' WHERE event_id=$1`, f.EventID, f.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if row := read(); row.Amount != nil {
		t.Fatal(row)
	}
	_, err = s.control.db.Exec(`UPDATE console_billing_error_evidence SET source_id=$2 WHERE event_id=$1`, f.EventID, f.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	if row := read(); row.Amount == nil || *row.Amount != 0 || row.ConfirmedUnbilled != 1 {
		t.Fatal(row)
	}
}

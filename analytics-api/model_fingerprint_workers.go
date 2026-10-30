package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

const fingerprintConcurrency = 50
const fingerprintSiteWorkers = "site-workers-v1"

var errFingerprintSiteBusy = errors.New("fingerprint_site_busy")

// Paths, credentials, protocol variants and account IDs do not create separate
// concurrency budgets for one upstream host. Non-default ports remain distinct.
func fingerprintSite(base string) string {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	host := strings.TrimRight(strings.ToLower(u.Hostname()), ".")
	port := u.Port()
	if port != "" && !((u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80")) {
		return net.JoinHostPort(host, port)
	}
	return host
}

func (s *Service) fingerprintLoop(ctx context.Context) {
	defer sharedFingerprintWorker.close()
	var wg sync.WaitGroup
	defer wg.Wait()
	finished := make(chan struct{}, 1)
	for ctx.Err() == nil {
		claim, done := context.WithTimeout(ctx, 5*time.Second)
		target, run, err := s.claimFingerprint(claim)
		done()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-finished:
			case <-time.After(2 * time.Second):
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runClaimedFingerprint(ctx, target, run)
			select {
			case finished <- struct{}{}:
			default:
			}
		}()
	}
}

// This row protects short scheduling transactions, not the probes. Its lease
// also fences old singleton workers during a rolling release (and rollback).
func (s *Service) fingerprintWorkerTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT deadline IS NULL OR deadline<now() OR run_id=$1 FROM console_fingerprint_worker WHERE id=true FOR UPDATE`, fingerprintSiteWorkers).Scan(&allowed)
	if err != nil || !allowed {
		tx.Rollback()
		if err == nil {
			err = sql.ErrNoRows
		}
		return nil, err
	}
	return tx, nil
}

func updateFingerprintWorker(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE console_fingerprint_worker SET run_id=$1,deadline=(SELECT max(deadline) FROM console_fingerprint_site_leases) WHERE id=true`, fingerprintSiteWorkers)
	return err
}

func (s *Service) claimFingerprint(ctx context.Context) (fingerprintTarget, string, error) {
	var target fingerprintTarget
	tx, err := s.fingerprintWorkerTx(ctx)
	if err != nil {
		return target, "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE console_model_fingerprints SET state='interrupted',message='检测中断，请重新检测',deadline=NULL WHERE state='running' AND deadline<now()`); err != nil {
		return target, "", err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM console_fingerprint_site_leases WHERE deadline<now()`); err != nil {
		return target, "", err
	}
	rows, err := tx.QueryContext(ctx, `SELECT site FROM console_fingerprint_site_leases`)
	if err != nil {
		return target, "", err
	}
	busy := map[string]bool{}
	for rows.Next() {
		var site string
		if err = rows.Scan(&site); err != nil {
			break
		}
		busy[site] = true
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil {
		return target, "", err
	}
	if readErr != nil {
		return target, "", readErr
	}
	site := ""
	if len(busy) < fingerprintConcurrency {
		rows, err = tx.QueryContext(ctx, `SELECT COALESCE(f.source_id,''),f.provider,COALESCE(f.account_id,''),f.group_id,f.model,f.site_key,COALESCE(a.base,c.base,'')
 FROM console_model_fingerprints f LEFT JOIN console_sub_accounts a ON a.id=f.account_id
 LEFT JOIN console_configured_channels c ON c.source_id=f.source_id AND c.provider=f.provider AND c.active
 WHERE f.state='queued' ORDER BY f.updated_at,f.scope,f.model FOR UPDATE OF f SKIP LOCKED`)
		if err != nil {
			return target, "", err
		}
		for rows.Next() {
			var candidate fingerprintTarget
			var hint, base string
			if err = rows.Scan(&candidate.Source, &candidate.Provider, &candidate.Account, &candidate.Group, &candidate.Model, &hint, &base); err != nil {
				break
			}
			if hint == "" {
				hint = fingerprintSite(base)
			}
			if hint == "" {
				// No inventory yet: reserve only a preparation slot. The fresh
				// configuration must acquire the real site before any paid I/O.
				hint = "unresolved:" + tokenHash(candidate.scope())
			}
			if !busy[hint] {
				target, site = candidate, hint
				break
			}
		}
		readErr = rows.Err()
		rows.Close()
		if err != nil {
			return target, "", err
		}
		if readErr != nil {
			return target, "", readErr
		}
	}
	run := ""
	if site != "" {
		run = randomID()
		_, err = tx.ExecContext(ctx, `UPDATE console_model_fingerprints SET state='running',run_id=$3,site_key=$4,deadline=now()+interval '30 seconds' WHERE scope=$1 AND model=$2`, target.scope(), target.Model, run, site)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO console_fingerprint_site_leases(run_id,site,deadline) VALUES($1,$2,now()+interval '30 seconds')`, run, site)
		}
		if err != nil {
			return target, "", err
		}
	}
	if err = updateFingerprintWorker(ctx, tx); err != nil {
		return target, "", err
	}
	if err = tx.Commit(); err != nil {
		return target, "", err
	}
	if run == "" {
		return target, "", sql.ErrNoRows
	}
	return target, run, nil
}

func (s *Service) bindFingerprintSite(ctx context.Context, t fingerprintTarget, run, site string) error {
	tx, err := s.fingerprintWorkerTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var alive bool
	err = tx.QueryRowContext(ctx, `UPDATE console_model_fingerprints SET site_key=$4 WHERE scope=$1 AND model=$2 AND run_id=$3 AND state='running' AND deadline>now()
 AND EXISTS(SELECT 1 FROM console_fingerprint_site_leases WHERE run_id=$3 AND deadline>now()) RETURNING true`, t.scope(), t.Model, run, site).Scan(&alive)
	if err != nil {
		return err
	}
	var busy bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_fingerprint_site_leases WHERE site=$1 AND run_id<>$2 AND deadline>now())`, site, run).Scan(&busy); err != nil {
		return err
	}
	if !busy {
		// An expired reservation must not prevent reclaiming the site.
		if _, err = tx.ExecContext(ctx, `DELETE FROM console_fingerprint_site_leases WHERE site=$1 AND run_id<>$2 AND deadline<now()`, site, run); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE console_fingerprint_site_leases SET site=$2 WHERE run_id=$1`, run, site); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if busy {
		return errFingerprintSiteBusy
	}
	return nil
}

func (s *Service) renewFingerprint(ctx context.Context, t fingerprintTarget, run string) error {
	tx, err := s.fingerprintWorkerTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var alive bool
	err = tx.QueryRowContext(ctx, `WITH live AS (UPDATE console_model_fingerprints SET deadline=now()+interval '30 seconds'
 WHERE scope=$1 AND model=$2 AND run_id=$3 AND state='running' AND deadline>now() RETURNING run_id)
 UPDATE console_fingerprint_site_leases SET deadline=now()+interval '30 seconds' WHERE run_id=$3 AND deadline>now() AND EXISTS(SELECT 1 FROM live) RETURNING true`, t.scope(), t.Model, run).Scan(&alive)
	if err != nil {
		return err
	}
	if err = updateFingerprintWorker(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) finishFingerprint(ctx context.Context, t fingerprintTarget, run, state, message, revision string, result json.RawMessage) error {
	tx, err := s.fingerprintWorkerTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw any
	if len(result) > 0 {
		raw = string(result)
	}
	// Preparation can fail or requeue before startFingerprintSamples validates the
	// saved evidence. Without a new result, retain its original configuration ID.
	_, err = tx.ExecContext(ctx, `UPDATE console_model_fingerprints SET state=$4,message=$5,result=COALESCE($6,result),revision=CASE WHEN $6 IS NULL OR $7='' THEN revision ELSE $7 END,deadline=NULL,
 updated_at=CASE WHEN $4='queued' THEN updated_at ELSE now() END
 WHERE scope=$1 AND model=$2 AND run_id=$3 AND state='running' AND (deadline>now() OR $4='interrupted')`, t.scope(), t.Model, run, state, message, raw, revision)
	if err != nil {
		return err
	}
	// Cancellation keeps the reservation until the process has actually stopped.
	if _, err = tx.ExecContext(ctx, `DELETE FROM console_fingerprint_site_leases WHERE run_id=$1`, run); err != nil {
		return err
	}
	if err = updateFingerprintWorker(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) fingerprintOne(parent context.Context) bool {
	claimCtx, claimDone := context.WithTimeout(parent, 5*time.Second)
	t, run, err := s.claimFingerprint(claimCtx)
	claimDone()
	if err != nil {
		return false
	}
	s.runClaimedFingerprint(parent, t, run)
	return true
}

func (s *Service) runClaimedFingerprint(parent context.Context, t fingerprintTarget, run string) {
	ctx, cancel := context.WithTimeout(parent, 780*time.Second)
	heartbeat := make(chan struct{})
	go func() {
		defer close(heartbeat)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pulse, done := context.WithTimeout(ctx, 3*time.Second)
				e := s.renewFingerprint(pulse, t, run)
				done()
				if e != nil {
					cancel()
					return
				}
			}
		}
	}()
	plan, err := s.prepareFingerprint(ctx, t)
	var result json.RawMessage
	if err == nil {
		err = s.bindFingerprintSite(ctx, t, run, plan.site)
	}
	var resume json.RawMessage
	if err == nil {
		resume, err = s.startFingerprintSamples(ctx, t, run, plan)
	}
	if err == nil {
		log.Printf("model_fingerprint_started run=%q site=%q model=%q", run, plan.site, t.Model)
		result, err = runFingerprintViaTransportProgress(ctx, &plan.client, plan.config, resume, func(public, samples json.RawMessage) error {
			return s.saveFingerprintSamples(ctx, t, run, plan.revision, plan.sampleKey, public, samples)
		})
	}
	state, message := "done", ""
	if errors.Is(err, errFingerprintSiteBusy) {
		// Preparation made no billable call. Remember the resolved site and let
		// the scheduler skip this job while another channel on its site runs.
		state = "queued"
	} else if err != nil {
		state, message = "error", err.Error()
	}
	if ctx.Err() != nil {
		state, message = "interrupted", "检测中断或超时，未自动重发请求"
	}
	cancel()
	<-heartbeat
	finish, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	e := s.finishFingerprint(finish, t, run, state, message, plan.revision, result)
	log.Printf("model_fingerprint run=%q site=%q model=%q state=%s saved=%t", run, plan.site, t.Model, state, e == nil)
}

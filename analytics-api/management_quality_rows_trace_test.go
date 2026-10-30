package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"testing"
)

func managementQualityTraceFixture(t *testing.T) *Service {
	t.Helper()
	s := managementBindingTestService(t)
	for _, query := range []string{
		`INSERT INTO console_sources(id,name,base,encrypted_key) VALUES('quality-source','fixture','https://private.test','')`,
		`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('quality-a','private-owner','fixture','https://private.test','private-email-a',''),('quality-b','private-owner','fixture','https://private.test','private-email-b',''),('quality-foreign','foreign-owner','fixture','https://private.test','private-email-f','')`,
		`INSERT INTO console_sub_targets(account_id,group_id,name,platform) SELECT a.id,g,'fixture','openai' FROM console_sub_accounts a CROSS JOIN generate_series(1,5) g`,
		`INSERT INTO console_quality_history(check_id,account_id,group_id,checked_at,verdict,successful,result) VALUES
 ('old','quality-a',1,100,'pass',true,'{"quality":{"text":"private-old"}}'),
 ('tie-first','quality-a',1,200,'pass',true,'{"quality":{"text":"private-tie-first"}}'),
 ('tie-last','quality-a',1,200,'fail',true,'{"quality":{"text":"private-tie-last","message":"private-message","duration_ms":12}}'),
 ('pending','quality-a',1,300,'running',false,'{}'),
 ('only-running','quality-a',2,300,'running',false,'{}'),
 ('json-null','quality-a',3,400,'error',false,'null'),
 ('sibling','quality-b',1,500,'fail',true,'{"quality":{"text":"private-sibling"}}'),
 ('foreign','quality-foreign',1,600,'pass',true,'{"quality":{"text":"private-foreign"}}')`,
		`INSERT INTO console_quality_history(check_id,source_id,provider,checked_at,verdict,successful,result) VALUES
 ('linked','quality-source','private-provider',700,'inconclusive',true,'{"verdict":"inconclusive","checked_at":700,"text":"private-linked"}'),
 ('unlinked','quality-source','private-provider',800,'pass',true,'{"verdict":"pass","text":"private-unlinked"}')`,
		`INSERT INTO console_quality_group_links(check_id,account_id,group_id) VALUES('linked','quality-a',4)`,
	} {
		if _, err := s.control.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestManagementSharedQualityTracePreservesProjectionAndScope(t *testing.T) {
	s := managementQualityTraceFixture(t)
	bindings := []qualityBinding{{Source: "quality-source", Provider: "private-provider", Account: "quality-a", Group: 4}}
	for _, scope := range []struct {
		owner, account string
		groups         int
	}{
		{"private-owner", "", 5}, {"private-owner", "quality-a", 4},
		{"private-owner", "quality-b", 1}, {"private-owner", "quality-foreign", 0},
		{"foreign-owner", "", 1}, {"absent", "", 0},
	} {
		t.Run(scope.owner+"/"+scope.account, func(t *testing.T) {
			read := func(traced bool) (sharedQuality, string) {
				ctx := context.Background()
				if traced {
					ctx = context.WithValue(managementTraceContext(ctx), consoleRequestIDKey{}, "quality-trace-123")
				}
				var output bytes.Buffer
				previous := log.Writer()
				log.SetOutput(&output)
				defer log.SetOutput(previous)
				got, err := readSharedQualityProjectionFrom(ctx, s.control.db, scope.owner, scope.account, bindings)
				if err != nil {
					t.Fatal(err)
				}
				return got, output.String()
			}
			want, untraced := read(false)
			got, output := read(true)
			if !reflect.DeepEqual(got, want) || len(got.Groups) != scope.groups || untraced != "" {
				t.Fatal("trace changed quality groups, binding identity or untraced logging")
			}
			var rawBytes int
			if err := s.control.db.QueryRow(`WITH owned AS (
 SELECT h.* FROM console_quality_group_history h JOIN console_sub_accounts a ON a.id=h.account_id
 WHERE a.owner=$1 AND ($2='' OR a.id=$2)), latest AS (
 SELECT DISTINCT ON(account_id,group_id) result FROM owned WHERE verdict<>'running' ORDER BY account_id,group_id,checked_at DESC,id DESC)
 SELECT COALESCE(sum(octet_length(result::text)),0) FROM latest`, scope.owner, scope.account).Scan(&rawBytes); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"stage=shared_quality_rows ", "query_ready_ms=", "decode_ms=", "elapsed_ms=", fmt.Sprintf("rows=%d ", scope.groups), fmt.Sprintf("bytes=%d ", rawBytes), "advance_ms=", "canceled=false", "request_id=quality-trace-123"} {
				if !strings.Contains(output, field) {
					t.Fatal("quality trace split missing", field, output)
				}
			}
			if strings.Count(output, "stage=shared_quality_rows ") != 1 {
				t.Fatal("quality trace duplicated")
			}
			for _, private := range []string{"private-", "quality-a", "quality-b", "quality-foreign", "quality-source", "foreign-owner", "SELECT", "console_quality"} {
				if strings.Contains(output, private) {
					t.Fatal("quality trace leaked business values", private)
				}
			}
			if scope.owner == "private-owner" && scope.account == "" {
				p := got.Groups[qualityGroupID("quality-a", 1)]
				if p.History != (qualitySummary{Total: 4, Successful: 3, Passed: 2}) || p.Check == nil || p.Check.Text != "private-tie-last" || p.Check.CheckedAt != 200 || got.Groups[qualityGroupID("quality-a", 2)].Check != nil {
					t.Fatal("running exclusion, tie break or totals changed", p)
				}
				c := got.channel("quality-source", "private-provider")
				if c == nil || c.Text != "private-linked" || c.CheckedAt != 700 || c.HistoryScope != "account_group" || c.History.Total != 1 {
					t.Fatal("historical channel provenance or shared origin changed", c)
				}
			}
		})
	}
	var links int
	if err := s.control.db.QueryRow(`SELECT count(*) FROM console_quality_group_links`).Scan(&links); err != nil || links != 1 {
		t.Fatal("read attributed unlinked history", links, err)
	}
}

func TestManagementSharedQualityTracePreservesDecodeErrorsAndCancellation(t *testing.T) {
	s := managementQualityTraceFixture(t)
	for _, tc := range []struct {
		name, id, raw string
		cancel        bool
	}{
		{name: "native_error", id: "tie-last", raw: `{"quality":{"duration_ms":"private-value"}}`},
		{name: "channel_error", id: "linked", raw: `{"checked_at":"private-value"}`},
		{name: "cancel", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := s.control.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if tc.id != "" {
				if _, err = tx.Exec(`UPDATE console_quality_history SET result=$2 WHERE check_id=$1`, tc.id, tc.raw); err != nil {
					t.Fatal(err)
				}
			}
			read := func(traced bool) (sharedQuality, error, string) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if traced {
					ctx = context.WithValue(managementTraceContext(ctx), consoleRequestIDKey{}, "quality-error-123")
				}
				if tc.cancel {
					cancel()
				}
				var output bytes.Buffer
				previous := log.Writer()
				log.SetOutput(&output)
				defer log.SetOutput(previous)
				got, err := readSharedQualityProjectionFrom(ctx, tx, "private-owner", "quality-a", nil)
				return got, err, output.String()
			}
			want, wantErr, untraced := read(false)
			got, gotErr, output := read(true)
			if wantErr == nil || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || fmt.Sprint(errors.Unwrap(gotErr)) != fmt.Sprint(errors.Unwrap(wantErr)) || !reflect.DeepEqual(got, want) || untraced != "" {
				t.Fatal("quality trace changed partial results or original error", wantErr, gotErr)
			}
			if strings.Count(output, "stage=shared_quality_rows ") != 1 || !strings.Contains(output, "request_id=quality-error-123") || strings.Contains(output, "private-") || strings.Contains(output, "SELECT") {
				t.Fatal("quality error trace missing, duplicated or exposed values", output)
			}
			if tc.cancel && !strings.Contains(output, "canceled=true") {
				t.Fatal("cancellation not reflected in quality trace")
			}
		})
	}
}

func TestManagementQualityTraceSeparatesBindingsFromProjection(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.WithValue(managementTraceContext(context.Background()), consoleRequestIDKey{}, "quality-stage-123")
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	if _, err := s.readManagementDatabaseFrom(ctx, s.control.db, "owner", nil); err != nil {
		t.Fatal(err)
	}
	last := -1
	for _, stage := range []string{"stage=database_quality_bindings ", "stage=shared_quality_rows ", "stage=database_quality "} {
		if strings.Count(output.String(), stage) != 1 {
			t.Fatal("quality phase missing or duplicated", stage)
		}
		at := strings.Index(output.String(), stage)
		if at <= last {
			t.Fatal("quality phase includes another phase", stage)
		}
		last = at
	}
}

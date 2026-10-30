package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestManagementModelSeekKeysAreBoundedOrderedAndSourceChecked(t *testing.T) {
	models := directoryModelsForTest(64)
	models[0].item.Group = -1 << 63
	positions := managementStreamPositionIndex{values: map[managementStreamIdentity]int{}, only: map[string]bool{}}
	for _, i := range []int{31, 0, 12} {
		item := models[i].item
		positions.only[item.Model.Model] = true
		positions.add(item, i)
	}
	before := mustJSON(models)
	keys, ok := managementModelSeekKeys(models, positions)
	want := []managementModelSeekKey{}
	for _, i := range []int{0, 12, 31} {
		item := models[i].item
		want = append(want, managementModelSeekKey{item.Account, item.Group, item.Model.Model})
	}
	if !ok || !reflect.DeepEqual(keys, want) || mustJSON(models) != before {
		t.Fatal("key order, exact source identities, or input changed")
	}
	keys[0].Model = "caller mutation"
	if next, _ := managementModelSeekKeys(models, positions); !reflect.DeepEqual(next, want) {
		t.Fatal("seek keys alias another call")
	}
	for _, mode := range []string{"legacy", "nil-index", "wide", "negative-position", "out-of-range", "changed-source", "off-scope"} {
		t.Run(mode, func(t *testing.T) {
			copyModels := slices.Clone(models)
			p := managementStreamPositionIndex{values: map[managementStreamIdentity]int{}, only: map[string]bool{}}
			for k, v := range positions.values {
				p.values[k] = v
			}
			for k, v := range positions.only {
				p.only[k] = v
			}
			var key managementStreamIdentity
			for k := range p.values {
				key = k
				break
			}
			switch mode {
			case "legacy":
				p.only = nil
			case "nil-index":
				p.values = nil
			case "wide":
				copyModels = copyModels[:16]
			case "negative-position":
				p.values[key] = -1
			case "out-of-range":
				p.values[key] = len(models)
			case "changed-source":
				copyModels[p.values[key]].item.Model.Model = "changed"
			case "off-scope":
				p.only[key.model] = false
			}
			if _, ok := managementModelSeekKeys(copyModels, p); ok {
				t.Fatal("unsafe source accepted")
			}
		})
	}
	empty, ok := managementModelSeekKeys(nil, managementStreamPositionIndex{values: map[managementStreamIdentity]int{}, only: map[string]bool{}})
	if !ok || empty == nil || len(empty) != 0 {
		t.Fatal("empty set lost JSON array shape")
	}
	for _, n := range []int{4096, 4097} {
		many := directoryModelsForTest(n * 8)
		for i := range many {
			many[i].item.Account, many[i].item.Group, many[i].item.Model.Model = "a", int64(i), "m"
		}
		p := managementStreamPositionIndex{values: map[managementStreamIdentity]int{}, only: map[string]bool{}}
		for i := 0; i < n; i++ {
			p.only[many[i].item.Model.Model] = true
			p.add(many[i].item, i)
		}
		if _, ok := managementModelSeekKeys(many, p); ok != (n == 4096) {
			t.Fatal("probe bound changed", n)
		}
	}
	long := directoryModelsForTest(64)
	long[0].item.Model.Model = strings.Repeat("x", 1<<20)
	p := managementStreamPositionIndex{values: map[managementStreamIdentity]int{}, only: map[string]bool{long[0].item.Model.Model: true}}
	p.add(long[0].item, 0)
	if _, ok := managementModelSeekKeys(long, p); ok {
		t.Fatal("unbounded single dictionary string expanded into seek payload")
	}
	long = directoryModelsForTest(512)
	p = managementStreamPositionIndex{values: map[managementStreamIdentity]int{}, only: map[string]bool{}}
	for i := 0; i < 64; i++ {
		long[i].item.Group, long[i].item.Model.Model = int64(i), strings.Repeat("\x01", 3000)
		p.only[long[i].item.Model.Model] = true
		p.add(long[i].item, i)
	}
	if _, ok := managementModelSeekKeys(long, p); ok {
		t.Fatal("repeated/escaped dictionary strings exceeded seek byte budget")
	}
}

func TestManagementSignalSeekKeepsStreamsAndMissingEvidenceFallback(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state)
 SELECT 'a',g,'off-scope-'||m,'idle' FROM generate_series(1,61) g CROSS JOIN generate_series(1,32) m`); err != nil {
		t.Fatal(err)
	}
	filters := managementQueryFilters{GroupStatus: "available", Model: "gpt-5.5"}
	page, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", false)
	if err != nil {
		t.Fatal(err)
	}
	full, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(page.Page.Rows, full.Page.Rows) || page.Page.Revision != full.Page.Revision ||
		page.Page.Total != full.Page.Total || page.Page.EligibleCount != full.Page.EligibleCount ||
		!reflect.DeepEqual(page.Page.Models, full.Page.Models) || !reflect.DeepEqual(page.Page.DetectionModels, full.Page.DetectionModels) {
		t.Fatal("seek page changed hydrated rows, revision, full selection or model facets")
	}
	compare := func(names []string, strict bool, wantErr error) {
		t.Helper()
		tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		scope := managementStreamScope{CompactRevision: true, Model: "gpt-5.5", Names: names}
		want, wantIDs, wantError := readManagementStreamModels(ctx, tx, "owner", strict, scope)
		directory := &managementDirectoryScope{store: &managementModelDirectories{}, database: s.control.db, tx: tx, owner: "owner", ready: true, accountRevision: "snapshot", modelRevision: 1}
		current := context.WithValue(ctx, managementDirectoryScopeKey{}, directory)
		var got []managementStreamModel
		var ids []string
		var actualError error
		var traces bytes.Buffer
		func() {
			previous := log.Writer()
			log.SetOutput(&traces)
			defer log.SetOutput(previous)
			got, ids, actualError = readManagementStreamModels(managementTraceContext(current), tx, "owner", strict, scope)
		}()
		if !strings.Contains(traces.String(), "management_signal_read primary_key_seek=true ") {
			t.Fatal("fixture did not execute the primary-key seek", traces.String())
		}
		if !errors.Is(actualError, wantErr) || !errors.Is(wantError, wantErr) {
			t.Fatal("fallback behavior changed", actualError, wantError, wantErr)
		}
		if wantErr != nil {
			return
		}
		slices.Sort(wantIDs)
		slices.Sort(ids)
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(ids, wantIDs) {
			t.Fatal("directory, predicates, or receipt membership changed")
		}
		p := managementStreamPositionIndex{values: map[managementStreamIdentity]int{}, only: map[string]bool{}}
		for _, name := range names {
			p.only[name] = true
		}
		if directory.directory == nil || !directory.directory.addModelPositions(p.only, p) {
			t.Fatal("versioned directory was not used")
		}
		keys, ok := managementModelSeekKeys(directory.directory.expand(), p)
		if !ok || len(keys) != len(p.values) {
			t.Fatal("fixture did not enter narrow seek branch")
		}
	}
	for _, names := range [][]string{nil, {}, {"missing"}, {"gpt-5.5", "gpt-6-astra"}, {"gpt-5.5", "gpt-5.5", "extra-model"}} {
		compare(names, true, nil)
	}
	for _, mutation := range []string{
		`UPDATE console_management_model_signal SET valid=false WHERE account_id='a' AND group_id=1 AND model='gpt-5.5'`,
		`UPDATE console_management_model_signal SET valid=true,evidence_digest=decode('ab','hex') WHERE account_id='a' AND group_id=1 AND model='gpt-5.5'`,
		`DELETE FROM console_management_model_signal WHERE account_id='a' AND group_id=1 AND model='gpt-5.5'`,
	} {
		if _, err := s.control.db.Exec(mutation); err != nil {
			t.Fatal(err)
		}
		compare([]string{"gpt-5.5", "gpt-6-astra"}, true, errManagementPredicateFallback)
	}
}

func TestManagementSignalSeekProductionCardinality(t *testing.T) {
	if os.Getenv("RUN_MANAGEMENT_SIGNAL_SEEK_SCALE") != "1" {
		t.Skip("opt-in local synthetic fixture; not an HTTP acceptance test")
	}
	db := migrationTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := db.ExecContext(ctx, `CREATE TABLE console_sub_accounts(id text PRIMARY KEY,owner text NOT NULL);
 CREATE TABLE console_management_model_signal(
 account_id text,group_id bigint,model text,valid boolean,present boolean,evidence_digest bytea,
 checked_at bigint,verdict text,availability text,terminal text,model_match text,probe_id text,quality_probe_id text,
 has_usage boolean,usage_status text,input_price double precision,output_price double precision,input_tokens bigint,output_tokens bigint,
 PRIMARY KEY(account_id,group_id,model));
 INSERT INTO console_sub_accounts SELECT 'account-'||n,CASE WHEN n=48 THEN 'foreign' ELSE 'owner' END FROM generate_series(0,48) n;
 INSERT INTO console_management_model_signal
 SELECT 'account-'||(g%49),g,CASE m WHEN 1 THEN 'gpt-6.1-terra' WHEN 2 THEN 'gpt-6-astra' ELSE 'model-'||m END,
 true,m%3=0,decode(repeat('ab',32),'hex'),g,'pass','success','complete','match','probe-'||g||'-'||m,'',true,'matched',2,3,100,20
 FROM generate_series(1,768) g CROSS JOIN generate_series(1,248) m;
 ANALYZE console_sub_accounts; ANALYZE console_management_model_signal;`)
	if err != nil {
		t.Fatal(err)
	}
	wanted := []managementModelSeekKey{}
	for group := int64(1); group <= 768; group++ {
		for _, model := range []string{"gpt-6.1-terra", "gpt-6-astra"} {
			wanted = append(wanted, managementModelSeekKey{fmt.Sprint("account-", group%49), group, model})
		}
	}
	// Include foreign identities too: SQL must independently enforce ownership.
	queries := []struct {
		name, query string
		args        []any
	}{
		{"model_any", managementModelStreamSQL + ` AND p.model=ANY($2::text[])`, []any{"owner", []string{"gpt-6.1-terra", "gpt-6-astra"}}},
		{"primary_key_seek", managementModelSeekSQL, []any{"owner", mustJSON(wanted)}},
	}
	var baseline []string
	for round := 0; round < 3; round++ {
		for _, tc := range queries {
			start := time.Now()
			rows, err := db.QueryContext(ctx, tc.query, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			columns, _ := rows.Columns()
			out := []string{}
			for rows.Next() {
				values, pointers := make([]any, len(columns)), make([]any, len(columns))
				for i := range values {
					pointers[i] = &values[i]
				}
				if err := rows.Scan(pointers...); err != nil {
					t.Fatal(err)
				}
				out = append(out, mustJSON(values))
			}
			readErr := rows.Err()
			rows.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			t.Logf("synthetic pass=%d mode=%s rows=%d elapsed=%s", round, tc.name, len(out), time.Since(start))
			sort.Strings(out)
			if baseline == nil {
				baseline = out
			}
			if !reflect.DeepEqual(out, baseline) {
				t.Fatal("predicate rows or ownership changed", tc.name)
			}
			if round == 0 {
				logManagementSignalSeekPlan(t, ctx, db, tc.name, tc.query, tc.args)
			}
		}
	}
}

func logManagementSignalSeekPlan(t *testing.T, ctx context.Context, db *sql.DB, name, query string, args []any) {
	t.Helper()
	rows, err := db.QueryContext(ctx, "EXPLAIN (ANALYZE,BUFFERS,FORMAT TEXT) "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := []string{}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s plan:\n%s", name, strings.Join(plan, "\n"))
}

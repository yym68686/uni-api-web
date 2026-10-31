package main

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func managementHydrationFixture(t *testing.T) *Service {
	t.Helper()
	s := managementBindingTestService(t)
	s.engine = postgresTestEngine(t)
	db := s.control.db
	exec := func(q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE storage_state(singleton boolean PRIMARY KEY,ingest_limit_bytes bigint NOT NULL)`)
	exec(`INSERT INTO storage_state VALUES(true,pg_database_size(current_database())+1000000000)`)
	exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('a','owner','fixture','https://a.test','fixture','')`)
	exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,active,state) SELECT 'a',g,'G'||g,'openai',g,true,'done' FROM generate_series(1,61) g`)
	exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,message,result)
 SELECT 'a',g,m,'done','model message',jsonb_build_object('model',m,'checked_at',g,'verdict','pass',
 'availability',jsonb_build_object('id','a-'||g||'-'||m,'status','success','protocol','responses','model_match','match','response_model',m,'ttft_ms',123,'message','current diagnostic'),
 'quality',jsonb_build_object('id','q-'||g||'-'||m,'status','success','message','quality diagnostic'))
 FROM generate_series(1,61) g CROSS JOIN unnest(ARRAY['gpt-5.5','gpt-6-astra','extra-model']) m`)
	exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result)
 SELECT prefix||g||'-'||m,'a',g,g,m,0,'[]','matched','{"input_price":2,"output_price":3}'
 FROM generate_series(1,61) g CROSS JOIN unnest(ARRAY['gpt-5.5','gpt-6-astra','extra-model']) m CROSS JOIN unnest(ARRAY['a-','q-']) prefix`)
	var schema string
	if err := db.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if err := prepareManagementProjection(context.Background(), db, schema, 0, nil); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestManagementProjectedPageHydratesOnlyItsGroupsAndKeepsCompleteSelection(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	f := managementQueryFilters{GroupStatus: "available", Model: "gpt-5.5"}
	page, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
	if err != nil {
		t.Fatal(err)
	}
	full, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !page.View.ProjectedModels || full.View.ProjectedModels || page.Page.Total != 61 || page.Page.EligibleCount != 61 || len(page.Page.Rows) != 25 || len(full.Rows) != 61 {
		t.Fatal("incorrect page/selection scope")
	}
	if page.Page.Revision != full.Page.Revision || !reflect.DeepEqual(page.Page.Rows, full.Page.Rows) {
		t.Fatal("hydrated display or shared revision differs from full read")
	}
	if !reflect.DeepEqual(managementContextForPage(page.View, page.Page, f.Model), managementContextForPage(full.View, full.Page, f.Model)) {
		t.Fatal("page metadata differs from full read")
	}
	for i, target := range page.View.Input.Accounts[0].Targets {
		for _, model := range target.Models {
			hydrated := model.Result != nil && model.Result.Availability.TTFT != nil
			if hydrated != (i < 25) {
				t.Fatal("hydration read outside the page or omitted a page model", i)
			}
		}
	}
	token := encodeManagementCursor(s.control.key, "owner", managementPageCursor{Version: 1, Revision: page.Page.Revision, Filter: tokenHash(mustJSON(f))})
	selection, err := s.readManagementQuerySnapshot(ctx, "owner", f, token, true)
	if err != nil || len(filterManagementQueryRows(selection.Rows, f, selection.Evidence).Rows) != 61 {
		t.Fatal("page token did not authorize complete selection", err)
	}
	// Latency is absent from predicates, but still affects optimization. An
	// off-page change must invalidate both pagination and full-selection tokens.
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','987') WHERE group_id=61 AND model='gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	if stale, err := s.readManagementQuerySnapshot(ctx, "owner", f, token, true); !errors.Is(err, errManagementRevision) || stale.Page.Revision != "" {
		t.Fatal("off-page display evidence change accepted", err)
	}
}

func TestManagementHydrationRemainsInsidePredicateSnapshot(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	f := managementQueryFilters{GroupStatus: "available", Model: "gpt-5.5"}
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	view, err := s.readManagementDatabaseViewFrom(ctx, tx, "owner", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := assembleManagementGatewayView(&view, nil, "owner"); err != nil {
		t.Fatal(err)
	}
	rows, err := buildManagementQueryRows(view.Input, f.Model, f.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	evidence := managementQueryEvidence(view.Input, rows, f.Model, view.Prices)
	page, err := buildManagementQueryPageFromRows(view.Input, rows, f, evidence, "owner", s.control.key, "", view.ReadAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','987') WHERE group_id=1 AND model='gpt-5.5'; UPDATE console_sub_usage SET result='{"input_price":9}' WHERE id='a-1-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	if err := s.hydrateManagementPageFrom(ctx, tx, "owner", &view, &page, f); err != nil {
		t.Fatal(err)
	}
	check := page.Rows[0].Selected.Result.Availability
	if *check.TTFT != 123 || *check.Usage.InputPrice != 2 {
		t.Fatal("hydration mixed later evidence with earlier predicates")
	}
	fresh, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
	if err != nil || *fresh.Page.Rows[0].Selected.Result.Availability.TTFT != 987 || *fresh.Page.Rows[0].Selected.Result.Availability.Usage.InputPrice != 9 || fresh.Page.Revision == page.Revision {
		t.Fatal("new read did not publish current evidence", err)
	}
}

func TestManagementHydrationPreservesByteOrderedModelsAndLegacySummary(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result)
 SELECT 'a',1,m,'done',jsonb_build_object('model',m,'availability',jsonb_build_object('status','success','ttft_ms',456))
 FROM unnest(ARRAY['Z-model','a-model','é-model','模型🙂']) m;
 DELETE FROM console_sub_model_summaries WHERE account_id='a' AND group_id=1 AND model='模型🙂'`); err != nil {
		t.Fatal(err)
	}
	f := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	page, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
	if err != nil {
		t.Fatal(err)
	}
	full, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", true)
	if err != nil || !reflect.DeepEqual(page.Page.Rows, full.Page.Rows) {
		t.Fatal("streaming page changed model order or omitted legacy evidence", err)
	}
	models := page.Page.Rows[0].Target.Models
	for i := 1; i < len(models); i++ {
		if models[i-1].Model >= models[i].Model {
			t.Fatal("model names lost byte ordering")
		}
	}
	if last := models[len(models)-1]; last.Model != "模型🙂" || last.Result == nil || *last.Result.Availability.TTFT != 456 {
		t.Fatal("legacy summary was not preserved")
	}
}

func TestManagementProjectedReadFallsBackToFullDecoderWithoutFalseError(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	f := managementQueryFilters{GroupStatus: "available", Model: "gpt-5.5"}
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result='{"Status":"matched","Input_Price":7}' WHERE id='a-1-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	page, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
	if err != nil || page.View.ProjectedModels || *page.Page.Rows[0].Selected.Result.Availability.Usage.InputPrice != 7 {
		t.Fatal("valid legacy evidence was rejected or replaced", err)
	}
	full, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", true)
	if err != nil || page.Page.Revision != full.Page.Revision || !reflect.DeepEqual(page.Page.Rows, full.Page.Rows) {
		t.Fatal("fallback lost shared revision or current values", err)
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result='{"message":false}' WHERE id='q-61-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	if partial, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false); err == nil || partial.Page.Revision != "" {
		t.Fatal("malformed off-page quality receipt published partial current data")
	}
}

func TestManagementRevisionIncludesQueueAndQualityReceiptsButNotReceiptPollClock(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	read := func() string {
		t.Helper()
		revision, err := readManagementModelRevisionFrom(ctx, s.control.db, "owner")
		if err != nil {
			t.Fatal(err)
		}
		return revision
	}
	initial := read()
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result=result||'{"checked_at":1234}' WHERE id='q-1-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	if read() != initial {
		t.Fatal("receipt poll clock invalidated unchanged summary")
	}
	for _, q := range []string{
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{input_price}','9') WHERE id='q-1-gpt-5.5'`,
		`INSERT INTO console_sub_check_queue(account_id,group_id,kind,model) VALUES('a',1,'model','gpt-5.5')`,
		`INSERT INTO console_sub_check_queue(account_id,group_id,kind,model) VALUES('a',1,'model','gpt-6-astra')`,
	} {
		prior := read()
		if _, err := s.control.db.Exec(q); err != nil {
			t.Fatal(err)
		}
		if read() == prior {
			t.Fatal("evidence changed without changing revision")
		}
	}
}

func TestManagementHydrationIncludesEveryAliasMemberOutsidePreferredGroup(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	prepareManagementRevisionsForTest(t, s.control.db)
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','987') WHERE group_id=61 AND model='gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reuse := &managementReceiptReuse{deferModelUsage: true}
	compact, err := s.readManagementDatabaseModeWithReceiptsFrom(ctx, tx, "owner", nil, true, "", false, reuse)
	if err != nil {
		t.Fatal(err)
	}
	if reuse.current(tx, "owner") == nil || len(reuse.receipts) != 0 {
		t.Fatal("alias fixture did not defer non-price model receipts")
	}
	full, err := s.readManagementDatabaseViewFrom(ctx, tx, "owner", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	channels := []managementChannel{}
	for i, group := range []int64{1, 61} {
		channels = append(channels, managementChannel{subInstalledChannel: subInstalledChannel{SourceID: string(rune('a' + i)), SourceName: "Source", Provider: "merged", Name: "merged", Kind: "configured", AccountID: "a", GroupID: group, Models: []string{"public-alias"}, ModelMappings: map[string]string{"public-alias": "gpt-5.5"}}, Engine: "gpt", AccountIDs: []string{"a"}})
	}
	compact.Input.Channels, full.Input.Channels = channels, channels
	f := managementQueryFilters{GroupStatus: "all", Search: "merged", Model: "public-alias"}
	build := func(view managementDatabaseView) managementQueryPage {
		t.Helper()
		rows, err := buildManagementQueryRows(view.Input, f.Model, "")
		if err != nil {
			t.Fatal(err)
		}
		evidence := managementQueryEvidence(view.Input, rows, f.Model, view.Prices)
		page, err := buildManagementQueryPageFromRows(view.Input, rows, f, evidence, "owner", s.control.key, "", view.ReadAt)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	page, expected := build(compact), build(full)
	if len(page.Rows) != 1 || page.Rows[0].Target.GroupID != 1 {
		t.Fatal("fixture did not merge alias members")
	}
	if err := s.hydrateManagementPageFrom(ctx, tx, "owner", &compact, &page, f); err != nil {
		t.Fatal(err)
	}
	if page.Revision != expected.Revision || !reflect.DeepEqual(page.Rows, expected.Rows) || page.Rows[0].Selected == nil || *page.Rows[0].Selected.Result.Availability.TTFT != 987 {
		t.Fatal("alias hydration lost non-preferred member's latest evidence")
	}
	proof, _, err := s.managementReadProof(ctx, tx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := buildManagementQueryRows(compact.Input, f.Model, "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := managementQuerySnapshot{View: compact, Page: page, Rows: rows, Evidence: managementQueryEvidence(compact.Input, rows, f.Model, compact.Prices), Proof: &proof}
	s.rememberManagementPagingPlan("owner", f, snapshot)
	got, ok, err := s.currentManagementPagingPage(ctx, "owner", f, "", false)
	if err != nil || !ok || mustJSON(got.Rows) != mustJSON(expected.Rows) || mustJSON(got.Context) != mustJSON(managementContextForPage(full, expected, f.Model)) {
		t.Fatal("directory lost cross-group alias fields", err)
	}
}

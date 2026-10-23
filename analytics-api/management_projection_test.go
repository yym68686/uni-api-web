package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func managementPreparationFixture(t *testing.T, count int) (*sql.DB, string) {
	t.Helper()
	db := migrationTestDB(t)
	ctx := context.Background()
	migrations := controlMigrations()
	if err := migrateControlSchema(ctx, db, migrations[:len(migrations)-1]); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE storage_state(singleton boolean PRIMARY KEY,ingest_limit_bytes bigint NOT NULL)`)
	exec(`INSERT INTO storage_state VALUES(true,pg_database_size(current_database())+1000000000)`)
	exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('a','owner','fixture','https://a.test','fixture','')`)
	exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES('a',1,'fixture','openai')`)
	exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result)
 SELECT 'a',1,'m'||lpad(n::text,4,'0'),'done',jsonb_build_object('checked_at',n,'availability',jsonb_build_object('id','p'||lpad(n::text,4,'0'),'status','success','usage',jsonb_build_object('status','pending')))
 FROM generate_series(0,$1::int-1) n`, count)
	exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result)
 SELECT 'p'||lpad(n::text,4,'0'),'a',1,0,'m'||lpad(n::text,4,'0'),0,'[]','matched',jsonb_build_object('input_price',n)
 FROM generate_series(0,$1::int-1) n`, count)
	if err := migrateControlSchema(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := db.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	return db, schema
}

func TestManagementProjectionPreparationIsBoundedResumableAndCurrent(t *testing.T) {
	db, schema := managementPreparationFixture(t, 1300)
	ctx := context.Background()
	if rows, err := readCurrentManagementModelSignals(ctx, db, "owner"); !errors.Is(err, errManagementPredicateFallback) || rows != nil {
		t.Fatal("unprepared projection became current", err)
	}
	var initial int
	if err := db.QueryRow(`SELECT count(*) FROM console_management_model_signal`).Scan(&initial); err != nil || initial != 0 {
		t.Fatal("startup scanned history", initial, err)
	}
	step, err := prepareManagementProjectionStep(ctx, db, schema)
	if err != nil || step.Phase != "models" || step.Rows != 512 || step.Complete {
		t.Fatal(step, err)
	}
	// A writer behind the cursor and an insert before the first batch remain
	// current through triggers. A restart does not need to scan those rows again.
	if _, err = db.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{checked_at}','9999') WHERE model='m0001';
 INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES('a',1,'before','done','{}');
 UPDATE console_sub_usage SET result='{"input_price":9999}' WHERE id='p0001';
 UPDATE console_sub_models SET model='a-renamed-behind-cursor' WHERE model='m1299';
 UPDATE console_sub_usage SET id='a-renamed-behind-cursor' WHERE id='p1299'`); err != nil {
		t.Fatal(err)
	}
	blocked, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = blocked.Exec(`SELECT 1 FROM console_sub_models WHERE model='m0512' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	if _, err = prepareManagementProjectionStep(ctx, db, schema); err == nil {
		t.Fatal("backfill passed locked writer")
	}
	if err = blocked.Rollback(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		step, err = prepareManagementProjectionStep(ctx, db, schema)
		if err != nil || step.Rows > 512 {
			t.Fatal(step, err)
		}
		if step.Phase == "verify_models" {
			break
		}
	}
	if step.Phase != "verify_models" || step.Complete {
		t.Fatal("unexpected preparation phase", step)
	}
	// A readiness marker requires a second complete comparison pass. A corrupt
	// derived row cannot be silently blessed as current, even after copy passes.
	if _, err = db.Exec(`UPDATE console_management_model_signal SET checked_at=-1 WHERE model='m0000'`); err != nil {
		t.Fatal(err)
	}
	if _, err = prepareManagementProjectionStep(ctx, db, schema); !errors.Is(err, errManagementProjectionMismatch) {
		t.Fatal("corruption was accepted", err)
	}
	if _, err = db.Exec(`UPDATE console_sub_models SET result=result WHERE model='m0000'`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20 && !step.Complete; i++ {
		step, err = prepareManagementProjectionStep(ctx, db, schema)
		if err != nil || step.Rows > 512 {
			t.Fatal(step, err)
		}
	}
	if !step.Complete {
		t.Fatal("preparation did not finish", step)
	}
	actual, err := readCurrentManagementModelSignals(ctx, db, "owner")
	if err != nil || len(actual) != 1301 {
		t.Fatal(len(actual), err)
	}
	expected, _, err := readManagementModelSignalsFrom(ctx, db, "owner")
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatal("resumed preparation differs", err)
	}
	if step, err = prepareManagementProjectionStep(ctx, db, schema); err != nil || !step.Complete || step.Rows != 0 {
		t.Fatal("completed preparation repeated work", step, err)
	}
	if _, err = db.Exec(`DELETE FROM console_management_model_signal WHERE model='m0001'`); err != nil {
		t.Fatal(err)
	}
	if partial, err := readCurrentManagementModelSignals(ctx, db, "owner"); !errors.Is(err, errManagementPredicateFallback) || partial != nil {
		t.Fatal("missing projected model published a partial snapshot", err)
	}
}

func TestManagementProjectionPreparationYieldsAndPreservesCursorOnCapacityOrCancellation(t *testing.T) {
	db, schema := managementPreparationFixture(t, 2)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE storage_state SET ingest_limit_bytes=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareManagementProjectionStep(ctx, db, schema); !errors.Is(err, errManagementProjectionHeadroom) {
		t.Fatal("capacity gate was bypassed", err)
	}
	var count int
	var started, complete bool
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM console_management_model_signal),started,complete FROM console_management_projection_state`).Scan(&count, &started, &complete); err != nil || count != 0 || started || complete {
		t.Fatal("failed batch changed projection or cursor", count, started, complete, err)
	}
	if _, err := db.Exec(`UPDATE storage_state SET ingest_limit_bytes=pg_database_size(current_database())+1000000000`); err != nil {
		t.Fatal(err)
	}
	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err = writer.Exec(`SELECT pg_advisory_xact_lock(8395015)`); err != nil {
		t.Fatal(err)
	}
	if _, err = prepareManagementProjectionStep(ctx, db, schema); !errors.Is(err, errManagementPreparationBusy) {
		t.Fatal("preparation did not yield to ingestion", err)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = prepareManagementProjectionStep(cancelled, db, schema); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	if err = prepareManagementProjection(ctx, db, schema, 0, nil); err != nil {
		t.Fatal(err)
	}
	// Prepared verification does not manufacture amounts for another owner.
	if got, err := readCurrentManagementModelSignals(ctx, db, "foreign"); err != nil || len(got) != 0 {
		t.Fatal("owner scope lost", err)
	}
}

func TestManagementProjectionRequiresFullTypedFallbackForUnsupportedReceiptEvidence(t *testing.T) {
	db, schema := managementPreparationFixture(t, 1)
	ctx := context.Background()
	if err := prepareManagementProjection(ctx, db, schema, 0, nil); err != nil {
		t.Fatal(err)
	}
	// All fields decoded by subAttachUsage must be validated, including fields
	// omitted from filtering. Values that the legacy decoder accepts but whose
	// representation the SQL fast path does not understand must also fall back.
	for _, raw := range []string{
		`{"message":false}`, `{"actual_cost":"3"}`, `{"log_id":1.5}`,
		`{"cache_read_tokens":"7"}`, `{"cache_creation_tokens":9223372036854775808}`,
		`{"request_id":[]}`, `{"duration_ms":{}}`, `{"first_token_ms":false}`,
		`{"created_at":7}`, `{"paid_input_price":1e400}`,
		`{"Status":"matched","Input_Price":2}`, `{"unknown_future_field":true}`,
		`{"input_price":1e-400}`,
	} {
		if _, err := db.Exec(`UPDATE console_sub_usage SET result=$1 WHERE id='p0000'`, raw); err != nil {
			t.Fatal(err)
		}
		got, err := readCurrentManagementModelSignals(ctx, db, "owner")
		if !errors.Is(err, errManagementPredicateFallback) || got != nil {
			t.Fatal("unsupported receipt became valid predicates", raw, err)
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"status":null,"input_price":0}`, `{"input_price":2,"cache_read_tokens":7,"message":"ok","actual_cost":0}`} {
		if _, err := db.Exec(`UPDATE console_sub_usage SET result=$1 WHERE id='p0000'`, raw); err != nil {
			t.Fatal(err)
		}
		got, err := readCurrentManagementModelSignals(ctx, db, "owner")
		want, _, wantErr := readManagementModelSignalsFrom(ctx, db, "owner")
		if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("supported receipt differs", raw, err, wantErr)
		}
	}
	for _, field := range []string{"ttft_ms", "response_created_ms", "first_response_ms", "duration_ms", "http_status", "protocol", "requested_model", "response_model", "message"} {
		raw := fmt.Sprintf(`{"availability":{"id":"p0000","%s":false}}`, field)
		if _, err := db.Exec(`UPDATE console_sub_models SET result=$1 WHERE model='m0000'`, raw); err != nil {
			t.Fatal(err)
		}
		if got, err := readCurrentManagementModelSignals(ctx, db, "owner"); !errors.Is(err, errManagementPredicateFallback) || got != nil {
			t.Fatal("unsupported model summary became valid predicates", field, err)
		}
		var decoded subResult
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
			t.Fatal("invalid fixture", raw)
		}
	}
}

func TestManagementProjectionReadKeepsCurrentReceiptAndWriterSnapshot(t *testing.T) {
	db, schema := managementPreparationFixture(t, 1)
	ctx := context.Background()
	if err := prepareManagementProjection(ctx, db, schema, 0, nil); err != nil {
		t.Fatal(err)
	}
	reader, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	before, err := readCurrentManagementModelSignals(ctx, reader, "owner")
	if err != nil {
		t.Fatal(err)
	}
	modelWriter, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer modelWriter.Rollback()
	if _, err = modelWriter.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{checked_at}','99') WHERE model='m0000'`); err != nil {
		t.Fatal(err)
	}
	receiptWriter, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer receiptWriter.Rollback()
	if _, err = receiptWriter.Exec(`UPDATE console_sub_usage SET result='{"input_price":42}' WHERE id='p0000'`); err != nil {
		t.Fatal(err)
	}
	if err = receiptWriter.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = modelWriter.Commit(); err != nil {
		t.Fatal(err)
	}
	old, err := readCurrentManagementModelSignals(ctx, reader, "owner")
	if err != nil || !reflect.DeepEqual(old, before) {
		t.Fatal("read mixed committed versions", err)
	}
	current, err := readCurrentManagementModelSignals(ctx, db, "owner")
	if err != nil || current[0].Model.Result.CheckedAt != 99 || *current[0].Model.Result.Availability.Usage.InputPrice != 42 {
		t.Fatal("independent writers lost evidence", err)
	}
	if _, err = db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('foreign','foreign','fixture','https://f.test','f',''); UPDATE console_sub_usage SET account_id='foreign' WHERE id='p0000'`); err != nil {
		t.Fatal(err)
	}
	current, err = readCurrentManagementModelSignals(ctx, db, "owner")
	if err != nil || current[0].Model.Result.Availability.Usage.Status != "pending" || current[0].Model.Result.Availability.Usage.InputPrice != nil {
		t.Fatal("foreign receipt was disclosed", err)
	}
	if _, err = db.Exec(`DELETE FROM console_sub_usage WHERE id='p0000';DELETE FROM console_sub_models WHERE model='m0000'`); err != nil {
		t.Fatal(err)
	}
	current, err = readCurrentManagementModelSignals(ctx, db, "owner")
	if err != nil || len(current) != 0 {
		t.Fatal("deleted model remained visible", err)
	}
}

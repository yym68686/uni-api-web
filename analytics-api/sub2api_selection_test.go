package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSubSelectedModelsQueueLargeBatchWithoutChangingOtherResults(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := "models-" + randomID()[:10]
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, id)
	if _, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$1,'Fixture','https://fixture.test','test@example.com','')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,encrypted_key) SELECT $1,n,'Group','openai','fixture' FROM generate_series(1,500) n`, id); err != nil {
		t.Fatal(err)
	}
	session, _ := store.newSession(context.Background(), id)
	service := &Service{control: store}
	request := func(models []string, count int) *httptest.ResponseRecorder {
		targets := []subSelection{}
		for i := 1; i <= count; i++ {
			targets = append(targets, subSelection{AccountID: id, GroupID: int64(i), Models: models})
		}
		body, _ := json.Marshal(map[string]any{"targets": targets})
		if count == 500 && len(body) <= 32<<10 {
			t.Fatal("fixture must exercise previous size limit")
		}
		req := httptest.NewRequest("POST", "/v1/sub2api/checks", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, req)
		return w
	}
	if w := request(subModels, 500); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var queued int
	if err = store.db.QueryRow(`SELECT count(*) FROM console_sub_models WHERE account_id=$1 AND state='queued'`, id).Scan(&queued); err != nil || queued != 500*len(subModels) {
		t.Fatal("batch incomplete", queued, err)
	}
	// Once results exist, a new selection must leave every unchecked model alone.
	store.db.Exec(`UPDATE console_sub_accounts SET state='idle' WHERE id=$1`, id)
	store.db.Exec(`UPDATE console_sub_models SET state='done',result='{"model":"old-result","checked_at":1}' WHERE account_id=$1`, id)
	selected := []string{"glm-5.3", "gemini-3.1-pro", "claude-opus-5"}
	if w := request(selected, 1); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var untouched int
	store.db.QueryRow(`SELECT count(*) FROM console_sub_models WHERE account_id=$1 AND state='queued'`, id).Scan(&queued)
	store.db.QueryRow(`SELECT count(*) FROM console_sub_models WHERE account_id=$1 AND state='done' AND result->>'model'='old-result'`, id).Scan(&untouched)
	if queued != 3 || untouched != 500*len(subModels)-3 {
		t.Fatal("unchecked model changed", queued, untouched)
	}
	if w := request([]string{"custom-channel-model"}, 1); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var extra int
	store.db.QueryRow(`SELECT count(*) FROM console_sub_check_queue WHERE account_id=$1 AND model='custom-channel-model'`, id).Scan(&extra)
	if extra != 1 {
		t.Fatal("extra model not queued", extra)
	}
	if w := request([]string{"bad\nmodel"}, 1); w.Code != 400 {
		t.Fatal("invalid model accepted", w.Code)
	}

}

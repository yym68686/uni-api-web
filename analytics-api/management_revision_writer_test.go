package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash"
	"math"
	"strings"
	"testing"
)

type recordingRevisionHash struct {
	hash.Hash
	last []byte
}

func (h *recordingRevisionHash) Write(b []byte) (int, error) {
	h.last = append(h.last[:0], b...)
	return h.Hash.Write(b)
}

func TestManagementRevisionWriterMatchesJSON(t *testing.T) {
	actual, expected := &recordingRevisionHash{Hash: sha256.New()}, &bytes.Buffer{}
	writer := newManagementRevisionWriter(actual)
	encoder := json.NewEncoder(expected)
	names := []string{"", `quotes"slash\\<>&`, "中文🙂\u2028\u2029\x00\n\t", "broken\xff"}
	for i := 0; i < 2500; i++ {
		account := names[i%len(names)] + fmt.Sprint(i/160)
		model := names[(i+1)%len(names)] + fmt.Sprint(i)
		state := names[(i+2)%len(names)] + fmt.Sprint(i%20)
		group := int64(i)
		if i%7 == 0 {
			group = math.MinInt64
		}
		if i%11 == 0 {
			group = math.MaxInt64
		}
		message := strings.Repeat(names[i%len(names)], i%200)
		digest := sha256.Sum256([]byte(fmt.Sprint(i)))
		evidence := digest[:]
		var receipt, quality []byte
		if i%2 == 0 {
			receipt = []byte{}
		}
		if i%3 == 0 {
			quality = evidence
		}
		expected.Reset()
		writer.write(account, group, model, state, message, evidence, receipt, quality)
		if err := encoder.Encode([]any{account, group, model, state, summaryMessage(message), evidence, receipt, quality}); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual.last, expected.Bytes()) {
			t.Fatal("revision bytes changed", i)
		}
		// The immediately repeated identity exercises prefix/string reuse too.
		writer.write(account, group, model, state, message, evidence, receipt, quality)
		if !bytes.Equal(actual.last, expected.Bytes()) {
			t.Fatal("reused revision bytes changed", i)
		}
	}
}

func BenchmarkManagementRevisionWriter(b *testing.B) {
	for _, manual := range []bool{false, true} {
		b.Run(fmt.Sprint("manual=", manual), func(b *testing.B) {
			models := make([]string, 160)
			for i := range models {
				models[i] = fmt.Sprintf("model-%03d", i)
			}
			digest := sha256.Sum256([]byte("fixture"))
			message := "complete current diagnostic"
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				h := sha256.New()
				var encoder *json.Encoder
				var writer *managementRevisionWriter
				if manual {
					writer = newManagementRevisionWriter(h)
				} else {
					encoder = json.NewEncoder(h)
				}
				for group := 0; group < 749; group++ {
					account := fmt.Sprint("account-", group%48)
					for _, model := range models {
						if manual {
							writer.write(account, int64(group), model, "done", message, digest[:], digest[:], nil)
						} else {
							_ = encoder.Encode([]any{account, int64(group), model, "done", summaryMessage(message), digest[:], digest[:], []byte(nil)})
						}
					}
				}
			}
		})
	}
}

func TestManagementRevisionWriterRetainsExistingCursorHashes(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	for _, update := range []string{"", `UPDATE console_sub_models SET message=repeat('🙂<>"',45),state='queued' WHERE group_id=61`, `UPDATE console_sub_usage SET result=jsonb_set(result,'{input_price}','9') WHERE id='q-61-gpt-5.5'`, `INSERT INTO console_sub_check_queue(account_id,group_id,kind,model) VALUES('a',1,'model','gpt-5.5')`} {
		if update != "" {
			if _, err := s.control.db.ExecContext(ctx, update); err != nil {
				t.Fatal(err)
			}
		}
		rows, err := s.control.db.QueryContext(ctx, `SELECT m.account_id,m.group_id,m.model,m.state,m.message,p.evidence_digest,r.evidence_digest,q.evidence_digest`+managementModelSignalsStoredFrom+` ORDER BY m.account_id COLLATE "C",m.group_id,m.model COLLATE "C"`, "owner")
		if err != nil {
			t.Fatal(err)
		}
		old := sha256.New()
		encoder := json.NewEncoder(old)
		for rows.Next() {
			var account, model, state, message string
			var group int64
			var evidence, receipt, quality []byte
			if err := rows.Scan(&account, &group, &model, &state, &message, &evidence, &receipt, &quality); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if err := encoder.Encode([]any{account, group, model, state, summaryMessage(message), evidence, receipt, quality}); err != nil {
				rows.Close()
				t.Fatal(err)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		expected, err := finishManagementModelRevision(ctx, s.control.db, "owner", old)
		if err != nil {
			t.Fatal(err)
		}
		revision, err := readManagementModelRevisionFrom(ctx, s.control.db, "owner")
		if err != nil || revision != expected {
			t.Fatal("catalog revision changed", err)
		}
		_, combined, err := readCurrentManagementSignalsAndRevision(ctx, s.control.db, "owner")
		if err != nil || combined != expected {
			t.Fatal("combined revision changed", err)
		}
	}
}

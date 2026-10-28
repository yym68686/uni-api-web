package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http/httptest"
	"testing"
)

func previousManagementResponseTag(mediaType, encoding string, body []byte) string {
	return `"` + tokenHash(mediaType+"\x00"+encoding+"\x00"+string(body)) + `"`
}

func TestManagementResponseTagMatchesPreviousBytes(t *testing.T) {
	rng := rand.New(rand.NewSource(19))
	bodies := [][]byte{nil, {}, []byte("\x00\xff模型🙂<>&\u2028"), []byte(`{"rows":[],"value":null}`)}
	for _, size := range []int{1, 63, 64, 65, 255, 256, 1024, (32 << 10) - 1, 32 << 10, (32 << 10) + 1, 1 << 20, 4 << 20} {
		body := make([]byte, size)
		_, _ = rng.Read(body)
		bodies = append(bodies, body)
	}
	for n := 0; n < 100; n++ {
		body := make([]byte, rng.Intn(32768))
		_, _ = rng.Read(body)
		bodies = append(bodies, body)
	}
	for _, media := range []string{"", managementPageV3MediaType, "application/json\x00suffix"} {
		for _, encoding := range []string{"", "br, gzip", "gzip, br", "gzip;q=0", "BR; q=1", "\x00\xff"} {
			for i, body := range bodies {
				before := bytes.Clone(body)
				want := previousManagementResponseTag(media, encoding, body)
				got := managementResponseETag(media, encoding, body)
				if got != want || !bytes.Equal(body, before) {
					t.Fatalf("tag bytes or input changed: body %d, media %q, encoding %q", i, media, encoding)
				}
			}
		}
	}
}

func TestManagementResponseTagPreservesHTTPValidation(t *testing.T) {
	value := map[string]any{"rows": []any{}, "text": "模型🙂<>&\u2028"}
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, encoding := range []string{"", "br, gzip", "gzip;q=0", "BR; q=1"} {
		tag := previousManagementResponseTag(managementPageV3MediaType, encoding, body)
		for _, condition := range []string{"", tag, `"other", ` + tag, `W/` + tag, `"other"`} {
			r := httptest.NewRequest("GET", "/v1/channel-management/query", nil)
			r.Header.Set("Accept-Encoding", encoding)
			r.Header.Set("X-Console-Current-Validation", "1")
			r.Header.Set("If-None-Match", condition)
			w := httptest.NewRecorder()
			w.Header().Set("X-Console-Validated-Request-ID", "validated-test-request")
			if err := writeManagementJSONMedia(w, r, value, managementPageV3MediaType); err != nil {
				t.Fatal(err)
			}
			wantStatus := 200
			if condition == tag || condition == `"other", `+tag {
				wantStatus = 304
			}
			if w.Code != wantStatus || w.Header().Get("ETag") != tag || w.Header().Get("Cache-Control") != "private, no-cache, must-revalidate" {
				t.Fatal("validation status, tag or cache policy changed", encoding, condition, w.Code)
			}
			if (wantStatus == 304 && w.Body.Len() != 0) || (wantStatus == 200 && !bytes.Equal(w.Body.Bytes(), body)) {
				t.Fatal("response bytes changed")
			}
		}
	}
}

func BenchmarkManagementResponseTag(b *testing.B) {
	for _, size := range []int{256 << 10, 4 << 20, 16 << 20} {
		body := bytes.Repeat([]byte("x"), size)
		want := previousManagementResponseTag(managementPageV3MediaType, "br, gzip", body)
		for _, entry := range []struct {
			name string
			tag  func(string, string, []byte) string
		}{{"previous_copies", previousManagementResponseTag}, {"buffered", managementResponseETag}} {
			b.Run(fmt.Sprintf("%d/%s", size, entry.name), func(b *testing.B) {
				b.SetBytes(int64(size))
				b.ReportAllocs()
				for range b.N {
					if got := entry.tag(managementPageV3MediaType, "br, gzip", body); got != want {
						b.Fatal("tag changed")
					}
				}
			})
		}
	}
}

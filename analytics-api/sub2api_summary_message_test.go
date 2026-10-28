package main

import (
	"math/rand"
	"strings"
	"testing"
)

func legacySummaryMessage(message string) string {
	runes := []rune(message)
	if len(runes) > 160 {
		return string(runes[:160]) + "…"
	}
	return message
}

func TestSummaryMessageMatchesLegacyBoundariesAndInvalidUTF8(t *testing.T) {
	for _, text := range []string{"", "a", "消息", "🙂", "e\u0301", "\xff", "\xe2\x82", "a\xff🙂"} {
		for _, count := range []int{0, 1, 39, 40, 79, 80, 159, 160, 161, 320, 4096} {
			message := strings.Repeat(text, count)
			if got, want := summaryMessage(message), legacySummaryMessage(message); got != want {
				t.Fatalf("summary changed for %q repeated %d: got %q want %q", text, count, got, want)
			}
		}
	}
	// Deterministic arbitrary bytes cover continuation bytes, overlong encodings
	// and invalid code points as well as valid text at the truncation boundary.
	rng := rand.New(rand.NewSource(728))
	for i := 0; i < 2000; i++ {
		raw := make([]byte, rng.Intn(1024))
		if _, err := rng.Read(raw); err != nil {
			t.Fatal(err)
		}
		message := string(raw)
		if got, want := summaryMessage(message), legacySummaryMessage(message); got != want {
			t.Fatalf("summary differs on arbitrary bytes: got %q want %q", got, want)
		}
	}
}

func FuzzSummaryMessageMatchesLegacy(f *testing.F) {
	for _, message := range []string{"", strings.Repeat("🙂a", 161), strings.Repeat("\xff", 320)} {
		f.Add(message)
	}
	f.Fuzz(func(t *testing.T, message string) {
		if got, want := summaryMessage(message), legacySummaryMessage(message); got != want {
			t.Fatalf("summary differs: got %q want %q", got, want)
		}
	})
}

var summaryMessageBenchmarkResult string

func BenchmarkSummaryMessage(b *testing.B) {
	for _, sample := range []struct{ name, message string }{
		{"short", "等待检测结果"},
		{"unicode_160", strings.Repeat("🙂", 160)},
		{"long", strings.Repeat("diagnostic response🙂 ", 1000)},
	} {
		for _, impl := range []struct {
			name   string
			format func(string) string
		}{{"legacy", legacySummaryMessage}, {"bounded", summaryMessage}} {
			b.Run(sample.name+"/"+impl.name, func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					summaryMessageBenchmarkResult = impl.format(sample.message)
				}
			})
		}
	}
}

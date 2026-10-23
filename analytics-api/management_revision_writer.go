package main

import (
	"encoding/base64"
	"encoding/json"
	"hash"
	"strconv"
)

// Reproduce the existing eight-field JSON-line revision encoding exactly.
// Quotes for repeated identity strings are cached only inside this read; the
// message, evidence and receipt bytes are always from the current snapshot.
// Bounded quote maps and a reusable buffer avoid per-model interface slices,
// reflection and base64 allocations without changing any signed revision.
type managementRevisionWriter struct {
	hash           hash.Hash
	prefix, buffer []byte
	account        string
	group          int64
	active         bool
	quotedModels   map[string][]byte
	quotedStates   map[string][]byte
	message        string
	quotedMessage  []byte
}

func newManagementRevisionWriter(h hash.Hash) *managementRevisionWriter {
	return &managementRevisionWriter{hash: h, buffer: make([]byte, 0, 512), quotedModels: map[string][]byte{}, quotedStates: map[string][]byte{}}
}

func managementRevisionQuote(s string, cache map[string][]byte, limit int) []byte {
	if raw, ok := cache[s]; ok {
		return raw
	}
	// Marshaling a string cannot fail, and keeps encoding/json HTML/UTF-8 rules.
	raw, _ := json.Marshal(s)
	if len(cache) < limit {
		cache[s] = raw
	}
	return raw
}

func (w *managementRevisionWriter) write(account string, group int64, model, state, message string, evidence, receipt, quality []byte) {
	if !w.active || w.account != account || w.group != group {
		raw, _ := json.Marshal(account)
		w.prefix = append(w.prefix[:0], '[')
		w.prefix = append(w.prefix, raw...)
		w.prefix = append(w.prefix, ',')
		w.prefix = strconv.AppendInt(w.prefix, group, 10)
		w.prefix = append(w.prefix, ',')
		w.account, w.group, w.active = account, group, true
	}
	w.buffer = append(w.buffer[:0], w.prefix...)
	w.buffer = append(w.buffer, managementRevisionQuote(model, w.quotedModels, 1024)...)
	w.buffer = append(w.buffer, ',')
	w.buffer = append(w.buffer, managementRevisionQuote(state, w.quotedStates, 16)...)
	w.buffer = append(w.buffer, ',')
	message = summaryMessage(message)
	if w.quotedMessage == nil || w.message != message {
		w.quotedMessage, _ = json.Marshal(message)
		w.message = message
	}
	w.buffer = append(w.buffer, w.quotedMessage...)
	for _, value := range [][]byte{evidence, receipt, quality} {
		w.buffer = append(w.buffer, ',')
		if value == nil {
			w.buffer = append(w.buffer, "null"...)
		} else {
			w.buffer = append(w.buffer, '"')
			w.buffer = base64.StdEncoding.AppendEncode(w.buffer, value)
			w.buffer = append(w.buffer, '"')
		}
	}
	w.buffer = append(w.buffer, ']', '\n')
	_, _ = w.hash.Write(w.buffer)
}

package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"io"
)

// This is private cache storage, not HTTP encoding. Keep the EXACT raw SQL
// bytes (including nil, empty and invalid JSON) so the original decoder still
// observes every field and error. Compress complete groups only, after the
// existing successful-read/admission gate and before publication. The existing
// owner/database/RR/source revisions, 128-group cap and 8MiB budget do not change.
const managementPackedModelLimit = 16 << 20

// Reuse the compressor only within this batch. No unaccounted global buffer
// pool survives a request, and groups still have independent bounded payloads.
type managementModelPacker struct {
	output bytes.Buffer
	writer *gzip.Writer
}

func (p *managementModelPacker) pack(ctx context.Context, e *managementPageModelStream) {
	if e.bytes < 4<<10 || len(e.rows) == 0 || ctx.Err() != nil {
		return
	}
	// Each row belongs to this one certified group. Length-prefixed bytes avoid
	// base64 expansion and a second JSON parse of every summary on reuse.
	raw := binary.AppendUvarint(make([]byte, 0, e.bytes), uint64(len(e.rows)))
	raw = binary.AppendUvarint(raw, uint64(len(e.group.account)))
	raw = append(raw, e.group.account...)
	raw = binary.AppendVarint(raw, e.group.group)
	rowBytes := 0
	for _, r := range e.rows {
		if r.account != e.group.account || r.group != e.group.group {
			return
		}
		for _, text := range []string{r.model, r.state, r.message} {
			raw = binary.AppendUvarint(raw, uint64(len(text)))
			raw = append(raw, text...)
		}
		if r.result == nil {
			raw = binary.AppendUvarint(raw, 0)
		} else {
			raw = binary.AppendUvarint(raw, uint64(len(r.result))+1)
			raw = append(raw, r.result...)
		}
		rowBytes += r.retainedBytes()
		if len(raw) > managementPackedModelLimit {
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	var err error
	p.output.Reset()
	if p.writer == nil {
		p.writer, err = gzip.NewWriterLevel(&p.output, gzip.BestSpeed)
		if err != nil {
			return
		}
	} else {
		p.writer.Reset(&p.output)
	}
	if _, err = p.writer.Write(raw); err != nil {
		return
	}
	if err = p.writer.Close(); err != nil || ctx.Err() != nil {
		return
	}
	// Include all retained metadata; do not retain both graphs or a buffer's
	// unused capacity. Incompressible/small groups keep their previous form.
	n := e.bytes - rowBytes + p.output.Len()
	if n >= e.bytes || n < p.output.Len() {
		return
	}
	e.packed = make([]byte, p.output.Len())
	copy(e.packed, p.output.Bytes())
	e.packedBytes, e.packedRows = len(raw), len(e.rows)
	e.rows, e.bytes = nil, n
}

func (e *managementPageModelStream) unpack(ctx context.Context) ([]managementPageModelRecord, bool) {
	if ctx.Err() != nil {
		return nil, false
	}
	if len(e.packed) == 0 {
		return e.rows, true
	}
	if e.packedBytes <= 0 || e.packedBytes > managementPackedModelLimit || e.packedRows <= 0 || e.packedRows > managementPageModelBudget/(managementPageModelRecord{}).retainedBytes() {
		return nil, false
	}
	reader, err := gzip.NewReader(bytes.NewReader(e.packed))
	if err != nil {
		return nil, false
	}
	defer reader.Close()
	// CRC/length/count failures are cache misses, never partial groups. Bound
	// decompression before decoding. These are server-produced bytes only.
	raw := make([]byte, e.packedBytes)
	if _, err := io.ReadFull(reader, raw); err != nil || ctx.Err() != nil {
		return nil, false
	}
	// Reading exactly N bytes can hide a missing gzip footer. Require a clean
	// EOF on a separate read, so truncated streams cannot masquerade as valid.
	var extra [1]byte
	if n, err := io.ReadFull(reader, extra[:]); n != 0 || err != io.EOF {
		return nil, false
	}
	count, n := binary.Uvarint(raw)
	if n <= 0 || count != uint64(e.packedRows) || count > uint64(len(raw)/4) {
		return nil, false
	}
	raw = raw[n:]
	// Every field is bounded by the remaining complete payload. No corrupt
	// count/length may allocate outside the original group's decoded byte cap.
	field := func(nullable bool) ([]byte, bool) {
		size, n := binary.Uvarint(raw)
		if n <= 0 {
			return nil, false
		}
		raw = raw[n:]
		if nullable {
			if size == 0 {
				return nil, true
			}
			size--
		}
		if size > uint64(len(raw)) {
			return nil, false
		}
		value := raw[:int(size):int(size)]
		raw = raw[int(size):]
		return value, true
	}
	account, ok := field(false)
	if !ok || string(account) != e.group.account {
		return nil, false
	}
	group, n := binary.Varint(raw)
	if n <= 0 || group != e.group.group {
		return nil, false
	}
	raw = raw[n:]
	out := make([]managementPageModelRecord, int(count))
	for i := range out {
		row := managementPageModelRecord{account: e.group.account, group: e.group.group}
		for _, text := range []*string{&row.model, &row.state, &row.message} {
			value, ok := field(false)
			if !ok {
				return nil, false
			}
			*text = string(value)
		}
		value, ok := field(true)
		if !ok {
			return nil, false
		}
		row.result = value
		out[i] = row
	}
	if len(raw) != 0 || ctx.Err() != nil {
		return nil, false
	}
	return out, true
}

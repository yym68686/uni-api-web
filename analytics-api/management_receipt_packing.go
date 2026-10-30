package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
)

// Private cache representation only. Source proofs, strictness, missing-row
// certificates and the raw 8 MiB admission bound remain unchanged. Pack after
// successful complete reads, before publishing an entry; never retain both
// forms. Small/incompressible payloads keep their original bytes.
func (e *managementReceiptSet) pack(ctx context.Context) {
	if e == nil || e.bytes > managementReceiptSetBudget || e.bodySize > 0 || e.absentSize > 0 || ctx.Err() != nil {
		return
	}
	var output bytes.Buffer
	var writer *gzip.Writer
	pack := func(raw []byte) ([]byte, int) {
		if len(raw) < 4<<10 || len(raw) > managementReceiptSetBudget || ctx.Err() != nil {
			return raw, 0
		}
		output.Reset()
		var err error
		if writer == nil {
			writer, err = gzip.NewWriterLevel(&output, gzip.BestSpeed)
			if err != nil {
				return raw, 0
			}
		} else {
			writer.Reset(&output)
		}
		if _, err = writer.Write(raw); err != nil {
			return raw, 0
		}
		if err = writer.Close(); err != nil || ctx.Err() != nil || output.Len() >= len(raw) {
			return raw, 0
		}
		packed := make([]byte, output.Len())
		copy(packed, output.Bytes())
		return packed, len(raw)
	}
	before := len(e.body) + len(e.absent)
	e.body, e.bodySize = pack(e.body)
	e.absent, e.absentSize = pack(e.absent)
	e.bytes += len(e.body) + len(e.absent) - before
}

func unpackManagementReceiptData(ctx context.Context, body []byte, size int) ([]byte, bool) {
	if ctx.Err() != nil || size < 0 || size > managementReceiptSetBudget {
		return nil, false
	}
	if size == 0 {
		return body, true
	}
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, false
	}
	defer reader.Close()
	raw := make([]byte, size)
	if _, err := io.ReadFull(reader, raw); err != nil || ctx.Err() != nil {
		return nil, false
	}
	// Require the exact decoded length AND a verified footer. A truncated or
	// extended stream must fall back to SQL, never publish partial receipts.
	var extra [1]byte
	if n, err := io.ReadFull(reader, extra[:]); n != 0 || err != io.EOF || ctx.Err() != nil {
		return nil, false
	}
	return raw, true
}

package main

import (
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
)

// Only authenticated configuration uses this budget. Login and individual
// credential/field validation retain their separate protection.
func configurationByteLimit() int64 {
	n, _ := strconv.ParseInt(os.Getenv("CONFIGURATION_MAX_BYTES"), 10, 64)
	if n < 0 {
		return 0
	}
	return n
}
func decodeConfiguration(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeControlLimit(w, r, v, configurationByteLimit())
}
func readConfiguration(r io.Reader) ([]byte, error) {
	limit := configurationByteLimit()
	if limit > 0 {
		if limit < math.MaxInt64 {
			r = io.LimitReader(r, limit+1)
		}
	}
	raw, err := io.ReadAll(r)
	if err == nil && limit > 0 && int64(len(raw)) > limit {
		return nil, errors.New("configuration reaches operator byte budget")
	}
	return raw, err
}

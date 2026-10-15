//go:build !linux

package main

import (
	"errors"
	"os"
)

func cloneCheckpointFile(dst, src *os.File) error { return errors.New("reflink unavailable") }

//go:build !linux

package main

import "os"

func discardCheckpointCache(_ *os.File, _, _ int64) {}

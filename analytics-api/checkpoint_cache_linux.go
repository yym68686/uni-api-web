//go:build linux

package main

import (
	"golang.org/x/sys/unix"
	"os"
)

// Only used on owned immutable scratch files. This is a best-effort hint and
// never affects correctness; failed hints retain cache rather than fail a save.
func discardCheckpointCache(file *os.File, offset, length int64) {
	_ = unix.Fadvise(int(file.Fd()), offset, length, unix.FADV_DONTNEED)
}

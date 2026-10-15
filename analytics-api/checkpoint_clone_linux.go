//go:build linux

package main

import (
	"golang.org/x/sys/unix"
	"os"
)

func cloneCheckpointFile(dst, src *os.File) error {
	return unix.IoctlFileClone(int(dst.Fd()), int(src.Fd()))
}

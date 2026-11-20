//go:build linux

// Linux reserves tail blocks through fallocate(FALLOC_FL_KEEP_SIZE): blocks
// are allocated up front to remove growth-point stalls from the append path,
// while the visible file size keeps tracking the real data.

package wal

import (
	"os"
	"syscall"
)

// fallocFlKeepSize keeps the apparent file size unchanged while still
// allocating the requested blocks on disk. The constant is not exported by
// the syscall package, so mirror the kernel value here.
const fallocFlKeepSize = 0x1

func preAllocate(f *os.File, offset, size int64) error {
	return syscall.Fallocate(int(f.Fd()), fallocFlKeepSize, offset, size)
}

func reserveSpace(f *os.File, offset, size int64) error {
	return preAllocate(f, offset, size)
}

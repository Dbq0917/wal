//go:build !linux

package wal

import "os"

// reserveSpace is a no-op on platforms without a block-reservation syscall in
// the standard library. Writes still succeed; they just extend the file as
// needed, matching the behavior of PreAllocateNone on those platforms.
func reserveSpace(f *os.File, offset, size int64) error {
	return nil
}

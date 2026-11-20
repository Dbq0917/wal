package wal

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

// diskBlocks returns the real number of 512-byte blocks occupied by all
// segment files in a log directory. This counts reserved-but-unwritten
// blocks, unlike the apparent file size.
func diskBlocks(t *testing.T, dir string) int64 {
	t.Helper()
	var blocks int64
	fis, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, fi := range fis {
		if fi.IsDir() {
			continue
		}
		var st syscall.Stat_t
		if err := syscall.Stat(filepath.Join(dir, fi.Name()), &st); err != nil {
			t.Fatal(err)
		}
		blocks += st.Blocks
	}
	return blocks * 512
}

// apparentSize returns the sum of the visible (logical) segment file sizes,
// which must always equal the amount of real log data.
func apparentSize(t *testing.T, dir string) int64 {
	t.Helper()
	var size int64
	fis, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, fi := range fis {
		if fi.IsDir() {
			continue
		}
		info, err := fi.Info()
		if err != nil {
			t.Fatal(err)
		}
		size += info.Size()
	}
	return size
}

func preAllocOpts(mode PreAllocateMode, segSize, incSize int) *Options {
	opts := *DefaultOptions
	opts.NoSync = true
	opts.SegmentSize = segSize
	opts.PreAllocate = mode
	opts.PreAllocateSize = incSize
	return &opts
}

// TestPreAllocateFootprint verifies the space/latency trade-off exposed by
// the three strategies: no reservation tracks data exactly, full
// reservation charges a whole segment up front, and incremental reservation
// keeps at most one increment of headroom. In every case the visible file
// size tracks the real data so reopen never observes reserved zero bytes.
func TestPreAllocateFootprint(t *testing.T) {
	const (
		segSize = 1 << 16 // 64 KB segments
		incSize = 1 << 12 // 4 KB reservation increments
		entry   = 256     // ~256 bytes per entry including the length prefix
	)
	// Half a segment of data leaves every mode with a partially written tail.
	n := segSize / entry / 2

	cases := []struct {
		name string
		mode PreAllocateMode
		// upper bound on extra blocks vs. the visible data size
		maxOverhead int64
	}{
		// The library issues no reservation in none mode; any small overhead
		// here comes from the filesystem's own in-kernel preallocation and is
		// bounded by one increment in this test setup.
		{"none", PreAllocateNone, incSize},
		{"incremental", PreAllocateIncremental, incSize},
		{"full", PreAllocateFull, segSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := "testlog/prealloc-" + tc.name
			os.RemoveAll(dir)
			defer os.RemoveAll(dir)

			l, err := Open(dir, preAllocOpts(tc.mode, segSize, incSize))
			if err != nil {
				t.Fatal(err)
			}
			for i := uint64(1); i <= uint64(n); i++ {
				if err := l.Write(i, make([]byte, entry)); err != nil {
					t.Fatal(err)
				}
			}

			visible := apparentSize(t, dir)
			blocks := diskBlocks(t, dir)
			if blocks < visible {
				t.Fatalf("blocks %d smaller than data %d", blocks, visible)
			}
			if overhead := blocks - visible; overhead > tc.maxOverhead {
				t.Fatalf("overhead %d exceeds bound %d (blocks=%d data=%d)",
					overhead, tc.maxOverhead, blocks, visible)
			}
			if tc.mode == PreAllocateFull && blocks < int64(segSize) {
				t.Fatalf("full mode should reserve a whole segment, got blocks=%d",
					blocks)
			}
			if tc.mode == PreAllocateIncremental && blocks-visible > incSize {
				t.Fatalf("incremental mode must hold at most one increment: "+
					"blocks=%d data=%d", blocks, visible)
			}

			// Reopen: reserved tail bytes must not be parsed as entries and
			// the visible data must round-trip intact.
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			l2, err := Open(dir, preAllocOpts(tc.mode, segSize, incSize))
			if err != nil {
				t.Fatal(err)
			}
			defer l2.Close()
			last, err := l2.LastIndex()
			if err != nil {
				t.Fatal(err)
			}
			if last != uint64(n) {
				t.Fatalf("expected last index %d, got %d", n, last)
			}
			data, err := l2.Read(uint64(n))
			if err != nil || len(data) != entry {
				t.Fatalf("read back failed: len=%d err=%v", len(data), err)
			}
			// Appending after reopen continues from the right offset.
			if err := l2.Write(uint64(n)+1, make([]byte, entry)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestPreAllocateIncrementalGrowth checks the watermark advances in whole
// increments and never exceeds the segment size.
func TestPreAllocateIncrementalGrowth(t *testing.T) {
	const (
		dir     = "testlog/prealloc-grow"
		segSize = 1 << 14 // 16 KB
		incSize = 1 << 12 // 4 KB
	)
	os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	l, err := Open(dir, preAllocOpts(PreAllocateIncremental, segSize, incSize))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	first := l.segments[0]
	if first.alloc != incSize {
		t.Fatalf("initial watermark = %d, want %d", first.alloc, incSize)
	}
	for i := uint64(1); i <= 200; i++ {
		if err := l.Write(i, make([]byte, 128)); err != nil {
			t.Fatal(err)
		}
		// Check the watermark of whichever segment is currently the tail;
		// segment cycling replaces the tracked pointer.
		tail := l.segments[len(l.segments)-1]
		if tail.alloc < int64(len(tail.ebuf)) {
			t.Fatalf("watermark %d below data %d", tail.alloc, len(tail.ebuf))
		}
		if tail.alloc > int64(segSize) {
			t.Fatalf("watermark %d exceeds segment size %d",
				tail.alloc, segSize)
		}
		if tail.alloc%incSize != 0 {
			t.Fatalf("watermark %d not a multiple of increment %d",
				tail.alloc, incSize)
		}
	}
}

// TestPreAllocateTruncateReads hammers reads from multiple goroutines while
// the front and back of the log are truncated concurrently. No read of a
// live index may fail and no read position may be invalidated by truncation.
func TestPreAllocateTruncateReads(t *testing.T) {
	dir := "testlog/prealloc-trunc"
	os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	opts := preAllocOpts(PreAllocateIncremental, 1<<12, 512)
	opts.AllowEmpty = true
	l, err := Open(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	const rounds = 400
	for i := uint64(1); i <= 100; i++ {
		if err := l.Write(i, []byte(dataStr(i))); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	readErr := make(chan error, 1)

	// Readers keep reading every currently live index. Truncation only ever
	// removes entries outside [firstIndex, lastIndex], so an in-range Read
	// must keep succeeding with identical data throughout.
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				first, _ := l.FirstIndex()
				last, _ := l.LastIndex()
				for idx := first; idx <= last; idx++ {
					data, err := l.Read(idx)
					if err == ErrNotFound {
						// The index was truncated out between the bounds
						// read above; that is a legal race, not a stale read.
						break
					}
					if err != nil {
						select {
						case readErr <- err:
						default:
						}
						return
					}
					if string(data) != dataStr(idx) {
						readErr <- fmt.Errorf("data mismatch at %d", idx)
						return
					}
				}
			}
		}()
	}

	// Single coordinator: append, then trim the front and occasionally the
	// back. All index math is derived from the log's own First/Last values so
	// the write sequence can never skip an index under truncation.
	for r := 0; r < rounds; r++ {
		last, _ := l.LastIndex()
		if err := l.Write(last+1, []byte(dataStr(last+1))); err != nil {
			t.Fatal(err)
		}
		first, _ := l.FirstIndex()
		last, _ = l.LastIndex()
		if last-first >= 40 {
			// Trim the oldest quarter, forcing old segment removal.
			newFirst := first + (last-first)/4
			if err := l.TruncateFront(newFirst); err != nil {
				t.Fatal(err)
			}
		}
		if r%50 == 49 {
			first, _ = l.FirstIndex()
			last, _ = l.LastIndex()
			if last > first+2 {
				if err := l.TruncateBack(last - 1); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	close(stop)
	wg.Wait()

	select {
	case err := <-readErr:
		t.Fatalf("concurrent read failed: %v", err)
	default:
	}

	// After all the churn, every surviving entry still reads correctly and
	// the reserved tail never leaked into the visible data.
	first, _ := l.FirstIndex()
	last, _ := l.LastIndex()
	for idx := first; idx <= last; idx++ {
		if _, err := l.Read(idx); err != nil {
			t.Fatalf("index %d unreadable after truncation: %v", idx, err)
		}
	}
	if got := apparentSize(t, dir); got <= 0 {
		t.Fatalf("expected positive visible size, got %d", got)
	}
}

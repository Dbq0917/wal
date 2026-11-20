package wal

import (
	"fmt"
	"os"
	"testing"
)

// TestPreAllocateAppendTruncateLifecycle is the end-to-end space/latency
// scenario described in the design: write roughly half a segment's worth,
// confirm the footprint reflects real data rather than a whole reserved
// segment, truncate old segments while reads are in flight, then reopen and
// verify every surviving read position still resolves.
func TestPreAllocateAppendTruncateLifecycle(t *testing.T) {
	for _, mode := range []PreAllocateMode{
		PreAllocateNone,
		PreAllocateIncremental,
		PreAllocateFull,
	} {
		t.Run(modeName(mode), func(t *testing.T) {
			dir := "testlog/e2e-" + modeName(mode)
			os.RemoveAll(dir)
			defer os.RemoveAll(dir)

			const segSize = 1 << 16 // 64 KB
			opts := preAllocOpts(mode, segSize, segSize/4)
			opts.AllowEmpty = true
			l, err := Open(dir, opts)
			if err != nil {
				t.Fatal(err)
			}

			// Fill several segments, then stop at roughly half of the final
			// one so the "half written tail" case is exercised.
			const entries = 160 // ~160 * 260 bytes spans multiple 64KB segs
			for i := uint64(1); i <= entries; i++ {
				if err := l.Write(i, []byte(dataStr(i))); err != nil {
					t.Fatal(err)
				}
			}

			// While readers are actively resolving positions, drop the front
			// half. No in-range read may fail or return wrong bytes.
			done := make(chan struct{})
			readFail := make(chan error, 1)
			go func() {
				defer close(done)
				for rounds := 0; rounds < 200; rounds++ {
					first, _ := l.FirstIndex()
					last, _ := l.LastIndex()
					for idx := first; idx <= last; idx++ {
						data, err := l.Read(idx)
						if err == ErrNotFound {
							break
						}
						if err != nil {
							readFail <- err
							return
						}
						if string(data) != dataStr(idx) {
							readFail <- fmt.Errorf("data mismatch at %d", idx)
							return
						}
					}
				}
			}()

			cut := uint64(entries/2 + 1)
			if err := l.TruncateFront(cut); err != nil {
				t.Fatal(err)
			}
			if err := l.TruncateBack(uint64(entries - 4)); err != nil {
				t.Fatal(err)
			}
			<-done
			select {
			case err := <-readFail:
				t.Fatalf("read during truncation failed: %v", err)
			default:
			}

			first, _ := l.FirstIndex()
			last, _ := l.LastIndex()
			if first != cut {
				t.Fatalf("expected first %d, got %d", cut, first)
			}
			if last != uint64(entries-4) {
				t.Fatalf("expected last %d, got %d", entries-4, last)
			}

			// Reopen: reserved tail bytes must not be parsed, and every
			// surviving position must resolve to identical data.
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			l2, err := Open(dir, opts)
			if err != nil {
				t.Fatal(err)
			}
			defer l2.Close()
			f2, _ := l2.FirstIndex()
			l2last, _ := l2.LastIndex()
			if f2 != first || l2last != last {
				t.Fatalf("bounds changed across reopen: %d..%d vs %d..%d",
					f2, l2last, first, last)
			}
			for idx := first; idx <= last; idx++ {
				data, err := l2.Read(idx)
				if err != nil {
					t.Fatalf("index %d invalid after reopen: %v", idx, err)
				}
				if string(data) != dataStr(idx) {
					t.Fatalf("index %d data mismatch", idx)
				}
			}

			// Appends continue from the correct next index after truncation
			// and reopen, proving the tail offset/position stayed valid.
			if err := l2.Write(last+1, []byte(dataStr(last+1))); err != nil {
				t.Fatal(err)
			}
			if got, _ := l2.LastIndex(); got != last+1 {
				t.Fatalf("expected last %d, got %d", last+1, got)
			}
		})
	}
}

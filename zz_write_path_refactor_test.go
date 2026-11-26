package wal

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type zzWritePathSegment struct {
	index uint64
	name  string
	data  []byte
}

func zzWritePathData(index int) []byte {
	return []byte(fmt.Sprintf("entry-%04d-%s", index, strings.Repeat("x", index%11)))
}

func zzRunWritePathLog(t *testing.T, segmentSize int, format LogFormat, mode string) []zzWritePathSegment {
	t.Helper()

	dir, err := ioutil.TempDir("", "wal-write-parity-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	l, err := Open(dir, &Options{
		NoSync:      true,
		SegmentSize: segmentSize,
		LogFormat:   format,
	})
	if err != nil {
		t.Fatal(err)
	}

	const count = 97
	writeOne := func(index int) {
		t.Helper()
		if err := l.Write(uint64(index), zzWritePathData(index)); err != nil {
			t.Fatalf("%s write %d: %v", mode, index, err)
		}
	}
	writeBatch := func(indexes []int) {
		t.Helper()
		b := new(Batch)
		for _, index := range indexes {
			b.Write(uint64(index), zzWritePathData(index))
		}
		if err := l.WriteBatch(b); err != nil {
			t.Fatalf("%s batch %v: %v", mode, indexes, err)
		}
	}

	switch mode {
	case "single":
		for i := 1; i <= count; i++ {
			writeOne(i)
		}
	case "batch":
		for i := 1; i <= count; {
			end := i + 1 + (i % 3)
			if end > count+1 {
				end = count + 1
			}
			indexes := make([]int, 0, end-i)
			for ; i < end; i++ {
				indexes = append(indexes, i)
			}
			writeBatch(indexes)
		}
	case "alternating":
		for i := 1; i <= count; {
			writeOne(i)
			i++
			indexes := make([]int, 0, 2)
			for j := 0; j < 2 && i <= count; j++ {
				indexes = append(indexes, i)
				i++
			}
			if len(indexes) > 0 {
				writeBatch(indexes)
			}
		}
	default:
		t.Fatalf("unknown mode %q", mode)
	}

	for i := 1; i <= count; i++ {
		data, err := l.Read(uint64(i))
		if err != nil {
			t.Fatalf("%s read %d: %v", mode, i, err)
		}
		if string(data) != string(zzWritePathData(i)) {
			t.Fatalf("%s entry %d mismatch", mode, i)
		}
	}

	segments := make([]zzWritePathSegment, 0, len(l.segments))
	for _, seg := range l.segments {
		segments = append(segments, zzWritePathSegment{index: seg.index, name: filepath.Base(seg.path)})
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	files, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range segments {
		path := filepath.Join(dir, segments[i].name)
		data, err := ioutil.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		segments[i].data = data
	}
	if len(files) != len(segments) {
		t.Fatalf("%s: expected %d segment files, got %d", mode, len(segments), len(files))
	}
	return segments
}

func zzAssertWritePathSegments(t *testing.T, want, got []zzWritePathSegment) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("segment count: want %d, got %d (%+v)", len(want), len(got), got)
	}
	for i := range want {
		if want[i].index != got[i].index || want[i].name != got[i].name {
			t.Fatalf("segment %d identity: want index=%d name=%s, got index=%d name=%s",
				i, want[i].index, want[i].name, got[i].index, got[i].name)
		}
		if string(want[i].data) != string(got[i].data) {
			t.Fatalf("segment %d (%s) contents differ", i, want[i].name)
		}
	}
}

func TestZZWritePathAlternatingParity(t *testing.T) {
	sizes := []int{1, 17, 64, 257, 4096}
	formats := []LogFormat{Binary, JSON}
	for _, format := range formats {
		for _, segmentSize := range sizes {
			name := fmt.Sprintf("format-%d-size-%d", format, segmentSize)
			t.Run(name, func(t *testing.T) {
				single := zzRunWritePathLog(t, segmentSize, format, "single")
				batch := zzRunWritePathLog(t, segmentSize, format, "batch")
				alternating := zzRunWritePathLog(t, segmentSize, format, "alternating")
				zzAssertWritePathSegments(t, single, batch)
				zzAssertWritePathSegments(t, single, alternating)
			})
		}
	}
}

func TestZZWritePathExactFill(t *testing.T) {
	data := []byte("exact-fill")
	encoded, _ := appendBinaryEntry(nil, data)
	segmentSize := len(encoded)

	dir, err := ioutil.TempDir("", "wal-exact-fill-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	l, err := Open(dir, &Options{NoSync: true, SegmentSize: segmentSize})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	if err := l.Write(1, data); err != nil {
		t.Fatal(err)
	}
	if len(l.segments) != 2 {
		t.Fatalf("expected filled segment plus fresh tail, got %d", len(l.segments))
	}
	if l.segments[0].index != 1 || l.segments[1].index != 2 {
		t.Fatalf("unexpected segment indexes: %d, %d", l.segments[0].index, l.segments[1].index)
	}
	if l.lastIndex != 1 || len(l.segments[1].ebuf) != 0 {
		t.Fatalf("full entry did not rotate cleanly: lastIndex=%d tailBytes=%d",
			l.lastIndex, len(l.segments[1].ebuf))
	}

	b := new(Batch)
	b.Write(2, data)
	if err := l.WriteBatch(b); err != nil {
		t.Fatal(err)
	}
	if len(l.segments) != 3 || l.segments[2].index != 3 {
		t.Fatalf("batch exact fill mismatch: count=%d tailIndex=%d",
			len(l.segments), l.segments[len(l.segments)-1].index)
	}

	if err := l.Write(3, data); err != nil {
		t.Fatal(err)
	}
	if len(l.segments) != 4 || l.segments[3].index != 4 {
		t.Fatalf("second single exact fill mismatch: count=%d tailIndex=%d",
			len(l.segments), l.segments[len(l.segments)-1].index)
	}

	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l, err = Open(dir, &Options{NoSync: true, SegmentSize: segmentSize})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	if err := l.Write(4, data); err != nil {
		t.Fatal(err)
	}
	b = new(Batch)
	b.Write(5, data)
	if err := l.WriteBatch(b); err != nil {
		t.Fatal(err)
	}
	if len(l.segments) != 6 || l.segments[5].index != 6 {
		t.Fatalf("reopened alternating exact fill mismatch: count=%d tailIndex=%d",
			len(l.segments), l.segments[len(l.segments)-1].index)
	}

	for index := uint64(1); index <= 5; index++ {
		got, err := l.Read(index)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(data) {
			t.Fatalf("entry %d: expected %q, got %q", index, data, got)
		}
	}
}

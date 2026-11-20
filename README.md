# `wal`
[![GoDoc](https://godoc.org/github.com/tidwall/wal?status.svg)](https://godoc.org/github.com/tidwall/wal)

A simple and fast write ahead log for Go.

## Features

- High durability
- Fast operations
- Monotonic indexes
- Batch writes
- Log truncation from front or back.

## Getting Started

### Installing

To start using `wal`, install Go and run `go get`:

```sh
$ go get -u github.com/tidwall/wal
```

This will retrieve the library.

### Example

```go
// open a new log file
log, err := wal.Open("mylog", nil)

// write some entries
err = log.Write(1, []byte("first entry"))
err = log.Write(2, []byte("second entry"))
err = log.Write(3, []byte("third entry"))

// read an entry
data, err := log.Read(1)
println(string(data))  // output: first entry

// close the log
err = log.Close()
```

Batch writes:

```go

// write three entries as a batch
batch := new(wal.Batch)
batch.Write(1, []byte("first entry"))
batch.Write(2, []byte("second entry"))
batch.Write(3, []byte("third entry"))

err = log.WriteBatch(batch)
```

Truncating:

```go
// write some entries
err = log.Write(1, []byte("first entry"))
...
err = log.Write(1000, []byte("thousandth entry"))

// truncate the log from index starting 350 and ending with 950.
err = log.TruncateFront(350)
err = log.TruncateBack(950)
```



## Segment preallocation

Appending to a segment that must grow on every write can stall when the
filesystem has to allocate new blocks, producing append-latency spikes.
Reserving the whole segment up front removes those stalls but charges the full
segment size to disk even when it is only half written, which inflates the
reported footprint. The `Options.PreAllocate` field selects a point on that
trade-off:

- `PreAllocateNone` reserves nothing. The segment grows write by write, so
  the on-disk size tracks the data closely, but growth points may spike.
- `PreAllocateIncremental` (the default) reserves blocks in fixed increments
  of `Options.PreAllocateSize` as the tail fills. Blocks are pre-reserved to
  smooth appends while the visible file size still equals the real data; at
  most one increment of headroom is held on the open tail.
- `PreAllocateFull` reserves a whole `SegmentSize` when a segment is created.
  Appends never wait on allocation inside a segment, at the cost of the full
  segment being charged to disk while it is open.

Reservation uses `fallocate(FALLOC_FL_KEEP_SIZE)` on Linux, so reserved tail
blocks never change the visible file size and can never be read back as log
entries. When a segment is sealed (segment rollover) or the log is closed, the
reserved headroom is trimmed so sealed segments occupy only their real data.
On platforms without a reservation syscall the option is accepted and writes
behave as `PreAllocateNone`.

Truncation never invalidates a live read position: `Read`, `TruncateFront`
and `TruncateBack` are serialized by the log's lock, and a segment file is
only removed once the truncation's replacement segment is in place.

## Contact

Josh Baker [@tidwall](http://twitter.com/tidwall)

## License

`wal` source code is available under the MIT [License](/LICENSE).

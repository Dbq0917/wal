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



## Crash recovery

A power loss or crash can leave a partially written record at the end of the
tail segment. On `Open`, the log scans each segment sequentially:

- A torn (incomplete) trailing record in the **tail segment** is truncated
  away, all preceding intact entries are kept, and the log opens normally.
- Corruption anywhere else, including a damaged interior record or any
  problem in a non-tail segment, returns `ErrCorrupt`. Entries are never
  silently skipped.
- Every recovery truncation is recorded on disk: the discarded bytes are
  written to a `<segment>.CORRUPT` sidecar file next to the segment (numbered
  `.CORRUPT.1`, `.CORRUPT.2`, ... if one already exists), so the truncation
  leaves an auditable trace instead of dropping data silently.

## Contact

Josh Baker [@tidwall](http://twitter.com/tidwall)

## License

`wal` source code is available under the MIT [License](/LICENSE).

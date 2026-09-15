# BBQr in Go

Go module `github.com/Gangleri42/BBQr/go`. Standard library only.

- `bbqr`: the protocol as [BBQr.md](../BBQr.md) specifies it, parity
  parts included. `Split`, `Join` and an accumulating `Decoder` for the
  Hex, Base32 and Zlib encodings. Hex and Base32 output is byte for
  byte the Python reference's, pinned by golden vectors generated from
  it (`bbqr/testdata/vectors.json`) and by the shared parity vectors in
  `test_data/parity-vectors.json`. Zlib output differs in the DEFLATE
  bitstream, as any compliant stream may.
- `internal/deflate`: a DEFLATE compressor whose back-references stay
  inside the 1 KiB window that `wbits=10` fixes, which `compress/flate`
  cannot promise. Decompression accepts any DEFLATE stream.
- `cmd/bbqr`: encode and decode on the command line.

The `Decoder` is written for scanners: parts arrive one at a time in
any order, duplicates are free, a part from another series is reported
as `ErrSeriesMismatch`, and `Limit` caps the memory a hostile series
can claim. With parity parts in the series it completes as soon as any
`Count` distinct parts are held.

## Use

```
go get github.com/Gangleri42/BBQr/go
```

```
go run github.com/Gangleri42/BBQr/go/cmd/bbqr@latest encode -type P -parity 2 tx.psbt > parts.txt
go run github.com/Gangleri42/BBQr/go/cmd/bbqr@latest decode parts.txt > tx.psbt
```

Parts are one QR content per line, in any order. With parity, `decode`
needs any `Count` of them, `Count` being the number in the header.

## Check

```
cd go && go test ./...
```

Releases are tagged `go/vX.Y.Z`, the form Go expects for a module in
a subdirectory; `go get` names the version without the `go/` prefix.

# BBQr in Go

Go module `github.com/Gangleri42/BBQr/go`. Standard library only.

- `bbqr`: the protocol as [BBQr.md](../BBQr.md) specifies it, parity
  parts included. `Split`, `Join` and an accumulating `Decoder` for the
  Hex, Base32 and Zlib encodings. Hex and Base32 output is byte for
  byte the Python reference's, pinned by golden vectors generated from
  it (`bbqr/testdata/vectors.json`) and by the shared parity vectors in
  `test_data/parity-vectors.json`. Zlib output differs in the DEFLATE
  bitstream, as any compliant stream may.
- `qr`: a QR Code encoder, versions 1 to 40 at all four error
  correction levels, so parts can be rendered without a dependency.
  Its error correction runs over the same GF(2^8) as the parity parts.
  Pinned cell for cell to pyqrcode's matrices (`qr/testdata`) and
  decoded by zbarimg when that is installed.
- `internal/deflate`: a DEFLATE compressor whose back-references stay
  inside the 1 KiB window that `wbits=10` fixes, which `compress/flate`
  cannot promise. Decompression accepts any DEFLATE stream.
- `internal/gf256`, `internal/apng`: the field, and an animated PNG
  writer.
- `cmd/bbqr`: the command line tool, a twin of the Python one. Same
  subcommands, same option names and letters, same output, so the
  examples in the repository README run unchanged.

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
bbqr make tx.psbt -p 2                 # parts as text, 2 parity QR codes
bbqr make tx.psbt -v 27 -o tx.png      # animated PNG, one frame per part
bbqr make BBQr.md -v 21 -o ex.gif -s 3 # animated GIF with a progress bar
bbqr make words.txt -o code.svg        # one SVG per part
bbqr decode < parts.txt                # PSBT as Base64, text as text, ...
bbqr decode --raw < parts.txt > tx.psbt
bbqr table                             # the size table from the spec
```

Install with `go install github.com/Gangleri42/BBQr/go/cmd/bbqr@latest`.
Options may follow the file name, as with the Python command. Where
the two commands differ: Go accepts `--parity 2` and `-p 2` but not
`-p2`, `decode` also takes a PARTS file, transactions print as plain
hex, and JSON input is detected (the Python command's check never
fires).

## Check

```
cd go && go test ./...
```

Releases are tagged `go/vX.Y.Z`, the form Go expects for a module in
a subdirectory; `go get` names the version without the `go/` prefix.

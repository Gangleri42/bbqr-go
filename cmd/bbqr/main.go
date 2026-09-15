// Command bbqr encodes data as a BBQr series and decodes a series back.
//
// Usage:
//
//	bbqr encode [-type B] [-enc auto|H|2|Z] [-minver N] [-maxver N]
//	            [-minsplit N] [-maxsplit N] [-parity N] [input]
//	bbqr decode [-limit N] [parts]
//
// encode reads binary data from the input file, or stdin, and writes
// one QR content per line. With -parity, that many parity parts follow
// the data parts, and any Count of the series, Count being the number
// in the header, recover the data. decode reads parts one per line in
// any order, blank lines ignored, and writes the data to stdout.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gangleri42/BBQr/go/bbqr"
)

func main() {
	err := run(os.Stdin, os.Stdout, os.Stderr, os.Args[1:])
	switch err {
	case nil:
	case errUsage:
		os.Exit(2)
	default:
		fmt.Fprintf(os.Stderr, "bbqr: %v\n", err)
		os.Exit(1)
	}
}

func run(stdin io.Reader, stdout, stderr io.Writer, args []string) error {
	if len(args) == 0 {
		usage(stderr)
		return errUsage
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "encode":
		return cmdEncode(stdin, stdout, stderr, args)
	case "decode":
		return cmdDecode(stdin, stdout, stderr, args)
	}
	usage(stderr)
	return fmt.Errorf("unknown command %q", cmd)
}

var errUsage = errors.New("bad usage")

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage:
  bbqr encode [-type B] [-enc auto|H|2|Z] [-minver N] [-maxver N] [-minsplit N] [-maxsplit N] [-parity N] [input]
  bbqr decode [-limit N] [parts]

encode reads data from the input file or stdin and writes one QR
content per line. -parity adds that many parity parts; any Count parts
of the series, Count being the number in the header, recover the data.
decode reads parts one per line, in any order, and writes the data.`)
}

func cmdEncode(stdin io.Reader, stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("encode", flag.ContinueOnError)
	typ := fs.String("type", "B", "file type: P, T, J, C, U, B or a name: psbt, txn, json, cbor, text, binary")
	enc := fs.String("enc", "auto", "encoding: auto, H, 2 or Z")
	var opts bbqr.SplitOptions
	fs.IntVar(&opts.MinVersion, "minver", 0, "smallest QR version (default 5)")
	fs.IntVar(&opts.MaxVersion, "maxver", 0, "largest QR version (default 40)")
	fs.IntVar(&opts.MinSplit, "minsplit", 0, "smallest number of data parts")
	fs.IntVar(&opts.MaxSplit, "maxsplit", 0, "largest number of data parts (default 1295)")
	fs.IntVar(&opts.Parity, "parity", 0, "parity parts to add; any Count parts of the series recover the data")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	t, err := parseType(*typ)
	if err != nil {
		return err
	}
	switch strings.ToUpper(*enc) {
	case "AUTO", "":
	case "H":
		opts.Encoding = bbqr.EncHex
	case "2":
		opts.Encoding = bbqr.EncBase32
	case "Z":
		opts.Encoding = bbqr.EncZlib
	default:
		return fmt.Errorf("bad -enc %q", *enc)
	}
	data, err := readInput(stdin, fs.Args())
	if err != nil {
		return err
	}
	s, err := bbqr.Split(data, t, opts)
	if err != nil {
		return err
	}
	for _, p := range s.Parts {
		fmt.Fprintln(stdout, p)
	}
	switch extra := len(s.Parts) - s.Count; {
	case len(s.Parts) == 1:
		fmt.Fprintf(stderr, "1 part, QR version %d\n", s.Version)
	case extra > 0:
		fmt.Fprintf(stderr, "%d parts, QR version %d: %d data + %d parity, any %d recover the data\n",
			len(s.Parts), s.Version, s.Count, extra, s.Count)
	default:
		fmt.Fprintf(stderr, "%d parts, QR version %d\n", len(s.Parts), s.Version)
	}
	return nil
}

func cmdDecode(stdin io.Reader, stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("decode", flag.ContinueOnError)
	limit := fs.Int("limit", 1<<30, "decoded payload size cap in bytes")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	parts, err := readParts(stdin, fs.Args())
	if err != nil {
		return err
	}
	d := bbqr.Decoder{Limit: *limit}
	for _, p := range parts {
		if err := d.Add(p); err != nil {
			return err
		}
	}
	typ, data, err := d.Result()
	if err != nil {
		return err
	}
	if _, err := stdout.Write(data); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "type %c, %d bytes\n", typ, len(data))
	return nil
}

// readInput reads all data from the named file, or stdin with no file.
func readInput(stdin io.Reader, args []string) ([]byte, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("want at most one input file, got %d", len(args))
	}
	if len(args) == 1 {
		return os.ReadFile(args[0])
	}
	return io.ReadAll(stdin)
}

// readParts reads BBQr parts, one per line, skipping blank lines.
func readParts(stdin io.Reader, args []string) ([]string, error) {
	raw, err := readInput(stdin, args)
	if err != nil {
		return nil, err
	}
	var parts []string
	for line := range strings.Lines(string(raw)) {
		if line = strings.TrimSpace(line); line != "" {
			parts = append(parts, line)
		}
	}
	if len(parts) == 0 {
		return nil, errors.New("no parts found")
	}
	return parts, nil
}

func parseType(s string) (byte, error) {
	if len(s) == 1 && s[0] >= 'A' && s[0] <= 'Z' {
		return s[0], nil
	}
	switch strings.ToLower(s) {
	case "psbt":
		return bbqr.TypePSBT, nil
	case "txn":
		return bbqr.TypeTxn, nil
	case "json":
		return bbqr.TypeJSON, nil
	case "cbor":
		return bbqr.TypeCBOR, nil
	case "text":
		return bbqr.TypeText, nil
	case "binary":
		return bbqr.TypeBinary, nil
	}
	return 0, fmt.Errorf("bad -type %q", s)
}

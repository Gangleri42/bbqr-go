// Command bbqr encodes files as BBQr series and decodes series back.
// It takes the same subcommands and options as the Python reference
// command, so the examples in the repository's README work with either.
//
// Usage:
//
//	bbqr make [options] INFILE
//	bbqr decode [-r|--raw] [--limit N] [PARTS]
//	bbqr table
//
// make reads INFILE, or stdin for "-", detects the file type unless
// --filetype is given, and writes the parts as text, as QR codes on the
// terminal, or as PNG, GIF or SVG images. With --parity, that many
// parity parts follow the data parts and any Count of the series, Count
// being the number in the header, recover the data. decode reads parts
// one per line in any order and prints the data in a form that suits
// its file type, or the raw bytes with --raw.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gangleri42/bbqr-go"
	"github.com/Gangleri42/bbqr-go/internal/apng"
	"github.com/Gangleri42/bbqr-go/qr"
)

func main() {
	err := run(os.Stdin, os.Stdout, os.Stderr, os.Args[1:])
	switch {
	case err == nil:
	case errors.Is(err, errUsage):
		os.Exit(2)
	default:
		fmt.Fprintf(os.Stderr, "bbqr: %v\n", err)
		os.Exit(1)
	}
}

var errUsage = errors.New("bad usage")

const usageText = `usage:
  bbqr make [options] INFILE
  bbqr decode [-r|--raw] [--limit N] [PARTS]
  bbqr table

make options:
  -e, --encoding CHAR     force the low-level encoding: H, 2 or Z
  -t, --filetype CHAR     force the file type code: P T J C U B X R S E
  -v, --max-version N     largest QR version to use (default 40)
  -m, --min-split N       produce at least this many QR codes (default 1)
  -p, --parity N          add this many parity QR codes; any N of the series recover the data
  -d, --frame-delay MS    delay between animation frames (default 250)
  -s, --scale N           pixels per QR module in images (default 4)
  -o, --outfile PATH      "-" for text (default), "stdout" for QR codes on the terminal,
                          or a file ending in .png, .gif or .svg
  -r, --randomize-order   shuffle the parts
      --fake-data N       encode N zero bytes instead of INFILE
      --min-version N     smallest QR version to use (default 5)
      --max-split N       produce at most this many QR codes (default 1295)

decode reads parts from PARTS or stdin, one per line in any order, and
prints the data as the file type suggests: PSBT as Base64, transactions
as hex, JSON indented, text as is. --raw writes the bytes instead.`

func run(stdin io.Reader, stdout, stderr io.Writer, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usageText)
		return errUsage
	}
	switch cmd, rest := args[0], args[1:]; cmd {
	case "make":
		return cmdMake(stdin, stdout, stderr, rest)
	case "decode":
		return cmdDecode(stdin, stdout, stderr, rest)
	case "table":
		return cmdTable(stdout)
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, usageText)
		return nil
	default:
		fmt.Fprintln(stderr, usageText)
		return fmt.Errorf("%w: unknown command %q", errUsage, cmd)
	}
}

// newFlags returns a flag set whose errors and help print our usage.
func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, usageText) }
	return fs
}

// parseArgs parses flags and positional arguments in any order, the way
// the Python command accepts "bbqr make file.psbt -v 27".
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

// Both spellings of an option bind to one variable.
func intOpt(fs *flag.FlagSet, p *int, long, short string, value int) {
	fs.IntVar(p, long, value, "")
	if short != "" {
		fs.IntVar(p, short, value, "")
	}
}

func strOpt(fs *flag.FlagSet, p *string, long, short, value string) {
	fs.StringVar(p, long, value, "")
	if short != "" {
		fs.StringVar(p, short, value, "")
	}
}

func boolOpt(fs *flag.FlagSet, p *bool, long, short string) {
	fs.BoolVar(p, long, false, "")
	if short != "" {
		fs.BoolVar(p, short, false, "")
	}
}

// helpOrErr turns flag's -h into a clean exit and anything else into a
// usage error.
func helpOrErr(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return fmt.Errorf("%w: %v", errUsage, err)
}

var typeNames = map[byte]string{
	'P': "PSBT", 'T': "Transaction", 'J': "JSON", 'C': "CBOR", 'U': "Unicode Text",
	'X': "Executable", 'B': "Binary", 'R': "KT Rx", 'S': "KT Tx", 'E': "KT PSBT",
}

func cmdMake(stdin io.Reader, stdout, stderr io.Writer, args []string) error {
	fs := newFlags("make", stderr)
	var (
		encoding, fileType, outfile string
		frameDelay, scale, fakeData int
		randomize                   bool
		opts                        bbqr.SplitOptions
	)
	strOpt(fs, &encoding, "encoding", "e", "")
	strOpt(fs, &fileType, "filetype", "t", "")
	intOpt(fs, &opts.MaxVersion, "max-version", "v", 40)
	intOpt(fs, &opts.MinSplit, "min-split", "m", 1)
	intOpt(fs, &opts.Parity, "parity", "p", 0)
	intOpt(fs, &frameDelay, "frame-delay", "d", 250)
	intOpt(fs, &scale, "scale", "s", 4)
	strOpt(fs, &outfile, "outfile", "o", "")
	boolOpt(fs, &randomize, "randomize-order", "r")
	intOpt(fs, &fakeData, "fake-data", "", 0)
	intOpt(fs, &opts.MinVersion, "min-version", "", 0)
	intOpt(fs, &opts.MaxSplit, "max-split", "", 0)
	positional, err := parseArgs(fs, args)
	if err != nil {
		return helpOrErr(err)
	}

	switch encoding {
	case "":
	case "H", "2", "Z":
		opts.Encoding = encoding[0]
	default:
		return fmt.Errorf("%w: --encoding must be H, 2 or Z, not %q", errUsage, encoding)
	}
	var typ byte
	if fileType != "" {
		if len(fileType) != 1 || typeNames[fileType[0]] == "" {
			return fmt.Errorf("%w: --filetype must be one of P T J C U B X R S E, not %q", errUsage, fileType)
		}
		typ = fileType[0]
	}
	if scale < 1 {
		return fmt.Errorf("%w: --scale must be at least 1", errUsage)
	}

	var raw []byte
	name := ""
	switch {
	case fakeData > 0:
		// For Mk4/Q: maximum psbt size.
		raw = make([]byte, fakeData)
	case len(positional) != 1:
		fmt.Fprintln(stderr, usageText)
		return fmt.Errorf("%w: make needs one INFILE", errUsage)
	case positional[0] == "-":
		if raw, err = io.ReadAll(stdin); err != nil {
			return err
		}
	default:
		name = positional[0]
		if raw, err = os.ReadFile(name); err != nil {
			return err
		}
	}
	if len(raw) <= 5 {
		return errors.New("input data too short")
	}
	if typ == 0 {
		if typ, raw, err = detectType(name, raw); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "Detected file type: %c -> %s\n", typ, typeNames[typ])
	}

	s, err := bbqr.Split(raw, typ, opts)
	if err != nil {
		return err
	}
	switch extra := len(s.Parts) - s.Count; {
	case len(s.Parts) == 1:
		fmt.Fprintf(stderr, "A single QR version %d will be needed.\n", s.Version)
	case extra > 0:
		fmt.Fprintf(stderr, "Need %d QR's each of version %d: %d data + %d parity, any %d recover the data.\n",
			len(s.Parts), s.Version, s.Count, extra, s.Count)
	default:
		fmt.Fprintf(stderr, "Need %d QR's each of version %d.\n", len(s.Parts), s.Version)
	}
	if randomize {
		rand.Shuffle(len(s.Parts), func(i, j int) { s.Parts[i], s.Parts[j] = s.Parts[j], s.Parts[i] })
	}

	if outfile == "" || outfile == "-" {
		for _, p := range s.Parts {
			fmt.Fprintln(stdout, p)
			fmt.Fprintln(stdout)
		}
		return nil
	}
	ext := strings.ToLower(filepath.Ext(outfile))
	if outfile != "stdout" && ext != ".png" && ext != ".gif" && ext != ".svg" {
		return fmt.Errorf("unsupported output file type: %s", strings.TrimPrefix(ext, "."))
	}

	fmt.Fprint(stderr, "Building QR images... ")
	codes := make([]*qr.Code, len(s.Parts))
	for i, p := range s.Parts {
		if codes[i], err = qr.Encode(p, qr.L, s.Version); err != nil {
			return err
		}
	}
	fmt.Fprintln(stderr, "done!")

	switch {
	case outfile == "stdout":
		for _, c := range codes {
			fmt.Fprint(stdout, terminal(c))
			fmt.Fprint(stdout, "\n\n\n")
		}
		return nil
	case ext == ".svg":
		// One file per part; the animation has no SVG form.
		root := strings.TrimSuffix(outfile, filepath.Ext(outfile))
		for i, c := range codes {
			fn := outfile
			if len(codes) > 1 {
				fn = fmt.Sprintf("%s-%d%s", root, i+1, filepath.Ext(outfile))
			}
			if err := os.WriteFile(fn, []byte(svg(c, scale)), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "Created file %q\n", fn)
		}
		return nil
	}

	frames := make([]*image.Gray, len(codes))
	for i, c := range codes {
		frames[i] = frame(c, scale, i, len(codes))
	}
	f, err := os.Create(outfile)
	if err != nil {
		return err
	}
	defer f.Close()
	delay := time.Duration(frameDelay) * time.Millisecond
	switch {
	case len(frames) == 1 && ext == ".png":
		err = png.Encode(f, frames[0])
	case ext == ".png":
		imgs := make([]image.Image, len(frames))
		for i, fr := range frames {
			imgs[i] = fr
		}
		err = apng.Encode(f, imgs, delay)
	case len(frames) == 1:
		err = gif.Encode(f, paletted(frames[0]), nil)
	default:
		g := &gif.GIF{LoopCount: 0}
		for _, fr := range frames {
			g.Image = append(g.Image, paletted(fr))
			g.Delay = append(g.Delay, frameDelay/10)
		}
		err = gif.EncodeAll(f, g)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Created %q with %d frames.\n", outfile, len(frames))
	return f.Close()
}

// detectType guesses the file type the way the Python command does:
// by file name for PSBT, by prefix for transactions in hex or binary,
// then JSON, text and binary. Hex transactions come back as bytes.
func detectType(name string, raw []byte) (byte, []byte, error) {
	switch {
	case strings.Contains(strings.ToLower(name), ".psb"):
		if !bytes.HasPrefix(raw, []byte("psbt\xff")) {
			return 0, nil, fmt.Errorf("%s: not a raw PSBT (Base64 or Hex encoded PSBT saved to disk? We want raw bytes)", name)
		}
		return 'P', raw, nil
	case bytes.HasPrefix(raw, []byte("01000000")) || bytes.HasPrefix(raw, []byte("02000000")):
		b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil {
			return 0, nil, fmt.Errorf("%s: looks like a hex transaction but is not hex: %v", name, err)
		}
		return 'T', b, nil
	case bytes.HasPrefix(raw, []byte{1, 0, 0, 0}) || bytes.HasPrefix(raw, []byte{2, 0, 0, 0}):
		return 'T', raw, nil
	case raw[0] == '{':
		return 'J', raw, nil
	case utf8.Valid(raw):
		return 'U', raw, nil
	}
	return 'B', raw, nil
}

func cmdDecode(stdin io.Reader, stdout, stderr io.Writer, args []string) error {
	fs := newFlags("decode", stderr)
	var raw bool
	var limit int
	boolOpt(fs, &raw, "raw", "r")
	intOpt(fs, &limit, "limit", "", 1<<30)
	positional, err := parseArgs(fs, args)
	if err != nil {
		return helpOrErr(err)
	}
	var input []byte
	switch len(positional) {
	case 0:
		if f, ok := stdin.(*os.File); ok {
			if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
				fmt.Fprintln(stderr, "Paste data received, in any order here. Newlines between them.")
			}
		}
		if input, err = io.ReadAll(stdin); err != nil {
			return err
		}
	case 1:
		if input, err = os.ReadFile(positional[0]); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: decode takes at most one PARTS file", errUsage)
	}

	d := bbqr.Decoder{Limit: limit}
	for line := range strings.Lines(string(input)) {
		if line = strings.TrimSpace(line); line != "" {
			if err := d.Add(line); err != nil {
				return err
			}
		}
	}
	typ, data, err := d.Result()
	if err != nil {
		return err
	}
	if raw {
		_, err := stdout.Write(data)
		return err
	}
	switch typ {
	case 'J':
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, data, "", "  "); err != nil {
			return fmt.Errorf("JSON payload does not parse: %v", err)
		}
		fmt.Fprintln(stdout, pretty.String())
	case 'U':
		fmt.Fprintln(stdout, string(data))
	case 'P':
		fmt.Fprintln(stderr, "PSBT File:")
		fmt.Fprintln(stdout, base64.StdEncoding.EncodeToString(data))
	case 'T':
		fmt.Fprintln(stderr, "Bitcoin Transaction:")
		fmt.Fprintln(stdout, hex.EncodeToString(data))
	case 'C':
		fmt.Fprintf(stderr, "%d bytes of raw CBOR data... (not shown)\n", len(data))
	case 'X', 'B', 'R', 'S', 'E':
		fmt.Fprintf(stderr, "%s: %d bytes of binary data (not shown)\n", typeNames[typ], len(data))
	default:
		fmt.Fprintf(stdout, "Unknown file type code: %c\n", typ)
	}
	return nil
}

// cmdTable prints the size table used in the spec.
func cmdTable(stdout io.Writer) error {
	hdr := "Vers | Pixels  | Chars |  Hex |  Base32 | 2xBase32 | 5xBase32 | 10xBase32"
	fmt.Fprintln(stdout, hdr)
	var dashes []string
	for _, col := range strings.Split(hdr, "|") {
		dashes = append(dashes, strings.Repeat("-", len(col)))
	}
	fmt.Fprintln(stdout, strings.Join(dashes, "|"))
	for v := 1; v <= bbqr.MaxVersion; v++ {
		chars := bbqr.VersionChars(v)
		if chars < 1500 && v != 1 && v != 11 && v != 14 {
			continue
		}
		size := 17 + 4*v
		capacity := chars - bbqr.HeaderLen
		b32 := capacity / 8 * 5
		fmt.Fprintf(stdout, " %2d  | %3dx%-3d |  %4d | %5d| %7d | %8d | %8d | %8d\n",
			v, size, size, chars, capacity/2, b32, 2*b32, 5*b32, 10*b32)
	}
	return nil
}

// frame renders one part for an animation: 10 modules of quiet zone
// and, for a series, a progress bar along the bottom with the current
// part's segment black and the others grey.
func frame(c *qr.Code, scale, index, count int) *image.Gray {
	img := c.Image(scale, 10)
	if count < 2 {
		return img
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	pw := w / count
	left := (w - pw*count) / 2
	bar := scale / 2
	y := h - bar - scale/2 - 1
	for j := 0; j < count; j++ {
		shade := byte(128)
		if j == index {
			shade = 0
		}
		for yy := y; yy <= y+bar; yy++ {
			for x := left + j*pw; x <= left+(j+1)*pw && x < w; x++ {
				img.Pix[yy*img.Stride+x] = shade
			}
		}
	}
	return img
}

// paletted converts a black, white and grey frame for GIF encoding.
func paletted(g *image.Gray) *image.Paletted {
	p := image.NewPaletted(g.Bounds(), color.Palette{color.Gray{Y: 255}, color.Gray{Y: 0}, color.Gray{Y: 128}})
	for i, v := range g.Pix {
		switch v {
		case 255:
			p.Pix[i] = 0
		case 0:
			p.Pix[i] = 1
		default:
			p.Pix[i] = 2
		}
	}
	return p
}

// terminal draws a code with ANSI background colours, two columns per
// module, inside a four module quiet zone.
func terminal(c *qr.Code) string {
	const quiet = 4
	var sb strings.Builder
	for y := -quiet; y < c.Size+quiet; y++ {
		for x := -quiet; x < c.Size+quiet; x++ {
			if x >= 0 && y >= 0 && x < c.Size && y < c.Size && c.Dark(x, y) {
				sb.WriteString("\x1b[40m  ")
			} else {
				sb.WriteString("\x1b[47m  ")
			}
		}
		sb.WriteString("\x1b[0m\n")
	}
	return sb.String()
}

// svg draws a code as one path of unit squares in module coordinates,
// scaled to scale pixels per module with a four module quiet zone.
func svg(c *qr.Code, scale int) string {
	const quiet = 4
	side := c.Size + 2*quiet
	var d strings.Builder
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; {
			if !c.Dark(x, y) {
				x++
				continue
			}
			run := 0
			for x+run < c.Size && c.Dark(x+run, y) {
				run++
			}
			fmt.Fprintf(&d, "M%d %dh%dv1h-%dz", x+quiet, y+quiet, run, run)
			x += run
		}
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" shape-rendering="crispEdges">
<path d="%s" fill="#000"/>
</svg>
`, side*scale, side*scale, side, side, d.String())
}

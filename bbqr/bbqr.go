// Package bbqr implements the [BBQr] protocol for transmitting binary
// data over a series of QR codes, sometimes called "animated QR codes".
//
// A BBQr series splits an encoded payload into equal sized parts and
// prepends an 8 character header to each part:
//
//	B$                  fixed protocol prefix
//	H                   one character encoding: H=Hex, 2=Base32, Z=Zlib+Base32
//	P                   one character file type: P=PSBT, T=Transaction, ...
//	05                  number of parts needed to decode, two base 36 digits
//	00                  index of this part, two base 36 digits
//
// All characters are from the QR alphanumeric character set
// (0-9A-Z$%*+-./:), so QR encoders can use the dense alphanumeric mode
// at 5.5 bits per character. Encoders are free to pick the encoding and
// QR version; decoders must accept all three encodings and parts
// scanned in any order.
//
// A sender may append parity parts, whose indexes follow the data
// parts, so that any Count parts of the series recover the data
// (BBQr.md, "Parity Parts"). The data parts stay a normal series;
// parity is systematic Reed-Solomon over GF(2^8). Receivers written
// before parity parts reject the extra indexes.
//
// The implementation is byte-for-byte compatible with the public
// domain [reference implementation] for the Hex and Base32 encodings,
// parity parts included. Zlib encoded output differs in the DEFLATE
// bitstream (any compliant decoder accepts it): the standard fixes
// wbits=10, a 1 KiB history, while Go's compress/flate cannot restrict
// its window, so Split's DEFLATE comes from internal/deflate, whose
// back-references never exceed 1 KiB. Zlib decoding accepts any
// DEFLATE stream.
//
// [BBQr]: https://github.com/coinkite/BBQr
// [reference implementation]: https://github.com/coinkite/BBQr/tree/master/python
package bbqr

import (
	"errors"
	"fmt"
	"strings"
)

// Encodings, selected by the third character of the part header.
const (
	EncBase32 = '2' // RFC 4648 Base32, padding omitted
	EncHex    = 'H' // uppercase hexadecimal
	EncZlib   = 'Z' // DEFLATE (zlib wbits=10, no header), then Base32
)

// File type codes, selected by the fourth character of the part header.
const (
	TypePSBT   = 'P' // BIP-174 partially signed Bitcoin transaction
	TypeTxn    = 'T' // ready to send Bitcoin wire transaction
	TypeJSON   = 'J' // JSON data
	TypeCBOR   = 'C' // CBOR data
	TypeText   = 'U' // Unicode text, UTF-8 encoded
	TypeBinary = 'B' // generic binary data; also used while experimenting
	TypeExec   = 'X' // executable data, platform dependent
)

// ErrSeriesMismatch reports a part that belongs to a different series
// than the one being collected. Receivers use it to start a fresh
// collection instead of failing the scan.
var ErrSeriesMismatch = errors.New("bbqr: part from a different series")

// ErrLimit reports a decoded payload exceeding the configured size
// limit. No amount of rescanning makes the series acceptable.
var ErrLimit = errors.New("bbqr: payload exceeds size limit")

const (
	// HeaderLen is the fixed length of the part header.
	HeaderLen = 8
	// MaxParts is the largest number of data parts expressible in the
	// two base 36 header digits.
	MaxParts = 1295
	// MaxParityIndex is the largest index a parity part can carry:
	// GF(2^8) has 256 points, and every part of a series is one.
	MaxParityIndex = 255
	// MaxVersion is the largest QR code version.
	MaxVersion = 40
)

// defaultMinVersion is the reference default for the smallest QR
// version considered by Split.
const defaultMinVersion = 5

// versionChars[v-1] is the number of alphanumeric characters a version v
// QR code holds at error correction level L, per ISO/IEC 18004. The
// values match pyqrcode.tables.data_capacity[v]["L"][2], which the
// reference implementation consults.
var versionChars = [MaxVersion]int{
	25, 47, 77, 114, 154, 195, 224, 279, 335, 395,
	468, 535, 619, 667, 758, 854, 938, 1046, 1153, 1249,
	1352, 1460, 1588, 1704, 1853, 1990, 2132, 2223, 2369, 2520,
	2677, 2840, 3009, 3183, 3351, 3537, 3729, 3927, 4087, 4296,
}

// Header is the parsed header of a single BBQr part.
type Header struct {
	Encoding byte // EncHex, EncBase32 or EncZlib
	FileType byte // TypePSBT, TypeTxn, ... or any other reserved code
	Count    int  // data parts in the series, and parts needed to decode it, 1..MaxParts
	Index    int  // 0..Count-1 for a data part, Count..MaxParityIndex for a parity part
}

// Parity reports whether the part carries parity rather than data.
func (h Header) Parity() bool {
	return h.Index >= h.Count
}

// ParseHeader parses and validates the 8 character header of a part.
func ParseHeader(part string) (Header, error) {
	var h Header
	if len(part) < HeaderLen {
		return h, errors.New("bbqr: part shorter than header")
	}
	if part[0:2] != "B$" {
		return h, errors.New("bbqr: missing B$ prefix")
	}
	switch h.Encoding = part[2]; h.Encoding {
	case EncHex, EncBase32, EncZlib:
	default:
		return h, fmt.Errorf("bbqr: unknown encoding %q", h.Encoding)
	}
	h.FileType = part[3]
	if !base36digit(h.FileType) {
		return h, fmt.Errorf("bbqr: invalid file type %q", h.FileType)
	}
	var ok bool
	if h.Count, ok = base36pair(part[4:6]); !ok || h.Count < 1 {
		return h, fmt.Errorf("bbqr: invalid part count %q", part[4:6])
	}
	// Parity indexes exist only for series of two or more data parts.
	h.Index, ok = base36pair(part[6:8])
	if !ok || (h.Index >= h.Count && (h.Count < 2 || h.Index > MaxParityIndex)) {
		return h, fmt.Errorf("bbqr: invalid part index %q", part[6:8])
	}
	return h, nil
}

// Series is the result of Split: the parts to render as QR codes, all
// at the same QR version.
type Series struct {
	Version  int      // QR version every part fits, for uniform rendering
	Encoding byte     // encoding actually used (Zlib may fall back to Base32)
	FileType byte     // file type from the Split call
	Count    int      // data parts; Parts[Count:] are parity, and any Count parts decode
	Parts    []string // the QR contents, header first
}

// SplitOptions tunes Split. The zero value applies the reference
// defaults: versions 5..40, 1..MaxParts parts, no parity.
type SplitOptions struct {
	Encoding   byte // 0 for auto (EncZlib, falling back to EncBase32), or an Enc* constant
	MinVersion int  // smallest QR version considered, default 5
	MaxVersion int  // largest QR version considered, default MaxVersion
	MinSplit   int  // smallest acceptable number of data parts, default 1
	MaxSplit   int  // largest acceptable number of data parts, default MaxParts
	Parity     int  // parity parts to append, 0..MaxParityIndex-1; none when the data fits one QR
}

// Split encodes data into a BBQr series of the given file type. It
// picks the encoding and QR version as described by the standard:
// unless an encoding is forced, the data is trial compressed and sent
// as EncZlib only when compression actually shrinks it. The data parts
// are equal length except possibly the last, and every part decodes to
// a whole number of bytes.
//
// With Parity set and the data needing more than one QR, that many
// parity parts follow the data parts at the same QR version, so a
// receiver holding any Count of the parts recovers the data. A single
// QR has nothing to lose, so it gets no parity.
func Split(data []byte, fileType byte, opts SplitOptions) (Series, error) {
	if len(data) == 0 {
		return Series{}, errors.New("bbqr: nothing to encode")
	}
	if fileType < 'A' || fileType > 'Z' {
		return Series{}, fmt.Errorf("bbqr: invalid file type %q", fileType)
	}
	if opts.Parity < 0 || opts.Parity >= MaxParityIndex {
		return Series{}, fmt.Errorf("bbqr: parity %d outside 0..%d", opts.Parity, MaxParityIndex-1)
	}
	enc, stream, err := encodeBytes(data, opts.Encoding)
	if err != nil {
		return Series{}, err
	}
	encoded := encodeText(enc, stream)
	groupChars, groupBytes := group(enc)
	ver, count, perEach, err := findBestVersion(len(encoded), groupChars, opts, 0)
	if err != nil {
		return Series{}, err
	}
	parity := opts.Parity
	if parity > 0 && count >= 2 {
		// Parity parts are one symbol group longer than data parts;
		// leave room for that now that the data needs several QR anyway.
		ver, count, perEach, err = findBestVersion(len(encoded), groupChars, opts, groupChars)
		if err != nil {
			return Series{}, err
		}
		if count+parity > MaxParityIndex+1 {
			return Series{}, fmt.Errorf("bbqr: %d data and %d parity parts exceed %d indexes",
				count, parity, MaxParityIndex+1)
		}
	} else {
		parity = 0
	}
	s := Series{Version: ver, Encoding: enc, FileType: fileType, Count: count}
	header := "B$" + string(enc) + string(fileType) + base36(count)
	for n, off := 0, 0; off < len(encoded); n, off = n+1, off+perEach {
		s.Parts = append(s.Parts, header+base36(n)+encoded[off:min(off+perEach, len(encoded))])
	}
	if parity > 0 {
		bData := perEach / groupChars * groupBytes
		for i, blk := range parityBlocks(stream, count, bData, groupBytes, parity) {
			s.Parts = append(s.Parts, header+base36(count+i)+encodeText(enc, blk))
		}
	}
	return s, nil
}

// Join assembles a BBQr series from its parts, in any order and
// tolerating duplicates, and returns the file type and the decoded
// data. Any Count parts of a series with parity are enough; without
// parity every part is needed. All parts must agree on encoding, file
// type and count. A Zlib payload is capped at DefaultLimit bytes; use
// a Decoder with an explicit Limit for larger series.
func Join(parts []string) (byte, []byte, error) {
	if len(parts) == 0 {
		return 0, nil, errors.New("bbqr: no parts")
	}
	first, err := ParseHeader(parts[0])
	if err != nil {
		return 0, nil, err
	}
	data := make([][]byte, first.Count)
	parity := map[int][]byte{}
	for _, p := range parts {
		h, err := ParseHeader(p)
		if err != nil {
			return 0, nil, err
		}
		if h.Encoding != first.Encoding || h.FileType != first.FileType || h.Count != first.Count {
			return 0, nil, errors.New("bbqr: conflicting part headers")
		}
		body, err := decodePart(h.Encoding, p[HeaderLen:])
		if err != nil {
			return 0, nil, err
		}
		if h.Parity() {
			err = place(parity[h.Index], body, h.Index, func(b []byte) { parity[h.Index] = b })
		} else {
			err = place(data[h.Index], body, h.Index, func(b []byte) { data[h.Index] = b })
		}
		if err != nil {
			return 0, nil, err
		}
	}
	_, groupBytes := group(first.Encoding)
	if err := completeData(data, parity, groupBytes); err != nil {
		return 0, nil, err
	}
	raw := concat(data)
	if first.Encoding == EncZlib {
		if raw, err = inflate(raw, DefaultLimit); err != nil {
			return 0, nil, err
		}
	}
	return first.FileType, raw, nil
}

// place stores body as part index unless the same body is already
// held; a different body under the same index is an error.
func place(held, body []byte, index int, store func([]byte)) error {
	if held == nil {
		store(body)
		return nil
	}
	if string(held) != string(body) {
		return fmt.Errorf("bbqr: duplicate part %d has different content", index)
	}
	return nil
}

// concat joins the data blocks into one payload.
func concat(blocks [][]byte) []byte {
	size := 0
	for _, b := range blocks {
		size += len(b)
	}
	raw := make([]byte, 0, size)
	for _, b := range blocks {
		raw = append(raw, b...)
	}
	return raw
}

// DefaultLimit is the default maximum decoded payload size accepted by
// a Decoder, in bytes.
const DefaultLimit = 1 << 20

// Decoder accumulates the parts of a single BBQr series in any order.
// It is the scanning counterpart of Split; for a complete set of parts
// held in memory, Join is simpler.
//
// The zero value is ready to use and limits the decoded payload to
// DefaultLimit bytes.
type Decoder struct {
	// Limit caps the decoded payload size in bytes; zero means
	// DefaultLimit. The cap guards memory constrained receivers
	// against hostile or absurd series. Parity parts count against it.
	Limit int

	enc    byte
	typ    byte
	data   [][]byte       // one slot per data part
	parity map[int][]byte // parity parts by index
	have   int            // distinct data parts held
	size   int
}

// Add consumes one scanned part. Adding a part identical to one seen
// before is a no-op; any other inconsistency is an error.
func (d *Decoder) Add(part string) error {
	h, err := ParseHeader(part)
	if err != nil {
		return err
	}
	if d.data != nil && (h.Encoding != d.enc || h.FileType != d.typ || h.Count != len(d.data)) {
		return ErrSeriesMismatch
	}
	// Decode before committing anything: a part rejected here must not
	// lock a fresh decoder to its series.
	body, err := decodePart(h.Encoding, part[HeaderLen:])
	if err != nil {
		return err
	}
	if d.data == nil {
		d.enc, d.typ = h.Encoding, h.FileType
		d.data = make([][]byte, h.Count)
		d.parity = map[int][]byte{}
	}
	held := d.data[min(h.Index, len(d.data)-1)]
	if h.Parity() {
		held = d.parity[h.Index]
	}
	if held != nil {
		if string(held) != string(body) {
			return fmt.Errorf("bbqr: duplicate part %d has different content", h.Index)
		}
		return nil
	}
	if d.size+len(body) > d.limit() {
		return fmt.Errorf("%w (%d bytes)", ErrLimit, d.limit())
	}
	if h.Parity() {
		d.parity[h.Index] = body
	} else {
		d.data[h.Index] = body
		d.have++
	}
	d.size += len(body)
	return nil
}

// Progress reports the number of distinct useful parts received and
// the number needed. Both are zero before the first part.
func (d *Decoder) Progress() (have, need int) {
	return min(d.have+len(d.parity), len(d.data)), len(d.data)
}

// Complete reports whether enough parts have been received: every data
// part, or parity parts standing in for the missing ones.
func (d *Decoder) Complete() bool {
	return d.data != nil && d.have+len(d.parity) >= len(d.data)
}

// Result returns the file type and decoded data of a complete series.
func (d *Decoder) Result() (byte, []byte, error) {
	if !d.Complete() {
		have, need := d.Progress()
		return 0, nil, fmt.Errorf("bbqr: %d of %d parts received", have, need)
	}
	// Work on a copy of the slots so a failed rebuild leaves the
	// decoder as it was.
	data := make([][]byte, len(d.data))
	copy(data, d.data)
	_, groupBytes := group(d.enc)
	if err := completeData(data, d.parity, groupBytes); err != nil {
		return 0, nil, err
	}
	raw := concat(data)
	if d.enc == EncZlib {
		var err error
		if raw, err = inflate(raw, int64(d.limit())); err != nil {
			return 0, nil, err
		}
	}
	return d.typ, raw, nil
}

func (d *Decoder) limit() int {
	if d.Limit == 0 {
		return DefaultLimit
	}
	return d.Limit
}

// partLengths enforces the splitter contract on a complete series:
// every part but the last decodes to the same number of bytes, and
// the runt to at most that. A whole aligned group lost from a part
// would otherwise join into silently corrupted output.
func partLengths(parts [][]byte) error {
	for i, p := range parts[:len(parts)-1] {
		if len(p) != len(parts[0]) {
			return fmt.Errorf("bbqr: part %d decodes to %d bytes, part 0 to %d", i, len(p), len(parts[0]))
		}
	}
	if last := parts[len(parts)-1]; len(last) > len(parts[0]) {
		return fmt.Errorf("bbqr: runt part decodes to %d bytes, more than part 0's %d", len(last), len(parts[0]))
	}
	return nil
}

// encodeBytes applies the encoding selection rules of the standard
// and returns the encoding and the bytes every part is cut from.
func encodeBytes(raw []byte, encoding byte) (byte, []byte, error) {
	switch encoding {
	case EncHex:
		return EncHex, raw, nil
	case 0, EncZlib:
		if cmp := deflate(raw); len(cmp) < len(raw) {
			return EncZlib, cmp, nil
		}
		fallthrough
	case EncBase32:
		return EncBase32, raw, nil
	}
	return 0, nil, fmt.Errorf("bbqr: unknown encoding %q", encoding)
}

// encodeText renders bytes in the encoding's character set: uppercase
// hex, or Base32 without padding.
func encodeText(encoding byte, b []byte) string {
	if encoding == EncHex {
		return hexEncode(b)
	}
	return b32encode(b)
}

// decodePart decodes the body of a single part. Decoding per part (not
// after concatenation) is what forces encoders to align split points.
func decodePart(encoding byte, body string) ([]byte, error) {
	switch encoding {
	case EncHex:
		return hexDecode(body)
	case EncBase32, EncZlib:
		return b32decode(body)
	}
	return nil, fmt.Errorf("bbqr: unknown encoding %q", encoding)
}

// numQRNeeded determines how many QR codes of the given version are
// needed to hold ll encoded characters, and how many characters each
// non-runt part carries. Split points are aligned to splitMod so every
// part decodes to whole bytes, and reserve characters are kept free in
// every part for the parity parts' extra group.
func numQRNeeded(ver, ll, splitMod, reserve int) (count, perEach int) {
	cap := versionChars[ver-1] - HeaderLen - reserve
	cap2 := cap - cap%splitMod
	need := (ll + cap2 - 1) / cap2
	if need == 1 {
		// No alignment concerns for a single part.
		return 1, ll
	}
	// need-1 aligned parts carry (need-1)*cap2 characters and the
	// runt the remainder, at most cap2 itself by the choice of need.
	return need, cap2
}

// findBestVersion picks the smallest number of parts, then the lowest
// QR version, within the option bounds.
func findBestVersion(ll, splitMod int, opts SplitOptions, reserve int) (ver, count, perEach int, err error) {
	minV, maxV := opts.MinVersion, opts.MaxVersion
	if minV == 0 {
		minV = defaultMinVersion
	}
	if maxV == 0 {
		maxV = MaxVersion
	}
	if minV < 1 || maxV > MaxVersion || minV > maxV {
		return 0, 0, 0, fmt.Errorf("bbqr: version range %d..%d out of bounds", minV, maxV)
	}
	minS, maxS := opts.MinSplit, opts.MaxSplit
	if minS == 0 {
		minS = 1
	}
	if maxS == 0 {
		maxS = MaxParts
	}
	if minS < 1 || minS > maxS || maxS > MaxParts {
		return 0, 0, 0, fmt.Errorf("bbqr: split range %d..%d out of bounds", minS, maxS)
	}
	bestCount := -1
	for v := minV; v <= maxV; v++ {
		c, pe := numQRNeeded(v, ll, splitMod, reserve)
		if c < minS || c > maxS {
			continue
		}
		if bestCount == -1 || c < bestCount {
			ver, count, perEach, bestCount = v, c, pe, c
		}
	}
	if bestCount == -1 {
		return 0, 0, 0, fmt.Errorf("bbqr: %d encoded characters do not fit %d..%d parts at versions %d..%d",
			ll, minS, maxS, minV, maxV)
	}
	return ver, count, perEach, nil
}

// base36 formats n as two base 36 digits, 00 through ZZ.
func base36(n int) string {
	digit := func(x int) byte {
		if x < 10 {
			return '0' + byte(x)
		}
		return 'A' + byte(x-10)
	}
	return string([]byte{digit(n / 36), digit(n % 36)})
}

func base36digit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'A' && c <= 'Z'
}

// base36pair parses two base 36 digits.
func base36pair(s string) (int, bool) {
	if !base36digit(s[0]) || !base36digit(s[1]) {
		return 0, false
	}
	val := func(c byte) int {
		if c <= '9' {
			return int(c - '0')
		}
		return int(c-'A') + 10
	}
	return val(s[0])*36 + val(s[1]), true
}

// hexEncode is strings.ToUpper(hex.EncodeToString(raw)) without the
// intermediate allocation.
func hexEncode(raw []byte) string {
	const digits = "0123456789ABCDEF"
	var sb strings.Builder
	sb.Grow(2 * len(raw))
	for _, b := range raw {
		sb.WriteByte(digits[b>>4])
		sb.WriteByte(digits[b&15])
	}
	return sb.String()
}

func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, errors.New("bbqr: odd number of hex digits")
	}
	val := func(c byte) (byte, bool) {
		switch {
		case c >= '0' && c <= '9':
			return c - '0', true
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10, true
		}
		return 0, false
	}
	out := make([]byte, len(s)/2)
	for i := range out {
		hi, ok1 := val(s[2*i])
		lo, ok2 := val(s[2*i+1])
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("bbqr: invalid hex digit in part")
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

// Package qr encodes text as QR Code symbols (ISO/IEC 18004): versions
// 1 to 40, all four error correction levels, numeric, alphanumeric and
// byte modes. It exists so BBQr parts can be rendered without a
// dependency; the error correction runs over the same GF(2^8) as the
// parity parts.
package qr

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Gangleri42/bbqr-go/internal/gf256"
)

// Level is the error correction level.
type Level int

// Error correction levels, by the share of codewords they can restore.
const (
	L Level = iota // about 7%
	M              // about 15%
	Q              // about 25%
	H              // about 30%
)

// String returns the level's letter.
func (l Level) String() string {
	if l < L || l > H {
		return fmt.Sprintf("Level(%d)", int(l))
	}
	return string("LMQH"[l])
}

// MaxVersion is the largest QR version. Version v is 17+4v modules
// square.
const MaxVersion = 40

// ErrTooLong reports text that does not fit the requested version and
// level, or any version at the level when the version was left open.
var ErrTooLong = errors.New("qr: text does not fit")

// Code is an encoded symbol.
type Code struct {
	Version int
	Level   Level
	Mask    int // mask pattern applied to the data region, 0..7
	Size    int // modules per side
	modules []bool
}

// Dark reports whether the module at column x, row y is dark.
func (c *Code) Dark(x, y int) bool {
	return c.modules[y*c.Size+x]
}

// Encode encodes text at the given level and version; version 0 picks
// the smallest that fits. Digits alone use numeric mode, the QR
// alphanumeric set uses alphanumeric mode, anything else byte mode.
// The mask pattern is chosen by the standard's penalty scoring.
func Encode(text string, level Level, version int) (*Code, error) {
	return encode(text, level, version, -1)
}

// encode is Encode with the mask pattern forced when mask is 0..7.
func encode(text string, level Level, version, mask int) (*Code, error) {
	if level < L || level > H {
		return nil, fmt.Errorf("qr: invalid level %d", int(level))
	}
	if version < 0 || version > MaxVersion {
		return nil, fmt.Errorf("qr: invalid version %d", version)
	}
	if len(text) == 0 {
		return nil, errors.New("qr: nothing to encode")
	}
	m := chooseMode(text)
	if version == 0 {
		for version = 1; version <= MaxVersion && bitsNeeded(m, len(text), version) > dataBits(version, level); version++ {
		}
		if version > MaxVersion {
			return nil, fmt.Errorf("%w any version at level %v", ErrTooLong, level)
		}
	} else if bitsNeeded(m, len(text), version) > dataBits(version, level) {
		return nil, fmt.Errorf("%w version %d at level %v", ErrTooLong, version, level)
	}

	s := newSymbol(version)
	s.drawFunctionPatterns(version)
	s.drawCodewords(interleave(encodeData(text, m, version, level), version, level))
	if mask < 0 {
		best := -1
		for k := 0; k < 8; k++ {
			s.applyMask(k)
			s.drawFormat(level, k)
			if p := s.penalty(); best < 0 || p < best {
				best, mask = p, k
			}
			s.applyMask(k) // XOR twice restores the unmasked data
		}
	}
	s.applyMask(mask)
	s.drawFormat(level, mask)
	return &Code{Version: version, Level: level, Mask: mask, Size: s.size, modules: s.dark}, nil
}

// Data encoding.

type mode int

const (
	numeric      mode = 1
	alphanumeric mode = 2
	byteMode     mode = 4
)

const alnumSet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:"

// chooseMode picks the densest single mode that covers every character.
func chooseMode(text string) mode {
	m := numeric
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c >= '0' && c <= '9':
		case strings.IndexByte(alnumSet, c) >= 0:
			m = max(m, alphanumeric)
		default:
			return byteMode
		}
	}
	return m
}

// countBits is the width of the character count indicator, which grows
// with the version class.
func countBits(m mode, version int) int {
	class := 0
	switch {
	case version >= 27:
		class = 2
	case version >= 10:
		class = 1
	}
	switch m {
	case numeric:
		return [3]int{10, 12, 14}[class]
	case alphanumeric:
		return [3]int{9, 11, 13}[class]
	}
	return [3]int{8, 16, 16}[class]
}

// bitsNeeded is the length of the mode indicator, count and data bits
// for n characters.
func bitsNeeded(m mode, n, version int) int {
	data := 8 * n
	switch m {
	case numeric:
		data = 10*(n/3) + [3]int{0, 4, 7}[n%3]
	case alphanumeric:
		data = 11*(n/2) + 6*(n%2)
	}
	return 4 + countBits(m, version) + data
}

// dataBits is the data capacity of a version at a level, in bits.
func dataBits(version int, level Level) int {
	b := ecBlocks[version-1][level]
	return 8 * (b.n1*b.d1 + b.n2*b.d2)
}

// bitWriter packs bits most significant first.
type bitWriter struct {
	buf []byte
	n   int
}

func (w *bitWriter) write(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.n%8 == 0 {
			w.buf = append(w.buf, 0)
		}
		if v>>uint(i)&1 != 0 {
			w.buf[w.n/8] |= 0x80 >> uint(w.n%8)
		}
		w.n++
	}
}

// encodeData returns the data codewords: mode, count, the characters,
// the terminator, and padding up to the version's capacity.
func encodeData(text string, m mode, version int, level Level) []byte {
	capacity := dataBits(version, level)
	var w bitWriter
	w.write(uint32(m), 4)
	w.write(uint32(len(text)), countBits(m, version))
	switch m {
	case numeric:
		for i := 0; i < len(text); i += 3 {
			chunk := text[i:min(i+3, len(text))]
			v := 0
			for j := 0; j < len(chunk); j++ {
				v = v*10 + int(chunk[j]-'0')
			}
			w.write(uint32(v), [4]int{0, 4, 7, 10}[len(chunk)])
		}
	case alphanumeric:
		for i := 0; i+1 < len(text); i += 2 {
			w.write(uint32(alnumIndex(text[i]))*45+uint32(alnumIndex(text[i+1])), 11)
		}
		if len(text)%2 == 1 {
			w.write(uint32(alnumIndex(text[len(text)-1])), 6)
		}
	default:
		for i := 0; i < len(text); i++ {
			w.write(uint32(text[i]), 8)
		}
	}
	w.write(0, min(4, capacity-w.n)) // terminator
	if w.n%8 != 0 {
		w.write(0, 8-w.n%8)
	}
	for i := 0; w.n < capacity; i++ {
		w.write([2]uint32{0xEC, 0x11}[i%2], 8) // pad codewords
	}
	return w.buf
}

func alnumIndex(c byte) int {
	return strings.IndexByte(alnumSet, c)
}

// Error correction.

// rsDivisor returns the Reed-Solomon generator polynomial for n error
// correction codewords as its n lower coefficients, highest power
// first; the leading coefficient is 1 and left implicit.
func rsDivisor(n int) []byte {
	div := make([]byte, n)
	div[n-1] = 1
	root := byte(1)
	for i := 0; i < n; i++ {
		// Multiply by (x - root), then move to the next root.
		for j := range div {
			div[j] = gf256.Mul(div[j], root)
			if j+1 < n {
				div[j] ^= div[j+1]
			}
		}
		root = gf256.Mul(root, 2)
	}
	return div
}

// rsRemainder returns the n error correction codewords of a data block.
func rsRemainder(data []byte, n int) []byte {
	div := rsDivisor(n)
	rem := make([]byte, n)
	for _, b := range data {
		factor := b ^ rem[0]
		copy(rem, rem[1:])
		rem[n-1] = 0
		for i, c := range div {
			rem[i] ^= gf256.Mul(c, factor)
		}
	}
	return rem
}

// interleave splits the data codewords into the version's blocks,
// computes each block's error correction, and interleaves both as the
// standard lays them out in the symbol.
func interleave(data []byte, version int, level Level) []byte {
	b := ecBlocks[version-1][level]
	blocks := make([][]byte, 0, b.n1+b.n2)
	for off, i := 0, 0; i < b.n1+b.n2; i++ {
		n := b.d1
		if i >= b.n1 {
			n = b.d2
		}
		blocks = append(blocks, data[off:off+n])
		off += n
	}
	ecs := make([][]byte, len(blocks))
	for i, blk := range blocks {
		ecs[i] = rsRemainder(blk, b.ec)
	}
	out := make([]byte, 0, len(data)+len(blocks)*b.ec)
	for i := 0; i < max(b.d1, b.d2); i++ {
		for _, blk := range blocks {
			if i < len(blk) {
				out = append(out, blk[i])
			}
		}
	}
	for i := 0; i < b.ec; i++ {
		for _, ec := range ecs {
			out = append(out, ec[i])
		}
	}
	return out
}

// The symbol.

// symbol is a matrix under construction: module colours plus a flag
// for function and reserved modules, which masking leaves alone.
type symbol struct {
	size int
	dark []bool
	fn   []bool
}

func newSymbol(version int) *symbol {
	size := 17 + 4*version
	return &symbol{size: size, dark: make([]bool, size*size), fn: make([]bool, size*size)}
}

// set colours a function module at column x, row y.
func (s *symbol) set(x, y int, dark bool) {
	i := y*s.size + x
	s.dark[i] = dark
	s.fn[i] = true
}

func (s *symbol) drawFunctionPatterns(version int) {
	for i := 0; i < s.size; i++ {
		s.set(6, i, i%2 == 0) // timing patterns
		s.set(i, 6, i%2 == 0)
	}
	s.drawFinder(3, 3)
	s.drawFinder(s.size-4, 3)
	s.drawFinder(3, s.size-4)
	pos := alignPositions[version-1]
	last := len(pos) - 1
	for i, y := range pos {
		for j, x := range pos {
			if (i == 0 && j == 0) || (i == 0 && j == last) || (i == last && j == 0) {
				continue // a finder pattern sits there
			}
			s.drawAlignment(x, y)
		}
	}
	s.drawFormat(L, 0) // reserve the format areas; redrawn once the mask is known
	s.drawVersion(version)
}

// drawFinder draws a finder pattern and its separator around (cx, cy).
func (s *symbol) drawFinder(cx, cy int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			x, y := cx+dx, cy+dy
			if x < 0 || y < 0 || x >= s.size || y >= s.size {
				continue
			}
			dist := max(abs(dx), abs(dy))
			s.set(x, y, dist != 2 && dist != 4)
		}
	}
}

func (s *symbol) drawAlignment(cx, cy int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			s.set(cx+dx, cy+dy, max(abs(dx), abs(dy)) != 1)
		}
	}
}

// formatBits returns the 15 format information bits for a level and
// mask: 5 data bits, a BCH(15,5) remainder, and the standard's mask.
func formatBits(level Level, mask int) uint {
	data := uint([4]int{1, 0, 3, 2}[level])<<3 | uint(mask)
	rem := data
	for i := 0; i < 10; i++ {
		rem = rem<<1 ^ (rem>>9)*0x537
	}
	return (data<<10 | rem) ^ 0x5412
}

func (s *symbol) drawFormat(level Level, mask int) {
	bits := formatBits(level, mask)
	bit := func(i int) bool { return bits>>uint(i)&1 != 0 }
	for i := 0; i <= 5; i++ {
		s.set(8, i, bit(i))
	}
	s.set(8, 7, bit(6))
	s.set(8, 8, bit(7))
	s.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		s.set(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		s.set(s.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		s.set(8, s.size-15+i, bit(i))
	}
	s.set(8, s.size-8, true) // the dark module
}

// versionBits returns the 18 version information bits: the version and
// a BCH(18,6) remainder.
func versionBits(version int) uint {
	rem := uint(version)
	for i := 0; i < 12; i++ {
		rem = rem<<1 ^ (rem>>11)*0x1F25
	}
	return uint(version)<<12 | rem
}

func (s *symbol) drawVersion(version int) {
	if version < 7 {
		return
	}
	bits := versionBits(version)
	for i := 0; i < 18; i++ {
		b := bits>>uint(i)&1 != 0
		a, c := s.size-11+i%3, i/3
		s.set(a, c, b)
		s.set(c, a, b)
	}
}

// drawCodewords lays the codeword bits into the data region, two
// columns at a time from the right, alternating upward and downward and
// skipping the vertical timing pattern. Modules the bits do not reach
// stay light: the remainder bits.
func (s *symbol) drawCodewords(data []byte) {
	i, total := 0, 8*len(data)
	for right := s.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < s.size; vert++ {
			for j := 0; j < 2; j++ {
				x, y := right-j, vert
				if (right+1)&2 == 0 {
					y = s.size - 1 - vert
				}
				if idx := y*s.size + x; !s.fn[idx] && i < total {
					s.dark[idx] = data[i/8]>>uint(7-i%8)&1 != 0
					i++
				}
			}
		}
	}
}

// applyMask XORs mask pattern k into the data region.
func (s *symbol) applyMask(k int) {
	for y := 0; y < s.size; y++ {
		for x := 0; x < s.size; x++ {
			var invert bool
			switch k {
			case 0:
				invert = (x+y)%2 == 0
			case 1:
				invert = y%2 == 0
			case 2:
				invert = x%3 == 0
			case 3:
				invert = (x+y)%3 == 0
			case 4:
				invert = (x/3+y/2)%2 == 0
			case 5:
				invert = x*y%2+x*y%3 == 0
			case 6:
				invert = (x*y%2+x*y%3)%2 == 0
			case 7:
				invert = ((x+y)%2+x*y%3)%2 == 0
			}
			if i := y*s.size + x; invert && !s.fn[i] {
				s.dark[i] = !s.dark[i]
			}
		}
	}
}

// penalty scores the symbol by the standard's four rules: runs of one
// colour, 2x2 blocks, finder-like patterns, and the dark proportion.
func (s *symbol) penalty() int {
	total := 0
	for i := 0; i < s.size; i++ {
		total += linePenalty(func(j int) bool { return s.dark[i*s.size+j] }, s.size)
		total += linePenalty(func(j int) bool { return s.dark[j*s.size+i] }, s.size)
	}
	for y := 0; y+1 < s.size; y++ {
		for x := 0; x+1 < s.size; x++ {
			c := s.dark[y*s.size+x]
			if c == s.dark[y*s.size+x+1] && c == s.dark[(y+1)*s.size+x] && c == s.dark[(y+1)*s.size+x+1] {
				total += 3
			}
		}
	}
	dark := 0
	for _, d := range s.dark {
		if d {
			dark++
		}
	}
	// Each 5% the dark share strays from 50% costs 10.
	modules := s.size * s.size
	total += 10 * (abs(20*dark-10*modules) / modules)
	return total
}

// linePenalty scores one row or column: runs of five or more cost 3
// plus one per extra module, and each finder-like 1:1:3:1:1 pattern
// with four light modules on one side costs 40.
func linePenalty(dark func(int) bool, n int) int {
	total, run := 0, 1
	for i := 1; i <= n; i++ {
		if i < n && dark(i) == dark(i-1) {
			run++
			continue
		}
		if run >= 5 {
			total += run - 2
		}
		run = 1
	}
	finder := [2][11]bool{
		{true, false, true, true, true, false, true, false, false, false, false},
		{false, false, false, false, true, false, true, true, true, false, true},
	}
	for i := 0; i+11 <= n; i++ {
		for _, pat := range finder {
			match := true
			for j := 0; j < 11 && match; j++ {
				match = dark(i+j) == pat[j]
			}
			if match {
				total += 40
			}
		}
	}
	return total
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

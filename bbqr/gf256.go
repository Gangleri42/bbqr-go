package bbqr

import (
	"maps"
	"slices"
)

// GF(2^8) arithmetic for the parity parts. The field is the one QR
// codes use for their own error correction: reduction polynomial
// x^8+x^4+x^3+x^2+1 (0x11D), generator 2, addition is XOR. For each
// byte position, part i of a series holds the value at x=i of a
// polynomial of degree below the data part count, so any that many
// parts determine every other one.

var gfExp, gfLog = gfTables()

// gfTables returns the antilog table, laid out twice so the sum of two
// logs indexes it without reduction, and the log table.
func gfTables() (exp [512]byte, log [256]byte) {
	x := 1
	for i := 0; i < 255; i++ {
		exp[i] = byte(x)
		log[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	copy(exp[255:], exp[:255])
	return exp, log
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

func gfDiv(a, b byte) byte {
	if b == 0 {
		panic("bbqr: division by zero in GF(2^8)")
	}
	if a == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])-int(gfLog[b])+255]
}

// lagrangeBasis returns, for the distinct points xs, the function
// giving the weights w with f(m) = XOR over i of w[i]*f(xs[i]) for any
// polynomial f of degree below len(xs). Everything independent of m is
// computed once here.
func lagrangeBasis(xs []byte) func(m byte) []byte {
	invDen := make([]byte, len(xs))
	for i, xi := range xs {
		den := byte(1)
		for j, xj := range xs {
			if j != i {
				den = gfMul(den, xi^xj)
			}
		}
		invDen[i] = gfDiv(1, den)
	}
	return func(m byte) []byte {
		full := byte(1)
		for _, xj := range xs {
			full = gfMul(full, m^xj)
		}
		w := make([]byte, len(xs))
		for i, xi := range xs {
			if full == 0 {
				// m is one of the points.
				if xi == m {
					w[i] = 1
				}
				continue
			}
			w[i] = gfMul(gfDiv(full, m^xi), invDen[i])
		}
		return w
	}
}

// combine returns the XOR over i of w[i]*blocks[i]. The blocks must
// have equal length.
func combine(blocks [][]byte, w []byte) []byte {
	out := make([]byte, len(blocks[0]))
	for i, blk := range blocks {
		switch w[i] {
		case 0:
		case 1:
			for p, b := range blk {
				out[p] ^= b
			}
		default:
			var mul [256]byte
			for b := range mul {
				mul[b] = gfMul(w[i], byte(b))
			}
			for p, b := range blk {
				out[p] ^= mul[b]
			}
		}
	}
	return out
}

// encodeParity returns blocks k..total-1 of a series whose data blocks
// 0..k-1 are given, all of equal length.
func encodeParity(data [][]byte, total int) [][]byte {
	xs := make([]byte, len(data))
	for i := range xs {
		xs[i] = byte(i)
	}
	weights := lagrangeBasis(xs)
	out := make([][]byte, 0, total-len(data))
	for m := len(data); m < total; m++ {
		out = append(out, combine(data, weights(byte(m))))
	}
	return out
}

// recoverBlocks returns data blocks 0..k-1 from any k blocks of the
// series, keyed by index, all of equal length. Data blocks that are
// present come back as given; the data blocks first and then the
// lowest parity indexes serve as the k points.
func recoverBlocks(have map[int][]byte, k int) [][]byte {
	xs := make([]byte, 0, k)
	ys := make([][]byte, 0, k)
	for _, i := range slices.Sorted(maps.Keys(have))[:k] {
		xs = append(xs, byte(i))
		ys = append(ys, have[i])
	}
	weights := lagrangeBasis(xs)
	out := make([][]byte, k)
	for i := range out {
		if b, ok := have[i]; ok {
			out[i] = b
		} else {
			out[i] = combine(ys, weights(byte(i)))
		}
	}
	return out
}

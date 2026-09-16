// Package gf256 is arithmetic in GF(2^8) with the reduction polynomial
// x^8+x^4+x^3+x^2+1 (0x11D) and generator 2: the field QR codes use for
// their error correction, and BBQr parity parts for theirs. Addition is
// XOR; Mul, Div and Exp cover the rest.
package gf256

var exp, log = tables()

// tables returns the antilog table, laid out twice so the sum of two
// logs indexes it without reduction, and the log table.
func tables() (exp [512]byte, log [256]byte) {
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

// Mul returns the product of a and b.
func Mul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return exp[int(log[a])+int(log[b])]
}

// Div returns a divided by b. It panics when b is zero.
func Div(a, b byte) byte {
	if b == 0 {
		panic("gf256: division by zero")
	}
	if a == 0 {
		return 0
	}
	return exp[int(log[a])-int(log[b])+255]
}

// Exp returns the generator raised to the power i.
func Exp(i int) byte {
	return exp[i%255]
}

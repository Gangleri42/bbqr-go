package gf256

import "testing"

func TestField(t *testing.T) {
	// 2 generates the field: every nonzero byte appears exactly once.
	seen := make([]bool, 256)
	for i := 0; i < 255; i++ {
		x := Exp(i)
		if x == 0 || seen[x] {
			t.Fatalf("exp table repeats or hits zero at %d", i)
		}
		seen[x] = true
	}
	if Exp(255) != 1 || Exp(300) != Exp(45) {
		t.Fatal("exponent does not wrap at 255")
	}
	for a := 1; a < 256; a++ {
		if Mul(byte(a), Div(1, byte(a))) != 1 {
			t.Fatalf("%d times its inverse is not 1", a)
		}
		if Div(Mul(byte(a), 77), 77) != byte(a) {
			t.Fatalf("division does not undo multiplication for %d", a)
		}
		if Mul(byte(a), 0) != 0 || Div(0, byte(a)) != 0 {
			t.Fatalf("zero is not absorbing for %d", a)
		}
	}
	// x^8 = x^4 + x^3 + x^2 + 1, the 0x11D reduction.
	if Mul(0x80, 2) != 0x1D {
		t.Fatalf("0x80 * 2 = %#x, want 0x1d", Mul(0x80, 2))
	}
	defer func() {
		if recover() == nil {
			t.Fatal("division by zero did not panic")
		}
	}()
	Div(1, 0)
}

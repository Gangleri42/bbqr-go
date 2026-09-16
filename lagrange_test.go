package bbqr

import (
	"bytes"
	"math/rand"
	"slices"
	"testing"

	"github.com/Gangleri42/bbqr-go/internal/gf256"
)

func TestLagrange(t *testing.T) {
	// Weights rebuild a known polynomial f(x) = 7x^2 + 3x + 9.
	f := func(x byte) byte { return gf256.Mul(7, gf256.Mul(x, x)) ^ gf256.Mul(3, x) ^ 9 }
	xs := []byte{5, 17, 200}
	var acc byte
	for i, w := range lagrangeBasis(xs)(42) {
		acc ^= gf256.Mul(w, f(xs[i]))
	}
	if acc != f(42) {
		t.Fatalf("interpolated %d, want %d", acc, f(42))
	}
	// Values used in the spec's worked example.
	w := lagrangeBasis([]byte{0, 1, 2})
	if got := w(3); !bytes.Equal(got, []byte{1, 1, 1}) {
		t.Fatalf("weights at 3 = %v", got)
	}
	if got := w(4); !bytes.Equal(got, []byte{15, 8, 6}) {
		t.Fatalf("weights at 4 = %v", got)
	}
	// At one of the points the weights select that point.
	if got := w(1); !bytes.Equal(got, []byte{0, 1, 0}) {
		t.Fatalf("weights at a data point = %v", got)
	}
}

func TestEncodeRecover(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for _, tc := range []struct{ k, total, size int }{{2, 3, 1}, {3, 5, 2}, {7, 12, 100}, {200, 256, 300}} {
		data := make([][]byte, tc.k)
		for i := range data {
			data[i] = make([]byte, tc.size)
			rng.Read(data[i])
		}
		all := append(slices.Clone(data), encodeParity(data, tc.total)...)
		for trial := 0; trial < 5; trial++ {
			have := map[int][]byte{}
			for _, i := range rng.Perm(tc.total)[:tc.k] {
				have[i] = all[i]
			}
			for i, b := range recoverBlocks(have, tc.k) {
				if !bytes.Equal(b, data[i]) {
					t.Fatalf("k=%d total=%d: block %d differs", tc.k, tc.total, i)
				}
			}
		}
	}
}

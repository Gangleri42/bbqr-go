package bbqr

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"os"
	"slices"
	"strings"
	"testing"
)

// The spec's worked example: 5 bytes as 3 data parts of 2 bytes, plus
// 2 parity parts.
var (
	specData    = []byte{1, 2, 3, 4, 5}
	specExample = []string{"B$HB03000102", "B$HB03010304", "B$HB030205", "B$HB0303078600", "B$HB03040919A7"}
)

func TestSpecExample(t *testing.T) {
	got := parityBlocks(specData, 3, 2, 1, 2)
	for i, blk := range got {
		if p := "B$HB030" + string(rune('3'+i)) + hexEncode(blk); p != specExample[3+i] {
			t.Fatalf("parity part %d = %q, want %q", i, p, specExample[3+i])
		}
	}
	for r := 3; r <= 5; r++ {
		for _, sub := range combinations(specExample, r) {
			ft, raw, err := Join(sub)
			if err != nil || ft != TypeBinary || !bytes.Equal(raw, specData) {
				t.Fatalf("Join(%v) = %q, %v, %v", sub, ft, raw, err)
			}
		}
	}
	for _, sub := range [][]string{specExample[:2], specExample[3:], {specExample[1], specExample[4]}} {
		if _, _, err := Join(sub); err == nil || !strings.Contains(err.Error(), "missing") {
			t.Fatalf("Join(%v) = %v, want a missing parts error", sub, err)
		}
	}
	// Two parts of five and a parity part: the Decoder path.
	var d Decoder
	for _, p := range []string{specExample[4], specExample[0], specExample[2]} {
		if err := d.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	if have, need := d.Progress(); !d.Complete() || have != 3 || need != 3 {
		t.Fatalf("progress %d/%d, complete %v", have, need, d.Complete())
	}
	if _, raw, err := d.Result(); err != nil || !bytes.Equal(raw, specData) {
		t.Fatalf("Decoder.Result = %v, %v", raw, err)
	}
}

func combinations(items []string, r int) [][]string {
	if r == 0 {
		return [][]string{nil}
	}
	if len(items) < r {
		return nil
	}
	var out [][]string
	for _, c := range combinations(items[1:], r-1) {
		out = append(out, append([]string{items[0]}, c...))
	}
	return append(out, combinations(items[1:], r)...)
}

func TestParityRejects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []string
		want  string
	}{
		{"bad padding", []string{specExample[0], specExample[1], "B$HB0303078601"}, "bad padding"},
		{"rebuilt length", []string{specExample[1], specExample[2], "B$HB0303010480"}, "rebuilt part 0"},
		{"single QR series has no parity", []string{"B$HB0100AA", "B$HB0101BB"}, "invalid part index"},
		{"index 256", []string{"B$HB0200AA", "B$HB0201BB", "B$HB0274CC"}, "invalid part index"},
		{"parity lengths differ", []string{specExample[0], "B$HB0303078600", "B$HB030409"}, "different lengths"},
		{"data length off", []string{"B$HB030001", specExample[1], specExample[3]}, "parity implies"},
	} {
		_, _, err := Join(tc.parts)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Join = %v, want error containing %q", tc.name, err, tc.want)
		}
	}
	// A damaged parity part that is not needed must not block the join.
	for _, junk := range []string{"B$HB03040919", "B$HB0304ZZZZZZ"} {
		parts := []string{specExample[0], specExample[1], specExample[3], junk}
		_, _, err := Join(parts)
		if junk == "B$HB0304ZZZZZZ" {
			// Undecodable bodies are rejected at parse time, as in every decoder.
			if err == nil {
				t.Fatal("invalid hex in a parity part accepted")
			}
			continue
		}
		if _, raw, err := Join(parts); err != nil || !bytes.Equal(raw, specData) {
			t.Fatalf("surplus truncated parity blocked the join: %v", err)
		}
	}
}

func TestParityRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for _, enc := range []byte{EncHex, EncBase32, EncZlib} {
		for _, size := range []int{100, 2000, 10000, 50000} {
			for _, parity := range []int{1, 2, 5} {
				for _, maxVer := range []int{11, 29, 40} {
					for _, lowEnt := range []bool{true, false} {
						data := make([]byte, size)
						if lowEnt {
							for i := range data {
								data[i] = 'A'
							}
						} else {
							rng.Read(data)
						}
						s, err := Split(data, TypePSBT, SplitOptions{Encoding: enc, Parity: parity, MaxVersion: maxVer})
						if err != nil {
							t.Fatalf("Split(%d bytes, enc %q, parity %d): %v", size, enc, parity, err)
						}
						if s.Version > maxVer {
							t.Fatalf("version %d over %d", s.Version, maxVer)
						}
						k := s.Count
						if k == 1 {
							if len(s.Parts) != 1 {
								t.Fatalf("single QR grew parity: %d parts", len(s.Parts))
							}
							continue
						}
						if len(s.Parts) != k+parity {
							t.Fatalf("%d parts, want %d+%d", len(s.Parts), k, parity)
						}
						chars, _ := group(s.Encoding)
						if len(s.Parts[k]) != len(s.Parts[0])+chars {
							t.Fatalf("parity part is %d chars, data part %d", len(s.Parts[k]), len(s.Parts[0]))
						}
						for _, p := range s.Parts {
							if len(p) > versionChars[s.Version-1] {
								t.Fatalf("part of %d chars exceeds version %d", len(p), s.Version)
							}
							if h, err := ParseHeader(p); err != nil || h.Count != k {
								t.Fatalf("header of %q: %+v, %v", p[:HeaderLen], h, err)
							}
						}
						check := func(parts []string) {
							t.Helper()
							ft, raw, err := Join(parts)
							if err != nil || ft != TypePSBT || !bytes.Equal(raw, data) {
								t.Fatalf("Join of %d parts (enc %q, size %d, parity %d): %v", len(parts), s.Encoding, size, parity, err)
							}
						}
						check(s.Parts[:k])                // classic path
						check(s.Parts[len(s.Parts)-k:])   // parity plus the tail
						shuffled := slices.Clone(s.Parts) // lose up to parity parts, any order
						rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
						check(shuffled[:len(shuffled)-1-rng.Intn(parity)])
						if _, _, err := Join(shuffled[:k-1]); err == nil {
							t.Fatal("one part short accepted")
						}
					}
				}
			}
		}
	}
}

func TestParityLimits(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	data := make([]byte, 20000)
	rng.Read(data)
	s, err := Split(data, TypePSBT, SplitOptions{Encoding: EncBase32, Parity: 200, MaxVersion: 15})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Parts) != s.Count+200 || s.Parts[len(s.Parts)-1][6:8] != base36(s.Count+199) {
		t.Fatalf("%d parts for count %d, last index %s", len(s.Parts), s.Count, s.Parts[len(s.Parts)-1][6:8])
	}
	if _, raw, err := Join(s.Parts[len(s.Parts)-s.Count:]); err != nil || !bytes.Equal(raw, data) {
		t.Fatalf("Join from parity: %v", err)
	}

	big := make([]byte, 60000)
	rng.Read(big)
	if _, err := Split(big, TypePSBT, SplitOptions{Encoding: EncHex, Parity: 10, MaxVersion: 11}); err == nil || !strings.Contains(err.Error(), "exceed") {
		t.Fatalf("too many parts: %v", err)
	}
	for _, bad := range []int{-1, 255} {
		if _, err := Split(data, TypeBinary, SplitOptions{Parity: bad}); err == nil {
			t.Fatalf("parity %d accepted", bad)
		}
	}

	// When everything fits one QR, asking for parity changes nothing:
	// 230 bytes is 460 hex characters, exactly version 11.
	small := make([]byte, 230)
	rng.Read(small)
	for _, enc := range []byte{EncHex, EncBase32} {
		plain, err := Split(small, TypeBinary, SplitOptions{Encoding: enc})
		if err != nil {
			t.Fatal(err)
		}
		withParity, err := Split(small, TypeBinary, SplitOptions{Encoding: enc, Parity: 2})
		if err != nil {
			t.Fatal(err)
		}
		if plain.Version != withParity.Version || !slices.Equal(plain.Parts, withParity.Parts) {
			t.Fatalf("enc %q: parity changed a single QR: version %d vs %d", enc, plain.Version, withParity.Version)
		}
	}
}

func TestDecoderParity(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	data := make([]byte, 5000)
	rng.Read(data)
	s, err := Split(data, TypeBinary, SplitOptions{Encoding: EncBase32, Parity: 3, MaxVersion: 12})
	if err != nil {
		t.Fatal(err)
	}
	k := s.Count
	// Feed parity first, then data, skipping two data parts.
	var d Decoder
	order := append(slices.Clone(s.Parts[k:]), s.Parts[2:k]...)
	for i, p := range order {
		if d.Complete() != (i >= k) {
			t.Fatalf("complete = %v after %d parts, need %d", d.Complete(), i, k)
		}
		if err := d.Add(p); err != nil {
			t.Fatal(err)
		}
		if err := d.Add(p); err != nil {
			t.Fatal("duplicate parity part must be a no-op:", err)
		}
	}
	if have, need := d.Progress(); !d.Complete() || have != k || need != k {
		t.Fatalf("progress %d/%d, complete %v", have, need, d.Complete())
	}
	ft, raw, err := d.Result()
	if err != nil || ft != TypeBinary || !bytes.Equal(raw, data) {
		t.Fatalf("Result: %v", err)
	}
	// The limit counts parity bytes too.
	small := Decoder{Limit: 100}
	if err := small.Add(s.Parts[k]); err == nil {
		t.Fatal("parity part over limit accepted")
	}
	// A corrupt parity part that the rebuild needs fails Result without
	// poisoning the decoder for a later, correct part.
	var bad Decoder
	corrupt := s.Parts[k][:len(s.Parts[k])-8] + "AAAAAAAA"
	for _, p := range append([]string{corrupt}, s.Parts[1:k]...) {
		if err := bad.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := bad.Result(); err == nil {
		t.Fatal("corrupt parity part accepted")
	}
	if err := bad.Add(s.Parts[0]); err != nil {
		t.Fatal(err)
	}
	if _, raw, err := bad.Result(); err != nil || !bytes.Equal(raw, data) {
		t.Fatalf("Result after the missing data part arrived: %v", err)
	}
}

type parityVector struct {
	Name     string `json:"name"`
	DataHex  string `json:"data_hex"`
	FileType string `json:"file_type"`
	Encoding string `json:"encoding"`
	Parity   int    `json:"parity"`
	Opts     struct {
		MinVersion int `json:"min_version"`
		MaxVersion int `json:"max_version"`
		MinSplit   int `json:"min_split"`
		MaxSplit   int `json:"max_split"`
	} `json:"opts"`
	Version int      `json:"version"`
	Parts   []string `json:"parts"`
}

// TestParityVectors pins Split, parity parts included, to the vectors
// the Python reference generated, which the JS tests check as well.
func TestParityVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/parity-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []parityVector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) == 0 {
		t.Fatal("no vectors")
	}
	rng := rand.New(rand.NewSource(17))
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			data, err := hex.DecodeString(v.DataHex)
			if err != nil {
				t.Fatal(err)
			}
			s, err := Split(data, v.FileType[0], SplitOptions{
				Encoding:   v.Encoding[0],
				Parity:     v.Parity,
				MinVersion: v.Opts.MinVersion,
				MaxVersion: v.Opts.MaxVersion,
				MinSplit:   v.Opts.MinSplit,
				MaxSplit:   v.Opts.MaxSplit,
			})
			if err != nil {
				t.Fatal(err)
			}
			if s.Version != v.Version || !slices.Equal(s.Parts, v.Parts) {
				t.Fatalf("Split differs from the reference: version %d vs %d", s.Version, v.Version)
			}
			for range 20 {
				keep := slices.Clone(s.Parts)
				rng.Shuffle(len(keep), func(i, j int) { keep[i], keep[j] = keep[j], keep[i] })
				ft, raw, err := Join(keep[:s.Count])
				if err != nil || ft != v.FileType[0] || !bytes.Equal(raw, data) {
					t.Fatalf("Join of %d shuffled parts: %v", s.Count, err)
				}
			}
		})
	}
}

// FuzzParity splits, drops parts as the fuzzer directs, and demands
// recovery whenever enough parts remain.
func FuzzParity(f *testing.F) {
	f.Add([]byte("seedhammer seedhammer seedhammer"), uint8(2), uint16(0x0005))
	f.Add(bytes.Repeat([]byte{7}, 3000), uint8(5), uint16(0xFFFF))
	f.Fuzz(func(t *testing.T, data []byte, parity uint8, drop uint16) {
		if len(data) == 0 || len(data) > 16*1024 || parity == 0 || parity >= MaxParityIndex {
			t.Skip()
		}
		s, err := Split(data, TypeBinary, SplitOptions{Encoding: EncHex, Parity: int(parity), MaxVersion: 12})
		if err != nil {
			t.Skip() // too many parts for the parity requested
		}
		var keep []string
		for i, p := range s.Parts {
			if drop>>(i%16)&1 == 0 {
				keep = append(keep, p)
			}
		}
		_, raw, err := Join(keep)
		if len(keep) < s.Count {
			if err == nil {
				t.Fatal("too few parts accepted")
			}
			return
		}
		if err != nil || !bytes.Equal(raw, data) {
			t.Fatalf("recovery from %d of %d parts: %v", len(keep), len(s.Parts), err)
		}
	})
}

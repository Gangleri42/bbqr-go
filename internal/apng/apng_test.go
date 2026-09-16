package apng

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"testing"
	"time"
)

func TestEncode(t *testing.T) {
	frames := make([]image.Image, 3)
	for i := range frames {
		img := image.NewGray(image.Rect(0, 0, 8, 8))
		for j := range img.Pix {
			img.Pix[j] = byte(i * 100)
		}
		frames[i] = img
	}
	var buf bytes.Buffer
	if err := Encode(&buf, frames, 250*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// A plain PNG decoder sees the first frame.
	img, err := png.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if g, ok := img.(*image.Gray); !ok || g.Pix[0] != 0 {
		t.Fatalf("first frame decodes to %T %v", img, img.Bounds())
	}
	// The chunk sequence is what the APNG specification prescribes, with
	// consecutive sequence numbers.
	var types []string
	var seqs []uint32
	b := buf.Bytes()
	for p := 8; p+12 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[p:]))
		typ := string(b[p+4 : p+8])
		types = append(types, typ)
		switch typ {
		case "acTL":
			if frames := binary.BigEndian.Uint32(b[p+8:]); frames != 3 {
				t.Fatalf("acTL frames = %d", frames)
			}
		case "fcTL":
			seqs = append(seqs, binary.BigEndian.Uint32(b[p+8:]))
			if d := binary.BigEndian.Uint16(b[p+8+20:]); d != 250 {
				t.Fatalf("fcTL delay = %d", d)
			}
		case "fdAT":
			seqs = append(seqs, binary.BigEndian.Uint32(b[p+8:]))
		}
		p += 12 + n
	}
	want := []string{"IHDR", "acTL", "fcTL", "IDAT", "fcTL", "fdAT", "fcTL", "fdAT", "IEND"}
	if len(types) != len(want) {
		t.Fatalf("chunks %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("chunks %v, want %v", types, want)
		}
	}
	for i, s := range seqs {
		if int(s) != i {
			t.Fatalf("sequence numbers %v", seqs)
		}
	}
	if err := Encode(&buf, []image.Image{frames[0], image.NewGray(image.Rect(0, 0, 9, 9))}, time.Second); err == nil {
		t.Fatal("mismatched frame sizes accepted")
	}
}

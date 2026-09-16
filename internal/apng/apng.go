// Package apng writes animated PNG files from a sequence of equally
// sized frames. Each frame is encoded with image/png, and its IDAT
// data is repackaged into the animation chunks the APNG extension
// defines, so any PNG decoder shows the first frame and an APNG
// decoder plays them all.
package apng

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"time"
)

// Encode writes frames as an APNG looping forever with the given delay
// between frames. All frames must have the same size and colour model.
func Encode(w io.Writer, frames []image.Image, delay time.Duration) error {
	if len(frames) == 0 {
		return errors.New("apng: no frames")
	}
	var ihdr []byte
	idats := make([][]byte, 0, len(frames))
	for i, f := range frames {
		var buf bytes.Buffer
		if err := png.Encode(&buf, f); err != nil {
			return err
		}
		hdr, data, err := split(buf.Bytes())
		if err != nil {
			return err
		}
		if ihdr == nil {
			ihdr = hdr
		} else if !bytes.Equal(ihdr, hdr) {
			return fmt.Errorf("apng: frame %d differs in size or colour model from frame 0", i)
		}
		idats = append(idats, data)
	}

	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	chunk(&out, "IHDR", ihdr)
	actl := make([]byte, 8)
	binary.BigEndian.PutUint32(actl, uint32(len(frames)))
	chunk(&out, "acTL", actl) // num_plays 0: loop forever

	ms := delay.Milliseconds()
	if ms < 0 || ms > 0xFFFF {
		return fmt.Errorf("apng: delay %v out of range", delay)
	}
	seq := uint32(0)
	for i, data := range idats {
		fctl := make([]byte, 26)
		binary.BigEndian.PutUint32(fctl[0:], seq)
		copy(fctl[4:12], ihdr[0:8]) // width and height
		binary.BigEndian.PutUint16(fctl[20:], uint16(ms))
		binary.BigEndian.PutUint16(fctl[22:], 1000)
		chunk(&out, "fcTL", fctl)
		seq++
		if i == 0 {
			chunk(&out, "IDAT", data)
			continue
		}
		fdat := make([]byte, 4+len(data))
		binary.BigEndian.PutUint32(fdat, seq)
		copy(fdat[4:], data)
		chunk(&out, "fdAT", fdat)
		seq++
	}
	chunk(&out, "IEND", nil)
	_, err := w.Write(out.Bytes())
	return err
}

// split returns the IHDR payload and the concatenated IDAT payloads of
// an encoded PNG.
func split(b []byte) (ihdr, idat []byte, err error) {
	if len(b) < 8 {
		return nil, nil, errors.New("apng: short png")
	}
	for p := 8; p+12 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[p:]))
		typ := string(b[p+4 : p+8])
		if p+12+n > len(b) {
			return nil, nil, errors.New("apng: truncated png chunk")
		}
		data := b[p+8 : p+8+n]
		switch typ {
		case "IHDR":
			ihdr = data
		case "IDAT":
			idat = append(idat, data...)
		}
		p += 12 + n
	}
	if ihdr == nil || idat == nil {
		return nil, nil, errors.New("apng: png without IHDR or IDAT")
	}
	return ihdr, idat, nil
}

// chunk writes one PNG chunk: length, type, data, CRC over type and data.
func chunk(w *bytes.Buffer, typ string, data []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(data)))
	w.Write(n[:])
	w.WriteString(typ)
	w.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte(typ))
	crc.Write(data)
	binary.BigEndian.PutUint32(n[:], crc.Sum32())
	w.Write(n[:])
}

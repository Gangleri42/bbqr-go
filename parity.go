package bbqr

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

// Parity parts, BBQr.md "Parity Parts": a sender appends parts whose
// indexes follow the data parts, and any Count parts of the series
// recover the data. The data parts stay a normal series. For the
// arithmetic, every data block is padded with one 0x80 byte and then
// zeros to one symbol group beyond a full block, so a rebuilt block
// always shows where its data ends. Parity blocks have that padded
// length, which is why parity parts are one group longer than data
// parts and why Split reserves the room for them.

// group returns the size of one symbol group in characters and in
// bytes: the unit split points align to so every part decodes to
// whole bytes.
func group(encoding byte) (chars, bytes int) {
	if encoding == EncHex {
		return 2, 1
	}
	return 8, 5
}

// padBlock pads a data block for the parity arithmetic: one 0x80,
// then zeros, up to size bytes. The block must be shorter than size.
func padBlock(block []byte, size int) []byte {
	out := make([]byte, size)
	copy(out, block)
	out[len(block)] = 0x80
	return out
}

// unpadBlock undoes padBlock on a rebuilt block: trailing zeros, then
// exactly one 0x80.
func unpadBlock(block []byte) ([]byte, error) {
	end := len(block)
	for end > 0 && block[end-1] == 0 {
		end--
	}
	if end == 0 || block[end-1] != 0x80 {
		return nil, errors.New("bbqr: rebuilt part has bad padding")
	}
	return block[:end-1], nil
}

// parityBlocks computes parity blocks for stream cut into count data
// blocks of bData bytes, the last possibly shorter, with groupBytes
// the byte size of one symbol group.
func parityBlocks(stream []byte, count, bData, groupBytes, parity int) [][]byte {
	size := bData + groupBytes
	blocks := make([][]byte, 0, count)
	for off := 0; off < len(stream); off += bData {
		blocks = append(blocks, padBlock(stream[off:min(off+bData, len(stream))], size))
	}
	if len(blocks) != count {
		panic("bbqr: data blocks do not match the part count")
	}
	return encodeParity(blocks, count+parity)
}

// completeData fills the missing entries of data from the parity
// blocks, or checks the part lengths of a complete set. It fails when
// fewer parts than needed are held, when a held part has an impossible
// length, or when a rebuilt block does not unpad to a legal length.
// Only the lowest parity indexes that are needed take part; the rest
// are ignored, damaged or not.
func completeData(data [][]byte, parity map[int][]byte, groupBytes int) error {
	var missing []int
	for i, d := range data {
		if d == nil {
			missing = append(missing, i)
		}
	}
	if len(missing) == 0 {
		return partLengths(data)
	}
	held := slices.Sorted(maps.Keys(parity))
	if len(held) < len(missing) {
		return fmt.Errorf("bbqr: missing %d of %d parts with %d parity parts held", len(missing), len(data), len(held))
	}
	held = held[:len(missing)]

	size := len(parity[held[0]])
	for _, i := range held {
		if len(parity[i]) != size {
			return errors.New("bbqr: parity parts decode to different lengths")
		}
	}
	bData := size - groupBytes
	if bData < 1 {
		return errors.New("bbqr: parity parts decode to too few bytes")
	}
	// Every data part is full except the last, which holds 1..bData bytes.
	last := len(data) - 1
	legal := func(i int, b []byte) bool {
		if i < last {
			return len(b) == bData
		}
		return len(b) >= 1 && len(b) <= bData
	}

	points := make(map[int][]byte, len(data))
	for i, d := range data {
		if d == nil {
			continue
		}
		if !legal(i, d) {
			return fmt.Errorf("bbqr: part %d decodes to %d bytes, parity implies %d", i, len(d), bData)
		}
		points[i] = padBlock(d, size)
	}
	for _, i := range held {
		points[i] = parity[i]
	}
	rebuilt := recoverBlocks(points, len(data))
	for _, m := range missing {
		b, err := unpadBlock(rebuilt[m])
		if err != nil {
			return err
		}
		if !legal(m, b) {
			return fmt.Errorf("bbqr: rebuilt part %d has %d bytes, want %d", m, len(b), bData)
		}
		data[m] = b
	}
	return nil
}

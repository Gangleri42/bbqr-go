package qr

import "image"

// Image renders the symbol with scale pixels per module and a quiet
// zone of quiet modules on every side, dark modules black on white.
func (c *Code) Image(scale, quiet int) *image.Gray {
	side := (c.Size + 2*quiet) * scale
	img := image.NewGray(image.Rect(0, 0, side, side))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if !c.Dark(x, y) {
				continue
			}
			px, py := (x+quiet)*scale, (y+quiet)*scale
			for dy := 0; dy < scale; dy++ {
				row := img.Pix[(py+dy)*img.Stride+px:]
				for dx := 0; dx < scale; dx++ {
					row[dx] = 0
				}
			}
		}
	}
	return img
}

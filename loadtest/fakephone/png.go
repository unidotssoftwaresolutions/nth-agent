package fakephone

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
)

// screenshotPNG draws a phone-sized screenshot whose encoded size is close to wantBytes: a flat background, a band per
// screen (so two screens' pictures differ), and enough rows of noise to carry the bytes a real phone's picture of a busy
// screen carries. A real screenshot compresses the way its content does; this one is sized on purpose, so what the path
// carries is what a real phone sends.
func screenshotPNG(width, height, wantBytes int, seed int64) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	background := color.NRGBA{R: 0xf6, G: 0xf4, B: 0xef, A: 0xff}
	band := color.NRGBA{R: uint8(40 + seed*37%180), G: uint8(90 + seed*53%140), B: uint8(120 + seed*71%120), A: 0xff}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			c := background
			if y < height/12 {
				c = band
			}
			img.SetNRGBA(x, y, c)
		}
	}
	// Noise compresses to about its own size: three bytes a pixel, so this many rows carry the bytes asked for.
	rows := wantBytes / (width * 3)
	if rows > height-height/12 {
		rows = height - height/12
	}
	r := rand.New(rand.NewSource(seed))
	start := height / 6
	for y := start; y < start+rows && y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(r.Intn(256)), G: uint8(r.Intn(256)), B: uint8(r.Intn(256)), A: 0xff})
		}
	}
	var out bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&out, img); err != nil {
		panic(err) // an in-memory encode of a well-formed image does not fail
	}
	return out.Bytes()
}

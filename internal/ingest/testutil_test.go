package ingest

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// fakeJPEGForTest returns a tiny valid JPEG for tests that exercise the
// cover-download path without hitting a real image host.
func fakeJPEGForTest(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 10), G: uint8(y * 10), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode fixture jpeg: %v", err)
	}
	return buf.Bytes()
}

package covers

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func fakeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode fixture jpeg: %v", err)
	}
	return buf.Bytes()
}

func TestFetchScalesDownAndSaves(t *testing.T) {
	data := fakeJPEG(t, 1200, 1800)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(data)
	}))
	defer srv.Close()

	dir := t.TempDir()
	relPath, err := Fetch(context.Background(), srv.Client(), dir, 42, srv.URL+"/poster.jpg")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if relPath != filepath.Join("covers", "42.jpg") {
		t.Errorf("relPath = %q, want covers/42.jpg", relPath)
	}

	saved, err := os.Open(filepath.Join(dir, relPath))
	if err != nil {
		t.Fatalf("open saved cover: %v", err)
	}
	defer saved.Close()
	cfg, _, err := image.DecodeConfig(saved)
	if err != nil {
		t.Fatalf("decode saved cover: %v", err)
	}
	if cfg.Width != maxWidth {
		t.Errorf("saved width = %d, want %d (scaled down)", cfg.Width, maxWidth)
	}
	if cfg.Height != 900 { // 1800 * 600/1200
		t.Errorf("saved height = %d, want 900 (aspect preserved)", cfg.Height)
	}
}

func TestFetchNeverUpscales(t *testing.T) {
	data := fakeJPEG(t, 300, 200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	dir := t.TempDir()
	relPath, err := Fetch(context.Background(), srv.Client(), dir, 7, srv.URL+"/small.jpg")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	saved, err := os.Open(filepath.Join(dir, relPath))
	if err != nil {
		t.Fatalf("open saved cover: %v", err)
	}
	defer saved.Close()
	cfg, _, err := image.DecodeConfig(saved)
	if err != nil {
		t.Fatalf("decode saved cover: %v", err)
	}
	if cfg.Width != 300 || cfg.Height != 200 {
		t.Errorf("saved size = %dx%d, want unchanged 300x200", cfg.Width, cfg.Height)
	}
}

func TestFetchUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := Fetch(context.Background(), srv.Client(), t.TempDir(), 1, srv.URL+"/missing.jpg"); err == nil {
		t.Error("want error on upstream 404")
	}
}

// Package covers downloads provider-supplied cover art and saves a
// resized local copy under the data dir, so the app never re-fetches
// the same image and never serves arbitrarily large upstream files.
package covers

import (
	"context"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
)

// maxWidth is the width a saved cover is scaled down to when it's
// wider than this. Covers narrower than this are left at their
// original size (never upscaled); height is never independently
// constrained — it scales proportionally with width.
const maxWidth = 600

// Fetch downloads url, decodes it as JPEG or PNG, scales it down to
// maxWidth if wider, and saves it as JPEG at
// {dataDir}/covers/{itemID}.jpg. It returns the path relative to
// dataDir for storage in media_items.cover_path.
func Fetch(ctx context.Context, client *http.Client, dataDir string, itemID int64, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("covers: build request for %s: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("covers: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("covers: fetch %s: status %s", url, resp.Status)
	}

	src, _, err := image.Decode(io.LimitReader(resp.Body, 20<<20)) // 20MB cap — generous for a cover image
	if err != nil {
		return "", fmt.Errorf("covers: decode %s: %w", url, err)
	}
	scaled := scaleDown(src)

	dir := filepath.Join(dataDir, "covers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("covers: create %s: %w", dir, err)
	}
	relPath := filepath.Join("covers", fmt.Sprintf("%d.jpg", itemID))
	fullPath := filepath.Join(dataDir, relPath)
	tmp := fullPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("covers: create %s: %w", tmp, err)
	}
	defer os.Remove(tmp) // no-op once renamed below; cleans up on any error path
	if err := jpeg.Encode(f, scaled, &jpeg.Options{Quality: 85}); err != nil {
		f.Close()
		return "", fmt.Errorf("covers: encode %s: %w", fullPath, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("covers: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, fullPath); err != nil {
		return "", fmt.Errorf("covers: rename %s to %s: %w", tmp, fullPath, err)
	}
	return relPath, nil
}

// scaleDown returns src unchanged if it's already narrower than
// maxWidth (never upscale); otherwise a bilinear-scaled copy at
// maxWidth wide, preserving aspect ratio.
func scaleDown(src image.Image) image.Image {
	b := src.Bounds()
	width, height := b.Dx(), b.Dy()
	if width <= maxWidth {
		return src
	}
	newHeight := height * maxWidth / width
	dst := image.NewRGBA(image.Rect(0, 0, maxWidth, newHeight))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

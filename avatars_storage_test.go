package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
)

func testGIFDataURI(t *testing.T) string {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, 2, 2), []color.Color{color.Black, color.White})
	img.SetColorIndex(0, 0, 1)
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: []*image.Paletted{img}, Delay: []int{0}}); err != nil {
		t.Fatalf("encoding test gif: %v", err)
	}
	return "data:image/gif;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestSavePfpStoresGIFWithoutExtensionForFreeUser(t *testing.T) {
	oldAvatarBaseDir := avatarBaseDir
	avatarBaseDir = t.TempDir()
	t.Cleanup(func() { avatarBaseDir = oldAvatarBaseDir })

	user := User{"username": "gifuser"}
	if err := savePfp(testGIFDataURI(t), &user); err != nil {
		t.Fatalf("savePfp returned error: %v", err)
	}

	stored := filepath.Join(avatarBaseDir, "gifuser")
	if _, err := os.Stat(stored); err != nil {
		t.Fatalf("extensionless avatar was not stored: %v", err)
	}
	if _, err := os.Stat(stored + ".gif"); !os.IsNotExist(err) {
		t.Fatalf("avatar should not be stored with .gif extension, stat err = %v", err)
	}

	path, contentType, _, err := getAvatarMetadata("gifuser")
	if err != nil {
		t.Fatalf("getAvatarMetadata returned error: %v", err)
	}
	if path != stored {
		t.Errorf("avatar path = %q, want %q", path, stored)
	}
	if contentType != "image/gif" {
		t.Errorf("avatar contentType = %q, want %q", contentType, "image/gif")
	}
}

func TestSaveBannerStoresGIFWithoutExtensionForFreeUser(t *testing.T) {
	oldBannerBaseDir := bannerBaseDir
	bannerBaseDir = t.TempDir()
	t.Cleanup(func() { bannerBaseDir = oldBannerBaseDir })

	user := User{"username": "gifuser"}
	if err := saveBanner(testGIFDataURI(t), &user); err != nil {
		t.Fatalf("saveBanner returned error: %v", err)
	}

	stored := filepath.Join(bannerBaseDir, "gifuser")
	if _, err := os.Stat(stored); err != nil {
		t.Fatalf("extensionless banner was not stored: %v", err)
	}
	if _, err := os.Stat(stored + ".gif"); !os.IsNotExist(err) {
		t.Fatalf("banner should not be stored with .gif extension, stat err = %v", err)
	}

	path, contentType, _, _, err := getBannerPath("gifuser")
	if err != nil {
		t.Fatalf("getBannerPath returned error: %v", err)
	}
	if path != stored {
		t.Errorf("banner path = %q, want %q", path, stored)
	}
	if contentType != "image/gif" {
		t.Errorf("banner contentType = %q, want %q", contentType, "image/gif")
	}
}

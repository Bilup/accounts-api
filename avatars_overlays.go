package main

import (
	"path/filepath"
	"sync"
)

type Overlay struct {
	Name     string `json:"name"`
	Requires string `json:"requires"`
}

var (
	overlayManifest []Overlay
	overlayMu       sync.RWMutex
)

func loadOverlays() {
	overlayManifest = loadJSONOrDefault(filepath.Join(COSMETICS_ASSETS_PATH, "overlays", "-manifest.json"), []Overlay(nil))
}

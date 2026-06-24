package main

import (
	"encoding/json"
	"log"
	"os"
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
	overlaysPath := filepath.Join(COSMETICS_ASSETS_PATH, "overlays", "-manifest.json")
	data, err := os.ReadFile(overlaysPath)
	if err != nil {
		log.Printf("[avatars] no overlay manifest found: %v", err)
		return
	}
	if err := json.Unmarshal(data, &overlayManifest); err != nil {
		log.Printf("[avatars] failed to parse overlay manifest: %v", err)
		return
	}
}

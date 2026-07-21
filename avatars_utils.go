package main

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"sync"
)

// --- LRU cache for transformed images ---

type avatarCacheEntry struct {
	data       []byte
	ct         string
	size       int64
	key        string
	prev, next *avatarCacheEntry
}

type avatarLRUCache struct {
	mu       sync.Mutex
	items    map[string]*avatarCacheEntry
	head     *avatarCacheEntry // most-recent
	tail     *avatarCacheEntry // least-recent
	maxItems int
	maxBytes int64
	curBytes int64
}

var avatarCache = newAvatarLRUCache(500, 100*1024*1024)

var avatarBufPool = sync.Pool{
	New: func() interface{} { return new(bytes.Buffer) },
}

func newAvatarLRUCache(maxItems int, maxBytes int64) *avatarLRUCache {
	c := &avatarLRUCache{
		items:    make(map[string]*avatarCacheEntry),
		maxItems: maxItems,
		maxBytes: maxBytes,
	}
	return c
}

func (c *avatarLRUCache) Get(key string) ([]byte, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		return nil, "", false
	}
	if e != c.head {
		c.removeEntry(e)
		c.pushFront(e)
	}
	return e.data, e.ct, true
}

func (c *avatarLRUCache) Set(key string, data []byte, ct string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	size := int64(len(data))

	// If key already exists, remove old entry first
	if existing, ok := c.items[key]; ok {
		c.curBytes -= existing.size
		c.removeEntry(existing)
	}

	for (len(c.items) >= c.maxItems || c.curBytes+size > c.maxBytes) && len(c.items) > 0 {
		c.evictOldest()
	}

	e := &avatarCacheEntry{data: data, ct: ct, size: size, key: key}
	c.items[key] = e
	c.curBytes += size
	c.pushFront(e)
}

func (c *avatarLRUCache) pushFront(e *avatarCacheEntry) {
	e.prev = nil
	e.next = c.head
	if c.head != nil {
		c.head.prev = e
	}
	c.head = e
	if c.tail == nil {
		c.tail = e
	}
}

func (c *avatarLRUCache) removeEntry(e *avatarCacheEntry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		c.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		c.tail = e.prev
	}
	e.prev = nil
	e.next = nil
}

func (c *avatarLRUCache) evictOldest() {
	if c.tail == nil {
		return
	}
	delete(c.items, c.tail.key)
	c.curBytes -= c.tail.size
	c.removeEntry(c.tail)
}

// loadDefaultAvatar loads a fallback default profile picture.
func loadDefaultAvatar() {
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	pix := img.Pix
	for i := 0; i < len(pix); i += 4 {
		pix[i] = 200
		pix[i+1] = 200
		pix[i+2] = 200
		pix[i+3] = 255
	}
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85})
	defaultAvatarContent = buf.Bytes()
	defaultAvatarEtag = fmt.Sprintf("%x", md5.Sum(defaultAvatarContent))
}

// loadDefaultBanner loads a fallback default banner.
func loadDefaultBanner() {
	img := image.NewRGBA(image.Rect(0, 0, 3, 1))
	var buf bytes.Buffer
	png.Encode(&buf, img)
	defaultBannerContent = buf.Bytes()
}

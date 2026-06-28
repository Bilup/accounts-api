package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"sync"

	"github.com/esimov/colorquant"
	"github.com/logica0419/resigif"
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

// --- Buffer pool ---

var avatarBufPool = sync.Pool{
	New: func() interface{} { return new(bytes.Buffer) },
}

// --- Image processing functions ---

func roundedRectMask(w, h, radius int) *image.Alpha {
	mask := image.NewAlpha(image.Rect(0, 0, w, h))

	for i := range mask.Pix {
		mask.Pix[i] = 255
	}

	if radius <= 0 {
		return mask
	}
	if radius > w/2 {
		radius = w / 2
	}
	if radius > h/2 {
		radius = h / 2
	}

	r2 := radius * radius
	for y := 0; y < radius; y++ {
		dy := y - radius
		for x := 0; x < radius; x++ {
			dx := x - radius
			if dx*dx+dy*dy > r2 {
				mask.Pix[y*mask.Stride+x] = 0             // top-left
				mask.Pix[y*mask.Stride+(w-1-x)] = 0       // top-right
				mask.Pix[(h-1-y)*mask.Stride+x] = 0       // bottom-left
				mask.Pix[(h-1-y)*mask.Stride+(w-1-x)] = 0 // bottom-right
			}
		}
	}

	return mask
}

func roundCorners(imgData []byte, radius int) ([]byte, string, error) {
	img, _, err := image.Decode(bytes.NewReader(imgData))
	if err != nil {
		return imgData, "image/jpeg", err
	}
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	if radius > h/2 {
		radius = h / 2
	}

	result := image.NewRGBA(bounds)
	mask := roundedRectMask(w, h, radius)
	draw.DrawMask(result, bounds, img, bounds.Min, mask, image.Point{}, draw.Over)

	buf := avatarBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer avatarBufPool.Put(buf)
	if err := png.Encode(buf, result); err != nil {
		return imgData, "image/jpeg", err
	}
	out := make([]byte, buf.Len())
	copy(out, buf.Bytes())
	return out, "image/png", nil
}

func applyMaskToPaletted(paletted *image.Paletted, mask *image.Alpha, w, h int) error {
	transIdx := -1
	for i, c := range paletted.Palette {
		if _, _, _, a := c.RGBA(); a == 0 {
			transIdx = i
			break
		}
	}
	if transIdx == -1 {
		if len(paletted.Palette) >= 256 {
			return fmt.Errorf("no room for transparent color in palette")
		}
		paletted.Palette = append(paletted.Palette, color.Transparent)
		transIdx = len(paletted.Palette) - 1
	}

	stride := paletted.Stride
	for y := 0; y < h; y++ {
		maskRow := y * mask.Stride
		palRow := y * stride
		for x := 0; x < w; x++ {
			if mask.Pix[maskRow+x] == 0 {
				paletted.Pix[palRow+x] = uint8(transIdx)
			}
		}
	}
	return nil
}

// roundGIF applies a rounded rectangle mask to all frames of a GIF.
func roundGIF(src *gif.GIF, radius int) (*gif.GIF, error) {
	if len(src.Image) == 0 {
		return nil, fmt.Errorf("no frames in GIF")
	}
	bounds := image.Rect(0, 0, src.Config.Width, src.Config.Height)
	w, h := bounds.Dx(), bounds.Dy()
	if radius > w/2 {
		radius = w / 2
	}
	if radius > h/2 {
		radius = h / 2
	}
	if radius <= 0 {
		return src, nil
	}

	mask := roundedRectMask(w, h, radius)

	dst := &gif.GIF{
		LoopCount: src.LoopCount,
		Delay:     src.Delay,
		Disposal:  make([]byte, len(src.Disposal)),
		Image:     make([]*image.Paletted, len(src.Image)),
		Config:    src.Config,
	}

	var bgColor color.Color
	if src.BackgroundIndex < byte(len(src.Image[0].Palette)) {
		bgColor = src.Image[0].Palette[src.BackgroundIndex]
	} else {
		bgColor = color.Transparent
	}

	compositor := image.NewRGBA(bounds)
	draw.Draw(compositor, bounds, &image.Uniform{bgColor}, image.Point{}, draw.Src)

	var prev *image.RGBA
	frameRect := bounds

	ditherer := colorquant.Dither{
		Filter: [][]float32{
			{0.0, 0.0, 7.0 / 16.0},
			{3.0 / 16.0, 5.0 / 16.0, 1.0 / 16.0},
		},
	}

	for i := range src.Image {
		frame := src.Image[i]
		frameRect = frame.Bounds()

		if src.Disposal[i] == gif.DisposalPrevious {
			prev = image.NewRGBA(bounds)
			draw.Draw(prev, bounds, compositor, image.Point{}, draw.Src)
		}

		draw.Draw(compositor, frameRect, frame, frameRect.Min, draw.Over)

		inputRGBA := image.NewRGBA(bounds)
		draw.Draw(inputRGBA, bounds, compositor, image.Point{}, draw.Src)

		paletted := image.NewPaletted(bounds, palette.WebSafe)
		numColors, withinLimit := countAndBuildPalette(inputRGBA, 255, paletted)
		var outputRGBA *image.RGBA

		if !withinLimit || numColors > 256 {
			outputRGBA = toRGBA(ditherer.Quantize(inputRGBA, paletted, 255, true, false))
		} else {
			outputRGBA = toRGBA(paletted)
		}

		maskPix := mask.Pix
		outPix := outputRGBA.Pix
		outStride := outputRGBA.Stride
		for y := 0; y < h; y++ {
			maskRow := y * mask.Stride
			outRow := y * outStride
			for x := 0; x < w; x++ {
				if maskPix[maskRow+x] == 0 {
					outPix[outRow+x*4+3] = 0
				}
			}
		}

		if err := applyMaskToPaletted(paletted, mask, w, h); err != nil {
			return nil, err
		}

		dst.Image[i] = paletted
		dst.Disposal[i] = gif.DisposalNone

		switch src.Disposal[i] {
		case gif.DisposalBackground:
			draw.Draw(compositor, frameRect, &image.Uniform{bgColor}, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			if prev != nil {
				draw.Draw(compositor, bounds, prev, image.Point{}, draw.Src)
			}
		}
	}
	return dst, nil
}

func countAndBuildPalette(img *image.RGBA, limit int, paletted *image.Paletted) (int, bool) {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	stride := img.Stride

	type rgbKey struct{ r, g, b, a uint8 }
	seen := make(map[rgbKey]uint8, 256)
	var pal color.Palette

	for y := 0; y < h; y++ {
		rowOff := y * stride
		for x := 0; x < w; x++ {
			off := rowOff + x*4
			key := rgbKey{img.Pix[off], img.Pix[off+1], img.Pix[off+2], img.Pix[off+3]}
			if idx, ok := seen[key]; ok {
				paletted.Pix[y*paletted.Stride+x] = idx
			} else {
				if len(seen) > limit {
					return len(seen), false
				}
				c := color.NRGBA{R: key.r, G: key.g, B: key.b, A: key.a}
				pal = append(pal, c)
				idx := uint8(len(pal) - 1)
				seen[key] = idx
				paletted.Pix[y*paletted.Stride+x] = idx
			}
		}
	}

	paletted.Palette = pal
	return len(seen), true
}

func toRGBA(src image.Image) *image.RGBA {
	bounds := src.Bounds()
	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, src, bounds.Min, draw.Src)
	return rgba
}

func resizeGIF(data []byte, width, height int) ([]byte, error) {
	src, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	dstImg, err := resigif.Resize(ctx, src, width, height, resigif.WithAspectRatio(resigif.Ignore))
	if err != nil {
		return nil, err
	}
	buf := avatarBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer avatarBufPool.Put(buf)
	if err := gif.EncodeAll(buf, dstImg); err != nil {
		return nil, err
	}
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	return result, nil
}

func decodeFirstGIFFrame(data []byte) (image.Image, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if len(g.Image) == 0 {
		return nil, fmt.Errorf("no frames in GIF")
	}
	b := image.Rect(0, 0, g.Config.Width, g.Config.Height)
	dst := image.NewRGBA(b)
	bg := &image.Uniform{C: image.Transparent}
	if g.BackgroundIndex < byte(len(g.Image[0].Palette)) {
		bg = &image.Uniform{C: g.Image[0].Palette[g.BackgroundIndex]}
	}
	draw.Draw(dst, b, bg, image.Point{}, draw.Src)
	frame := g.Image[0]
	draw.Draw(dst, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
	return dst, nil
}

func encodePNG(img image.Image) ([]byte, error) {
	buf := avatarBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer avatarBufPool.Put(buf)
	if err := png.Encode(buf, img); err != nil {
		return nil, err
	}
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	return result, nil
}

func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	if quality <= 0 {
		quality = 85
	}
	buf := avatarBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer avatarBufPool.Put(buf)
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	return result, nil
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

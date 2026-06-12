package main

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nfnt/resize"
)

var (
	avatarBaseDir string
	bannerBaseDir string

	defaultAvatarContent []byte
	defaultAvatarEtag    string
	defaultBannerContent []byte

	userTierCache   map[Username]string
	userTierCacheMu sync.RWMutex
)

const defaultAvatarURL = "https://raw.githubusercontent.com/Mistium/Origin-OS/main/Resources/no-pfp.jpeg"

func getUserTierCached(username Username) (string, bool) {
	username = username.ToLower()
	userTierCacheMu.RLock()
	tier, ok := userTierCache[username]
	userTierCacheMu.RUnlock()
	if ok {
		if tier == "" {
			return "", false
		}
		return tier, true
	}
	user, err := getAccountByUsername(Username(username))
	userTierCacheMu.Lock()
	if userTierCache == nil {
		userTierCache = make(map[Username]string)
	}
	if err != nil {
		userTierCache[username] = ""
		userTierCacheMu.Unlock()
		return "", false
	}
	tier = user.GetSubscription().Tier
	userTierCache[username] = tier
	userTierCacheMu.Unlock()
	return tier, true
}

func InvalidateUserTierCache(username Username) {
	userTierCacheMu.Lock()
	delete(userTierCache, username.ToLower())
	userTierCacheMu.Unlock()
}

func loadAvatarConfig() {
	documentPath := os.Getenv("HOME")
	if documentPath == "" {
		documentPath = "/tmp"
	}
	avatarBaseDir = mustEnv("AVATAR_DIR", filepath.Join(documentPath, "Documents", "rotur", "avatars"))
	bannerBaseDir = mustEnv("BANNER_DIR", filepath.Join(documentPath, "Documents", "rotur", "banners"))
}

func init() {
	loadAvatarConfig()

	// Try to fetch the real default image; fall back to generated placeholder
	resp, err := http.Get(defaultAvatarURL)
	if err != nil || resp.StatusCode != 200 {
		log.Printf("[avatars] could not load default avatar from URL, using placeholder")
		loadDefaultAvatar()
	} else {
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			loadDefaultAvatar()
		} else {
			defaultAvatarContent = body
			defaultAvatarEtag = fmt.Sprintf("%x", md5.Sum(body))
		}
	}

	loadDefaultBanner()
	loadOverlays()
}

var (
	overlayCosmeticsCache   map[UserId]string
	overlayCosmeticsCacheMu sync.RWMutex
)

func getActiveOverlayCached(userId UserId) string {
	overlayCosmeticsCacheMu.RLock()
	name, ok := overlayCosmeticsCache[userId]
	overlayCosmeticsCacheMu.RUnlock()
	if ok {
		return name
	}

	uc, err := loadUserCosmetics(userId)
	name = ""
	if err == nil && uc != nil {
		name = uc.ActiveCosmetics[CosmeticTypeOverlay]
	}
	if err == nil {
		overlayCosmeticsCacheMu.Lock()
		if overlayCosmeticsCache == nil {
			overlayCosmeticsCache = make(map[UserId]string)
		}
		overlayCosmeticsCache[userId] = name
		overlayCosmeticsCacheMu.Unlock()
	}
	return name
}

func InvalidateOverlayCosmeticsCache(userId UserId) {
	overlayCosmeticsCacheMu.Lock()
	delete(overlayCosmeticsCache, userId)
	overlayCosmeticsCacheMu.Unlock()
}

func clearOverlayCosmeticsCache() {
	overlayCosmeticsCacheMu.Lock()
	overlayCosmeticsCache = make(map[UserId]string)
	overlayCosmeticsCacheMu.Unlock()
}

func getAvatarMetadata(username Username) (filePath, contentType, etag string, err error) {
	base := username.ToLower()
	for _, ext := range []string{".gif", ".jpg"} {
		fp := filepath.Join(avatarBaseDir, string(base)+ext)
		info, statErr := os.Stat(fp)
		if statErr == nil {
			ct := "image/jpeg"
			if ext == ".gif" {
				ct = "image/gif"
			}
			return fp, ct, fmt.Sprintf("%s-%d", username, info.ModTime().Unix()), nil
		}
	}
	return "", "", "", os.ErrNotExist
}

func deleteAvatars(username Username) {
	base := username.ToLower()
	for _, ext := range []string{".gif", ".jpg"} {
		os.Remove(filepath.Join(avatarBaseDir, string(base)+ext))
	}
}

func shouldConvertToStill(c *gin.Context, tier string, contentType string, metaErr error) bool {
	if metaErr != nil || contentType != "image/gif" {
		return false
	}
	if c.Query("no_animate") != "" {
		return true
	}
	allowAnimated := hasTierOrHigher(tier, "plus")
	return !allowAnimated
}

// --- Avatar handler ---

func avatarHandler(c *gin.Context) {
	usernameStr, _ := strings.CutSuffix(strings.ToLower(c.Param("username")), ".gif")
	if usernameStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Username is required"})
		return
	}
	username := Username(usernameStr)

	if len(usernameStr) == 36 {
		user := UserId(usernameStr).User()
		if user == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "User not found"})
			return
		}
		username = user.GetUsername()
	}

	radiusStr := c.Query("radius")
	sizeStr := c.Query("s")
	clientEtag := c.GetHeader("If-None-Match")

	filePath, contentType, baseEtag, metaErr := getAvatarMetadata(username)
	tier, _ := getUserTierCached(username)

	forceStill := shouldConvertToStill(c, tier, contentType, metaErr)

	var sb strings.Builder
	if metaErr != nil {
		contentType = "image/jpeg"
		sb.WriteString(defaultAvatarEtag)
	} else {
		sb.WriteString(baseEtag)
	}
	if forceStill {
		sb.WriteString("-still")
	}
	if sizeStr != "" {
		sb.WriteString("-s=")
		sb.WriteString(sizeStr)
	}
	if radiusStr != "" {
		sb.WriteString("-r=")
		sb.WriteString(radiusStr)
	}

	cacheKey := sb.String()

	if forceStill {
		contentType = "image/jpeg"
	}

	modifier := sizeStr != "" || radiusStr != "" || forceStill

	if !modifier && metaErr == nil {
		etagQuoted := `"` + baseEtag + `"`
		if clientEtag == etagQuoted {
			c.Status(http.StatusNotModified)
			return
		}
		c.Header("ETag", etagQuoted)
		c.Header("Content-Type", contentType)
		c.Header("Cache-Control", "public, max-age=300, must-revalidate")
		if c.Request.Method == http.MethodHead {
			c.Status(200)
			return
		}
		c.File(filePath)
		return
	}

	etagQuoted := `"` + cacheKey + `"`

	if c.Request.Method == http.MethodHead {
		c.Header("Content-Type", contentType)
		c.Header("Cache-Control", "public, max-age=300, must-revalidate")
		c.Header("ETag", etagQuoted)
		c.Status(200)
		return
	}

	if cached, ct, ok := avatarCache.Get(cacheKey); ok {
		if clientEtag == etagQuoted {
			c.Status(http.StatusNotModified)
			return
		}
		c.Header("ETag", etagQuoted)
		c.Header("Cache-Control", "public, max-age=300, must-revalidate")
		c.Data(http.StatusOK, ct, cached)
		return
	}

	// Load image data once
	var imageData []byte
	if metaErr != nil {
		imageData = defaultAvatarContent
		contentType = "image/jpeg"
	} else {
		var err error
		imageData, err = os.ReadFile(filePath)
		if err != nil {
			imageData = defaultAvatarContent
			contentType = "image/jpeg"
		}
	}

	if contentType == "image/gif" && forceStill {
		if img, err := decodeFirstGIFFrame(imageData); err == nil {
			if encoded, err := encodeJPEG(img, 85); err == nil {
				imageData = encoded
				contentType = "image/jpeg"
			}
		}
	}

	if contentType == "image/gif" {
		if sizeStr != "" {
			if sz, err := strconv.Atoi(sizeStr); err == nil && sz > 0 && sz <= 256 {
				if resized, err := resizeGIF(imageData, sz, sz); err == nil {
					imageData = resized
				}
			}
		}
		if radiusStr != "" {
			radiusInt, err := strconv.Atoi(strings.TrimSuffix(radiusStr, "px"))
			if err == nil && radiusInt > 0 {
				if radiusInt >= 128 {
					radiusInt = 128
				}
				if src, err := gif.DecodeAll(bytes.NewReader(imageData)); err == nil {
					if rounded, err := roundGIF(src, radiusInt); err == nil {
						buf := avatarBufPool.Get().(*bytes.Buffer)
						buf.Reset()
						defer avatarBufPool.Put(buf)
						if err := gif.EncodeAll(buf, rounded); err == nil {
							imageData = make([]byte, buf.Len())
							copy(imageData, buf.Bytes())
						}
					}
				}
			}
		}

		avatarCache.Set(cacheKey, imageData, "image/gif")
		if clientEtag == etagQuoted {
			c.Status(http.StatusNotModified)
			return
		}
		c.Header("Content-Type", "image/gif")
		c.Header("Cache-Control", "public, max-age=300, must-revalidate")
		c.Header("ETag", etagQuoted)
		c.Data(http.StatusOK, "image/gif", imageData)
		return
	}

	img, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Error decoding image"})
		return
	}

	if radiusStr != "" {
		radiusInt, err := strconv.Atoi(strings.TrimSuffix(radiusStr, "px"))
		if err == nil && radiusInt > 0 {
			bounds := img.Bounds()
			w, h := bounds.Dx(), bounds.Dy()
			if radiusInt > h/2 {
				radiusInt = h / 2
			}
			result := image.NewRGBA(bounds)
			draw.Draw(result, bounds, img, bounds.Min, draw.Src)
			mask := roundedRectMask(w, h, radiusInt)
			draw.DrawMask(result, bounds, result, bounds.Min, mask, image.Point{}, draw.Over)
			img = result
		}
	}

	if sizeStr != "" {
		if sz, err := strconv.Atoi(sizeStr); err == nil && sz > 0 && sz <= 256 {
			resized := resize.Resize(uint(sz), 0, img, resize.Lanczos3)
			buf := avatarBufPool.Get().(*bytes.Buffer)
			buf.Reset()
			defer avatarBufPool.Put(buf)
			if contentType == "image/png" {
				png.Encode(buf, resized)
			} else {
				jpeg.Encode(buf, resized, &jpeg.Options{Quality: 85})
			}
			imageData = make([]byte, buf.Len())
			copy(imageData, buf.Bytes())
			contentType = "image/jpeg"
		} else {
			if radiusStr != "" {
				buf := avatarBufPool.Get().(*bytes.Buffer)
				buf.Reset()
				defer avatarBufPool.Put(buf)
				if contentType == "image/png" {
					png.Encode(buf, img)
				} else {
					jpeg.Encode(buf, img, &jpeg.Options{Quality: 85})
				}
				imageData = make([]byte, buf.Len())
				copy(imageData, buf.Bytes())
			}
		}
	} else if radiusStr != "" {
		buf := avatarBufPool.Get().(*bytes.Buffer)
		buf.Reset()
		defer avatarBufPool.Put(buf)
		if contentType == "image/png" {
			png.Encode(buf, img)
		} else {
			jpeg.Encode(buf, img, &jpeg.Options{Quality: 85})
		}
		imageData = make([]byte, buf.Len())
		copy(imageData, buf.Bytes())
	}

	avatarCache.Set(cacheKey, imageData, contentType)
	if clientEtag == etagQuoted {
		c.Status(http.StatusNotModified)
		return
	}
	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "public, max-age=300, must-revalidate")
	c.Header("ETag", etagQuoted)
	c.Data(http.StatusOK, contentType, imageData)
}

func overlayHandler(c *gin.Context) {
	usernameStr, _ := strings.CutSuffix(strings.ToLower(c.Param("username")), ".gif")
	username := Username(usernameStr)

	if len(usernameStr) == 36 {
		user := UserId(usernameStr).User()
		if user == nil {
			sendEmpty(c)
			return
		}
		username = user.GetUsername()
		if username == "" {
			sendEmpty(c)
			return
		}
	}

	if _, exists := getUserTierCached(username); !exists {
		sendEmpty(c)
		return
	}

	userId := username.Id()
	if userId == "" {
		sendEmpty(c)
		return
	}
	overlayName := getActiveOverlayCached(userId)
	if overlayName == "" {
		sendEmpty(c)
		return
	}

	path := filepath.Join(COSMETICS_ASSETS_PATH, "overlays", overlayName+".gif")
	if !fileExists(path) {
		sendEmpty(c)
		return
	}

	c.Header("Cache-Control", "public, max-age=300, must-revalidate")
	c.File(path)
}

func sendEmpty(c *gin.Context) {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "image/png", buf.Bytes())
}

// decodeGIFFrame decodes a single composited frame from a GIF at the given index.
func decodeGIFFrame(g *gif.GIF, frameIdx int) image.Image {
	if frameIdx >= len(g.Image) {
		return nil
	}
	bounds := image.Rect(0, 0, g.Config.Width, g.Config.Height)
	dst := image.NewRGBA(bounds)
	for i := 0; i <= frameIdx; i++ {
		frame := g.Image[i]
		draw.Draw(dst, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
	}
	return dst
}

// savePfp decodes the data-URI image and writes the resized profile picture
// directly to disk. No HTTP round-trip is performed.
func savePfp(dataURI string, user *User) error {
	parts := strings.Split(dataURI, ",")
	if len(parts) != 2 {
		return fmt.Errorf("invalid image format")
	}
	mimeHeader := parts[0]
	estimatedSize := (len(parts[1]) * 3) / 4
	if estimatedSize > 10*1024*1024 {
		return fmt.Errorf("image too large")
	}
	imageData, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("invalid image data: %w", err)
	}
	os.MkdirAll(avatarBaseDir, 0755)
	username := user.GetUsername().ToLower()
	tier := strings.ToLower(user.GetSubscription().Tier)
	benefits := subs_benefits[tier]

	var ext, contentType string
	switch {
	case strings.Contains(mimeHeader, "image/gif"):
		if benefits.Has_Animated_Pfp {
			ext = ".gif"
			contentType = "image/gif"
		} else {
			ext = ".jpg"
			contentType = "image/jpeg"
		}
	default:
		ext = ".jpg"
		contentType = "image/jpeg"
	}

	deleteAvatars(username)

	invalidateAvatarCacheForUser(string(username))

	filePath := filepath.Join(avatarBaseDir, string(username)+ext)

	if contentType == "image/gif" {
		resizedData, err := resizeGIF(imageData, 256, 256)
		if err != nil {
			return fmt.Errorf("error resizing GIF: %w", err)
		}
		if err := os.WriteFile(filePath, resizedData, 0644); err != nil {
			return fmt.Errorf("error saving GIF: %w", err)
		}
	} else {
		img, _, err := image.Decode(bytes.NewReader(imageData))
		if err != nil {
			return fmt.Errorf("error decoding image: %w", err)
		}
		resized := resize.Resize(256, 256, img, resize.Lanczos3)
		out, err := os.Create(filePath)
		if err != nil {
			return fmt.Errorf("error saving image: %w", err)
		}
		defer out.Close()
		if err := jpeg.Encode(out, resized, &jpeg.Options{Quality: 85}); err != nil {
			return fmt.Errorf("error encoding image: %w", err)
		}
	}
	return nil
}

func invalidateAvatarCacheForUser(username string) {
	avatarCache.mu.Lock()
	defer avatarCache.mu.Unlock()
	for key := range avatarCache.items {
		if strings.HasPrefix(key, username) {
			avatarCache.curBytes -= avatarCache.items[key].size
			avatarCache.removeEntry(avatarCache.items[key])
			delete(avatarCache.items, key)
		}
	}
}

func uploadPfpHandler(c *gin.Context) {
	var req struct {
		Image string `json:"image"`
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON data"})
		return
	}
	user := authenticateWithKey(req.Token)
	if user == nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "Invalid token"})
		return
	}
	if req.Image == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing image"})
		return
	}
	if err := savePfp(req.Image, user); err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "invalid image format") ||
			strings.Contains(err.Error(), "image too large") ||
			strings.Contains(err.Error(), "invalid image data") ||
			strings.Contains(err.Error(), "error decoding image") {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  "Success",
		"message": "Profile picture uploaded successfully",
	})
}

// --- Banner handler ---

func getBannerPath(username Username) (string, string, string, time.Time, error) {
	base := username.ToLower()
	for _, ext := range []string{".gif", ".jpg", ".png"} {
		fp := filepath.Join(bannerBaseDir, string(base)+ext)
		fi, err := os.Stat(fp)
		if err == nil {
			ct := "image/jpeg"
			switch ext {
			case ".gif":
				ct = "image/gif"
			case ".png":
				ct = "image/png"
			}
			return fp, ct, fmt.Sprintf("%s-%d", username, fi.ModTime().Unix()), fi.ModTime(), nil
		}
	}
	return "", "", "", time.Time{}, os.ErrNotExist
}

func deleteBanners(username Username) {
	base := username.ToLower()
	for _, ext := range []string{".gif", ".jpg", ".png"} {
		os.Remove(filepath.Join(bannerBaseDir, string(base)+ext))
	}
}

func bannerHandler(c *gin.Context) {
	usernameStr, _ := strings.CutSuffix(strings.ToLower(c.Param("username")), ".gif")
	if usernameStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Username is required"})
		return
	}
	username := Username(usernameStr)

	if len(usernameStr) == 36 {
		user := UserId(usernameStr).User()
		if user == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "User not found"})
			return
		}
		username = user.GetUsername()
	}

	radiusStr := c.Query("radius")
	noAnimate := c.Query("no_animate") != ""
	radiusInt, parseErr := strconv.Atoi(strings.TrimSuffix(radiusStr, "px"))
	needRounding := radiusStr != "" && parseErr == nil && radiusInt > 0

	tier, _ := getUserTierCached(username)
	isPro := hasTierOrHigher(tier, "Pro")

	bannerPath, contentType, etag, modTime, err := getBannerPath(username)
	forceStill := !isPro && err == nil && contentType == "image/gif"
	if noAnimate && contentType == "image/gif" {
		forceStill = true
	}
	if forceStill {
		contentType = "image/jpeg"
		if etag != "" {
			etag = etag + "-still"
		}
	}

	clientEtag := c.GetHeader("If-None-Match")

	var imageData []byte
	if err != nil {
		imageData = defaultBannerContent
		contentType = "image/jpeg"
		needRounding = false
	}

	if !needRounding {
		if etag != "" {
			etagQuoted := `"` + etag + `"`
			if clientEtag == etagQuoted {
				c.Status(http.StatusNotModified)
				return
			}
			c.Header("ETag", etagQuoted)
		}
		c.Header("Content-Type", contentType)
		if !modTime.IsZero() {
			c.Header("Last-Modified", modTime.Format(http.TimeFormat))
		}
		c.Header("Cache-Control", "public, max-age=300, must-revalidate")
		if c.Request.Method == http.MethodHead {
			c.Status(200)
			return
		}

		if forceStill {
			if bannerPath != "" {
				imageData, err = os.ReadFile(bannerPath)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Error reading banner file"})
					return
				}
			}
			img, err := decodeFirstGIFFrame(imageData)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Error decoding GIF"})
				return
			}
			jpegData, err := encodeJPEG(img, 85)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Error encoding JPEG"})
				return
			}
			c.Data(http.StatusOK, "image/jpeg", jpegData)
			return
		}

		if bannerPath != "" {
			c.File(bannerPath)
		} else {
			c.Data(http.StatusOK, contentType, imageData)
		}
		return
	}

	etagQuoted := `"` + etag + `"`
	if clientEtag == etagQuoted {
		c.Status(http.StatusNotModified)
		return
	}

	if c.Request.Method == http.MethodHead {
		c.Header("Content-Type", contentType)
		c.Header("Cache-Control", "public, max-age=300, must-revalidate")
		c.Header("ETag", etagQuoted)
		c.Status(200)
		return
	}

	if bannerPath != "" {
		imageData, err = os.ReadFile(bannerPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error reading banner file"})
			return
		}
	}

	if forceStill {
		img, err := decodeFirstGIFFrame(imageData)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error decoding GIF"})
			return
		}
		imageData, err = encodeJPEG(img, 85)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error encoding JPEG"})
			return
		}
		contentType = "image/jpeg"
	}

	if contentType == "image/gif" {
		src, err := gif.DecodeAll(bytes.NewReader(imageData))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error decoding GIF"})
			return
		}
		rounded, err := roundGIF(src, radiusInt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error rounding GIF"})
			return
		}
		buf := avatarBufPool.Get().(*bytes.Buffer)
		buf.Reset()
		defer avatarBufPool.Put(buf)
		if err := gif.EncodeAll(buf, rounded); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error encoding GIF"})
			return
		}
		c.Header("Content-Type", "image/gif")
		c.Header("Cache-Control", "public, max-age=300, must-revalidate")
		c.Header("ETag", etagQuoted)
		c.Data(http.StatusOK, "image/gif", buf.Bytes())
		return
	}

	rounded, newContentType, err := roundCorners(imageData, radiusInt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error rounding image"})
		return
	}
	c.Header("Cache-Control", "public, max-age=300, must-revalidate")
	c.Header("ETag", etagQuoted)
	c.Data(http.StatusOK, newContentType, rounded)
}

func reloadOverlays(c *gin.Context) {
	if !isAdmin(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Forbidden"})
		return
	}
	loadOverlays()
	c.JSON(http.StatusOK, gin.H{"status": "Success"})
}

// --- Upload banner handler ---

// saveBanner decodes the data-URI image and writes the resized banner
// directly to disk. No HTTP round-trip is performed.
func saveBanner(dataURI string, user *User) error {
	parts := strings.Split(dataURI, ",")
	if len(parts) != 2 {
		return fmt.Errorf("invalid image format")
	}
	mimeHeader := parts[0]
	imageData, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("invalid image data: %w", err)
	}
	if len(imageData) > 10*1024*1024 {
		return fmt.Errorf("image too large")
	}
	img, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return fmt.Errorf("error decoding image: %w", err)
	}
	tier := strings.ToLower(user.GetSubscription().Tier)
	benefits := subs_benefits[tier]

	var ext, contentType string
	switch {
	case strings.Contains(mimeHeader, "image/gif"):
		if benefits.Has_Animated_Banner {
			ext = ".gif"
			contentType = "image/gif"
		} else {
			ext = ".jpg"
			contentType = "image/jpeg"
		}
	case strings.Contains(mimeHeader, "image/png"):
		ext = ".png"
		contentType = "image/png"
	default:
		ext = ".jpg"
		contentType = "image/jpeg"
	}

	username := user.GetUsername().ToLower()
	os.MkdirAll(bannerBaseDir, 0755)
	deleteBanners(username)

	filePath := filepath.Join(bannerBaseDir, string(username)+ext)

	if contentType == "image/gif" {
		resizedData, err := resizeGIF(imageData, 900, 300)
		if err != nil {
			return fmt.Errorf("error resizing GIF: %w", err)
		}
		if err := os.WriteFile(filePath, resizedData, 0644); err != nil {
			return fmt.Errorf("error saving GIF: %w", err)
		}
	} else {
		resized := resize.Resize(900, 300, img, resize.Lanczos3)
		file, err := os.Create(filePath)
		if err != nil {
			return fmt.Errorf("error saving banner: %w", err)
		}
		defer file.Close()
		if err := jpeg.Encode(file, resized, &jpeg.Options{Quality: 85}); err != nil {
			return fmt.Errorf("error encoding banner: %w", err)
		}
	}
	return nil
}

func uploadBannerHandler(c *gin.Context) {
	var req struct {
		Image string `json:"image"`
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON data"})
		return
	}
	user := authenticateWithKey(req.Token)
	if user == nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "Invalid token"})
		return
	}
	if req.Image == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing image"})
		return
	}
	if err := saveBanner(req.Image, user); err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "invalid image format") ||
			strings.Contains(err.Error(), "image too large") ||
			strings.Contains(err.Error(), "invalid image data") ||
			strings.Contains(err.Error(), "error decoding image") {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  "Success",
		"message": "Banner uploaded successfully",
	})
}

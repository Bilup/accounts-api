package main

import (
	"bytes"
	"claw/internal/config"
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

	"claw/internal/imageutil"

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

	userGroupTagCache   map[Username]string
	userGroupTagCacheMu sync.RWMutex
)

func getUserGroupTagCached(username Username) string {
	username = username.ToLower()
	userGroupTagCacheMu.RLock()
	tag, ok := userGroupTagCache[username]
	userGroupTagCacheMu.RUnlock()
	if ok {
		return tag
	}
	tag = ""
	if user, err := getAccountByUsername(username); err == nil {
		tag = groupTagForUser(user)
	}
	userGroupTagCacheMu.Lock()
	if userGroupTagCache == nil {
		userGroupTagCache = make(map[Username]string)
	}
	userGroupTagCache[username] = tag
	userGroupTagCacheMu.Unlock()
	return tag
}

func InvalidateUserGroupTagCache(username Username) {
	deleteUnderLock(&userGroupTagCacheMu, userGroupTagCache, username.ToLower())
}

// groupTagForUser resolves the tag of the group referenced by the user's
// sys.group id, or "" if the user is not in a group.
func groupTagForUser(user User) string {
	gid := user.GetString("sys.group")
	if gid == "" {
		return ""
	}
	groupsDataMutex.RLock()
	defer groupsDataMutex.RUnlock()
	for tag, data := range groupsData {
		if string(data.Group.Id) == gid {
			return tag
		}
	}
	return ""
}

const defaultAvatarURL = "https://raw.githubusercontent.com/Mistium/Origin-OS/main/Resources/no-pfp.jpeg"

const imageCacheControl = "public, max-age=300, must-revalidate"

func deleteUnderLock[K comparable, V any](mu *sync.RWMutex, m map[K]V, key K) {
	mu.Lock()
	delete(m, key)
	mu.Unlock()
}

func avatarURL[T ~string](username T) string {
	return "https://avatars.rotur.dev/" + string(username)
}

func bannerURL[T ~string](username T) string {
	return "https://avatars.rotur.dev/.banners/" + string(username)
}

func getUserTierCached(username Username) (string, bool) {
	username = username.ToLower()
	userTierCacheMu.RLock()
	tier, ok := userTierCache[username]
	userTierCacheMu.RUnlock()
	if ok {
		if tier == "" {
			return "", false
		}
		normalizedTier := normalizeSubscriptionTier(tier)
		if normalizedTier != tier {
			userTierCacheMu.Lock()
			userTierCache[username] = normalizedTier
			userTierCacheMu.Unlock()
		}
		return normalizedTier, true
	}
	// Resolve the tier before taking the write lock: GetSubscription can call
	// InvalidateUserTierCache (via SetSubscription), which locks userTierCacheMu.
	user, err := getAccountByUsername(Username(username))
	tier = ""
	if err == nil {
		tier = user.GetSubscription().Tier
	}
	userTierCacheMu.Lock()
	if userTierCache == nil {
		userTierCache = make(map[Username]string)
	}
	userTierCache[username] = tier
	userTierCacheMu.Unlock()
	if err != nil {
		return "", false
	}
	return tier, true
}

func InvalidateUserTierCache(username Username) {
	deleteUnderLock(&userTierCacheMu, userTierCache, username.ToLower())
}

func loadAvatarConfig() {
	documentPath := os.Getenv("HOME")
	if documentPath == "" {
		documentPath = "/tmp"
	}
	avatarBaseDir = config.MustEnv("AVATAR_DIR", filepath.Join(documentPath, "Documents", "rotur", "avatars"))
	bannerBaseDir = config.MustEnv("BANNER_DIR", filepath.Join(documentPath, "Documents", "rotur", "banners"))
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
	deleteUnderLock(&overlayCosmeticsCacheMu, overlayCosmeticsCache, userId)
}

func clearOverlayCosmeticsCache() {
	overlayCosmeticsCacheMu.Lock()
	overlayCosmeticsCache = make(map[UserId]string)
	overlayCosmeticsCacheMu.Unlock()
}

func imageContentTypeForExt(ext string) string {
	switch ext {
	case ".gif":
		return "image/gif"
	case ".png":
		return "image/png"
	default:
		return "image/jpeg"
	}
}

func getAvatarMetadata(username Username) (filePath, contentType, etag string, err error) {
	base := username.ToLower()
	for _, ext := range []string{"", ".gif", ".jpg", ".png"} {
		fp := filepath.Join(avatarBaseDir, string(base)+ext)
		info, statErr := os.Stat(fp)
		if statErr == nil {
			ct := getStoredImageContentType(fp, imageContentTypeForExt(ext))
			return fp, ct, fmt.Sprintf("%s-%d", base, info.ModTime().UnixNano()), nil
		}
	}
	return "", "", "", os.ErrNotExist
}

func deleteAvatars(username Username) {
	base := username.ToLower()
	for _, ext := range []string{"", ".gif", ".jpg", ".png"} {
		os.Remove(filepath.Join(avatarBaseDir, string(base)+ext))
	}
}

func shouldConvertToStill(c *gin.Context, tier string, contentType string, metaErr error) bool {
	if metaErr != nil || contentType != "image/gif" {
		return false
	}
	if c.Query("no_animate") == "1" {
		return true
	}
	allowAnimated := hasTierOrHigher(tier, "plus")
	return !allowAnimated
}

// --- Avatar handler ---

func notModified(c *gin.Context, clientEtag, etag string) bool {
	if clientEtag == etag {
		c.Header("ETag", etag)
		c.Header("Cache-Control", imageCacheControl)
		c.Status(http.StatusNotModified)
		return true
	}
	return false
}

func avatarHandler(c *gin.Context) {
	usernameStr, _ := strings.CutSuffix(strings.ToLower(c.Param("username")), ".gif")
	if usernameStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Username is required"})
		return
	}
	username := Username(usernameStr)

	if len(usernameStr) == 36 {
		user := getUserById(UserId(usernameStr))
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
	srcIsGif := contentType == "image/gif"

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
		contentType = "image/png"
	}

	modifier := sizeStr != "" || radiusStr != "" || forceStill

	if !modifier && metaErr == nil {
		etagQuoted := `"` + baseEtag + `"`
		if notModified(c, clientEtag, etagQuoted) {
			return
		}
		c.Header("ETag", etagQuoted)
		c.Header("Content-Type", contentType)
		c.Header("Cache-Control", imageCacheControl)
		if c.Request.Method == http.MethodHead {
			c.Status(200)
			return
		}
		c.File(filePath)
		return
	}

	etagQuoted := `"` + cacheKey + `"`

	if c.Request.Method == http.MethodHead {
		if notModified(c, clientEtag, etagQuoted) {
			return
		}
		c.Header("Content-Type", contentType)
		c.Header("Cache-Control", imageCacheControl)
		c.Header("ETag", etagQuoted)
		c.Status(200)
		return
	}

	if cached, ct, ok := avatarCache.Get(cacheKey); ok {
		if notModified(c, clientEtag, etagQuoted) {
			return
		}
		c.Header("ETag", etagQuoted)
		c.Header("Cache-Control", imageCacheControl)
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

	if srcIsGif && forceStill {
		if img, err := imageutil.DecodeFirstGIFFrame(imageData); err == nil {
			if encoded, err := imageutil.EncodePNG(img); err == nil {
				imageData = encoded
				contentType = "image/png"
			}
		}
	}

	if contentType == "image/gif" {
		if sizeStr != "" {
			if sz, err := strconv.Atoi(sizeStr); err == nil && sz > 0 && sz <= 256 {
				if resized, err := imageutil.ResizeGIF(imageData, sz, sz); err == nil {
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
					if rounded, err := imageutil.RoundGIF(src, radiusInt); err == nil {
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
		if notModified(c, clientEtag, etagQuoted) {
			return
		}
		c.Header("Content-Type", "image/gif")
		c.Header("Cache-Control", imageCacheControl)
		c.Header("ETag", etagQuoted)
		c.Data(http.StatusOK, "image/gif", imageData)
		return
	}

	img, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Error decoding image"})
		return
	}

	origW := img.Bounds().Dx()

	resized := false
	if sizeStr != "" {
		if sz, err := strconv.Atoi(sizeStr); err == nil && sz > 0 && sz <= 256 {
			img = resize.Resize(uint(sz), 0, img, resize.Lanczos3)
			resized = true
		}
	}

	rounded := false
	if radiusStr != "" {
		radiusInt, err := strconv.Atoi(strings.TrimSuffix(radiusStr, "px"))
		if err == nil && radiusInt > 0 {
			bounds := img.Bounds()
			w, h := bounds.Dx(), bounds.Dy()
			if resized && origW > 0 {
				radiusInt = radiusInt * w / origW
			}
			if radiusInt > h/2 {
				radiusInt = h / 2
			}
			result := image.NewRGBA(bounds)
			mask := imageutil.RoundedRectMaskAA(w, h, radiusInt)
			draw.DrawMask(result, bounds, img, bounds.Min, mask, image.Point{}, draw.Over)
			img = result
			contentType = "image/png"
			rounded = true
		}
	}

	if resized || rounded {
		buf := avatarBufPool.Get().(*bytes.Buffer)
		buf.Reset()
		defer avatarBufPool.Put(buf)
		if contentType == "image/png" {
			png.Encode(buf, img)
		} else {
			jpeg.Encode(buf, img, &jpeg.Options{Quality: 85})
			contentType = "image/jpeg"
		}
		imageData = make([]byte, buf.Len())
		copy(imageData, buf.Bytes())
	}

	avatarCache.Set(cacheKey, imageData, contentType)
	if notModified(c, clientEtag, etagQuoted) {
		return
	}
	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", imageCacheControl)
	c.Header("ETag", etagQuoted)
	c.Data(http.StatusOK, contentType, imageData)
}

func overlayHandler(c *gin.Context) {
	usernameStr, _ := strings.CutSuffix(strings.ToLower(c.Param("username")), ".gif")
	username := Username(usernameStr)

	if len(usernameStr) == 36 {
		user := getUserById(UserId(usernameStr))
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

	userId := getIdByUsername(username)
	if userId == "" {
		sendEmpty(c)
		return
	}
	overlayName := getActiveOverlayCached(userId)
	if overlayName == "" || strings.Contains(overlayName, "..") || strings.ContainsAny(overlayName, `/\`) {
		sendEmpty(c)
		return
	}

	path := filepath.Join(config.COSMETICS_ASSETS_PATH, "overlays", overlayName+".gif")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		sendEmpty(c)
		return
	}

	etagQuoted := fmt.Sprintf(`"%s-%d"`, overlayName, info.ModTime().UnixNano())
	if notModified(c, c.GetHeader("If-None-Match"), etagQuoted) {
		return
	}
	c.Header("ETag", etagQuoted)
	c.Header("Content-Type", "image/gif")
	c.Header("Cache-Control", imageCacheControl)
	if c.Request.Method == http.MethodHead {
		c.Status(http.StatusOK)
		return
	}
	c.File(path)
}

var emptyPixelPNG = func() []byte {
	var buf bytes.Buffer
	png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	return buf.Bytes()
}()

const emptyPixelEtag = `"empty"`

func sendEmpty(c *gin.Context) {
	if notModified(c, c.GetHeader("If-None-Match"), emptyPixelEtag) {
		return
	}
	c.Header("ETag", emptyPixelEtag)
	c.Header("Cache-Control", imageCacheControl)
	if c.Request.Method == http.MethodHead {
		c.Header("Content-Type", "image/png")
		c.Status(http.StatusOK)
		return
	}
	c.Data(http.StatusOK, "image/png", emptyPixelPNG)
}

const maxImagePixels = 50 * 1000 * 1000

func decodeImageDataURI(dataURI string) (mimeHeader string, data []byte, err error) {
	parts := strings.Split(dataURI, ",")
	if len(parts) != 2 {
		return "", nil, fmt.Errorf("invalid image format")
	}
	imageData, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", nil, fmt.Errorf("invalid image data: %w", err)
	}
	return parts[0], imageData, nil
}

func writeResizedJPEG(imageData []byte, w, h uint, destPath string) error {
	img, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return err
	}
	resized := resize.Resize(w, h, img, resize.Lanczos3)
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()
	return jpeg.Encode(out, resized, &jpeg.Options{Quality: 85})
}

func writeResizedPNG(imageData []byte, w, h uint, destPath string) error {
	img, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return err
	}
	resized := resize.Resize(w, h, img, resize.Lanczos3)
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()
	return png.Encode(out, resized)
}

func checkImageDimensions(imageData []byte) error {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(imageData))
	if err != nil {
		return fmt.Errorf("error decoding image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return fmt.Errorf("image too large")
	}
	return nil
}

// savePfp decodes the data-URI image and writes the resized profile picture
// directly to disk. No HTTP round-trip is performed.
func savePfp(dataURI string, user *User) error {
	mimeHeader, imageData, err := decodeImageDataURI(dataURI)
	if err != nil {
		return err
	}
	if len(imageData) > 10*1024*1024 {
		return fmt.Errorf("image too large")
	}
	if err := checkImageDimensions(imageData); err != nil {
		return err
	}
	username := user.GetUsername().ToLower()
	if username == "" {
		return fmt.Errorf("cannot save avatar: user has no username")
	}
	os.MkdirAll(avatarBaseDir, 0755)

	contentType := "image/jpeg"
	if strings.Contains(mimeHeader, "image/gif") {
		contentType = "image/gif"
	}

	deleteAvatars(username)

	invalidateAvatarCacheForUser(string(username))

	filePath := filepath.Join(avatarBaseDir, string(username))

	if contentType == "image/gif" {
		resizedData, err := imageutil.ResizeGIF(imageData, 256, 256)
		if err != nil {
			return fmt.Errorf("error resizing GIF: %w", err)
		}
		if err := os.WriteFile(filePath, resizedData, 0644); err != nil {
			return fmt.Errorf("error saving GIF: %w", err)
		}
	} else {
		if err := writeResizedJPEG(imageData, 256, 256, filePath); err != nil {
			return fmt.Errorf("error encoding image: %w", err)
		}
	}
	return nil
}

func invalidateAvatarCacheForUser(username string) {
	avatarCache.mu.Lock()
	defer avatarCache.mu.Unlock()
	prefix := username + "-"
	for key := range avatarCache.items {
		if strings.HasPrefix(key, prefix) {
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

func getStoredImageContentType(path, fallback string) string {
	f, err := os.Open(path)
	if err != nil {
		return fallback
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return fallback
	}
	switch http.DetectContentType(buf[:n]) {
	case "image/gif":
		return "image/gif"
	case "image/png":
		return "image/png"
	case "image/jpeg":
		return "image/jpeg"
	default:
		return fallback
	}
}

func getBannerPath(username Username) (string, string, string, time.Time, error) {
	base := username.ToLower()
	for _, ext := range []string{"", ".gif", ".jpg", ".png"} {
		fp := filepath.Join(bannerBaseDir, string(base)+ext)
		fi, err := os.Stat(fp)
		if err == nil {
			ct := getStoredImageContentType(fp, imageContentTypeForExt(ext))
			return fp, ct, fmt.Sprintf("%s-%d", base, fi.ModTime().UnixNano()), fi.ModTime(), nil
		}
	}
	return "", "", "", time.Time{}, os.ErrNotExist
}

func deleteBanners(username Username) {
	base := username.ToLower()
	for _, ext := range []string{"", ".gif", ".jpg", ".png"} {
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
		user := getUserById(UserId(usernameStr))
		if user == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "User not found"})
			return
		}
		username = user.GetUsername()
	}

	radiusStr := c.Query("radius")
	noAnimate := c.Query("no_animate") == "1"
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
			if notModified(c, clientEtag, etagQuoted) {
				return
			}
			c.Header("ETag", etagQuoted)
		}
		c.Header("Content-Type", contentType)
		if !modTime.IsZero() {
			c.Header("Last-Modified", modTime.Format(http.TimeFormat))
		}
		c.Header("Cache-Control", imageCacheControl)
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
			img, err := imageutil.DecodeFirstGIFFrame(imageData)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Error decoding GIF"})
				return
			}
			jpegData, err := imageutil.EncodeJPEG(img, 85)
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
	if notModified(c, clientEtag, etagQuoted) {
		return
	}

	if c.Request.Method == http.MethodHead {
		c.Header("Content-Type", contentType)
		c.Header("Cache-Control", imageCacheControl)
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
		img, err := imageutil.DecodeFirstGIFFrame(imageData)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error decoding GIF"})
			return
		}
		imageData, err = imageutil.EncodeJPEG(img, 85)
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
		rounded, err := imageutil.RoundGIF(src, radiusInt)
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
		c.Header("Cache-Control", imageCacheControl)
		c.Header("ETag", etagQuoted)
		c.Data(http.StatusOK, "image/gif", buf.Bytes())
		return
	}

	rounded, newContentType, err := imageutil.RoundCorners(imageData, radiusInt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error rounding image"})
		return
	}
	c.Header("Cache-Control", imageCacheControl)
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
	mimeHeader, imageData, err := decodeImageDataURI(dataURI)
	if err != nil {
		return err
	}
	if len(imageData) > 10*1024*1024 {
		return fmt.Errorf("image too large")
	}
	if err := checkImageDimensions(imageData); err != nil {
		return err
	}
	contentType := "image/jpeg"
	switch {
	case strings.Contains(mimeHeader, "image/gif"):
		contentType = "image/gif"
	case strings.Contains(mimeHeader, "image/png"):
		contentType = "image/png"
	}

	username := user.GetUsername().ToLower()
	if username == "" {
		return fmt.Errorf("cannot save banner: user has no username")
	}
	os.MkdirAll(bannerBaseDir, 0755)
	deleteBanners(username)

	filePath := filepath.Join(bannerBaseDir, string(username))

	if contentType == "image/gif" {
		resizedData, err := imageutil.ResizeGIF(imageData, 900, 300)
		if err != nil {
			return fmt.Errorf("error resizing GIF: %w", err)
		}
		if err := os.WriteFile(filePath, resizedData, 0644); err != nil {
			return fmt.Errorf("error saving GIF: %w", err)
		}
	} else if contentType == "image/png" {
		if err := writeResizedPNG(imageData, 900, 300, filePath); err != nil {
			return fmt.Errorf("error encoding banner: %w", err)
		}
	} else {
		if err := writeResizedJPEG(imageData, 900, 300, filePath); err != nil {
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

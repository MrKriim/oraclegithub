package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"image"
	_ "image/gif" // decoder registration, even though we reject GIFs — needed so image.Decode doesn't error before your GIF check can run on genuinely mixed uploads
	"image/jpeg"
	_ "image/png" // decoder registration for PNG uploads
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"github.com/gofiber/fiber/v2"
)

// ─────────────────────────────────────────────────────────────────────────────
// IMAGE COMPRESSION SETTINGS
// ─────────────────────────────────────────────────────────────────────────────
// Tuned for slow/metered mobile connections. maxUploadWidth caps resolution
// (most phone screens don't benefit from anything wider than this in-feed),
// and targetMaxBytes forces a real file-size budget by stepping quality
// down until the encoded JPEG actually lands under the target — "smaller"
// isn't good enough on its own, a fixed KB ceiling is what actually
// guarantees fast loads on a weak connection.
const (
	maxUploadWidth  = 1280
	startingQuality = 75
	minQuality      = 35
	qualityStep     = 10
	targetMaxBytes  = 300 * 1024 // 300 KB ceiling per image
)

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "fallback"
	}
	return fmt.Sprintf("%x", b)
}

func HandleImageUpload(c *fiber.Ctx) (string, error) {
	file, err := c.FormFile("image")
	if err != nil {
		return "", nil // No file uploaded is fine
	}

	// 1. Universal Image Validation
	contentType := file.Header.Get("Content-Type")
	// Allow any MIME type starting with "image/" BUT block GIFs
	if !strings.HasPrefix(contentType, "image/") || contentType == "image/gif" {
		return "", fmt.Errorf("invalid file type: please upload a static image (JPG, PNG, WebP, etc.)")
	}
	if file.Size > imgMaxBytes {
		return "", fmt.Errorf("image too large (max 10 MB)")
	}

	// 2. Prepare Directory
	uploadDir := "./uploads/posts"
	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		os.MkdirAll(uploadDir, 0755)
	}

	// 3. Generate Secure, Unique Filename.
	// Output is always re-encoded as .jpg regardless of the original
	// format (png/webp/etc all convert cleanly to JPEG here), since JPEG
	// gives the best size/quality tradeoff for photos at these settings.
	extension := strings.ToLower(filepath.Ext(file.Filename))
	if extension == ".gif" {
		return "", fmt.Errorf("GIFs are not allowed")
	}

	uniqueName := fmt.Sprintf("%d-%s.jpg",
		time.Now().UnixNano(),
		randomHex(4),
	)

	filePath := filepath.Join(uploadDir, uniqueName)

	// 4. Decode directly from the uploaded file into memory — no need to
	// save the original to disk first, we only ever want the compressed
	// version served to users.
	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("open upload: %w", err)
	}
	cfg, _, cfgErr := image.DecodeConfig(src)
	src.Close()
	if cfgErr != nil {
		return "", fmt.Errorf("decode image: %w", cfgErr)
	}
	if imageTooLarge(cfg.Width, cfg.Height) {
		return "", fmt.Errorf("image dimensions too large")
	}

	src, err = file.Open()
	if err != nil {
		return "", fmt.Errorf("open upload: %w", err)
	}
	defer src.Close()

	img, _, err := image.Decode(src)
	if err != nil {
		return "", fmt.Errorf("decode image: %w", err)
	}

	// 5. Downscale — never upscale a smaller image.
	if img.Bounds().Dx() > maxUploadWidth {
		img = imaging.Resize(img, maxUploadWidth, 0, imaging.Lanczos)
	}

	// 6. Encode with a real KB budget: start at startingQuality and step
	// down until the result fits under targetMaxBytes, or we hit
	// minQuality (never go so low the image looks broken).
	encoded, err := encodeUnderBudget(img)
	if err != nil {
		return "", fmt.Errorf("encode image: %w", err)
	}

	// 7. Write the final compressed bytes to disk.
	if err := os.WriteFile(filePath, encoded, 0644); err != nil {
		return "", fmt.Errorf("save image: %w", err)
	}

	return "/uploads/posts/" + uniqueName, nil
}

// encodeUnderBudget JPEG-encodes img, stepping quality down each pass
// until the output is under targetMaxBytes or minQuality is reached.
func encodeUnderBudget(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	quality := startingQuality

	for {
		buf.Reset()
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
		if buf.Len() <= targetMaxBytes || quality <= minQuality {
			break
		}
		quality -= qualityStep
	}

	return buf.Bytes(), nil
}

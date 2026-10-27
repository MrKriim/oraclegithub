package main

import (
	"bytes"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"mime/multipart"

	"github.com/gofiber/fiber/v2"
	"github.com/nfnt/resize"
)

const (
	imgMaxWidth    = 1280
	imgMaxHeight   = 1280
	imgJPEGQuality = 78
	imgMaxBytes    = 10 << 20 // 10 MB raw input guard
	imgMaxSide     = 8192     // reject pathological dimensions before Decode
	imgMaxPixels   = 20_000_000
)

func imageTooLarge(w, h int) bool {
	if w <= 0 || h <= 0 {
		return true
	}
	if w > imgMaxSide || h > imgMaxSide {
		return true
	}
	return int64(w)*int64(h) > imgMaxPixels
}

// CompressUpload resizes (if needed) and re-encodes any uploaded image as JPEG.
// Use instead of c.SaveFile() — call this, then os.WriteFile() the returned bytes.
// Always output as .jpg regardless of original format.
func CompressUpload(fh *multipart.FileHeader) ([]byte, error) {
	if fh.Size > imgMaxBytes {
		return nil, fiber.NewError(fiber.StatusBadRequest, "image too large (max 10 MB)")
	}

	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(f)
	f.Close()
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid or unsupported image format")
	}
	if imageTooLarge(cfg.Width, cfg.Height) {
		return nil, fiber.NewError(fiber.StatusBadRequest, "image dimensions too large")
	}

	f, err = fh.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid or unsupported image format")
	}

	// Downscale only — never upscale small images.
	bounds := img.Bounds()
	w, h := uint(bounds.Dx()), uint(bounds.Dy())
	if w > imgMaxWidth || h > imgMaxHeight {
		img = resize.Thumbnail(imgMaxWidth, imgMaxHeight, img, resize.Lanczos3)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: imgJPEGQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

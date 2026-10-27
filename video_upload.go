package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// videoMaxBytes is the hard cap for post videos — 200MB, enforced here
// (in addition to main.go's BodyLimit) so we can return a clean message
// instead of a dropped connection.
const videoMaxBytes = 500 * 1024 * 1024

// allowedVideoExt maps a SNIFFED (not client-supplied) MIME type to a safe
// extension. Never trust file.Header.Get("Content-Type") or the filename
// extension alone for what gets written to disk in a web-served
// directory — http.DetectContentType reads the actual magic bytes.
var allowedVideoExt = map[string]string{
	"video/mp4":        ".mp4",
	"video/webm":       ".webm",
	"video/quicktime":  ".mov",
	"video/x-matroska": ".mkv",
}

// HandleVideoUpload reads the same "image" field HandleImageUpload uses.
// If the uploaded file isn't a video, it returns ("", nil) so the caller
// falls through to HandleImageUpload unchanged. If it IS a video but
// fails validation (too large, unsupported codec/container), it returns
// a real error so the caller can show it instead of silently dropping it.
func HandleVideoUpload(c *fiber.Ctx) (string, error) {
	file, err := c.FormFile("image")
	if err != nil {
		return "", nil // no file at all — not this function's problem
	}

	src, err := file.Open()
	if err != nil {
		return "", nil
	}
	sniff := make([]byte, 512)
	n, _ := src.Read(sniff)
	src.Close()

	detected := strings.SplitN(http.DetectContentType(sniff[:n]), ";", 2)[0]
	ext, isVideo := allowedVideoExt[detected]
	if !isVideo {
		return "", nil // not a video — let HandleImageUpload try it as an image
	}

	if file.Size <= 0 {
		return "", fmt.Errorf("empty video file")
	}
	if file.Size > videoMaxBytes {
		return "", fmt.Errorf("video too large — max 200MB, got %.1fMB", float64(file.Size)/1024/1024)
	}

	uploadDir := "./uploads/posts" // already served by app.Static("/uploads/posts", ...) in main.go
	if _, statErr := os.Stat(uploadDir); os.IsNotExist(statErr) {
		os.MkdirAll(uploadDir, 0755)
	}

	uniqueName := fmt.Sprintf("%d-%s%s", time.Now().UnixNano(), randomHex(4), ext)
	filePath := filepath.Join(uploadDir, uniqueName)

	if err := c.SaveFile(file, filePath); err != nil {
		return "", fmt.Errorf("save video: %w", err)
	}
	if chmodErr := os.Chmod(filePath, 0644); chmodErr != nil {
		fmt.Printf("warning: could not chmod %s: %v\n", filePath, chmodErr)
	}

	return "/uploads/posts/" + uniqueName, nil
}

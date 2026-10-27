package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
)

const (
	chunkTmpDir   = "./uploads/tmp"
	chunkMaxBytes = 10 * 1024 * 1024 // 10MB per chunk — well under Cloudflare's ~100MB cap and our own smallBodyCap gate in main.go; pure disk-append, cheap on a 4-core box.
)

func initChunkUploadDir() {
	os.MkdirAll(chunkTmpDir, 0755)
}

// sanitizeUploadID restricts the client-supplied upload id to a safe
// charset so it can never be used for path traversal when building the
// temp file path below.
func sanitizeUploadID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) < 8 || len(out) > 80 {
		return ""
	}
	return out
}

// handleVideoChunkUpload receives one chunk of a large video at a time,
// appended to a per-upload temp file — the browser never sends more than
// chunkMaxBytes in a single request, which is what actually gets a
// >100MB video past Cloudflare's per-request body cap: Cloudflare only
// ever sees small chunk requests, never the full file in one shot. On
// the final chunk it validates + finalizes exactly the same way a direct
// upload does (magic-byte sniff on the assembled file, safe rename,
// move into ./uploads/posts) — never trusting a client-supplied filename
// or extension.
func handleVideoChunkUpload(c *fiber.Ctx) error {
	author := getCurrentUser(c)
	if author == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Not logged in"})
	}

	uploadID := sanitizeUploadID(c.Get("X-Upload-Id"))
	if uploadID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid upload id"})
	}
	chunkIndex, err1 := strconv.Atoi(c.Get("X-Chunk-Index"))
	totalChunks, err2 := strconv.Atoi(c.Get("X-Total-Chunks"))
	if err1 != nil || err2 != nil || chunkIndex < 0 || totalChunks <= 0 || chunkIndex >= totalChunks {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid chunk metadata"})
	}
	// Ceiling on chunk count so a malicious client can't force an
	// unbounded final file size via a huge totalChunks value.
	maxChunks := int(videoMaxBytes/chunkMaxBytes) + 2
	if totalChunks > maxChunks {
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": "Video too large"})
	}

	body := c.Body()
	if len(body) == 0 || len(body) > chunkMaxBytes {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid chunk size"})
	}

	os.MkdirAll(chunkTmpDir, 0755)
	tmpPath := filepath.Join(chunkTmpDir, uploadID+".part")

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Storage error"})
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Write failed"})
	}
	f.Close()

	if chunkIndex < totalChunks-1 {
		return c.JSON(fiber.Map{"done": false, "received": chunkIndex + 1})
	}

	// Last chunk — finalize.
	src, err := os.Open(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Finalize failed"})
	}
	sniff := make([]byte, 512)
	n, _ := src.Read(sniff)
	src.Close()

	detected := strings.SplitN(http.DetectContentType(sniff[:n]), ";", 2)[0]
	ext, ok := allowedVideoExt[detected]
	if !ok {
		os.Remove(tmpPath)
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": fmt.Sprintf("Unsupported video type (%s). Allowed: mp4, webm, mov, mkv.", detected),
		})
	}

	if fi, statErr := os.Stat(tmpPath); statErr == nil && fi.Size() > videoMaxBytes {
		os.Remove(tmpPath)
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": "Video too large"})
	}

	uploadDir := "./uploads/posts"
	os.MkdirAll(uploadDir, 0755)
	idSuffix := uploadID
	if len(idSuffix) > 8 {
		idSuffix = idSuffix[:8]
	}
	finalName := fmt.Sprintf("%s-%s%s", randomHex(8), idSuffix, ext)
	finalPath := filepath.Join(uploadDir, finalName)

	if err := os.Rename(tmpPath, finalPath); err != nil {
		// Cross-filesystem fallback — os.Rename fails if tmp and
		// uploads/posts aren't on the same volume/mount.
		if copyErr := chunkCopyFile(tmpPath, finalPath); copyErr != nil {
			os.Remove(tmpPath)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not save video"})
		}
		os.Remove(tmpPath)
	}

	return c.JSON(fiber.Map{"done": true, "url": "/uploads/posts/" + finalName})
}

func chunkCopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
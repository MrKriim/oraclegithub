package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"
)

var (
	emojiMap  map[string]string // lowercase shortcode -> filename, e.g. "c1" -> "c1.png"
	emojiOnce sync.Once
	// Matches a dot followed by one or more letters/digits, anywhere in
	// the text — no word-boundary requirement, so back-to-back codes
	// like ".p5.p5.p5" each match individually. Anything that isn't a
	// recognized code in emojiMap is left untouched by the caller.
	emojiRegex = regexp.MustCompile(`\.([a-zA-Z0-9]+)`)
)

const emojiDir = "./static/emojis"

// loadEmojiMap scans static/emojis/ once at startup and caches
// shortcode -> filename, so adding a new emoji is just "drop a file in
// that folder and restart" — no code changes needed per-emoji.
func loadEmojiMap() {
	emojiOnce.Do(func() {
		emojiMap = make(map[string]string)
		entries, err := os.ReadDir(emojiDir)
		if err != nil {
			log.Printf("⚠️  loadEmojiMap: could not read %s: %v (emoji shortcodes disabled)", emojiDir, err)
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			code := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
			if code == "" {
				continue
			}
			emojiMap[code] = name
		}
		log.Printf("✅ Loaded %d emoji shortcodes from %s", len(emojiMap), emojiDir)
	})
}

// EmojiShortcodeMiddleware rewrites recognized ".aN" tokens into <img>
// tags across EVERY response body — applied once, globally, after the
// real handler has already produced its HTML. This is deliberately a
// response-layer rewrite rather than a write-time one: content stays
// stored in the DB exactly as typed (so edit forms show the original
// ".a1" text back, unmangled), and every current AND future text field
// gets shortcode support for free, with no per-handler wiring.
func EmojiShortcodeMiddleware(c *fiber.Ctx) error {
	if err := c.Next(); err != nil {
		return err
	}

	path := c.Path()
	// Skip anything that isn't rendered text: static/uploaded binaries,
	// downloads, and the SSE stream (which writes incrementally and
	// has no single buffered body to rewrite). Also skip /emojis itself
	// — that page intentionally displays raw ".code" text so people
	// know what to type; rewriting it here would convert the
	// instructional codes into images mid-tag and corrupt the markup.
	if strings.HasPrefix(path, "/uploads") ||
		strings.HasPrefix(path, "/static") ||
		strings.HasPrefix(path, "/downloads") ||
		path == "/feed/stream" ||
		path == "/emojis" {
		return nil
	}

	ct := string(c.Response().Header.ContentType())
	if !strings.HasPrefix(ct, "text/") {
		return nil // application/json, images, etc. — leave untouched
	}

	if len(emojiMap) == 0 {
		return nil
	}

	body := c.Response().Body()
	if !bytes.ContainsRune(body, '.') {
		return nil // fast skip — nothing to even look at
	}

	newBody := emojiRegex.ReplaceAllFunc(body, func(match []byte) []byte {
		code := strings.ToLower(string(match[1:]))
		file, ok := emojiMap[code]
		if !ok {
			return match // not a recognized code — leave it as plain text
		}
		return []byte(`<img src="/static/emojis/` + file + `" alt="` + string(match) + `" class="inline-emoji" style="display:inline-block;height:clamp(20px,1.6em,32px);width:clamp(20px,1.6em,32px);vertical-align:-0.3em;object-fit:contain;">`)
	})

	c.Response().SetBodyRaw(newBody)
	return nil
}
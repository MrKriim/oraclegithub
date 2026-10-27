package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// EmojiGalleryHandler shows every loaded emoji shortcode with a preview
// image and its ".code" so users know what to type. Reads from the
// in-memory emojiMap (populated once at startup by loadEmojiMap) rather
// than the disk, so this stays fast even with a large emoji folder.
func EmojiGalleryHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Redirect("/login")
	}

	codes := make([]string, 0, len(emojiMap))
	for code := range emojiMap {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	var cards strings.Builder
	for _, code := range codes {
		file := emojiMap[code]
		cards.WriteString(fmt.Sprintf(`
			<div style="background:#fff; border:1px solid #eee; border-radius:16px; padding:12px; display:flex; flex-direction:column; align-items:center; gap:8px; box-shadow:0 4px 10px -4px rgba(0,0,0,0.08);">
				<img src="/static/emojis/%s" alt=".%s" style="width:40px; height:40px; object-fit:contain;">
				<code style="background:#f3f4f6; padding:4px 10px; border-radius:8px; font-size:13px; font-weight:700; color:#111;">.%s</code>
			</div>`, file, code, code))
	}

	if len(codes) == 0 {
		cards.WriteString(`<p style="color:#888; font-family:sans-serif;">No emojis loaded yet.</p>`)
	}

	body := fmt.Sprintf(`
		<div style="width:100%%; max-width:900px; margin:0 auto; padding:20px; box-sizing:border-box; font-family:sans-serif;">
			<h2 style="text-align:center; color:#333; font-size:20px; font-weight:800; text-transform:uppercase; letter-spacing:2px; margin-bottom:8px;">Emojis</h2>
			<p style="text-align:center; color:#666; font-size:14px; margin-bottom:24px; line-height:1.6;">
				Type the code shown under an emoji (e.g. <code style="background:#f3f4f6; padding:2px 6px; border-radius:6px;">.%s</code>) anywhere in a post, comment, or message — it'll turn into the emoji automatically once you submit.
			</p>
			<div style="display:grid; grid-template-columns:repeat(auto-fill, minmax(80px, 1fr)); gap:12px;">
				%s
			</div>
		</div>`, firstOrPlaceholder(codes), cards.String())

	return c.Type("html").SendString(Layout("Emojis", body, currentUser))
}

func firstOrPlaceholder(codes []string) string {
	if len(codes) == 0 {
		return "a1"
	}
	return codes[0]
}
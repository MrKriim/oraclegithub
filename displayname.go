package main

import (
	"fmt"
	"html"
	"strings"
	"unicode"
)

const (
	minDisplayNameRunes = 1
	maxDisplayNameRunes = 10
)

// sanitizeDisplayName strips everything that can break layout — newlines,
// control chars, zero-width characters, and Zalgo-style stacked combining
// marks (capped at 2 per base character) — then clamps to the rune limit.
// Returns "" if nothing usable remains, meaning "fall back to username".
func sanitizeDisplayName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	var out []rune
	combiningRun := 0
	for _, r := range raw {
		switch {
		case r == '\n', r == '\r', r == '\t':
			continue
		case unicode.IsControl(r):
			continue
		case r == '\u200b', r == '\u200c', r == '\u200d', r == '\ufeff':
			continue
		case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), unicode.Is(unicode.Mc, r):
			combiningRun++
			if combiningRun > 2 {
				continue
			}
			out = append(out, r)
			continue
		default:
			combiningRun = 0
			out = append(out, r)
		}
	}

	if len(out) > maxDisplayNameRunes {
		out = out[:maxDisplayNameRunes]
	}
	if len(out) < minDisplayNameRunes {
		return ""
	}
	return string(out)
}

var allowedNameColors = map[string]string{
	"":        "",
	"red":     "#ef4444",
	"orange":  "#f97316",
	"amber":   "#f59e0b",
	"emerald": "#10b981",
	"sky":     "#0ea5e9",
	"blue":    "#3b82f6",
	"violet":  "#8b5cf6",
	"pink":    "#ec4899",
	"gold":    "#d4af37",
	"rainbow": "rainbow",
}

var allowedNameFonts = map[string]string{
	"":          "",
	"serif":     "Georgia,'Times New Roman',serif",
	"mono":      "'JetBrains Mono',ui-monospace,monospace",
	"condensed": "'Arial Narrow',sans-serif",
	"boldsans":  "'Arial Black',sans-serif",
}

// allowedNameEffects — fixed set of extra styling toggles. Users can pick
// any combination (stored comma-separated in display_name_effects), each
// maps to a single, safe, pre-approved CSS declaration. No raw CSS from
// the user, ever.
var allowedNameEffects = map[string]string{
	"bold":      "font-weight:900",
	"italic":    "font-style:italic",
	"underline": "text-decoration:underline",
	"uppercase": "text-transform:uppercase",
	"spaced":    "letter-spacing:0.12em",
	"glow":      "text-shadow:0 0 6px currentColor",
}

// orderedEffectKeys keeps checkbox/render order stable and predictable.
var orderedEffectKeys = []string{"bold", "italic", "underline", "uppercase", "spaced", "glow"}

// parseDisplayNameEffects validates a comma-separated form value against
// the allow-list and returns a clean, deduplicated, comma-separated string
// safe to store. Unknown tokens are silently dropped.
func parseDisplayNameEffects(raw string) string {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		key := strings.TrimSpace(part)
		if key == "" || seen[key] {
			continue
		}
		if _, ok := allowedNameEffects[key]; !ok {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return strings.Join(out, ",")
}

// effectsCSS turns a stored comma-separated effects string into CSS
// declarations, ignoring anything not on the allow-list (defensive, in
// case of stale/edited-by-hand data).
func effectsCSS(stored string) []string {
	var parts []string
	for _, key := range strings.Split(stored, ",") {
		key = strings.TrimSpace(key)
		if css, ok := allowedNameEffects[key]; ok && css != "" {
			parts = append(parts, css)
		}
	}
	return parts
}

const rainbowNameCSS = `<style id="rainbow-name-styles">
@keyframes rainbowNameShift{0%{background-position:0% 50%;}100%{background-position:200% 50%;}}
.rainbow-name{background-image:linear-gradient(90deg,#ef4444,#f59e0b,#10b981,#3b82f6,#8b5cf6,#ec4899,#ef4444);background-size:200% auto;-webkit-background-clip:text;background-clip:text;color:transparent;animation:rainbowNameShift 4s linear infinite;}
</style>`

// displayNameCollidesWithUsername reports whether name exactly matches
// (case-insensitively) another real user's username. Blocks a display
// name crafted to impersonate a real account.
func displayNameCollidesWithUsername(name, owner string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	var exists bool
	_ = db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE LOWER(username) = LOWER($1) AND username != $2)`,
		name, owner,
	).Scan(&exists)
	return exists
}

// buildStyledNameHTML renders just the styled name span (no subtitle) —
// shared by RenderDisplayName and the live JS preview markup.
func buildStyledNameHTML(name, color, font, effects string) string {
	escaped := html.EscapeString(name)
	fontFam := allowedNameFonts[font]
	styleParts := effectsCSS(effects)

	if color == "rainbow" {
		if fontFam != "" {
			styleParts = append(styleParts, "font-family:"+fontFam)
		}
		style := ""
		if len(styleParts) > 0 {
			style = fmt.Sprintf(` style="%s"`, strings.Join(styleParts, ";"))
		}
		return rainbowNameCSS + fmt.Sprintf(`<span class="rainbow-name"%s>%s</span>`, style, escaped)
	}

	if hex := allowedNameColors[color]; hex != "" {
		styleParts = append(styleParts, "color:"+hex)
	}
	if fontFam != "" {
		styleParts = append(styleParts, "font-family:"+fontFam)
	}
	if len(styleParts) == 0 {
		return escaped
	}
	return fmt.Sprintf(`<span style="%s">%s</span>`, strings.Join(styleParts, ";"), escaped)
}

// RenderDisplayName renders a user's stylized display name for SHOW ONLY —
// never use this for hrefs, hx-* targets, session/cache keys, or any
// FK/DB lookup; those must always use the raw username.
//
// If no custom display name is set (or it equals the username), this
// returns GetDecoratedUsername(username) UNCHANGED — the original
// rank/admin-based name styling keeps working exactly as before.
//
// If a custom display name IS set, it renders styled on top, with the
// real "@username" printed clearly underneath — bigger and higher-
// contrast than a barely-visible caption, so the real account is always
// easy to identify even when the display name is styled to resemble
// someone else. Combined with displayNameCollidesWithUsername (enforced
// at save time), an exact-match impersonation is blocked outright, and
// any close lookalike still shows the true account right below it.
func RenderDisplayName(username string) string {
	meta := getCachedAuthorMeta(username)

	name := strings.TrimSpace(meta.DisplayName)
	if name == "" || name == username {
		return GetDecoratedUsername(username)
	}

	nameHTML := buildStyledNameHTML(name, meta.DisplayColor, meta.DisplayFont, meta.DisplayEffects)

	return fmt.Sprintf(
		`<a href="/profile/%s" class="hover:no-underline" style="display:inline-flex;flex-direction:column;line-height:1.15;vertical-align:middle;">%s<span style="font-size:8.5px;font-weight:600;color:#9ca3af;letter-spacing:.01em;margin-top:1px;">@%s</span></a>`,
		html.EscapeString(username), nameHTML, html.EscapeString(username),
	)
}

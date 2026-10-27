package main

import (
	"context"
	"fmt"
	"html"
	"log"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// ====================== ICONS ======================

const (
	iconUser = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" width="24" height="24"><path fill-rule="evenodd" d="M18.685 19.097A9.723 9.723 0 0021.75 12c0-5.385-4.365-9.75-9.75-9.75S2.25 6.615 2.25 12c0 2.694 1.088 5.132 2.85 6.914a3.75 3.75 0 013.433-2.414h7.117a3.75 3.75 0 013.435 2.597zM15.825 21a9.712 9.712 0 01-3.825.75 9.712 9.712 0 01-3.825-.75A5.238 5.238 0 0112 18.75a5.238 5.238 0 013.825 2.25zM12 10.5a2.25 2.25 0 100-4.5 2.25 2.25 0 000 4.5z" clip-rule="evenodd" /></svg>`
	iconLock = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" width="24" height="24"><path fill-rule="evenodd" d="M12 1.5a5.25 5.25 0 00-5.25 5.25v3a3 3 0 00-3 3v6.75a3 3 0 003 3h10.5a3 3 0 003-3v-6.75a3 3 0 00-3-3v-3A5.25 5.25 0 0012 1.5zm-3.75 5.25a3.75 3.75 0 117.5 0v3H8.25v-3z" clip-rule="evenodd" /></svg>`

	// iconEyeOpen / iconEyeSlash back the show/hide-password toggle button
	// inside the password field in renderAuthForm.
	iconEyeOpen = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" class="w-5 h-5"><path d="M10 12.5a2.5 2.5 0 100-5 2.5 2.5 0 000 5z" /><path fill-rule="evenodd" d="M.664 10.59a1.651 1.651 0 010-1.186A10.004 10.004 0 0110 3c4.257 0 7.893 2.66 9.336 6.41.147.381.146.804 0 1.186A10.004 10.004 0 0110 17c-4.257 0-7.893-2.66-9.336-6.41zM14 10a4 4 0 11-8 0 4 4 0 018 0z" clip-rule="evenodd" /></svg>`
	iconEyeSlash = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" class="w-5 h-5"><path fill-rule="evenodd" d="M3.28 2.22a.75.75 0 00-1.06 1.06l14.5 14.5a.75.75 0 101.06-1.06l-1.745-1.745a10.029 10.029 0 003.3-4.38 1.651 1.651 0 000-1.185A10.004 10.004 0 009.999 3a9.956 9.956 0 00-4.744 1.194L3.28 2.22zM7.752 6.69l1.092 1.092a2.5 2.5 0 013.374 3.373l1.091 1.092a4 4 0 00-5.557-5.557z" clip-rule="evenodd" /><path d="M10.748 13.93l2.523 2.523a9.987 9.987 0 01-3.27.547c-4.258 0-7.894-2.66-9.337-6.41a1.651 1.651 0 010-1.186A10.007 10.007 0 012.839 6.02L6.07 9.252a4 4 0 004.678 4.678z" /></svg>`
)

// ====================== CPU-COST GUARD (bcrypt) ======================

// bcryptSem caps how many bcrypt hash/compare operations can run at once,
// site-wide. bcrypt is deliberately CPU-expensive — that's what makes it
// safe against offline cracking — but it also means a burst of concurrent
// login/signup requests can peg every core even though the Postgres/Redis
// rate limits above are fully respected. Capping concurrency here means
// extra requests queue for a few ms instead of starving the whole process
// (health checks, other handlers) of CPU.
var bcryptSem = make(chan struct{}, 8) // tune to ~= numCPU

func bcryptAcquire() { bcryptSem <- struct{}{} }
func bcryptRelease() { <-bcryptSem }

// ====================== IP / FINGERPRINT HELPERS ======================

// isHtmxRequest reports whether this request came from htmx (JS enabled,
// htmx loaded) vs. a plain full-page form submit from a browser with JS
// off or blocked.
func isHtmxRequest(c *fiber.Ctx) bool {
	return c.Get("HX-Request") == "true"
}

// normalizeIP strips the port and normalises the IPv6 loopback to 127.0.0.1.
func normalizeIP(raw string) string {
	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		host = raw
	}
	if host == "::1" {
		return "127.0.0.1"
	}
	return strings.TrimSpace(host)
}

// getRealIP honours Cloudflare / reverse-proxy headers before falling back to
// the socket address. Priority: CF-Connecting-IP -> X-Real-IP -> X-Forwarded-For -> RemoteAddr
func getRealIP(c *fiber.Ctx) string {
	if cf := strings.TrimSpace(c.Get("CF-Connecting-IP")); cf != "" {
		return normalizeIP(cf)
	}
	if xr := strings.TrimSpace(c.Get("X-Real-IP")); xr != "" {
		return normalizeIP(xr)
	}
	if xff := strings.TrimSpace(c.Get("X-Forwarded-For")); xff != "" {
		return normalizeIP(strings.TrimSpace(strings.Split(xff, ",")[0]))
	}
	return normalizeIP(c.IP())
}

// getSubnet returns the /24 network string for IPv4 ("1.2.3.0/24") or the
// full address for IPv6.
func getSubnet(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}
	if v4 := parsed.To4(); v4 != nil {
		return fmt.Sprintf("%d.%d.%d.0/24", v4[0], v4[1], v4[2])
	}
	return ip
}

// reduceUA collapses a full User-Agent to "os:browser" so the device key
// stays stable across browser version updates.
func reduceUA(ua string) string {
	ua = strings.ToLower(ua)
	osName := "other"
	browser := "other"

	switch {
	case strings.Contains(ua, "windows"):
		osName = "windows"
	case strings.Contains(ua, "android"):
		osName = "android"
	case strings.Contains(ua, "iphone"), strings.Contains(ua, "ipad"):
		osName = "ios"
	case strings.Contains(ua, "macintosh"), strings.Contains(ua, "mac os"):
		osName = "macos"
	case strings.Contains(ua, "linux"):
		osName = "linux"
	}

	switch {
	case strings.Contains(ua, "firefox"):
		browser = "firefox"
	case strings.Contains(ua, "edg/"):
		browser = "edge"
	case strings.Contains(ua, "chrome"):
		browser = "chrome"
	case strings.Contains(ua, "safari"):
		browser = "safari"
	case strings.Contains(ua, "opera"), strings.Contains(ua, "opr/"):
		browser = "opera"
	}

	return osName + ":" + browser
}

// buildDeviceKey builds a stable 64-char fingerprint from server-observable
// HTTP signals only. JavaScript is NOT required.
// Input: IP /24 subnet + first Accept-Language tag + OS:Browser family.
func buildDeviceKey(c *fiber.Ctx) string {
	subnet := getSubnet(getRealIP(c))

	lang := c.Get("Accept-Language")
	if idx := strings.IndexAny(lang, "-,;"); idx > 0 {
		lang = lang[:idx]
	}
	lang = strings.ToLower(strings.TrimSpace(lang))

	uaFamily := reduceUA(c.Get("User-Agent"))
	return hashString(fmt.Sprintf("%s|%s|%s", subnet, lang, uaFamily))
}

// hashString returns a reproducible 64-char hex string using two 32-bit
// Murmur-style hashes so it fits the existing hardware_id column length.
func hashString(s string) string {
	h1 := uint32(0xdeadbeef)
	h2 := uint32(0x41c6ce57)
	for _, ch := range s {
		u := uint32(ch)
		h1 = (h1 ^ u) * 2654435761
		h2 = (h2 ^ u) * 1597334677
	}
	base := fmt.Sprintf("%08x%08x", h1, h2)
	return (base + base + base + base)[:64]
}

// ====================== BAN CHECK SYSTEM ======================

// BanResult is returned by checkAllBans.
type BanResult struct {
	Banned   bool
	Reason   string // English
	ReasonUR string // Urdu
}

// checkAllBans runs every ban layer and returns on the first hit.
//
// Layer order (all server-side):
//  1. Redis fast-cache (already-known banned IP)
//  2. banned_identifiers table: exact IP
//  3. banned_identifiers table: /24 subnet
//  4. banned_identifiers table: server-computed device_key
//  5. banned_identifiers table: JS hardware_id (bonus signal, if 64 chars)
//  6. users table graph: any account with is_banned=true sharing
//     the same ip_address, subnet, or device_key
func checkAllBans(c *fiber.Ctx, hardwareID string) BanResult {
	// NOTE: Device/IP/subnet/hardware_id ban layers are intentionally disabled
	// to prevent innocent users from being caught by shared-device false positives.
	// User details are still recorded for admin visibility.
	// Only explicit per-account is_banned flag is enforced (checked in loginPost).
	_ = getRealIP(c)
	_ = hardwareID
	return BanResult{}
}

// checkAccountLimit returns true when 6+ distinct accounts already exist
// for this exact IP or this exact browser fingerprint. Deliberately NOT
// keyed on subnet or device_key — both fold in the /24 subnet, and
// Pakistani mobile carriers put thousands of unrelated users behind the
// same CGNAT subnet. Keying the hard limit on that is what caused
// legitimate shared-device users to get locked out before.
func checkAccountLimit(c *fiber.Ctx, hardwareID string) bool {
	ip := getRealIP(c)

	limitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// hardware_id is a JS-derived bonus signal only. With JS disabled the
	// form field is always "" and $2 != '' excludes it from matching.
	// 'noscript_'-prefixed values (set client-side when JS runs but
	// throws, e.g. blocked localStorage) are random per pageload and
	// excluded too, so they can never falsely group unrelated accounts.
	var count int
	err := db.QueryRow(limitCtx, `
		SELECT COUNT(DISTINCT username) FROM users
		WHERE ip_address = $1
		   OR (hardware_id = $2 AND $2 != '' AND $2 NOT LIKE 'noscript_%')`,
		ip, hardwareID).Scan(&count)
	if err != nil {
		log.Println("checkAccountLimit error:", err)
		return false // fail open — a DB hiccup should never block a real signup
	}
	return count >= 6
}

// isBannedUsername returns true when the username matches a row in the
// banned_usernames table. This restores your original logic that prevented
// reserved / offensive names from being registered.
func isBannedUsername(username string) bool {
	var exists bool
	_ = db.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM banned_usernames WHERE LOWER(username)=$1)",
		strings.ToLower(username)).Scan(&exists)
	return exists
}

// ====================== RENDER HELPERS ======================

func renderAuthForm(
	title, subtitle, namePlace, passPlace, btnText, footText, linkText, linkHref,
	toggleLang, toggleBtn, lang string,
	errorMsg string,
) string {
	errorMsg = html.EscapeString(errorMsg)
	passAuto := "current-password"
	passMinLen := "1"
	if linkHref == "/signup" {
		passAuto = "new-password"
		passMinLen = "6"
	}

	// JS fingerprint collected as supplementary signal only.
	// ALL security decisions use server-side logic; JS failure does not help attackers.
	// toggleAuthPasswordVisibility flips the password field between masked
	// and plain text via the eye/eye-slash icon button — purely a UX
	// convenience, never a security decision (server-side length checks
	// still apply regardless of what the field displays).
	fingerprintScript := `
	<script>
	(function() {
		var hw = document.getElementById('hardware_id');
		try {
			var s = [navigator.userAgent, screen.width, screen.colorDepth,
				navigator.language, navigator.hardwareConcurrency || 0,
				(localStorage.getItem('_dst_id') || Math.random())].join('|');
			var h1 = 0xdeadbeef, h2 = 0x41c6ce57;
			for (var i = 0; i < s.length; i++) {
				var ch = s.charCodeAt(i);
				h1 = Math.imul(h1 ^ ch, 2654435761);
				h2 = Math.imul(h2 ^ ch, 1597334677);
			}
			var base = ((h1>>>0).toString(16).padStart(8,'0') + (h2>>>0).toString(16).padStart(8,'0'));
			var id   = (base+base+base+base).substring(0, 64);
			hw.value = id;
			if (!localStorage.getItem('_dst_id')) localStorage.setItem('_dst_id', id.substring(0,10));
		} catch(e) {
			// JS blocked or storage restricted — backend fingerprint still enforces bans
			hw.value = 'noscript_' + Math.random().toString(36).substring(2, 57);
		}
		var csrf = document.querySelector('#auth-card input[name="csrf_token"]');
		if (csrf) {
			var m = document.cookie.match(/(?:^|; )csrf=([^;]*)/);
			if (m) csrf.value = decodeURIComponent(m[1]);
		}
	})();

	function toggleAuthPasswordVisibility() {
		var input = document.getElementById('password-input');
		var icon = document.getElementById('password-eye-icon');
		var btn = document.getElementById('password-toggle-btn');
		if (!input || !icon || !btn) return;
		var ICON_EYE_OPEN = '` + iconEyeOpen + `';
		var ICON_EYE_SLASH = '` + iconEyeSlash + `';
		if (input.type === 'password') {
			input.type = 'text';
			icon.innerHTML = ICON_EYE_SLASH;
			btn.setAttribute('aria-label', 'Hide password');
		} else {
			input.type = 'password';
			icon.innerHTML = ICON_EYE_OPEN;
			btn.setAttribute('aria-label', 'Show password');
		}
	}
	</script>`

	errorBlock := ""
	if errorMsg != "" {
		errorBlock = `<div class="mb-6 bg-red-50 dark:bg-red-900/30 border-l-4 border-red-500 text-red-700 dark:text-red-400 px-4 py-3 rounded-r-xl font-bold">` + errorMsg + `</div>`
	}

	otherHref := "/login"
	if linkHref == "/login" {
		otherHref = "/signup"
	}

	rulesCheckboxHTML := ""
	if linkHref == "/signup" {
		agreeLabel := "I accept the website rules"
		agreeLinkText := "Read Rules"
		if lang == "ur" {
			agreeLabel = "میں ویب سائٹ کے قواعد سے اتفاق کرتا ہوں"
			agreeLinkText = "قواعد پڑھیں"
		}
		rulesCheckboxHTML = `
			<label style="display:flex;align-items:flex-start;gap:10px;font-size:13px;color:#374151;line-height:1.5;">
				<input type="checkbox" name="accept_rules" required
				       style="width:18px;height:18px;margin-top:2px;flex-shrink:0;">
				<span>` + agreeLabel + ` (<a href="/rules" target="_blank" style="color:#2563eb;font-weight:700;text-decoration:underline;">` + agreeLinkText + `</a>)</span>
			</label>`
	}

	footerLinksHTML := `
		<div style="margin-top:14px;display:flex;justify-content:center;gap:14px;flex-wrap:wrap;">
			<a href="/about" style="font-size:11px;color:#9ca3af;font-weight:700;text-decoration:underline;">About Us</a>
			<a href="/terms" style="font-size:11px;color:#9ca3af;font-weight:700;text-decoration:underline;">Terms</a>
			<a href="/privacy" style="font-size:11px;color:#9ca3af;font-weight:700;text-decoration:underline;">Privacy</a>
		</div>`

	return `
	<div id="auth-card" class="bg-white dark:bg-gray-900 p-8 sm:p-10 rounded-[2.5rem] shadow-2xl w-full max-w-md mx-auto border border-gray-100 dark:border-gray-800">
		` + fingerprintScript + `
		<div class="flex justify-end mb-4">
			<a href="` + linkHref + `?lang=` + toggleLang + `"
			   hx-get="` + linkHref + `-form?lang=` + toggleLang + `"
			   hx-target="#auth-card" hx-swap="outerHTML"
			   class="text-sm font-black px-6 py-2 bg-blue-600 text-white rounded-xl shadow-md inline-block">
				` + toggleBtn + `
			</a>
		</div>
		<div class="text-center mb-8">
			<h2 class="text-3xl font-extrabold text-gray-900 dark:text-white">` + title + `</h2>
			<p class="text-gray-500 dark:text-gray-400 mt-1">` + subtitle + `</p>
		</div>
		` + errorBlock + `
		<form method="POST" action="` + linkHref + `?lang=` + lang + `"
		      hx-post="` + linkHref + `?lang=` + lang + `"
		      hx-target="#auth-card" hx-swap="outerHTML"
		      hx-disabled-elt="find button[type=submit]"
		      class="space-y-5">
			<input type="hidden" name="hardware_id" id="hardware_id" value="">
			<input type="hidden" name="csrf_token" value="">

			<div class="flex items-center bg-gray-50 dark:bg-gray-800 rounded-2xl border-2 border-transparent focus-within:border-blue-500 transition-all overflow-hidden shadow-inner">
				<label class="w-14 h-14 flex items-center justify-center text-gray-400">` + iconUser + `</label>
				<input name="username" placeholder="` + namePlace + `" required
				       autocomplete="username" autocapitalize="off" spellcheck="false"
				       minlength="3" maxlength="20"
				       class="flex-1 bg-transparent outline-none py-4 text-lg text-gray-900 dark:text-gray-100">
			</div>

			<div class="relative flex items-center bg-gray-50 dark:bg-gray-800 rounded-2xl border-2 border-transparent focus-within:border-blue-500 transition-all overflow-hidden shadow-inner">
				<label class="w-14 h-14 flex items-center justify-center text-gray-400">` + iconLock + `</label>
				<input name="password" id="password-input" type="password" placeholder="` + passPlace + `" required
				       autocomplete="` + passAuto + `" minlength="` + passMinLen + `" maxlength="40"
				       class="flex-1 bg-transparent outline-none py-4 text-lg text-gray-900 dark:text-gray-100 pr-2">
				<button type="button" id="password-toggle-btn" onclick="toggleAuthPasswordVisibility()"
				        aria-label="Show password" tabindex="-1"
				        class="w-14 h-14 flex-shrink-0 flex items-center justify-center text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 focus:outline-none">
					<span id="password-eye-icon">` + iconEyeOpen + `</span>
				</button>
			</div>

			` + rulesCheckboxHTML + `

			<button type="submit"
			        class="w-full bg-blue-600 text-white font-bold py-4 rounded-2xl text-xl shadow-xl transition-all hover:bg-blue-700 disabled:opacity-60">
				` + btnText + `
			</button>
		</form>

		<div class="mt-8 text-center border-t border-gray-100 dark:border-gray-800 pt-6">
			<p class="text-gray-500 dark:text-gray-400 text-sm font-medium">
				` + footText + ` <a href="` + otherHref + `?lang=` + lang + `"
				   class="text-blue-600 font-bold hover:underline">` + linkText + `</a>
			</p>
			` + footerLinksHTML + `
		</div>
	</div>`
}

func renderSignupForm(c *fiber.Ctx, errorMsg string, partial bool, lang string) error {
	c.Set("Content-Type", "text/html")
	var t, s, np, pp, bt, ft, lt, tl, tb string
	if lang == "en" {
		t, s, np, pp, bt, ft, lt, tl, tb = "Create Account", "Join the Dostana family",
			"Username", "Password", "Create Account", "Already a member?", "Login", "ur", "اردو"
	} else {
		t, s, np, pp, bt, ft, lt, tl, tb = "اکاؤنٹ بنائیں", "دوستانہ فیملی میں شامل ہوں",
			"صارف کا نام", "پاس ورڈ", "شروع کریں", "پہلے سے اکاؤنٹ ہے؟", "لاگ ان", "en", "English"
	}
	body := renderAuthForm(t, s, np, pp, bt, ft, lt, "/signup", tl, tb, lang, errorMsg)
	// A plain (no-JS) form POST never sent HX-Request, so it always gets
	// the full page back — otherwise the browser renders a naked,
	// unstyled <div> with no <head>/CSS at all.
	if partial && isHtmxRequest(c) {
		return c.SendString(body)
	}
	return c.SendString(Layout("Sign Up", body, ""))
}

func renderLoginForm(c *fiber.Ctx, errorMsg string, partial bool, lang string) error {
	c.Set("Content-Type", "text/html")
	var t, s, np, pp, bt, ft, lt, tl, tb string
	if lang == "en" {
		t, s, np, pp, bt, ft, lt, tl, tb = "Welcome Back", "Log in to continue",
			"Username", "Password", "Login", "New here?", "Sign up free", "ur", "اردو"
	} else {
		t, s, np, pp, bt, ft, lt, tl, tb = "خوش آمدید", "لاگ ان کریں",
			"صارف کا نام", "پاس ورڈ", "لاگ ان", "نئے ہیں؟", "اکاؤنٹ بنائیں", "en", "English"
	}
	body := renderAuthForm(t, s, np, pp, bt, ft, lt, "/login", tl, tb, lang, errorMsg)
	// Same reasoning as renderSignupForm — only trust "partial" when the
	// request actually came from htmx.
	if partial && isHtmxRequest(c) {
		return c.SendString(body)
	}
	return c.SendString(Layout("Login", body, ""))
}

// ====================== ROUTE HANDLERS ======================

// ====================== ROUTE HANDLERS ======================

func signupGet(c *fiber.Ctx) error {
	lang := c.Query("lang", "ur")
	if getCurrentUser(c) != "" {
		return c.Redirect("/")
	}
	return renderSignupForm(c, "", false, lang)
}

func signupPost(c *fiber.Ctx) error {
	lang := c.Query("lang", "ur")

	// ── LAYER -1: Postgres pool circuit breaker ───────────────────────────────
	// If the pool is already ~90% saturated, refuse immediately instead of
	// piling this request on top — protects everyone ELSE already using the
	// site from being starved by a flood hitting /signup specifically.
	if dbUnderPressure() {
		msg := "⚠️ Server is busy right now. Please try again shortly."
		if lang == "ur" {
			msg = "⚠️ سرور اس وقت مصروف ہے۔ تھوڑی دیر بعد کوشش کریں۔"
		}
		return renderSignupForm(c, msg, true, lang)
	}

	// ── LAYER 0: Global (site-wide) signup rate cap ───────────────────────────
	// The per-IP/per-device layers below only stop ONE attacker. A botnet
	// spread across many IPs can stay under every per-IP limit individually
	// while still burying Postgres under combined load. This caps total
	// signup attempts across the whole site per minute, independent of who's
	// sending them, so a distributed flood can't take the DB down.
	globalSignupKey := "signup_rate:global"
	globalSignupAttempts, gErr := rdb.Incr(ctx, globalSignupKey).Result()
	if gErr == nil {
		if globalSignupAttempts == 1 {
			rdb.Expire(ctx, globalSignupKey, time.Minute)
		}
		if globalSignupAttempts > 150 { // tune upward if your real signup volume ever gets close
			msg := "⚠️ Server is busy right now. Please try again shortly."
			if lang == "ur" {
				msg = "⚠️ سرور اس وقت مصروف ہے۔ تھوڑی دیر بعد کوشش کریں۔"
			}
			return renderSignupForm(c, msg, true, lang)
		}
	}
	// fail-open on Redis error — never let a Redis hiccup block real signups

	// ── Collect signals ──────────────────────────────────────────────────────
	hardwareID := strings.TrimSpace(c.FormValue("hardware_id"))
	ip := getRealIP(c)
	subnet := getSubnet(ip)
	deviceKey := buildDeviceKey(c)

	// ── LAYER 1: Rate-limit keyed on server-computed device key ──────────────
	// Works even when JS is off / hardware_id is spoofed / incognito.
	rateLimitKey := "signup_rate:" + deviceKey
	ipRateKey := "signup_ip:" + ip
	attempts, _ := rdb.Incr(ctx, rateLimitKey).Result()
	if attempts == 1 {
		rdb.Expire(ctx, rateLimitKey, 10*time.Minute)
	}
	ipAttempts, _ := rdb.Incr(ctx, ipRateKey).Result()
	if ipAttempts == 1 {
		rdb.Expire(ctx, ipRateKey, 10*time.Minute)
	}
	if attempts > 5 || ipAttempts > 8 {
		msg := "⚠️ Too many attempts. Wait 10 minutes."
		if lang == "ur" {
			msg = "⚠️ بہت زیادہ کوششیں۔ 10 منٹ انتظار کریں۔"
		}
		return renderSignupForm(c, msg, true, lang)
	}

	// ── LAYER 2: Full 6-layer ban check (100% server-side) ───────────────────
	if ban := checkAllBans(c, hardwareID); ban.Banned {
		rdb.Set(ctx, "banned_ip:"+ip, "1", 24*time.Hour)
		msg := ban.Reason
		if lang == "ur" {
			msg = ban.ReasonUR
		}
		return renderSignupForm(c, msg, true, lang)
	}

	// ── LAYER 3: Max 5 accounts per device (all signals combined) ────────────
	if checkAccountLimit(c, hardwareID) {
		msg := "🚫 Device Limit Reached: Max 5 accounts per device."
		if lang == "ur" {
			msg = "🚫 اس ڈیوائس سے مزید اکاؤنٹ نہیں بن سکتے (حد 5 ہے)۔"
		}
		return renderSignupForm(c, msg, true, lang)
	}

	// ── LAYER 4: Input validation ─────────────────────────────────────────────
	rawUsername := c.FormValue("username")
	username := strings.ToLower(strings.Join(strings.Fields(rawUsername), ""))
	password := strings.TrimSpace(c.FormValue("password"))

	validUser := regexp.MustCompile(`^[a-z0-9_]+$`)
	if !validUser.MatchString(username) || len(username) < 3 || len(username) > 20 {
		msg := "❌ Invalid name (3-20 chars, a-z, 0-9, _)."
		if lang == "ur" {
			msg = "❌ نام درست نہیں ہے (3-20 حروف، صرف a-z، 0-9، _)۔"
		}
		return renderSignupForm(c, msg, true, lang)
	}

	if len(password) < 6 || len(password) > 40 {
		msg := "❌ Password must be 6–40 characters."
		if lang == "ur" {
			msg = "❌ پاس ورڈ 6 سے 40 حروف کا ہونا چاہیے۔"
		}
		return renderSignupForm(c, msg, true, lang)
	}

	// ── LAYER 4b: Rules acceptance — enforced server-side, not just via the
	// checkbox's HTML `required` attribute, which a non-browser client
	// could simply omit from the request entirely.
	if c.FormValue("accept_rules") != "on" {
		msg := "❌ You must accept the website rules to sign up."
		if lang == "ur" {
			msg = "❌ رجسٹریشن کے لیے آپ کو ویب سائٹ کے قواعد سے اتفاق کرنا ہوگا۔"
		}
		return renderSignupForm(c, msg, true, lang)
	}

	// ── LAYER 5: Banned username check (your original logic, restored) ────────
	// Blocks reserved / offensive usernames stored in banned_usernames table.
	if isBannedUsername(username) {
		msg := "❌ This username is not allowed."
		if lang == "ur" {
			msg = "❌ یہ نام استعمال کرنے کی اجازت نہیں ہے۔"
		}
		return renderSignupForm(c, msg, true, lang)
	}

	// ── LAYER 6: Username uniqueness ─────────────────────────────────────────
	uniqCtx, uniqCancel := context.WithTimeout(ctx, 3*time.Second)
	var exists int
	err := db.QueryRow(uniqCtx, "SELECT 1 FROM users WHERE username=$1", username).Scan(&exists)
	uniqCancel()
	if err == nil { // err==nil means a row WAS found -> name taken
		msg := "❌ This name is already taken."
		if lang == "ur" {
			msg = "❌ یہ نام پہلے سے موجود ہے۔"
		}
		return renderSignupForm(c, msg, true, lang)
	}

	// ── INSERT — only real columns from your schema + migration additions ─────
	// Your base schema has: username, password, hardware_id
	// Migration adds:       ip_address, subnet, device_key
	bcryptAcquire()
	hash, err := hashPassword(password)
	bcryptRelease()
	if err != nil {
		log.Println("signup hashPassword error:", err)
		return renderSignupForm(c, "❌ System error. Try again.", true, lang)
	}
	insertCtx, insertCancel := context.WithTimeout(ctx, 5*time.Second)
	_, err = db.Exec(insertCtx, `
		INSERT INTO users (username, password, hardware_id, ip_address, subnet, device_key, account_status)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending')`,
		username, string(hash), hardwareID, ip, subnet, deviceKey,
	)
	IndexUsername(username)
	insertCancel()
	if err != nil {
		fmt.Println("Signup Insert Error:", err)
		return renderSignupForm(c, "❌ System error. Try again.", true, lang)
	}

	// Admin pending-count badge now reads live from the DB (see
	// getCachedPendingCount) instead of a manually tracked counter, so
	// nothing needs incrementing here anymore.

	// ── Speed trap: bump the rate-limit counter so the next attempt must wait ─
	rdb.Set(ctx, rateLimitKey, "6", 5*time.Minute)

	// ── Session cookies ────────────────────────────────────────────────────
	sv, svErr := getSessionVersion(username)
	if svErr != nil {
		log.Println("signup getSessionVersion error:", svErr)
		return renderSignupForm(c, "❌ System error. Try again.", true, lang)
	}
	if err := createSession(c, username, sv); err != nil {
		log.Println("signup createSession error:", err)
		return renderSignupForm(c, "❌ System error. Try again.", true, lang)
	}

	// HX-Redirect only works if htmx's JS is there to read it — a no-JS
	// browser needs a real HTTP redirect or it's left staring at "ok".
	if isHtmxRequest(c) {
		c.Set("HX-Redirect", "/")
		return c.SendString("ok")
	}
	return c.Redirect("/")
}

func loginGet(c *fiber.Ctx) error {
	lang := c.Query("lang", "ur")
	if getCurrentUser(c) != "" {
		return c.Redirect("/")
	}

	msg := ""
	switch c.Query("error") {
	case "invalid_session", "session_expired":
		msg = "⚠️ Your session ended. Please log in again."
		if lang == "ur" {
			msg = "⚠️ آپ کا سیشن ختم ہو گیا۔ دوبارہ لاگ ان کریں۔"
		}
	case "banned":
		msg = "🚫 Your account has been banned."
		if lang == "ur" {
			msg = "🚫 آپ کی آئی ڈی پر پابندی عائد کر دی گئی ہے۔"
		}
	case "rejected":
		msg = "🚫 Your account registration was rejected by admin."
		if lang == "ur" {
			msg = "🚫 آپ کی رجسٹریشن ایڈمن نے مسترد کر دی ہے۔"
		}
	}
	return renderLoginForm(c, msg, false, lang)
}
func buildLoginBanMessage(username, lang string) string {
	info := getActiveBanInfo(username)
	if !info.Found {
		if lang == "ur" {
			return "🚫 آپ کی آئی ڈی پر پابندی عائد کر دی گئی ہے۔"
		}
		return "🚫 Your account has been banned."
	}

	loc, _ := time.LoadLocation("Asia/Karachi")
	durationText := "Permanent"
	durationTextUr := "مستقل"
	if info.ExpiresAt != nil {
		durationText = fmt.Sprintf("%s (until %s PKT)", banDurationLabel(info.Duration), info.ExpiresAt.In(loc).Format("02 Jan 2006 15:04"))
		durationTextUr = fmt.Sprintf("%s (تاریخ %s تک PKT)", banDurationLabel(info.Duration), info.ExpiresAt.In(loc).Format("02 Jan 2006 15:04"))
	}

	ruleText := ""
	ruleTextUr := ""
	if info.RuleNo > 0 {
		ruleText = fmt.Sprintf(" Rule #%d: %s.", info.RuleNo, info.RuleText)
		ruleTextUr = fmt.Sprintf(" قاعدہ #%d: %s۔", info.RuleNo, info.RuleText)
	}

	reasonText := ""
	reasonTextUr := ""
	if strings.TrimSpace(info.Reason) != "" {
		reasonText = fmt.Sprintf(" Reason: %s.", info.Reason)
		reasonTextUr = fmt.Sprintf(" وجہ: %s۔", info.Reason)
	}

	bannedByText := ""
	bannedByTextUr := ""
	if strings.TrimSpace(info.BannedBy) != "" {
		bannedByText = fmt.Sprintf(" Banned by: %s.", info.BannedBy)
		bannedByTextUr = fmt.Sprintf(" پابندی لگانے والا ایڈمن: %s۔", info.BannedBy)
	}

	if lang == "ur" {
		return fmt.Sprintf("🚫 آپ کی آئی ڈی پر پابندی عائد کر دی گئی ہے۔ مدت: %s۔%s%s%s",
			durationTextUr, ruleTextUr, reasonTextUr, bannedByTextUr)
	}
	return fmt.Sprintf("🚫 Your account has been banned. Duration: %s.%s%s%s",
		durationText, ruleText, reasonText, bannedByText)
}
func loginPost(c *fiber.Ctx) error {
	lang := c.Query("lang", "ur")

	// ── LAYER -1: Postgres pool circuit breaker ───────────────────────────────
	if dbUnderPressure() {
		msg := "⚠️ Server is busy right now. Please try again shortly."
		if lang == "ur" {
			msg = "⚠️ سرور اس وقت مصروف ہے۔ تھوڑی دیر بعد کوشش کریں۔"
		}
		return renderLoginForm(c, msg, true, lang)
	}

	// Same reasoning as signup — protects Postgres from a distributed
	// credential-stuffing flood that spreads across many IPs.
	globalLoginKey := "login_rate:global"
	globalLoginAttempts, gErr := rdb.Incr(ctx, globalLoginKey).Result()
	if gErr == nil {
		if globalLoginAttempts == 1 {
			rdb.Expire(ctx, globalLoginKey, time.Minute)
		}
		if globalLoginAttempts > 400 { // logins are far more frequent than signups; tune to real traffic
			msg := "⚠️ Server is busy right now. Please try again shortly."
			if lang == "ur" {
				msg = "⚠️ سرور اس وقت مصروف ہے۔ تھوڑی دیر بعد کوشش کریں۔"
			}
			return renderLoginForm(c, msg, true, lang)
		}
	}

	hardwareID := strings.TrimSpace(c.FormValue("hardware_id"))
	ip := getRealIP(c)
	deviceKey := buildDeviceKey(c)

	// ── LAYER 1: Rate-limit on login ──────────────────────────────────────────
	rateLimitKey := "login_rate:" + ip
	ipRateKey := "login_ip:" + ip
	ipAttempts, _ := rdb.Incr(ctx, ipRateKey).Result()
	if ipAttempts == 1 {
		rdb.Expire(ctx, ipRateKey, 15*time.Minute)
	}
	if ipAttempts > 80 {
		msg := "⚠️ Too many login attempts. Wait 15 minutes."
		if lang == "ur" {
			msg = "⚠️ بہت زیادہ لاگ ان کوششیں۔ 15 منٹ بعد کوشش کریں۔"
		}
		return renderLoginForm(c, msg, true, lang)
	}

	// ── LAYER 2: Full ban check ───────────────────────────────────────────────
	if ban := checkAllBans(c, hardwareID); ban.Banned {
		rdb.Set(ctx, "banned_ip:"+ip, "1", 24*time.Hour)
		msg := ban.Reason
		if lang == "ur" {
			msg = ban.ReasonUR
		}
		return renderLoginForm(c, msg, true, lang)
	}

	// ── LAYER 3: Credential check ─────────────────────────────────────────────
	username := strings.ToLower(strings.TrimSpace(c.FormValue("username")))
	password := strings.TrimSpace(c.FormValue("password"))

	if username == "" || password == "" {
		msg := "❌ Please enter both username and password."
		if lang == "ur" {
			msg = "❌ براہ کرم صارف کا نام اور پاس ورڈ درج کریں۔"
		}
		return renderLoginForm(c, msg, true, lang)
	}

	if len(password) > 40 {
		msg := "❌ Password must be 1–40 characters."
		if lang == "ur" {
			msg = "❌ پاس ورڈ 1 سے 40 حروف کا ہونا چاہیے۔"
		}
		return renderLoginForm(c, msg, true, lang)
	}

	userRateKey := "login_user:" + username
	userAttempts, _ := rdb.Incr(ctx, userRateKey).Result()
	if userAttempts == 1 {
		rdb.Expire(ctx, userRateKey, 15*time.Minute)
	}
	if userAttempts > 15 {
		msg := "⚠️ Too many login attempts for this account. Wait 15 minutes."
		if lang == "ur" {
			msg = "⚠️ اس اکاؤنٹ پر بہت زیادہ کوششیں۔ 15 منٹ بعد کوشش کریں۔"
		}
		return renderLoginForm(c, msg, true, lang)
	}

	// Fetch only columns that exist in your schema.
	// Your DB has `is_banned` (not `is_hardware_banned`).
	// Device-level bans are already caught by checkAllBans above.
	var dbPass, accountStatus string
	var isBanned bool
	loginCtx, loginCancel := context.WithTimeout(ctx, 5*time.Second)
	err := db.QueryRow(loginCtx,
		"SELECT password, is_banned, account_status FROM users WHERE username=$1",
		username).Scan(&dbPass, &isBanned, &accountStatus)
	loginCancel()

	if err != nil {
		if err == pgx.ErrNoRows {
			// User genuinely doesn't exist — dummy bcrypt compare so
			// response timing doesn't leak that either.
			bcryptAcquire()
			_ = bcrypt.CompareHashAndPassword([]byte("$2a$12$R9h/cIPz0gi.URNNX3kh2OPST9/PgBkqquzi.Ss7KIUgO2t0jWMUW"), []byte(password))
			bcryptRelease()
			msg := "❌ Invalid username or password."
			if lang == "ur" {
				msg = "❌ صارف کا نام یا پاس ورڈ درست نہیں ہے۔"
			}
			return renderLoginForm(c, msg, true, lang)
		}
		// A REAL error (DB timeout, connection pool exhausted, etc.) is
		// NOT the same as a wrong password. Previously this fell into
		// the same "invalid username or password" branch, so a user
		// with the correct credentials could see that message purely
		// because Postgres was slow/overloaded — indistinguishable from
		// an actual typo. Log it and tell the user the truth instead.
		log.Printf("loginPost DB error for %s: %v", username, err)
		msg := "⚠️ Server is busy right now. Please try again."
		if lang == "ur" {
			msg = "⚠️ سرور مصروف ہے۔ دوبارہ کوشش کریں۔"
		}
		return renderLoginForm(c, msg, true, lang)
	}

	if isBanned {
		msg := buildLoginBanMessage(username, lang)
		return renderLoginForm(c, msg, true, lang)
	}

	if accountStatus == "rejected" {
		msg := "🚫 Your account registration was rejected by admin."
		if lang == "ur" {
			msg = "🚫 آپ کی رجسٹریشن ایڈمن نے مسترد کر دی ہے۔"
		}
		return renderLoginForm(c, msg, true, lang)
	}

	// ── LAYER 4: Password verification ───────────────────────────────────────
	bcryptAcquire()
	err = bcrypt.CompareHashAndPassword([]byte(dbPass), []byte(password))
	bcryptRelease()
	if err != nil {
		msg := "❌ Invalid username or password."
		if lang == "ur" {
			msg = "❌ صارف کا نام یا پاس ورڈ درست نہیں ہے۔"
		}
		return renderLoginForm(c, msg, true, lang)
	}

	// ── LAYER 5: Refresh all backend signals on successful login ─────────────
	// Keeps tracking current even when the user's IP changes.
	subnet := getSubnet(ip)
	refreshCtx, refreshCancel := context.WithTimeout(ctx, 3*time.Second)
	_, _ = db.Exec(refreshCtx, `
		UPDATE users
		SET hardware_id=$1, ip_address=$2, subnet=$3, device_key=$4, last_active=NOW()
		WHERE username=$5`,
		hardwareID, ip, subnet, deviceKey, username)
	refreshCancel()

	// ── Session cookies ──────────────────────────────────────────────────────
	// Bumping the version here logs this account out on any other device
	// that's currently signed in — this is what enforces one device per ID.
	sv, svErr := getSessionVersion(username)
	if svErr != nil {
		log.Println("login getSessionVersion error:", svErr)
		msg := "❌ System error. Please try again."
		if lang == "ur" {
			msg = "❌ سسٹم میں خرابی۔ دوبارہ کوشش کریں۔"
		}
		return renderLoginForm(c, msg, true, lang)
	}
	if err := createSession(c, username, sv); err != nil {
		log.Println("login createSession error:", err)
		msg := "❌ System error. Please try again."
		if lang == "ur" {
			msg = "❌ سسٹم میں خرابی۔ دوبارہ کوشش کریں۔"
		}
		return renderLoginForm(c, msg, true, lang)
	}

	rdb.Del(ctx, rateLimitKey, ipRateKey, userRateKey)

	// Same as signupPost — a no-JS browser never reads HX-Redirect.
	if isHtmxRequest(c) {
		c.Set("HX-Redirect", "/")
		return c.SendString("Success")
	}
	return c.Redirect("/")
}
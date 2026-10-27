package main

import (
	"context"
	"io"
	"log"
	"mime"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mysocialmediawebsite/pkg/platform"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/helmet"
	"github.com/gofiber/fiber/v2/middleware/idempotency"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	fiberrecover "github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
	goredis "github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

// =============================================================================
// HELPERS
// =============================================================================

// rateLimitIP resolves the client's real IP the same way every other
// IP-based check in this codebase does (X-Real-IP, then first hop of
// X-Forwarded-For, then the raw socket address) — kept in one place so
// the rate limiter can't be dodged by hitting a code path that resolves
// IP differently.

var acctGateSF singleflight.Group

type acctGateResult struct {
	banned bool
	sv     int
	status string
}

// cookieDomain and cookieSecureOverride control every cookie this app
// sets — session, js_ok, and csrf. A STATIC guess at Secure/Domain
// (decided once at startup) is what previously broke logins and language
// switching "once and for all": if the guess doesn't match how a given
// request actually arrived (Secure=true set while the visitor is on
// plain HTTP, or Domain set to a value that doesn't match the host being
// used), the browser silently drops the Set-Cookie header — no error, it
// just never lands. That produces exactly two symptoms users hit: (1) a
// login that "succeeds" but the very next request finds no session and
// bounces back to /login?error=invalid_session (the Urdu "session
// expired" message), and (2) a language-switch click that gets no
// js_ok cookie, triggers JSRequiredMiddleware's HX-Refresh, and just
// reloads the current page at its current language.
//
// The fix: decide Secure/Domain PER REQUEST from what actually happened
// on that connection (see cookieSecureFor / cookieDomainFor below),
// instead of a fixed env-derived guess. COOKIE_DOMAIN and COOKIE_SECURE
// remain available as explicit overrides for an operator who wants to
// force one behavior, but neither is required for cookies to work
// correctly in any environment (IP, localhost, staging domain, or the
// real production domain, HTTP or HTTPS).
var cookieDomain string
var cookieSecureOverride *bool

func initCookieConfig() {
	cookieDomain = strings.TrimSpace(os.Getenv("COOKIE_DOMAIN"))
	if v := strings.TrimSpace(os.Getenv("COOKIE_SECURE")); v != "" {
		b := v != "false" && v != "0"
		cookieSecureOverride = &b
	}
	if cookieDomain == "" {
		log.Println("ℹ️  COOKIE_DOMAIN not set — cookies will be host-only (fine for IP/localhost testing; set COOKIE_DOMAIN=.dostana.biz in production if you need cross-subdomain cookies)")
	}
	log.Println("ℹ️  Cookie Secure is auto-detected per request from the actual connection (TLS or X-Forwarded-Proto) unless COOKIE_SECURE is explicitly set")
}

// requestIsSecure reports whether THIS request actually arrived over
// TLS — either directly, or via a reverse proxy that identifies itself
// with X-Forwarded-Proto: https. This is checked live per request
// instead of assumed once at startup, so the Secure cookie attribute
// always matches reality.
func requestIsSecure(c *fiber.Ctx) bool {
	if c.Protocol() == "https" {
		return true
	}
	return strings.EqualFold(c.Get("X-Forwarded-Proto"), "https")
}

// cookieSecureFor decides the Secure attribute for a cookie on this
// specific request. COOKIE_SECURE, if explicitly set, always wins;
// otherwise it's derived live from whether this connection is actually
// secure, so a cookie set over HTTP is never marked Secure (which would
// make the browser silently refuse to store it) and one set over HTTPS
// always is.
func cookieSecureFor(c *fiber.Ctx) bool {
	if cookieSecureOverride != nil {
		return *cookieSecureOverride
	}
	return requestIsSecure(c)
}

// cookieDomainFor returns cookieDomain only when it actually applies to
// this request's host. A Domain attribute that doesn't match the host
// (or isn't a real domain — e.g. a bare IP address) makes browsers
// silently reject the entire Set-Cookie header. Falling back to ""
// (host-only) instead keeps cookies working in every environment: IP
// testing, a different staging host, or the real production domain.
func cookieDomainFor(c *fiber.Ctx) string {
	if cookieDomain == "" {
		return ""
	}
	host := c.Hostname()
	if net.ParseIP(host) != nil {
		return ""
	}
	suffix := strings.TrimPrefix(cookieDomain, ".")
	if host == suffix || strings.HasSuffix(host, "."+suffix) {
		return cookieDomain
	}
	return ""
}

func rateLimitIP(c *fiber.Ctx) string {
	return c.IP()
}

// clearUserCookie expires the session cookie and purges legacy cookie names.
func clearUserCookie(c *fiber.Ctx) {
	if u := getCurrentUser(c); u != "" {
		revokeCurrentSession(u, c.Cookies(sessionCookieName))
	}
	dom := cookieDomainFor(c)
	sec := cookieSecureFor(c)
	c.Cookie(&fiber.Cookie{Name: sessionCookieName, Value: "", Domain: dom, Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, HTTPOnly: true, Secure: sec, SameSite: "Lax"})
	c.Cookie(&fiber.Cookie{Name: "user", Value: "", Domain: dom, Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, HTTPOnly: true, Secure: sec, SameSite: "Lax"})
	c.Cookie(&fiber.Cookie{Name: "sv", Value: "", Domain: dom, Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, HTTPOnly: true, Secure: sec, SameSite: "Lax"})

	// Also clear a HOST-ONLY variant (no Domain set) of the session
	// cookie. A cookie set without an explicit Domain attribute is
	// scoped only to the exact host that set it and is a DIFFERENT
	// cookie from a Domain-scoped one as far as the browser is
	// concerned — deleting one does not remove the other. Any account
	// that logged in before Domain was set explicitly would carry a
	// host-only cookie that this second delete is what actually
	// clears; without it that stale cookie keeps getting sent forever
	// and AccountGateCheck keeps rejecting it.
	c.Cookie(&fiber.Cookie{Name: sessionCookieName, Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, HTTPOnly: true, Secure: sec, SameSite: "Lax"})

	// Belt-and-suspenders: also explicitly clear the KNOWN production
	// Domain-scoped variant (".dostana.biz"), regardless of what
	// COOKIE_DOMAIN resolves to right now. cookieDomainFor() only ever
	// targets TODAY's env config — if COOKIE_DOMAIN was ever set/unset
	// differently in the past, a browser can still be holding a
	// `session` cookie scoped to ".dostana.biz" that the two deletes
	// above never touch (neither targets that Domain anymore). That
	// stale, already-revoked cookie then gets sent on the very next
	// request after logout, fails lookupSession, and AccountGateCheck
	// permanently bounces the user to /login?error=invalid_session —
	// even though the "current" cookie was cleared correctly.
	if dom != ".dostana.biz" {
		c.Cookie(&fiber.Cookie{Name: sessionCookieName, Value: "", Domain: ".dostana.biz", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, HTTPOnly: true, Secure: true, SameSite: "Lax"})
	}
}

// =============================================================================
// MIDDLEWARE
// =============================================================================

// OnlineMiddleware tracks active sessions in Redis.
func OnlineMiddleware(c *fiber.Ctx) error {
	if jsCheckExempt(c.Path()) {
		return c.Next()
	}
	username := getCurrentUser(c)
	if username == "" {
		return c.Next()
	}
	key := "online_tick:" + username
	rctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	isNew, err := platform.RDB.SetNX(rctx, key, "1", 45*time.Second).Result()
	cancel()
	if err == nil && isNew {
		go trackSession(username)
	}
	return c.Next()
}

// AdminOnlyMiddleware blocks any route under its prefix unless the caller
// is a logged-in admin (checked via the 90s author-meta cache).
func AdminOnlyMiddleware(c *fiber.Ctx) error {
	username := getCurrentUser(c)
	if username == "" {
		return c.Status(fiber.StatusForbidden).SendString("Forbidden")
	}
	dbCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var userType string
	if err := db.QueryRow(dbCtx, "SELECT user_type FROM users WHERE username=$1", username).Scan(&userType); err != nil || userType != "admin" {
		return c.Status(fiber.StatusForbidden).SendString("Forbidden")
	}
	return c.Next()
}

// AccountGateCheck: single per-request gate for logged-in users — not banned,
// session not invalidated, not pending/rejected. Result is cached in Redis
// for 30s so polling doesn't hit Postgres every tick. A ban/status change can
// take up to ~30s to apply to an in-flight session under that cache; session
// revocation (logout, password change, login elsewhere) is instant regardless,
// since that's enforced via the separate Redis session lookup below.
func AccountGateCheck(c *fiber.Ctx) error {
	path := c.Path()
	if jsCheckExempt(path) {
		return c.Next()
	}

	token := c.Cookies(sessionCookieName)
	username, cookieSV, valid := lookupSession(token)

	// These must stay reachable NO MATTER WHAT — even with a broken,
	// stale, or unclearable cookie — checked BEFORE the invalid-session
	// redirect below. Previously this switch ran only after a valid
	// session was confirmed, so a stale cookie that failed to clear
	// client-side (e.g. a legacy host-only cookie predating an explicit
	// Domain, which clearUserCookie's domain-scoped delete can't touch)
	// sent every user back through /login?error=invalid_session forever
	// — ERR_TOO_MANY_REDIRECTS.
	switch path {
	case "/logout", "/login", "/login-form", "/signup", "/signup-form", "/favicon.ico":
		if valid {
			cacheUsername(c, username)
		} else {
			cacheUsername(c, "")
		}
		return c.Next()
	}

	if !valid {
		cacheUsername(c, "")
		if token != "" {
			// A cookie was actually sent but didn't resolve to a live
			// session — genuinely expired/revoked/tampered. Clear it and
			// bounce to login with the "session expired" notice.
			clearUserCookie(c)
			c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
			if path == "/notifications/count" || path == "/invites/count" || path == "/online-slider" {
				return c.SendStatus(fiber.StatusNoContent)
			}
			return c.Redirect("/login?error=invalid_session")
		}
		// No cookie at all — a brand-new/anonymous visitor, not someone
		// whose session actually expired. This used to fall into the
		// same branch above and get redirected to
		// /login?error=invalid_session on every page including "/",
		// which is why a first-time visitor saw "Your session expired,
		// log in again" instead of ever reaching homeGet's own
		// anonymous -> /signup redirect. Let them through to the real
		// handler; public pages already handle an empty currentUser
		// correctly, and protected actions still prompt for login via
		// requireUser/getCurrentUser downstream.
		if path == "/notifications/count" || path == "/invites/count" || path == "/online-slider" {
			c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
			return c.SendStatus(fiber.StatusNoContent)
		}
		return c.Next()
	}
	cacheUsername(c, username)
	// Quiet polling endpoints get an empty response instead of a redirect
	// when blocked, since they're never rendered as full pages.
	isQuietPoll := path == "/notifications/count" || path == "/invites/count" || path == "/online-slider"

	var isBanned bool
	var dbSessionVersion int
	var accountStatus string
	cacheHit := false

	cacheKey := "acct_gate:" + username

	cacheCtx, cacheCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	cached, cacheErr := platform.RDB.Get(cacheCtx, cacheKey).Result()
	cacheCancel()

	if cacheErr == nil {
		if parts := strings.SplitN(cached, "|", 3); len(parts) == 3 {
			isBanned = parts[0] == "1"
			dbSessionVersion, _ = strconv.Atoi(parts[1])
			accountStatus = parts[2]
			cacheHit = true
		}
	}

	if !cacheHit {
		// Concurrent requests for the same username share ONE Postgres
		// query instead of each firing its own. Without this, a Redis
		// blip turns into a stampede of identical SELECTs on every
		// request from every logged-in user at once.
		v, sfErr, _ := acctGateSF.Do(cacheKey, func() (interface{}, error) {
			dbCtx, dbCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer dbCancel()

			var banned bool
			var sv int
			var status string
			qErr := db.QueryRow(dbCtx,
				"SELECT is_banned, session_version, account_status FROM users WHERE username=$1",
				username,
			).Scan(&banned, &sv, &status)
			if qErr != nil {
				return nil, qErr
			}

			flag := "0"
			if banned {
				flag = "1"
			}
			setCtx, setCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			platform.RDB.Set(setCtx, cacheKey, flag+"|"+strconv.Itoa(sv)+"|"+status, 30*time.Second)
			setCancel()

			return acctGateResult{banned: banned, sv: sv, status: status}, nil
		})

		if sfErr != nil {
			if sfErr == pgx.ErrNoRows {
				cacheUsername(c, "")
				clearUserCookie(c)
				c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
				return c.Redirect("/login?error=rejected")
			}
			log.Printf("AccountGateCheck DB error for %s: %v", username, sfErr)
			if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
				return c.Status(fiber.StatusServiceUnavailable).SendString("Service temporarily unavailable. Please try again.")
			}
			return c.Next()
		}

		res := v.(acctGateResult)
		isBanned = res.banned
		dbSessionVersion = res.sv
		accountStatus = res.status
	}

	if cookieSV != dbSessionVersion {
		clearUserCookie(c)
		c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
		if isQuietPoll {
			return c.SendStatus(fiber.StatusNoContent)
		}
		return c.Redirect("/login?error=session_expired")
	}

	if isBanned {
		go func(u string) {
			bgCtx, bgCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer bgCancel()
			platform.RDB.ZRem(bgCtx, "active_sessions", u)
		}(username)

		clearUserCookie(c)
		c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
		if isQuietPoll {
			return c.SendStatus(fiber.StatusNoContent)
		}
		return c.Redirect("/login?error=banned")
	}

	if accountStatus == "pending" {
		if isQuietPoll {
			c.Set("Cache-Control", "no-store")
			return c.SendStatus(fiber.StatusNoContent)
		}
		// Pending accounts can browse the site like anyone else (GET/HEAD)
		// but can't take any action — post, comment, react, follow, chat,
		// etc — until an admin approves them. c.Locals flags this for
		// handlers (see homeGet) that want to show a friendly reminder.
		c.Locals("pendingApproval", true)
		if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
			msg := "⏳ Your account is still awaiting admin approval — you can look around, but can't post, comment, follow, react, or chat yet."
			return c.Status(fiber.StatusForbidden).SendString(msg)
		}
		return c.Next()
	}
	if accountStatus == "rejected" {
		clearUserCookie(c)
		if isQuietPoll {
			c.Set("Cache-Control", "no-store")
			return c.SendStatus(fiber.StatusNoContent)
		}
		return c.Redirect("/login?error=rejected")
	}

	return c.Next()
}

// lastActiveMiddleware updates last_active at most once per minute per user.
func lastActiveMiddleware(c *fiber.Ctx) error {
	if jsCheckExempt(c.Path()) {
		return c.Next()
	}
	username := getCurrentUser(c)
	if username == "" {
		return c.Next()
	}

	trigger := c.Get("HX-Trigger")
	path := c.Path()
	isPoll := trigger == "inbox-badge-bottom" ||
		trigger == "invite-badge-bottom" ||
		path == "/notifications/count" ||
		path == "/invites/count" ||
		path == "/online-slider"

	if !isPoll {
		key := "active_status:" + username
		rctx, cancel := context.WithTimeout(platform.Ctx, 1*time.Second)
		defer cancel()

		if isNew, err := platform.RDB.SetNX(rctx, key, "1", time.Minute).Result(); err == nil && isNew {
			go func(u string) {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("panic in last_active update: %v", r)
					}
				}()
				dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer dbCancel()
				_, _ = db.Exec(dbCtx, "UPDATE users SET last_active=NOW() WHERE username=$1", u)
			}(username)
		}
	}
	return c.Next()
}

// ipTrackingMiddleware logs IP/user-agent at most once per 5 min per user.
// Requires: users.user_agent, users.last_seen_at, and the user_ip_history
// table (see initDatabase for the actual migrations/indexes).
func ipTrackingMiddleware(c *fiber.Ctx) error {
	if jsCheckExempt(c.Path()) {
		return c.Next()
	}
	username := getCurrentUser(c)
	if username == "" {
		return c.Next()
	}

	path := c.Path()
	if len(path) >= 8 && path[:8] == "/uploads" {
		return c.Next()
	}
	if len(path) >= 7 && path[:7] == "/static" {
		return c.Next()
	}
	if path == "/notifications/count" || path == "/invites/count" ||
		path == "/online-slider" || path == "/favicon.ico" {
		return c.Next()
	}

	ip := rateLimitIP(c)

	subnet := ""
	octets := strings.Split(ip, ".")
	if len(octets) == 4 {
		subnet = octets[0] + "." + octets[1] + "." + octets[2] + ".0/24"
	}

	userAgent := c.Get("User-Agent")
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}

	redisKey := "ip_track:" + username

	rctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	isNew, err := platform.RDB.SetNX(rctx, redisKey, "1", 5*time.Minute).Result()
	cancel()

	if err != nil || !isNew {
		return c.Next()
	}

	go func(u, ipAddr, sn, ua string) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in ipTrackingMiddleware goroutine: %v", r)
			}
		}()

		dbCtx, dbCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer dbCancel()

		if _, dbErr := db.Exec(dbCtx, `
			UPDATE users
			SET ip_address   = $1,
			    subnet       = $2,
			    user_agent   = $3,
			    last_seen_at = NOW()
			WHERE username = $4`,
			ipAddr, sn, ua, u); dbErr != nil {

			log.Printf("ipTracking users UPDATE error for %s: %v", u, dbErr)
			return
		}

		if _, dbErr := db.Exec(dbCtx, `
			INSERT INTO user_ip_history (username, ip_address, subnet, user_agent)
			SELECT $1, $2, $3, $4
			WHERE NOT EXISTS (
			    SELECT 1 FROM user_ip_history
			    WHERE username   = $1
			      AND ip_address = $2
			      AND seen_at    > NOW() - INTERVAL '1 hour'
			)`,
			u, ipAddr, sn, ua); dbErr != nil {
			log.Printf("ipTracking history INSERT error for %s: %v", u, dbErr)
		}
	}(username, ip, subnet, userAgent)

	return c.Next()
}

// =============================================================================
// JAVASCRIPT REQUIREMENT GATE
// =============================================================================

const jsCookieName = "js_ok"

func jsCheckExempt(path string) bool {
	if len(path) >= 8 && path[:8] == "/uploads" {
		return true
	}
	if len(path) >= 7 && path[:7] == "/static" {
		return true
	}
	if len(path) >= 12 && path[:12] == "/.well-known" {
		return true
	}
	switch path {
	case "/favicon.ico", "/robots.txt", "/sitemap.xml",
		"/sitemap-profiles.xml", "/sitemap-posts.xml", "/.env", "/js-ok",
		"/api/device-token":
		return true
	}
	return false
}

func JSRequiredMiddleware(c *fiber.Ctx) error {
	path := c.Path()
	if jsCheckExempt(path) {
		return c.Next()
	}
	if verifySignedValue(c.Cookies(jsCookieName)) {
		return c.Next()
	}

	if c.Get("HX-Request") == "true" {
		c.Set("HX-Refresh", "true")
		return c.SendStatus(fiber.StatusOK)
	}

	c.Set("Cache-Control", "no-store")

	target := c.OriginalURL()
	if len(target) == 0 || target[0] != '/' {
		target = "/"
	}

	if c.Query("jschk") == "1" {
		return c.Status(fiber.StatusOK).Type("html").SendString(noJSPage())
	}

	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	redirectTarget := target + sep + "jschk=1"

	return c.Status(fiber.StatusOK).Type("html").SendString(jsCheckPage(redirectTarget))
}

func jsCheckPage(redirectTo string) string {
	// Previously: fetch("/js-ok") then wait for its response before
	// redirecting — two sequential round trips before the real page even
	// starts loading. On Pakistani mobile-data RTTs (300-800ms is
	// common) that alone adds well over a second of dead time on every
	// first visit. navigator.sendBeacon fires the same cookie-setting
	// POST but doesn't block on a response, so the redirect (which is
	// what the browser actually needs to move forward) happens
	// immediately in parallel instead of waiting on it.
	return `<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Loading Dostana…</title></head>
<body style="margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;font-family:system-ui,sans-serif;background:#0f172a;color:#fff;">
<script>
// IMPORTANT: wait for /js-ok to actually complete (so the Set-Cookie
// header has landed) before navigating. Firing-and-forgetting via
// sendBeacon caused a race on slow connections: the redirect could
// reach the server before the cookie was set, making JS-enabled
// browsers get bounced to the "JavaScript is disabled" page.
var go = function () { window.location.replace(` + strconv.Quote(redirectTo) + `); };
var timedOut = false;
var t = setTimeout(function () { timedOut = true; go(); }, 4000); // safety net if /js-ok never responds
fetch("/js-ok", {method:"POST", credentials:"same-origin", keepalive:true})
  .catch(function(){})
  .then(function () {
    if (!timedOut) { clearTimeout(t); go(); }
  });
</script>
<noscript>` + noJSBody() + `</noscript>
<div>Loading…</div>
</body></html>`
}

func noJSPage() string {
	return `<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>JavaScript Required | Dostana</title></head>
<body style="margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;font-family:system-ui,sans-serif;background:#0f172a;color:#fff;padding:20px;">
` + noJSBody() + `
</body></html>`
}

func noJSBody() string {
	return `<div style="max-width:420px;text-align:center;background:#111827;border:1px solid #1f2937;border-radius:20px;padding:32px 24px;">
	<div style="font-size:40px;margin-bottom:12px;">⚠️</div>
	<h1 style="font-size:20px;font-weight:800;margin:0 0 10px;color:#fff;">JavaScript is disabled</h1>
	<p style="font-size:14px;line-height:1.6;color:#9ca3af;margin:0 0 16px;">
		Dostana needs JavaScript (and cookies) turned on to work — please enable it in your browser and reload this page.
	</p>
	<p style="font-size:13px;line-height:1.6;color:#9ca3af;margin:0;">
		Not sure how? Try a current, well-known browser with JavaScript on by default, such as
		<strong style="color:#e5e7eb;">Google Chrome</strong>, <strong style="color:#e5e7eb;">Mozilla Firefox</strong>,
		<strong style="color:#e5e7eb;">Microsoft Edge</strong>, or <strong style="color:#e5e7eb;">Safari</strong>.
	</p>
</div>`
}

// =============================================================================
// MAIN
// =============================================================================

// DisplayName returns the user's stylized show name if they've set one,
// else falls back to their username. Render-only — every backend/system
// process (sessions, cache keys, FKs, URLs) must keep using username.
func DisplayName(username, displayName string) string {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return username
	}
	if r := []rune(displayName); len(r) > maxDisplayNameRunes {
		displayName = string(r[:maxDisplayNameRunes])
	}
	return displayName
}
func main() {
	if err := os.Setenv("TZ", "Asia/Karachi"); err != nil {
		log.Fatal("Could not set timezone:", err)
	}
	if loc, err := time.LoadLocation("Asia/Karachi"); err != nil {
		log.Printf("Warning: could not load Asia/Karachi: %v", err)
	} else {
		time.Local = loc
		log.Println("🌍 Timezone set to Asia/Karachi")
	}
	log.Printf("Current App Time: %s", time.Now().Format("02 Jan 2006 03:04 PM"))

	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found — using system environment variables")
	}
	mime.AddExtensionType(".mp4", "video/mp4")
	mime.AddExtensionType(".webm", "video/webm")
	mime.AddExtensionType(".mov", "video/quicktime")
	mime.AddExtensionType(".mkv", "video/x-matroska")

	initCookieConfig()
	initRedis()
	initAppSecret()
	initDatabase()
	if err := InitFCM("/etc/dostana/firebase-sa.json"); err != nil {
		log.Printf("⚠️  FCM not initialized (push notifications disabled): %v", err)
	}
	backfillUsernameIndex()
	initUploadDir()
	initChunkUploadDir()
	loadEmojiMap()
	StartTrendingWorker()
	initDPCompetitionSchema()
	StartDPCompetitionWorker()
	initWordGameSchema()
	initCakeSchema()
	StartWordGameWorker()
	StartBanExpiryWorker()
	startFeedHub()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in points-flush worker (loop stopped): %v", r)
			}
		}()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("panic in FlushPendingPoints: %v", r)
					}
				}()
				FlushPendingPoints()
			}()
		}
	}()

	app := fiber.New(fiber.Config{
		// 210MB — 200MB video ceiling + multipart overhead. The real
		// 200MB check happens inside HandleVideoUpload, so an oversized
		// file gets a clean error message instead of a raw connection
		// reset at the transport layer.
		BodyLimit:       520 * 1024 * 1024,
		ReadTimeout:     12 * time.Second,
		WriteTimeout:    0, // SSE /feed/stream needs an unbounded write
		IdleTimeout:     60 * time.Second,
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		// SSE holds connections open indefinitely; real concurrent load is
		// active streams + polling + page loads stacked together. Lowered
		// from 20000 — the per-IP limiter above now rejects abusive
		// traffic before it ever reaches this ceiling, so this only needs
		// to cover genuine simultaneous legitimate load, not an
		// unbounded flood.
		Concurrency:           3000,
		ReduceMemoryUsage:     true,
		DisableStartupMessage: false,
		StreamRequestBody:     true,
		// Only trust X-Forwarded-For when the connection is actually
		// coming from your own proxy/LB — fill in its real IP/CIDR below.
		EnableTrustedProxyCheck: true,
		TrustedProxies:          []string{"127.0.0.1"}, // <- replace with your proxy/LB's real IP or CIDR
		ProxyHeader:             fiber.HeaderXForwardedFor,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			msg := err.Error()
			if !strings.Contains(msg, "EOF") &&
				!strings.Contains(msg, "reading request headers") &&
				!strings.Contains(msg, "well-known") {
				log.Printf("Fiber error [%s %s]: %v", c.Method(), c.Path(), err)
			}
			return c.Status(code).SendString("Something went wrong. Please try again.")
		},
	})

	app.Use(helmet.New(helmet.Config{
		XSSProtection:             "1; mode=block",
		ContentTypeNosniff:        "nosniff",
		XFrameOptions:             "SAMEORIGIN",
		ReferrerPolicy:            "strict-origin-when-cross-origin",
		CrossOriginEmbedderPolicy: "require-corp",
		CrossOriginOpenerPolicy:   "same-origin",
	}))

	app.Use(cors.New(cors.Config{
		AllowOrigins:     "https://dostana.biz",
		AllowCredentials: true,
		AllowMethods:     "GET,POST,PUT,DELETE",
	}))

	app.Use(idempotency.New()) // no-op unless a client sends Idempotency-Key — safe to mount globally

	app.Use(fiberrecover.New(fiberrecover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(c *fiber.Ctx, e interface{}) {
			log.Printf("PANIC recovered [%s %s]: %v", c.Method(), c.Path(), e)
		},
	}))

	app.Use(compress.New(compress.Config{
		Level: compress.LevelBestSpeed,
		Next: func(c *fiber.Ctx) bool {
			return c.Path() == "/feed/stream"
		},
	}))

	app.Use(func(c *fiber.Ctx) error {
		c.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' 'unsafe-eval' https://cdnjs.cloudflare.com https://pagead2.googlesyndication.com https://*.googlesyndication.com https://*.googletagservices.com https://*.google.com; "+
				"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "+
				"font-src 'self' https://fonts.gstatic.com; "+
				"img-src 'self' data: https:; "+
				"media-src 'self' blob:; "+
				"connect-src 'self' https://pagead2.googlesyndication.com https://*.googlesyndication.com https://*.google.com https://*.doubleclick.net https://ep1.adtrafficquality.google https://*.adtrafficquality.google; "+
				"object-src 'none'; "+
				"base-uri 'self'; "+
				"form-action 'self'; "+
				"frame-src 'self' https://www.youtube-nocookie.com https://www.youtube.com https://*.googlesyndication.com https://*.doubleclick.net; "+
				"frame-ancestors 'self';")
		c.Set("X-Frame-Options", "SAMEORIGIN")
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		c.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=(self)")
		return c.Next()
	})

	// ── Per-path body size cap ───────────────────────────────────────────────
	// BodyLimit above is 520MB globally so video uploads on /post and /share
	// work — but that also means every OTHER endpoint (login, search,
	// comments, reactions, jhappi/chupair) accepts up to 520MB bodies too.
	// A rate limiter counts REQUESTS, not bytes: an attacker can open a
	// handful of connections, each sending a huge body to a cheap endpoint,
	// and exhaust server memory/disk well before hitting any request-count
	// limit. This rejects oversized bodies immediately via Content-Length —
	// before the body is ever read into memory — for every path except the
	// few that legitimately need large uploads.
	largeBodyAllowed := map[string]bool{
		"/post":                true,
		"/post/voice":          true,
		"/update-profile":      true,
		"/mehfil/upload-image": true,
	}
	const smallBodyCap = 15 * 1024 * 1024 // 15MB — generous for normal forms/JSON, nowhere near video size
	app.Use(func(c *fiber.Ctx) error {
		if largeBodyAllowed[c.Path()] || strings.HasPrefix(c.Path(), "/chat/send-voice/") {
			return c.Next()
		}
		req := c.Request()
		// Header.ContentLength() is fasthttp's parsed value. It is -1 for
		// chunked/unknown-length requests — that is NORMAL (proxies, CDNs,
		// some fetch() calls including the admin panel), not an attack.
		// Only hard-reject when the size IS known and over the cap. When
		// it's unknown, clamp the body STREAM itself with io.LimitReader
		// so the server can never be forced to buffer more than
		// smallBodyCap+1 bytes — same DoS protection, without falsely
		// blocking legitimate small chunked requests.
		if n := req.Header.ContentLength(); n > smallBodyCap {
			return c.Status(fiber.StatusRequestEntityTooLarge).SendString("Request too large for this endpoint.")
		} else if n < 0 {
			if bs := req.BodyStream(); bs != nil {
				req.SetBodyStream(io.LimitReader(bs, smallBodyCap+1), -1)
			}
		}
		return c.Next()
	})

	// Standard fiber rate limiter — just rate limits, doesn't throttle
	// real users. Default KeyGenerator (c.IP()).
	app.Use(limiter.New(limiter.Config{
		Max:        300,
		Expiration: 1 * time.Minute,
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).SendString("Too many requests — please slow down.")
		},
	}))

	app.Use(JSRequiredMiddleware)
	app.Use(CSRFProtect)
	app.Use(limiter.New(limiter.Config{
		Max:        20,
		Expiration: 1 * time.Minute,
		Next: func(c *fiber.Ctx) bool {
			p := c.Path()
			return !(p == "/login" || p == "/signup")
		},
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).SendString("Too many attempts — please slow down.")
		},
	}))
	app.Use(AccountGateCheck)
	app.Use(OnlineMiddleware)
	app.Use(lastActiveMiddleware)
	app.Use(ipTrackingMiddleware)
	app.Use(EmojiShortcodeMiddleware)
	// ── Static files ──────────────────────────────────────────────────────────
	app.Static("/uploads/mehfil_images", "./uploads/mehfil_images")
	app.Static("/uploads/posts", "./uploads/posts", fiber.Static{
		ByteRange: true,
	})
	app.Static("/uploads/chat_images", "./uploads/chat_images")
	app.Static("/uploads/home_voice_chat", "./uploads/home_voice_chat")
	app.Static("/uploads/chat_voice_chat", "./uploads/chat_voice_chat")
	app.Static("/uploads", "./uploads/profile_images")
	app.Static("/downloads", "./downloads")
	app.Use("/uploads", func(c *fiber.Ctx) error {
		return c.Status(404).SendString("")
	})
	app.Static("/static", "./static", fiber.Static{
		Compress:      true,
		ByteRange:     true,
		CacheDuration: 7 * 24 * time.Hour,
		MaxAge:        7 * 24 * 3600,
	})

	// ── SEO ───────────────────────────────────────────────────────────────────
	app.Get("/sitemap.xml", SitemapHandler)
	app.Get("/sitemap-profiles.xml", SitemapProfilesHandler)
	app.Get("/sitemap-posts.xml", SitemapPostsHandler)
	app.Get("/robots.txt", RobotsHandler)

	// ── HTMX partial form loaders ─────────────────────────────────────────────
	app.Post("/js-ok", jsVerifyHandler)
	app.Get("/signup-form", func(c *fiber.Ctx) error {
		return renderSignupForm(c, "", true, c.Query("lang", "ur"))
	})
	app.Get("/login-form", func(c *fiber.Ctx) error {
		return renderLoginForm(c, "", true, c.Query("lang", "ur"))
	})

	// ── Giphy ─────────────────────────────────────────────────────────────────
	app.Get("/giphy/search", GiphySearchHandler)
	app.Get("/post/:id/edit-form", postEditFormHandler)
	app.Get("/post/:id/edit-cancel", postEditCancelHandler)
	app.Post("/post/:id/edit", postEditSubmitHandler)

	app.Get("/comment/:id/edit-form", commentEditFormHandler)
	app.Get("/comment/:id/edit-cancel", commentEditCancelHandler)
	app.Post("/comment/:id/edit", commentEditSubmitHandler)
	// ── Mentions ──────────────────────────────────────────────────────────────
	app.Get("/mention/search", MentionSearchHandler)

	// ── Post interactions ─────────────────────────────────────────────────────
	app.Post("/react/:type/:id", HandleReaction)
	app.Post("/repost/:id", repostHandler)

	app.Get("/post/:id/interactions", func(c *fiber.Ctx) error {
		return c.Render("interactions_page", fiber.Map{"PostID": c.Params("id")})
	})

	// ── Block ─────────────────────────────────────────────────────────────────
	app.Post("/block-toggle/:username", BlockToggleHandler)

	// ── Invites ───────────────────────────────────────────────────────────────
	app.Get("/invites/count", func(c *fiber.Ctx) error { return c.SendString("") })

	// ── Search / Settings / Discovery ─────────────────────────────────────────
	app.Get("/search", SearchPage)
	app.Get("/settings", SettingsPage)
	app.Get("/emojis", EmojiGalleryHandler)
	app.Get("/for-me", ForMePage)
	app.Get("/leaderboard", LeaderboardPageHandler)
	app.Get("/leaderboard/section", LeaderboardSectionHandler)
	app.Get("/top-stars", leaderboardRedirect("scorers"))
	app.Get("/categories", categoriesPageHandler)
	app.Get("/categories/filter", filterCategoryHandler)
	app.Get("/online-slider", onlineSliderHandler)
	app.Get("/chat/deleted/:username", GetDeletedForEveryoneHandler)
	app.Delete("/notifications/clear", clearAllNotificationsHandler)
	app.Delete("/notifications/:type/:id/dismiss", notifDismissHandler)
	// ── Pending ID system ─────────────────────────────────────────────────────
	app.Get("/rules", rulesPageHandler)
	app.Get("/about", aboutPageHandler)
	app.Get("/terms", termsPageHandler)
	app.Get("/privacy", privacyPageHandler)

	app.Use("/.well-known", func(c *fiber.Ctx) error { return c.SendStatus(204) })
	app.Use("/.env", func(c *fiber.Ctx) error { return c.SendStatus(204) })

	// ── Admin (role-gated) ────────────────────────────────────────────────────
	app.Use("/admin", AdminOnlyMiddleware)
	app.Get("/admin/dashboard", adminDashboardHandler)
	app.Get("/admin/ban-logs", banLogsPageHandler)
	app.Get("/admin/ban-logs/load-more", banLogsLoadMoreHandler)
	app.Get("/admin/pending", adminPendingListHandler)
	app.Post("/admin/pending/approve/:username", adminApprovePendingHandler)
	app.Post("/admin/pending/reject/:username", adminRejectPendingHandler)
	app.Post("/admin/toggle-ban/:username", adminToggleBanHandler)
	app.Post("/admin/set-points/:username", adminSetPointsHandler)
	app.Get("/admin/identity/:username", adminIdentityPanelHandler)
	app.Get("/admin/bans", adminBansPageHandler)
	app.Get("/admin/bans/search", adminBanSearchHandler)
	app.Get("/admin/bans/form/:username", adminBanUserFormHandler)
	app.Post("/admin/bans/create/:username", adminCreateBanHandler)
	app.Post("/admin/bans/unban/:username", adminUnbanHandler)
	app.Post("/mehfil/invite/:id", SendMehfilInvite)
	app.Post("/mehfil/invite-respond/:id/:action", RespondMehfilInvite)
	app.Post("/mehfil/invite/:id", SendMehfilInvite)
	app.Get("/mehfil/invite-respond/:id/:action", RespondMehfilInvite)
	app.Post("/mehfil/invite-respond/:id/:action", RespondMehfilInvite)
	// ── Chat invites ──────────────────────────────────────────────────────────
	app.Post("/chat/invite/:username", SendChatInviteHandler)
	app.Get("/invites", InvitesPageHandler)
	app.Delete("/chat/leave/:username", LeaveChatHandler)
	app.Post("/chat/accept/:username", AcceptInviteHandler)
	app.Post("/chat/reject/:username", RejectInviteHandler)
	app.Delete("/chat/conversation/:username", DeleteConversationHandler)

	// ── Chat ──────────────────────────────────────────────────────────────────
	app.Get("/chat/view/:username", ChatViewHandler)
	app.Get("/chat/:username", ChatViewHandler)
	app.Get("/chat/messages/:username", GetMessagesHandler)
	app.Get("/chat/ticks/:username", GetTicksHandler)
	app.Post("/chat/send/:username", SendMessageHandler)
	app.Post("/chat/send-voice/:username", SendVoiceMessageHandler)
	app.Delete("/chat/delete/:username/:id", DeleteChatMessageHandler)
	app.Post("/chat/img-permission/:username/approve/:msgId", ApproveImagePermissionHandler)
	app.Post("/chat/img-permission/:username/deny/:msgId", DenyImagePermissionHandler)
	app.Get("/user/status/:username", UserStatusHandler)

	// ── Mehfil ────────────────────────────────────────────────────────────────
	app.Get("/popular", PopularPage)
	app.Get("/joined-mehfils", JoinedMehfilsPage)
	app.Post("/leave-mehfil/:id", LeaveMehfilHandler)
	app.Post("/delete-mehfil/:id", DeleteMehfilHandler)
	app.Get("/create", CreatePage)
	app.Post("/create", CreateAction)
	app.Get("/view/:id", ViewPage)
	app.Get("/mehfil/messages/:id", GetMehfilMessages)
	app.Post("/mehfil/send/:id", SendMehfilMessage)
	app.Post("/mehfil/join/:id", JoinMehfil)
	app.Post("/mehfil/leave/:id", LeaveMehfil)
	app.Post("/mehfil/react/:id", ReactToMessage)
	app.Post("/mehfil/pin/:id", PinMessage)
	app.Post("/mehfil/announce/:id", AnnounceMessage)
	app.Post("/mehfil/broadcast/:id", BroadcastAnnouncement)
	app.Post("/mehfil/mute/:id/:username", MuteMember)
	app.Post("/mehfil/role/:id/:username/:role", UpdateUserRole)
	app.Post("/mehfil/set-color/:id", SetMehfilColor)
	app.Post("/mehfil/upload-image", UploadMehfilImage)
	app.Put("/mehfil/update-topic/:id", UpdateMehfilTopic)
	app.Put("/mehfil/update-rules/:id", UpdateMehfilRules)
	app.Put("/mehfil/update-fee/:id", UpdateJoiningFee)
	app.Put("/mehfil/toggle-lock/:id", ToggleMehfilLock)
	app.Put("/mehfil/update-welcome/:id", UpdateWelcomeMsg)
	app.Put("/mehfil/update-maxmembers/:id", UpdateMaxMembers)
	app.Delete("/mehfil/hide/:id", HideComment)
	app.Delete("/mehfil/kick/:id/:username", KickMember)
	app.Delete("/mehfil/delete-own/:id", DeleteOwnMessage)

	// ── Earn points ───────────────────────────────────────────────────────────
	app.Get("/earn-points", AuthRequiredGuard, PointsEarnPageHandler)
	app.Post("/claim-bonus-points", AuthRequiredGuard, PostClaimPointsHandler)

	// ── Birthday cake ─────────────────────────────────────────────────────────
	app.Get("/birthday-cake", AuthRequiredGuard, birthdayCakePage)
	app.Get("/birthday-cake/state", birthdayCakeStateHandler)
	app.Post("/birthday-cake/eat", birthdayCakeEatHandler)
	app.Post("/birthday-cake/wish", birthdayCakeWishHandler)
	app.Post("/birthday/pop", birthdayPopHandler)

	// ── Auth ──────────────────────────────────────────────────────────────────
	app.Get("/signup", signupGet)
	app.Post("/signup", signupPost)
	app.Get("/login", loginGet)
	app.Post("/login", loginPost)
	app.Get("/logout", logoutHandler)

	// ── Push notifications (Android app) ─────────────────────────────────────
	app.Post("/api/device-token", RegisterDeviceToken)

	// ── Share ─────────────────────────────────────────────────────────────────
	app.Get("/share", shareGetHandler)
	app.Get("/share/photo", shareGetHandler)
	app.Get("/share/text", shareGetHandler)
	app.Get("/share/voice", shareGetHandler)

	// ── Post settings ─────────────────────────────────────────────────────────
	app.Get("/post-settings", postSettingsGetHandler)
	app.Post("/post-settings", postSettingsSaveHandler)
	app.Post("/post-settings/apply-existing", postSettingsApplyExistingHandler)
	app.Post("/react-comment/:type/:id", HandleCommentReaction)

	// ── DP Competition ────────────────────────────────────────────────────────
	app.Get("/dpcompete", DPCompetePage)
	app.Post("/dpcompete/create", StartDPCompetitionHandler)
	app.Post("/dpcompete/vote/:id/:side", VoteDPCompetitionHandler)
	app.Post("/dpcompete/forfeit/:id", ForfeitDPCompetitionHandler)
	app.Delete("/dpcompete/delete/:id", DeleteDPCompetitionHandler)
	app.Get("/dpcompete/section", DPCompetitionsSectionHandler)

	// ── For Me (inline) ───────────────────────────────────────────────────────
	app.Get("/forme-inline", forMeInlineHandler)

	// ── Follow ────────────────────────────────────────────────────────────────
	app.Get("/follow_list/:username", FollowListHandler)
	app.Post("/remove_follower/:username", RemoveFollowerHandler)
	app.Post("/follow/:username", FollowUser)
	app.Post("/unfollow/:username", UnfollowUser)

	// ── Main feed & posts ─────────────────────────────────────────────────────
	app.Get("/", homeGet)
	app.Post("/post", postHandler)
	app.Get("/feed", feedHandler)
	app.Get("/post/:id", postViewHandler)
	app.Delete("/post/:id", deletePostHandler)
	app.Post("/post/voice", voiceMessageHandler)
	app.Post("/video-chunk", handleVideoChunkUpload)

	// ── Trending ──────────────────────────────────────────────────────────────
	app.Get("/trending", TrendingFeedHandler)
	app.Get("/trending-leaderboard", leaderboardRedirect("stars"))
	app.Get("/feed/new-count", newPostsPeekHandler)
	app.Get("/feed/stream", sseConnectionCap, feedStreamHandler)
	app.Get("/post/fragment/:id", postFragmentHandler)

	// ── Notifications ─────────────────────────────────────────────────────────
	app.Get("/notifications/count", func(c *fiber.Ctx) error {
		currentUser := getCurrentUser(c)
		if currentUser == "" {
			return c.SendString("")
		}
		totalCount := getNotificationCount(currentUser)
		if totalCount <= 0 {
			return c.SendString("")
		}
		return c.SendString(GetMobileBadge(totalCount))
	})
	app.Get("/notifications", notificationsHandler)
	app.Get("/notifications/:id", notificationViewHandler)
	app.Get("/notifications/:id/skip", skipNotificationHandler)
	app.Get("/favicon.ico", func(c *fiber.Ctx) error { return c.SendStatus(204) })

	// ── Profile ───────────────────────────────────────────────────────────────
	app.Get("/profile/:username?", profileHandler)
	app.Get("/posts-tab/:username", postsTabHandler)
	app.Get("/posts-load-more/:username", postsLoadMoreHandler)
	app.Get("/games-tab/:username", gamesTabHandler)
	app.Get("/mehfils-tab/:username", mehfilsTabHandler)
	app.Get("/homechat-tab/:username", homeChatTabHandler)
	app.Get("/change-password", changePasswordGet)
	app.Post("/change-password", changePasswordPost)
	app.Get("/edit-profile-form", editProfileFormHandler)
	app.Post("/update-profile", updateProfileHandler)
	app.Get("/profile-info/:username?", profileInfoHandler)
	app.Post("/toggle-profile-privacy", toggleProfilePrivacyHandler)
	app.Post("/toggle-follow-approval", toggleFollowApprovalHandler)
	app.Post("/follow-request/:username", followRequestHandler)
	app.Post("/follow-request/accept/:username", acceptFollowRequestHandler)
	app.Post("/follow-request/reject/:username", rejectFollowRequestHandler)

	// ── Comments ──────────────────────────────────────────────────────────────
	app.Post("/comment/:id", postCommentHandler)
	app.Post("/comment/:id/reply", replyToCommentHandler)
	app.Get("/comment/:id/reply-form", replyFormHandler)
	app.Get("/comment/:id/reply-collapse", replyCollapseHandler)
	app.Delete("/comment/:id", deleteCommentHandler)
	app.Get("/post/:id/comments", commentsOnlyHandler)
	app.Get("/post/:id/comments-only", commentsOnlyHandler)

	log.Printf("🚀 Server starting on :3000")
	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Listen(":3000")
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil {
			log.Fatalf("server failed to start: %v", err)
		}
	case <-quit:
		log.Println("🛑 Shutting down…")
		if err := app.ShutdownWithTimeout(15 * time.Second); err != nil {
			log.Printf("shutdown error: %v", err)
		}
		runShutdownHooks()
	}
}

// sseConnectionCap limits each IP to a small number of simultaneous
// /feed/stream connections. Without this, opening N tabs holds N
// permanently-open goroutines and sockets on the server forever — the
// per-request rate limiter above never sees this as "requests" because
// it's one request that just never finishes.

const maxSSEPerIP = 2

func sseConnectionCap(c *fiber.Ctx) error {
	ip := rateLimitIP(c)
	key := "sse_conn:" + ip

	rctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	count, err := platform.RDB.Incr(rctx, key).Result()
	cancel()
	if err != nil {
		return c.Next() // fail open on Redis hiccup — don't block real users over infra issues
	}
	if count == 1 {
		expCtx, expCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		platform.RDB.Expire(expCtx, key, 2*time.Hour) // safety TTL in case decrement is ever missed
		expCancel()
	}
	if count > maxSSEPerIP {
		decrCtx, decrCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		platform.RDB.Decr(decrCtx, key)
		decrCancel()
		return c.Status(fiber.StatusTooManyRequests).SendString("Too many open connections from this network — close some tabs.")
	}

	c.Locals("sseDecrKey", key)
	return c.Next()
}

func jsVerifyHandler(c *fiber.Ctx) error {
	issueSignedJSCookie(c)
	issueCSRFCookie(c)
	c.Set("Cache-Control", "no-store")
	return c.SendStatus(fiber.StatusNoContent)
}

// =============================================================================
// REDIS INIT
// =============================================================================

func initRedis() {
	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	platform.RDB = goredis.NewClient(&goredis.Options{
		Addr:            redisAddr,
		PoolSize:        12,
		MinIdleConns:    2,
		DialTimeout:     2 * time.Second,
		ReadTimeout:     1500 * time.Millisecond,
		WriteTimeout:    1500 * time.Millisecond,
		PoolTimeout:     2 * time.Second,
		MaxRetries:      1,
		MinRetryBackoff: 50 * time.Millisecond,
		MaxRetryBackoff: 200 * time.Millisecond,
		ConnMaxIdleTime: 3 * time.Minute,
		ConnMaxLifetime: 15 * time.Minute,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := platform.RDB.Ping(ctx).Err(); err != nil {
		log.Printf("⚠️  WARNING: Redis not reachable at startup: %v", err)
		log.Printf("⚠️  App will run without caching — expect higher DB load")
	} else {
		info, _ := platform.RDB.Info(ctx, "memory").Result()
		log.Printf("✅ Redis connected. %s", redisMemorySummary(info))
	}
}

func redisMemorySummary(info string) string {
	for _, line := range splitLines(info) {
		if len(line) > 18 && line[:18] == "used_memory_human:" {
			return "Redis memory: " + line[18:]
		}
	}
	return ""
}

// IsUserBanned checks if a user is banned directly via PostgreSQL.
func IsUserBanned(username string) bool {
	if username == "" {
		return false
	}

	dbCtx, dbCancel := context.WithTimeout(platform.Ctx, 2*time.Second)
	defer dbCancel()

	var isBanned bool
	err := db.QueryRow(dbCtx, "SELECT is_banned FROM users WHERE username = $1", username).Scan(&isBanned)
	if err != nil {
		log.Printf("IsUserBanned standalone check error for %s: %v", username, err)
		return false
	}

	return isBanned
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
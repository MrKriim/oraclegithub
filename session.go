package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"mysocialmediawebsite/pkg/platform"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12
const sessionTTL = 90 * 24 * time.Hour
const sessionCookieName = "session"
const csrfCookieName = "csrf"
const localsUserKey = "auth_username"
const localsUserCached = "auth_username_cached"

var appSecret []byte

func initAppSecret() {
	s := strings.TrimSpace(os.Getenv("APP_SECRET"))
	if s == "" {
		s = strings.TrimSpace(os.Getenv("COOKIE_SECRET"))
	}
	if s == "" {
		tok, err := newSessionToken()
		if err != nil {
			log.Fatal("could not generate APP_SECRET:", err)
		}
		s = tok
		log.Println("⚠️  APP_SECRET is not set — using an ephemeral secret (JS/CSRF cookies reset on restart). Set APP_SECRET in .env for production.")
	}
	appSecret = []byte(s)
}

func cacheUsername(c *fiber.Ctx, username string) {
	c.Locals(localsUserKey, username)
	c.Locals(localsUserCached, true)
}

func cachedUsername(c *fiber.Ctx) (string, bool) {
	if c.Locals(localsUserCached) != true {
		return "", false
	}
	s, _ := c.Locals(localsUserKey).(string)
	return s, true
}

// hashPassword is the ONE place password hashes get created.
// signup and change-password both call this so they can never drift apart.
func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(h), err
}

// newSessionToken generates a cryptographically random 256-bit token. This
// is the ONLY thing that ever reaches the browser — it carries no
// information about the user, so there's nothing in it to decode, sign,
// or forge; even full knowledge of this source gives no path to
// constructing a valid one without already possessing an issued token.
func newSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken returns the SHA-256 hex digest of a token. Only this hash is
// ever stored server-side — never the raw token — so a Redis dump alone
// can't be replayed as a session; SHA-256 preimage resistance means the
// original token can't be recovered from its hash.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// getCurrentUser is the ONLY place in the codebase allowed to read the raw
// session cookie — every other handler must call this function instead.
// It resolves the opaque token against the server-side session store; a
// missing, expired, revoked, or made-up cookie value returns "" here,
// never a username.
func getCurrentUser(c *fiber.Ctx) string {
	if username, ok := cachedUsername(c); ok {
		return username
	}
	token := c.Cookies(sessionCookieName)
	username, _, ok := lookupSession(token)
	if !ok {
		cacheUsername(c, "")
		return ""
	}
	cacheUsername(c, username)
	return username
}

// createSession mints a brand-new opaque token and stores it in Redis
// alongside every other currently-active session for this username — it
// no longer revokes existing sessions. Multiple simultaneous logins (main
// tab + incognito, multiple browsers, multiple devices) are intentional:
// each login adds its own token to a per-user SET ("user_sessions:<username>")
// instead of overwriting a single tracked session. Only revokeSession
// (ban, password change, explicit "log out everywhere") removes every
// session at once; ordinary Logout should call revokeCurrentSession
// instead, which removes only the calling tab's own session.
//
// cookieDomain and cookieSecure are the package-level vars set once by
// initCookieConfig() in main.go — this is the single source of truth for
// every cookie this app sets. Do not hardcode a Domain/Secure value here
// or anywhere else; see the comment on those vars for why that silently
// breaks cookies on any host other than the exact configured domain.
func createSession(c *fiber.Ctx, username string, sv int) error {
	token, err := newSessionToken()
	if err != nil {
		return err
	}
	tokenHash := hashToken(token)

	rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pipe := platform.RDB.Pipeline()
	pipe.Set(rctx, "session:"+tokenHash, username+"|"+strconv.Itoa(sv), sessionTTL)
	pipe.SAdd(rctx, "user_sessions:"+username, tokenHash)
	pipe.Expire(rctx, "user_sessions:"+username, sessionTTL)
	if _, err := pipe.Exec(rctx); err != nil {
		return err
	}

	c.Cookie(&fiber.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Domain:   cookieDomainFor(c),
		Expires:  time.Now().Add(sessionTTL),
		MaxAge:   int(sessionTTL.Seconds()),
		HTTPOnly: true,
		Secure:   cookieSecureFor(c),
		SameSite: "Lax",
		Path:     "/",
	})
	issueCSRFCookie(c)
	cacheUsername(c, username)
	return nil
}

// lookupSession resolves a raw cookie token into (username, sv, ok). ok
// is false for an expired, revoked, or simply made-up token — all of
// which look identical to the server, which is exactly the point.
func lookupSession(token string) (string, int, bool) {
	if token == "" {
		return "", 0, false
	}
	rctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	val, err := platform.RDB.Get(rctx, "session:"+hashToken(token)).Result()
	if err != nil {
		return "", 0, false
	}
	parts := strings.SplitN(val, "|", 2)
	if len(parts) != 2 {
		return "", 0, false
	}
	sv, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, false
	}
	return parts[0], sv, true
}

// revokeSession revokes EVERY active session for this username — every
// tab, browser, and device at once. Use this for ban, password change, or
// any other "log out everywhere" action. It is NOT called on ordinary
// login/signup anymore (see createSession) — that's what allows multiple
// simultaneous sessions per account.
func revokeSession(username string) {
	rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	hashes, err := platform.RDB.SMembers(rctx, "user_sessions:"+username).Result()
	if err == nil && len(hashes) > 0 {
		keys := make([]string, len(hashes))
		for i, h := range hashes {
			keys[i] = "session:" + h
		}
		platform.RDB.Del(rctx, keys...)
	}
	platform.RDB.Del(rctx, "user_sessions:"+username)
	// Legacy single-session key from before multi-session support —
	// harmless if it's already gone, cleaned up here in case it lingers.
	platform.RDB.Del(rctx, "active_session:"+username)
}

// revokeCurrentSession removes only the ONE session tied to this exact
// token, leaving every other tab/browser/device signed into this account
// untouched. This is what a normal Logout button should call.
func revokeCurrentSession(username, token string) {
	if token == "" {
		return
	}
	tokenHash := hashToken(token)
	rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	platform.RDB.Del(rctx, "session:"+tokenHash)
	platform.RDB.SRem(rctx, "user_sessions:"+username, tokenHash)
}

// getSessionVersion reads the account's current session_version WITHOUT
// incrementing it. Ordinary login/signup should call this (not
// bumpSessionVersion) so a new session's embedded sv matches the DB and
// every other already-open session for this account stays valid.
func getSessionVersion(username string) (int, error) {
	var sv int
	err := db.QueryRow(ctx, "SELECT session_version FROM users WHERE username=$1", username).Scan(&sv)
	return sv, err
}

// bumpSessionVersion increments session_version in Postgres — instantly
// invalidating the embedded sv of EVERY session for this account, even
// ones Redis has no record of — and also revokes every live Redis
// session directly for immediate effect. Reserved for actions that must
// log the account out everywhere: password change, ban, admin action.
// Do NOT call this from ordinary login/signup — see getSessionVersion.
func bumpSessionVersion(username string) (int, error) {
	var sv int
	err := db.QueryRow(ctx,
		"UPDATE users SET session_version = session_version + 1 WHERE username=$1 RETURNING session_version",
		username).Scan(&sv)
	if err == nil {
		revokeSession(username)
		// AccountGateCheck caches the DB session_version for 30s under
		// "acct_gate:<username>" to avoid a Postgres round trip on every
		// request. That cache going stale for up to 30s after a bump is
		// what previously caused "login?error=session_expired" right
		// after a fresh login: the new session token carries the NEW
		// sv, but AccountGateCheck was still comparing it against the
		// OLD cached sv until the cache naturally expired. Deleting it
		// here forces the very next request to read the current value
		// straight from Postgres.
		delCtx, delCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		platform.RDB.Del(delCtx, "acct_gate:"+username)
		delCancel()
	}
	return sv, err
}

// requireUser returns the logged-in username from the Redis session.
// It never trusts a client-supplied cookie value as an identity.
func requireUser(c *fiber.Ctx) (string, error) {
	username := getCurrentUser(c)
	if username != "" {
		return username, nil
	}
	if isHtmxRequest(c) {
		c.Set("HX-Redirect", "/login")
		return "", c.Status(fiber.StatusUnauthorized).SendString("Login required")
	}
	accept := c.Get("Accept")
	if strings.Contains(accept, "application/json") {
		return "", c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "login required"})
	}
	return "", c.Redirect("/login")
}

func requireAdmin(c *fiber.Ctx) (string, error) {
	username, err := requireUser(c)
	if err != nil {
		return "", err
	}
	if !isAdmin(username) {
		return "", c.Status(fiber.StatusForbidden).SendString("Unauthorized")
	}
	return username, nil
}

func signValue(raw string) string {
	mac := hmac.New(sha256.New, appSecret)
	mac.Write([]byte(raw))
	return raw + "." + hex.EncodeToString(mac.Sum(nil))
}

func verifySignedValue(val string) bool {
	if val == "" || val == "1" || !strings.Contains(val, ".") {
		return false
	}
	raw, sig, ok := strings.Cut(val, ".")
	if !ok || raw == "" || sig == "" {
		return false
	}
	mac := hmac.New(sha256.New, appSecret)
	mac.Write([]byte(raw))
	expected := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(sig)) == 1
}

func issueSignedJSCookie(c *fiber.Ctx) {
	nonce, err := newSessionToken()
	if err != nil {
		return
	}
	c.Cookie(&fiber.Cookie{
		Name:     jsCookieName,
		Value:    signValue(nonce),
		Domain:   cookieDomainFor(c),
		MaxAge:   7 * 24 * 3600,
		HTTPOnly: true,
		Secure:   cookieSecureFor(c),
		SameSite: "Lax",
		Path:     "/",
	})
}

func issueCSRFCookie(c *fiber.Ctx) string {
	if existing := c.Cookies(csrfCookieName); len(existing) >= 22 {
		return existing
	}
	tok, err := newSessionToken()
	if err != nil {
		return ""
	}
	c.Cookie(&fiber.Cookie{
		Name:     csrfCookieName,
		Value:    tok,
		Domain:   cookieDomainFor(c),
		MaxAge:   int(sessionTTL.Seconds()),
		HTTPOnly: false,
		Secure:   cookieSecureFor(c),
		SameSite: "Lax",
		Path:     "/",
	})
	return tok
}

func CSRFProtect(c *fiber.Ctx) error {
	path := c.Path()
	if jsCheckExempt(path) || path == "/js-ok" {
		return c.Next()
	}
	switch c.Method() {
	case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
		issueCSRFCookie(c)
		return c.Next()
	}
	cookie := c.Cookies(csrfCookieName)
	header := c.Get("X-CSRF-Token")
	if header == "" {
		header = c.FormValue("csrf_token")
	}
	if len(cookie) < 22 || subtle.ConstantTimeCompare([]byte(cookie), []byte(header)) != 1 {
		if isHtmxRequest(c) {
			return c.Status(fiber.StatusForbidden).SendString("Session expired. Refresh the page and try again.")
		}
		return c.Status(fiber.StatusForbidden).SendString("Invalid request. Refresh the page and try again.")
	}
	return c.Next()
}
package main

import (
	"context"
	"mysocialmediawebsite/pkg/platform"
	"time"

	"github.com/gofiber/fiber/v2"
)

func logoutHandler(c *fiber.Ctx) error {
	username := getCurrentUser(c)
	if username != "" {
		// Grab the raw token BEFORE clearUserCookie wipes it, so we can
		// revoke exactly this tab's session. revokeSession(username)
		// would log the account out on every tab/browser/device, not
		// just this one — revokeCurrentSession only removes the token
		// this request actually carried, leaving other open sessions
		// for this account untouched.
		token := c.Cookies(sessionCookieName)
		revokeCurrentSession(username, token)

		redisCtx, redisCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer redisCancel()

		// active_sessions is presence/online-status tracking (used by
		// getOnlineUsers()/isUserOnline() in home.go), unrelated to
		// auth. With multi-session support it's no longer safe to
		// remove unconditionally here — if the same user is still
		// logged in on another tab, this would wrongly mark them
		// offline everywhere just because one tab logged out. Entries
		// there already age out naturally after 300s of inactivity
		// (see getOnlineUsers()'s own threshold), so it's fine to just
		// let it expire instead of force-clearing it on every logout.
		platform.RDB.Del(redisCtx, "notif_count:"+username)
		platform.RDB.Del(redisCtx, "ban:"+username)
		platform.RDB.Del(redisCtx, "acct_gate:"+username)
	}
	clearUserCookie(c)
	c.Cookie(&fiber.Cookie{Name: csrfCookieName, Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, SameSite: "Lax"})
	c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Set("Pragma", "no-cache")
	c.Set("Expires", "0")
	return c.Redirect("/login")
}
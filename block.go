package main

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
)

// isTopThreeScorer reports whether username currently holds rank 1, 2, or 3
// on the site-wide leaderboard (admins are excluded from ranking, same as
// everywhere else in the app). Top-3 users are protected — nobody else can
// block them.
func isTopThreeScorer(username string) bool {
	if username == "" {
		return false
	}
	var rank int64
	err := db.QueryRow(ctx, `
		SELECT r.rank FROM (
			SELECT username, RANK() OVER (ORDER BY points DESC) as rank
			FROM users
			WHERE COALESCE(user_type, '') != 'admin' AND LOWER(username) != 'heera'
		) r WHERE r.username = $1`, username).Scan(&rank)
	if err != nil {
		return false
	}
	return rank >= 1 && rank <= 3
}

// BlockToggleHandler toggles the block status between two users
func BlockToggleHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	targetUser := c.Params("username")
	if currentUser == "" || targetUser == "" {
		return c.Status(401).SendString("Unauthorized")
	}
	if currentUser == targetUser {
		return c.Status(400).SendString("You cannot block yourself")
	}

	// 1. SECURITY CHECK: Check if the target user is an Admin
	var targetRole string
	err := db.QueryRow(ctx, "SELECT COALESCE(user_type, 'user') FROM users WHERE username = $1", targetUser).Scan(&targetRole)
	if err != nil {
		return c.Status(500).SendString("Database Error")
	}
	if targetRole == "admin" {
		return c.SendString(`
            <script>alert("❌ Admin cannot be blocked.");</script>
            <div hx-swap-oob="true" id="block-btn-area">
                ` + GetBlockButton(targetUser, false) + `
            </div>
        `)
	}

	// 2. Check if *I* have blocked the target. Direction matters: only the
	// row where blocker = me represents a block I created, and only that
	// row should ever be removable by me.
	var blockedByMe bool
	err = db.QueryRow(ctx, `
        SELECT EXISTS(
            SELECT 1 FROM blocks
            WHERE blocker = $1 AND blocked = $2
        )`, currentUser, targetUser).Scan(&blockedByMe)
	if err != nil {
		return c.Status(500).SendString("Database Error")
	}

	if blockedByMe {
		// === UNBLOCK ===
		// Only remove the row I own. If the other user separately blocked
		// me, that row belongs to them and is left untouched.
		_, err = db.Exec(ctx, `
            DELETE FROM blocks
            WHERE blocker = $1 AND blocked = $2`,
			currentUser, targetUser)
		if err != nil {
			return c.Status(500).SendString("Failed to unblock")
		}
		return c.SendString(`
            <div hx-swap-oob="true" id="block-btn-area">
                ` + GetBlockButton(targetUser, false) + `
            </div>
            <div hx-swap-oob="true" id="chat-btn-area">
                ` + RenderChatButton(currentUser, targetUser) + `
            </div>
        `)
	} else {
		// === BLOCK ===
		// Record only my block. The reverse direction is not created here —
		// it belongs solely to the other user, if they ever block back.
		_, err = db.Exec(ctx, `
            INSERT INTO blocks (blocker, blocked) VALUES ($1, $2)
            ON CONFLICT DO NOTHING`,
			currentUser, targetUser)
		if err != nil {
			return c.Status(500).SendString("Failed to block")
		}
		return c.SendString(`
            <div hx-swap-oob="true" id="block-btn-area">
                ` + GetBlockButton(targetUser, true) + `
            </div>
            <div hx-swap-oob="true" id="chat-btn-area">
                ` + RenderChatButton(currentUser, targetUser) + `
            </div>
        `)
	}
}

// GetBlockButton returns the Block/Unblock button
func GetBlockButton(username string, isBlocked bool) string {
	if isBlocked {
		return fmt.Sprintf(`
            <button hx-post="/block-toggle/%s"
                    hx-swap="outerHTML"
                    class="text-[10px] bg-gray-200 dark:bg-gray-700 text-gray-700 dark:text-gray-200 px-2 py-1 rounded-lg font-bold cursor-pointer active:scale-95 transition-transform">
                🔓 Unblock
            </button>`, username)
	}
	return fmt.Sprintf(`
        <button hx-post="/block-toggle/%s"
                hx-swap="outerHTML"
                hx-confirm="Are you sure? You won't see their interactions."
                class="text-[10px] bg-red-50 dark:bg-red-900/20 text-red-600 px-2 py-1 rounded-lg font-bold cursor-pointer active:scale-95 transition-transform">
            🚫 Block
        </button>`, username)
}

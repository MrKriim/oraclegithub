package main

import (
	"log"

	"github.com/gofiber/fiber/v2"
)

// adminToggleBanHandler handles the banning and unbanning of users.
func adminToggleBanHandler(c *fiber.Ctx) error {
	adminUser, err := requireAdmin(c)
	if err != nil {
		return err
	}
	_ = adminUser
	targetUser := c.Params("username")

	// 2. Toggle Ban and Reset Points
	// We set points to 0 and is_banned to true
	_, err = db.Exec(ctx, `
        UPDATE users 
        SET is_banned = NOT is_banned, 
            points = CASE WHEN is_banned = FALSE THEN 0 ELSE points END 
        WHERE username = $1`, targetUser)

	if err != nil {
		return c.SendString("Error updating user status")
	}

	// 3. Delete all content if we just banned them
	var isNowBanned bool
	db.QueryRow(ctx, "SELECT is_banned FROM users WHERE username = $1", targetUser).Scan(&isNowBanned)

	if isNowBanned {
		// Delete posts, comments, and notifications
		// This cleans up the "4444555" spam posts automatically
		db.Exec(ctx, "DELETE FROM posts WHERE author = $1", targetUser)
		db.Exec(ctx, "DELETE FROM comments WHERE author = $1", targetUser)
		db.Exec(ctx, "DELETE FROM notifications WHERE sender = $1 OR recipient = $1", targetUser)
		db.Exec(ctx, "DELETE FROM messages WHERE sender = $1 OR recipient = $1", targetUser)

		// Remove from Redis (Kick them out of active sessions)
		revokeSession(targetUser)
		rdb.Del(ctx, "acct_gate:"+targetUser)
		rdb.Del(ctx, BanCacheKey(targetUser))
	}

	return profileInfoHandler(c)
}

func AuthMiddleware(c *fiber.Ctx) error {
	return AccountGateCheck(c)
}

// adminSetPointsHandler allows admins to manually override a user's points/score.
func adminSetPointsHandler(c *fiber.Ctx) error {
	adminUser, err := requireAdmin(c)
	if err != nil {
		return err
	}
	targetUser := c.Params("username")
	newPoints := c.FormValue("points")

	// 2. Update the score in the database
	_, err = db.Exec(ctx, "UPDATE users SET points = $1 WHERE username = $2", newPoints, targetUser)
	if err != nil {
		log.Printf("CRITICAL: Point update error for user %s: %v", targetUser, err)
		return c.SendString("Database error: Could not update points.")
	}

	log.Printf("ADMIN ACTION: %s set points for %s to %s", adminUser, targetUser, newPoints)

	// 3. Return refreshed info
	return profileInfoHandler(c)
}

package main

import (
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
)

func UnfollowUser(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	targetUser := c.Params("username")
	postID := c.Query("post_id")

	if currentUser == "" {
		return c.SendStatus(fiber.StatusUnauthorized)
	}

	// 1. Delete follow relationship
	_, err := db.Exec(ctx, `
        DELETE FROM followers 
        WHERE follower = $1 AND followee = $2`, currentUser, targetUser)
	if err != nil {
		log.Println("Error unfollowing user:", err)
		return c.SendStatus(fiber.StatusInternalServerError)
	}

	// 2. Cleanup the Notification
	// Removes the notification so the target user doesn't see a follow alert from someone who already unfollowed
	_, err = db.Exec(ctx, `
        DELETE FROM notifications 
        WHERE recipient = $1 AND sender = $2 AND type = 'follow'`, targetUser, currentUser)
	if err != nil {
		log.Println("Error deleting follow notification:", err)
	}

	// 3. UI Toggle Logic — mirrors FollowUser: if this unfollow came from a post
	// card, re-render the FULL post so the button and its surrounding markup
	// stay in sync. Without this branch, unfollowing from a post replaced the
	// entire post's outerHTML with profile-page follow-section markup instead.
	if postID != "" {
		var id, dpCompID int
		var author, content, imageUrl, category, voiceUrl string
		var createdAt time.Time
		var expiresAt *time.Time
		var points int
		var allowJ, allowC, allowComments, followersOnly bool

		err = db.QueryRow(ctx, `
            SELECT p.id, p.author, p.content, p.created_at, u.points, 
                   p.allow_jhappi, p.allow_chupair, 
                   COALESCE(p.image_url, ''), COALESCE(p.category, 'General'), COALESCE(p.voice_url, ''),
                   COALESCE(p.allow_comments, true), COALESCE(p.followers_only, false), p.expires_at,
                   COALESCE(p.dp_competition_id, 0)
            FROM posts p 
            JOIN users u ON p.author = u.username 
            WHERE p.id = $1`, postID).Scan(
			&id, &author, &content, &createdAt, &points,
			&allowJ, &allowC, &imageUrl, &category, &voiceUrl,
			&allowComments, &followersOnly, &expiresAt, &dpCompID,
		)

		if err != nil {
			log.Println("Error fetching post for re-render:", err)
			return c.SendString("Error updating post")
		}

		return c.SendString(renderPostHTML(
			id,
			author,
			content,
			createdAt,
			currentUser,
			points,
			allowJ,
			allowC,
			imageUrl,
			category,
			voiceUrl,
			allowComments,
			followersOnly,
			expiresAt,
			dpCompID,
		))
	}

	// Default: Return the updated Follow/Unfollow button and count (profile page context)
	followersCount, followingCount := getFollowCounts(targetUser)
	return c.SendString(RenderFollowSection(currentUser, targetUser, followersCount, followingCount))
}

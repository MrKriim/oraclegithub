package main

import (
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
)

func FollowUser(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	targetUser := c.Params("username")
	postID := c.Query("post_id")

	if currentUser == "" {
		return c.SendStatus(fiber.StatusUnauthorized)
	}

	if currentUser == targetUser {
		return c.SendStatus(fiber.StatusBadRequest)
	}

	// 1. Insert follow relationship
	_, err := db.Exec(ctx, `
        INSERT INTO followers (follower, followee) 
        VALUES ($1, $2) 
        ON CONFLICT DO NOTHING`, currentUser, targetUser)

	if err != nil {
		log.Println("Error following user:", err)
		return c.SendStatus(fiber.StatusInternalServerError)
	}

	// 2. Create the Notification
	_, err = db.Exec(ctx, `
        INSERT INTO notifications (recipient, sender, type, content)
        SELECT $1, $2, 'follow', 'started following you'
        WHERE NOT EXISTS (
            SELECT 1 FROM notifications 
            WHERE recipient = $1 AND sender = $2 AND type = 'follow'
        )`, targetUser, currentUser)

	if err != nil {
		log.Println("Error creating follow notification:", err)
	}

	Push(targetUser, currentUser, "started following you", profileLink(currentUser))

	// 3. UI Toggle Logic
	if postID != "" {
		var id, dpCompID int
		var author, content, imageUrl, category, voiceUrl string
		var createdAt time.Time
		var points int
		var allowJ, allowC bool

		err = db.QueryRow(ctx, `
            SELECT p.id, p.author, p.content, p.created_at, u.points, 
                   p.allow_jhappi, p.allow_chupair, 
                   COALESCE(p.image_url, ''), COALESCE(p.category, 'General'), COALESCE(p.voice_url, ''),
                   COALESCE(p.dp_competition_id, 0)
            FROM posts p 
            JOIN users u ON p.author = u.username 
            WHERE p.id = $1`, postID).Scan(
			&id, &author, &content, &createdAt, &points,
			&allowJ, &allowC, &imageUrl, &category, &voiceUrl, &dpCompID,
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
			true,
			false,
			nil,
			dpCompID,
		))

	}

	// Default: Return the profile stats section (for the profile page)
	followersCount, followingCount := getFollowCounts(targetUser)
	return c.SendString(RenderFollowSection(currentUser, targetUser, followersCount, followingCount))
}

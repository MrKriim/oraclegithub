package main

import (
    "strconv"
    "time"
    "mysocialmediawebsite/pkg/platform"
    "github.com/gofiber/fiber/v2"
)

const postCooldown = 1 * time.Second

func postHandler(c *fiber.Ctx) error {
	author := c.Cookies("user")
	content := c.FormValue("content")

	if author == "" {
		return c.SendStatus(fiber.StatusUnauthorized)
	}
	if content == "" {
		return c.SendStatus(fiber.StatusBadRequest)
	}

	now := time.Now().UTC()

	// Begin transaction
	tx, err := db.Begin(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("internal error")
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Acquire advisory lock to avoid races
	_, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", author)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("internal error")
	}

	// Check last post time
	var lastCreatedAt time.Time
	err = tx.QueryRow(ctx, "SELECT created_at FROM posts WHERE author = $1 ORDER BY created_at DESC LIMIT 1", author).Scan(&lastCreatedAt)
	if err != nil && err.Error() != "no rows in result set" {
		return c.Status(fiber.StatusInternalServerError).SendString("internal error")
	}

	if !lastCreatedAt.IsZero() {
		elapsed := now.Sub(lastCreatedAt)
		if elapsed < postCooldown {
			remaining := int((postCooldown - elapsed).Seconds())
			if remaining < 1 {
				remaining = 1
			}
			c.Set("Retry-After", strconv.Itoa(remaining))
			return c.Status(fiber.StatusTooManyRequests).SendString("please wait before creating another post")
		}
	}

	// Insert post
	_, err = tx.Exec(ctx, "INSERT INTO posts (content, author, created_at) VALUES ($1, $2, $3)", content, author, now)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("failed to insert post")
	}

	// Increase points
	_, err = tx.Exec(ctx, "UPDATE users SET points = COALESCE(points, 0) + 1 WHERE username = $1", author)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("failed to update points")
	}

	if err := tx.Commit(ctx); err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("failed to commit")
	}

	// --- REDIS CACHE INVALIDATION (Real-Time Fix) ---
	// We run this in a goroutine so the user doesn't wait for the cleanup
	go func() {
		// Use SCAN to find all feed keys and delete them
		// This ensures all users see the new post on their next feed load
		iter := platform.RDB.Scan(platform.Ctx, 0, "feed:u:*", 0).Iterator()
		for iter.Next(platform.Ctx) {
			platform.RDB.Del(platform.Ctx, iter.Val())
		}
	}()

	// Fetch new points for the UI
	var newPoints int
	err = db.QueryRow(ctx, "SELECT COALESCE(points, 0) FROM users WHERE username = $1", author).Scan(&newPoints)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("internal error")
	}

	newPostHTML := renderPostHTML(0, author, content, now, author, newPoints)
	return c.SendString(newPostHTML)
}
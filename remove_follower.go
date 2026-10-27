package main

import (
    "log"
    "github.com/gofiber/fiber/v2"
)

func RemoveFollowerHandler(c *fiber.Ctx) error {
    currentUser := getCurrentUser(c)
    followerToRemove := c.Params("username") // The person being kicked out

    if currentUser == "" {
        return c.SendStatus(fiber.StatusUnauthorized)
    }

    // Delete the relationship where:
    // follower = the person to remove
    // followee = the current logged-in user (the owner)
    _, err := db.Exec(ctx, `
        DELETE FROM followers 
        WHERE follower = $1 AND followee = $2`, followerToRemove, currentUser)

    if err != nil {
        log.Println("Error removing follower:", err)
        return c.SendStatus(fiber.StatusInternalServerError)
    }

    // Return empty string (HTMX hx-swap="outerHTML" will remove the row from the list)
    return c.SendString("")
}
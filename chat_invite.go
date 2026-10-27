package main

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

func SendChatInviteHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	targetUser := strings.ToLower(strings.TrimSpace(c.Params("username")))
	c.Set("Content-Type", "text/html; charset=utf-8")

	if currentUser == "" || strings.EqualFold(currentUser, targetUser) {
		return c.Status(400).SendString("Invalid request")
	}

	// 1. Fetch User Status and Type
	var isBanned bool
	var userType string
	err := db.QueryRow(ctx, "SELECT is_banned, COALESCE(user_type, 'user') FROM users WHERE username = $1", currentUser).Scan(&isBanned, &userType)
	if err != nil {
		return c.Status(500).SendString("Error checking user status")
	}

	// 2. Ban Guard
	if isBanned {
		return c.SendString(`<span class="text-xs text-red-500 font-bold">🚫 Banned users cannot send invites</span>`)
	}

	// 3. ADMIN AUTO-ACCEPT LOGIC
	if userType == "admin" {
		// Admins create an 'accepted' status immediately
		_, err = db.Exec(ctx, `
            INSERT INTO chat_invites (sender, recipient, status) 
            VALUES ($1, $2, 'accepted') 
            ON CONFLICT (sender, recipient) 
            DO UPDATE SET status = 'accepted'`,
			currentUser, targetUser)

		if err != nil {
			return c.Status(500).SendString("Error creating chat")
		}

		// Notify the user that an Admin started a chat
		db.Exec(ctx, `
            INSERT INTO notifications (recipient, sender, type, content)
            VALUES ($1, $2, 'chat_invite', 'started a direct chat with you')`,
			targetUser, currentUser)

		// Use HTMX to redirect the admin directly to the active chat room
		Push(targetUser, currentUser, "started a direct chat with you", siteBase+"/chat/"+currentUser)
		c.Set("HX-Redirect", "/chat/view/"+targetUser)
		return c.SendString(`<span class="text-xs text-emerald-500 font-bold">🚀 Direct Chat Created...</span>`)
	}

	// 4. NORMAL USER: Process as Pending Invite
	_, err = db.Exec(ctx, `
        INSERT INTO chat_invites (sender, recipient, status) 
        VALUES ($1, $2, 'pending') 
        ON CONFLICT (sender, recipient) DO NOTHING`,
		currentUser, targetUser)

	if err != nil {
		return c.Status(500).SendString("Error sending invite")
	}

	// 5. Send the system notification for regular users
	db.Exec(ctx, `
        INSERT INTO notifications (recipient, sender, type, content)
        VALUES ($1, $2, 'chat_invite', 'sent you a 1-on-1 chat invite')`,
		targetUser, currentUser)

	Push(targetUser, currentUser, "sent you a 1-on-1 chat invite", siteBase+"/invites")

	c.Set("Content-Type", "text/html")
	return c.SendString(`<span class="text-xs text-emerald-500 font-bold">📩 Invite Sent!</span>`)
}

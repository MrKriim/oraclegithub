package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/gofiber/fiber/v2"
)

func FollowListHandler(c *fiber.Ctx) error {
	targetUser := c.Params("username")
	listType := c.Query("type") // "followers" or "following"
	currentUser := getCurrentUser(c)

	// Check if the person viewing the list is the owner of this profile
	isOwner := (currentUser == targetUser && currentUser != "")

	// Joined against users so the real profile picture + points show up
	// next to each name via renderUserAvatarRow, instead of a plain
	// letter-initial placeholder — same row component already used by
	// the Blocked and Follow Requests tabs, so every user list in the
	// app now looks and behaves the same way.
	var query string
	if listType == "followers" {
		query = `
			SELECT u.username, COALESCE(u.profile_image,''), COALESCE(u.points,0)
			FROM followers f
			JOIN users u ON u.username = f.follower
			WHERE f.followee = $1
			ORDER BY f.created_at DESC`
	} else {
		query = `
			SELECT u.username, COALESCE(u.profile_image,''), COALESCE(u.points,0)
			FROM followers f
			JOIN users u ON u.username = f.followee
			WHERE f.follower = $1
			ORDER BY f.created_at DESC`
	}

	rows, err := db.Query(ctx, query, targetUser)
	if err != nil {
		log.Println("Error fetching list:", err)
		return c.SendString(`<p style="color:#ef4444;font-size:13px;text-align:center;padding:20px;">Error loading list</p>`)
	}
	defer rows.Close()

	var rowsHTML strings.Builder
	count := 0

	for rows.Next() {
		var username, profileImage string
		var points int
		if err := rows.Scan(&username, &profileImage, &points); err != nil {
			continue
		}
		count++

		// Remove button ONLY if viewer is the owner looking at their own followers.
		removeBtn := ""
		if isOwner && listType == "followers" {
			removeBtn = fmt.Sprintf(`
				<button hx-post="/remove_follower/%[1]s" hx-target="#follow-row-%[1]s" hx-swap="outerHTML"
				        hx-confirm="Remove %[1]s from your followers?"
				        onmouseover="this.style.background='#fee2e2';this.style.color='#dc2626'"
				        onmouseout="this.style.background='#f3f4f6';this.style.color='#374151'"
				        onfocus="this.style.outline='2px solid #111827';this.style.outlineOffset='2px'" onblur="this.style.outline='none'"
				        style="flex-shrink:0;padding:8px 14px;border-radius:10px;font-weight:800;font-size:11px;text-transform:uppercase;letter-spacing:0.03em;background:#f3f4f6;color:#374151;border:none;cursor:pointer;transition:background .15s,color .15s;">
					Remove
				</button>`, username)
		}

		rowsHTML.WriteString(renderUserAvatarRow("follow-row-"+username, username, profileImage, points, removeBtn))
	}

	if count == 0 {
		emptyLabel := "No followers yet."
		if listType != "followers" {
			emptyLabel = "Not following anyone yet."
		}
		return c.SendString(fmt.Sprintf(`
		<div style="text-align:center;padding:48px 20px;color:#6b7280;">
			<div style="font-size:44px;margin-bottom:12px;">👥</div>
			<p style="font-size:13px;font-style:italic;">%s</p>
		</div>`, emptyLabel))
	}

	// No outer card/header/close-button wrapper anymore — this renders
	// straight into the tab panel (#tab-followers / #tab-following),
	// which already provides its own spacing, so the list starts right
	// at the top instead of sitting inside a nested floating card.
	return c.SendString(fmt.Sprintf(`
	<div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(230px,1fr));gap:10px;">
		%s
	</div>`, rowsHTML.String()))
}
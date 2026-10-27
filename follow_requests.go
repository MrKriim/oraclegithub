package main

import (
	"fmt"
	"html"
	"time"

	"github.com/gofiber/fiber/v2"
)

// ─────────────────────────────────────────────────────────────────────────────
// FOLLOW REQUESTS — "Request to Follow" privacy feature
// ─────────────────────────────────────────────────────────────────────────────
// When a user turns ON "Request to Follow" in Profile Settings, anyone who
// taps Follow on their profile sends a pending request instead of following
// directly. The owner sees a "Follow Requests (N)" button on their own
// profile; tapping it lists every pending request with Accept / Reject.
//
//   Accept -> creates the real follower row, deletes the request row,
//             notifies the requester: "follow request accepted"
//   Reject -> marks the request rejected with a timestamp, notifies the
//             requester: "follow request rejected", and blocks that same
//             requester from sending another request to the same target
//             for 24 hours.
//
// Routes to register alongside your other routes:
//   app.Post("/toggle-follow-approval",       toggleFollowApprovalHandler)
//   app.Get ("/profile-settings-panel",       profileSettingsPanelHandler)
//   app.Post("/follow-request/:username",     followRequestHandler)
//   app.Get ("/follow-requests",              followRequestsListHandler)
//   app.Post("/follow-request/:id/accept",    acceptFollowRequestHandler)
//   app.Post("/follow-request/:id/reject",    rejectFollowRequestHandler)
// ─────────────────────────────────────────────────────────────────────────────

const followRequestCooldown = 24 * time.Hour

// ── toggleFollowApprovalHandler ─────────────────────────────────────────────
// Flips the current user's "Request to Follow" setting on/off and re-renders
// the Profile Settings panel.


// ── profileSettingsPanelHandler ─────────────────────────────────────────────
// GET /profile-settings-panel — lazy-loaded panel shown from the "⚙️ Profile
// Settings" button on the owner's own profile.


// renderProfileSettingsPanel draws the toggle + the "Follow Requests (N)"
// entry point. Kept intentionally small — the request list itself is lazy
// loaded only when the button is tapped.


// ── followRequestHandler ────────────────────────────────────────────────────
// POST /follow-request/:username — called instead of the plain /follow route
// whenever the target has "Request to Follow" turned on. Handles: brand-new
// request, an already-pending request (no-op), a rejection still inside its
// 24h cooldown (blocked), and a rejection whose cooldown has expired (allowed
// to re-request).
func followRequestHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	target := c.Params("username")
	if currentUser == "" || currentUser == target {
		return c.SendStatus(400)
	}

	var requiresApproval bool
	if err := db.QueryRow(ctx, "SELECT COALESCE(require_follow_approval,false) FROM users WHERE username=$1", target).
		Scan(&requiresApproval); err != nil {
		return c.SendStatus(404)
	}

	// Target doesn't require approval — follow directly, same as a normal follow.
	if !requiresApproval {
		db.Exec(ctx, "INSERT INTO followers (follower, followee) VALUES ($1,$2) ON CONFLICT DO NOTHING", currentUser, target)
		Push(target, currentUser, "started following you", profileLink(currentUser))
		followersCount, followingCount := getFollowCounts(target)
		return c.SendString(RenderFollowSection(currentUser, target, followersCount, followingCount))
	}

	var status string
	var resolvedAt *time.Time
	err := db.QueryRow(ctx, `SELECT status, resolved_at FROM follow_requests WHERE requester=$1 AND target=$2`,
		currentUser, target).Scan(&status, &resolvedAt)
	notify := false
	switch {
	case err == nil && status == "pending":
		// Already requested — nothing to do.
	case err == nil && status == "rejected" && resolvedAt != nil && time.Since(*resolvedAt) < followRequestCooldown:
		// Still inside the 24h cooldown — blocked, no new request.
	case err == nil:
		db.Exec(ctx, `UPDATE follow_requests SET status='pending', created_at=NOW(), resolved_at=NULL WHERE requester=$1 AND target=$2`,
			currentUser, target)
		db.Exec(ctx, `INSERT INTO notifications (recipient, sender, type, content) VALUES ($1,$2,'follow_request',$3)`,
			target, currentUser, fmt.Sprintf("@%s wants to follow you", currentUser))
		notify = true
	default:
		db.Exec(ctx, `INSERT INTO follow_requests (requester, target, status) VALUES ($1,$2,'pending')`, currentUser, target)
		db.Exec(ctx, `INSERT INTO notifications (recipient, sender, type, content) VALUES ($1,$2,'follow_request',$3)`,
			target, currentUser, fmt.Sprintf("@%s wants to follow you", currentUser))
		notify = true
	}
	if notify {
		Push(target, currentUser, "wants to follow you", profileLink(currentUser))
	}

	followersCount, followingCount := getFollowCounts(target)
	return c.SendString(RenderFollowSection(currentUser, target, followersCount, followingCount))
}

// ── followRequestsListHandler ───────────────────────────────────────────────
// GET /follow-requests — every pending request for the current user, each
// with Accept / Reject buttons.




// ── acceptFollowRequestHandler ──────────────────────────────────────────────
// POST /follow-request/:id/accept — creates the real follower relationship,
// removes the request row, and notifies the requester.
func acceptFollowRequestHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.SendStatus(401)
	}
	requester := c.Params("username")

	res, err := db.Exec(ctx, `DELETE FROM follow_requests WHERE requester=$1 AND target=$2 AND status='pending'`, requester, currentUser)
	if err != nil || res.RowsAffected() == 0 {
		return c.SendStatus(404)
	}

	db.Exec(ctx, `INSERT INTO followers (follower, followee) VALUES ($1,$2) ON CONFLICT DO NOTHING`, requester, currentUser)
	db.Exec(ctx, `INSERT INTO notifications (recipient, sender, type, content) VALUES ($1,$2,'follow_request_accepted',$3)`,
		requester, currentUser, fmt.Sprintf("@%s accepted your follow request", html.EscapeString(currentUser)))
	Push(requester, currentUser, "accepted your follow request", profileLink(currentUser))

	return c.SendStatus(200)
}

// ── rejectFollowRequestHandler ──────────────────────────────────────────────
// POST /follow-request/:id/reject — marks the request rejected (starting the
// 24h cooldown), and notifies the requester. The row is kept (not deleted)
// so followRequestHandler can enforce the cooldown on a future attempt.
func rejectFollowRequestHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.SendStatus(401)
	}
	requester := c.Params("username")

	res, err := db.Exec(ctx, `UPDATE follow_requests SET status='rejected', resolved_at=NOW() WHERE requester=$1 AND target=$2 AND status='pending'`,
		requester, currentUser)
	if err != nil || res.RowsAffected() == 0 {
		return c.SendStatus(404)
	}

	db.Exec(ctx, `INSERT INTO notifications (recipient, sender, type, content) VALUES ($1,$2,'follow_request_rejected',$3)`,
		requester, currentUser, fmt.Sprintf("@%s declined your follow request", html.EscapeString(currentUser)))
	Push(requester, currentUser, "declined your follow request", profileLink(currentUser))

	return c.SendStatus(200)
}

package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// SendMehfilInvite — admin invites a user by username (typed via HX-Prompt,
// same pattern as broadcast/mute/topic-update elsewhere in mehfil.go).
func SendMehfilInvite(c *fiber.Ctx) error {
	mehfilID := c.Params("id")
	currentUser, err := requireUser(c)
	if err != nil {
		return err
	}
	target := strings.TrimSpace(c.Get("HX-Prompt"))

	var adminName, topic string
	if err := db.QueryRow(ctx, "SELECT admin_username, topic FROM mehfils WHERE id=$1", mehfilID).
		Scan(&adminName, &topic); err != nil || currentUser != adminName {
		return c.Status(403).SendString("Sirf admin invite bhej sakta hai")
	}
	if target == "" || target == currentUser {
		return c.Status(400).SendString("Sahi username likhein")
	}

	var exists bool
	db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE username=$1)", target).Scan(&exists)
	if !exists {
		return c.Status(404).SendString("User nahi mila")
	}

	var alreadyMember bool
	db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM mehfil_members WHERE mehfil_id=$1 AND username=$2)",
		mehfilID, target).Scan(&alreadyMember)
	if alreadyMember {
		return c.Status(400).SendString("Yeh pehle hi Mehfil mein hai")
	}

	var status string
	var rejectedAt *time.Time
	db.QueryRow(ctx, "SELECT status, rejected_at FROM mehfil_invites WHERE mehfil_id=$1 AND invitee=$2",
		mehfilID, target).Scan(&status, &rejectedAt)
	if status == "rejected" && rejectedAt != nil && time.Since(*rejectedAt) < 24*time.Hour {
		left := int((24*time.Hour - time.Since(*rejectedAt)).Hours()) + 1
		return c.Status(429).SendString(fmt.Sprintf("Is user ne reject ki thi — %dh baad dubara koshish karein", left))
	}

	if _, err := db.Exec(ctx, `
		INSERT INTO mehfil_invites (mehfil_id, inviter, invitee, status, rejected_at)
		VALUES ($1,$2,$3,'pending',NULL)
		ON CONFLICT (mehfil_id, invitee)
		DO UPDATE SET status='pending', rejected_at=NULL, inviter=$2, created_at=NOW()`,
		mehfilID, currentUser, target); err != nil {
		return c.Status(500).SendString("Invite bhejne mein masla hua")
	}

	_, _ = db.Exec(ctx, `
		INSERT INTO notifications (recipient,sender,type,content,mehfil_id,is_read,created_at)
		VALUES ($1,$2,'mehfil_invite',$3,$4,false,NOW())`,
		target, currentUser, fmt.Sprintf("@%s ne apko '%s' Mehfil mein bulaya hai", currentUser, topic), mehfilID)

	Push(target, currentUser, fmt.Sprintf("ne apko '%s' Mehfil mein bulaya hai", topic), siteBase+"/notifications")

	return c.SendString(fmt.Sprintf(`<div class="mf-member-row" style="opacity:.6;"><span style="font-size:12px;">✅</span><div class="mf-member-name">@%s ko invite bhej di gayi</div></div>`, target))
}

// RespondMehfilInvite — accept joins + redirects into the mehfil; reject
// stamps rejected_at so SendMehfilInvite blocks re-inviting for 24h.
func RespondMehfilInvite(c *fiber.Ctx) error {
	mehfilID := c.Params("id")
	action := c.Params("action") // "accept" | "reject"
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Redirect("/signup")
	}

	var status string
	if err := db.QueryRow(ctx, "SELECT status FROM mehfil_invites WHERE mehfil_id=$1 AND invitee=$2",
		mehfilID, currentUser).Scan(&status); err != nil {
		return c.Status(404).SendString("Invite nahi mili — shayad expire ho gayi")
	}

	switch action {
	case "accept":
		_, _ = db.Exec(ctx, "INSERT INTO mehfil_members (mehfil_id, username, role) VALUES ($1,$2,'member') ON CONFLICT DO NOTHING", mehfilID, currentUser)
		_, _ = db.Exec(ctx, "UPDATE mehfil_invites SET status='accepted' WHERE mehfil_id=$1 AND invitee=$2", mehfilID, currentUser)
		return c.Redirect("/mehfil/" + mehfilID)
	case "reject":
		_, _ = db.Exec(ctx, "UPDATE mehfil_invites SET status='rejected', rejected_at=NOW() WHERE mehfil_id=$1 AND invitee=$2", mehfilID, currentUser)
		return c.SendString(`<span style="font-size:11px;color:var(--mf-sub);font-weight:700;">❌ Invite reject kar di gayi</span>`)
	default:
		return c.Status(400).SendString("")
	}
}
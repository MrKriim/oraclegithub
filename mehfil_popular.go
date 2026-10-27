package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/gofiber/fiber/v2"
)

func LeaveMehfilHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	mehfilID := c.Params("id")

	if currentUser == "" {
		return c.Status(401).SendString("Unauthorized")
	}

	// Deletes your membership from the table
	_, err := db.Exec(ctx, "DELETE FROM mehfil_members WHERE mehfil_id = $1 AND username = $2", mehfilID, currentUser)
	if err != nil {
		return c.Status(500).SendString("Database Error")
	}

	// HTMX will remove the card because we return an empty string
	return c.SendString("")
}

func DeleteMehfilHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	mehfilID := c.Params("id")

	if currentUser == "" {
		return c.Status(401).SendString("Unauthorized")
	}

	// Verification: Is the requester actually the admin?
	var admin string
	err := db.QueryRow(ctx, "SELECT admin_username FROM mehfils WHERE id = $1", mehfilID).Scan(&admin)
	if err != nil || admin != currentUser {
		return c.Status(403).SendString("Sirf Admin hi delete kar sakta hai!")
	}

	// Delete members first to avoid foreign key issues, then delete the mehfil
	_, _ = db.Exec(ctx, "DELETE FROM mehfil_members WHERE mehfil_id = $1", mehfilID)
	_, err = db.Exec(ctx, "DELETE FROM mehfils WHERE id = $1", mehfilID)

	if err != nil {
		return c.Status(500).SendString("Database Error")
	}

	return c.SendString("")
}

func RenderMehfilCard(id int, rank int, topic, admin, currentUser, timeStr string, mCount, cCount int) string {
	rankHtml := ""
	if rank > 0 {
		rankHtml = fmt.Sprintf(`<div class="card-rank">%02d</div>`, rank)
	}

	// Only show the Action Button if we are on the "Joined" logic
	// (You can pass a boolean or check if rank == 0)
	actionBtn := ""
	if rank == 0 {
		isAdmin := (currentUser == admin)
		btnText := "🚪 Exit"
		btnUrl := fmt.Sprintf("/leave-mehfil/%d", id)
		if isAdmin {
			btnText = "🗑️ Delete"
			btnUrl = fmt.Sprintf("/delete-mehfil/%d", id)
		}
		actionBtn = fmt.Sprintf(`
            <button hx-post="%s" hx-confirm="Are you sure?" class="action-btn">
                %s
            </button>`, btnUrl, btnText)
	}

	return fmt.Sprintf(`
    <div id="mehfil-card-%d" class="mehfil-card">
        %s
        <div class="card-main" onclick="window.location='/view/%d'">
            <div class="topic-row">
                <span class="topic-label">Topic:</span>
                <h3 class="topic-text">%s</h3>
            </div>
            <div class="card-footer">
                <div class="footer-item">👤 %s%s</div>
                <div class="footer-item">👥 %d Members</div>
                <div class="footer-item">💬 %d Messages</div>
                <div class="footer-item">🕒 %s</div>
            </div>
        </div>
        %s
        <div class="card-arrow" onclick="window.location='/view/%d'">→</div>
    </div>`, id, rankHtml, id, topic, admin, TrendingStarHTML(admin), mCount, cCount, timeStr, actionBtn, id)
}

func JoinedMehfilsPage(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Redirect("/login")
	}

	// Optimized Query to get counts for joined mehfils
	rows, err := db.Query(ctx, `
        SELECT m.id, m.topic, m.admin_username, 
        EXTRACT(EPOCH FROM (NOW() - m.created_at))/3600,
        (SELECT COUNT(*) FROM mehfil_members WHERE mehfil_id = m.id),
        (SELECT COUNT(*) FROM mehfil_messages WHERE mehfil_id = m.id)
        FROM mehfils m
        JOIN mehfil_members mm ON m.id = mm.mehfil_id
        WHERE mm.username = $1
        ORDER BY m.id DESC`, currentUser)

	if err != nil {
		return c.Status(500).SendString("Database Error")
	}
	defer rows.Close()

	var listContent strings.Builder
	count := 0
	for rows.Next() {
		count++
		var id int
		var topic, admin string
		var hoursAgo float64
		var mCount, cCount int
		rows.Scan(&id, &topic, &admin, &hoursAgo, &mCount, &cCount)

		timeStr := "Just now"
		if hoursAgo >= 1 {
			timeStr = fmt.Sprintf("%dh ago", int(hoursAgo))
		}

		// Using the modular helper!
		listContent.WriteString(RenderMehfilCard(id, 0, topic, admin, currentUser, timeStr, mCount, cCount))
	}

	emptyMsg := ""
	if count == 0 {
		emptyMsg = `<div style="text-align:center;padding:56px 20px;background:#fff;border:1px dashed #d1d5db;border-radius:24px;margin-top:6px;">
			<div style="font-size:46px;margin-bottom:12px;">🎪</div>
			<p style="font-size:14px;font-weight:700;color:#374151;margin:0 0 6px;">Ap ne abhi tak koi Mehfil join nahi ki</p>
			<p style="font-size:12px;color:#9ca3af;margin:0 0 18px;">Popular Mehfils explore karein aur nayi baithkein join karein</p>
			<a href="/popular" style="display:inline-flex;align-items:center;gap:6px;background:#10b981;color:#fff;padding:11px 22px;border-radius:14px;font-size:13px;font-weight:800;text-decoration:none;box-shadow:0 4px 14px rgba(16,185,129,0.25);">
				🔍 Popular Mehfils Dekhein
			</a>
		</div>`
	}

	content := fmt.Sprintf(`
    <div style="max-width:720px;margin:0 auto;padding:16px 12px 100px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">
        <div style="background:#fff;border:1px solid #e5e7eb;border-radius:24px;padding:20px;margin-bottom:18px;box-shadow:0 2px 10px rgba(0,0,0,0.04);">
            <div style="display:flex;align-items:center;justify-content:space-between;gap:10px;flex-wrap:wrap;">
                <div>
                    <span style="font-size:9px;font-weight:900;letter-spacing:.18em;text-transform:uppercase;color:#9ca3af;display:block;margin-bottom:4px;">Ap ki</span>
                    <h1 style="font-size:24px;font-weight:900;color:#111827;margin:0;">Joined Mehfils</h1>
                </div>
                <a href="/popular" style="display:inline-flex;align-items:center;gap:6px;background:#f3f4f6;color:#374151;padding:10px 16px;border-radius:12px;font-size:12px;font-weight:800;text-decoration:none;border:1px solid #e5e7eb;">
                    ← Popular
                </a>
            </div>
        </div>
        <div style="display:flex;flex-direction:column;gap:12px;">
            %s
        </div>
        %s
    </div>`, listContent.String(), emptyMsg)

	return c.Type("html").SendString(Layout("Joined", content, currentUser))
}

func PopularPage(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)

	// Excludes mehfils whose admin is banned, and ranks by recent activity
	// (messages in the last 48h, then most-recent message time) before
	// falling back to lifetime member+message count as a tiebreaker — so
	// a mehfil that's currently buzzing outranks one that was big once but
	// has gone quiet.
	rows, err := db.Query(ctx, `
        WITH Stats AS (
            SELECT mehfil_id, COUNT(*) as c_count FROM mehfil_messages GROUP BY mehfil_id
        ),
        MemberStats AS (
            SELECT mehfil_id, COUNT(*) as m_count FROM mehfil_members GROUP BY mehfil_id
        ),
        RecentActivity AS (
            SELECT mehfil_id, COUNT(*) as recent_count, MAX(created_at) as last_msg_at
            FROM mehfil_messages
            WHERE created_at > NOW() - INTERVAL '48 hours'
            GROUP BY mehfil_id
        )
        SELECT 
            m.id, m.topic, m.admin_username, 
            EXTRACT(EPOCH FROM (NOW() - m.created_at))/3600,
            COALESCE(ms.m_count, 0), 
            COALESCE(s.c_count, 0)
        FROM mehfils m
        JOIN users u ON u.username = m.admin_username
        LEFT JOIN Stats s ON s.mehfil_id = m.id
        LEFT JOIN MemberStats ms ON ms.mehfil_id = m.id
        LEFT JOIN RecentActivity ra ON ra.mehfil_id = m.id
        WHERE COALESCE(u.is_banned,false) = false
        ORDER BY COALESCE(ra.recent_count,0) DESC,
                 ra.last_msg_at DESC NULLS LAST,
                 (COALESCE(ms.m_count, 0) + COALESCE(s.c_count, 0)) DESC 
        LIMIT 10`)

	if err != nil {
		log.Println("PopularPage Error:", err)
		return c.Status(500).SendString("Database Error")
	}
	defer rows.Close()

	var listContent strings.Builder
	rank := 0

	for rows.Next() {
		rank++
		var id int
		var topic, admin string
		var hoursAgo float64
		var mCount, cCount int
		rows.Scan(&id, &topic, &admin, &hoursAgo, &mCount, &cCount)

		timeStr := "Recently"
		if hoursAgo >= 1 {
			timeStr = fmt.Sprintf("%dh ago", int(hoursAgo))
		} else {
			mins := int(hoursAgo * 60)
			if mins < 1 {
				timeStr = "Just now"
			} else {
				timeStr = fmt.Sprintf("%dm ago", mins)
			}
		}

		// MODULAR CALL: Using the same design helper
		listContent.WriteString(RenderMehfilCard(id, rank, topic, admin, currentUser, timeStr, mCount, cCount))
	}

	emptyMsg := ""
	if rank == 0 {
		emptyMsg = `<div style="text-align:center;padding:56px 20px;background:#fff;border:1px dashed #d1d5db;border-radius:24px;">
			<div style="font-size:46px;margin-bottom:12px;">🎪</div>
			<p style="font-size:14px;font-weight:700;color:#374151;margin:0 0 6px;">Abhi tak koi Mehfil nahi bani</p>
			<p style="font-size:12px;color:#9ca3af;margin:0 0 18px;">Sab se pehli Mehfil banane wale ap hi banein</p>
			<a href="/create" style="display:inline-flex;align-items:center;gap:6px;background:#10b981;color:#fff;padding:11px 22px;border-radius:14px;font-size:13px;font-weight:800;text-decoration:none;box-shadow:0 4px 14px rgba(16,185,129,0.25);">
				➕ Create Mehfil
			</a>
		</div>`
	}

	content := fmt.Sprintf(`
    <div style="max-width:720px;margin:0 auto;padding:16px 12px 100px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">
        <div style="background:#fff;border:1px solid #e5e7eb;border-radius:24px;padding:20px;margin-bottom:18px;box-shadow:0 2px 10px rgba(0,0,0,0.04);">
            <div style="display:flex;align-items:center;justify-content:space-between;gap:10px;flex-wrap:wrap;">
                <div>
                    <span style="font-size:9px;font-weight:900;letter-spacing:.18em;text-transform:uppercase;color:#9ca3af;display:block;margin-bottom:4px;">Explore</span>
                    <h1 style="font-size:24px;font-weight:900;color:#111827;margin:0;">Top 10 Popular Mehfils</h1>
                </div>
                <div style="display:flex;gap:8px;flex-wrap:wrap;">
                    <a href="/joined-mehfils" style="display:inline-flex;align-items:center;gap:6px;background:#eff6ff;color:#2563eb;padding:10px 16px;border-radius:12px;font-size:12px;font-weight:800;text-decoration:none;border:1px solid #bfdbfe;">
                        🎪 Joined Mehfils
                    </a>
                    <a href="/create" style="display:inline-flex;align-items:center;gap:6px;background:#10b981;color:#fff;padding:10px 16px;border-radius:12px;font-size:12px;font-weight:800;text-decoration:none;box-shadow:0 4px 12px rgba(16,185,129,0.25);">
                        ➕ Create Mehfil
                    </a>
                </div>
            </div>
        </div>
        <div style="display:flex;flex-direction:column;gap:12px;">
            %s
        </div>
        %s
    </div>`, listContent.String(), emptyMsg)

	return c.Type("html").SendString(Layout("Explore", content, currentUser))
}

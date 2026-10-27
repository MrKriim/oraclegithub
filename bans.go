package main

import (
	"context"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// ── durations ────────────────────────────────────────────────────────────

type banDurationOpt struct {
	Key   string
	Label string
	TTL   time.Duration // 0 = permanent
}

var banDurationOptions = []banDurationOpt{
	{"6h", "6 Hours", 6 * time.Hour},
	{"1d", "1 Day", 24 * time.Hour},
	{"3d", "3 Days", 72 * time.Hour},
	{"7d", "7 Days", 7 * 24 * time.Hour},
	{"1m", "1 Month", 30 * 24 * time.Hour},
	{"perm", "Permanent", 0},
}

func banDurationLabel(key string) string {
	for _, o := range banDurationOptions {
		if o.Key == key {
			return o.Label
		}
	}
	return key
}

// ── schema ───────────────────────────────────────────────────────────────

var dostanaRules = []string{
	"No abuse.",
	"No nude pictures upload.",
	"No vulgar or sexual act pictures upload.",
	"No bloody or gore pictures upload.",
	"Don't leak someone's private pictures.",
	"Don't harass anyone.",
	"Don't share any other social media website here.",
	"Don't beg on the home page for 1-on-1 chat.",
	"Don't create fake accounts.",
	"Don't upload a vulgar DP (profile picture).",
	"Don't try to damage the website in any way.",
	"No firqawariat (sectarianism).",
	"Don't say anything wrong about any religion.",
	"No political posts.",
	"Don't spread misinformation.",
	"Don't spread misinformation about admins.",
	"Don't leak anyone's phone number.",
	"Don't leak anyone's email address.",
	"I will not misuse any bug of the website and will report it to the admin.",
	"I will not spread misinformation about Dostana.",
	"I will not try to damage the reputation of Dostana or its admins on any other website.",
	"I will not upload images of small babies.",
	"I will not upload groom or bride pictures.",
	"I will not spam on the website.",
	"I will not misuse the website in any way.",
	"I will not try to attack the website via DDoS or any kind of hacking.",
	"I agree to follow these rules, and I understand new rules may be added in the future.",
}

func initBanSchema() {
	mustExec("create rules", `
		CREATE TABLE IF NOT EXISTS rules (
			rule_no    INT PRIMARY KEY,
			rule_text  TEXT NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`)

	mustExec("create user_bans", `
		CREATE TABLE IF NOT EXISTS user_bans (
			id         SERIAL PRIMARY KEY,
			username   TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			banned_by  TEXT NOT NULL DEFAULT '',
			rule_no    INT REFERENCES rules(rule_no) ON DELETE SET NULL,
			reason     TEXT NOT NULL DEFAULT '',
			duration   TEXT NOT NULL DEFAULT '',
			banned_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ,
			is_active  BOOLEAN NOT NULL DEFAULT TRUE
		)`)

	softExecBatch([]struct{ label, sql string }{
		{"index idx_user_bans_username", `CREATE INDEX IF NOT EXISTS idx_user_bans_username ON user_bans(username)`},
		{"index idx_user_bans_active", `CREATE INDEX IF NOT EXISTS idx_user_bans_active ON user_bans(username) WHERE is_active = TRUE`},
		{"index idx_user_bans_expires", `CREATE INDEX IF NOT EXISTS idx_user_bans_expires ON user_bans(expires_at) WHERE is_active = TRUE AND expires_at IS NOT NULL`},
		{"alter user_bans add unbanned_by", `ALTER TABLE user_bans ADD COLUMN IF NOT EXISTS unbanned_by TEXT NOT NULL DEFAULT ''`},
		{"alter user_bans add unbanned_at", `ALTER TABLE user_bans ADD COLUMN IF NOT EXISTS unbanned_at TIMESTAMPTZ`},
	})

	for i, text := range dostanaRules {
		ruleNo := i + 1
		if _, err := db.Exec(ctx,
			`INSERT INTO rules (rule_no, rule_text) VALUES ($1, $2) ON CONFLICT (rule_no) DO NOTHING`,
			ruleNo, text); err != nil {
			log.Printf("WARN seedRules rule %d: %v", ruleNo, err)
		}
	}
}

// ── expiry worker ────────────────────────────────────────────────────────

func StartBanExpiryWorker() {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in ban-expiry worker (loop stopped): %v", r)
			}
		}()
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("WARN [banExpiryWorker] recovered: %v", r)
					}
				}()
				sweepExpiredBans()
			}()
		}
	}()
}

func sweepExpiredBans() {
	sCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := db.Query(sCtx, `
		SELECT DISTINCT username FROM user_bans
		WHERE is_active = TRUE AND expires_at IS NOT NULL AND expires_at <= NOW()`)
	if err != nil {
		log.Printf("WARN [banExpirySweep] query: %v", err)
		return
	}
	var usernames []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err == nil {
			usernames = append(usernames, u)
		}
	}
	rows.Close()
	if len(usernames) == 0 {
		return
	}

	if _, err := db.Exec(sCtx, `
		UPDATE user_bans SET is_active = FALSE, unbanned_by = 'System (expired)', unbanned_at = NOW()
		WHERE is_active = TRUE AND expires_at IS NOT NULL AND expires_at <= NOW()`); err != nil {
		log.Printf("WARN [banExpirySweep] deactivate: %v", err)
		return
	}

	for _, u := range usernames {
		if _, err := db.Exec(sCtx, `UPDATE users SET is_banned = FALSE WHERE username = $1`, u); err != nil {
			log.Printf("WARN [banExpirySweep] unban %s: %v", u, err)
			continue
		}
		InvalidateBanCache(u)
		CacheDel("acct_gate:" + u)
		log.Printf("⏰ Ban expired, auto-unbanned: %s", u)
	}
}

// ── active ban lookup ────────────────────────────────────────────────────

type activeBanInfo struct {
	Found     bool
	Reason    string
	RuleNo    int
	RuleText  string
	Duration  string
	BannedBy  string
	BannedAt  time.Time
	ExpiresAt *time.Time
}

func getActiveBanInfo(username string) activeBanInfo {
	var info activeBanInfo
	var ruleNo *int
	var ruleText *string
	err := db.QueryRow(ctx, `
		SELECT b.reason, b.rule_no, r.rule_text, b.duration, b.banned_by, b.banned_at, b.expires_at
		FROM user_bans b
		LEFT JOIN rules r ON r.rule_no = b.rule_no
		WHERE b.username = $1 AND b.is_active = TRUE
		ORDER BY b.banned_at DESC
		LIMIT 1`, username).
		Scan(&info.Reason, &ruleNo, &ruleText, &info.Duration, &info.BannedBy, &info.BannedAt, &info.ExpiresAt)
	if err == nil {
		info.Found = true
		if ruleNo != nil {
			info.RuleNo = *ruleNo
		}
		if ruleText != nil {
			info.RuleText = *ruleText
		}
		return info
	}

	// Fallback: users.is_banned can be TRUE with no matching user_bans row
	// (e.g. banned via a path that doesn't write a ban record). Without
	// this, the ban form and public notice both think nobody is banned —
	// hiding Unban entirely even though the account is actually locked
	// out. Surface it anyway so an Unban path always exists.
	var isBanned bool
	if chkErr := db.QueryRow(ctx, "SELECT COALESCE(is_banned,false) FROM users WHERE username=$1", username).Scan(&isBanned); chkErr == nil && isBanned {
		return activeBanInfo{
			Found:    true,
			Reason:   "No ban record found for this account — it was likely banned outside the ban form.",
			Duration: "unknown",
			BannedBy: "unknown",
		}
	}

	return activeBanInfo{}
}
func renderBanNoticeHTML(username string) string {
	info := getActiveBanInfo(username)

	ruleLine := ""
	if info.Found && info.RuleNo > 0 {
		ruleLine = fmt.Sprintf(`<div style="margin-top:10px;font-size:13px;color:#7f1d1d;"><strong>Rule #%d:</strong> %s</div>`,
			info.RuleNo, html.EscapeString(info.RuleText))
	}

	bannedByLine := ""
	if info.Found && strings.TrimSpace(info.BannedBy) != "" {
		bannedByLine = fmt.Sprintf(`<div style="margin-top:6px;font-size:12px;color:#9f1239;">Banned by: <strong>%s</strong></div>`,
			html.EscapeString(info.BannedBy))
	}

	reasonLine := ""
	if info.Found && strings.TrimSpace(info.Reason) != "" {
		reasonLine = fmt.Sprintf(`<div style="margin-top:6px;font-size:13px;color:#7f1d1d;"><strong>Reason:</strong> %s</div>`, html.EscapeString(info.Reason))
	}

	durationLine := ""
	if info.Found {
		loc, _ := time.LoadLocation("Asia/Karachi")
		durLabel := banDurationLabel(info.Duration)
		if info.ExpiresAt != nil {
			durationLine = fmt.Sprintf(`<div style="margin-top:6px;font-size:12px;color:#9f1239;">Duration: %s · Expires: %s (PKT)</div>`,
				durLabel, info.ExpiresAt.In(loc).Format("02 Jan 2006 15:04"))
		} else {
			durationLine = `<div style="margin-top:6px;font-size:12px;color:#9f1239;">Duration: Permanent</div>`
		}
	}

	return fmt.Sprintf(`
	<div style="text-align:center;padding:40px 20px;background:#fff1f2;border:2px solid #fda4af;border-radius:32px;">
		<div style="font-size:50px;margin-bottom:15px;">🚫</div>
		<h2 style="color:#be123c;margin:0;font-size:22px;font-weight:800;">Account Ban Kar Diya Gaya Hai</h2>
		<p style="color:#fb7185;font-size:14px;margin-top:8px;">Is user ki profile aur posts ab invisible hain.</p>
		%s
		%s
		%s
		%s
		<div style="margin-top:14px;">
			<a href="/rules" style="font-size:12px;color:#be123c;font-weight:700;text-decoration:underline;">Read Dostana Rules</a>
		</div>
	</div>`, ruleLine, reasonLine, durationLine, bannedByLine)
}

// ── admin ban management page ───────────────────────────────────────────

func adminBansPageHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	c.Set("Content-Type", "text/html") // same text/plain-on-refresh bug as rulesPageHandler
	q := strings.TrimSpace(c.Query("q"))
	selected := strings.TrimSpace(c.Query("username"))

	searchHTML := renderBanUserSearchResults(q)
	formHTML := ""
	if selected != "" {
		formHTML = renderBanFormFragment(selected)
	}

	body := fmt.Sprintf(`
	<div class="mehfil-container" style="max-width:760px;">
		<div class="page-header"><h1>🛡️ Ban Management</h1></div>
		<div style="background:#fff;border-radius:20px;padding:20px;border:1px solid #e5e7eb;margin-bottom:20px;">
			<form hx-get="/admin/bans/search" hx-target="#ban-search-results" hx-swap="innerHTML" style="display:flex;gap:10px;">
				<input type="text" name="q" value="%s" placeholder="Search username…"
					style="flex:1;padding:11px 14px;border:1px solid #d1d5db;border-radius:14px;font-size:14px;">
				<button type="submit" style="background:#111827;color:#fff;padding:11px 22px;border-radius:14px;font-weight:700;border:none;cursor:pointer;">Search</button>
			</form>
			<div id="ban-search-results" style="margin-top:16px;display:flex;flex-direction:column;gap:8px;">
				%s
			</div>
		</div>
		<div id="ban-form-area">
			%s
		</div>
	</div>`, html.EscapeString(q), searchHTML, formHTML)

	return c.SendString(Layout("Ban Management • Admin", body, currentUser))
}

func renderBanUserSearchResults(q string) string {
	if q == "" {
		return `<p style="font-size:13px;color:#6b7280;">Type a username to search.</p>`
	}
	rows, err := db.Query(ctx, `
		SELECT username, COALESCE(is_banned,false)
		FROM users WHERE username ILIKE $1
		ORDER BY username LIMIT 20`, "%"+q+"%")
	if err != nil {
		return `<p style="font-size:13px;color:#dc2626;">Search error.</p>`
	}
	defer rows.Close()

	var b strings.Builder
	count := 0
	for rows.Next() {
		var uname string
		var banned bool
		if err := rows.Scan(&uname, &banned); err != nil {
			continue
		}
		count++
		statusBadge := `<span style="background:#d1fae5;color:#047857;font-size:10px;font-weight:800;padding:2px 8px;border-radius:999px;">ACTIVE</span>`
		if banned {
			statusBadge = `<span style="background:#fee2e2;color:#b91c1c;font-size:10px;font-weight:800;padding:2px 8px;border-radius:999px;">BANNED</span>`
		}
		b.WriteString(fmt.Sprintf(`
		<div hx-get="/admin/bans/form/%[1]s" hx-target="#ban-form-area" hx-swap="innerHTML"
			style="display:flex;align-items:center;justify-content:space-between;padding:10px 14px;border:1px solid #f3f4f6;border-radius:12px;cursor:pointer;">
			<span style="font-weight:700;font-size:14px;">%[1]s</span>
			%[2]s
		</div>`, uname, statusBadge))
	}
	if count == 0 {
		return `<p style="font-size:13px;color:#6b7280;">No users found.</p>`
	}
	return b.String()
}

func renderBanFormFragment(username string) string {
	var exists bool
	db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE username=$1)", username).Scan(&exists)
	if !exists {
		return `<p style="color:#dc2626;font-size:13px;">User not found.</p>`
	}

	info := getActiveBanInfo(username)

	durationOptions := ""
	for _, o := range banDurationOptions {
		durationOptions += fmt.Sprintf(`<option value="%s">%s</option>`, o.Key, o.Label)
	}

	ruleOptions := `<option value="">— No specific rule —</option>`
	rrows, err := db.Query(ctx, "SELECT rule_no, rule_text FROM rules ORDER BY rule_no")
	if err == nil {
		defer rrows.Close()
		for rrows.Next() {
			var no int
			var text string
			rrows.Scan(&no, &text)
			ruleOptions += fmt.Sprintf(`<option value="%d">Rule #%d — %s</option>`, no, no, html.EscapeString(text))
		}
	}

	currentStatusHTML := `<div style="padding:12px 16px;background:#d1fae5;color:#047857;border-radius:14px;font-weight:700;font-size:13px;margin-bottom:16px;">✅ Not currently banned</div>`
	unbanBtn := ""
	if info.Found {
		loc, _ := time.LoadLocation("Asia/Karachi")
		expiryText := "Permanent"
		if info.ExpiresAt != nil {
			expiryText = info.ExpiresAt.In(loc).Format("02 Jan 2006 15:04") + " PKT"
		}
		ruleText := ""
		if info.RuleNo > 0 {
			ruleText = fmt.Sprintf(" · Rule #%d", info.RuleNo)
		}
		currentStatusHTML = fmt.Sprintf(`
		<div style="padding:14px 16px;background:#fee2e2;color:#991b1b;border-radius:14px;font-size:13px;margin-bottom:16px;">
			🚫 <strong>Currently banned</strong> by %s%s<br>
			Reason: %s<br>
			Duration: %s · Expires: %s
		</div>`, html.EscapeString(info.BannedBy), ruleText, html.EscapeString(info.Reason),
			banDurationLabel(info.Duration), expiryText)

		unbanBtn = fmt.Sprintf(`
		<button type="button" hx-post="/admin/bans/unban/%s" hx-target="#ban-form-area" hx-swap="innerHTML" hx-confirm="Unban %s?"
			style="background:#059669;color:#fff;padding:12px 24px;border-radius:14px;font-weight:700;border:none;cursor:pointer;">
			✅ Unban Now
		</button>`, username, username)
	}

	return fmt.Sprintf(`
	<div style="background:#fff;border-radius:20px;padding:20px;border:1px solid #e5e7eb;">
		<h2 style="font-size:16px;font-weight:900;margin-bottom:12px;">Managing: %s</h2>
		%s
		<form hx-post="/admin/bans/create/%s" hx-target="#ban-form-area" hx-swap="innerHTML" style="display:flex;flex-direction:column;gap:14px;">
			<div>
				<label style="display:block;font-size:12px;font-weight:700;color:#374151;margin-bottom:6px;">Rule Broken</label>
				<select name="rule_no" style="width:100%%;padding:11px 14px;border:1px solid #d1d5db;border-radius:14px;font-size:13px;">
					%s
				</select>
			</div>
			<div>
				<label style="display:block;font-size:12px;font-weight:700;color:#374151;margin-bottom:6px;">Reason (shown to user)</label>
				<textarea name="reason" rows="3" style="width:100%%;padding:11px 14px;border:1px solid #d1d5db;border-radius:14px;font-size:13px;resize:vertical;"></textarea>
			</div>
			<div>
				<label style="display:block;font-size:12px;font-weight:700;color:#374151;margin-bottom:6px;">Ban Duration</label>
				<select name="duration" style="width:100%%;padding:11px 14px;border:1px solid #d1d5db;border-radius:14px;font-size:13px;">
					%s
				</select>
			</div>
			<div style="display:flex;gap:12px;">
				<button type="submit" style="background:#dc2626;color:#fff;padding:12px 24px;border-radius:14px;font-weight:700;border:none;cursor:pointer;flex:1;">
					🚫 Ban User
				</button>
				%s
			</div>
		</form>
	</div>`, username, currentStatusHTML, username, ruleOptions, durationOptions, unbanBtn)
}

func adminBanSearchHandler(c *fiber.Ctx) error {
	return c.SendString(renderBanUserSearchResults(strings.TrimSpace(c.Query("q"))))
}

func adminBanUserFormHandler(c *fiber.Ctx) error {
	return c.SendString(renderBanFormFragment(c.Params("username")))
}

func adminCreateBanHandler(c *fiber.Ctx) error {
	currentAdmin := getCurrentUser(c)
	username := c.Params("username")

	var exists bool
	db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE username=$1)", username).Scan(&exists)
	if !exists {
		return c.Status(404).SendString("<p style='color:#dc2626;'>User not found</p>")
	}

	reason := strings.TrimSpace(c.FormValue("reason"))
	if len(reason) > 500 {
		reason = reason[:500]
	}
	durationKey := c.FormValue("duration")
	ruleNoStr := strings.TrimSpace(c.FormValue("rule_no"))

	var ruleNo *int
	if ruleNoStr != "" {
		if n, err := strconv.Atoi(ruleNoStr); err == nil {
			ruleNo = &n
		}
	}

	var ttl time.Duration
	found := false
	for _, o := range banDurationOptions {
		if o.Key == durationKey {
			ttl = o.TTL
			found = true
			break
		}
	}
	if !found {
		durationKey = "1d"
		ttl = 24 * time.Hour
	}

	var expiresAt *time.Time
	if ttl > 0 {
		t := time.Now().Add(ttl)
		expiresAt = &t
	}

	if _, err := db.Exec(ctx, `
		UPDATE user_bans SET is_active = FALSE, unbanned_by = $2, unbanned_at = NOW()
		WHERE username=$1 AND is_active = TRUE`, username, currentAdmin+" (updated ban)"); err != nil {
		log.Printf("adminCreateBanHandler deactivate old bans error: %v", err)
	}

	if _, err := db.Exec(ctx, `
		INSERT INTO user_bans (username, banned_by, rule_no, reason, duration, expires_at, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, TRUE)`,
		username, currentAdmin, ruleNo, reason, durationKey, expiresAt); err != nil {
		log.Println("adminCreateBanHandler insert error:", err)
		return c.Status(500).SendString("<p style='color:#dc2626;'>Failed to save ban</p>")
	}

	if _, err := db.Exec(ctx, "UPDATE users SET is_banned = TRUE WHERE username = $1", username); err != nil {
		log.Println("adminCreateBanHandler ban flag error:", err)
	}
	InvalidateBanCache(username)
	CacheDel("acct_gate:" + username)

	return c.SendString(renderBanFormFragment(username))
}

func adminUnbanHandler(c *fiber.Ctx) error {
	currentAdmin := getCurrentUser(c)
	username := c.Params("username")

	if _, err := db.Exec(ctx, `
		UPDATE user_bans SET is_active = FALSE, unbanned_by = $2, unbanned_at = NOW()
		WHERE username=$1 AND is_active = TRUE`, username, currentAdmin); err != nil {
		log.Println("adminUnbanHandler deactivate error:", err)
	}
	if _, err := db.Exec(ctx, "UPDATE users SET is_banned = FALSE WHERE username = $1", username); err != nil {
		log.Println("adminUnbanHandler unban error:", err)
		return c.Status(500).SendString("<p style='color:#dc2626;'>Failed to unban</p>")
	}
	InvalidateBanCache(username)
	CacheDel("acct_gate:" + username)

	return c.SendString(renderBanFormFragment(username))
}

// ── public rules page ────────────────────────────────────────────────────

func rulesPageHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	// Without an explicit Content-Type, Fiber's SendString defaults to
	// text/plain — htmx-boosted navigation still looked fine (it swaps
	// raw response text into the DOM regardless of header), but a real
	// hard refresh made the browser display the literal HTML source
	// instead of rendering it.
	c.Set("Content-Type", "text/html")

	rows, err := db.Query(ctx, "SELECT rule_no, rule_text FROM rules ORDER BY rule_no")
	var rulesHTML strings.Builder
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var no int
			var text string
			if err := rows.Scan(&no, &text); err != nil {
				continue
			}
			rulesHTML.WriteString(fmt.Sprintf(`
			<div style="display:flex;gap:14px;padding:14px 4px;border-bottom:1px solid #f3f4f6;">
				<span style="flex-shrink:0;width:30px;height:30px;border-radius:50%%;background:#059669;color:#fff;display:flex;align-items:center;justify-content:center;font-weight:900;font-size:13px;">%d</span>
				<span style="flex:1;font-size:14px;color:#1f2937;line-height:1.6;padding-top:4px;">%s</span>
			</div>`, no, html.EscapeString(text)))
		}
	}

	durationsList := ""
	for _, o := range banDurationOptions {
		durationsList += fmt.Sprintf(`<li>%s</li>`, o.Label)
	}

	body := fmt.Sprintf(`
	<div class="mehfil-container" style="max-width:720px;">
		<div style="background:#fff;border-radius:24px;padding:28px 22px;border:1px solid #f3f4f6;box-shadow:0 10px 40px rgba(0,0,0,0.06);">
			<h1 style="font-size:26px;font-weight:900;margin-bottom:6px;">📜 Dostana Rules</h1>
			<p style="font-size:14px;color:#6b7280;margin-bottom:20px;line-height:1.7;">
				By joining this website I agree to follow the rules below. If I don't agree, my account can be banned
				for the durations listed below. These are the strict rules of Dostana:
			</p>
			<div style="display:flex;flex-direction:column;">
				%s
			</div>
			<div style="margin-top:24px;padding:18px;background:#f9fafb;border-radius:18px;">
				<h2 style="font-size:14px;font-weight:800;margin-bottom:10px;color:#111827;">⏱️ Possible Ban Durations</h2>
				<ul style="margin:0;padding-left:18px;font-size:13px;color:#374151;line-height:1.9;">
					%s
				</ul>
			</div>
		</div>
	</div>`, rulesHTML.String(), durationsList)

	return c.SendString(Layout("Rules • Dostana", body, currentUser))
}

// ── ban logs ─────────────────────────────────────────────────────────────

type banLogEntry struct {
	ID         int
	Username   string
	BannedBy   string
	RuleNo     *int
	RuleText   *string
	Reason     string
	Duration   string
	BannedAt   time.Time
	ExpiresAt  *time.Time
	IsActive   bool
	UnbannedBy string
	UnbannedAt *time.Time
}

const banLogSelectCols = `b.id, b.username, b.banned_by, b.rule_no, r.rule_text, b.reason, b.duration,
	b.banned_at, b.expires_at, b.is_active, COALESCE(b.unbanned_by,''), b.unbanned_at`

func scanBanLogRow(rows interface{ Scan(dest ...interface{}) error }) (banLogEntry, error) {
	var e banLogEntry
	err := rows.Scan(&e.ID, &e.Username, &e.BannedBy, &e.RuleNo, &e.RuleText, &e.Reason, &e.Duration,
		&e.BannedAt, &e.ExpiresAt, &e.IsActive, &e.UnbannedBy, &e.UnbannedAt)
	return e, err
}

const banLogsBatchSize = 20

func queryBanLogs(beforeID, limit int) (rows []banLogEntry, hasMore bool) {
	var q string
	var args []interface{}
	if beforeID > 0 {
		q = `SELECT ` + banLogSelectCols + ` FROM user_bans b LEFT JOIN rules r ON r.rule_no=b.rule_no
			WHERE b.id < $1 ORDER BY b.id DESC LIMIT $2`
		args = []interface{}{beforeID, limit + 1}
	} else {
		q = `SELECT ` + banLogSelectCols + ` FROM user_bans b LEFT JOIN rules r ON r.rule_no=b.rule_no
			ORDER BY b.id DESC LIMIT $1`
		args = []interface{}{limit + 1}
	}

	dbRows, err := db.Query(ctx, q, args...)
	if err != nil {
		log.Println("queryBanLogs error:", err)
		return nil, false
	}
	defer dbRows.Close()
	for dbRows.Next() {
		e, err := scanBanLogRow(dbRows)
		if err != nil {
			continue
		}
		rows = append(rows, e)
	}
	if len(rows) > limit {
		rows = rows[:limit]
		hasMore = true
	}
	return
}

func banLogStatusBadge(e banLogEntry) string {
	if e.IsActive {
		return `<span style="background:#fee2e2;color:#b91c1c;font-size:10px;font-weight:900;padding:3px 10px;border-radius:999px;white-space:nowrap;">🚫 ACTIVE</span>`
	}
	switch {
	case strings.Contains(e.UnbannedBy, "System (expired)"):
		return `<span style="background:#e0e7ff;color:#4338ca;font-size:10px;font-weight:900;padding:3px 10px;border-radius:999px;white-space:nowrap;">⏰ EXPIRED</span>`
	case strings.Contains(e.UnbannedBy, "(updated ban)"):
		return `<span style="background:#fef3c7;color:#b45309;font-size:10px;font-weight:900;padding:3px 10px;border-radius:999px;white-space:nowrap;">🔄 DURATION CHANGED</span>`
	default:
		return `<span style="background:#d1fae5;color:#047857;font-size:10px;font-weight:900;padding:3px 10px;border-radius:999px;white-space:nowrap;">✅ UNBANNED</span>`
	}
}

func renderBanLogRow(e banLogEntry) string {
	loc, _ := time.LoadLocation("Asia/Karachi")

	ruleLine := ""
	if e.RuleNo != nil {
		rt := ""
		if e.RuleText != nil {
			rt = *e.RuleText
		}
		ruleLine = fmt.Sprintf(`<div style="font-size:12px;color:#6b7280;margin-top:4px;">Rule #%d — %s</div>`, *e.RuleNo, html.EscapeString(rt))
	}

	reasonLine := ""
	if strings.TrimSpace(e.Reason) != "" {
		reasonLine = fmt.Sprintf(`<div style="font-size:13px;color:#374151;margin-top:6px;"><strong>Reason:</strong> %s</div>`, html.EscapeString(e.Reason))
	}

	expiryText := "Permanent"
	if e.ExpiresAt != nil {
		expiryText = e.ExpiresAt.In(loc).Format("02 Jan 2006 15:04") + " PKT"
	}

	unbanLine := ""
	if !e.IsActive {
		who := e.UnbannedBy
		if who == "" {
			who = "unknown"
		}
		when := ""
		if e.UnbannedAt != nil {
			when = " on " + e.UnbannedAt.In(loc).Format("02 Jan 2006 15:04") + " PKT"
		}
		unbanLine = fmt.Sprintf(`<div style="font-size:12px;color:#6b7280;margin-top:8px;border-top:1px dashed #e5e7eb;padding-top:6px;">↩️ Lifted by <strong>%s</strong>%s</div>`,
			html.EscapeString(who), when)
	}

	return fmt.Sprintf(`
	<div style="background:#fff;border:1px solid #f3f4f6;border-radius:16px;padding:14px 16px;">
		<div style="display:flex;align-items:center;justify-content:space-between;gap:10px;flex-wrap:wrap;">
			<a href="/profile/%s" style="font-weight:800;font-size:14px;color:#111827;text-decoration:none;">%s</a>
			%s
		</div>
		<div style="font-size:12px;color:#6b7280;margin-top:4px;">
			Banned by <strong>%s</strong> · %s · Duration: %s · Expires: %s
		</div>
		%s
		%s
		%s
	</div>`,
		e.Username, html.EscapeString(e.Username), banLogStatusBadge(e),
		html.EscapeString(e.BannedBy), e.BannedAt.In(loc).Format("02 Jan 2006 15:04")+" PKT",
		banDurationLabel(e.Duration), expiryText,
		ruleLine, reasonLine, unbanLine)
}

func banLogLoadMoreButtonHTML(cursorID int) string {
	return fmt.Sprintf(`
	<div id="ban-log-more" style="text-align:center;padding:16px 0;">
		<button hx-get="/admin/ban-logs/load-more?before_id=%d" hx-target="#ban-log-more" hx-swap="outerHTML"
			style="background:#f3f4f6;color:#374151;padding:10px 26px;border-radius:14px;font-weight:700;font-size:13px;border:none;cursor:pointer;">
			Load More
		</button>
	</div>`, cursorID)
}

func banLogsPageHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	c.Set("Content-Type", "text/html")

	rows, hasMore := queryBanLogs(0, banLogsBatchSize)

	var list strings.Builder
	for _, r := range rows {
		list.WriteString(renderBanLogRow(r))
	}

	more := ""
	if hasMore && len(rows) > 0 {
		more = banLogLoadMoreButtonHTML(rows[len(rows)-1].ID)
	}

	empty := ""
	if len(rows) == 0 {
		empty = `<div style="text-align:center;padding:48px 20px;color:#6b7280;">
			<div style="font-size:44px;margin-bottom:12px;">📋</div>
			<p style="font-size:13px;">No ban activity yet.</p>
		</div>`
	}

	body := fmt.Sprintf(`
	<div class="mehfil-container" style="max-width:760px;">
		<div class="page-header">
			<h1>📋 Ban Logs</h1>
			<a href="/admin/dashboard" hx-boost="true" style="font-size:12px;font-weight:700;color:#374151;text-decoration:none;background:#f3f4f6;padding:8px 16px;border-radius:12px;">← Dashboard</a>
		</div>
		<div id="ban-log-list" style="display:flex;flex-direction:column;gap:10px;">
			%s
		</div>
		%s
		%s
	</div>`, list.String(), empty, more)

	return c.SendString(Layout("Ban Logs • Admin", body, currentUser))
}

func banLogsLoadMoreHandler(c *fiber.Ctx) error {
	beforeID, _ := strconv.Atoi(c.Query("before_id"))
	rows, hasMore := queryBanLogs(beforeID, banLogsBatchSize)

	if len(rows) == 0 {
		return c.SendString("")
	}

	var list strings.Builder
	for _, r := range rows {
		list.WriteString(renderBanLogRow(r))
	}

	if hasMore {
		list.WriteString(banLogLoadMoreButtonHTML(rows[len(rows)-1].ID))
	}
	return c.SendString(list.String())
}

// ── admin dashboard ──────────────────────────────────────────────────────

func adminDashboardHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	c.Set("Content-Type", "text/html")

	pendingCount := getCachedPendingCount()
	var activeBanCount, totalUsers int
	db.QueryRow(ctx, "SELECT COUNT(*) FROM user_bans WHERE is_active=true").Scan(&activeBanCount)
	db.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&totalUsers)

	card := func(href, icon, title, desc string, badge int) string {
		badgeHTML := ""
		if badge > 0 {
			badgeHTML = fmt.Sprintf(`<span style="background:#dc2626;color:#fff;font-size:11px;font-weight:900;padding:2px 9px;border-radius:999px;">%d</span>`, badge)
		}
		return fmt.Sprintf(`
		<a href="%s" hx-boost="true" style="display:flex;align-items:center;gap:14px;background:#fff;border:1px solid #f3f4f6;border-radius:20px;padding:18px 20px;text-decoration:none;box-shadow:0 4px 14px rgba(0,0,0,0.04);">
			<span style="font-size:26px;flex-shrink:0;">%s</span>
			<div style="flex:1;min-width:0;">
				<div style="display:flex;align-items:center;gap:8px;">
					<span style="font-size:15px;font-weight:800;color:#111827;">%s</span>
					%s
				</div>
				<div style="font-size:12px;color:#6b7280;margin-top:2px;">%s</div>
			</div>
			<span style="color:#9ca3af;font-size:18px;">›</span>
		</a>`, href, icon, title, badgeHTML, desc)
	}

	body := fmt.Sprintf(`
	<div class="mehfil-container" style="max-width:640px;">
		<div class="page-header"><h1>🛡️ Admin Dashboard</h1></div>
		<div style="display:flex;flex-direction:column;gap:12px;">
			%s
			%s
			%s
		</div>
		<div style="margin-top:24px;display:grid;grid-template-columns:repeat(auto-fit,minmax(140px,1fr));gap:10px;">
			<div style="background:#f9fafb;border-radius:16px;padding:14px;text-align:center;">
				<div style="font-size:20px;font-weight:900;color:#111827;">%d</div>
				<div style="font-size:11px;color:#6b7280;">Total Users</div>
			</div>
			<div style="background:#f9fafb;border-radius:16px;padding:14px;text-align:center;">
				<div style="font-size:20px;font-weight:900;color:#b91c1c;">%d</div>
				<div style="font-size:11px;color:#6b7280;">Active Bans</div>
			</div>
			<div style="background:#f9fafb;border-radius:16px;padding:14px;text-align:center;">
				<div style="font-size:20px;font-weight:900;color:#b45309;">%d</div>
				<div style="font-size:11px;color:#6b7280;">Pending Approval</div>
			</div>
		</div>
	</div>`,
		card("/admin/pending", "🕓", "Pending Approvals", "Review and approve or reject new signups", pendingCount),
		card("/admin/bans", "🚫", "Ban Management", "Search a user to ban, unban, or update their ban duration", 0),
		card("/admin/ban-logs", "📋", "Ban Logs", "Full history — who banned/unbanned whom, for how long, and why", 0),
		totalUsers, activeBanCount, pendingCount)

	return c.SendString(Layout("Admin Dashboard", body, currentUser))
}


package main

import (
	"context"
	"fmt"
	"html"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
)

// pendingReviewPage — shown to users whose account is still pending.
//
// Deliberately NOT routed through Layout(). Layout's bottom nav renders
// the notification/invite badges with hx-trigger="every 1s" / "every 10s"
// polling and loads htmx/morphdom — none of which a pending account is
// allowed to actually use, since AccountGateCheck already blocks every
// real endpoint for them. Left wired to Layout, a single pending session
// sitting on this page alone generated a real DB-backed request roughly
// once a second forever, and nothing stopped a rejected/banned user from
// scripting many such sessions at once. This page loads no JS at all —
// a pending session can read the message or click Log Out, and nothing
// else.
func pendingReviewPage(c *fiber.Ctx) error {
	username := html.EscapeString(getCurrentUser(c))
	page := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Account Under Review | Dostana</title></head>
<body style="margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;font-family:system-ui,sans-serif;background:#0f172a;padding:20px;">
    <div style="display:flex;flex-direction:column;align-items:center;justify-content:center;gap:20px;padding:40px 20px;text-align:center;max-width:420px;background:#111827;border:1px solid #1f2937;border-radius:20px;">
        <div style="font-size:48px">⏳</div>
        <h1 style="font-size:22px;font-weight:700;color:#fff;margin:0;">Account Under Review</h1>
        <p style="max-width:380px;color:#9ca3af;line-height:1.6;margin:0;">
            Hey <strong style="color:#e5e7eb;">%s</strong>, your account is pending admin approval.
            You'll have full access as soon as an admin reviews your details.
            This usually takes a short while.
        </p>
        <a href="/logout" style="padding:10px 24px;background:#ef4444;color:#fff;border-radius:8px;font-weight:700;font-size:14px;text-decoration:none">
            Log Out
        </a>
    </div>
</body></html>`, username)
	c.Set("Cache-Control", "no-store")
	return c.Type("html").SendString(page)
}

// adminPendingListHandler — admin page listing all pending accounts
func adminPendingListHandler(c *fiber.Ctx) error {
	currentUser, err := requireAdmin(c)
	if err != nil {
		return err
	}

	// Clear the "new pending" notification flag
	CacheDel("pending_notify:new")

	listCtx, listCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer listCancel()
	rows, err := db.Query(listCtx, `
        SELECT username, ip_address, subnet, device_key, hardware_id,
               bio, city, gender, birth_date, created_at
        FROM users
        WHERE account_status = 'pending'
        ORDER BY created_at ASC
        LIMIT 100`)
	if err != nil {
		log.Printf("adminPendingList error: %v", err)
		return c.Status(500).SendString("DB error")
	}
	defer rows.Close()

	type LinkedAccount struct {
		Username string
		Status   string
		Reason   string
	}

	type PendingUser struct {
		Username   string
		IP         string
		Subnet     string
		DeviceKey  string
		HardwareID string
		Bio        string
		City       string
		Gender     string
		BirthDate  string
		CreatedAt  string
		IsAdultStr string
		Linked     []LinkedAccount
	}

	var users []PendingUser
	var scanErrCount int
	for rows.Next() {
		var u PendingUser
		var birthDate *time.Time
		var createdAt time.Time
		var bio, city, gender, ip, subnet, deviceKey, hardwareID *string
		if err := rows.Scan(&u.Username, &ip, &subnet, &deviceKey, &hardwareID,
			&bio, &city, &gender, &birthDate, &createdAt); err != nil {
			log.Printf("adminPendingList scan error: %v", err)
			scanErrCount++
			continue
		}
		if ip != nil {
			u.IP = *ip
		}
		if subnet != nil {
			u.Subnet = *subnet
		}
		if deviceKey != nil {
			u.DeviceKey = *deviceKey
		}
		if hardwareID != nil {
			u.HardwareID = *hardwareID
		}
		if bio != nil {
			u.Bio = *bio
		}
		if city != nil {
			u.City = *city
		}
		if gender != nil {
			u.Gender = *gender
		}

		if birthDate != nil {
			u.BirthDate = birthDate.Format("2006-01-02")
			if IsAdult(*birthDate) {
				u.IsAdultStr = "✅ Adult (18+)"
			} else {
				u.IsAdultStr = "⚠️ Minor"
			}
		} else {
			u.BirthDate = "Not provided"
			u.IsAdultStr = "❓ Unknown"
		}
		u.CreatedAt = createdAt.Format("02 Jan 2006 03:04 PM")

		// ── Find linked accounts sharing same device/IP/subnet/hwid ──────────
		seen := map[string]bool{u.Username: true}
		type linkQuery struct{ reason, col, val string }
		queries := []linkQuery{
			{"Same device key", "device_key", u.DeviceKey},
			{"Same hardware ID", "hardware_id", u.HardwareID},
			{"Same IP", "ip_address", u.IP},
			{"Same subnet", "subnet", u.Subnet},
		}
		for _, q := range queries {
			if q.val == "" || q.val == "127.0.0.1" {
				continue
			}
			linkCtx, linkCancel := context.WithTimeout(context.Background(), 3*time.Second)
			lrows, lerr := db.Query(linkCtx, fmt.Sprintf(`
				SELECT username, COALESCE(account_status,'approved'), is_banned
				FROM users
				WHERE %s = $1 AND username != $2
				LIMIT 10`, q.col), q.val, u.Username)
			if lerr != nil {
				linkCancel()
				continue
			}
			for lrows.Next() {
				var lname, lstatus string
				var lbanned bool
				lrows.Scan(&lname, &lstatus, &lbanned)
				if seen[lname] {
					continue
				}
				seen[lname] = true
				display := lstatus
				if lbanned {
					display = "banned"
				}
				u.Linked = append(u.Linked, LinkedAccount{
					Username: lname,
					Status:   display,
					Reason:   q.reason,
				})
			}
			lrows.Close()
			linkCancel()
		}

		users = append(users, u)
	}

	if err := rows.Err(); err != nil {
		log.Printf("adminPendingList rows error: %v", err)
	}

	// Badge count is now derived live from the DB on every page load
	// (see getCachedPendingCount in layout.go) — nothing to reset here.

	cards := ""
	if len(users) == 0 {
		cards = `<div class="text-center py-16 text-gray-400 font-black text-lg">No pending accounts 🎉</div>`
	}

	for i, u := range users {
		cardID := fmt.Sprintf("pcard-%d", i)

		// ── Linked accounts HTML ──────────────────────────────────────────────
		linkedHTML := ""
		if len(u.Linked) > 0 {
			linkedRows := ""
			for _, la := range u.Linked {
				statusColor := "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-300"
				if la.Status == "banned" {
					statusColor = "bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-300"
				} else if la.Status == "pending" {
					statusColor = "bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300"
				}
				linkedRows += fmt.Sprintf(`
				<div class="flex items-center justify-between gap-2 py-2 border-b border-gray-100 dark:border-gray-700 last:border-0">
					<a href="/profile/%s" target="_blank"
					   class="font-black text-blue-600 dark:text-blue-400 text-sm hover:underline">@%s</a>
					<div class="flex items-center gap-2 flex-shrink-0">
						<span class="text-[10px] text-gray-400">%s</span>
						<span class="text-[10px] font-black px-2 py-0.5 rounded-full %s">%s</span>
					</div>
				</div>`, la.Username, la.Username, la.Reason, statusColor, la.Status)
			}
			linkedHTML = fmt.Sprintf(`
			<details class="mt-1">
				<summary class="cursor-pointer text-[11px] font-black text-amber-600 dark:text-amber-400 uppercase tracking-widest select-none flex items-center gap-1">
					⚠️ %d linked account(s) found — tap to review
				</summary>
				<div class="mt-2 bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-700 rounded-xl px-3 py-1">
					%s
				</div>
			</details>`, len(u.Linked), linkedRows)
		} else {
			linkedHTML = `<div class="text-[11px] text-emerald-600 dark:text-emerald-400 font-bold">✓ No linked accounts found</div>`
		}

		// ── Bio block ─────────────────────────────────────────────────────────
		bioHTML := ""
		if u.Bio != "" {
			bioHTML = fmt.Sprintf(`
			<div class="text-sm text-gray-700 dark:text-gray-300 bg-gray-50 dark:bg-gray-700/50 rounded-xl px-3 py-2">
				<span class="text-[10px] font-black text-gray-400 uppercase tracking-widest block mb-1">Bio</span>
				%s
			</div>`, u.Bio)
		}

		cards += fmt.Sprintf(`
		<details id="%s" open class="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-2xl shadow-sm overflow-hidden">

			<!-- ── Collapsible header (always visible) ── -->
			<summary class="flex items-center justify-between gap-3 px-4 py-4 cursor-pointer select-none list-none" onclick="">
				<div class="flex items-center gap-3 min-w-0">
					<div class="w-9 h-9 rounded-xl bg-indigo-100 dark:bg-indigo-900/40 flex items-center justify-center text-base flex-shrink-0 font-black text-indigo-600 dark:text-indigo-400">
						%s
					</div>
					<div class="min-w-0">
						<div class="font-black text-gray-900 dark:text-white text-base leading-tight truncate">@%s</div>
						<div class="text-[11px] text-gray-400 leading-tight">%s · %s · %s</div>
					</div>
				</div>
				<div class="flex items-center gap-2 flex-shrink-0">
					%s
					<svg class="w-4 h-4 text-gray-400 transition-transform details-chevron" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2.5">
						<path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7"/>
					</svg>
				</div>
			</summary>

			<!-- ── Expanded body ── -->
			<div class="px-4 pb-4 flex flex-col gap-4 border-t border-gray-100 dark:border-gray-700 pt-4">

				<!-- Action buttons -->
				<div class="flex gap-2">
					<form method="POST" action="/admin/pending/approve/%s" class="flex-1">
						<button class="w-full py-2.5 bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800 text-white font-black text-sm rounded-xl border-none cursor-pointer transition">
							✓ Approve
						</button>
					</form>
					<form method="POST" action="/admin/pending/reject/%s" class="flex-1"
					      onsubmit="return confirm('Reject and permanently DELETE @%s?')">
						<button class="w-full py-2.5 bg-red-500 hover:bg-red-600 active:bg-red-700 text-white font-black text-sm rounded-xl border-none cursor-pointer transition">
							✗ Reject & Delete
						</button>
					</form>
				</div>

				<!-- Identity grid -->
				<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
					<div class="bg-gray-50 dark:bg-gray-700/50 rounded-xl p-3">
						<span class="text-[10px] font-black text-gray-400 uppercase tracking-widest block mb-1">IP Address</span>
						<code class="text-gray-800 dark:text-gray-100 text-xs break-all font-mono">%s</code>
					</div>
					<div class="bg-gray-50 dark:bg-gray-700/50 rounded-xl p-3">
						<span class="text-[10px] font-black text-gray-400 uppercase tracking-widest block mb-1">Subnet</span>
						<code class="text-gray-800 dark:text-gray-100 text-xs break-all font-mono">%s</code>
					</div>
					<div class="bg-gray-50 dark:bg-gray-700/50 rounded-xl p-3 sm:col-span-2">
						<span class="text-[10px] font-black text-gray-400 uppercase tracking-widest block mb-1">Device Key</span>
						<code class="text-gray-800 dark:text-gray-100 text-xs break-all font-mono">%s</code>
					</div>
					<div class="bg-gray-50 dark:bg-gray-700/50 rounded-xl p-3 sm:col-span-2">
						<span class="text-[10px] font-black text-gray-400 uppercase tracking-widest block mb-1">Hardware ID</span>
						<code class="text-gray-800 dark:text-gray-100 text-xs break-all font-mono">%s</code>
					</div>
					<div class="bg-gray-50 dark:bg-gray-700/50 rounded-xl p-3">
						<span class="text-[10px] font-black text-gray-400 uppercase tracking-widest block mb-1">City</span>
						<span class="text-gray-800 dark:text-gray-100 text-sm">%s</span>
					</div>
					<div class="bg-gray-50 dark:bg-gray-700/50 rounded-xl p-3">
						<span class="text-[10px] font-black text-gray-400 uppercase tracking-widest block mb-1">Gender</span>
						<span class="text-gray-800 dark:text-gray-100 text-sm">%s</span>
					</div>
					<div class="bg-gray-50 dark:bg-gray-700/50 rounded-xl p-3">
						<span class="text-[10px] font-black text-gray-400 uppercase tracking-widest block mb-1">Birth Date</span>
						<span class="text-gray-800 dark:text-gray-100 text-sm">%s</span>
					</div>
					<div class="bg-gray-50 dark:bg-gray-700/50 rounded-xl p-3">
						<span class="text-[10px] font-black text-gray-400 uppercase tracking-widest block mb-1">Age Check</span>
						<span class="text-gray-800 dark:text-gray-100 text-sm">%s</span>
					</div>
				</div>

				%s
				%s

			</div>
		</details>`,
			cardID,
			firstLetter(u.Username), // first letter avatar
			u.Username, u.CreatedAt, orDash(u.City), u.IsAdultStr,
			func() string { // linked badge in header
				if len(u.Linked) > 0 {
					return fmt.Sprintf(`<span class="bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300 text-[10px] font-black px-2 py-0.5 rounded-full">%d linked</span>`, len(u.Linked))
				}
				return ""
			}(),
			u.Username, u.Username, u.Username,
			u.IP, u.Subnet, u.DeviceKey, u.HardwareID,
			orDash(u.City), orDash(u.Gender), u.BirthDate, u.IsAdultStr,
			linkedHTML,
			bioHTML,
		)
	}

	scanErrBanner := ""
	if scanErrCount > 0 {
		scanErrBanner = fmt.Sprintf(`<div class="bg-red-100 text-red-800 border border-red-300 rounded-xl px-4 py-2 mb-4 text-sm font-bold">
			⚠️ %d pending account(s) failed to load due to a data error and are NOT shown below. Check server logs for "adminPendingList scan error".
		</div>`, scanErrCount)
	}

	body := fmt.Sprintf(`
	<div class="max-w-2xl mx-auto px-3 py-5">
		%s
		<div class="flex items-center gap-3 mb-5 px-1">
			<span class="text-2xl">⏳</span>
			<h1 class="text-xl font-black text-gray-900 dark:text-white uppercase tracking-tight">
				Pending IDs
			</h1>
			<span class="bg-red-500 text-white text-xs font-black px-2.5 py-0.5 rounded-full">%d</span>
			<button onclick="document.querySelectorAll('details').forEach(d=>d.open=false)"
			        class="ml-auto text-[11px] font-black text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 uppercase tracking-widest">
				Collapse all
			</button>
		</div>
		<div class="flex flex-col gap-3">%s</div>
	</div>
	<style>
		details[open] .details-chevron { transform: rotate(180deg); }
		details summary::-webkit-details-marker { display: none; }
	</style>`, scanErrBanner, len(users), cards)

	return c.Type("html").SendString(Layout("Pending Accounts", body, currentUser))
}

func firstLetter(s string) string {
	if s == "" {
		return "?"
	}
	r := []rune(s)
	return string(r[0:1])
}

func adminApprovePendingHandler(c *fiber.Ctx) error {
	currentUser, err := requireAdmin(c)
	if err != nil {
		return err
	}
	target := c.Params("username")
	if _, err := db.Exec(context.Background(),
		`UPDATE users SET account_status='approved' WHERE username=$1 AND account_status='pending'`, target); err != nil {
		log.Printf("approve pending error: %v", err)
		return c.Status(500).SendString("DB error")
	}

	// Clear the 30s AccountGateCheck cache so the newly-approved account
	// gets full access on its very next request instead of waiting out
	// the stale cache window, and drop a short-lived flag so the next
	// page they load shows a one-time "you're approved" notice.
	CacheDel("acct_gate:" + target)
	rdb.Set(ctx, "approval_notice:"+target, "1", 15*time.Minute)

	log.Printf("✅ Admin %s approved pending account: %s", currentUser, target)
	return c.Redirect("/admin/pending")
}


func adminRejectPendingHandler(c *fiber.Ctx) error {
	currentUser, err := requireAdmin(c)
	if err != nil {
		return err
	}
	target := c.Params("username")

	// Block this exact username from being reused before deleting the row,
	// so the same rejected person can't just resubmit under the identical name.
	if _, err := db.Exec(context.Background(),
		`INSERT INTO banned_usernames (username) VALUES ($1) ON CONFLICT DO NOTHING`, target); err != nil {
		log.Printf("reject pending: banned_usernames insert error: %v", err)
	}

	// Hard delete — they were never really a member
	if _, err := db.Exec(context.Background(),
		`DELETE FROM users WHERE username=$1 AND account_status='pending'`, target); err != nil {
		log.Printf("reject pending error: %v", err)
		return c.Status(500).SendString("DB error")
	}

	// Kill every live session for this username immediately and clear the
	// gate cache — without this a rejected user's browser tab can keep
	// working for up to 30s (the AccountGateCheck cache TTL) before they
	// get bounced to the "rejected" login screen.
	revokeSession(target)
	CacheDel("acct_gate:" + target)

	log.Printf("🗑 Admin %s rejected+deleted pending account: %s", currentUser, target)
	return c.Redirect("/admin/pending")
}


// ── helpers ───────────────────────────────────────────────────────────────

func isAdmin(username string) bool {
	if username == "" {
		return false
	}
	var userType string
	dbCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	db.QueryRow(dbCtx, "SELECT user_type FROM users WHERE username=$1", username).Scan(&userType)
	return userType == "admin"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// renderApprovalNoticeBanner is a one-time, dismissible banner shown on
// the next home-page load right after an admin approves a pending
// account (see adminApprovePendingHandler, which drops the
// "approval_notice:<username>" Redis flag that homeGet consumes).
func renderApprovalNoticeBanner() string {
	return `
	<div id="approval-notice" style="display:flex;align-items:center;gap:12px;background:#d1fae5;border:1.5px solid #6ee7b7;color:#047857;padding:14px 16px;border-radius:16px;margin-bottom:14px;font-weight:700;font-size:13px;">
		<span style="font-size:20px;flex-shrink:0;">🎉</span>
		<span style="flex:1;">Your account has been approved! You now have full access — post, follow, chat, and more.</span>
		<button type="button" onclick="var n=document.getElementById('approval-notice'); if(n) n.remove();"
			aria-label="Close"
			style="flex-shrink:0;background:none;border:none;color:#047857;font-size:18px;font-weight:900;cursor:pointer;line-height:1;padding:2px 6px;">
			✕
		</button>
	</div>`
}

// renderPendingApprovalBanner reminds a pending user, on every page load
// while they wait, that they can browse but can't interact yet — matches
// the 403 message AccountGateCheck returns if they try anyway.
func renderPendingApprovalBanner() string {
	return `
	<div id="pending-approval-notice" style="display:flex;align-items:center;gap:12px;background:#fef3c7;border:1.5px solid #fcd34d;color:#92400e;padding:14px 16px;border-radius:16px;margin-bottom:14px;font-weight:700;font-size:13px;">
		<span style="font-size:20px;flex-shrink:0;">⏳</span>
		<span style="flex:1;">Your account is awaiting admin approval. You can look around, but posting, following, reacting, and chatting are locked until then.</span>
		<button type="button" onclick="var n=document.getElementById('pending-approval-notice'); if(n) n.remove();"
			aria-label="Close"
			style="flex-shrink:0;background:none;border:none;color:#92400e;font-size:18px;font-weight:900;cursor:pointer;line-height:1;padding:2px 6px;">
			✕
		</button>
	</div>`
}

func bioBlock(bio string) string {
	if bio == "" {
		return ""
	}
	return fmt.Sprintf(`<div class="text-sm pt-1 border-t border-gray-100 dark:border-gray-700"><span class="text-gray-400 font-bold">Bio:</span> <span class="text-gray-800 dark:text-gray-200">%s</span></div>`, bio)
}

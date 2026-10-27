package main

import (
	"context"
	"database/sql" // CRITICAL for sql.NullString
	"fmt"
	htmlesc "html"
	"log" // CRITICAL for log.Println
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// notifRow is the normalized shape every notification (whichever table or
// type it came from) gets scanned into, so one renderer can handle all of
// them consistently instead of a growing switch of copy-pasted HTML blocks.
type notifRow struct {
	ID, PostID, CommentID, MehfilID   int
	Sender, Content, ProfilePic       string
	PostBody, Type                    string
	RepliedToSnippet, RepliedToAuthor string
	CreatedAt                         time.Time
	IsRead                            bool
}

// notifStyle holds the FULL literal Tailwind class strings for a
// notification type. These are written out per-case rather than built by
// concatenating a color name at runtime — Tailwind can only pick up classes
// it can see as complete literal strings.
type notifStyle struct {
	Icon       string
	Label      string
	CardBorder string
	IconBg     string
}

func styleForNotifType(ntype string) notifStyle {
	switch {
	case ntype == "follow_request":
		return notifStyle{"👋", "Follow Request", "border-amber-100 dark:border-amber-900/30", "bg-amber-500/10 text-amber-600"}
	case ntype == "follow_request_accepted":
		return notifStyle{"✅", "Request Accepted", "border-emerald-100 dark:border-emerald-900/30", "bg-emerald-500/10 text-emerald-600"}
	case ntype == "follow_request_rejected":
		return notifStyle{"🚫", "Request Declined", "border-slate-200 dark:border-slate-700", "bg-slate-500/10 text-slate-500"}
	case ntype == "follow":
		return notifStyle{"➕", "New Follower", "border-emerald-100 dark:border-emerald-900/20", "bg-emerald-500/10 text-emerald-600"}
	case ntype == "chat_invite":
		return notifStyle{"💌", "Chat Invite", "border-purple-100 dark:border-purple-900/30", "bg-purple-500/10 text-purple-600"}
	case ntype == "chat_accept":
		return notifStyle{"🤝", "Invite Accepted", "border-blue-100 dark:border-blue-900/30", "bg-blue-500/10 text-blue-600"}
	case ntype == "chat_message":
		return notifStyle{"💬", "New Message", "border-blue-100 dark:border-blue-900/30", "bg-blue-500/10 text-blue-600"}
	case ntype == "dp_competition":
		return notifStyle{"🥊", "DP Competition", "border-orange-100 dark:border-orange-900/30", "bg-orange-500/10 text-orange-600"}
	case ntype == "dp_competition_result":
		return notifStyle{"🏆", "Competition Result", "border-purple-100 dark:border-purple-900/30", "bg-purple-500/10 text-purple-600"}
	case ntype == "mehfil_kick":
		return notifStyle{"🚪", "Action Taken", "border-red-100 dark:border-red-900/30", "bg-red-500/10 text-red-600"}
	case ntype == "mehfil_role":
		return notifStyle{"🎖️", "Rank Update", "border-amber-100 dark:border-amber-900/30", "bg-amber-500/10 text-amber-600"}
	case ntype == "mehfil_reply":
		return notifStyle{"💬", "New Message", "border-emerald-100 dark:border-emerald-900/20", "bg-emerald-500/10 text-emerald-600"}
	case ntype == "mehfil_join":
		return notifStyle{"🎉", "New Member", "border-orange-100 dark:border-orange-900/30", "bg-orange-500/10 text-orange-600"}
	case ntype == "mehfil_invite":
		return notifStyle{"➕", "Mehfil Invite", "border-emerald-100 dark:border-emerald-900/30", "bg-emerald-500/10 text-emerald-600"}
	case strings.HasPrefix(ntype, "mehfil_"):
		// Any Mehfil event not explicitly listed above — kick/role/join
		// etc. are handled specifically, but this catches things like a
		// member-leave notification, topic/fee/rules updates, or any
		// future mehfil_* type. Previously anything not in the switch
		// fell all the way through to the "post reply" default case
		// below and rendered as "@user replied" with an empty quoted
		// post — this is the inconsistent design you saw. Now it always
		// gets one sane fallback card instead.
		return notifStyle{"🔔", "Mehfil Alert", "border-slate-100 dark:border-white/5", "bg-blue-500/10 text-blue-500"}
	case ntype == "reaction":
		return notifStyle{"❤️", "New Reaction", "border-pink-100 dark:border-pink-900/30", "bg-pink-500/10 text-pink-600"}
	case ntype == "mention", ntype == "comment_mention":
		return notifStyle{"📣", "Mention", "border-blue-100 dark:border-blue-900/30", "bg-blue-500/10 text-blue-600"}
	case ntype == "reply" || ntype == "":
		return notifStyle{"💬", "New Reply", "border-gray-100 dark:border-gray-700", "bg-blue-500/10 text-blue-600"}
	default:
		return notifStyle{"🔔", "Notification", "border-gray-100 dark:border-gray-700", "bg-slate-500/10 text-slate-500"}
	}
}

// notifMessageAndAction builds the headline text and the single primary
// action button for a notification, based on its type.
func notifMessageAndAction(row notifRow) (message, actionLabel, actionURL string) {
	sender := htmlesc.EscapeString(row.Sender)
	switch {
	case row.Type == "follow_request":
		return fmt.Sprintf(`<b>@%s</b> wants to follow you`, sender), "Review", "/profile/" + row.Sender
	case row.Type == "follow_request_accepted":
		return fmt.Sprintf(`<b>@%s</b> accepted your follow request`, sender), "View Profile", "/profile/" + row.Sender
	case row.Type == "follow_request_rejected":
		return fmt.Sprintf(`<b>@%s</b> declined your follow request`, sender), "View Profile", "/profile/" + row.Sender
	case row.Type == "follow":
		return fmt.Sprintf(`<b>@%s</b> started following you`, sender), "View Profile", "/profile/" + row.Sender
	case row.Type == "chat_invite":
		return fmt.Sprintf(`<b>@%s</b> sent you a chat invite`, sender), "View Invite", "/invites"
	case row.Type == "chat_accept":
		return fmt.Sprintf(`<b>@%s</b> accepted your chat invite`, sender), "Chat Now", "/chat/" + row.Sender
	case row.Type == "chat_message":
		return fmt.Sprintf(`<b>@%s</b> sent you a message`, sender), "Reply", "/chat/" + row.Sender
	case row.Type == "dp_competition":
		return fmt.Sprintf(`<b>@%s</b> started a DP competition with you`, sender), "View", fmt.Sprintf("/post/%d", row.PostID)
	case row.Type == "dp_competition_result":
		return htmlesc.EscapeString(row.Content), "View", fmt.Sprintf("/post/%d", row.PostID)
	case row.Type == "mehfil_join":
		return htmlesc.EscapeString(row.Content), "Say Welcome", fmt.Sprintf("/view/%d", row.MehfilID)
	case row.Type == "mehfil_reply":
		return htmlesc.EscapeString(row.Content), "View Conversation", fmt.Sprintf("/view/%d#msg-%d", row.MehfilID, row.CommentID)
	case row.Type == "mehfil_invite":
		// No single action link — Accept/Reject render as two buttons via
		// notifInviteButtonsHTML instead of the generic actionBtn.
		return htmlesc.EscapeString(row.Content), "", ""
	case strings.HasPrefix(row.Type, "mehfil_"):
		return htmlesc.EscapeString(row.Content), "Enter Mehfil", fmt.Sprintf("/view/%d", row.MehfilID)
	case row.Type == "reaction":
		return fmt.Sprintf(`<b>@%s</b> %s`, sender, htmlesc.EscapeString(row.Content)), "View Post", fmt.Sprintf("/post/%d", row.PostID)
	case row.Type == "mention":
		return fmt.Sprintf(`<b>@%s</b> mentioned you in a post`, sender), "View Post", fmt.Sprintf("/post/%d", row.PostID)
	case row.Type == "comment_mention":
		return fmt.Sprintf(`<b>@%s</b> mentioned you in a comment`, sender), "View Post", fmt.Sprintf("/post/%d", row.PostID)
	default: // "reply" / "" — a comment on the post, or a reply to one of its comments
		if row.RepliedToAuthor != "" {
			return fmt.Sprintf(`<b>@%s</b> replied to your comment`, sender), "View Post", fmt.Sprintf("/post/%d", row.PostID)
		}
		return fmt.Sprintf(`<b>@%s</b> replied to your post`, sender), "View Post", fmt.Sprintf("/post/%d", row.PostID)
	}
}

// notifSnippetsHTML shows a truncated preview of the post the notification
// is about, and — when this is a reply to a specific comment rather than
// the post itself — a truncated preview of that comment too, so the user
// can tell at a glance which post and which comment a notification concerns
// without opening it.
func notifSnippetsHTML(row notifRow) string {
	var b strings.Builder
	if row.PostBody != "" {
		b.WriteString(fmt.Sprintf(
			`<div class="mt-2 pl-3 border-l-2 border-gray-200 dark:border-gray-700 text-[11px] text-gray-500 dark:text-gray-400 italic truncate">On: "%s"</div>`,
			htmlesc.EscapeString(truncateRunes(row.PostBody, 60))))
	}
	if row.RepliedToSnippet != "" {
		b.WriteString(fmt.Sprintf(
			`<div class="mt-1 pl-3 border-l-2 border-blue-200 dark:border-blue-800 text-[11px] text-blue-500 dark:text-blue-400 italic truncate">Your comment: "%s"</div>`,
			htmlesc.EscapeString(truncateRunes(row.RepliedToSnippet, 60))))
	}
	return b.String()
}

// notifReplyBubbleHTML shows the actual new comment/reply text (truncated)
// as a small chat bubble, only for plain post-reply notifications — for
// other types the headline message already carries the content.
func notifReplyBubbleHTML(row notifRow) string {
	if row.Type != "" && row.Type != "reply" && row.Type != "comment_mention" {
		return ""
	}
	if row.Content == "" {
		return ""
	}
	return fmt.Sprintf(
		`<div class="inline-block bg-blue-600 text-white px-3 py-1.5 rounded-xl rounded-tl-none text-xs mt-2 max-w-full break-words">%s</div>`,
		truncateRunes(row.Content, 140))
}

// notifReplyFormHTML renders an inline reply box so the user can respond
// straight from the notifications page. It posts to the existing
// /comment/{id}/reply endpoint, using CommentID — the id of the comment
// that triggered this notification — as the parent, so the reply threads
// correctly whether the notification was for a top-level comment on the
// post or a reply to one of its comments.
func notifReplyFormHTML(row notifRow) string {
	replyable := row.Type == "" || row.Type == "reply" || row.Type == "comment_mention" || row.Type == "mention"
	if !replyable {
		return ""
	}

	// A plain reply / comment-mention notification carries a CommentID —
	// the comment it happened in — so the box threads straight under
	// that comment. A post-level mention has no comment to thread under,
	// so it falls back to posting a fresh top-level comment on the post.
	var action string
	switch {
	case row.CommentID > 0:
		action = fmt.Sprintf("/comment/%d/reply", row.CommentID)
	case row.PostID > 0:
		action = fmt.Sprintf("/comment/%d", row.PostID)
	default:
		return ""
	}

	return fmt.Sprintf(`
	<form class="notif-reply-form mt-3 flex gap-2"
	      hx-post="%s"
	      hx-swap="none">
	  <input name="content" autocomplete="off" placeholder="Write a reply…"
	         class="flex-1 min-w-0 px-3 py-2 bg-gray-50 dark:bg-gray-800 border border-transparent rounded-xl focus:bg-white dark:focus:bg-gray-700 outline-none text-xs">
	  <button type="submit" class="bg-blue-600 text-white font-bold px-4 rounded-xl text-xs shrink-0">Reply</button>
	</form>
	<div class="text-[11px] font-bold mt-1"></div>`, action)
}

// notifInviteButtonsHTML renders Accept/Reject buttons for a Mehfil invite
// notification. Accept is a plain GET link (full navigation straight into
// the Mehfil, same pattern as notificationViewHandler/skipNotificationHandler
// elsewhere in this file). Reject is an htmx POST that swaps the whole card
// out for a small "rejected" confirmation.
func notifInviteButtonsHTML(row notifRow) string {
	if row.Type != "mehfil_invite" {
		return ""
	}
	return fmt.Sprintf(`
		<a href="/mehfil/invite-respond/%d/accept" class="inline-flex items-center gap-1 text-[10px] font-black uppercase tracking-wide text-white bg-emerald-600 hover:bg-emerald-700 px-4 py-2 rounded-xl transition">✅ Accept</a>
		<button hx-post="/mehfil/invite-respond/%d/reject" hx-target="#notif-%d" hx-swap="outerHTML"
		        class="inline-flex items-center gap-1 text-[10px] font-black uppercase tracking-wide text-red-500 border border-red-200 dark:border-red-900/40 hover:bg-red-500 hover:text-white px-4 py-2 rounded-xl transition">❌ Reject</button>`,
		row.MehfilID, row.MehfilID, row.ID)
}

// renderNotificationCard is the single template every notification type
// renders through, so the whole feed has one consistent look — an
// icon+label header, sender line, optional post/comment snippets, optional
// reply bubble, one primary action, a per-item dismiss button, and (for
// plain post replies) an inline reply box.
func renderNotificationCard(row notifRow) string {
	style := styleForNotifType(row.Type)
	message, actionLabel, actionURL := notifMessageAndAction(row)

	unreadClass := ""
	if !row.IsRead {
		unreadClass = "ring-2 ring-blue-500/30 bg-blue-50/40 dark:bg-blue-900/10"
	}

	initial := "?"
	if row.Sender != "" {
		initial = strings.ToUpper(row.Sender[:1])
	}
	avatar := fmt.Sprintf(`<div class="w-11 h-11 rounded-2xl bg-blue-600 shrink-0 flex items-center justify-center text-white font-bold text-sm">%s</div>`, initial)
	if row.ProfilePic != "" {
		avatar = fmt.Sprintf(`<img src="%s" class="w-11 h-11 rounded-2xl object-cover shrink-0">`, row.ProfilePic)
	}

	actionBtn := ""
	if actionLabel != "" && actionURL != "" {
		actionBtn = fmt.Sprintf(`<a href="%s" class="inline-flex items-center gap-1 text-[10px] font-black uppercase tracking-wide text-white bg-blue-600 hover:bg-blue-700 px-4 py-2 rounded-xl transition">%s →</a>`, actionURL, actionLabel)
	}

	dismissBtn := fmt.Sprintf(`
		<button hx-delete="/notifications/%s/%d/dismiss" hx-target="#notif-%d" hx-swap="outerHTML"
		        class="text-gray-300 hover:text-red-500 transition p-1 shrink-0" title="Dismiss">✕</button>`,
		row.Type, row.ID, row.ID)

	return fmt.Sprintf(`
	<div id="notif-%d" class="bg-white dark:bg-[#0f0f0f] p-4 mb-3 rounded-[24px] border %s %s transition-all">
		<div class="flex items-start gap-3">
			%s
			<div class="min-w-0 flex-1">
				<div class="flex items-center justify-between gap-2 mb-0.5">
					<span class="text-[9px] font-black uppercase tracking-[0.15em] text-slate-400 dark:text-slate-500 flex items-center gap-1.5">
						<span class="inline-flex items-center justify-center w-5 h-5 rounded-lg %s text-[11px]">%s</span>
						%s
					</span>
					<div class="flex items-center gap-2 shrink-0">
						<span class="text-[9px] font-bold text-slate-400/70 uppercase">%s</span>
						%s
					</div>
				</div>
				<p class="text-[13px] font-semibold text-slate-900 dark:text-white leading-snug break-words">%s</p>
				%s
				%s
				<div class="flex items-center gap-2 mt-3">%s</div>
				%s
			</div>
		</div>
	</div>`,
		row.ID, style.CardBorder, unreadClass,
		avatar,
		style.IconBg, style.Icon, style.Label,
		formatTwitterTime(row.CreatedAt), dismissBtn,
		message,
		notifSnippetsHTML(row),
		notifReplyBubbleHTML(row),
		actionBtn+notifInviteButtonsHTML(row),
		notifReplyFormHTML(row),
	)
}

// clearAllNotificationsHandler wipes every notification for the current
// user. Two plain DELETEs on an already-indexed column (recipient) —
// cheap regardless of how many rows exist, no per-row work, no loop.
func clearAllNotificationsHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Status(fiber.StatusUnauthorized).SendString("")
	}

	delCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	if _, err := db.Exec(delCtx, "DELETE FROM notifications WHERE recipient = $1", currentUser); err != nil {
		log.Printf("clearAllNotificationsHandler notifications delete error: %v", err)
		return c.Status(500).SendString(`<p class="text-center text-red-400 py-10 text-sm font-bold">⚠️ Notifications clear nahi ho sakin, dobara koshish karein.</p>`)
	}
	if _, err := db.Exec(delCtx, "DELETE FROM chat_notifications WHERE recipient = $1", currentUser); err != nil {
		log.Printf("clearAllNotificationsHandler chat_notifications delete error: %v", err)
		return c.Status(500).SendString(`<p class="text-center text-red-400 py-10 text-sm font-bold">⚠️ Notifications clear nahi ho sakin, dobara koshish karein.</p>`)
	}

	c.Set("HX-Trigger", "refreshNotifications")
	c.Set("Content-Type", "text/html")
	return c.SendString(`<p class="text-center text-gray-400 py-20 font-medium">No notifications yet!</p>`)
}

// notifDismissHandler removes a single notification. type tells us which
// table it lives in — chat_message notifications come from
// chat_notifications, everything else from notifications. Both deletes
// are by primary key + recipient — indexed, single-row, cheap.
func notifDismissHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Status(fiber.StatusUnauthorized).SendString("")
	}
	id := c.Params("id")
	ntype := c.Params("type")

	delCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if ntype == "chat_message" {
		db.Exec(delCtx, "DELETE FROM chat_notifications WHERE id=$1 AND recipient=$2", id, currentUser)
	} else {
		db.Exec(delCtx, "DELETE FROM notifications WHERE id=$1 AND recipient=$2", id, currentUser)
	}
	return c.SendString("")
}

// --- 1. HELPER: Get Unread Count ---
func getNotificationCount(username string) int {
	var count int
	// We use a subquery to sum unread counts from both notification tables
	query := `
        SELECT 
            (SELECT COUNT(*) FROM notifications WHERE recipient = $1 AND is_read = FALSE) +
            (SELECT COUNT(*) FROM chat_notifications WHERE recipient = $1 AND is_read = FALSE)
    `
	err := db.QueryRow(ctx, query, username).Scan(&count)
	if err != nil {
		log.Printf("Error counting notifications: %v", err)
		return 0
	}
	return count
}

// --- 2. HELPER: Mark Read & Redirect ---
func notificationViewHandler(c *fiber.Ctx) error {
	notificationID := c.Params("id")
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Redirect("/login")
	}

	var postID int
	db.QueryRow(ctx, `UPDATE notifications SET is_read = TRUE WHERE id = $1 AND recipient = $2 RETURNING post_id`,
		notificationID, currentUser).Scan(&postID)

	return c.Redirect(fmt.Sprintf("/post/%d", postID))
}

// --- 3. HELPER: Skip Notification ---
func skipNotificationHandler(c *fiber.Ctx) error {
	notificationID := c.Params("id")
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Redirect("/login")
	}

	db.Exec(ctx, "UPDATE notifications SET is_read = TRUE WHERE id = $1 AND recipient = $2", notificationID, currentUser)
	return c.Redirect("/notifications")
}

// --- 4. MAIN HANDLER: Notifications Feed ---
func notificationsHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Redirect("/login")
	}

	// Two extra LEFT JOINs on comments (by primary key — indexed
	// automatically) so we can also show what comment a reply-to-a-comment
	// notification was about: rc is the comment that triggered the
	// notification, rpc is the comment it replied to (NULL for a plain
	// top-level comment on the post).
	rows, err := db.Query(ctx, `
SELECT id, sender, content, created_at, post_id, comment_id, type, profile_pic, post_body, is_read, mehfil_id, replied_to_snippet, replied_to_author
FROM (
    SELECT 
        n.id, n.sender, n.content, n.created_at, 
        COALESCE(n.post_id, 0) as post_id, 
        COALESCE(n.comment_id, 0) as comment_id, 
        n.type,
        COALESCE(u.profile_image, '') as profile_pic, 
        COALESCE(p.content, '') as post_body,
        n.is_read,
        COALESCE(n.mehfil_id, 0) as mehfil_id,
        COALESCE(LEFT(rpc.content, 60), '') as replied_to_snippet,
        COALESCE(rpc.author, '') as replied_to_author
    FROM notifications n
    LEFT JOIN users u ON n.sender = u.username
    LEFT JOIN posts p ON n.post_id = p.id
    LEFT JOIN comments rc ON n.comment_id = rc.id
    LEFT JOIN comments rpc ON rc.parent_comment_id = rpc.id
    WHERE n.recipient = $1
    
    UNION ALL

    SELECT 
        cn.id, cn.sender, 'sent you a message' as content, cn.created_at, 
        0 as post_id, 0 as comment_id, 'chat_message' as type, 
        COALESCE(u.profile_image, '') as profile_pic,
        '' as post_body, 
        cn.is_read,
        0 as mehfil_id,
        '' as replied_to_snippet,
        '' as replied_to_author
    FROM chat_notifications cn
    LEFT JOIN users u ON cn.sender = u.username
    WHERE cn.recipient = $1
) AS combined
ORDER BY created_at DESC LIMIT 30`, currentUser)

	if err != nil {
		log.Println("SQL Query Error:", err)
		return c.SendString("Error loading notifications")
	}
	defer rows.Close()

	var listBuf strings.Builder
	hasNotifications := false

	for rows.Next() {
		var row notifRow
		var nTypeRaw sql.NullString
		err := rows.Scan(&row.ID, &row.Sender, &row.Content, &row.CreatedAt, &row.PostID, &row.CommentID,
			&nTypeRaw, &row.ProfilePic, &row.PostBody, &row.IsRead, &row.MehfilID,
			&row.RepliedToSnippet, &row.RepliedToAuthor)
		if err != nil {
			log.Printf("SCAN ERROR: %v", err)
			continue
		}
		row.Type = strings.TrimSpace(nTypeRaw.String)
		hasNotifications = true
		listBuf.WriteString(renderNotificationCard(row))
	}

	// Mark everything read, then tell the navbar badge to refresh.
	_, _ = db.Exec(ctx, "UPDATE notifications SET is_read = TRUE WHERE recipient = $1 AND is_read = FALSE", currentUser)
	_, _ = db.Exec(ctx, "UPDATE chat_notifications SET is_read = TRUE WHERE recipient = $1 AND is_read = FALSE", currentUser)
	c.Set("HX-Trigger", "refreshNotifications")

	listHTML := listBuf.String()
	if !hasNotifications {
		listHTML = `<p class="text-center text-gray-400 py-20 font-medium">No notifications yet!</p>`
	}

	clearAllBtn := ""
	if hasNotifications {
		clearAllBtn = `
		<button hx-delete="/notifications/clear" hx-target="#notif-list" hx-swap="innerHTML"
		        hx-confirm="Clear ALL notifications? Ye wapis nahi aa sakti."
		        class="inline-flex items-center gap-1.5 text-[11px] font-black uppercase tracking-wide text-red-500 hover:text-white hover:bg-red-500 border border-red-200 dark:border-red-900/40 px-3 py-2 rounded-xl transition">
		  🗑️ Clear All
		</button>`
	}

	pageHTML := fmt.Sprintf(`
<div class="max-w-xl mx-auto py-10 px-4">
  <div class="flex items-center justify-between mb-8">
    <h1 class="text-3xl font-black dark:text-white tracking-tighter">Notifications</h1>
    %s
  </div>
  <div id="notif-list">%s</div>
</div>

<!-- Bootstrap trick (same one used on /share): a <script> tag inserted
     via an htmx innerHTML/boosted swap never executes — that's a
     deliberate browser rule. An inline event-handler ATTRIBUTE like
     onerror below fires regardless of how the DOM was inserted, so this
     invisible image is what actually attaches the listener. Guarded by
     window.__notifReplyBound so navigating here more than once in the
     same tab doesn't attach it twice. Real addEventListener (not an
     hx-on:: attribute) because hx-on:: relies on Function(), which this
     site's CSP (script-src 'unsafe-inline' only, no 'unsafe-eval')
     silently blocks. -->
<img src="data:," alt="" style="display:none" width="0" height="0"
     onerror="this.remove(); if(window.__notifReplyBound) return; window.__notifReplyBound = true;
       document.body.addEventListener('htmx:afterRequest', function(evt){
         var form = evt.detail.elt;
         if (!form || !form.classList || !form.classList.contains('notif-reply-form')) return;
         var statusEl = form.nextElementSibling;
         if (!statusEl) return;
         if (evt.detail.successful) {
           statusEl.textContent = '✅ Reply sent!';
           statusEl.className = 'text-[11px] font-bold mt-1 text-emerald-600';
           form.reset();
           var inp = form.querySelector('input'); if (inp) inp.disabled = true;
           var btn = form.querySelector('button'); if (btn) btn.disabled = true;
         } else {
           statusEl.textContent = '⚠️ Reply nahi ho saka, dobara koshish karein.';
           statusEl.className = 'text-[11px] font-bold mt-1 text-red-500';
         }
       });">`, clearAllBtn, listHTML)

	c.Set("Content-Type", "text/html")
	return c.SendString(Layout("Notifications", pageHTML, currentUser))
}
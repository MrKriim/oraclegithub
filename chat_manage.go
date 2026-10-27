package main

import (
	"fmt"
	"html"
	"io"
	"log"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// ─────────────────────────────────────────────────────────────────────────────
// TIME AGO HELPER
// ─────────────────────────────────────────────────────────────────────────────

func timeAgo(t time.Time) string {
	loc, _ := time.LoadLocation("Asia/Karachi")
	now := time.Now().In(loc)
	t = t.In(loc)
	diff := now.Sub(t)

	switch {
	case diff < 5*time.Second:
		return "just now"
	case diff < 60*time.Second:
		s := int(math.Round(diff.Seconds()))
		return fmt.Sprintf("%ds ago", s)
	case diff < 60*time.Minute:
		m := int(diff.Minutes())
		if m == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d mins ago", m)
	case diff < 24*time.Hour:
		h := int(diff.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	case diff < 7*24*time.Hour:
		d := int(diff.Hours() / 24)
		if d == 1 {
			return "yesterday"
		}
		return fmt.Sprintf("%d days ago", d)
	case diff < 4*7*24*time.Hour:
		w := int(diff.Hours() / (24 * 7))
		if w == 1 {
			return "1 week ago"
		}
		return fmt.Sprintf("%d weeks ago", w)
	case diff < 365*24*time.Hour:
		mo := int(diff.Hours() / (24 * 30))
		if mo <= 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", mo)
	default:
		y := int(diff.Hours() / (24 * 365))
		if y == 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", y)
	}
}

// Clock time for bubble meta (e.g. "3:45 PM")
func clockTime(t time.Time) string {
	loc, _ := time.LoadLocation("Asia/Karachi")
	return t.In(loc).Format("3:04 PM")
}

// ─────────────────────────────────────────────────────────────────────────────
// TICK ICONS
// ─────────────────────────────────────────────────────────────────────────────

func tickHTML(isRead bool) string {
	if isRead {
		return `<span class="chv-ticks read" title="Seen">
			<svg viewBox="0 0 18 18" fill="none">
				<path d="M1 9l4 4L12 4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
				<path d="M5 9l4 4 7-9" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
			</svg>
		</span>`
	}
	return `<span class="chv-ticks sent" title="Sent">
		<svg viewBox="0 0 18 18" fill="none">
			<path d="M1 9l4 4 8-8" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
			<path d="M6 9l4 4 8-8" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
		</svg>
	</span>`
}

type chatMessage struct {
	ID          int
	Sender      string
	Recipient   string
	Content     string
	ImageURL    string
	VoiceURL    string
	IsRead      bool
	CreatedAt   time.Time
	ReplyToID   *int
	ReplyText   string
	ReplyAuthor string
	ReplyHidden bool
}

func renderBubble(msg chatMessage, currentUser, otherUser, otherAvatar string, isLastMe bool) string {
	isMe := strings.EqualFold(msg.Sender, currentUser)
	side := "other"
	if isMe {
		side = "me"
	}

	timeAgoStr := timeAgo(msg.CreatedAt)
	clockStr := clockTime(msg.CreatedAt)
	ts := msg.CreatedAt.Unix()

	// ── Seen avatar ────────────────────────────────────────────────────────
	seenAvatarHTML := ""
	if isMe && isLastMe {
		if msg.IsRead && otherAvatar != "" {
			seenAvatarHTML = fmt.Sprintf(`<img src="%s" class="chv-msg-seen-avatar visible" title="%s has seen this">`, otherAvatar, otherUser)
		} else if !msg.IsRead {
			seenAvatarHTML = `<img class="chv-msg-seen-avatar" src="">`
		}
	}

	// ── Reply quote ────────────────────────────────────────────────────────
	replyQuoteHTML := ""
	if msg.ReplyToID != nil && *msg.ReplyToID > 0 {
		var replyAuthor, replyText string
		if msg.ReplyHidden {
			replyAuthor = "Unavailable"
			replyText = "Original message no longer available"
		} else {
			replyAuthor = html.EscapeString(msg.ReplyAuthor)
			replyText = html.EscapeString(msg.ReplyText)
			if msg.ReplyText == "" {
				replyText = "📷 Image"
			}
		}
		replyQuoteHTML = fmt.Sprintf(`
			<div class="chv-reply-quote" onclick="jumpToMsg(%d)">
				<div class="chv-reply-quote-author">%s</div>
				<div class="chv-reply-quote-text">%s</div>
			</div>`, *msg.ReplyToID, replyAuthor, replyText)
	}

	// ── Bubble content ────────────────────────────────────────────────────
	var bodyHTML string

	switch msg.Content {

	case "__IMG_PERMISSION_REQUEST__":
		// FIX: The requester sees their own request as a plain note.
		// The approver sees the Approve/Deny buttons.
		// We use a data attribute instead of an inline <script> tag because
		// scripts injected via innerHTML do NOT execute in modern browsers.
		if isMe {
			bodyHTML = `<span style="font-style:italic;opacity:0.8;font-size:13px;">📷 You requested image permission</span>`
		} else {
			bodyHTML = fmt.Sprintf(`
				<div class="chv-perm-request-msg">
					📷 <strong>%s</strong> wants to send you an image.<br>
					<button class="chv-perm-approve-btn" onclick="approveImagePermission(%d,'%s')">✓ Allow</button>
					<button class="chv-perm-deny-btn" onclick="denyImagePermission(%d,'%s')">✕ Deny</button>
				</div>`, msg.Sender, msg.ID, msg.Sender, msg.ID, msg.Sender)
		}

	case "__IMG_PERMISSION_GRANTED__":
		// FIX: Instead of a <script> tag (which does NOT run when injected via
		// innerHTML), we use a data-chv-action attribute. The poll's DOM
		// insertion code in chat_view.go reads this attribute and calls the
		// correct JS function directly. This is the correct, reliable pattern.
		if isMe {
			// "isMe" here means: the APPROVER is viewing their own grant message.
			// No action needed on their side.
			bodyHTML = `<span style="font-style:italic;opacity:0.8;font-size:13px;">✓ You allowed image sharing</span>`
		} else {
			// The REQUESTER sees this message arriving via poll.
			// data-chv-action tells the JS poll handler to call onImgPermissionGranted().
			bodyHTML = `<span style="font-style:italic;opacity:0.8;font-size:13px;color:#25D366;" data-chv-action="img-permission-granted">✓ Image permission granted! You can now send images.</span>`
		}

	case "__IMG_PERMISSION_DENIED__":
		// FIX: Same pattern — use data-chv-action instead of inline <script>.
		if isMe {
			bodyHTML = `<span style="font-style:italic;opacity:0.8;font-size:13px;color:#f44336;">✗ Image permission denied</span>`
		} else {
			bodyHTML = `<span style="font-style:italic;opacity:0.8;font-size:13px;color:#f44336;" data-chv-action="img-permission-denied">✗ Image permission denied</span>`
		}

	default:
		if msg.VoiceURL != "" {
			// Voice message
			bodyHTML = fmt.Sprintf(`
				<div class="chv-voice-player">
					<button class="chv-voice-play-btn" onclick="playVoice(this, '%s', this.parentElement.querySelector('.chv-voice-waveform'))">
						<svg viewBox="0 0 24 24" fill="none" stroke="currentColor">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"/>
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/>
						</svg>
					</button>
					<div class="chv-voice-waveform" data-wv-id="%d"></div>
					<span class="chv-voice-time">0:00</span>
				</div>`, msg.VoiceURL, msg.ID)
		} else if msg.ImageURL != "" {
			// Image message
			textHTML := ""
			if msg.Content != "" {
				textHTML = fmt.Sprintf(`<div style="margin-top:4px;font-size:14.5px;">%s</div>`, html.EscapeString(msg.Content))
			}
			bodyHTML = fmt.Sprintf(`
				<img src="%s" class="chv-bubble-img" loading="lazy" onclick="openLightbox('%s')" alt="image">%s`,
				msg.ImageURL, msg.ImageURL, textHTML)
		} else {
			bodyHTML = fmt.Sprintf(`<span style="white-space:pre-wrap;word-break:break-word;">%s</span>`, html.EscapeString(msg.Content))
		}
	}

	// ── Meta bar (time + ticks) ───────────────────────────────────────────
	metaHTML := fmt.Sprintf(`<div class="chv-bubble-meta">
		<span class="chv-bubble-time" title="%s">%s</span>`, timeAgoStr, clockStr)
	if isMe {
		metaHTML += tickHTML(msg.IsRead)
	}
	metaHTML += `</div>`

	// ── Long press handlers ───────────────────────────────────────────────
	safeText := strings.ReplaceAll(msg.Content, `\`, `\\`)
	safeText = strings.ReplaceAll(safeText, `'`, `\'`)
	safeText = strings.ReplaceAll(safeText, "\n", "\\n")
	safeText = strings.ReplaceAll(safeText, "\r", "")
	safeText = strings.ReplaceAll(safeText, `&`, `&amp;`)
	safeText = strings.ReplaceAll(safeText, `"`, `&quot;`)
	safeText = strings.ReplaceAll(safeText, `<`, `&lt;`)
	longPressAttrs := fmt.Sprintf(
		`ontouchstart="startLongPress(this,%d,'%s','%s')" ontouchend="cancelLongPress()" ontouchcancel="cancelLongPress()" oncontextmenu="event.preventDefault();openCtxMenu(%d,'%s','%s',event.pageX,event.pageY);return false;"`,
		msg.ID, msg.Sender, safeText, msg.ID, msg.Sender, safeText)

	seenPos := ""
	if isMe {
		seenPos = seenAvatarHTML
	}

	return fmt.Sprintf(`
<div class="chv-msg-row %s" data-msg-id="%d" data-ts="%d">
  %s
  <div class="chv-bubble-wrap">
    <div class="chv-bubble" %s>
      %s%s%s
    </div>
  </div>
</div>`, side, msg.ID, ts, seenPos, longPressAttrs, replyQuoteHTML, bodyHTML, metaHTML)
}

func GetChatButton(currentUser, targetUser string) string {
	if currentUser == "" || currentUser == targetUser {
		return ""
	}

	var status string
	var sender string

	err := db.QueryRow(ctx, `
        SELECT status, sender FROM chat_invites 
        WHERE (sender = $1 AND recipient = $2) 
           OR (sender = $2 AND recipient = $1) 
        LIMIT 1`, currentUser, targetUser).Scan(&status, &sender)

	if err != nil {
		return fmt.Sprintf(`
            <button hx-post="/chat/invite/%s" 
                    hx-swap="outerHTML"
                    class="flex items-center gap-2 px-3 py-1.5 bg-blue-50 dark:bg-blue-500/10 text-blue-600 dark:text-blue-400 border border-blue-200 dark:border-blue-500/20 rounded-xl text-xs font-bold hover:bg-blue-600 hover:text-white transition-all">
                <span>📩</span> Invite
            </button>`, targetUser)
	}

	if status == "accepted" {
		return fmt.Sprintf(`
            <a href="/chat/%s" 
               class="flex items-center gap-2 px-3 py-1.5 bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-200 dark:border-emerald-500/20 rounded-xl text-xs font-bold hover:bg-emerald-600 hover:text-white transition-all">
                <span>💬</span> Message
            </a>`, targetUser)
	}

	if status == "pending" {
		if sender == currentUser {
			return `
                <button disabled class="flex items-center gap-2 px-3 py-1.5 bg-gray-100 dark:bg-gray-800 text-gray-400 border border-gray-200 dark:border-gray-700 rounded-xl text-xs font-bold cursor-not-allowed">
                    <span>⏳</span> Pending
                </button>`
		}
		return `
                <a href="/invites" class="flex items-center gap-2 px-3 py-1.5 bg-orange-50 dark:bg-orange-500/10 text-orange-600 dark:text-orange-400 border border-orange-200 dark:border-orange-500/20 rounded-xl text-xs font-bold animate-pulse">
                    <span>🔔</span> View Invite
                </a>`
	}

	return fmt.Sprintf(`
        <button hx-post="/chat/invite/%s" 
                hx-swap="outerHTML"
                class="flex items-center gap-2 px-3 py-1.5 bg-blue-50 dark:bg-blue-500/10 text-blue-600 dark:text-blue-400 border border-blue-200 dark:border-blue-500/20 rounded-xl text-xs font-bold hover:bg-blue-600 hover:text-white transition-all">
            <span>📩</span> Invite
        </button>`, targetUser)
}

func AcceptInviteHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	sender := c.Params("username")

	if currentUser == "" {
		return c.Status(401).SendString("Unauthorized")
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return c.Status(500).SendString("Server Error")
	}
	defer tx.Rollback(ctx)

	result, err := tx.Exec(ctx, `
        UPDATE chat_invites SET status = 'accepted' 
        WHERE sender = $1 AND recipient = $2 AND status = 'pending'`,
		sender, currentUser)

	if err != nil {
		return c.Status(500).SendString("Database error")
	}

	if result.RowsAffected() == 0 {
		return c.Status(403).SendString("No pending invite found.")
	}

	_, err = tx.Exec(ctx, `
        INSERT INTO notifications (recipient, sender, type, content) 
        VALUES ($1, $2, $3, $4)`,
		sender, currentUser, "chat_accept",
		"accepted your chat invite! You can now start a private chat.")

	if err != nil {
		log.Printf("Notification failed: %v", err)
	}

if err := tx.Commit(ctx); err != nil {
		return c.Status(500).SendString("Failed to save changes")
	}

	go NotifyUser(sender, currentUser, "accepted your chat invite", "https://dostana.biz/chat/"+currentUser)

	InvalidateChatAuthCache(currentUser, sender)
	c.Set("HX-Redirect", "/chat/"+sender)
	return c.SendStatus(200)
}

// chatAuthorized reports whether currentUser is allowed to exchange chat
// messages with otherUser: there must be an accepted invite between them
// and neither party may have blocked the other.
// chatAuthCacheKey is order-independent (a<b) so both directions of a
// conversation share one cache entry instead of two.
func chatAuthCacheKey(a, b string) string {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	if a > b {
		a, b = b, a
	}
	return "chat_auth:" + a + ":" + b
}

// InvalidateChatAuthCache clears the cached authorization result for a
// pair. Call after anything that can flip it: invite accept/reject,
// leaving a chat, or block/unblock. The block-toggle handler (wired via
// /block-toggle/:username, see post_renderer.go) should also call this
// — add InvalidateChatAuthCache(currentUser, targetUser) there.
func InvalidateChatAuthCache(a, b string) {
	CacheDel(chatAuthCacheKey(a, b))
}

// chatAuthorized reports whether currentUser is allowed to exchange chat
// messages with otherUser: there must be an accepted invite between them
// and neither party may have blocked the other.
//
// This runs on EVERY message poll (every 3s per open chat tab, forever —
// see chat_view.go's schedulePoll) and every send, which meant 2
// sequential Postgres round trips per tick just to re-confirm something
// that almost never changes. Now backed by a 30s Redis cache: the cold
// path still does both checks and stores the combined result; everything
// else is a single Redis GET that never touches the pgx pool. Worst case
// is up to 30s of staleness after a block/invite change — acceptable for
// a chat-access gate — and the accept/reject/leave paths below invalidate
// immediately, so the common case has zero staleness.
func chatAuthorized(currentUser, otherUser string) bool {
	if currentUser == "" || otherUser == "" || strings.EqualFold(currentUser, otherUser) {
		return false
	}

	key := chatAuthCacheKey(currentUser, otherUser)
	if val, ok := CacheGet(key); ok {
		return val == "1"
	}

	var status string
	err := db.QueryRow(ctx, `
		SELECT status FROM chat_invites
		WHERE (LOWER(sender)=LOWER($1) AND LOWER(recipient)=LOWER($2))
		   OR (LOWER(sender)=LOWER($2) AND LOWER(recipient)=LOWER($1))`,
		currentUser, otherUser).Scan(&status)

	authorized := err == nil && status == "accepted"
	if authorized {
		var blocked bool
		_ = db.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM blocks
			WHERE (blocker=$1 AND blocked=$2) OR (blocker=$2 AND blocked=$1))`,
			currentUser, otherUser).Scan(&blocked)
		authorized = !blocked
	}

	cacheVal := "0"
	if authorized {
		cacheVal = "1"
	}
	CacheSet(key, cacheVal, 30*time.Second)
	return authorized
}
func SendMessageHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	otherUser := strings.ToLower(strings.TrimSpace(c.Params("username")))

	if currentUser == "" {
		return c.Status(401).SendString("")
	}
	if !chatAuthorized(currentUser, otherUser) {
		return c.Status(403).SendString("")
	}

	content := strings.TrimSpace(c.FormValue("content"))
	replyToIDStr := c.FormValue("reply_to_id")
	var replyToID *int
	if replyToIDStr != "" {
		var rid int
		fmt.Sscanf(replyToIDStr, "%d", &rid)
		if rid > 0 {
			replyToID = &rid
		}
	}

	// Deduplicate permission requests: at most one pending per 10 min.
	if content == "__IMG_PERMISSION_REQUEST__" {
		var existing int
		_ = db.QueryRow(ctx, `
			SELECT COUNT(*) FROM messages
			WHERE LOWER(sender) = LOWER($1)
			  AND LOWER(recipient) = LOWER($2)
			  AND content = '__IMG_PERMISSION_REQUEST__'
			  AND created_at > NOW() - INTERVAL '10 minutes'`,
			currentUser, otherUser).Scan(&existing)
		if existing > 0 {
			return c.SendStatus(200)
		}
	}

	// Handle image upload — only allowed if Redis permission key is set.
	var imageURL string
	file, err := c.FormFile("image")
	if err == nil && file != nil {
		// FIX: check Redis permission before accepting the file.
		if !hasImgPermission(currentUser, otherUser) {
			return c.Status(403).SendString("image permission not granted")
		}
		dir := "./uploads/chat_images"
		_ = os.MkdirAll(dir, 0755)
		ext := filepath.Ext(file.Filename)
		if ext == "" {
			ext = ".jpg"
		}
		fname := fmt.Sprintf("ci_%s_%d%s", currentUser, time.Now().UnixNano(), ext)
		dst := filepath.Join(dir, fname)
		if src, err2 := file.Open(); err2 == nil {
			defer src.Close()
			if out, err3 := os.Create(dst); err3 == nil {
				defer out.Close()
				io.Copy(out, src)
				imageURL = "/uploads/chat_images/" + fname
			}
		}
	}

	if content == "" && imageURL == "" {
		return c.Status(400).SendString("")
	}

	var msgID int
	err = db.QueryRow(ctx, `
		INSERT INTO messages (sender, recipient, content, image_url, reply_to_id, is_read, created_at)
		VALUES ($1, $2, $3, $4, $5, false, NOW())
		RETURNING id`,
		currentUser, otherUser, content, imageURL, replyToID).Scan(&msgID)
	if err != nil {
		log.Printf("[SendMessage] insert error: %v", err)
		return c.Status(500).SendString("")
	}

	goSafe("chat notif insert", func() {
		_, _ = db.Exec(ctx, `
			INSERT INTO chat_notifications (recipient, sender, message_id, is_read, is_seen, created_at)
			VALUES ($1, $2, $3, false, false, NOW())`,
			otherUser, currentUser, msgID)
	})

	preview := content
	if preview == "" && imageURL != "" {
		preview = "📷 Photo"
	}
	if r := []rune(preview); len(r) > 80 {
		preview = string(r[:80]) + "…"
	}
	go NotifyUser(otherUser, currentUser, preview, "https://dostana.biz/chat/"+currentUser)

	loc, _ := time.LoadLocation("Asia/Karachi")
	msg := chatMessage{
		ID:        msgID,
		Sender:    currentUser,
		Recipient: otherUser,
		Content:   content,
		ImageURL:  imageURL,
		IsRead:    false,
		CreatedAt: time.Now().In(loc),
		ReplyToID: replyToID,
	}
	if replyToID != nil {
		_ = db.QueryRow(ctx, `SELECT COALESCE(content,''), COALESCE(sender,'') FROM messages WHERE id=$1`, *replyToID).
			Scan(&msg.ReplyText, &msg.ReplyAuthor)
	}

	return c.Type("html").SendString(renderBubble(msg, currentUser, otherUser, "", true))
}

func SendVoiceMessageHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	otherUser := strings.ToLower(strings.TrimSpace(c.Params("username")))

	if currentUser == "" {
		return c.Status(401).SendString("")
	}
	if !chatAuthorized(currentUser, otherUser) {
		return c.Status(403).SendString("")
	}

	file, err := c.FormFile("voice")
	if err != nil || file == nil {
		return c.Status(400).SendString("no voice file")
	}

	dir := "./uploads/chat_voice_chat"
	_ = os.MkdirAll(dir, 0755)
	fname := fmt.Sprintf("voice_%s_%d.webm", currentUser, time.Now().UnixNano())
	dst := filepath.Join(dir, fname)

	src, err := file.Open()
	if err != nil {
		return c.Status(500).SendString("")
	}
	defer src.Close()

	out, err := os.Create(dst)
	if err != nil {
		return c.Status(500).SendString("")
	}
	defer out.Close()
	io.Copy(out, src)

	voiceURL := "/uploads/chat_voice_chat/" + fname

	var msgID int
	err = db.QueryRow(ctx, `
		INSERT INTO messages (sender, recipient, content, voice_url, is_read, created_at)
		VALUES ($1, $2, '', $3, false, NOW())
		RETURNING id`,
		currentUser, otherUser, voiceURL).Scan(&msgID)
	if err != nil {
		log.Printf("[SendVoice] insert error: %v", err)
		return c.Status(500).SendString("")
	}

	goSafe("chat notif insert", func() {
		_, _ = db.Exec(ctx, `
			INSERT INTO chat_notifications (recipient, sender, message_id, is_read, is_seen, created_at)
			VALUES ($1, $2, $3, false, false, NOW())`,
			otherUser, currentUser, msgID)
	})

	go NotifyUser(otherUser, currentUser, "🎙️ Voice message", "https://dostana.biz/chat/"+currentUser)

	return c.SendStatus(200)
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE MESSAGE
// ─────────────────────────────────────────────────────────────────────────────

func DeleteChatMessageHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	otherUser := strings.ToLower(strings.TrimSpace(c.Params("username")))
	var msgID int
	fmt.Sscanf(c.Params("id"), "%d", &msgID)

	if currentUser == "" || msgID == 0 {
		return c.Status(400).SendString("")
	}

	var msgSender string
	err := db.QueryRow(ctx, `
		SELECT sender FROM messages
		WHERE id = $1
		  AND (
		        (LOWER(sender) = LOWER($2) AND LOWER(recipient) = LOWER($3))
		     OR (LOWER(sender) = LOWER($3) AND LOWER(recipient) = LOWER($2))
		      )`, msgID, currentUser, otherUser).Scan(&msgSender)

	if err != nil {
		return c.Status(404).SendString("")
	}

	if strings.EqualFold(msgSender, currentUser) {
		CacheZAdd(chatDeletedEveryoneKey(currentUser, otherUser), float64(time.Now().Unix()), fmt.Sprintf("%d", msgID))
	} else {
		CacheSAdd(chatHiddenMsgKey(currentUser), fmt.Sprintf("%d", msgID))
	}
	return c.SendStatus(200)
}

func GetDeletedForEveryoneHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	otherUser := strings.ToLower(strings.TrimSpace(c.Params("username")))
	if currentUser == "" || otherUser == "" {
		return c.JSON(fiber.Map{"deleted_ids": []int{}})
	}

	sinceStr := c.Query("since", "0")
	ids := CacheZRangeByScore(chatDeletedEveryoneKey(currentUser, otherUser), sinceStr, "+inf")

	out := make([]int, 0, len(ids))
	for _, s := range ids {
		var n int
		if _, err := fmt.Sscanf(s, "%d", &n); err == nil {
			out = append(out, n)
		}
	}
	return c.JSON(fiber.Map{"deleted_ids": out})
}
func imgPermKey(requester, approver string) string {
	return "img_perm:" + strings.ToLower(requester) + ":" + strings.ToLower(approver)
}

// hasImgPermission returns true if `sender` has been granted permission to
// send images to `recipient`.
func hasImgPermission(sender, recipient string) bool {
	val, ok := CacheGet(imgPermKey(sender, recipient))
	return ok && val == "1"
}

func chatHiddenMsgKey(viewer string) string {
	return "chat_hidden_msgs:" + strings.ToLower(viewer)
}

func chatClearedKey(viewer, otherUser string) string {
	return "chat_cleared:" + strings.ToLower(viewer) + ":" + strings.ToLower(otherUser)
}

// chatDeletedEveryoneKey is order-independent so both directions of a
// conversation share one sorted set, scored by deletion time so the
// poll below can ask "what changed since I last checked".
func chatDeletedEveryoneKey(a, b string) string {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	if a > b {
		a, b = b, a
	}
	return "chat_deleted_everyone:" + a + ":" + b
}

// ─────────────────────────────────────────────────────────────────────────────
// IMAGE PERMISSION HANDLERS
// ─────────────────────────────────────────────────────────────────────────────

func ApproveImagePermissionHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	otherUser := strings.ToLower(strings.TrimSpace(c.Params("username")))

	if currentUser == "" {
		return c.Status(401).SendString("")
	}

	// 1. Persist permission in Redis for 30 days.
	CacheSet(imgPermKey(otherUser, currentUser), "1", 30*24*time.Hour)

	// 2. Clean up all pending request messages.
	_, _ = db.Exec(ctx, `
		DELETE FROM messages
		WHERE LOWER(sender) = LOWER($1)
		  AND LOWER(recipient) = LOWER($2)
		  AND content = '__IMG_PERMISSION_REQUEST__'`,
		otherUser, currentUser)

	// 3. Insert the one-time granted signal for the requester's poll to consume.
	_, _ = db.Exec(ctx, `
		INSERT INTO messages (sender, recipient, content, is_read, created_at)
		VALUES ($1, $2, '__IMG_PERMISSION_GRANTED__', false, NOW())`,
		currentUser, otherUser)

	return c.SendStatus(200)
}

func DenyImagePermissionHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	otherUser := strings.ToLower(strings.TrimSpace(c.Params("username")))

	if currentUser == "" {
		return c.Status(401).SendString("")
	}

	// 1. Remove any previously granted permission.
	CacheDel(imgPermKey(otherUser, currentUser))

	// 2. Clean up request messages.
	_, _ = db.Exec(ctx, `
		DELETE FROM messages
		WHERE LOWER(sender) = LOWER($1)
		  AND LOWER(recipient) = LOWER($2)
		  AND content = '__IMG_PERMISSION_REQUEST__'`,
		otherUser, currentUser)

	// 3. One-time denial signal for requester's poll.
	_, _ = db.Exec(ctx, `
		INSERT INTO messages (sender, recipient, content, is_read, created_at)
		VALUES ($1, $2, '__IMG_PERMISSION_DENIED__', false, NOW())`,
		currentUser, otherUser)

	return c.SendStatus(200)
}

func GetTicksHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	otherUser := strings.ToLower(strings.TrimSpace(c.Params("username")))
	if currentUser == "" {
		return c.JSON(fiber.Map{"read_ids": []int{}})
	}
rows, err := db.Query(ctx, `
		SELECT id FROM messages
		WHERE LOWER(sender)=LOWER($1)
		  AND LOWER(recipient)=LOWER($2)
		  AND is_read=true
		ORDER BY id DESC
		LIMIT 200`,
		currentUser, otherUser)
	if err != nil {
		return c.JSON(fiber.Map{"read_ids": []int{}})
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	if ids == nil {
		ids = []int{}
	}
	return c.JSON(fiber.Map{"read_ids": ids})
}

func GetMessagesHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	otherUser := strings.ToLower(strings.TrimSpace(c.Params("username")))

	if currentUser == "" {
		return c.SendString("")
	}
	if !chatAuthorized(currentUser, otherUser) {
		return c.SendString("")
	}

	afterTs := c.QueryInt("after", 0)

	baseQuery := `
		SELECT 
			m.id, m.sender, m.recipient, 
			COALESCE(m.content,'') AS content,
			COALESCE(m.image_url,'') AS image_url,
			COALESCE(m.voice_url,'') AS voice_url,
			m.is_read, m.created_at,
			m.reply_to_id,
			COALESCE(rm.content,'') AS reply_text,
			COALESCE(rm.sender,'') AS reply_author,
			rm.created_at AS reply_created_at
		FROM messages m
		LEFT JOIN messages rm ON rm.id = m.reply_to_id
		WHERE (
			(LOWER(m.sender) = LOWER($1) AND LOWER(m.recipient) = LOWER($2))
			OR
			(LOWER(m.sender) = LOWER($2) AND LOWER(m.recipient) = LOWER($1))
		)
	`

	var pgRows interface {
		Close()
		Next() bool
		Scan(dest ...interface{}) error
	}
	var err error

	beforeID := c.QueryInt("before_id", 0)

	if afterTs > 0 {
		baseQuery += fmt.Sprintf(" AND EXTRACT(EPOCH FROM m.created_at)::BIGINT > %d", afterTs)
		baseQuery += " ORDER BY m.created_at ASC"
		pgRows, err = db.Query(ctx, baseQuery, currentUser, otherUser)
	} else if beforeID > 0 {
		// Lazy-load older messages: fetch 50 rows before the given ID
		baseQuery += fmt.Sprintf(" AND m.id < %d", beforeID)
		baseQuery += " ORDER BY m.created_at DESC LIMIT 50"
		fullQ := fmt.Sprintf("SELECT * FROM (%s) sub ORDER BY created_at ASC", baseQuery)
		pgRows, err = db.Query(ctx, fullQ, currentUser, otherUser)
	} else {
		// Initial load: last 50 messages only, so scroll-to-bottom works instantly
		baseQuery += " ORDER BY m.created_at DESC LIMIT 50"
		fullQ := fmt.Sprintf("SELECT * FROM (%s) sub ORDER BY created_at ASC", baseQuery)
		pgRows, err = db.Query(ctx, fullQ, currentUser, otherUser)
	}

	if err != nil {
		log.Printf("[GetMessages] query error: %v", err)
		return c.SendString("")
	}
	defer pgRows.Close()

// REPLACE WITH:
	// Was a fresh Postgres query on EVERY poll tick (every 3s per open
	// chat tab, forever). getCachedAuthorMeta (post_renderer.go) already
	// maintains this exact data in a Redis-backed map refreshed at most
	// once per 90s, shared across the whole app. Reusing it removes a
	// full DB round trip from the chat poll hot path entirely; worst
	// case the avatar is up to 90s stale, invisible for a profile pic.
	otherAvatar := getCachedAuthorMeta(otherUser).ProfileImage

	hiddenForViewer := CacheSMembers(chatHiddenMsgKey(currentUser))
	deletedForEveryoneList := CacheZRangeByScore(chatDeletedEveryoneKey(currentUser, otherUser), "-inf", "+inf")
	deletedForEveryone := make(map[string]bool, len(deletedForEveryoneList))
	for _, id := range deletedForEveryoneList {
		deletedForEveryone[id] = true
	}
	var clearedAt int64
	if clearedStr, ok := CacheGet(chatClearedKey(currentUser, otherUser)); ok {
		fmt.Sscanf(clearedStr, "%d", &clearedAt)
	}

	var msgs []chatMessage
	hasIncoming := false
	for pgRows.Next() {
		var msg chatMessage
		var voiceURL *string
		var replyCreatedAt *time.Time
		err := pgRows.Scan(
			&msg.ID, &msg.Sender, &msg.Recipient,
			&msg.Content, &msg.ImageURL, &voiceURL,
			&msg.IsRead, &msg.CreatedAt,
			&msg.ReplyToID,
			&msg.ReplyText, &msg.ReplyAuthor,
			&replyCreatedAt,
		)
		if err != nil {
			log.Printf("[GetMessages] scan error: %v", err)
			continue
		}
		if voiceURL != nil {
			msg.VoiceURL = *voiceURL
		}
		if strings.EqualFold(msg.Sender, otherUser) {
			hasIncoming = true
		}
		idStr := fmt.Sprintf("%d", msg.ID)
		if hiddenForViewer[idStr] || deletedForEveryone[idStr] {
			continue
		}
		if clearedAt > 0 && msg.CreatedAt.UnixNano() <= clearedAt {
			continue
		}
		if msg.ReplyToID != nil {
			replyIDStr := fmt.Sprintf("%d", *msg.ReplyToID)
			if hiddenForViewer[replyIDStr] || deletedForEveryone[replyIDStr] {
				msg.ReplyHidden = true
			} else if clearedAt > 0 && replyCreatedAt != nil && replyCreatedAt.UnixNano() <= clearedAt {
				msg.ReplyHidden = true
			}
		}
		msgs = append(msgs, msg)
	}

	// Mark as read + clear notifications + delete one-time permission signals.
	// Was firing unconditionally on EVERY poll tick (every 3s per open chat
	// tab, forever, per chat_view.go's schedulePoll) regardless of whether
	// there was anything to mark — 3 extra writes every 3s per idle open
	// chat, compounding with feed/trending load on the connection pool
	// that was already hitting acquired:12/12 under normal traffic (see
	// dostana-watchdog.log, Aug 14-22). Only run it on the initial full
	// load (afterTs==0, so any pre-existing unread messages still get
	// caught) or when this poll actually surfaced a message from the
	// other user.
	if afterTs == 0 || hasIncoming {
		goSafe("chat mark-read", func() {
			_, _ = db.Exec(ctx, `
				UPDATE messages SET is_read=true
				WHERE LOWER(recipient)=LOWER($1) AND LOWER(sender)=LOWER($2) AND is_read=false`,
				currentUser, otherUser)
			_, _ = db.Exec(ctx, `
				DELETE FROM chat_notifications
				WHERE LOWER(recipient)=LOWER($1) AND LOWER(sender)=LOWER($2)`,
				currentUser, otherUser)
			// Delete the one-time signal messages after they've been seen.
			// Redis key persists independently so the state is not lost.
			_, _ = db.Exec(ctx, `
				DELETE FROM messages
				WHERE (
					(LOWER(recipient)=LOWER($1) AND LOWER(sender)=LOWER($2))
					OR
					(LOWER(recipient)=LOWER($2) AND LOWER(sender)=LOWER($1))
				)
				AND content IN ('__IMG_PERMISSION_GRANTED__', '__IMG_PERMISSION_DENIED__')`,
				currentUser, otherUser)
		})
	}

	if len(msgs) == 0 {
		if afterTs == 0 {
			return c.SendString(`<div style="text-align:center;padding:32px 16px;color:#8696a0;font-size:14px;">
				<div style="font-size:40px;margin-bottom:12px;">👋</div>
				<div style="font-weight:600;">Say hello!</div>
				<div style="font-size:12px;margin-top:4px;">Messages are end-to-end private</div>
			</div>`)
		}
		return c.SendString("")
	}

	var sb strings.Builder

	// FIX: Read imgPermGranted from Redis — reliable across page reloads and
	// existing chats, unlike scanning deletable signal messages.
	if afterTs == 0 {
		imgPermGranted := hasImgPermission(currentUser, otherUser)
		if imgPermGranted {
			sb.WriteString(`<div id="chv-img-perm-state" data-granted="1" style="display:none"></div>`)
		}
	}

	var lastDate string
	// Find index of last message sent by currentUser (for seen avatar)
	lastMeIdx := -1
	for i, msg := range msgs {
		if strings.EqualFold(msg.Sender, currentUser) {
			lastMeIdx = i
		}
	}

	for i, msg := range msgs {
		loc, _ := time.LoadLocation("Asia/Karachi")
		msgDate := msg.CreatedAt.In(loc).Format("2006-01-02")
		if afterTs == 0 && msgDate != lastDate {
			lastDate = msgDate
			dateLabel := formatDateDivider(msg.CreatedAt.In(loc))
			sb.WriteString(fmt.Sprintf(`<div class="chv-date-divider"><span>%s</span></div>`, dateLabel))
		}
		sb.WriteString(renderBubble(msg, currentUser, otherUser, otherAvatar, i == lastMeIdx))
	}

	return c.Type("html").SendString(sb.String())
}

func formatDateDivider(t time.Time) string {
	loc, _ := time.LoadLocation("Asia/Karachi")
	now := time.Now().In(loc)
	y, m, d := t.Date()
	ny, nm, nd := now.Date()
	if y == ny && m == nm && d == nd {
		return "Today"
	}
	yesterday := now.AddDate(0, 0, -1)
	yy, ym, yd := yesterday.Date()
	if y == yy && m == ym && d == yd {
		return "Yesterday"
	}
	return t.Format("2 Jan 2006")
}

func UserStatusHandler(c *fiber.Ctx) error {
	if getCurrentUser(c) == "" {
		return c.Status(401).SendString("")
	}
	username := strings.ToLower(strings.TrimSpace(c.Params("username")))

	var lastActive time.Time
	var userType string

	c.Set("Content-Type", "text/html")

	err := db.QueryRow(ctx, `
        SELECT last_active, COALESCE(user_type, 'normal') 
        FROM users 
        WHERE LOWER(username) = $1`, username).
		Scan(&lastActive, &userType)

	if err != nil {
		log.Printf("[Dostana Status Debug] SQL Query Failed: %v", err)
		return c.SendString(`<span class="text-gray-400 text-xs italic">User Not Found</span>`)
	}

	if lastActive.IsZero() {
		return c.SendString(`<span class="text-gray-400 text-xs italic">Never Active</span>`)
	}

	timeAgoStr := formatTwitterTime(lastActive)

	if timeAgoStr == "Just now" || strings.HasSuffix(timeAgoStr, "s ago") {
		return c.SendString(`
            <span class="flex items-center gap-1.5">
                <span class="relative flex h-2 w-2">
                    <span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
                    <span class="relative inline-flex rounded-full h-2 w-2 bg-emerald-500"></span>
                </span>
                <span class="text-emerald-500 font-black text-xs uppercase tracking-wider">Active Now</span>
            </span>`)
	}

	return c.SendString(fmt.Sprintf(
		`<span class="text-gray-500 text-[10px] font-black uppercase tracking-tight">Last seen %s</span>`,
		timeAgoStr,
	))
}

func RejectInviteHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	sender := c.Params("username")

	if currentUser == "" {
		return c.Status(401).SendString("Unauthorized")
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return c.Status(500).SendString("Server Error")
	}
	defer tx.Rollback(ctx)

	result, err := tx.Exec(ctx, `
        DELETE FROM chat_invites 
        WHERE sender = $1 AND recipient = $2 AND status = 'pending'`,
		sender, currentUser)

	if err != nil {
		return c.Status(500).SendString("Database Error")
	}

	if result.RowsAffected() == 0 {
		return c.Status(404).SendString("Invite no longer exists.")
	}

	_, err = tx.Exec(ctx, `
        INSERT INTO notifications (recipient, sender, type, content) 
        VALUES ($1, $2, 'chat_reject', 'declined your chat invite.')`,
		sender, currentUser)

	if err != nil {
		log.Printf("Notification failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return c.Status(500).SendString("Commit Error")
	}

	go NotifyUser(sender, currentUser, "declined your chat invite", "https://dostana.biz/invites")

	InvalidateChatAuthCache(currentUser, sender)
	c.Set("Content-Type", "text/html")
	return c.SendString(`
        <div class="p-3 bg-gray-100 dark:bg-gray-800/50 rounded-xl text-center">
            <p class="text-xs text-gray-500 font-medium italic">Invite Declined</p>
        </div>`)
}

func LeaveChatHandler(c *fiber.Ctx) error {
	currentUser, err := requireUser(c)
	if err != nil {
		return err
	}

	rawParam, err := url.QueryUnescape(c.Params("username"))
	if err != nil {
		rawParam = c.Params("username")
	}
	targetUserCleaned := strings.ToLower(strings.TrimSpace(rawParam))

	var trueTargetName string
	err = db.QueryRow(ctx, "SELECT username FROM users WHERE LOWER(username) = $1", targetUserCleaned).Scan(&trueTargetName)
	if err != nil {
		c.Set("HX-Redirect", "/invites")
		return c.SendStatus(200)
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return c.Status(500).SendString("Server Error")
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
        DELETE FROM chat_invites 
        WHERE (LOWER(sender) = LOWER($1) AND LOWER(recipient) = LOWER($2)) 
           OR (LOWER(sender) = LOWER($2) AND LOWER(recipient) = LOWER($1))`,
		currentUser, trueTargetName)

	_, err = tx.Exec(ctx, `
        DELETE FROM messages 
        WHERE (LOWER(sender) = LOWER($1) AND LOWER(recipient) = LOWER($2)) 
           OR (LOWER(sender) = LOWER($2) AND LOWER(recipient) = LOWER($1))`,
		currentUser, trueTargetName)

	_, err = tx.Exec(ctx, `
        DELETE FROM chat_notifications 
        WHERE (LOWER(recipient) = LOWER($1) AND LOWER(sender) = LOWER($2)) 
           OR (LOWER(recipient) = LOWER($2) AND LOWER(sender) = LOWER($1))`,
		currentUser, trueTargetName)

	_, err = tx.Exec(ctx, `
        DELETE FROM notifications 
        WHERE ((LOWER(recipient) = LOWER($1) AND LOWER(sender) = LOWER($2)) 
           OR (LOWER(recipient) = LOWER($2) AND LOWER(sender) = LOWER($1)))
        AND type IN ('chat_invite', 'chat_accept', 'chat_message')`,
		currentUser, trueTargetName)

	if err != nil {
		return c.Status(500).SendString("Failed to wipe chat data")
	}

	_, err = tx.Exec(ctx, `
        INSERT INTO notifications (recipient, sender, type, content) 
        VALUES ($1, $2, 'chat_leave', 'has ended the chat session. All history has been deleted.')`,
		trueTargetName, currentUser)

if err := tx.Commit(ctx); err != nil {
		return c.Status(500).SendString("Commit Error")
	}

	go NotifyUser(trueTargetName, currentUser, "ended the chat and deleted the history", "https://dostana.biz/invites")

	InvalidateChatAuthCache(currentUser, trueTargetName)
	c.Set("HX-Trigger", "refreshNotifications")
	c.Set("HX-Redirect", "/invites")
	return c.SendStatus(200)
}
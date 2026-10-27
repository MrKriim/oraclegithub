package main

import (
	"context"
	"fmt"
	"html"
	"log"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// ====================== REDIS HELPER KEYS ======================
// Centralized key format so cache invalidation is consistent.

func cacheKeyBanned(username string) string { return "ban:" + username }
func cacheKeyBlocked(a, b string) string    { return "block:" + a + ":" + b }
func cacheKeyPoints(username string) string { return "pts_pending:" + username }

// ====================== BAN CHECK (Redis-cached, 2 min TTL) ======================
// Returns true if the user is banned. Hits Redis first, falls back to DB.
// On DB error it returns false (fail-open) so a Redis outage never locks users out.
func isUserBanned(username string) bool {
	rctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	key := cacheKeyBanned(username)
	if val, err := rdb.Get(rctx, key).Result(); err == nil {
		return val == "1"
	}

	var banned bool
	dbCtx, dbCancel := context.WithTimeout(ctx, 3*time.Second)
	defer dbCancel()
	if err := db.QueryRow(dbCtx, "SELECT is_banned FROM users WHERE username=$1", username).Scan(&banned); err != nil {
		log.Printf("isUserBanned DB error for %s: %v", username, err)
		return false // fail-open
	}

	cacheVal := "0"
	if banned {
		cacheVal = "1"
	}
	go func() {
		setCtx, setCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer setCancel()
		rdb.Set(setCtx, key, cacheVal, 2*time.Minute)
	}()
	return banned
}

// ====================== BLOCK CHECK (Redis-cached, 5 min TTL) ======================
// Returns true if `blocker` has blocked `target`.
// Cached for 5 minutes — block relationships change rarely.
func isBlockedBy(userA, userB string) bool {
	if userA == "" || userB == "" {
		return false
	}

	// Normalise so (A,B) and (B,A) share one cache entry
	keyA, keyB := userA, userB
	if userA > userB {
		keyA, keyB = userB, userA
	}
	cacheKey := "block_rel:" + keyA + ":" + keyB

	rctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	if val, err := rdb.Get(rctx, cacheKey).Result(); err == nil {
		return val == "1"
	}

	var exists bool
	dbCtx, dbCancel := context.WithTimeout(ctx, 3*time.Second)
	defer dbCancel()
	err := db.QueryRow(dbCtx, `
		SELECT EXISTS(
			SELECT 1 FROM blocks
			WHERE (blocker = $1 AND blocked = $2)
			   OR (blocker = $2 AND blocked = $1)
		)`, userA, userB,
	).Scan(&exists)
	if err != nil {
		log.Printf("isBlockedBy DB error (%s↔%s): %v", userA, userB, err)
		return false
	}

	cacheVal := "0"
	if exists {
		cacheVal = "1"
	}
	go func() {
		setCtx, setCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer setCancel()
		rdb.Set(setCtx, cacheKey, cacheVal, 5*time.Minute)
	}()
	return exists
}

// ====================== POINTS UPDATE (Redis buffered, flushed async) ======================
// Instead of hitting Postgres on every comment/reply, we accumulate points in
// Redis and flush to DB in a background goroutine. This keeps comment posting
// snappy without losing accuracy — worst case on crash: <10s of points lost.
//
// Call flushPendingPoints() periodically (e.g. every 10s) from a goroutine in main().
func bufferPoints(username string, delta int) {
	rctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	key := cacheKeyPoints(username)
	// INCRBY is atomic — safe for concurrent requests.
	if err := rdb.IncrBy(rctx, key, int64(delta)).Err(); err != nil {
		// Redis unavailable — fall back to direct DB update.
		log.Printf("bufferPoints Redis unavailable for %s, writing to DB directly: %v", username, err)
		goSafe("bufferPoints fallback", func() {
			dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer dbCancel()
			if _, err := db.Exec(dbCtx,
				"UPDATE users SET points = COALESCE(points,0) + $1 WHERE username=$2",
				delta, username,
			); err != nil {
				log.Printf("bufferPoints fallback DB error for %s: %v", username, err)
			}
		})
	}
}

// FlushPendingPoints scans all pts_pending:* keys in Redis and flushes them
// to Postgres in a single transaction per key. Call this from a goroutine in main().
//
//	go func() {
//	    ticker := time.NewTicker(10 * time.Second)
//	    for range ticker.C { FlushPendingPoints() }
//	}()
func FlushPendingPoints() {
	if rdb == nil {
		return
	}
	rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	keys, err := rdb.Keys(rctx, "pts_pending:*").Result()
	if err != nil || len(keys) == 0 {
		return
	}

	for _, key := range keys {
		username := strings.TrimPrefix(key, "pts_pending:")

		// GetDel atomically reads and removes the key.
		val, err := rdb.GetDel(rctx, key).Result()
		if err != nil {
			continue
		}
		delta, err := strconv.ParseInt(val, 10, 64)
		if err != nil || delta == 0 {
			continue
		}

		dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err = db.Exec(dbCtx,
			"UPDATE users SET points = COALESCE(points,0) + $1 WHERE username=$2",
			delta, username,
		)
		dbCancel()
		if err != nil {
			log.Printf("FlushPendingPoints DB error for %s (+%d): %v", username, delta, err)
			u, d := username, delta
			goSafe("FlushPendingPoints requeue", func() {
				reCtx, reCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer reCancel()
				rdb.IncrBy(reCtx, cacheKeyPoints(u), d)
			})
		}
	}
}
func checkCommentAllowed(author, postAuthor string, allowComments, followersOnly bool) string {
	if author == "" {
		return errorHTML("🔒", "Login required", "You need to be logged in to comment.")
	}
	if isUserBanned(author) {
		return errorHTML("🚫", "Account Banned",
			"Apka account ban he kisi ki post per comment nahi karskte.")
	}
	if secsLeft, ok := commentBanSecondsLeft(author); ok {
		mins := secsLeft/60 + 1
		return errorHTML("⏳", "Spam Detected",
			fmt.Sprintf("Ap comment kar sakenge %d minute(s) bad, apko tez tez likhne ki waja se ban kar dia gaya he.", mins))
	}
	if postAuthor != "" && isBlockedBy(postAuthor, author) {
		return errorHTML("🙅", "You are blocked by this user",
			"Ap is user ki Post pe comment nahi kar sakte isne apko block kia hua he.")
	}
	if author != postAuthor {
		if !allowComments {
			return errorHTML("💬", "Comments Off", "Is post per comments band kar diye gaye hain.")
		}
		if followersOnly && !isFollowingUser(author, postAuthor) {
			return errorHTML("🔒", "Followers Only", "Sirf is user ke followers hi comment kar sakte hain. Pehle follow karein.")
		}
	}
	return ""
}

// commentBanSecondsLeft reports whether the user is currently serving a
// spam-triggered commenting ban, and how many seconds remain.
func commentBanSecondsLeft(username string) (int, bool) {
	dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var bannedUntil *time.Time
	if err := db.QueryRow(dbCtx,
		"SELECT comment_banned_until FROM users WHERE username=$1", username,
	).Scan(&bannedUntil); err != nil || bannedUntil == nil {
		return 0, false
	}
	remaining := time.Until(*bannedUntil)
	if remaining <= 0 {
		return 0, false
	}
	return int(remaining.Seconds()), true
}

// awardConversationPoints looks at the author's recent comment history:
//   - first-ever comment: no spam check runs, flat 1-3 conversational points.
//   - 5 comments (this one + 4 prior) inside 10 seconds: spam — sets a
//     5-minute comment_banned_until and awards 0 points for this comment.
//   - a quick (<2 min) reply to someone else reads as a real back-and-forth
//     conversation → random 1-3 points. Slower/standalone comments get 1.
func awardConversationPoints(author, otherParty string) int {
	dbCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var hasPrevious bool
	if err := db.QueryRow(dbCtx,
		"SELECT EXISTS(SELECT 1 FROM comments WHERE author=$1)", author,
	).Scan(&hasPrevious); err != nil || !hasPrevious {
		return randPoints(1, 3)
	}

	rows, err := db.Query(dbCtx,
		"SELECT created_at FROM comments WHERE author=$1 ORDER BY created_at DESC LIMIT 5", author)
	if err != nil {
		return 1
	}
	defer rows.Close()

	var times []time.Time
	for rows.Next() {
		var t time.Time
		if rows.Scan(&t) == nil {
			times = append(times, t)
		}
	}

	if len(times) >= 4 && time.Since(times[len(times)-1]) < 10*time.Second {
		banUntil := time.Now().Add(5 * time.Minute)
		goSafe("comment spam ban", func() {
			setCtx, setCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer setCancel()
			db.Exec(setCtx, "UPDATE users SET comment_banned_until=$1 WHERE username=$2", banUntil, author)
		})
		return 0
	}

	if otherParty != "" && otherParty != author && len(times) >= 2 {
		if gap := times[0].Sub(times[1]); gap > 0 && gap < 2*time.Minute {
			return randPoints(1, 3)
		}
	}
	return 1
}

func randPoints(min, max int) int {
	return min + rand.Intn(max-min+1)
}

func isFollowingUser(follower, followee string) bool {
	var exists bool
	dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = db.QueryRow(dbCtx, "SELECT EXISTS(SELECT 1 FROM followers WHERE follower=$1 AND followee=$2)", follower, followee).Scan(&exists)
	return exists
}

// ====================== FRIENDLY ERROR HTML ======================
// Returns a small inline error div that HTMX can swap into the comments list.
func errorHTML(icon, title, detail string) string {
	return fmt.Sprintf(`
		<div class="flex items-start gap-3 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 
		            rounded-xl p-4 mb-3 animate-fade-in" role="alert">
			<span class="text-2xl">%s</span>
			<div>
				<p class="font-semibold text-red-700 dark:text-red-400 text-sm">%s</p>
				<p class="text-red-600 dark:text-red-300 text-xs mt-0.5">%s</p>
			</div>
		</div>`, icon, title, detail)
}

// ====================== POST VIEW HANDLER ======================
func postViewHandler(c *fiber.Ctx) error {
	postIDStr := c.Params("id")
	currentUser := getCurrentUser(c)
	postID, _ := strconv.Atoi(postIDStr)

	ctx2, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var (
		author, content, img, cat, voice             string
		createdAt                                    time.Time
		expiresAt                                    *time.Time
		points, dpCompID                             int
		allowJ, allowC, allowComments, followersOnly bool
	)

	err := db.QueryRow(ctx2, `
        SELECT p.author, p.content, p.created_at, u.points, p.allow_jhappi, p.allow_chupair,
               COALESCE(p.image_url, ''), COALESCE(p.category, 'General'), COALESCE(p.voice_url, ''),
               COALESCE(p.allow_comments, true), COALESCE(p.followers_only, false), p.expires_at,
               COALESCE(p.dp_competition_id, 0)
        FROM posts p 
        JOIN users u ON p.author = u.username 
        WHERE p.id = $1`, postID).Scan(
		&author, &content, &createdAt, &points,
		&allowJ, &allowC, &img, &cat, &voice,
		&allowComments, &followersOnly, &expiresAt, &dpCompID,
	)
	if err != nil {
		log.Printf("Error fetching post: %v", err)
		return c.SendString("Error refreshing post")
	}

	postHTML := renderPostHTML(postID, author, content, createdAt, currentUser, points, allowJ, allowC, img, cat, voice, allowComments, followersOnly, expiresAt, dpCompID)
	commentsHTML := renderCommentsThreaded(postID, currentUser, author)

	pageTitle, pageDesc := buildPostMeta(author, content, cat)
	_ = pageDesc // wired into Layout() below once it accepts a description

	fullHTML := fmt.Sprintf(`
        <div class="bg-white dark:bg-gray-900 rounded-2xl shadow-xl py-4 sm:py-8 px-0 border border-gray-100 dark:border-gray-700 max-w-full overflow-hidden">
            <a href="/" class="inline-block mb-4 text-blue-600 dark:text-blue-400 hover:underline text-sm font-bold">← Back</a>
            %s
            <div class="mt-6 sm:mt-8" id="comments-section">
                %s
            </div>
        </div>
        <script>
            let pollInterval = "every 5s";

            function toggleReply(id) {
                const form = document.getElementById('reply-form-' + id);
                const isHidden = form.classList.contains('hidden');
                const poller = document.getElementById('poll-trigger');
                
                document.querySelectorAll('.reply-form').forEach(f => f.classList.add('hidden'));
                
                if (isHidden) {
                    form.classList.remove('hidden');
                    poller.removeAttribute('hx-trigger');
                    htmx.process(poller); 
                    const input = form.querySelector('input');
                    if(input) input.focus();
                } else {
                    form.classList.add('hidden');
                    poller.setAttribute('hx-trigger', pollInterval);
                    htmx.process(poller); 
                }
            }

            // Block background refresh while a reply form is open
            document.addEventListener('htmx:beforeRequest', function(evt) {
                const activeForm = document.querySelector('.reply-form:not(.hidden)');
                if (activeForm && evt.detail.requestConfig.method === 'GET') {
                    if (evt.detail.target && evt.detail.target.id === 'comments-list') {
                        evt.preventDefault();
                    }
                }
            });

            // Restore polling and close reply forms after a successful POST
            document.addEventListener('htmx:afterRequest', function(evt) {
                if (evt.detail.successful && evt.detail.requestConfig.method === 'POST') {
                    // Close all open reply forms
                    document.querySelectorAll('.reply-form').forEach(f => f.classList.add('hidden'));
                    // Restore polling
                    const poller = document.getElementById('poll-trigger');
                    if (poller) {
                        poller.setAttribute('hx-trigger', pollInterval);
                        htmx.process(poller);
                    }
                }
            });

            // Remove error messages automatically after 5 seconds
            document.addEventListener('htmx:afterSwap', function(evt) {
                const errors = evt.detail.target.querySelectorAll('[role="alert"]');
                errors.forEach(el => {
                    setTimeout(() => {
                        el.style.transition = 'opacity 0.4s';
                        el.style.opacity = '0';
                        setTimeout(() => el.remove(), 400);
                    }, 5000);
                });
            });
        </script>
    `, postHTML, commentsHTML)

	c.Set("Content-Type", "text/html")
	return c.SendString(Layout(pageTitle, fullHTML, currentUser))
}

// buildPostMeta derives a unique, content-based <title> and meta description
// for a post page, instead of the generic "Post • username" used before.
// Google/AdSense need distinct titles+descriptions per URL to index and
// approve the site properly.
func buildPostMeta(author, content, category string) (title, description string) {
	plain := strings.Join(strings.Fields(html.UnescapeString(content)), " ")

	title = truncateRunes(plain, 60)
	if title == "" {
		title = fmt.Sprintf("%s's %s post", author, category)
	}
	title = title + " | Dostana"

	description = truncateRunes(plain, 155)
	if description == "" {
		description = fmt.Sprintf("Read %s's post in %s on Dostana.", author, category)
	}
	return title, description
}

func truncateRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	cut := r[:limit]
	if idx := strings.LastIndexAny(string(cut), " "); idx > 0 {
		cut = []rune(string(cut)[:idx])
	}
	return string(cut) + "…"
}

// ====================== COMMENTS THREAD UI ======================
func renderCommentsThreaded(postID int, currentUser string, postAuthor string) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`
        <div class="space-y-4">
            <h3 class="text-base sm:text-lg font-semibold text-gray-800 dark:text-gray-200 mb-4 flex items-center gap-2">
                <span>💬</span> Conversation
            </h3>
            
            <form hx-post="/comment/%d"
                  hx-target="#comments-list"
                  hx-swap="afterbegin"
                  hx-on::after-request="if(event.detail.successful) this.reset()"
                  class="flex gap-2 mb-6">
                <input name="content" autocomplete="off" placeholder="Write a reply..."
                       class="flex-1 min-w-0 px-3 py-2 bg-gray-50 dark:bg-gray-800 border border-transparent rounded-xl focus:bg-white dark:focus:bg-gray-700 outline-none text-sm">
                <button type="submit" class="bg-blue-600 text-white font-medium px-4 rounded-xl text-sm">Post</button>
            </form>

            <!-- poll-trigger: visible=false but NOT display:none so HTMX can process it -->
            <div id="poll-trigger" 
                 hx-get="/post/%d/comments-only" 
                 hx-trigger="every 5s" 
                 hx-target="#comments-list" 
                 hx-swap="innerHTML"
                 aria-hidden="true"
                 style="position:absolute;width:0;height:0;overflow:hidden;"></div>

            <div id="comments-list" class="space-y-3">
    `, postID, postID))

	rows, err := db.Query(ctx, `
		SELECT c.id, c.author, c.content, c.created_at, c.parent_comment_id,
			   p.author AS parent_author, LEFT(p.content, 40) AS parent_snippet,
			   COALESCE(u.points, 0), COALESCE(u.profile_image, '')
		FROM comments c
		LEFT JOIN comments p ON c.parent_comment_id = p.id
		LEFT JOIN users u ON c.author = u.username
		WHERE c.post_id = $1
		ORDER BY c.created_at DESC`, postID)
	if err != nil {
		log.Printf("renderCommentsThreaded query error: %v", err)
		b.WriteString(`</div></div>`)
		return b.String()
	}
	defer rows.Close()

	for rows.Next() {
		var fc FlatComment
		rows.Scan(&fc.ID, &fc.Author, &fc.Content, &fc.CreatedAt, &fc.ParentID, &fc.ParentAuthor, &fc.ParentSnippet, &fc.Points, &fc.ProfileImage)
		renderFlatComment(&b, fc, currentUser, postAuthor)
	}

	b.WriteString(`</div></div>`)
	return b.String()
}

// ====================== SINGLE COMMENT RENDER ======================
func renderFlatComment(b *strings.Builder, c FlatComment, currentUser string, postAuthor string) {
	editBtn := ""
	if currentUser != "" && currentUser == c.Author {
		editBtn = fmt.Sprintf(`<button hx-get="/comment/%d/edit-form" hx-target="#comment-%d" hx-swap="outerHTML" class="text-blue-400 hover:text-blue-600 transition p-1" title="Edit">
            <svg xmlns="http://www.w3.org/2000/svg" class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" /></svg>
        </button>`, c.ID, c.ID)
	}

	deleteBtn := ""
	if currentUser != "" && (currentUser == c.Author || currentUser == postAuthor) {
		deleteBtn = fmt.Sprintf(`<button hx-delete="/comment/%d" hx-target="#comment-%d" hx-swap="outerHTML" hx-confirm="Delete?" class="text-red-400 hover:text-red-600 transition p-1">
            <svg xmlns="http://www.w3.org/2000/svg" class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" /></svg>
        </button>`, c.ID, c.ID)
	}

	avatarHTML := ""
	if c.ProfileImage == "" {
		avatarHTML = fmt.Sprintf(`<a href="/profile/%s" hx-boost="true" class="w-full h-full bg-gray-200 dark:bg-gray-700 flex items-center justify-center text-[10px] text-gray-500">👤</a>`, c.Author)
	} else {
		avatarHTML = fmt.Sprintf(`<a href="/profile/%s" hx-boost="true"><img src="%s" class="w-full h-full object-cover"></a>`, c.Author, c.ProfileImage)
	}

	referenceContent := ""
	if c.ParentID != nil {
		pAuth := "User"
		if c.ParentAuthor != nil {
			pAuth = *c.ParentAuthor
		}
		pSnip := ""
		if c.ParentSnippet != nil {
			pSnip = *c.ParentSnippet
		}
		referenceContent = fmt.Sprintf(`
            <div class="flex items-center gap-1 text-[11px] text-gray-500 dark:text-gray-400 mt-1 italic">
                <span class="font-bold text-blue-400">➜</span>
                <a href="/profile/%s" hx-boost="true" class="font-medium hover:underline text-blue-400">%s:</a>
                <span class="truncate max-w-[150px]">"%s..."</span>
            </div>`, pAuth, pAuth, pSnip)
	}

	b.WriteString(fmt.Sprintf(`
        <div id="comment-%d" class="group bg-gray-50 dark:bg-gray-800/40 rounded-xl p-3 sm:p-4 border border-transparent hover:border-gray-200 dark:hover:border-gray-700 transition-all mb-3 relative">
            <div class="absolute top-3 right-3 flex items-center gap-2">
                <span class="text-[10px] text-gray-400">%s</span>
                %s
                %s
            </div>
            <div class="flex gap-3">
                <div class="shrink-0 w-9 h-9 rounded-lg overflow-hidden shadow-sm">%s</div>
                <div class="flex-1 min-w-0">
                    <div class="flex items-center gap-1.5 flex-wrap">
                      <a href="/profile/%s" hx-boost="true" class="font-bold text-gray-900 dark:text-white text-xs hover:underline">%s</a>%s
                        <span class="text-emerald-600 dark:text-emerald-400 font-medium text-[11px] sm:text-xs">(%d)</span>
                    </div>
                    %s
                    <div class="text-gray-700 dark:text-gray-300 text-sm mt-1.5 break-words leading-relaxed">%s</div>
                    %s
                    %s
                </div>
            </div>
        </div>
    `, c.ID, formatTwitterTime(c.CreatedAt), editBtn, deleteBtn, avatarHTML, c.Author, c.Author, TrendingStarHTML(c.Author), c.Points, referenceContent, c.Content, renderCommentReactionBar(c.ID, currentUser), renderReplySlotCollapsed(c.ID)))
}

// ====================== REPLY SLOT (HTMX, no page-level JS needed) ======================
// Each comment gets its own #reply-slot-<id> div. The Reply button GETs the
// form into that slot; the form's Cancel button (and a successful submit)
// GET the collapsed button back into the same slot. Both swaps are plain
// HTMX outerHTML swaps — no reliance on any <script> block being present
// on whatever page happens to be rendering this comment.
// renderCommentEditFormHTML shows an inline edit box in place of the
// comment card. c.Content is used as-is (no re-escaping) because
// postCommentHandler/replyToCommentHandler already store it
// HTML-escaped — the same rule renderFlatComment relies on.
func renderCommentEditFormHTML(c FlatComment) string {
	return fmt.Sprintf(`
	<div id="comment-%d" class="bg-gray-50 dark:bg-gray-800/40 rounded-xl p-3 sm:p-4 border-2 border-blue-200 dark:border-blue-800 mb-3">
		<form hx-post="/comment/%d/edit" hx-target="#comment-%d" hx-swap="outerHTML" class="flex flex-col gap-2">
			<textarea name="content" rows="2" maxlength="2000" autofocus
				class="w-full px-3 py-2 bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-lg text-sm outline-none">%s</textarea>
			<div class="flex items-center gap-2 justify-end">
				<button type="button" hx-get="/comment/%d/edit-cancel" hx-target="#comment-%d" hx-swap="outerHTML"
					class="text-[11px] font-bold text-gray-500 px-3 py-1.5 rounded-lg border border-gray-200 dark:border-gray-700">Cancel</button>
				<button type="submit" class="text-[11px] font-bold text-white bg-blue-600 hover:bg-blue-700 px-3 py-1.5 rounded-lg">Save</button>
			</div>
		</form>
	</div>`, c.ID, c.ID, c.ID, c.Content, c.ID, c.ID)
}

// renderCommentByID re-fetches a comment plus everything renderFlatComment
// needs (its parent snippet, its post's author for delete permissions)
// and renders the normal comment card — used by both Cancel and a
// successful Save.
func renderCommentByID(id int, currentUser string) (string, error) {
	var f FlatComment
	var postAuthor string
	err := db.QueryRow(ctx, `
		SELECT c.id, c.author, c.content, c.created_at, c.parent_comment_id,
		       p.author, LEFT(p.content,40), COALESCE(u.points,0), COALESCE(u.profile_image,''),
		       posts.author
		FROM comments c
		LEFT JOIN comments p ON c.parent_comment_id = p.id
		LEFT JOIN users u ON c.author = u.username
		JOIN posts ON posts.id = c.post_id
		WHERE c.id=$1`, id).
		Scan(&f.ID, &f.Author, &f.Content, &f.CreatedAt, &f.ParentID, &f.ParentAuthor, &f.ParentSnippet, &f.Points, &f.ProfileImage, &postAuthor)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	renderFlatComment(&b, f, currentUser, postAuthor)
	return b.String(), nil
}

func commentEditFormHandler(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return c.Status(400).SendString("")
	}
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Status(401).SendString("")
	}
	var f FlatComment
	if err := db.QueryRow(ctx, "SELECT id, author, content, created_at FROM comments WHERE id=$1", id).
		Scan(&f.ID, &f.Author, &f.Content, &f.CreatedAt); err != nil {
		return c.Status(404).SendString("")
	}
	if currentUser != f.Author {
		return c.Status(403).SendString("")
	}
	c.Set("Content-Type", "text/html")
	return c.SendString(renderCommentEditFormHTML(f))
}

func commentEditCancelHandler(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return c.Status(400).SendString("")
	}
	rendered, err := renderCommentByID(id, getCurrentUser(c))
	if err != nil {
		return c.Status(404).SendString("")
	}
	c.Set("Content-Type", "text/html")
	return c.SendString(rendered)
}

// commentEditSubmitHandler is the real authorization gate — checked
// independently of commentEditFormHandler, same reasoning as
// postEditSubmitHandler above.
func commentEditSubmitHandler(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return c.Status(400).SendString("")
	}
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Status(401).SendString("")
	}

	var commentAuthor string
	if err := db.QueryRow(ctx, "SELECT author FROM comments WHERE id=$1", id).Scan(&commentAuthor); err != nil {
		return c.Status(404).SendString("")
	}
	if currentUser != commentAuthor {
		return c.Status(403).SendString("Aap sirf apna hi comment edit kar saktay hain")
	}

	newContent := html.EscapeString(strings.TrimSpace(c.FormValue("content")))
	if newContent == "" {
		rendered, _ := renderCommentByID(id, currentUser)
		return c.Status(400).SendString(rendered)
	}
	if r := []rune(newContent); len(r) > 2000 {
		newContent = string(r[:2000])
	}

	if _, err := db.Exec(ctx, "UPDATE comments SET content=$1 WHERE id=$2", newContent, id); err != nil {
		log.Println("commentEditSubmitHandler update error:", err)
		return c.Status(500).SendString("")
	}

	rendered, err := renderCommentByID(id, currentUser)
	if err != nil {
		return c.Status(500).SendString("")
	}
	c.Set("Content-Type", "text/html")
	return c.SendString(rendered)
}
func renderReplySlotCollapsed(commentID int) string {
	return fmt.Sprintf(`
        <div id="reply-slot-%d" class="mt-3">
            <button hx-get="/comment/%d/reply-form" hx-target="#reply-slot-%d" hx-swap="outerHTML"
                    class="flex items-center gap-1 text-blue-500 font-bold text-[10px] uppercase tracking-wider hover:text-blue-600 transition">
                <svg xmlns="http://www.w3.org/2000/svg" class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 10h10a8 8 0 018 8v2M3 10l6 6m-6-6l6-6" /></svg>
                Reply
            </button>
        </div>`, commentID, commentID, commentID)
}

func renderReplySlotExpanded(commentID int, replyingToAuthor string) string {
	return fmt.Sprintf(`
        <div id="reply-slot-%d" class="reply-form mt-3 bg-white dark:bg-gray-900 p-2 rounded-lg border border-gray-200 dark:border-gray-700 shadow-sm">
            <form hx-post="/comment/%d/reply"
                  hx-target="#comments-list"
                  hx-swap="afterbegin"
                  hx-on::after-request="if(event.detail.successful){ this.reset(); htmx.ajax('GET', '/comment/%d/reply-collapse', {target:'#reply-slot-%d', swap:'outerHTML'}); }"
                  class="flex gap-2">
                <input name="content" autocomplete="off" placeholder="Reply to %s..."
                       class="flex-1 min-w-0 px-3 py-1.5 bg-gray-50 dark:bg-gray-800 border-none rounded-md text-xs outline-none">
                <button type="submit" class="bg-blue-600 text-white text-[10px] px-4 rounded-md font-bold">Send</button>
                <button type="button"
                        hx-get="/comment/%d/reply-collapse" hx-target="#reply-slot-%d" hx-swap="outerHTML"
                        class="text-gray-400 text-[10px] px-2 font-bold">Cancel</button>
            </form>
        </div>`, commentID, commentID, commentID, commentID, replyingToAuthor, commentID, commentID)
}

func replyFormHandler(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return c.Status(400).SendString("")
	}
	var author string
	dbCtx, dbCancel := context.WithTimeout(ctx, 2*time.Second)
	defer dbCancel()
	if err := db.QueryRow(dbCtx, "SELECT author FROM comments WHERE id=$1", id).Scan(&author); err != nil {
		return c.Status(404).SendString("")
	}
	c.Set("Content-Type", "text/html")
	return c.SendString(renderReplySlotExpanded(id, author))
}

func replyCollapseHandler(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return c.Status(400).SendString("")
	}
	c.Set("Content-Type", "text/html")
	return c.SendString(renderReplySlotCollapsed(id))
}

// ====================== BACKEND HANDLERS ======================

func postCommentHandler(c *fiber.Ctx) error {
	postID, _ := strconv.Atoi(c.Params("id"))
	author, err := requireUser(c)
	if err != nil {
		return err
	}
	content := html.EscapeString(strings.TrimSpace(c.FormValue("content")))

	if content == "" {
		return c.Status(400).SendString(errorHTML("✏️", "Empty comment", "Please write something before posting."))
	}

	// ── Fetch post author for guard + notification ──────────────────────────
	var postAuthor string
	var allowComments, followersOnly bool
	dbCtx, dbCancel := context.WithTimeout(ctx, 3*time.Second)
	defer dbCancel()
	_ = db.QueryRow(dbCtx, "SELECT author, allow_comments, followers_only FROM posts WHERE id=$1", postID).Scan(&postAuthor, &allowComments, &followersOnly)

	// ── Permission check ────────────────────────────────────────────────────
	if errHTML := checkCommentAllowed(author, postAuthor, allowComments, followersOnly); errHTML != "" {
		// Return 200 so HTMX swaps the error into the list; the error auto-removes after 5s.
		c.Set("Content-Type", "text/html")
		return c.SendString(errHTML)
	}

	// ── Insert comment ──────────────────────────────────────────────────────
	var commentID int
	insCtx, insCancel := context.WithTimeout(ctx, 5*time.Second)
	defer insCancel()
	err = db.QueryRow(insCtx,
		"INSERT INTO comments (post_id, author, content) VALUES ($1, $2, $3) RETURNING id",
		postID, author, content,
	).Scan(&commentID)
	if err != nil {
		log.Printf("postCommentHandler insert error: %v", err)
		return c.Status(500).SendString(errorHTML("⚠️", "Server error", "Could not save your comment. Please try again."))
	}

	// ── Points (buffered via Redis, flushed every 10s) ──────────────────────
	if pts := awardConversationPoints(author, postAuthor); pts > 0 {
		bufferPoints(author, pts)
	}

	// ── Notification (non-blocking) ─────────────────────────────────────────
	if postAuthor != "" && postAuthor != author {
		go CreateNotification(postAuthor, author, "reply", postID, commentID, content)
		Push(postAuthor, author, "commented: "+content, postLink(postID))
	}

	// ── Mentions in this comment (non-blocking) ─────────────────────────────
	go ExtractAndNotifyCommentMentions(postID, commentID, author, content)

	return returnSingleComment(c, commentID, author, postAuthor)
}

func replyToCommentHandler(c *fiber.Ctx) error {
	parentID, _ := strconv.Atoi(c.Params("id"))
	author, err := requireUser(c)
	if err != nil {
		return err
	}
	content := html.EscapeString(strings.TrimSpace(c.FormValue("content")))

	if content == "" {
		return c.Status(400).SendString(errorHTML("✏️", "Empty reply", "Please write something before replying."))
	}

	// ── Fetch parent comment info ───────────────────────────────────────────
	var postID int
	var parentAuthor string
	dbCtx, dbCancel := context.WithTimeout(ctx, 3*time.Second)
	defer dbCancel()
	if err := db.QueryRow(dbCtx,
		"SELECT post_id, author FROM comments WHERE id=$1", parentID,
	).Scan(&postID, &parentAuthor); err != nil {
		return c.Status(404).SendString(errorHTML("🔍", "Not found", "The comment you're replying to no longer exists."))
	}

	// ── Fetch post author for block check ───────────────────────────────────
	var postAuthor string
	var allowComments, followersOnly bool
	_ = db.QueryRow(dbCtx, "SELECT author, allow_comments, followers_only FROM posts WHERE id=$1", postID).Scan(&postAuthor, &allowComments, &followersOnly)

	// ── Permission check ────────────────────────────────────────────────────
	if errHTML := checkCommentAllowed(author, postAuthor, allowComments, followersOnly); errHTML != "" {
		c.Set("Content-Type", "text/html")
		return c.SendString(errHTML)
	}

	// ── Insert reply ────────────────────────────────────────────────────────
	var newID int
	insCtx, insCancel := context.WithTimeout(ctx, 5*time.Second)
	defer insCancel()
	if err := db.QueryRow(insCtx,
		"INSERT INTO comments (post_id, author, content, parent_comment_id) VALUES ($1, $2, $3, $4) RETURNING id",
		postID, author, content, parentID,
	).Scan(&newID); err != nil {
		log.Printf("replyToCommentHandler insert error: %v", err)
		return c.Status(500).SendString(errorHTML("⚠️", "Server error", "Could not save your reply. Please try again."))
	}

	// ── Points (buffered) ───────────────────────────────────────────────────
	if pts := awardConversationPoints(author, parentAuthor); pts > 0 {
		bufferPoints(author, pts)
	}

	// ── Notification (non-blocking) ─────────────────────────────────────────
	if parentAuthor != "" && parentAuthor != author {
		go CreateNotification(parentAuthor, author, "reply", postID, newID, content)
		Push(parentAuthor, author, "replied: "+content, postLink(postID))
	}

	// ── Mentions in this reply (non-blocking) ───────────────────────────────
	go ExtractAndNotifyCommentMentions(postID, newID, author, content)

	return returnSingleComment(c, newID, author, postAuthor)
}

func deleteCommentHandler(c *fiber.Ctx) error {
	id, _ := strconv.Atoi(c.Params("id"))
	user := getCurrentUser(c)
	if user == "" {
		return c.Status(401).SendString("Unauthorized")
	}

	var commentAuthor, postAuthor string
	dbCtx, dbCancel := context.WithTimeout(ctx, 3*time.Second)
	defer dbCancel()
	err := db.QueryRow(dbCtx, `
		SELECT c.author, p.author
		FROM comments c
		JOIN posts p ON c.post_id = p.id
		WHERE c.id = $1`, id).Scan(&commentAuthor, &postAuthor)
	if err != nil {
		return c.Status(404).SendString("Not found")
	}

	// Allow the comment's own author, or the owner of the post it's on
	// (post owners can moderate comments on their own posts).
	if user != commentAuthor && user != postAuthor {
		return c.Status(401).SendString("Unauthorized")
	}

	delCtx, delCancel := context.WithTimeout(ctx, 3*time.Second)
	defer delCancel()
	_, _ = db.Exec(delCtx, "DELETE FROM comments WHERE id=$1", id)
	return c.SendString("")
}

func commentsOnlyHandler(c *fiber.Ctx) error {
	postID, _ := strconv.Atoi(c.Params("id"))
	currentUser := getCurrentUser(c)

	dbCtx, dbCancel := context.WithTimeout(ctx, 5*time.Second)
	defer dbCancel()

	var postAuthor string
	_ = db.QueryRow(dbCtx, "SELECT author FROM posts WHERE id=$1", postID).Scan(&postAuthor)

	rows, err := db.Query(dbCtx, `
		SELECT c.id, c.author, c.content, c.created_at, c.parent_comment_id, 
		       p.author, LEFT(p.content, 40), 
		       COALESCE(u.points, 0), COALESCE(u.profile_image, '') 
		FROM comments c 
		LEFT JOIN comments p ON c.parent_comment_id = p.id 
		LEFT JOIN users u ON c.author = u.username 
		WHERE c.post_id = $1 
		ORDER BY c.created_at DESC`, postID)
	if err != nil {
		return c.Status(500).SendString("DB Error")
	}
	defer rows.Close()

	var b strings.Builder
	for rows.Next() {
		var f FlatComment
		_ = rows.Scan(&f.ID, &f.Author, &f.Content, &f.CreatedAt, &f.ParentID, &f.ParentAuthor, &f.ParentSnippet, &f.Points, &f.ProfileImage)
		renderFlatComment(&b, f, currentUser, postAuthor)
	}
	c.Set("Content-Type", "text/html")
	return c.SendString(b.String())
}

func invalidateBlockCache(userA, userB string) {
	keyA, keyB := userA, userB
	if userA > userB {
		keyA, keyB = userB, userA
	}
	rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rdb.Del(rctx, "block_rel:"+keyA+":"+keyB)
}

func returnSingleComment(c *fiber.Ctx, id int, user string, postAuthor string) error {
	dbCtx, dbCancel := context.WithTimeout(ctx, 5*time.Second)
	defer dbCancel()

	var f FlatComment
	err := db.QueryRow(dbCtx, `
		SELECT c.id, c.author, c.content, c.created_at, c.parent_comment_id, 
		       p.author, LEFT(p.content, 40), 
		       COALESCE(u.points, 0), COALESCE(u.profile_image, '') 
		FROM comments c 
		LEFT JOIN comments p ON c.parent_comment_id = p.id 
		LEFT JOIN users u ON c.author = u.username 
		WHERE c.id = $1`, id).
		Scan(&f.ID, &f.Author, &f.Content, &f.CreatedAt, &f.ParentID, &f.ParentAuthor, &f.ParentSnippet, &f.Points, &f.ProfileImage)
	if err != nil {
		log.Printf("returnSingleComment scan error: %v", err)
		return c.Status(500).SendString(errorHTML("⚠️", "Render error", "Comment saved but couldn't display it. Refresh to see it."))
	}

	var b strings.Builder
	renderFlatComment(&b, f, user, postAuthor)
	c.Set("Content-Type", "text/html")
	return c.SendString(b.String())
}

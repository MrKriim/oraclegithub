package main

import (
	"context"
	"fmt"
	"html"
	"mysocialmediawebsite/pkg/platform"
	"strings"
	"time"
	"unicode"

	"github.com/gofiber/fiber/v2"
)

const postCooldown = 5 * time.Second

// gameGuessLooksLikeAnswer does a cheap, best-effort check for whether a
// post's raw content could be a live Word Game or Trivia guess — a short,
// single-line, no-media submission. Used only to decide whether to skip
// the per-user post cooldown below; the actual win/loss check still
// happens later in checkWordGameGuess/checkTriviaGuess against the real
// DB-held answer, so this never grants points on its own.

// ─────────────────────────────────────────────────────────────────────────────
// SPAM GUARD — duplicate content, rapid-fire posting, and trivial
// (emoji-only / 1-character) posts.
//
// Only applies to users who already have at least one previous post
// (postCount > 0 in postHandler) — brand-new users with zero posts are
// never subject to these checks, since there's nothing to compare against.
//
// Violations are tracked in Redis on a rolling window. Once a user hits
// spamViolationLimit violations, they get temporarily banned from
// posting: first offense = 2 minutes, every offense after that (while
// they're still in the "repeat offender" window) = 5 minutes.
// ─────────────────────────────────────────────────────────────────────────────

const (
	spamMinPostInterval   = 2 * time.Second  // "last post 1 second ago" territory
	spamViolationWindow   = 10 * time.Minute // rolling window violations are counted over
	spamViolationLimit    = 3                // 3-4 strikes before a ban kicks in
	spamBanFirstDuration  = 2 * time.Minute
	spamBanRepeatDuration = 5 * time.Minute
	spamBanLevelTTL       = 2 * time.Hour // how long "repeat offender" status sticks
)

func spamBanKey(username string) string        { return "spam_ban:" + username }
func spamViolationsKey(username string) string  { return "spam_violations:" + username }
func spamBanLevelKey(username string) string    { return "spam_ban_level:" + username }

// checkSpamBan reports whether the user is currently serving a temporary
// spam-ban, and how much time is left on it.
func checkSpamBan(author string) (bool, time.Duration) {
	rctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	ttl, err := platform.RDB.TTL(rctx, spamBanKey(author)).Result()
	if err != nil || ttl <= 0 {
		return false, 0
	}
	return true, ttl
}

// isEmojiRune is a best-effort check for common emoji code blocks.
func isEmojiRune(r rune) bool {
	switch {
	case r >= 0x1F300 && r <= 0x1FAFF:
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return true
	case r >= 0x2190 && r <= 0x21FF:
		return true
	case r >= 0xFE00 && r <= 0xFE0F: // variation selectors
		return true
	case r >= 0x1F1E6 && r <= 0x1F1FF: // flag letters
		return true
	case r == 0x2764 || r == 0x2665:
		return true
	}
	return false
}

func isEmojiOnlyContent(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		if !isEmojiRune(r) {
			return false
		}
	}
	return true
}

// isRepeatedWordSpam catches posts like "abc abc abc abc" — the same
// single word repeated over and over as the entire post.
func isRepeatedWordSpam(content string) bool {
	fields := strings.Fields(strings.ToLower(content))
	if len(fields) < 3 {
		return false
	}
	first := fields[0]
	for _, w := range fields[1:] {
		if w != first {
			return false
		}
	}
	return true
}

// spamReasonAndMessage compares the new post against the user's last post
// and flags it if it looks like spam. Only call this when postCount > 0.
func spamContentReason(content, lastContent string, hasMedia bool) (reason, message string) {
	trimmed := strings.TrimSpace(content)

	if hasMedia || trimmed == "" {
		return "", ""
	}
	if strings.EqualFold(trimmed, strings.TrimSpace(lastContent)) {
		return "duplicate", "⚠️ Bar bar aik hi baat na likhein! Kuch naya likhein."
	}
	if isRepeatedWordSpam(trimmed) {
		return "duplicate", "⚠️ Bar bar aik hi baat na likhein! Kuch naya likhein."
	}
	if len([]rune(trimmed)) <= 1 || isEmojiOnlyContent(trimmed) {
		return "trivial", "⚠️ Sirf emoji ya 1 letter wali post allowed nahi hai. Thora likhein."
	}
	return "", ""
}

// spamFastReason flags rapid-fire posting. This one IS skipped for
// likely game guesses by the caller — guessing quickly in a live Word
// Game / Trivia round is expected, not spamming.
func spamFastReason(now, lastCreatedAt time.Time) (reason, message string) {
	if lastCreatedAt.Year() > 1970 && now.Sub(lastCreatedAt) < spamMinPostInterval {
		return "fast", "🐢 Itni jaldi jaldi post na karein! Spamming na karein, thora sabar karein."
	}
	return "", ""
}

// registerSpamViolation bumps the rolling violation counter and, once the
// limit is hit, applies (or escalates) a temporary posting ban.
// Returns (banApplied, banDuration).
func registerSpamViolation(author string) (bool, time.Duration) {
	rctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	count, err := platform.RDB.Incr(rctx, spamViolationsKey(author)).Result()
	if err != nil {
		return false, 0
	}
	if count == 1 {
		platform.RDB.Expire(rctx, spamViolationsKey(author), spamViolationWindow)
	}
	if count < spamViolationLimit {
		return false, 0
	}

	// Acting on it now — reset the strike counter.
	platform.RDB.Del(rctx, spamViolationsKey(author))

	levelStr, _ := platform.RDB.Get(rctx, spamBanLevelKey(author)).Result()
	duration := spamBanFirstDuration
	if levelStr != "" {
		duration = spamBanRepeatDuration // already a repeat offender
	}

	platform.RDB.Set(rctx, spamBanKey(author), "1", duration)
	platform.RDB.Set(rctx, spamBanLevelKey(author), "1", spamBanLevelTTL)

	return true, duration
}
func gameGuessLooksLikeAnswer(content string) bool {
	c := strings.TrimSpace(content)
	if c == "" || strings.Contains(c, "\n") {
		return false
	}
	return len([]rune(c)) <= 60
}

func postHandler(c *fiber.Ctx) error {
	author := getCurrentUser(c)
	if author == "" {
		return c.Status(fiber.StatusUnauthorized).SendString("Pehle login karein!")
	}

	// --- 1. BAN CHECK (FAST FAILURE) ---
	isBanned := false
	cacheKey := "ban_check:" + author
	rctx, rcancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer rcancel()

	if val, err := platform.RDB.Get(rctx, cacheKey).Result(); err == nil {
		isBanned = val == "1"
	} else {
		dbCtx, dbCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer dbCancel()
		_ = db.QueryRow(dbCtx, "SELECT is_banned FROM users WHERE username=$1", author).Scan(&isBanned)
		cacheVal := "0"
		if isBanned {
			cacheVal = "1"
		}
		go func(user string, valStr string) {
			ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel2()
			platform.RDB.Set(ctx2, "ban_check:"+user, valStr, 2*time.Minute)
		}(author, cacheVal)
	}

		if isBanned {
		return c.Status(fiber.StatusForbidden).SendString(`
			<div hx-swap-oob="innerHTML:#post-error">
				<div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 p-4 rounded-2xl mb-4 flex items-center justify-between">
					<p class="text-red-700 dark:text-red-400 text-sm font-bold">
						🚫 Aap ka account ban hai! Aap nayi post nahi kar sakte.
					</p>
					<button onclick="this.parentElement.remove()">✕</button>
				</div>
			</div>`)
	}

	// --- 1b. SPAM BAN CHECK (temporary posting ban from repeated spam) ---
	if spamBanned, remaining := checkSpamBan(author); spamBanned {
		mins := int(remaining.Minutes())
		secs := int(remaining.Seconds()) % 60
		return c.Status(fiber.StatusTooManyRequests).SendString(fmt.Sprintf(`
			<div hx-swap-oob="innerHTML:#post-error">
				<div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 p-4 rounded-2xl mb-4 flex items-center justify-between">
					<p class="text-red-700 dark:text-red-400 text-sm font-bold">
						🚫 Spamming ki wajah se aap %d minute %d second ke liye block hain.
					</p>
					<button onclick="this.parentElement.remove()">✕</button>
				</div>
			</div>`, mins, secs))
	}

	rawContent := strings.TrimSpace(c.FormValue("content"))

	// --- 2. ABUSE FILTER ---
	badWords := []string{"sex", "porn", "randi", "kanjri", "lun", "chot", "chut", "ass", "boobs", "penis", "dick", "vagina", "lund"}
	contentLower := strings.ToLower(rawContent)
	replacer := strings.NewReplacer(
		"@", "a", "4", "a", "3", "e", "1", "i", "!", "i",
		"0", "o", "$", "s", "5", "s", "7", "t", "(", "c",
		".", "", "-", "", "_", "",
	)
	normalized := replacer.Replace(contentLower)
	userWords := strings.Fields(normalized)

	isAbusive := false
	foundWord := ""

	for _, userWord := range userWords {
		for _, badWord := range badWords {
			if userWord == strings.ToLower(badWord) {
				foundWord = userWord
				isAbusive = true
				break
			}
		}
		if isAbusive {
			break
		}
	}

	if !isAbusive {
		for _, badWord := range badWords {
			if strings.Contains(badWord, " ") {
				if strings.Contains(normalized, strings.ReplaceAll(strings.ToLower(badWord), " ", "")) {
					foundWord = badWord
					isAbusive = true
					break
				}
			}
		}
	}

	if isAbusive {
		return c.Status(fiber.StatusUnprocessableEntity).SendString(fmt.Sprintf(`
			<div hx-swap-oob="innerHTML:#post-error">
				<div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 p-4 rounded-2xl mb-4 flex items-center justify-between">
					<p class="text-red-700 dark:text-red-400 text-sm font-bold">
						⚠️ Dostana par galiyan dena mana he! Aap post me '<b>%s</b>' nahi likh sakte.
					</p>
					<button onclick="this.parentElement.remove()">✕</button>
				</div>
			</div>`, foundWord))
	}

	// --- 3. PREPARE INPUTS ---
	content := rawContent
	category := html.EscapeString(c.FormValue("category"))
	if category == "" {
		category = "General"
	}

	imageUrl := c.FormValue("image_url")
	if imageUrl == "" {
		videoUrl, vErr := HandleVideoUpload(c)
		if vErr != nil {
			return c.Status(fiber.StatusUnprocessableEntity).SendString(fmt.Sprintf(`
				<div hx-swap-oob="innerHTML:#post-error">
					<div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 p-4 rounded-2xl mb-4 flex items-center justify-between">
						<p class="text-red-700 dark:text-red-400 text-sm font-bold">⚠️ %s</p>
						<button onclick="this.parentElement.remove()">✕</button>
					</div>
				</div>`, html.EscapeString(vErr.Error())))
		}
		if videoUrl != "" {
			imageUrl = videoUrl
		} else {
			uploadedUrl, err := HandleImageUpload(c)
			if err == nil {
				imageUrl = uploadedUrl
			}
		}
	}

	voiceUrl := c.FormValue("voice_url")

	source := c.FormValue("source")
	if source != "quickthought" {
		source = "share" // any post not explicitly marked quickthought defaults to share
	}

	universal := fetchUniversalPostSettings(author)
	disabledReactions := map[string]bool{}
	for k, v := range universal.DisabledReactions {
		disabledReactions[k] = v
	}
	for _, r := range reactionList {
		if c.FormValue("disable_"+r.Key) == "on" {
			disabledReactions[r.Key] = true
		}
	}
	var disabledKeys []string
	for k := range disabledReactions {
		disabledKeys = append(disabledKeys, k)
	}
	disabledReactionsCSV := strings.Join(disabledKeys, ",")
	allowJhappi := true
	allowChupair := true
	allowComments := c.FormValue("disable_comments") != "on" && !universal.DisableComments
	followersOnly := c.FormValue("followers_only") == "on"

	if content == "" && imageUrl == "" && voiceUrl == "" {
		return c.Status(fiber.StatusBadRequest).SendString("Khali post nahi hosakti.")
	}

	// Pinned to Asia/Karachi explicitly (not time.Local, which depends on
	// the server OS's configured timezone) so this always agrees with
	// Postgres's session timezone — set to Asia/Karachi in initDatabase —
	// which is what userCreatedAt/lastCreatedAt/lastImageAt come back in.
	loc, locErr := time.LoadLocation("Asia/Karachi")
	if locErr != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)

	expiresChoice := c.FormValue("expires_in")
	if expiresChoice == "" {
		expiresChoice = universal.ExpiresIn
	}
	var expiresAt *time.Time
	switch expiresChoice {
	case "24h":
		t := now.Add(24 * time.Hour)
		expiresAt = &t
	case "3d":
		t := now.Add(72 * time.Hour)
		expiresAt = &t
	}

	// --- 4. TRANSACTION & COOLDOWN ---
	txCtx, txCancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer txCancel()

	tx, err := db.Begin(txCtx)
	if err != nil {
		return c.Status(500).SendString("Database connection error")
	}
	defer tx.Rollback(txCtx)

	_, err = tx.Exec(txCtx, "SELECT pg_advisory_xact_lock(hashtext($1))", author)
	if err != nil {
		return c.Status(500).SendString("Locking error")
	}

	var userCreatedAt time.Time
	var postCount int
	var lastCreatedAt time.Time
	var lastImageAt time.Time
	var lastContent string
	var userType string

	err = tx.QueryRow(txCtx, `
		SELECT 
			u.created_at,
			(SELECT COUNT(*) FROM posts WHERE author = $1),
			(SELECT COALESCE(MAX(created_at), '1970-01-01 00:00:00') FROM posts WHERE author = $1),
			(SELECT COALESCE(MAX(created_at), '1970-01-01 00:00:00') FROM posts WHERE author = $1 AND image_url != ''),
			COALESCE((SELECT content FROM posts WHERE author = $1 ORDER BY created_at DESC LIMIT 1), ''),
			COALESCE(u.user_type, '')
		FROM users u 
		WHERE u.username = $1
	`, author).Scan(&userCreatedAt, &postCount, &lastCreatedAt, &lastImageAt, &lastContent, &userType)

	if err != nil {
		return c.Status(500).SendString("Error fetching user data and cooldowns")
	}

	isAdminPoster := strings.EqualFold(userType, "admin")

	// --- Spam / duplicate / trivial / rapid-fire post detection ---
	// Skipped entirely for brand-new users (postCount == 0) — no history
	// to compare against yet — and for admin accounts.
	//
	// IMPORTANT: the "likely game guess" shape (short, single-line,
	// no-media) matches almost any casual short post — including plain
	// spam ("qwd", "asdasda", "😂😂" typed over and over). So it must NOT
	// exempt content from the duplicate/trivial checks, only from the
	// rapid-posting timer below — otherwise spammers slip through by
	// simply keeping every post short, exactly as happened here.
	isLikelyGuess := gameGuessLooksLikeAnswer(content)
	if postCount > 0 && !isAdminPoster {
		hasMedia := imageUrl != "" || voiceUrl != ""

		reason, msg := spamContentReason(content, lastContent, hasMedia)
		if reason == "" && !isLikelyGuess {
			reason, msg = spamFastReason(now, lastCreatedAt)
		}

		if reason != "" {
			banned, banDur := registerSpamViolation(author)
			if banned {
				mins := int(banDur.Minutes())
				return c.Status(fiber.StatusTooManyRequests).SendString(fmt.Sprintf(`
					<div hx-swap-oob="innerHTML:#post-error">
						<div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 p-4 rounded-2xl mb-4 flex items-center justify-between">
							<p class="text-red-700 dark:text-red-400 text-sm font-bold">
								🚫 Bar bar spamming karne ki wajah se aap %d minute ke liye block ho gaye hain.
							</p>
							<button onclick="this.parentElement.remove()">✕</button>
						</div>
					</div>`, mins))
			}
			return c.Status(fiber.StatusUnprocessableEntity).SendString(fmt.Sprintf(`
				<div hx-swap-oob="innerHTML:#post-error">
					<div class="bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 p-4 rounded-2xl mb-4 flex items-center justify-between">
						<p class="text-amber-700 dark:text-amber-400 text-sm font-bold">%s</p>
						<button onclick="this.parentElement.remove()">✕</button>
					</div>
				</div>`, msg))
		}
	}

	if imageUrl != "" {
		if now.Sub(userCreatedAt) < 7*24*time.Hour {
			return c.Status(fiber.StatusUnprocessableEntity).SendString(`
				<div hx-swap-oob="innerHTML:#post-error">
					<div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 p-4 rounded-2xl mb-4 flex items-center justify-between">
						<p class="text-red-700 dark:text-red-400 text-sm">
							🛡️ <b>Naye accounts image post nahi kar sakte.</b> Aap ka account 7 din purana hona chahiye. Text post try karein!
						</p>
						<button onclick="this.parentElement.remove()">✕</button>
					</div>
				</div>`)
		}

		const imageCooldown = 1 * time.Minute
		if lastImageAt.Year() > 1970 {
			elapsedImage := now.Sub(lastImageAt)
			if elapsedImage < imageCooldown {
				remaining := int((imageCooldown - elapsedImage).Seconds())
				if remaining < 1 {
					remaining = 1
				}
				return c.Status(fiber.StatusTooManyRequests).SendString(fmt.Sprintf(`
					<div hx-swap-oob="innerHTML:#post-error">
						<div class="bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 p-4 rounded-2xl mb-4 flex items-center justify-between">
							<p class="text-amber-700 dark:text-amber-400 text-sm">
								📸 Sabar karein! Aap nayi image <b>%d</b> seconds baad post kar sakein gay. (Text posts abhi allowed hain)
							</p>
							<button onclick="this.parentElement.remove()">✕</button>
						</div>
					</div>`, remaining))
			}
		}
	}

	// Word Game / Trivia answers are submitted as ordinary posts, so the
	// per-user cooldown below used to block someone from firing off a
	// second guess (their own retry, or a guess at a different live
	// game) within postCooldown of their last post — throttling exactly
	// the users we want fast, low-friction guessing from. Short,
	// single-line submissions skip the cooldown entirely; this never
	// grants points by itself, it only decides whether to wait, and the
	// real answer check still happens later against the DB-held value.
	if postCount > 0 && !gameGuessLooksLikeAnswer(content) {
		elapsed := now.Sub(lastCreatedAt)
		if elapsed < postCooldown {
			remaining := int((postCooldown - elapsed).Seconds())
			if remaining < 1 {
				remaining = 1
			}
			return c.Status(fiber.StatusTooManyRequests).SendString(fmt.Sprintf(`
				<div hx-swap-oob="innerHTML:#post-error">
					<div class="bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 p-4 rounded-2xl mb-4 flex items-center justify-between">
						<p class="text-amber-700 dark:text-amber-400 text-sm">
							Sabar karein! Aap <b>%d</b> seconds baad post kar sakein gay.
						</p>
						<button onclick="this.parentElement.remove()">✕</button>
					</div>
				</div>`, remaining))
		}
	}

	var newPostID int
	var dbCreatedAt time.Time

	err = tx.QueryRow(txCtx, `
		INSERT INTO posts (content, author, created_at, allow_jhappi, allow_chupair, image_url, category, voice_url, allow_comments, followers_only, expires_at, disabled_reactions, source) 
		VALUES ($1, $2, CURRENT_TIMESTAMP, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) 
		RETURNING id, created_at`,
		content, author, allowJhappi, allowChupair, imageUrl, category, voiceUrl,
		allowComments, followersOnly, expiresAt, disabledReactionsCSV, source).Scan(&newPostID, &dbCreatedAt)
	if err != nil {
		return c.Status(500).SendString("Failed to save post")
	}

	// Update points and RETURNING in single step (Fixes Bug 1)
	var currentPoints int
	err = tx.QueryRow(txCtx, "UPDATE users SET points = COALESCE(points, 0) + 1 WHERE username = $1 RETURNING points", author).Scan(&currentPoints)
	if err != nil {
		return c.Status(500).SendString("Points update error")
	}

	if err := tx.Commit(txCtx); err != nil {
		return c.Status(500).SendString("Commit error")
	}

	// Warm the Redis-first trending path immediately so the first jhappi
	// on this post never falls back to a cold-cache DB read — see
	// trending_posts.go (CachePostMeta / RegisterTrendingCandidate).
	CachePostMeta(newPostID, author, content, imageUrl, category, dbCreatedAt)
	RegisterTrendingCandidate(newPostID, category)
	ExtractAndNotifyMentions(newPostID, author, content)

	publishNewPost(newPostID, imageUrl, author)

	// Targeted cache deletion (Fixes Bug 2)
	go func() {
		delCtx, delCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer delCancel()
		platform.RDB.Del(delCtx, "feed:u:"+author, "post_count:all", "post_count:text", "post_count:pics")
	}()
	checkWordGameGuess(newPostID, author, content)
	checkTriviaGuess(newPostID, author, content)
	c.Set("HX-Trigger", "postCreated")
	return c.SendString(renderPostHTML(
		newPostID,
		author,
		rawContent,
		now,
		author,
		currentPoints,
		allowJhappi,
		allowChupair,
		imageUrl,
		category,
		voiceUrl,
		allowComments,
		followersOnly,
		expiresAt,
		0,
	))
}

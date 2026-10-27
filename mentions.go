package main

import (
	"context"
	"log"
	"regexp"
	"strings"
	"time"

	"mysocialmediawebsite/pkg/platform"

	"github.com/gofiber/fiber/v2"
	goredis "github.com/redis/go-redis/v9"
)

// mentionRegex pulls @username tokens out of post content. Same charset
// you'd expect usernames to use (letters/digits/underscore, 2-32 chars).
var mentionRegex = regexp.MustCompile(`(?:^|[\s(])@([A-Za-z0-9_]{2,32})`)

const usernameIndexKey = "username_index" // Redis ZSET, lexicographically sorted lowercase usernames

// ─────────────────────────────────────────────────────────────────────────
// REDIS USERNAME INDEX — O(log N) prefix search, zero DB load per keystroke
// ─────────────────────────────────────────────────────────────────────────

// IndexUsername adds a username to the autocomplete index. Call this right
// after a new user row is inserted in signupPost.
func IndexUsername(username string) {
	rctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	platform.RDB.ZAdd(rctx, usernameIndexKey, goredis.Z{Score: 0, Member: strings.ToLower(username)})
}

// backfillUsernameIndex populates the ZSET from Postgres once at boot.
// Idempotent (ZADD on an existing member is a cheap no-op), pipelined so
// it doesn't hammer Redis with one round trip per user.
func backfillUsernameIndex() {
	goSafe("backfillUsernameIndex", func() {
		dbCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rows, err := db.Query(dbCtx, "SELECT username FROM users")
		if err != nil {
			return
		}
		defer rows.Close()

		rctx, rcancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer rcancel()
		pipe := platform.RDB.Pipeline()
		count := 0
		for rows.Next() {
			var u string
			if rows.Scan(&u) == nil {
				pipe.ZAdd(rctx, usernameIndexKey, goredis.Z{Score: 0, Member: strings.ToLower(u)})
				count++
				if count%500 == 0 {
					pipe.Exec(rctx)
					pipe = platform.RDB.Pipeline()
				}
			}
		}
		pipe.Exec(rctx)
	})
}

// searchUsernamesByPrefix does a ZRANGEBYLEX range query — the classic
// Redis autocomplete trick. No Postgres hit at all.
func searchUsernamesByPrefix(prefix string, limit int) []string {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return nil
	}
	rctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	vals, err := platform.RDB.ZRangeByLex(rctx, usernameIndexKey, &goredis.ZRangeBy{
		Min:   "[" + prefix,
		Max:   "[" + prefix + "\xff",
		Count: int64(limit),
	}).Result()
	if err != nil {
		return nil
	}
	return vals
}

// ─────────────────────────────────────────────────────────────────────────
// MENTION PERMISSIONS — policy + block check, Redis-cached like ban/block
// ─────────────────────────────────────────────────────────────────────────

func mentionPolicyCacheKey(username string) string { return "mention_policy:" + username }

// getMentionPolicy returns "everyone" | "followers" | "none", cached 5 min.
func getMentionPolicy(username string) string {
	if v, ok := CacheGet(mentionPolicyCacheKey(username)); ok {
		return v
	}
	dbCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	policy := "everyone"
	_ = db.QueryRow(dbCtx, "SELECT COALESCE(mention_policy,'everyone') FROM users WHERE username=$1", username).Scan(&policy)
	CacheSet(mentionPolicyCacheKey(username), policy, 5*time.Minute)
	return policy
}

func InvalidateMentionPolicy(username string) { CacheDel(mentionPolicyCacheKey(username)) }

func isFollowingCached(follower, followee string) bool {
	key := "is_following:" + follower + ":" + followee
	if v, ok := CacheGet(key); ok {
		return v == "1"
	}
	dbCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var exists bool
	_ = db.QueryRow(dbCtx, "SELECT EXISTS(SELECT 1 FROM followers WHERE follower=$1 AND followee=$2)", follower, followee).Scan(&exists)
	val := "0"
	if exists {
		val = "1"
	}
	CacheSet(key, val, 2*time.Minute)
	return exists
}

func isBlockedEitherWay(a, b string) bool {
	key := BlockCacheKey(a, b)
	if v, ok := CacheGet(key); ok {
		return v == "1"
	}
	dbCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var exists bool
	_ = db.QueryRow(dbCtx, `SELECT EXISTS(
		SELECT 1 FROM blocks WHERE (blocker=$1 AND blocked=$2) OR (blocker=$2 AND blocked=$1)
	)`, a, b).Scan(&exists)
	val := "0"
	if exists {
		val = "1"
	}
	CacheSet(key, val, ttlBlockRel)
	return exists
}

// CanMention reports whether `author` may mention `target`. Block relation
// (either direction) always wins over policy — a blocked/blocking user can
// never be mentioned, regardless of their mention_policy setting.
func CanMention(author, target string) bool {
	if strings.EqualFold(author, target) {
		return false
	}
	if isBlockedEitherWay(author, target) {
		return false
	}
	switch getMentionPolicy(target) {
	case "none":
		return false
	case "followers":
		return isFollowingCached(author, target) || isFollowingCached(target, author)
	default: // "everyone"
		return true
	}
}

// ─────────────────────────────────────────────────────────────────────────
// GET /mention/search?q=ali  — suggestion dropdown while typing @word
// ─────────────────────────────────────────────────────────────────────────

func MentionSearchHandler(c *fiber.Ctx) error {
	author := getCurrentUser(c)
	if author == "" {
		return c.SendStatus(fiber.StatusUnauthorized)
	}
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		return c.SendString("")
	}

	// Over-fetch from Redis, then filter down to ones actually mentionable
	// by this author (policy + block check), capped to 6 for the dropdown.
	candidates := searchUsernamesByPrefix(q, 25)
	var mentionable []string
	for _, uname := range candidates {
		if strings.EqualFold(uname, author) {
			continue
		}
		if CanMention(author, uname) {
			mentionable = append(mentionable, uname)
			if len(mentionable) >= 6 {
				break
			}
		}
	}

	if len(mentionable) == 0 {
		return c.Type("html").SendString(`<div class="mention-empty">No user found with this name</div>`)
	}

	dbCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := db.Query(dbCtx,
		`SELECT username, COALESCE(display_name,''), COALESCE(profile_image,'') FROM users WHERE LOWER(username) = ANY($1)`,
		mentionable)
	type meta struct{ display, img string }
	metaByUser := map[string]meta{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var u, dn, img string
			if rows.Scan(&u, &dn, &img) == nil {
				metaByUser[strings.ToLower(u)] = meta{dn, img}
			}
		}
	}

	var sb strings.Builder
	for _, uname := range mentionable {
		m := metaByUser[uname]
		display := m.display
		if display == "" {
			display = uname
		}
		sb.WriteString(`<div class="mention-suggestion" onmousedown="event.preventDefault();shareApplyMention('` + uname + `')">`)
		if m.img != "" {
			sb.WriteString(`<img src="` + m.img + `" class="mention-avatar">`)
		}
		sb.WriteString(`<span class="mention-uname">@` + uname + `</span>`)
		if display != uname {
			sb.WriteString(`<span class="mention-display">` + display + `</span>`)
		}
		sb.WriteString(`</div>`)
	}
	return c.Type("html").SendString(sb.String())
}

// ─────────────────────────────────────────────────────────────────────────
// EXTRACT + NOTIFY — call right after a post is committed
// ─────────────────────────────────────────────────────────────────────────

// ExtractAndNotifyMentions parses @tokens out of content, keeps only real
// users who are actually mentionable by the poster (skips the poster's
// own username, duplicates, and anyone CanMention rejects — blocked users
// or a target whose mention_policy excludes this poster), and inserts one
// "mention" notification per unique, allowed recipient. Called once, right
// after a post is committed (see postHandler in post.go).
func ExtractAndNotifyMentions(postID int, poster, content string) {
	matches := mentionRegex.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return
	}

	seen := map[string]bool{strings.ToLower(poster): true}
	var targets []string
	for _, m := range matches {
		uname := strings.ToLower(m[1])
		if seen[uname] {
			continue
		}
		seen[uname] = true
		if !CanMention(poster, uname) {
			continue
		}
		targets = append(targets, uname)
	}
	if len(targets) == 0 {
		return
	}

	nctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for _, uname := range targets {
		if _, err := db.Exec(nctx, `
			INSERT INTO notifications (recipient, sender, content, type, post_id, created_at)
			VALUES ($1, $2, $3, 'mention', $4, CURRENT_TIMESTAMP)`,
			uname, poster, "mentioned you in a post", postID); err != nil {
			log.Println("ExtractAndNotifyMentions insert error:", err)
		}
		Push(uname, poster, "mentioned you in a post", postLink(postID))
	}
}

// ExtractAndNotifyCommentMentions is the comment/reply counterpart to
// ExtractAndNotifyMentions — parses @tokens out of a comment or reply's
// content and notifies each unique, allowed recipient. Unlike a post
// mention, this records commentID so the notification can render a reply
// box that threads straight under the comment the mention happened in,
// and stores the actual comment text as content (instead of a generic
// line) so the notification can show what was really said. Call this
// right after a comment or reply is committed.
func ExtractAndNotifyCommentMentions(postID, commentID int, author, content string) {
	matches := mentionRegex.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return
	}

	seen := map[string]bool{strings.ToLower(author): true}
	var targets []string
	for _, m := range matches {
		uname := strings.ToLower(m[1])
		if seen[uname] {
			continue
		}
		seen[uname] = true
		if !CanMention(author, uname) {
			continue
		}
		targets = append(targets, uname)
	}
	if len(targets) == 0 {
		return
	}

	nctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for _, uname := range targets {
		if _, err := db.Exec(nctx, `
			INSERT INTO notifications (recipient, sender, content, type, post_id, comment_id, created_at)
			VALUES ($1, $2, $3, 'comment_mention', $4, $5, CURRENT_TIMESTAMP)`,
			uname, author, content, postID, commentID); err != nil {
			log.Println("ExtractAndNotifyCommentMentions insert error:", err)
		}
		Push(uname, author, "mentioned you: "+content, postLink(postID))
	}
}
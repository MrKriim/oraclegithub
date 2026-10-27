package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/gofiber/fiber/v2"
)

// ─────────────────────────────────────────────────────────────────────────────
// DELETE POST HANDLER
// ─────────────────────────────────────────────────────────────────────────────

func deletePostHandler(c *fiber.Ctx) error {
	postID := c.Params("id")
	// Was reading the raw "user" cookie directly, unlike every other
	// authenticated handler in this codebase (getCurrentUser(c)). If
	// getCurrentUser does anything beyond a bare cookie read — session
	// version checks, ban checks — this could disagree with the rest of
	// the site and return 401 for an otherwise-logged-in user. Because
	// the delete button is wired via hx-delete with hx-swap="outerHTML",
	// htmx only swaps the DOM on a 2xx response — a 401 here means the
	// button click does nothing visible at all, which is exactly what
	// "delete doesn't work" looks like from the user's side.
	currentUser := getCurrentUser(c)

	if currentUser == "" {
		return c.Status(401).SendString("Unauthorized")
	}

	var author string
	if err := db.QueryRow(ctx, "SELECT author FROM posts WHERE id = $1", postID).Scan(&author); err != nil {
		return c.Status(404).SendString("Post not found")
	}

	if author != currentUser {
		var viewerType string
		db.QueryRow(ctx, "SELECT user_type FROM users WHERE username=$1", currentUser).Scan(&viewerType)
		if viewerType != "admin" {
			return c.Status(403).SendString("You can only delete your own posts")
		}
	}

	if _, err := db.Exec(ctx, "DELETE FROM posts WHERE id = $1", postID); err != nil {
		return c.Status(500).SendString("Error deleting post")
	}

	if idInt, convErr := strconv.Atoi(postID); convErr == nil {
		removeTrendingCandidate(idInt)
		CacheDel(postMetaKey(idInt), senderSetKey(idInt))
	}

	return c.SendStatus(200)
}

// ─────────────────────────────────────────────────────────────────────────────
// HELPERS
// ─────────────────────────────────────────────────────────────────────────────

func getUserProfileImage(username string) string {
	var profileImage string
	if err := db.QueryRow(ctx,
		"SELECT COALESCE(profile_image, '') FROM users WHERE username = $1", username,
	).Scan(&profileImage); err != nil || profileImage == "" {
		return `<div class="w-full h-full bg-gradient-to-br from-blue-500 to-emerald-500 flex items-center justify-center text-2xl sm:text-3xl text-white shadow-inner">👤</div>`
	}
	return avatarHTMLFromPath(profileImage, username)
}

// avatarHTMLFromPath renders the avatar markup from an already-known image
// path (e.g. pulled from the Redis-cached authorMeta map) instead of
// hitting Postgres — used by renderPostHTML, which is called once per post
// per feed load and previously issued a fresh query here every time.
func avatarHTMLFromPath(profileImage, username string) string {
	if profileImage == "" {
		return `<div class="w-full h-full bg-gradient-to-br from-blue-500 to-emerald-500 flex items-center justify-center text-2xl sm:text-3xl text-white shadow-inner">👤</div>`
	}
	return fmt.Sprintf(`<img src="%s" class="w-full h-full object-cover rounded-3xl" alt="%s's avatar">`, profileImage, username)
}

func getCommentCount(postID int) int {
	qctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	var count int
	_ = db.QueryRow(qctx, "SELECT COUNT(*) FROM comments WHERE post_id = $1", postID).Scan(&count)
	return count
}

type postRenderState struct {
	viewerIsBlocked    bool
	iHaveBlockedAuthor bool
	isFollowingAuthor  bool
	myReaction         string
	reactionCounts     map[string]int
	disabledReactions  string
	commentCount       int
	isTrending         bool
	repostOf           int // 0 = not a repost
	repostThought      string
	repostCount        int
	repostPolicy       string
	thoughtsPolicy     string
	iHaveReposted      bool
}

func prefetchPostRenderStates(viewer string, postIDs []int, authors []string) map[int]postRenderState {
	out := make(map[int]postRenderState, len(postIDs))
	if len(postIDs) == 0 {
		return out
	}

	qctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rows, err := db.Query(qctx, `
		SELECT p.id,
		       COALESCE(p.is_trending, false),
		       COALESCE(p.disabled_reactions, ''),
		       (SELECT COUNT(*) FROM comments c WHERE c.post_id = p.id),
		       COALESCE(p.repost_of, 0),
		       COALESCE(p.repost_thought, ''),
		       COALESCE(p.repost_count, 0),
		       COALESCE(p.repost_policy, 'public'),
		       COALESCE(p.thoughts_policy, 'public')
		FROM posts p
		WHERE p.id = ANY($1)`, postIDs)
	if err != nil {
		log.Printf("prefetchPostRenderStates counts: %v", err)
		return out
	}
	for rows.Next() {
		var st postRenderState
		var id int
		if rows.Scan(&id, &st.isTrending, &st.disabledReactions, &st.commentCount,
			&st.repostOf, &st.repostThought, &st.repostCount, &st.repostPolicy, &st.thoughtsPolicy) == nil {
			st.reactionCounts = map[string]int{}
			out[id] = st
		}
	}
	rows.Close()

	if cRows, cErr := db.Query(qctx, `
		SELECT post_id, type, COUNT(*) FROM interactions
		WHERE post_id = ANY($1) GROUP BY post_id, type`, postIDs); cErr == nil {
		for cRows.Next() {
			var pid, n int
			var typ string
			if cRows.Scan(&pid, &typ, &n) == nil {
				if st, ok := out[pid]; ok {
					st.reactionCounts[typ] = n
					out[pid] = st
				}
			}
		}
		cRows.Close()
	}

	if viewer == "" {
		return out
	}

	uniqAuthors := make([]string, 0, len(authors))
	seen := make(map[string]struct{}, len(authors))
	for _, a := range authors {
		if a == "" {
			continue
		}
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		uniqAuthors = append(uniqAuthors, a)
	}

	blocked := map[string]bool{}
	if bRows, bErr := db.Query(qctx, `
		SELECT blocker, blocked FROM blocks
		WHERE (blocker = $1 AND blocked = ANY($2))
		   OR (blocked = $1 AND blocker = ANY($2))`, viewer, uniqAuthors); bErr == nil {
		for bRows.Next() {
			var blocker, blockedUser string
			if bRows.Scan(&blocker, &blockedUser) == nil {
				if blocker == viewer {
					blocked["i:"+blockedUser] = true
				} else {
					blocked["they:"+blocker] = true
				}
			}
		}
		bRows.Close()
	}

	following := map[string]bool{}
	if fRows, fErr := db.Query(qctx, `
		SELECT followee FROM followers WHERE follower = $1 AND followee = ANY($2)`,
		viewer, uniqAuthors); fErr == nil {
		for fRows.Next() {
			var followee string
			if fRows.Scan(&followee) == nil {
				following[followee] = true
			}
		}
		fRows.Close()
	}

	myReactions := map[int]string{}
	if rRows, rErr := db.Query(qctx, `
		SELECT post_id, type FROM interactions WHERE sender = $1 AND post_id = ANY($2)`,
		viewer, postIDs); rErr == nil {
		for rRows.Next() {
			var pid int
			var typ string
			if rRows.Scan(&pid, &typ) == nil {
				myReactions[pid] = typ
			}
		}
		rRows.Close()
	}

	iReposted := map[int]bool{}
	if rpRows, rpErr := db.Query(qctx, `
		SELECT repost_of FROM posts WHERE author = $1 AND repost_of = ANY($2)`,
		viewer, postIDs); rpErr == nil {
		for rpRows.Next() {
			var rid int
			if rpRows.Scan(&rid) == nil {
				iReposted[rid] = true
			}
		}
		rpRows.Close()
	}

	for i, author := range authors {
		if i >= len(postIDs) {
			break
		}
		id := postIDs[i]
		st := out[id]
		st.iHaveBlockedAuthor = blocked["i:"+author]
		st.viewerIsBlocked = blocked["they:"+author]
		st.isFollowingAuthor = following[author]
		st.myReaction = myReactions[id]
		st.iHaveReposted = iReposted[id]
		out[id] = st
	}
	return out
}

func fetchPostRenderState(id int, author, currentUser string) postRenderState {
	st := postRenderState{reactionCounts: map[string]int{}}
	qctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if cRows, cErr := db.Query(qctx, `
		SELECT type, COUNT(*) FROM interactions WHERE post_id=$1 GROUP BY type`, id); cErr == nil {
		for cRows.Next() {
			var typ string
			var n int
			if cRows.Scan(&typ, &n) == nil {
				st.reactionCounts[typ] = n
			}
		}
		cRows.Close()
	}

	if currentUser != "" {
		var myReaction *string
		_ = db.QueryRow(qctx, `
			SELECT
				EXISTS(SELECT 1 FROM blocks WHERE blocker=$1 AND blocked=$2),
				EXISTS(SELECT 1 FROM blocks WHERE blocker=$2 AND blocked=$1),
				EXISTS(SELECT 1 FROM followers WHERE follower=$2 AND followee=$1),
				(SELECT type FROM interactions WHERE post_id=$3 AND sender=$2),
				(SELECT COUNT(*) FROM comments WHERE post_id=$3),
				COALESCE((SELECT is_trending FROM posts WHERE id=$3), false),
				COALESCE((SELECT disabled_reactions FROM posts WHERE id=$3), ''),
				COALESCE((SELECT repost_of FROM posts WHERE id=$3), 0),
				COALESCE((SELECT repost_thought FROM posts WHERE id=$3), ''),
				COALESCE((SELECT repost_count FROM posts WHERE id=$3), 0),
				COALESCE((SELECT repost_policy FROM posts WHERE id=$3), 'public'),
				COALESCE((SELECT thoughts_policy FROM posts WHERE id=$3), 'public'),
				EXISTS(SELECT 1 FROM posts WHERE repost_of=$3 AND author=$2)`,
			author, currentUser, id,
		).Scan(&st.viewerIsBlocked, &st.iHaveBlockedAuthor, &st.isFollowingAuthor,
			&myReaction, &st.commentCount, &st.isTrending, &st.disabledReactions,
			&st.repostOf, &st.repostThought, &st.repostCount, &st.repostPolicy, &st.thoughtsPolicy,
			&st.iHaveReposted)
		if myReaction != nil {
			st.myReaction = *myReaction
		}
		return st
	}

	_ = db.QueryRow(qctx, `
		SELECT
			(SELECT COUNT(*) FROM comments WHERE post_id=$1),
			COALESCE((SELECT is_trending FROM posts WHERE id=$1), false),
			COALESCE((SELECT disabled_reactions FROM posts WHERE id=$1), ''),
			COALESCE((SELECT repost_of FROM posts WHERE id=$1), 0),
			COALESCE((SELECT repost_thought FROM posts WHERE id=$1), ''),
			COALESCE((SELECT repost_count FROM posts WHERE id=$1), 0),
			COALESCE((SELECT repost_policy FROM posts WHERE id=$1), 'public'),
			COALESCE((SELECT thoughts_policy FROM posts WHERE id=$1), 'public')`,
		id,
	).Scan(&st.commentCount, &st.isTrending, &st.disabledReactions,
		&st.repostOf, &st.repostThought, &st.repostCount, &st.repostPolicy, &st.thoughtsPolicy)
	return st
}

// formatCompactNumber abbreviates large counts the way every major social
// app does (1.2K, 59.1M) so badges stay a consistent width and don't look
// like a rendering bug when a user has a very high point total.
func formatCompactNumber(n int) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CATEGORY METADATA
// ─────────────────────────────────────────────────────────────────────────────
// Keys here MUST match (case-insensitively) the <option value="..."> values
// used in the post composer dropdown (see homeGet in home.go), so every
// category a user can actually select gets a proper icon + color instead of
// falling back to the generic gray folder icon.

type categoryMeta struct {
	icon string
	grad string
}

var categoryMap = map[string]categoryMeta{
	// ── Default ──
	"GENERAL": {icon: "🌍", grad: "linear-gradient(90deg,#64748b,#3f4c5f)"},

	// ── Top-level / misc ──
	"ASEEL":    {icon: "🐓", grad: "linear-gradient(90deg,#b45309,#78350f)"},
	"MOTOVLOG": {icon: "🏍️", grad: "linear-gradient(90deg,#1f2937,#0f172a)"},
	"GAMING":   {icon: "🎮", grad: "linear-gradient(90deg,#7c3aed,#4c1d95)"},

	// ── Poetry & Literature (deep jewel tones — romantic/literary feel) ──
	"SAD POETRY":      {icon: "🥺", grad: "linear-gradient(90deg,#475569,#1e293b)"},
	"LOVE POETRY":     {icon: "❤️", grad: "linear-gradient(90deg,#be123c,#7f1d1d)"},
	"URDU GHAZAL":     {icon: "📜", grad: "linear-gradient(90deg,#9333ea,#581c87)"},
	"SUFI POETRY":     {icon: "🕌", grad: "linear-gradient(90deg,#0e7490,#134e4a)"},
	"DEEP LINES":      {icon: "🖋️", grad: "linear-gradient(90deg,#4f46e5,#312e81)"},
	"ATTITUDE POETRY": {icon: "😎", grad: "linear-gradient(90deg,#18181b,#000000)"},
	"FUNNY POETRY":    {icon: "😂", grad: "linear-gradient(90deg,#d97706,#92400e)"},
	"PUNJABI POETRY":  {icon: "🚜", grad: "linear-gradient(90deg,#4d7c0f,#1a2e05)"},
	"ISLAMIC POETRY":  {icon: "🌙", grad: "linear-gradient(90deg,#0f766e,#022c22)"},
	"LITERATURE":      {icon: "📖", grad: "linear-gradient(90deg,#78350f,#292524)"},

	// ── Lifestyle (fresh, bright but readable) ──
	"PHOTOGRAPHY": {icon: "📸", grad: "linear-gradient(90deg,#0284c7,#0c4a6e)"},
	"TRAVEL":      {icon: "✈️", grad: "linear-gradient(90deg,#0891b2,#155e75)"},
	"FOOD":        {icon: "🍱", grad: "linear-gradient(90deg,#ea580c,#9a3412)"},
	"FASHION":     {icon: "✨", grad: "linear-gradient(90deg,#db2777,#831843)"},
	"FITNESS":     {icon: "💪", grad: "linear-gradient(90deg,#dc2626,#7f1d1d)"},
	"VLOG":        {icon: "🎥", grad: "linear-gradient(90deg,#7c3aed,#4c1d95)"},
	"PETS":        {icon: "🐾", grad: "linear-gradient(90deg,#a16207,#713f12)"},
	"GARDENING":   {icon: "🌻", grad: "linear-gradient(90deg,#4d7c0f,#14532d)"},

	// ── Knowledge & Work (cool, professional, techy) ──
	"TECH":      {icon: "💻", grad: "linear-gradient(90deg,#059669,#065f46)"},
	"AI":        {icon: "🤖", grad: "linear-gradient(90deg,#4f46e5,#1e1b4b)"},
	"EDUCATION": {icon: "📚", grad: "linear-gradient(90deg,#1d4ed8,#1e3a8a)"},
	"BUSINESS":  {icon: "💼", grad: "linear-gradient(90deg,#0f766e,#134e4a)"},
	"CRYPTO":    {icon: "💰", grad: "linear-gradient(90deg,#ca8a04,#713f12)"},
	"SCIENCE":   {icon: "🧪", grad: "linear-gradient(90deg,#0d9488,#134e4a)"},

	// ── Entertainment (vivid, high-energy) ──
	"MEMES":  {icon: "😂", grad: "linear-gradient(90deg,#d97706,#92400e)"},
	"MOVIES": {icon: "🎬", grad: "linear-gradient(90deg,#b91c1c,#450a0a)"},
	"MUSIC":  {icon: "🎵", grad: "linear-gradient(90deg,#7c3aed,#4c1d95)"},
	"ART":    {icon: "🎨", grad: "linear-gradient(90deg,#c026d3,#701a75)"},
	"DRAMA":  {icon: "📺", grad: "linear-gradient(90deg,#e11d48,#881337)"},
	"ANIME":  {icon: "⛩️", grad: "linear-gradient(90deg,#be123c,#4c0519)"},

	// ── Community (neutral/serious, each visually distinct) ──
	"NEWS":       {icon: "📰", grad: "linear-gradient(90deg,#2563eb,#1e3a8a)"},
	"SPORTS":     {icon: "🏏", grad: "linear-gradient(90deg,#ea580c,#7c2d12)"},
	"AUTOMOTIVE": {icon: "🚗", grad: "linear-gradient(90deg,#334155,#0f172a)"},
	"POLITICS":   {icon: "⚖️", grad: "linear-gradient(90deg,#57534e,#292524)"},
	"HEALTH":     {icon: "🏥", grad: "linear-gradient(90deg,#dc2626,#7f1d1d)"},
	"RELIGION":   {icon: "🕋", grad: "linear-gradient(90deg,#047857,#022c22)"},
}

func getCategoryMeta(category string) (icon, grad string) {
	key := strings.ToUpper(strings.TrimSpace(category))
	if key == "" {
		key = "GENERAL"
	}
	if m, ok := categoryMap[key]; ok {
		return m.icon, m.grad
	}
	return "📁", "linear-gradient(90deg,#64748b,#334155)"
}

// ─────────────────────────────────────────────────────────────────────────────
// RANK / ADMIN STYLES
// ─────────────────────────────────────────────────────────────────────────────

type rankStyle struct {
	container  string
	avatar     string
	text       string
	nameShadow string
	adminExtra string
	frameClass string
	topBadge   string
	bottomBar  string
}

func getRankStyle(userType string, rank int64) rankStyle {
	base := rankStyle{
		container: "background-color: var(--post-bg); border: 1px solid var(--post-border);",
		avatar:    "border: 2px solid var(--post-border);",
		text:      "color: var(--post-text);",
	}

	if userType == "admin" {
		// Premium, glow-free admin treatment: a crisp animated two-tone
		// gradient border (drawn via the double-background-layer trick,
		// not a blurred box-shadow), a gradient-text username, and a
		// layered offset-ring avatar frame. No filter:blur, no diffuse
		// box-shadow "glow" anywhere in this block.
		return rankStyle{
			container:  "border: 3px solid transparent !important; background-image: linear-gradient(var(--post-bg), var(--post-bg)), linear-gradient(120deg, #4c1d95, #7c3aed, #c084fc, #f0abfc, #c084fc, #7c3aed, #4c1d95); background-origin: border-box; background-clip: padding-box, border-box; background-size: 100% 100%, 260% 100%; animation: adminBorderFlow 7s linear infinite;",
			avatar:     "border: 3px solid #7c3aed !important; box-shadow: 0 0 0 2px var(--post-bg), 0 0 0 4px #c084fc;",
			text:       "background-image: linear-gradient(90deg,#6d28d9,#a855f7,#6d28d9); -webkit-background-clip: text; background-clip: text; color: transparent; font-weight: 900; letter-spacing: -0.02em;",
			nameShadow: "",
			frameClass: "rank-frame rank-frame-admin",
		}
	}

	if t := GetRankTheme(int(rank)); t.Hex != "" {
		borderWidth := "4px"
		ringExtra := ""
		if rank >= 1 && rank <= 3 {
			// Top-3 get a thicker border plus a soft colored ring/lift
			// shadow for extra presence.
			borderWidth = "5px"
			ringExtra = ", 0 0 0 1px " + t.Hex + "55, 0 8px 20px -6px " + t.Hex + "80"
		}
		// !important here is required: .post-fire-border sets
		// `border:3px solid transparent !important`, which otherwise
		// silently wins over a plain inline border and makes the
		// rank frame border disappear whenever the same author is also
		// a currently-trending creator. An inline !important still
		// beats a stylesheet !important, so this restores the border.
		base.container = "border: " + borderWidth + " solid " + t.Hex + " !important; background: linear-gradient(135deg, " + t.Hex + "1a 0%, var(--post-bg) 100%); box-shadow: inset 0 0 0 1px " + t.Hex + "40" + ringExtra + " !important;"
		base.avatar = "border: 4px solid " + t.Hex + " !important; box-shadow: 0 0 0 2px var(--post-bg), 0 0 0 4px " + t.Hex + "66;"
		base.text = "color: " + t.TextHex + ";"
		base.frameClass = t.FrameClass()

		// Top-3 scorers get an extra corner badge + bottom accent strip,
		// built entirely from this rank's own t.Hex — same color already
		// assigned, just more visual presence for #1/#2/#3.
		if rank >= 1 && rank <= 3 {
			medal := map[int64]string{1: "🥇", 2: "🥈", 3: "🥉"}[rank]
			// Inline pill (no absolute positioning) sized to sit neatly
			// inside the category-name box instead of floating over the
			// whole card and colliding with the category bar.
			base.topBadge = fmt.Sprintf(`<span style="display:inline-flex;align-items:center;gap:2px;padding:0 5px;margin-left:4px;border-radius:999px;background:%s;box-shadow:0 1px 4px rgba(0,0,0,.35), inset 0 1px 0 rgba(255,255,255,.4);border:1px solid rgba(255,255,255,.55);vertical-align:middle;line-height:1.4;">
  <span style="font-size:10px;line-height:1;filter:drop-shadow(0 1px 1px rgba(0,0,0,.4));">%s</span>
  <span style="font-size:8px;font-weight:900;color:#ffffff;text-shadow:0 1px 2px rgba(0,0,0,.75);letter-spacing:.03em;text-transform:uppercase;">#%d</span>
</span>`, t.Hex, medal, rank)

			// Bottom accent strip is now tiered by rank (#1 most premium
			// down to #3 least) instead of every top-3 post getting the
			// identical "❖ ❖ ❖" decoration with only the color changed.
			// Each tier also carries a plain-language "TOP SCORER #N"
			// label — the old version was pure decoration with no text
			// explaining what it signified, which left new users with
			// no idea why a post looked different.
			switch rank {
			case 1:
				// Most premium: thickest glowing strip, shimmering
				// two-tone gradient, biggest/boldest label, crown icons.
				base.bottomBar = fmt.Sprintf(`<div style="height:7px;width:100%%;background:linear-gradient(90deg, transparent, %s, #fff7cd, %s, transparent);opacity:1;box-shadow:0 0 10px 1px %s;"></div>
<div style="text-align:center;padding:5px 0 4px;background:linear-gradient(180deg, %s26 0%%, transparent 100%%);">
  <span style="font-size:11px;font-weight:900;letter-spacing:.06em;color:%s;text-shadow:0 1px 3px rgba(0,0,0,.25);">👑 TOP SCORER #1 👑</span>
</div>`, t.Hex, t.Hex, t.Hex, t.Hex, t.Hex)
			case 2:
				// Less premium than #1: thinner strip, softer glow,
				// smaller label, single medal icon.
				base.bottomBar = fmt.Sprintf(`<div style="height:5px;width:100%%;background:linear-gradient(90deg, transparent, %s, transparent);opacity:.9;box-shadow:0 0 6px 1px %s;"></div>
<div style="text-align:center;padding:4px 0 2px;background:linear-gradient(180deg, %s1a 0%%, transparent 100%%);">
  <span style="font-size:10px;font-weight:800;letter-spacing:.04em;color:%s;opacity:.9;">🥈 TOP SCORER #2</span>
</div>`, t.Hex, t.Hex, t.Hex, t.Hex)
			default:
				// Lesser-premium than #1/#2: thinnest strip, no glow,
				// smallest/dimmest label — rank 3.
				base.bottomBar = fmt.Sprintf(`<div style="height:3px;width:100%%;background:linear-gradient(90deg, transparent, %s, transparent);opacity:.75;"></div>
<div style="text-align:center;padding:3px 0 1px;background:linear-gradient(180deg, %s0d 0%%, transparent 100%%);">
  <span style="font-size:9px;font-weight:700;letter-spacing:.03em;color:%s;opacity:.75;">🥉 TOP SCORER #3</span>
</div>`, t.Hex, t.Hex, t.Hex)
			}
		}
	}

	return base
}

// ─────────────────────────────────────────────────────────────────────────────
// BUTTON STYLE CONSTANTS
// ─────────────────────────────────────────────────────────────────────────────
// Stacked layout (icon on top line, label below) — each line is short enough
// to fit inside a narrow equal-width box without clipping, even at 5-across.

const (
	reactBtnStyle = "action-btn btn-react"

	replyStyle    = "action-btn btn-reply"
	followStyle   = "follow-btn"
	unfollowStyle = "unfollow-btn"
	shareStyle    = "action-btn btn-share"
	blockStyle    = "action-btn btn-block"
	deleteStyle   = "action-btn btn-delete"
	repostStyle   = "action-btn btn-repost"
)

// ─────────────────────────────────────────────────────────────────────────────
// REACTIONS
// ─────────────────────────────────────────────────────────────────────────────

type reactionDef struct {
	Key   string // stored verbatim in interactions.type
	Gif   string // swap these paths for your real GIF URLs
	Label string
	Verb  string // activity-feed phrase
}

var reactionList = []reactionDef{
	{"like", "/static/reactions/like.gif", "Like", "ne like kiya"},
	{"dislike", "/static/reactions/dislike.gif", "Dislike", "ne dislike kiya"},
	{"taliyan", "/static/reactions/taliyan.gif", "Taliyaan", "ne zordaar taliyaan bajayi"},
	{"chupair", "/static/reactions/chupair.gif", "Chupair", "ne bhare bazar me chupair mari"},
	{"funny", "/static/reactions/funny.gif", "Funny", "ko apki post se hasi ka dora par gya"},
	{"sad", "/static/reactions/sad.gif", "Sad", "is post ko dekh ke bohot udas hua"},
	{"angry", "/static/reactions/angry.gif", "Angry", "is post ko dekh ke ghuse se lal peela hogahya"},
	{"disgusted", "/static/reactions/disgusted.gif", "Disgusted", "ko is post se ghinn aayi"},
	{"sleepy", "/static/reactions/sleepy.gif", "Sleepy", "ko ye post dekh ke neend agai"},
	{"love", "/static/reactions/love.gif", "Love", "ne apki post ko piyar kia"},
	{"wow", "/static/reactions/wow.gif", "Wow", "ne apki post pe wow keh dia"},
	{"follow", "/static/reactions/follow.gif", "Follow", "ne follow ka ishara kardia"},
}

func getReactionDef(key string) reactionDef {
	// normalize: rows inserted before a key rename/case change (or any
	// stray whitespace) were silently falling through to the generic
	// "ne react kiya" fallback with no gif/label — this is the exact
	// text reported ("kriim ne react kia").
	key = strings.ToLower(strings.TrimSpace(key))
	for _, r := range reactionList {
		if r.Key == key {
			return r
		}
	}
	return reactionDef{Key: key, Verb: "ne react kiya"}
}

var negativeReactionTypes = map[string]bool{"dislike": true, "disgusted": true, "angry": true, "chupair": true}

func isNegativeReaction(typ string) bool { return negativeReactionTypes[typ] }

func isReactionDisabled(disabled, key string) bool {
	for _, d := range strings.Split(disabled, ",") {
		if strings.TrimSpace(d) == key {
			return true
		}
	}
	return false
}

// actionButtonCSS is the single source of truth for how every post/comment/
// DP-competition action button looks (Reply, Follow, Share, Block, Delete,
// Taliyaan, Chupair, Rate, Give Up). It used to live only in home.go's page
// <style> block, which meant any page that renders a post WITHOUT going
// through homeGet — the single-post view (/post/:id) and profile pages —
// never received the CSS at all, so those buttons rendered with the right
// class names but no matching styles. Embedding it here, self-contained,
// and prepending it to every renderPostHTML output guarantees identical
// button styling on every page, with no dependency on which page loaded it
// first. The id lets a browser safely ignore exact duplicate <style> tags
// injected by multiple posts on the same page — harmless repetition, not a
// bug — while guaranteeing it's present the first time it's needed.
const actionButtonCSS = `<style id="action-btn-styles">
.action-btn{display:inline-flex!important;flex-direction:column!important;flex-shrink:0!important;flex-grow:0!important;align-items:center;justify-content:center;gap:1px;width:auto;min-height:34px!important;min-width:0;border:1px solid;border-top-color:currentcolor;border-right-color:currentcolor;border-bottom-color:currentcolor;border-left-color:currentcolor;border-radius:7px;background:#fff;font-size:7.5px;font-weight:700;cursor:pointer;transition:.15s;padding:4px 6px!important;text-decoration:none;line-height:1.1;white-space:nowrap;box-sizing:border-box!important;}
.action-btn{transition:all .1s ease!important;cursor:pointer!important;}
.action-btn span:first-child{font-size:10px;line-height:1;}
.btn-react{color:#2563eb;position:relative;}
.btn-reply{color:#2563eb;}
.btn-edit{color:#0891b2;}
.btn-share{color:#475569;}
.btn-block{color:#d97706;}
.btn-delete{color:#dc2626;}
.btn-repost{color:#059669;}
.btn-repost.reposted{color:#047857;background:#ecfdf5;border-color:#059669;}
.follow-btn,.unfollow-btn{display:inline-flex!important;flex-direction:column!important;flex-shrink:0!important;flex-grow:0!important;align-items:center;justify-content:center;gap:1px;min-height:34px!important;border:1px solid;border-radius:7px;font-size:7.5px;font-weight:700;cursor:pointer;transition:.15s;padding:4px 6px!important;text-decoration:none;line-height:1.1;white-space:nowrap;box-sizing:border-box!important;background:#fff;}
.follow-btn{border-color:#c4b5fd;color:#7c3aed;}
.unfollow-btn{border-color:#c4b5fd;color:#7c3aed;}
.follow-btn span:first-child,.unfollow-btn span:first-child{font-size:10px;line-height:1;}
.follow-btn:active,.unfollow-btn:active{transform:scale(.92);}
.action-btn:active{transform:scale(.92);}
.dark .action-btn,.dark .follow-btn,.dark .unfollow-btn{background:#1f2937;border-color:currentcolor;}
.dark .btn-react{color:#60a5fa;}
.dark .btn-reply{color:#60a5fa;}
.dark .btn-edit{color:#22d3ee;}
.dark .btn-share{color:#94a3b8;}
.dark .btn-block{color:#fbbf24;}
.dark .btn-delete{color:#f87171;}
.dark .btn-repost{color:#34d399;}
.dark .btn-repost.reposted{color:#6ee7b7;background:#052e16;border-color:#34d399;}
.dark .follow-btn,.dark .unfollow-btn{border-color:#a78bfa;color:#c4b5fd;}
.post-actions-row{-webkit-overflow-scrolling:touch;scroll-behavior:smooth;scroll-snap-type:x proximity;-webkit-mask-image:linear-gradient(to right,#000 90%,transparent 100%);mask-image:linear-gradient(to right,#000 90%,transparent 100%);}
.post-actions-row>*{scroll-snap-align:start;}
</style>`

// reactionPickerCSS drives the Facebook-style hover/hold-to-reveal 9-GIF
// reaction popup attached to the React button. Self-contained like
// actionButtonCSS so it works wherever renderPostHTML is used.
const reactionPickerCSS = `<style id="reaction-picker-styles">
.reaction-wrap{position:relative;display:inline-flex;}
.reaction-picker{
  position:fixed;left:0;top:0;transform:translate(-50%,-100%) scale(.92);
  transform-origin:bottom center;
  display:flex;gap:3px;padding:5px 6px;border-radius:999px;background:#fff;
  box-shadow:0 6px 20px rgba(0,0,0,.18),0 0 0 1px rgba(0,0,0,.06);
  opacity:0;pointer-events:none;visibility:hidden;transition:opacity .12s ease,transform .12s ease;
  z-index:100000;white-space:nowrap;
}
.reaction-picker.open{opacity:1;pointer-events:auto;visibility:visible;transform:translate(-50%,-100%) scale(1);}
.reaction-picker.open.flip-down{transform:translate(-50%,8px) scale(1);}
.dark .reaction-picker{background:#1f2937;box-shadow:0 6px 20px rgba(0,0,0,.45),0 0 0 1px rgba(255,255,255,.08);}
.reaction-opt{width:38px;height:38px;border-radius:50%;border:none;background:transparent;cursor:pointer;display:inline-flex;align-items:center;justify-content:center;transition:transform .12s ease;flex-shrink:0;padding:0;-webkit-tap-highlight-color:transparent;touch-action:manipulation;}
.reaction-opt img{width:34px;height:34px;object-fit:contain;pointer-events:none;}
.reaction-opt:hover{transform:translateY(-6px) scale(1.25);}
.reaction-opt:active{transform:scale(1.3);}
@media (max-width:480px){
  .reaction-picker{gap:3px;padding:5px 6px;max-width:92vw;overflow-x:auto;-webkit-overflow-scrolling:touch;}
  .reaction-opt{width:34px;height:34px;flex:0 0 auto;}
  .reaction-opt img{width:30px;height:30px;}
}
.reaction-mine img{outline:2px solid #2563eb;border-radius:50%;}
</style>`

// repostPickerCSS drives the small "Repost" popup (plain repost vs
// repost-with-a-thought) attached to the Repost button. Self-contained
// like reactionPickerCSS so it renders correctly wherever renderPostHTML
// is used.
const repostPickerCSS = `<style id="repost-picker-styles">
.repost-wrap{position:relative;display:inline-flex;}
.repost-picker{
  position:fixed;left:0;top:0;transform:translate(-50%,-100%) scale(.94);
  transform-origin:bottom center;
  width:230px;max-width:88vw;padding:10px;border-radius:14px;background:#fff;
  box-shadow:0 6px 20px rgba(0,0,0,.18),0 0 0 1px rgba(0,0,0,.06);
  opacity:0;pointer-events:none;visibility:hidden;transition:opacity .12s ease,transform .12s ease;
  z-index:100000;
}
.repost-picker.open{opacity:1;pointer-events:auto;visibility:visible;transform:translate(-50%,-100%) scale(1);}
.repost-picker.open.flip-down{transform:translate(-50%,8px) scale(1);}
.dark .repost-picker{background:#1f2937;box-shadow:0 6px 20px rgba(0,0,0,.45),0 0 0 1px rgba(255,255,255,.08);}
.repost-picker-btn{display:flex;align-items:center;gap:7px;width:100%;padding:8px 9px;margin-bottom:5px;border:1px solid #d1fae5;border-radius:9px;background:#ecfdf5;color:#059669;font-size:11px;font-weight:800;cursor:pointer;text-align:left;}
.repost-picker-btn:last-child{margin-bottom:0;}
.dark .repost-picker-btn{background:#052e16;border-color:#065f46;color:#6ee7b7;}
.repost-thought-box{display:none;flex-direction:column;gap:6px;margin-top:6px;}
.repost-thought-box.open{display:flex;}
.repost-thought-box textarea{width:100%;min-height:52px;max-height:100px;resize:none;border:1px solid #d1d5db;border-radius:8px;padding:6px 8px;font-size:11.5px;box-sizing:border-box;}
.dark .repost-thought-box textarea{background:#111827;border-color:#374151;color:#f3f4f6;}
.repost-thought-send{align-self:flex-end;padding:5px 12px;border:none;border-radius:999px;background:#059669;color:#fff;font-size:10.5px;font-weight:800;cursor:pointer;}
</style>`

// rankFrameCSS drives the animated glow/shimmer on top-10 (and admin) post
// cards. Pure CSS keyframes + !important so it can't be overridden by page
// styles and survives every htmx outerHTML swap, exactly like actionButtonCSS.
const rankFrameCSS = `<style id="rank-frame-styles">
@keyframes rankFloatIn{from{opacity:0;transform:translateY(6px) scale(.99);}to{opacity:1;transform:translateY(0) scale(1);}}
@keyframes adminBorderFlow{0%{background-position:0 0,0% 50%;}100%{background-position:0 0,200% 50%;}}
.rank-frame{animation:rankFloatIn .5s ease-out !important;position:relative;}
.rank-frame-admin{animation:adminBorderFlow 7s linear infinite,rankFloatIn .5s ease-out !important;position:relative;overflow:hidden;}
.rank-frame-admin::before{content:"";position:absolute;inset:0;z-index:0;pointer-events:none;border-radius:inherit;background-image:repeating-linear-gradient(135deg,rgba(124,58,237,.05) 0px,rgba(124,58,237,.05) 2px,transparent 2px,transparent 12px);}
.rank-frame-admin::after{content:"👑 ADMIN";position:absolute;top:10px;right:-32px;z-index:5;pointer-events:none;transform:rotate(40deg);width:150px;text-align:center;padding:3px 0;font-size:9px;font-weight:900;letter-spacing:.12em;color:#fff;background:linear-gradient(90deg,#6d28d9,#a855f7);box-shadow:0 2px 5px rgba(76,29,149,.35);}
.dark .rank-frame-admin::before{background-image:repeating-linear-gradient(135deg,rgba(196,181,253,.07) 0px,rgba(196,181,253,.07) 2px,transparent 2px,transparent 12px);}
</style>`

// creatureCSS defines the looping heartbeat pulse used by every draggable
// emoji (rank creature or admin crown) while idle. Kept separate from
// rankFrameCSS since this applies to the small emoji, not the post border.
const creatureCSS = `<style id="creature-styles">
@keyframes creatureHeartbeat{0%,100%{transform:scale(1);}15%{transform:scale(1.28);}30%{transform:scale(1);}45%{transform:scale(1.22);}60%{transform:scale(1);}}
.creature-heartbeat{animation:creatureHeartbeat 1.1s ease-in-out infinite;display:inline-block;}
</style>`

// fireBorderCSS drives the animated "on fire" border shown around an
// entire post card when its author is a top-10 leaderboard scorer or is
// currently trending. Self-contained (like rankFrameCSS/creatureCSS) so
// it renders correctly wherever renderPostHTML is used — feed, single
// post view, profile pages — with no dependency on home.go's page-level
// <style> block. Plain CSS, no Tailwind utility classes.
const fireBorderCSS = `<style id="post-fire-border-styles">
@keyframes postTopStarDrift{0%{background-position:0% 50%;}100%{background-position:200% 50%;}}
.post-fire-border{
  border:3px solid transparent !important;
  background-image:linear-gradient(var(--post-bg),var(--post-bg)),linear-gradient(115deg,#78350f,#d97706,#fde68a,#fff7cd,#fde68a,#d97706,#78350f);
  background-origin:border-box;
  background-clip:padding-box, border-box;
  background-size:100% 100%, 260% 100%;
  animation:postTopStarDrift 8s linear infinite;
  box-shadow:0 0 14px 1px rgba(217,119,6,.32);
}
.dark .post-fire-border{
  box-shadow:0 0 16px 1px rgba(251,191,36,.4);
}
</style>`

// repostedEmbedCSS styles the read-only "quoted" original post shown
// inside a repost card (see buildRepostedOriginalCard): it hides the
// embedded post's own action-button row and reaction/activity section
// so a repost never shows two stacked sets of the same buttons.
const repostedEmbedCSS = `<style id="reposted-embed-styles">
.reposted-embed .post-card{box-shadow:none!important;border-color:rgba(5,150,105,0.18)!important;}
.reposted-embed .post-actions-row{display:none!important;}
.reposted-embed .pt-2.border-t{display:none!important;}
.dark .reposted-embed{background:rgba(16,185,129,0.06)!important;border-left-color:#065f46!important;}
</style>`

// buildCreatureWidget renders the small rank-creature emoji next to a
// username as a draggable toy: the user can pick it up and drop it
// anywhere on screen, and it smoothly springs back to its original spot
// on release. While idle (not being dragged) it periodically performs a
// random trick animation (spin/flip/hop) in place. Self-contained inline
// CSS + JS so it works wherever renderPostHTML is used, with no external
// dependency on page-level scripts.
// buildDraggableEmoji renders a small emoji (rank creature or admin crown)
// next to a username as a draggable toy: the user can pick it up and drop
// it anywhere on the page. While dragging, it's reparented to
// document.body so no ancestor's overflow:hidden or filter/transform
// (which creates a new containing block for position:fixed elements) can
// trap it inside the post card — that was why it previously couldn't
// leave the card. On release it animates back to its exact original spot
// and is reinserted there in the DOM. While idle it loops a heartbeat
// pulse in place. Self-contained inline CSS + JS, no external dependency.
// slot distinguishes the DOM id (e.g. "creature" vs "crown") so a post
// can safely render more than one.
func buildDraggableEmoji(id int, slot, emoji, cssClass, title string) string {
	if emoji == "" {
		return ""
	}
	return fmt.Sprintf(`
<span id="%[4]s-%[1]d" class="%[3]s creature-heartbeat" title="%[5]s — drag me!"
      style="display:inline-block;cursor:grab;user-select:none;-webkit-user-select:none;touch-action:none;position:relative;">%[2]s</span>
<script>
(function(){
  var id = %[1]d;
  var el = document.getElementById('%[4]s-'+id);
  if (!el || el._creatureReady) return;
  el._creatureReady = true;

  var dragging = false, startX = 0, startY = 0, baseX = 0, baseY = 0;
  var placeholder = document.createElement('span');
  placeholder.style.display = 'none';

  function setPos(x, y){
    el.style.left = x + 'px';
    el.style.top = y + 'px';
  }

  function down(e){
    e.preventDefault();
    var pt = e.touches ? e.touches[0] : e;
    var rect = el.getBoundingClientRect();

    // Mark exactly where el lives so we can put it back later, then move
    // it to document.body so no ancestor can clip or contain it.
    el.parentNode.insertBefore(placeholder, el);
    document.body.appendChild(el);

    el.classList.remove('creature-heartbeat');
    el.style.position = 'fixed';
    el.style.margin = '0';
    el.style.zIndex = 999999;
    el.style.fontSize = '26px';
    el.style.pointerEvents = 'auto';
    el.style.transition = '';
    baseX = rect.left; baseY = rect.top;
    startX = pt.clientX; startY = pt.clientY;
    dragging = true;
    el.style.cursor = 'grabbing';
    setPos(baseX, baseY);
  }

  function move(e){
    if (!dragging) return;
    e.preventDefault();
    var pt = e.touches ? e.touches[0] : e;
    var dx = pt.clientX - startX, dy = pt.clientY - startY;
    setPos(baseX + dx, baseY + dy);
  }

  function up(){
    if (!dragging) return;
    dragging = false;
    el.style.cursor = 'grab';

    var homeRect = placeholder.getBoundingClientRect();
    el.style.transition = 'left .45s cubic-bezier(.34,1.56,.64,1), top .45s cubic-bezier(.34,1.56,.64,1), font-size .3s ease';
    setPos(homeRect.left, homeRect.top);
    el.style.fontSize = '';

    setTimeout(function(){
      if (placeholder.parentNode) {
        placeholder.parentNode.insertBefore(el, placeholder);
        placeholder.parentNode.removeChild(placeholder);
      }
      el.style.position = '';
      el.style.left = '';
      el.style.top = '';
      el.style.margin = '';
      el.style.zIndex = '';
      el.style.transition = '';
      el.classList.add('creature-heartbeat');
    }, 460);
  }

  el.addEventListener('mousedown', down);
  el.addEventListener('touchstart', down, {passive:false});
  document.addEventListener('mousemove', move, {passive:false});
  document.addEventListener('touchmove', move, {passive:false});
  document.addEventListener('mouseup', up);
  document.addEventListener('touchend', up);
})();
</script>`, id, emoji, cssClass, slot, title)
}

// buildCreatureWidget wraps buildDraggableEmoji for the small rank-creature
// emoji shown next to a username.
func buildCreatureWidget(id int, creature, animClass string) string {
	return buildDraggableEmoji(id, "creature", creature, "text-sm "+animClass, "Rank Member")
}

// buildAdminCrownWidget wraps buildDraggableEmoji for the admin crown.
func buildAdminCrownWidget(id int) string {
	return buildDraggableEmoji(id, "crown", "👑", "text-xl animate-pulse", "Admin")
}

// ─────────────────────────────────────────────────────────────────────────────
// IMAGE BLOCK
// ─────────────────────────────────────────────────────────────────────────────

// buildExpiryBadge shows a live countdown until the post is deleted, when
// the post has an expires_at set. Client-side JS ticks it down every
// second; server only needs to emit the target timestamp once.
func buildExpiryBadge(id int, expiresAt *time.Time) string {
	if expiresAt == nil {
		return ""
	}
	return fmt.Sprintf(`
<span id="expiry-badge-%d" data-expires="%s" style="display:inline-flex;align-items:center;gap:3px;padding:1px 7px;border-radius:999px;background:rgba(0,0,0,0.65);color:#fbbf24;font-size:9px;font-weight:900;letter-spacing:.04em;text-transform:uppercase;vertical-align:middle;margin-left:4px;white-space:nowrap;">⏳ <span class="expiry-time">--:--</span></span>
<script>
(function(){
  var el = document.getElementById('expiry-badge-%d');
  if (!el || el._expiryReady) return;
  el._expiryReady = true;
  var target = new Date(el.dataset.expires).getTime();
  var timeEl = el.querySelector('.expiry-time');
  function tick(){
    var diff = target - Date.now();
    if (diff <= 0) { timeEl.textContent = 'Expired'; return; }
    var d = Math.floor(diff / 86400000);
    var h = Math.floor((diff %% 86400000) / 3600000);
    var m = Math.floor((diff %% 3600000) / 60000);
    var s = Math.floor((diff %% 60000) / 1000);
    if (d > 0) timeEl.textContent = d + 'd ' + h + 'h left';
    else if (h > 0) timeEl.textContent = h + 'h ' + m + 'm left';
    else timeEl.textContent = m + 'm ' + s + 's left';
    setTimeout(tick, 1000);
  }
  tick();
})();
</script>`, id, expiresAt.Format(time.RFC3339), id)
}

func buildImageBlock(imageUrl string) string {
	if imageUrl == "" {
		return ""
	}
	lower := strings.ToLower(imageUrl)
	isVideo := strings.HasSuffix(lower, ".mp4") || strings.HasSuffix(lower, ".webm") ||
		strings.HasSuffix(lower, ".mov") || strings.HasSuffix(lower, ".mkv")
	if isVideo {
		return fmt.Sprintf(`
<div style="margin-top:8px;border-radius:14px;overflow:hidden;border:1px solid rgba(0,0,0,0.08);background:#000;text-align:center;">
  <video src="%s" controls playsinline preload="metadata"
         style="max-width:100%%;max-height:480px;width:auto;height:auto;display:block;margin:0 auto;border-radius:14px;">
    Your browser does not support the video tag.
  </video>
</div>`, imageUrl)
	}
	return fmt.Sprintf(`
<div style="margin-top:8px;border-radius:14px;overflow:hidden;border:1px solid rgba(0,0,0,0.08);background:#f3f4f6;text-align:center;">
  <img src="%s" loading="lazy"
       style="max-width:100%%;max-height:480px;width:auto;height:auto;object-fit:contain;display:block;margin:0 auto;border-radius:14px;"
       alt="Post image">
</div>`, imageUrl)
}

// ─────────────────────────────────────────────────────────────────────────────
// VOICE BLOCK
// ─────────────────────────────────────────────────────────────────────────────

func buildVoiceBlock(id int, voiceUrl string) string {
	if voiceUrl == "" {
		return ""
	}
	return fmt.Sprintf(`
<div style="display:flex;align-items:center;gap:10px;padding:10px 14px;margin-top:10px;
            border-radius:14px;background:linear-gradient(135deg,#ecfdf5,#f0fdf4);
            border:1.5px solid #bbf7d0;box-shadow:0 2px 8px rgba(16,185,129,0.1);">

  <button id="vp-btn-%[1]d" type="button"
          onclick="window['vpPlay_%[1]d']&&window['vpPlay_%[1]d']()"
          style="flex-shrink:0;width:38px;height:38px;border-radius:50%%;border:none;cursor:pointer;
                 background:linear-gradient(135deg,#10b981,#059669);color:white;font-size:15px;
                 display:flex;align-items:center;justify-content:center;
                 box-shadow:0 4px 12px rgba(16,185,129,0.4);transition:transform .15s;
                 -webkit-tap-highlight-color:transparent;">▶</button>

  <div style="flex:1;min-width:0;display:flex;flex-direction:column;gap:5px;">
    <div style="display:flex;justify-content:space-between;align-items:center;">
      <span style="font-size:10px;font-weight:900;color:#065f46;letter-spacing:.04em;text-transform:uppercase;">🎙️ Voice</span>
      <span id="vp-time-%[1]d"
            style="font-size:10px;font-weight:800;color:#059669;font-variant-numeric:tabular-nums;">0:00</span>
    </div>
    <div id="vp-bar-%[1]d"
         onclick="window['vpSeek_%[1]d']&&window['vpSeek_%[1]d'](event)"
         style="position:relative;height:6px;border-radius:999px;background:#d1fae5;cursor:pointer;overflow:hidden;">
      <div id="vp-prog-%[1]d"
           style="height:100%%;width:0%%;border-radius:999px;
                  background:linear-gradient(90deg,#10b981,#34d399);
                  transition:width .1s linear;pointer-events:none;"></div>
    </div>
  </div>

  <audio id="vp-audio-%[1]d" src="%[2]s" preload="none"></audio>
</div>
<script>
(function(){
  var id = %[1]d;

  function getEls() {
    return {
      audio: document.getElementById('vp-audio-'+id),
      btn:   document.getElementById('vp-btn-'+id),
      prog:  document.getElementById('vp-prog-'+id),
      bar:   document.getElementById('vp-bar-'+id),
      timer: document.getElementById('vp-time-'+id),
    };
  }

  function fmt(s) {
    s = Math.floor(s || 0);
    return Math.floor(s / 60) + ':' + (('0' + (s %% 60)).slice(-2));
  }

  function pauseOthers(current) {
    document.querySelectorAll('audio[id^="vp-audio-"]').forEach(function(a) {
      if (a === current || a.paused) return;
      a.pause();
      var xid = a.id.replace('vp-audio-', '');
      var xb = document.getElementById('vp-btn-'+xid);
      var xp = document.getElementById('vp-prog-'+xid);
      var xt = document.getElementById('vp-time-'+xid);
      if (xb) xb.innerHTML = '▶';
      if (xp) xp.style.width = '0%%';
      if (xt) xt.textContent = '0:00';
    });
  }

  function init(attempt) {
    attempt = attempt || 0;
    var els = getEls();
    if (!els.audio || !els.btn || !els.prog || !els.bar || !els.timer) {
      if (attempt < 20) setTimeout(function() { init(attempt + 1); }, 50);
      return;
    }

    var audio = els.audio, btn = els.btn, prog = els.prog, bar = els.bar, timer = els.timer;

    if (audio._vpReady) return;
    audio._vpReady = true;

    audio.addEventListener('timeupdate', function() {
      if (!audio.duration || isNaN(audio.duration)) return;
      prog.style.width = (audio.currentTime / audio.duration * 100) + '%%';
      timer.textContent = fmt(audio.currentTime);
    });

    audio.addEventListener('ended', function() {
      btn.innerHTML = '▶';
      prog.style.width = '0%%';
      timer.textContent = '0:00';
      audio.currentTime = 0;
    });

    audio.addEventListener('error', function() {
      btn.title = 'Cannot load: ' + audio.currentSrc;
      btn.style.background = '#ef4444';
      btn.innerHTML = '✕';
      console.error('Voice load error. src=' + audio.currentSrc +
        ' networkState=' + audio.networkState +
        ' error=' + (audio.error ? audio.error.code : 'null'));
    });

    window['vpPlay_'+id] = function() {
      if (audio.paused) {
        pauseOthers(audio);
        audio.play().catch(function(e) { console.warn('play() rejected:', e); });
        btn.innerHTML = '⏸';
      } else {
        audio.pause();
        btn.innerHTML = '▶';
      }
    };

    window['vpSeek_'+id] = function(evt) {
      if (!audio.duration || isNaN(audio.duration)) return;
      var rect = bar.getBoundingClientRect();
      var ratio = Math.max(0, Math.min(1, (evt.clientX - rect.left) / rect.width));
      audio.currentTime = ratio * audio.duration;
    };
  }

  init();
})();
</script>`, id, voiceUrl)
}

// ─────────────────────────────────────────────────────────────────────────────
// REACTION COUNTS SECTION  (taliyaan/chupair summary + activity feed)
// ─────────────────────────────────────────────────────────────────────────────
// Rules:
//   - 0 total reactions        -> render nothing (no empty "0 👏 0 ✋" row).
//   - 1–4 total reactions      -> no count badges at all (too few to be worth
//     a summary) — just the centered activity feed.
//   - 5+ total reactions       -> show the compact counts as a tap-to-expand
//     toggle button, so long activity lists don't
//     dominate the post.
func buildReactionCountsSection(id int, author string, counts map[string]int) string {
	total := 0
	for _, n := range counts {
		total += n
	}
	if total == 0 {
		return ""
	}

	if total >= 5 {
		var badges strings.Builder
		for _, r := range reactionList {
			n := counts[r.Key]
			if n == 0 {
				continue
			}
			badges.WriteString(fmt.Sprintf(
				`<span style="display:inline-flex;align-items:center;gap:3px;"><img src="%s" loading="lazy" style="width:14px;height:14px;object-fit:contain;" alt="%s">%d</span>`,
				r.Gif, r.Label, n,
			))
		}
		return fmt.Sprintf(`
    <div class="pt-2 border-t border-gray-100 dark:border-gray-800" style="margin-top:8px;text-align:center;">
      <button type="button" onclick="var c=document.getElementById('activity-%d');c.classList.toggle('hidden');" style="display:inline-flex;align-items:center;justify-content:center;gap:10px;padding:5px 14px;border-radius:999px;background:linear-gradient(180deg,rgba(0,0,0,0.02),rgba(0,0,0,0.05));border:1px solid var(--post-border);cursor:pointer;margin:4px auto 0;transition:background .15s;font-size:11px;font-weight:800;color:var(--post-text);">
        %s
      </button>
      <div id="activity-%d" class="hidden mt-2 space-y-1" style="text-align:center!important;">%s</div>
    </div>`, id, badges.String(), id, buildInteractionsList(id, author))
	}

	return fmt.Sprintf(`
    <div class="pt-2 border-t border-gray-100 dark:border-gray-800" style="margin-top:8px;">
      <div class="space-y-1" style="text-align:center!important;">%s</div>
    </div>`, buildInteractionsList(id, author))
}

// ─────────────────────────────────────────────────────────────────────────────
// MAIN POST RENDERER
// ─────────────────────────────────────────────────────────────────────────────

func renderPostHTML(id int, author, content string, createdAt time.Time, currentUser string, points int, allowTaliyaan bool, allowChupair bool, imageUrl string, category string, voiceUrl string, allowComments bool, followersOnly bool, expiresAt *time.Time, dpCompetitionID int) string {
	st := fetchPostRenderState(id, author, currentUser)
	return renderPostHTMLWithState(id, author, content, createdAt, currentUser, points, allowTaliyaan, allowChupair, imageUrl, category, voiceUrl, allowComments, followersOnly, expiresAt, dpCompetitionID, &st)
}

func renderPostHTMLWithState(id int, author, content string, createdAt time.Time, currentUser string, points int, allowTaliyaan bool, allowChupair bool, imageUrl string, category string, voiceUrl string, allowComments bool, followersOnly bool, expiresAt *time.Time, dpCompetitionID int, st *postRenderState) string {

	// ── DP COMPETITION POST ───────────────────────────────────────────────────
	if dpCompetitionID > 0 {
		comp, err := fetchDPCompetition(fmt.Sprintf("%d", dpCompetitionID))
		if err != nil {
			return ""
		}
		return actionButtonCSS + renderDPCompetitionPostCard(comp, currentUser, id)
	}

	// ── WORD GAME POST (auto-spawned, see wordgame.go) ────────────────────────
	if wg := getWordGameForPost(id); wg != nil {
		return renderWordGamePostCard(wg, currentUser)
	}

	// ── SITE TRIVIA POST (auto-spawned, see trivia.go) ─────────────────────────
	if tv := getTriviaForPost(id); tv != nil {
		return renderTriviaPostCard(tv, currentUser)
	}

	if st == nil {
		fallback := fetchPostRenderState(id, author, currentUser)
		st = &fallback
	}

	viewerIsBlocked := st.viewerIsBlocked
	iHaveBlockedAuthor := st.iHaveBlockedAuthor
	isFollowingAuthor := st.isFollowingAuthor
	commentCount := st.commentCount
	isTrending := st.isTrending

	// If the current user has blocked this post's author, hide the post entirely.
	if iHaveBlockedAuthor || viewerIsBlocked {
		return ""
	}

	// ── 2. RANK & STYLE ───────────────────────────────────────────────────────
	authorMeta := getCachedAuthorMeta(author)
	userType := authorMeta.UserType
	rank := authorMeta.Rank

	// Admins can delete ANY post, not just their own — computed once here
	// so the button-building logic below can check it regardless of
	// whether currentUser is the author.
	viewerIsAdmin := currentUser != "" && currentUser != author && getCachedAuthorMeta(currentUser).UserType == "admin"

rs := getRankStyle(userType, rank)
	if isDenzBirthday() && isBirthdayUser(author) {
		rs.frameClass += " bday-denz-post"
		rs.avatar += " box-shadow:0 0 0 2px var(--post-bg),0 0 0 4px #f472b6,0 0 16px 4px rgba(244,114,182,.7);"
		rs.bottomBar = birthdayPostBar() + rs.bottomBar
	}
	creature, animClass := "", ""
	adminCrownHTML := ""
	if userType == "admin" {
		adminCrownHTML = buildAdminCrownWidget(id)
	} else if t := GetRankTheme(int(rank)); t.Hex != "" {
		creature, animClass = t.Creature, t.AnimClass
	}

	// Animated fire border on the post card itself — ONLY for currently
	// trending-creator authors (has_trending_star). Top-10 leaderboard
	// rank already gets its own distinct frame via rs.frameClass
	// (getRankStyle/GetRankTheme) and must NOT also get this — the two
	// are separate visual systems and rank alone should never trigger
	// the fire border.
	//
	// Sourced from authorMeta.HasTrendingStar (the cached 90s snapshot)
	// instead of calling the live TrendingStarHTML(author) check here —
	// that check was being invoked several more times below (name badge,
	// category badge, activity rows) and could disagree with itself from
	// one call to the next, which is why the fire icon/border used to
	// flicker on and off. Every trending decision in this function now
	// reads this one cached value, computed once, so it can only change
	// when the cache itself refreshes.
if isHeera(author) {
		heeraApplyStyle(&rs)
		creature, animClass = "🦋", "anim-float"
	}
	isTrendingAuthor := authorMeta.HasTrendingStar
	trendingStarBadgeHTML := ""
	if isTrendingAuthor {
		trendingStarBadgeHTML = TrendingStarHTML(author)
	}
	postFireClass := ""
	if isTrendingAuthor {
		postFireClass = " post-fire-border"
	}

	// Category-bar badge: the top-3 rank medal now renders inline next to
	// the category name (rs.topBadge) instead of the old absolutely-
	// positioned overlay that sat on top of it. If this same top-3 author
	// is also a currently trending creator, add a small fire icon right
	// beside it so both signals show together in the category box.
	topRankCategoryBadge := ""
	if rank >= 1 && rank <= 3 {
		topRankCategoryBadge = rs.topBadge
		if isTrendingAuthor {
			topRankCategoryBadge += ` <span title="Trending Creator" style="font-size:10px;">🔥</span>`
		}
	}

		if isDenzBirthday() && isBirthdayUser(author) {
		topRankCategoryBadge += ` <span style="display:inline-flex;align-items:center;gap:2px;padding:0 6px;margin-left:4px;border-radius:999px;background:linear-gradient(90deg,#ec4899,#f59e0b);color:#fff;font-size:8px;font-weight:900;letter-spacing:.04em;vertical-align:middle;">🎂 BIRTHDAY GIRL</span>`
	}

	// ── 2b. ONLINE STATUS ─────────────────────────────────────────────────────
	lastActive := authorMeta.LastActive
if isHeera(author) {
		topRankCategoryBadge += ` <span style="display:inline-flex;align-items:center;gap:2px;padding:0 6px;margin-left:4px;border-radius:999px;background:linear-gradient(90deg,#f472b6,#a78bfa,#38bdf8);color:#fff;font-size:8px;font-weight:900;letter-spacing:.04em;vertical-align:middle;">🦋 SPECIAL</span>`
	}
	authorOnline := isUserOnline(author)

	onlineDotHTML := ""
	if authorOnline {
		onlineDotHTML = `<span style="position:absolute;bottom:0;right:0;width:10px;height:10px;background:#22c55e;border-radius:50%;border:2px solid var(--post-bg);display:block;"></span>`
	}

	var statusHTML string
	if authorOnline {
		statusHTML = `<span style="color:#16a34a;font-weight:800;">● Active now</span>`
	} else {
		statusHTML = fmt.Sprintf(`<span style="color:#9ca3af;font-weight:600;">Last seen %s</span>`, formatLastSeen(lastActive))
	}

	// ── 3. CATEGORY ───────────────────────────────────────────────────────────
	displayCategory := strings.TrimSpace(category)
	if displayCategory == "" {
		displayCategory = "General"
	}
	catUpper := strings.ToUpper(displayCategory)
	catIcon, catGrad := getCategoryMeta(displayCategory)

	// ── 3b. FULL CONTENT (no truncation) ──────────────────────────────────────
	// Previously capped at postPreviewLimit chars with a "Read more" link to
	// /post/:id. Now the complete post always renders — in the feed AND on
	// /post/:id itself (which uses this same renderPostHTML) — so there's
	// never a truncated version anywhere and nothing to click through for.
	previewContent := content
	readMoreBtn := ""

	// ── 4. BUILD BUTTONS ──────────────────────────────────────────────────────
	var replyBtn, followBtn, reactionButtons, deleteBtn, blockBtn, repostBtn, editBtn string

	postUrl := fmt.Sprintf("https://dostana.pk/post/%d", id)
	shareGroup := fmt.Sprintf(
		`<button onclick="navigator.share&&navigator.share({url:'%s'})" class="%s"><span>📤</span><span>Share</span></button>`,
		postUrl, shareStyle,
	)

	// Centered action row on your own post (nothing else competing for
	// attention while you review it); everyone else keeps the normal
	// left-aligned, horizontally-scrollable row.
	actionsJustify := "flex-start"
	if currentUser == author {
		actionsJustify = "center"
	}

	if currentUser != "" && !viewerIsBlocked {
		switch {
		case currentUser == author:
			replyBtn = fmt.Sprintf(
				`<a href="/post/%d" class="%s"><span>💬</span><span>Comments %d</span></a>`,
				id, replyStyle, commentCount,
			)
		case !allowComments:
			// Comments are off for everyone except the owner — show a
			// disabled-looking indicator instead of a working link.
			replyBtn = fmt.Sprintf(
				`<span class="%s" style="opacity:.55;cursor:not-allowed;"><span>🔒</span><span>Comments Off</span></span>`,
				replyStyle,
			)
		case followersOnly && !isFollowingAuthor:
			// Comments are on, but restricted to followers, and this
			// viewer doesn't follow the author yet.
			replyBtn = fmt.Sprintf(
				`<a href="/profile/%s" class="%s" style="color:#7c3aed;"><span>🔒</span><span>Follow to Reply</span></a>`,
				author, replyStyle,
			)
		default:
			replyBtn = fmt.Sprintf(
				`<a href="/post/%d" class="%s"><span>💬</span><span>Comments %d</span></a>`,
				id, replyStyle, commentCount,
			)
		}

		if currentUser == author {
			editBtn = fmt.Sprintf(
				`<button hx-get="/post/%d/edit-form" hx-target="#post-%d" hx-swap="outerHTML" class="action-btn btn-edit"><span>✏️</span><span>Edit</span></button>`,
				id, id,
			)
			deleteBtn = fmt.Sprintf(
				`<button hx-delete="/post/%d" hx-target="#post-%d" hx-swap="outerHTML" class="%s"><span>🗑</span><span>Delete</span></button>`,
				id, id, deleteStyle,
			)
		} else if viewerIsAdmin {
			deleteBtn = fmt.Sprintf(
				`<button hx-delete="/post/%d" hx-target="#post-%d" hx-swap="outerHTML" hx-confirm="Admin: delete this post by %s? This cannot be undone." class="%s" style="border-color:#7c3aed;color:#7c3aed;"><span>🛡️🗑</span><span>Admin Delete</span></button>`,
				id, id, html.EscapeString(author), deleteStyle,
			)
		}

		if currentUser != author {
			// Follow / Unfollow
			fLabel, fIcon, fClass, fRoute := "Follow", "➕", followStyle, fmt.Sprintf("/follow/%s?post_id=%d", author, id)
			if isFollowingAuthor {
				fLabel, fIcon, fClass, fRoute = "Unfollow", "✖️", unfollowStyle, fmt.Sprintf("/unfollow/%s?post_id=%d", author, id)
			}
			followBtn = fmt.Sprintf(
				`<button hx-post="%s" hx-target="#post-%d" hx-swap="outerHTML" class="%s"><span>%s</span><span>%s</span></button>`,
				fRoute, id, fClass, fIcon, fLabel,
			)

			// Block / Unblock — top-3 scorers can now be blocked like
			// anyone else; the old "Protected" restriction is removed.
			bLabel, bIcon := "Block", "🚫"
			bConfirm := fmt.Sprintf("Are you sure you want to block %s?", html.EscapeString(author))
			if iHaveBlockedAuthor {
				bLabel, bIcon = "Unblock", "✅"
				bConfirm = fmt.Sprintf("Unblock %s?", html.EscapeString(author))
			}
			blockBtn = fmt.Sprintf(
				`<div id="block-btn-%d" style="display:inline-flex;flex-shrink:0;"><button hx-post="/block-toggle/%s" hx-target="#block-btn-%d" hx-swap="outerHTML" hx-confirm="%s" class="%s"><span>%s</span><span>%s</span></button></div>`,
				id, author, id, bConfirm, blockStyle, bIcon, bLabel,
			)
			// Single React button — reveals the 9-GIF reaction picker on
			// hover / long-press, Facebook-style.
			reactionButtons = buildReactButton(id, st.myReaction, st.disabledReactions)

			// Repost button — hidden entirely if this post's repost_policy
			// doesn't allow the current viewer (e.g. "no one", or
			// "followers only" and the viewer doesn't follow).
			repostBtn = buildRepostButton(id, author, currentUser, isFollowingAuthor, st.repostPolicy, st.thoughtsPolicy, st.iHaveReposted)
		}
	}

	// ── 4b. REPOST EMBED ──────────────────────────────────────────────────────
	// Rendered as a compact, READ-ONLY preview card (Twitter/X-style quote
	// card) instead of a full recursive renderPostHTML() call. Nesting an
	// entire interactive post card — its own avatar row, its own
	// margin-left:55px text offset, its own button row, its own <style>
	// ids — inside the parent post's already-narrow content column is what
	// squeezed it into a tiny box with cut-off buttons. This card has no
	// nested action buttons at all (tap it to open the real post), is full
	// card width, and is placed OUTSIDE the 55px text indent below.
	repostBannerHTML := ""
	repostEmbedHTML := ""
	if st.repostOf > 0 {
		repostBannerHTML = fmt.Sprintf(`<div style="font-size:10px;font-weight:800;color:#059669;display:flex;align-items:center;gap:4px;margin:0 0 4px;"><span>🔁</span><span>%s ne repost kiya</span></div>`, html.EscapeString(DisplayName(author, authorMeta.DisplayName)))
		repostEmbedHTML = buildRepostedOriginalCard(id, st.repostOf, currentUser)
	}

	// ── 5. ASSEMBLE BLOCKS ────────────────────────────────────────────────────
	countsSectionHTML := buildReactionCountsSection(id, author, st.reactionCounts)

	// ── 6. RENDER ─────────────────────────────────────────────────────────────
	// NOTE: buttonCSSBlock is intentionally NOT emitted here anymore — it's
	// injected once in home.go's main <style> block instead. Repeating a full
	// <style> tag inside every single post (dozens per feed, re-parsed on every
	// htmx swap / DOMParser extraction for Trending/For Me/Leaderboard) was
	// fragile and was causing the CSS to leak as literal visible text on the
	// page instead of being applied as a stylesheet.
	return heeraCSSFor(author) + actionButtonCSS + reactionPickerCSS + repostPickerCSS + rankFrameCSS + creatureCSS + fireBorderCSS + repostedEmbedCSS + fmt.Sprintf(`
<div id="post-notice-%d"></div>
<div id="post-%d" class="post-card mb-4 overflow-hidden rounded-2xl transition-all duration-500 bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 %s%s" style="%s">
  <!-- Category + Timestamp bar -->
  <div class="w-full px-4 py-1.5 flex justify-between items-center" style="background:%s;box-shadow:inset 0 -6px 8px -6px rgba(0,0,0,0.15);">
    <span style="color:#ffffff!important;background-color:rgba(0,0,0,0.6)!important;padding:1px 6px;border-radius:4px;text-shadow:0 1px 2px rgba(0,0,0,0.8);font-size:9px;font-weight:900;text-transform:uppercase;letter-spacing:0.05em;display:inline-flex;align-items:center;line-height:1.2;">%s %s%s</span>%s
    <span style="color:#ffffff!important;background-color:rgba(0,0,0,0.6)!important;padding:2px 8px;border-radius:4px;text-shadow:0 1px 2px rgba(0,0,0,0.8);font-size:9px;font-weight:700;text-transform:uppercase;letter-spacing:0.05em;display:inline-block;line-height:1.2;margin-right:2px;">%s</span>%s
  </div>

  <div class="p-2.5">

    <!-- Author row -->
    <div class="flex items-center" style="gap:9px!important;padding-left:10px!important;padding-right:8px!important;margin-bottom:7px!important;">
      <div style="position:relative;flex-shrink:0;">
        <div class="w-9 h-9 rounded-full overflow-hidden bg-gray-100 dark:bg-gray-800" style="%s">%s</div>
        %s
      </div>
      <div class="flex-1 min-w-0">
        <div class="flex items-center gap-1" style="flex-wrap:wrap;">
          <span class="inline-flex items-center gap-1 min-w-0" style="line-height:1;">%s%s</span>
          %s %s
          <span class="text-[10px] font-bold text-gray-500 dark:text-gray-400" style="%s;align-self:center;">(%d)</span>
        </div>
        <div style="font-size:9px;margin-top:1px;">%s</div>
      </div>
    </div>

     <!-- Content -->
    <div class="mb-3" style="padding-left:0!important;margin-left:55px!important;padding-right:12px!important;box-sizing:border-box!important;max-width:calc(100%% - 55px)!important;overflow-wrap:anywhere!important;">
      %s
      <p class="text-[14.5px] sm:text-[15.5px] font-normal break-words text-gray-800 dark:text-gray-100" style="%s;line-height:1.62;letter-spacing:0.005em;margin:0!important;overflow-wrap:anywhere!important;">%s%s</p>
      %s
      %s
    </div>

    <!-- Reposted original — deliberately OUTSIDE the 55px text indent
         above, so it spans the full post card width instead of being
         squeezed into the same narrow column as the caption text. -->
    <div style="padding:0 4px;box-sizing:border-box;width:100%%;">%s</div>

    <!-- All buttons in one row — horizontally scrollable (not wrapped),
         so extra buttons (e.g. admin's Delete) never get visually cut
         off: a right-edge fade mask (see .post-actions-row CSS) signals
         there's more to scroll instead of hard-clipping the last button. -->
    <div class="no-scrollbar post-actions-row" style="display:flex!important;flex-direction:row!important;flex-wrap:nowrap!important;justify-content:%s!important;align-items:center!important;gap:6px!important;overflow-x:auto!important;-webkit-overflow-scrolling:touch;width:100%%!important;padding:6px 4px 14px;margin-top:6px;border-top:1px solid #f1f5f9;scrollbar-width:none;-ms-overflow-style:none;">
      %s %s %s %s %s %s %s %s
    </div>

    <!-- Counts / activity — hidden entirely when there are 0 reactions -->
    %s

  </div>
  %s
</div>`,
		// post div
		// rs.topBadge is intentionally NOT emitted here anymore — the
		// #1/#2/#3 medal now shows in exactly one place: inline inside
		// the category box via topRankCategoryBadge below. Previously
		// it also rendered here, right at the top of the card, which
		// duplicated the same badge in two spots on every top-3 post.
		id, id, rs.frameClass, postFireClass, rs.container,
		// category bar
		catGrad, catIcon, catUpper, topRankCategoryBadge, TrendingFireBadge(isTrending), formatTwitterTime(createdAt), buildExpiryBadge(id, expiresAt),
		// author avatar
		rs.avatar, avatarHTMLFromPath(authorMeta.ProfileImage, author), onlineDotHTML,
		// author name row
		// RenderDisplayName(author) already returns a fully self-styled,
		// self-linking element (either GetDecoratedUsername's own <a>
		// with rank styling, or a custom-name <a> with the @username
		// caption beneath it) — it must never be nested inside another
		// <a href="/profile/...">, which was producing invalid nested
		// anchors and throwing the points badge out of vertical
		// alignment whenever a two-line custom display name was in play.
		RenderDisplayName(author), trendingStarBadgeHTML,
		adminCrownHTML, buildCreatureWidget(id, creature, animClass),
		rs.text, points,
		// online / last-seen status line
		statusHTML,
		// content
		repostBannerHTML,
		rs.text, processContent(previewContent, id), readMoreBtn,
		buildImageBlock(imageUrl),
		buildVoiceBlock(id, voiceUrl),
		// reposted original — full-width card below the caption
		repostEmbedHTML,
		// all buttons in one row — centered on your own post, left-aligned
		// on everyone else's
		actionsJustify,
		reactionButtons, editBtn, replyBtn, followBtn, repostBtn, shareGroup, blockBtn, deleteBtn,
		// counts / activity section (already fully rendered, or empty string)
		countsSectionHTML, rs.bottomBar,
	)
}

// ─────────────────────────────────────────────────────────────────────────────
// INTERACTIONS LIST
// ─────────────────────────────────────────────────────────────────────────────

type interactionRow struct {
	sender       string
	typ          string
	senderType   string
	authorType   string
	senderPoints int
	senderRank   int64
}

func buildInteractionsList(postID int, postAuthor string) string {
	rows, err := db.Query(ctx, `
		SELECT sender, type
		FROM interactions
		WHERE post_id = $1
		ORDER BY created_at DESC`, postID)

	if err != nil {
		return `<p class="text-[10px] text-gray-400 italic px-2">No activity yet</p>`
	}
	defer rows.Close()

	metaMap := getCachedAuthorMetaMap()
	authorInfo := metaMap[postAuthor]

	var sb strings.Builder
	hasItems := false
	num := 0

	for rows.Next() {
		hasItems = true
		num++
		var ir interactionRow
		rows.Scan(&ir.sender, &ir.typ)
		senderInfo := metaMap[ir.sender]
		ir.senderType = senderInfo.UserType
		ir.senderRank = senderInfo.Rank
		ir.authorType = authorInfo.UserType

		isAdminSender := ir.senderType == "admin"
		isAdminAuthor := ir.authorType == "admin"
		isTopScorer := ir.senderRank >= 1 && ir.senderRank <= 3
		isTrending := senderInfo.HasTrendingStar
		senderStarHTML := ""
		if isTrending {
			senderStarHTML = TrendingStarHTML(ir.sender)
		}

		def := getReactionDef(ir.typ)
		isNegative := isNegativeReaction(ir.typ)
		pts, note := calcReactionPoints(ir.typ, isAdminSender, isAdminAuthor, isTrending, isTopScorer)

		colorClass, sign, badgeClass := "text-emerald-600 dark:text-emerald-500", "+", "bg-emerald-100 dark:bg-emerald-900/40"
		if isNegative {
			colorClass, sign, badgeClass = "text-rose-600 dark:text-rose-500", "-", "bg-rose-100 dark:bg-rose-900/40"
		}

		sb.WriteString(fmt.Sprintf(`
<div class="text-[10px] font-medium %s flex-wrap" style="display:flex!important;align-items:center!important;justify-content:center!important;gap:4px!important;text-align:center!important;width:100%%!important;">
  <span class="font-bold text-gray-400">%d.</span> <a href="/profile/%s" class="font-black hover:underline">%s</a>%s
  <img src="%s" loading="lazy" style="width:14px;height:14px;object-fit:contain;vertical-align:middle;" alt="%s">
  <span>%s!</span>
  <span class="%s px-1 rounded text-[9px] font-bold">%s%d Score</span>
  %s
</div>`, colorClass, num, ir.sender, ir.sender, senderStarHTML, def.Gif, def.Label, def.Verb, badgeClass, sign, pts, note))
	}

	if !hasItems {
		return `<p class="text-[10px] text-gray-400 italic px-2">No activity yet</p>`
	}
	return sb.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// POINT CALCULATORS  (extracted from inline logic)
// ─────────────────────────────────────────────────────────────────────────────

// calcReactionPoints returns a non-negative point magnitude plus an
// optional bonus/penalty note for any of the 9 reaction types. Callers
// add it for positive reactions and subtract it for negative ones
// (isNegativeReaction).
func calcReactionPoints(typ string, isAdminSender, isAdminAuthor, isTrending, isTopScorer bool) (pts int, note string) {
	if isNegativeReaction(typ) {
		switch {
		case isAdminSender:
			return 100000, `<span class="bg-purple-100 dark:bg-purple-900/40 px-1 rounded text-[9px] font-bold text-purple-700">👑 Admin Slap! -100,000</span>`
		case isTrending && isTopScorer:
			return 5000, `<span class="bg-amber-100 dark:bg-amber-900/40 px-1 rounded text-[9px] font-bold text-amber-700">🔥 Trending + Top 3 Penalty!</span>`
		case isTrending:
			return 5000, `<span class="bg-orange-100 dark:bg-orange-900/40 px-1 rounded text-[9px] font-bold text-orange-600">🔥 Trending Creator!</span>`
		case isTopScorer:
			return 50, `<span class="bg-yellow-100 dark:bg-yellow-900/40 px-1 rounded text-[9px] font-bold text-yellow-700">⭐ Top 3 Penalty!</span>`
		default:
			return 3, ""
		}
	}
	switch {
	case isAdminAuthor:
		return 5, ""
	case isTrending && isTopScorer:
		return 5000, `<span class="bg-amber-100 dark:bg-amber-900/40 px-1 rounded text-[9px] font-bold text-amber-700">🔥 Trending + Top 3 Bonus!</span>`
	case isTrending:
		return 5000, `<span class="bg-orange-100 dark:bg-orange-900/40 px-1 rounded text-[9px] font-bold text-orange-600">🔥 Trending Creator!</span>`
	case isTopScorer:
		return 100, `<span class="bg-yellow-100 dark:bg-yellow-900/40 px-1 rounded text-[9px] font-bold text-yellow-700">⭐ Top 3 Bonus!</span>`
	default:
		return 5, ""
	}
}

// buildReactButton renders the single React button plus its hidden
// 9-GIF picker (Facebook-style: tap = quick "like", hover/long-press =
// reveal all). Posts with every reaction disabled render nothing.
// buildReactButton renders the single React button plus its hidden
// 9-GIF picker (Facebook-style: tap = quick "like", hover/long-press =
// reveal all). Posts with every reaction disabled render nothing.
func buildReactButton(id int, myReaction, disabledReactions string) string {
	var visible []reactionDef
	for _, r := range reactionList {
		if !isReactionDisabled(disabledReactions, r.Key) {
			visible = append(visible, r)
		}
	}
	if len(visible) == 0 {
		return ""
	}

	mainInner := `<span>👍</span><span>React</span>`
	if myReaction != "" {
		if d := getReactionDef(myReaction); d.Gif != "" {
			mainInner = fmt.Sprintf(`<img src="%s" loading="lazy" style="width:16px;height:16px;object-fit:contain;" alt="">%s`, d.Gif, d.Label)
		}
	}

	var opts strings.Builder
	for _, r := range visible {
		mine := ""
		if r.Key == myReaction {
			mine = " reaction-mine"
		}
		opts.WriteString(fmt.Sprintf(
			`<button type="button" class="reaction-opt%s" title="%s" onclick="event.stopPropagation();reactPick(%d,'%s')"><img src="%s" loading="lazy" alt="%s"></button>`,
			mine, r.Label, id, r.Key, r.Gif, r.Label,
		))
	}

	return fmt.Sprintf(`
<div class="reaction-wrap" id="react-wrap-%d">
  <button type="button" class="%s" onclick="event.stopPropagation();reactToggle(%d)">%s</button>
  <div class="reaction-picker" id="react-picker-%d">%s</div>
</div>
<script>
(function(){
  if (window._reactJsReady) return;
  window._reactJsReady = true;

  // The picker is reparented to document.body while open (dodges the
  // position:fixed containing-block problem from transformed rank-frame
  // ancestors) and put back via a placeholder before any htmx swap.
  //
  // IMPORTANT: it closes ONLY on (a) picking a reaction, (b) tapping
  // React again, or (c) tapping anywhere outside it. It deliberately
  // does NOT close on scroll, resize, or background polling —
  // this page polls constantly (inbox badge every 1s, online slider
  // every 8s, invites every 10s), and tapping React itself can nudge
  // the post's own horizontally-scrollable button row, which used to
  // fire a scroll event and close the picker the instant it opened.
  // Removing those listeners is the actual fix for that.
  var openState = null; // { id, picker, placeholder }

  function restorePicker(){
    if (!openState) return;
    var picker = openState.picker, placeholder = openState.placeholder;
    picker.classList.remove('open', 'flip-down');
    if (placeholder.parentNode) {
      placeholder.parentNode.insertBefore(picker, placeholder);
      placeholder.parentNode.removeChild(placeholder);
    }
    openState = null;
  }

  function closeAllPickers(){
    restorePicker();
  }

  function openPicker(id){
    var wrap = document.getElementById('react-wrap-'+id);
    var picker = document.getElementById('react-picker-'+id);
    if (!wrap || !picker) return;
    var btn = wrap.querySelector('.btn-react') || wrap;
    var rect = btn.getBoundingClientRect();

    var placeholder = document.createElement('span');
    placeholder.style.display = 'none';
    picker.parentNode.insertBefore(placeholder, picker);
    document.body.appendChild(picker);

    var spaceAbove = rect.top;
    var flipDown = spaceAbove < 70;
    picker.classList.toggle('flip-down', flipDown);
    picker.style.left = (window.innerWidth / 2) + 'px';
    picker.style.top = flipDown ? (rect.bottom + 6) + 'px' : (rect.top - 6) + 'px';

    openState = { id: id, picker: picker, placeholder: placeholder };
    void picker.offsetWidth;
    picker.classList.add('open');
  }

  window.reactToggle = function(id){
    var wasOpenForThisId = openState && openState.id === id;
    closeAllPickers();
    if (!wasOpenForThisId) openPicker(id);
  };

  window.reactPick = function(id, type){
    closeAllPickers();
    htmx.ajax('POST', '/react/'+type+'/'+id, {target:'#post-'+id, swap:'outerHTML'});
  };

  document.addEventListener('click', function(e){
    if (!openState) return;
    var wrap = document.getElementById('react-wrap-'+openState.id);
    if (openState.picker.contains(e.target)) return;
    if (wrap && wrap.contains(e.target)) return;
    closeAllPickers();
  });
})();
</script>`, id, reactBtnStyle, id, mainInner, id, opts.String())
}

// buildRepostButton renders the Repost action button plus its small popup
// (plain repost vs repost-with-a-thought). Renders nothing if the viewer
// isn't allowed to repost this post at all.
func buildRepostButton(id int, author, currentUser string, isFollowing bool, repostPolicy, thoughtsPolicy string, iAlreadyReposted bool) string {
	if !canRepost(repostPolicy, currentUser, author, isFollowing) {
		return ""
	}
	canThought := canRepost(thoughtsPolicy, currentUser, author, isFollowing)

	mainCls := repostStyle
	label := "Repost"
	if iAlreadyReposted {
		mainCls += " reposted"
		label = "Reposted"
	}

	thoughtBox, quoteRow := "", ""
	if canThought {
		thoughtBox = fmt.Sprintf(`
<div class="repost-thought-box" id="repost-thought-box-%d">
  <textarea id="repost-thought-text-%d" maxlength="500" placeholder="Apna khayal likhein (optional)…"></textarea>
  <button type="button" class="repost-thought-send" onclick="event.stopPropagation();repostSend(%d)">Repost Karein</button>
</div>`, id, id, id)
		quoteRow = fmt.Sprintf(`<button type="button" class="repost-picker-btn" onclick="event.stopPropagation();repostToggleThought(%d)"><span>💭</span><span>Khayal Ke Sath Repost</span></button>`, id)
	}

	return fmt.Sprintf(`
<div class="repost-wrap" id="repost-wrap-%[1]d">
  <button type="button" class="%[2]s" onclick="event.stopPropagation();repostToggle(%[1]d)"><span>🔁</span><span>%[3]s</span></button>
  <div class="repost-picker" id="repost-picker-%[1]d">
    <button type="button" class="repost-picker-btn" onclick="event.stopPropagation();repostSend(%[1]d)"><span>🔁</span><span>Fauran Repost Karein</span></button>
    %[4]s
    %[5]s
  </div>
</div>
<script>
(function(){
  if (window._repostJsReady) return;
  window._repostJsReady = true;

  var openState = null;

  function restorePicker(){
    if (!openState) return;
    var picker = openState.picker, placeholder = openState.placeholder;
    picker.classList.remove('open', 'flip-down');
    if (placeholder.parentNode) {
      placeholder.parentNode.insertBefore(picker, placeholder);
      placeholder.parentNode.removeChild(placeholder);
    }
    openState = null;
  }

  window.repostToggle = function(id){
    var wasOpenForThisId = openState && openState.id === id;
    restorePicker();
    if (wasOpenForThisId) return;

    var wrap = document.getElementById('repost-wrap-'+id);
    var picker = document.getElementById('repost-picker-'+id);
    if (!wrap || !picker) return;
    var btn = wrap.querySelector('.btn-repost') || wrap;
    var rect = btn.getBoundingClientRect();

    var placeholder = document.createElement('span');
    placeholder.style.display = 'none';
    picker.parentNode.insertBefore(placeholder, picker);
    document.body.appendChild(picker);

    var flipDown = rect.top < 140;
    picker.classList.toggle('flip-down', flipDown);
    picker.style.left = Math.min(Math.max(rect.left, 120), window.innerWidth-120) + 'px';
    picker.style.top = flipDown ? (rect.bottom + 6) + 'px' : (rect.top - 6) + 'px';

    openState = { id: id, picker: picker, placeholder: placeholder };
    void picker.offsetWidth;
    picker.classList.add('open');
  };

  window.repostToggleThought = function(id){
    var box = document.getElementById('repost-thought-box-'+id);
    if (box) box.classList.toggle('open');
  };

  window.repostSend = function(id){
    var ta = document.getElementById('repost-thought-text-'+id);
    var thought = ta ? ta.value : '';
    restorePicker();
    htmx.ajax('POST', '/repost/'+id, {
      target: '#post-notice-'+id,
      swap: 'innerHTML',
      values: { thought: thought }
    }).then(function(){
      setTimeout(function(){
        var n = document.getElementById('post-notice-'+id);
        if (n) n.innerHTML = '';
      }, 2500);
    });
  };

  document.addEventListener('click', function(e){
    if (!openState) return;
    var wrap = document.getElementById('repost-wrap-'+openState.id);
    if (openState.picker.contains(e.target)) return;
    if (wrap && wrap.contains(e.target)) return;
    restorePicker();
  });
})();
</script>`, id, mainCls, label, quoteRow, thoughtBox)
}

// buildRepostedOriginalCard renders a compact, read-only, full-width
// preview of the original post being reposted — avatar, name, timestamp,
// category chip, content, image/voice. It deliberately has NO action
// buttons of its own (tap anywhere to open the real post at /post/:id) —
// that's what fixes the "buttons cut off / tiny box" problem: there is
// nothing left to clip, and the card is never narrower than the parent
// post card itself. repostID (the repost post's own id, not the
// original's) is only used to keep this card's inner DOM/JS ids unique
// from any other copy of the SAME original post that might independently
// be rendered elsewhere on the same page (e.g. also visible in the feed).
// buildRepostedOriginalCard now renders the ORIGINAL post as a full,
// fully-interactive post card — same avatar/rank styling, same category
// bar, same points badge, same React/Comments/Follow/Repost/Share/
// Block/Delete button row, same reaction counts/activity feed — instead
// of the old stripped-down read-only preview that had none of that.
// This is exactly what renderPostHTML() builds for a normal post, so a
// reposted post now shows 100% the same information a viewer would see
// on /post/<origID> directly, just embedded below the repost's caption.
// Its own action-button row and reaction/activity section are hidden by
// repostedEmbedCSS above so the repost never shows two stacked sets of
// the same buttons — tapping anywhere on the embedded card (see the <a>
// wrapper below) takes the viewer straight to /post/<origID>.
//
// KNOWN TRADE-OFF: renderPostHTML's markup uses ids keyed only to the
// post's own numeric id (id="post-N", "react-wrap-N", "block-btn-N",
// "vp-audio-N", etc). If the SAME original post is also visible
// standalone elsewhere on the same page at the same time (e.g. it's in
// your normal feed AND someone you follow reposted it, both on screen
// at once), the DOM will contain two elements sharing those ids, and an
// id-based hx-target resolves to the first match. This is the accepted
// cost of full parity over a stripped preview, and mirrors the
// duplicate-id tolerance this codebase already accepts elsewhere (see
// the actionButtonCSS <style id> comment above).
func buildRepostedOriginalCard(repostID, origID int, currentUser string) string {
	var oAuthor, oContent, oCategory, oImageURL, oVoiceURL string
	var oCreatedAt time.Time
	var oPoints, oDPCompID, oRootRepostOf int
	var oAllowJ, oAllowC, oAllowComments, oFollowersOnly bool
	var oExpiresAt *time.Time

	deletedHTML := `<div style="width:100%;box-sizing:border-box;margin-top:6px;padding:16px 12px;border:1.5px dashed #d1d5db;border-radius:14px;font-size:11.5px;font-weight:700;color:#9ca3af;text-align:center;">🗑️ Ye post delete ho chuki hai</div>`

	if err := db.QueryRow(ctx, `
		SELECT p.author, p.content, COALESCE(p.category,'General'),
		       COALESCE(p.image_url,''), COALESCE(p.voice_url,''), p.created_at,
		       COALESCE(u.points,0), p.allow_jhappi, p.allow_chupair,
		       COALESCE(p.allow_comments,true), COALESCE(p.followers_only,false),
		       p.expires_at, COALESCE(p.dp_competition_id,0), COALESCE(p.repost_of,0)
		FROM posts p
		LEFT JOIN users u ON u.username = p.author
		WHERE p.id = $1`, origID,
	).Scan(&oAuthor, &oContent, &oCategory, &oImageURL, &oVoiceURL, &oCreatedAt,
		&oPoints, &oAllowJ, &oAllowC, &oAllowComments, &oFollowersOnly, &oExpiresAt,
		&oDPCompID, &oRootRepostOf,
	); err != nil {
		return deletedHTML
	}

	// Safety net only: repostHandler always resolves to the true root
	// post before storing repost_of, so this should never fire on data
	// created going forward — it just guards against any legacy row
	// where repost_of chains two deep, which would otherwise nest one
	// repost embed inside another.
	if oRootRepostOf > 0 {
		origID = oRootRepostOf
		if err := db.QueryRow(ctx, `
			SELECT p.author, p.content, COALESCE(p.category,'General'),
			       COALESCE(p.image_url,''), COALESCE(p.voice_url,''), p.created_at,
			       COALESCE(u.points,0), p.allow_jhappi, p.allow_chupair,
			       COALESCE(p.allow_comments,true), COALESCE(p.followers_only,false),
			       p.expires_at, COALESCE(p.dp_competition_id,0)
			FROM posts p
			LEFT JOIN users u ON u.username = p.author
			WHERE p.id = $1`, origID,
		).Scan(&oAuthor, &oContent, &oCategory, &oImageURL, &oVoiceURL, &oCreatedAt,
			&oPoints, &oAllowJ, &oAllowC, &oAllowComments, &oFollowersOnly, &oExpiresAt,
			&oDPCompID,
		); err != nil {
			return deletedHTML
		}
	}

	innerCard := renderPostHTML(
		origID, oAuthor, oContent, oCreatedAt, currentUser, oPoints,
		oAllowJ, oAllowC, oImageURL, oCategory, oVoiceURL,
		oAllowComments, oFollowersOnly, oExpiresAt, oDPCompID,
	)

	// .reposted-embed hides the embedded original's own action-button row
	// (see repostedEmbedCSS above). The wrapper is a <div> with an onclick
	// navigation instead of an <a> — the embedded card itself still
	// contains its own <a href="/profile/..."> (author name) and other
	// anchor links, and nesting <a> inside <a> is invalid HTML: browsers
	// silently close the outer anchor early at the first inner one, which
	// splits the DOM mid-layout and was exactly what misaligned the
	// avatar/username/content. A div+onclick avoids that entirely while
	// still taking the viewer to /post/origID on tap. stopPropagation
	// isn't needed on the inner profile-name link — letting it win on
	// direct taps (go to the profile) is the expected behavior anyway.
	return fmt.Sprintf(`<div class="reposted-embed" onclick="if(!event.target.closest('a')){window.location='/post/%d';}" style="display:block;width:100%%;max-width:100%%;box-sizing:border-box;margin-top:6px;cursor:pointer;">%s</div>`, origID, innerCard)
}

// canRepost applies a post's repost_policy / thoughts_policy value
// ("public" | "followers" | "none") for a given viewer. The author can
// always act on their own post.
func canRepost(policy, viewer, author string, isFollowing bool) bool {
	if viewer != "" && viewer == author {
		return true
	}
	switch policy {
	case "none":
		return false
	case "followers":
		return isFollowing
	default: // "public" (or unset/unknown -> default to public)
		return true
	}
}

// repostHandler creates a new post that references an existing one via
// repost_of, optionally carrying a short "thought" as its own content.
// Reposts always chain back to the original content post (never to a
// repost of a repost), and both the repost itself and adding a thought
// are gated per-post by repost_policy / thoughts_policy.
func repostHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Status(401).SendString("Unauthorized")
	}

	origID, convErr := strconv.Atoi(c.Params("id"))
	if convErr != nil {
		return c.Status(400).SendString("Invalid post id")
	}

	var author, category, repostPolicy, thoughtsPolicy string
	var rootRepostOf int
	if err := db.QueryRow(ctx, `
		SELECT author, category, COALESCE(repost_policy,'public'), COALESCE(thoughts_policy,'public'), COALESCE(repost_of,0)
		FROM posts WHERE id = $1`, origID,
	).Scan(&author, &category, &repostPolicy, &thoughtsPolicy, &rootRepostOf); err != nil {
		return c.Status(404).SendString("Post not found")
	}

	targetID := origID
	if rootRepostOf > 0 {
		targetID = rootRepostOf
		if err := db.QueryRow(ctx, `
			SELECT author, category, COALESCE(repost_policy,'public'), COALESCE(thoughts_policy,'public')
			FROM posts WHERE id = $1`, targetID,
		).Scan(&author, &category, &repostPolicy, &thoughtsPolicy); err != nil {
			return c.Status(404).SendString("Post not found")
		}
	}

	if currentUser == author {
		return c.Status(400).SendString("Ap apni hi post repost nahi kar saktay")
	}

	var isFollowing bool
	db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM followers WHERE follower=$1 AND followee=$2)`,
		currentUser, author).Scan(&isFollowing)

	if !canRepost(repostPolicy, currentUser, author, isFollowing) {
		return c.Status(403).SendString("Is post ko repost karne ki ijazat nahi hai")
	}

	thought := strings.TrimSpace(c.FormValue("thought"))
	if r := []rune(thought); len(r) > 500 {
		thought = string(r[:500])
	}
	if thought != "" && !canRepost(thoughtsPolicy, currentUser, author, isFollowing) {
		thought = "" // repost allowed, thoughts aren't — fall back to a plain repost
	}

	var newID int
	if err := db.QueryRow(ctx, `
		INSERT INTO posts (content, author, category, repost_of, repost_thought)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		thought, currentUser, category, targetID, thought,
	).Scan(&newID); err != nil {
		log.Printf("repostHandler insert error: %v", err)
		return c.Status(500).SendString("Repost nahi ho saka, dobara koshish karein.")
	}

	db.Exec(ctx, `UPDATE posts SET repost_count = repost_count + 1 WHERE id = $1`, targetID)

	return c.Status(200).SendString(`<div style="text-align:center;padding:5px 8px;border-radius:8px;background:#ecfdf5;color:#059669;font-size:10.5px;font-weight:800;">✅ Repost ho gaya!</div>`)
}

// renderPostEditFormHTML swaps a post card into an inline edit form. Only
// ever shown after postEditFormHandler has verified the viewer is the
// post's own author.
func renderPostEditFormHTML(id int, rawContent string) string {
	return fmt.Sprintf(`
	<div id="post-%d" class="post-card mb-4 rounded-2xl border-2 border-blue-200 dark:border-blue-800 bg-white dark:bg-gray-900 p-4">
		<form hx-post="/post/%d/edit" hx-target="#post-%d" hx-swap="outerHTML" class="flex flex-col gap-3">
			<textarea name="content" rows="4" maxlength="5000" autofocus
				class="w-full px-3 py-2 bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-xl text-sm outline-none focus:bg-white dark:focus:bg-gray-700"
				placeholder="Apni post edit karein...">%s</textarea>
			<div class="flex items-center gap-2 justify-end">
				<button type="button" hx-get="/post/%d/edit-cancel" hx-target="#post-%d" hx-swap="outerHTML"
					class="text-xs font-bold text-gray-500 px-4 py-2 rounded-xl border border-gray-200 dark:border-gray-700">Cancel</button>
				<button type="submit" class="text-xs font-bold text-white bg-blue-600 hover:bg-blue-700 px-4 py-2 rounded-xl">Save Changes</button>
			</div>
		</form>
	</div>`, id, id, id, html.EscapeString(rawContent), id, id)
}

// renderFullPostByID re-fetches a post's current DB state and renders it
// through the normal card renderer — used to restore the real card after
// Cancel, and to show the updated card after a successful Save.
func renderFullPostByID(postID int, currentUser string) (string, error) {
	var (
		author, content, img, cat, voice             string
		createdAt                                    time.Time
		expiresAt                                     *time.Time
		points, dpCompID                              int
		allowJ, allowC, allowComments, followersOnly bool
	)
	err := db.QueryRow(ctx, `
		SELECT p.author, p.content, p.created_at, u.points, p.allow_jhappi, p.allow_chupair,
		       COALESCE(p.image_url,''), COALESCE(p.category,'General'), COALESCE(p.voice_url,''),
		       COALESCE(p.allow_comments,true), COALESCE(p.followers_only,false), p.expires_at,
		       COALESCE(p.dp_competition_id,0)
		FROM posts p JOIN users u ON p.author = u.username WHERE p.id=$1`, postID).Scan(
		&author, &content, &createdAt, &points, &allowJ, &allowC, &img, &cat, &voice,
		&allowComments, &followersOnly, &expiresAt, &dpCompID)
	if err != nil {
		return "", err
	}
	return renderPostHTML(postID, author, content, createdAt, currentUser, points,
		allowJ, allowC, img, cat, voice, allowComments, followersOnly, expiresAt, dpCompID), nil
}

// postEditFormHandler shows the edit form — GET only, no DB write, so the
// server-side ownership check here is just to avoid leaking whether a
// post exists to someone who isn't allowed to edit it; the real
// authorization gate is postEditSubmitHandler below.
func postEditFormHandler(c *fiber.Ctx) error {
	postID, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return c.Status(400).SendString("")
	}
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Status(401).SendString("")
	}
	var author, content string
	if err := db.QueryRow(ctx, "SELECT author, content FROM posts WHERE id=$1", postID).Scan(&author, &content); err != nil {
		return c.Status(404).SendString("")
	}
	if currentUser != author {
		return c.Status(403).SendString("")
	}
	c.Set("Content-Type", "text/html")
	return c.SendString(renderPostEditFormHTML(postID, content))
}

func postEditCancelHandler(c *fiber.Ctx) error {
	postID, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return c.Status(400).SendString("")
	}
	rendered, err := renderFullPostByID(postID, getCurrentUser(c))
	if err != nil {
		return c.Status(404).SendString("")
	}
	c.Set("Content-Type", "text/html")
	return c.SendString(rendered)
}

// postEditSubmitHandler is the actual write path. currentUser != author
// is checked here independently of postEditFormHandler — this is the
// check that actually matters, since a request straight to this endpoint
// never has to have gone through the form handler first.
func postEditSubmitHandler(c *fiber.Ctx) error {
	postID, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return c.Status(400).SendString("")
	}
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Status(401).SendString("")
	}

	var author string
	if err := db.QueryRow(ctx, "SELECT author FROM posts WHERE id=$1", postID).Scan(&author); err != nil {
		return c.Status(404).SendString("")
	}
	if currentUser != author {
		return c.Status(403).SendString("Aap sirf apni hi post edit kar saktay hain")
	}

	newContent := strings.TrimSpace(c.FormValue("content"))
	if newContent == "" {
		return c.Status(400).SendString(renderPostEditFormHTML(postID, ""))
	}
	if r := []rune(newContent); len(r) > 5000 {
		newContent = string(r[:5000])
	}

	if _, err := db.Exec(ctx, "UPDATE posts SET content=$1 WHERE id=$2", newContent, postID); err != nil {
		log.Println("postEditSubmitHandler update error:", err)
		return c.Status(500).SendString("")
	}

	// Re-warm the cached preview so trending/feed views reflect the edit
	// immediately instead of waiting for a cold-cache read.
	CachePostMeta(postID, author, newContent, "", "", time.Now())
	// Any newly-added @mentions in the edited text still get notified.
	go ExtractAndNotifyMentions(postID, author, newContent)

	rendered, err := renderFullPostByID(postID, currentUser)
	if err != nil {
		return c.Status(500).SendString("")
	}
	c.Set("Content-Type", "text/html")
	return c.SendString(rendered)
}

// ─────────────────────────────────────────────────────────────────────────────
// PROFILE HELPERS
// ─────────────────────────────────────────────────────────────────────────────

func calculateStarSign(birth time.Time) string {
	md := birth.Format("01-02")
	switch {
	case md >= "03-21" && md <= "04-19":
		return "♈ Aries"
	case md >= "04-20" && md <= "05-20":
		return "♉ Taurus"
	case md >= "05-21" && md <= "06-20":
		return "♊ Gemini"
	case md >= "06-21" && md <= "07-22":
		return "♋ Cancer"
	case md >= "07-23" && md <= "08-22":
		return "♌ Leo"
	case md >= "08-23" && md <= "09-22":
		return "♍ Virgo"
	case md >= "09-23" && md <= "10-22":
		return "♎ Libra"
	case md >= "10-23" && md <= "11-21":
		return "♏ Scorpio"
	case md >= "11-22" && md <= "12-21":
		return "♐ Sagittarius"
	case md >= "12-22" || md <= "01-19":
		return "♑ Capricorn"
	case md >= "01-20" && md <= "02-18":
		return "♒ Aquarius"
	default:
		return "♓ Pisces"
	}
}

func getProfileImageHTML(path string) string {
	if path == "" {
		return `<div class="w-full h-full bg-gradient-to-br from-blue-500 to-emerald-500 flex items-center justify-center text-6xl">👤</div>`
	}
	return fmt.Sprintf(`<img src="%s" class="w-full h-full object-cover rounded-3xl" alt="Profile photo">`, path)
}

// ─────────────────────────────────────────────────────────────────────────────
// CONTENT PROCESSOR
// ─────────────────────────────────────────────────────────────────────────────

var (
	ytRegex   = regexp.MustCompile(`(?i)(?:https?://)?(?:www\.|m\.)?(?:youtube\.com/(?:watch\?(?:.*&)?v=|embed/|shorts/)|youtu\.be/)([a-zA-Z0-9_-]{11})`)
	vidRegex  = regexp.MustCompile(`(?i)(https?://[^\s<]+\.(?:mp4|webm|ogg|mov))`)
	imgRegex  = regexp.MustCompile(`(?i)(https?://[^\s<]+\.(?:jpg|jpeg|png|gif|webp|svg))`)
	linkRegex = regexp.MustCompile(`(?i)\b((?:https?://|www\.)[^\s<]+)\b`)
)

const postPreviewLimit = 600

// truncateForFeed cuts raw (pre-processed) content to postPreviewLimit runes,
// breaking on a word boundary where possible so words aren't sliced mid-way.
func truncateForFeed(raw string) (truncated string, wasTruncated bool) {
	r := []rune(raw)
	if len(r) <= postPreviewLimit {
		return raw, false
	}

	cut := r[:postPreviewLimit]

	// Try to back off to the last space so we don't cut a word in half.
	if idx := strings.LastIndexAny(string(cut), " \n\t"); idx > 0 {
		cut = []rune(string(cut)[:idx])
	}

	return string(cut), true
}

func processContent(content string, postID int) string {
	// 1. Escape only what's dangerous for HTML injection.
	// html.EscapeString also converts quotes to &#34;/&#39; which is
	// unnecessary here (content isn't placed inside an attribute) and
	// makes quotes render wrong to the user. Only <, >, & are unsafe
	// when content sits inside a text node.
	content = strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	).Replace(content)

	var embeds []string

	placeholder := func(html string) string {
		embeds = append(embeds, html)
		return fmt.Sprintf(" ___EMBED_%d___ ", len(embeds)-1)
	}

	// 2. YouTube embeds
	content = ytRegex.ReplaceAllStringFunc(content, func(match string) string {
		sub := ytRegex.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		return placeholder(fmt.Sprintf(
			`<div class="yt-container" style="width:100%%;max-width:500px;margin:12px 0;clear:both;"><div style="position:relative;padding-bottom:56.25%%;height:0;overflow:hidden;border-radius:16px;background:#000;border:1px solid #333;"><iframe style="position:absolute;top:0;left:0;width:100%%;height:100%%;border:0;" src="https://www.youtube-nocookie.com/embed/%s?rel=0&modestbranding=1&enablejsapi=1" allow="accelerometer;autoplay;clipboard-write;encrypted-media;gyroscope;picture-in-picture" allowfullscreen loading="lazy"></iframe></div></div>`,
			sub[1],
		))
	})

	// 3. Direct video files
	content = vidRegex.ReplaceAllStringFunc(content, func(match string) string {
		return placeholder(fmt.Sprintf(
			`<div style="width:100%%;max-width:500px;margin:12px 0;clear:both;"><video controls playsinline preload="metadata" style="max-width:100%%;height:auto;border-radius:16px;background:#000;border:1px solid #333;display:block;"><source src="%s">Your browser does not support the video tag.</video></div>`,
			match,
		))
	})

	// 4. Inline images
	content = imgRegex.ReplaceAllStringFunc(content, func(match string) string {
		return placeholder(fmt.Sprintf(
			`<img src="%s" style="max-width:100%%;height:auto;border-radius:16px;margin:10px 0;display:block;border:1px solid #333;" loading="lazy" alt="Post image">`,
			match,
		))
	})

	// 4b. @mentions -> profile links. Only usernames that actually exist
	// (checked against the same 90s author-meta cache renderPostHTML
	// already uses, so this costs nothing extra per render) become links;
	// anything else is left as plain text instead of a dead /profile/...
	// link. Inserted directly (not via the placeholder/embeds mechanism
	// used above) since this is inline text, not a block-level embed —
	// running it through placeholder() would pad it with the stray
	// leading/trailing spaces that mechanism adds for block content.
	metaMap := getCachedAuthorMetaMap()
	content = mentionRegex.ReplaceAllStringFunc(content, func(match string) string {
		sub := mentionRegex.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		uname := sub[1]
		if _, ok := metaMap[strings.ToLower(uname)]; !ok {
			return match
		}
		lead := ""
		if match[0] != '@' {
			lead = string(match[0])
		}
		return lead + fmt.Sprintf(
			`<a href="/profile/%s" style="color:#2563eb;font-weight:700;text-decoration:none;">@%s</a>`,
			uname, uname,
		)
	})

	// 5. Linkify remaining URLs
	content = linkRegex.ReplaceAllStringFunc(content, func(link string) string {
		if strings.Contains(link, "___EMBED_") {
			return link
		}
		href := link
		if !strings.HasPrefix(strings.ToLower(href), "http") {
			href = "https://" + href
		}
		return fmt.Sprintf(`<a href="%s" target="_blank" rel="noopener noreferrer" style="color:#3b82f6;text-decoration:underline;font-weight:600;">%s</a>`, href, link)
	})

	// 6. Line breaks
	content = strings.ReplaceAll(content, "\n", "<br>")

	// 7. Restore embed placeholders
	for i, block := range embeds {
		content = strings.Replace(content, fmt.Sprintf(" ___EMBED_%d___ ", i), block, 1)
	}

	return content
}

// ─────────────────────────────────────────────────────────────────────────────
// AUTHOR META CACHE — user_type + points rank, used by renderPostHTML.
// Was previously two live queries PER POST RENDERED, one of them a
// RANK() OVER() window function sorting the entire users table — meaning
// a single 25-post feed load triggered 25 full-table sorts. Now it's at
// most one full-table sort per authorMetaCacheTTL, shared across all posts,
// all viewers, all tabs.
// ─────────────────────────────────────────────────────────────────────────────

type authorMeta struct {
	UserType        string    `json:"user_type"`
	Rank            int64     `json:"rank"`
	ProfileImage    string    `json:"profile_image"`
	LastActive      time.Time `json:"last_active"`
	HasTrendingStar bool      `json:"has_trending_star"`
	DisplayName     string    `json:"display_name"`
	DisplayColor    string    `json:"display_color"`
	DisplayFont     string    `json:"display_font"`
	DisplayEffects  string    `json:"display_effects"`
}

const authorMetaCacheKey = "author_meta_cache"
const authorMetaCacheTTL = 90 * time.Second

func getCachedAuthorMeta(username string) authorMeta {
	m := getCachedAuthorMetaMap()
	if e, ok := m[username]; ok {
		return e
	}
	return authorMeta{}
}

func getCachedAuthorMetaMap() map[string]authorMeta {
	raw := sfGroup.do(authorMetaCacheKey, func() string {
		if val, ok := CacheGet(authorMetaCacheKey); ok && val != "" {
			return val
		}
		m := rebuildAuthorMetaMap()
		b, err := json.Marshal(m)
		if err != nil {
			return "{}"
		}
		return string(b)
	})
	var m map[string]authorMeta
	if json.Unmarshal([]byte(raw), &m) != nil {
		return map[string]authorMeta{}
	}
	return m
}

func rebuildAuthorMetaMap() map[string]authorMeta {
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Rank is computed only over non-admin users (admins often carry
	// inflated test-account scores that would otherwise distort every
	// normal user's rank), then left-joined back onto the full user list
	// so admins still get a cache entry (rank=0) for their profile
	// image/last-active/user_type lookups.
	rows, err := db.Query(rctx, `
		SELECT u.username, COALESCE(u.user_type,'normal'),
		       COALESCE(u.profile_image,''), COALESCE(u.last_active, u.created_at),
		       COALESCE(u.has_trending_star,false),
		       COALESCE(u.display_name,''), COALESCE(u.display_name_color,''), COALESCE(u.display_name_font,''),
		       COALESCE(u.display_name_effects,''),
		       COALESCE(r.rank, 0) AS rank
		FROM users u
		LEFT JOIN (
			SELECT username, RANK() OVER (ORDER BY points DESC) AS rank
			FROM users
			WHERE COALESCE(user_type,'') != 'admin' AND LOWER(username) != 'heera'
		) r ON r.username = u.username`)
	if err != nil {
		return map[string]authorMeta{}
	}
	defer rows.Close()

	m := make(map[string]authorMeta)
	for rows.Next() {
		var uname, utype, pimg, dname, dcolor, dfont, deffects string
		var lastActive time.Time
		var hasStar bool
		var rank int64
		if rows.Scan(&uname, &utype, &pimg, &lastActive, &hasStar, &dname, &dcolor, &dfont, &deffects, &rank) == nil {
			m[uname] = authorMeta{
				UserType: utype, Rank: rank, ProfileImage: pimg, LastActive: lastActive,
				HasTrendingStar: hasStar, DisplayName: dname, DisplayColor: dcolor, DisplayFont: dfont,
				DisplayEffects: deffects,
			}
		}
	}
	if b, err := json.Marshal(m); err == nil {
		CacheSet(authorMetaCacheKey, string(b), authorMetaCacheTTL)
	}
	return m
}
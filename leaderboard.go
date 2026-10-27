package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// ============================================================================
// leaderboard.go — unified leaderboard page with 7 categories:
//
//   scorers  — Top Scorers      (all-time points)
//   stars    — Top Stars        (weekly trending-creator leaderboard)
//   posters  — Top Posters      (most total posts)
//   liked    — Most Liked       (highest aggregate Taliyaan reactions)
//   chatters — Top Chatters     (chat messages + comments combined)
//   oldest   — Oldest Members   (longest-registered, active accounts)
//   newest   — Newest Members   (most recently approved accounts)
//
// Route: GET /leaderboard?view=<id>   (default: scorers)
//
// Tab switching and the help modal are both driven by a single delegated
// click listener bound once on `document` (see lbInitScript below) — NOT
// per-element onclick attributes — so they keep working even if this
// markup is later re-inserted via el.innerHTML by some other client-side
// loader on the site, which would otherwise silently strip out and never
// re-run any inline <script> tag (see the long comment in lbInitScript).
//
// Dependencies this file assumes already exist elsewhere and are
// UNCHANGED by this refactor:
//   getCurrentUser, db, Layout                    (main.go / db.go)
//   GetRankTheme, GetRankTitle, RankTheme          (rank_theme.go or similar)
//   TrendingStarHTML, GetCachedLeaderboard,
//   daysUntilWeeklyReset                           (trending_posts.go)
// ============================================================================

const leaderboardRowLimit = 50
const dbLeaderboardTimeout = 5 * time.Second

// ── TAB / CATEGORY DEFINITIONS ────────────────────────────────────────────

type lbTabDef struct {
	ID        string
	Label     string
	Icon      string
	Criteria  string // "how to get on this list"
	Mechanics string // "how the ranking works"
}

var lbTabs = []lbTabDef{
	{
		ID: "scorers", Label: "Top Scorers", Icon: "🥇",
		Criteria:  "Earn points by using the app — reacting to posts, commenting, sharing voice notes, and completing point-earning actions on the Earn Points page.",
		Mechanics: "Ranked by total lifetime points, highest first. Banned accounts and admins are excluded. Updated live.",
	},
	{
		ID: "stars", Label: "Top Stars", Icon: "⭐",
		Criteria:  "Get one or more of your posts trending during the current week — trending is driven by engagement (reactions, comments, shares) in a short window after posting.",
		Mechanics: "Ranked by how many of your posts have gone trending this week. Recalculated daily. Resets weekly (Pakistan time).",
	},
	{
		ID: "posters", Label: "Top Posters", Icon: "📝",
		Criteria:  "Just keep posting — every text, photo, video, or voice post you publish counts toward this list.",
		Mechanics: "Ranked by your total number of posts, all-time, highest first. Banned accounts are excluded.",
	},
	{
		ID: "liked", Label: "Most Liked", Icon: "❤️",
		Criteria:  "Post content people want to react to with Taliyaan (👏) — the more your posts resonate, the higher you climb.",
		Mechanics: "Ranked by the total number of Taliyaan (positive) reactions received across all of your posts, highest first. Banned accounts are excluded.",
	},
	{
		ID: "disliked", Label: "Most Disliked", Icon: "💢",
		Criteria:  "This one isn't a goal to chase — it tracks posts that got a lot of Chupair (negative) reactions.",
		Mechanics: "Ranked by the total number of Chupair (negative) reactions received across all of your posts, highest first. Banned accounts are excluded.",
	},
	{
		ID: "chatters", Label: "Top Chatters", Icon: "💬",
		Criteria:  "Be active in direct chats and in post/mehfil comment threads.",
		Mechanics: "Ranked by your combined total of direct chat messages sent plus comments posted, highest first. Banned accounts are excluded.",
	},
	{
		ID: "private_chats", Label: "Most Chats", Icon: "🗨️",
		Criteria:  "Message lots of different people one-on-one — this tracks how many distinct people you've chatted with, not how many messages you've sent.",
		Mechanics: "Ranked by your number of distinct 1-on-1 chat partners, highest first. Banned accounts are excluded.",
	},
	{
		ID: "oldest", Label: "Oldest Members", Icon: "🌱",
		Criteria:  "Nothing to do — just be one of our earliest members and keep your account in good standing.",
		Mechanics: "Ranked by account creation date, oldest first. Only currently active (not banned) accounts are shown.",
	},
	{
		ID: "newest", Label: "Newest Members", Icon: "🆕",
		Criteria:  "Sign up and get your account approved.",
		Mechanics: "Ranked by account creation date, newest first. Only approved, not-banned accounts are shown.",
	},
	{
		ID: "followers", Label: "Most Followed", Icon: "👥",
		Criteria:  "Build a following — the more people who follow your profile, the higher you rank.",
		Mechanics: "Ranked by your total number of followers, highest first. Banned accounts are excluded.",
	},
	{
		ID: "following", Label: "Most Following", Icon: "➕",
		Criteria:  "Follow lots of other people's profiles.",
		Mechanics: "Ranked by the total number of accounts you follow, highest first. Banned accounts are excluded.",
	},
	{
		ID: "mehfils", Label: "Top Mehfils", Icon: "🏛️",
		Criteria:  "Create and grow a mehfil — get people to join it.",
		Mechanics: "Ranked by member count, highest first, one row per mehfil (an admin with multiple mehfils can appear more than once). Kicked members don't count. Only mehfils owned by a not-banned admin are shown.",
	},
}

func lbTabByID(id string) (lbTabDef, bool) {
	for _, t := range lbTabs {
		if t.ID == id {
			return t, true
		}
	}
	return lbTabDef{}, false
}

// ── PAGE HANDLER ─────────────────────────────────────────────────────────

func LeaderboardPageHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Redirect("/login")
	}

	initialView := c.Query("view", "scorers")
	if _, ok := lbTabByID(initialView); !ok {
		initialView = "scorers"
	}

	var tabsHTML, panelsHTML strings.Builder
	for _, t := range lbTabs {
		activeTab, activePanel, ariaSel := "", "", "false"
		panelContent := `<div class="lb-loading">Loading…</div>`
		loadedAttr := ` data-lb-loaded="false"`
		if t.ID == initialView {
			activeTab, activePanel, ariaSel = " lb-tab-active", " lb-panel-active", "true"
			// This used to render EVERY tab's section on EVERY page load,
			// which meant 11 separate DB queries (several full-table
			// scans/joins) fired on every single visit regardless of
			// which tab was actually shown — that's what made the page
			// slow. Now only the tab visible on first paint is queried;
			// the rest lazy-load on demand (see lbInitScript's lbSwitch)
			// and stay cached in the DOM once fetched.
			panelContent = lbRenderSection(t.ID, currentUser)
			loadedAttr = ` data-lb-loaded="true"`
		}
		tabsHTML.WriteString(fmt.Sprintf(`
    <button id="lb-tab-%[1]s" type="button" role="tab" aria-selected="%[2]s"
      aria-controls="lb-panel-%[1]s" class="lb-tab%[3]s" data-lb-tab="%[1]s">
      <span class="lb-tab-icon">%[4]s</span>
      <span class="lb-tab-label">%[5]s</span>
      <span class="lb-help-btn" data-lb-help="%[1]s" role="button" tabindex="0"
            aria-label="How the %[5]s list works" title="How this list works">?</span>
    </button>`, t.ID, ariaSel, activeTab, t.Icon, t.Label))

		panelsHTML.WriteString(fmt.Sprintf(
			`<div id="lb-panel-%[1]s" role="tabpanel" aria-labelledby="lb-tab-%[1]s" class="lb-panel%[2]s"%[4]s>%[3]s</div>`,
			t.ID, activePanel, panelContent, loadedAttr))
	}

	helpJSON := lbHelpJSON()

	body := fmt.Sprintf(`
%s
<div style="max-width:640px;margin:0 auto;padding:10px 12px 40px;">

  <div style="display:flex;align-items:center;gap:6px;margin-bottom:10px;">
    <span style="font-size:15px;">🏆</span>
    <span style="font-size:12.5px;font-weight:800;color:var(--post-text);opacity:.75;">Leaderboard — tap ? on a tab for details</span>
  </div>

  <div role="tablist" aria-label="Leaderboard view" class="lb-tabbar" id="lb-tabbar">%s</div>

  %s
</div>

<!-- ══════ Help modal — one shared instance, populated from LB_HELP ══════ -->
<div class="lb-help-backdrop" id="lb-help-backdrop" aria-hidden="true">
  <div class="lb-help-modal" role="dialog" aria-modal="true" aria-labelledby="lb-help-title">
    <div class="lb-help-modal-header">
      <button type="button" class="lb-help-nav-btn" id="lb-help-back" aria-label="Back">←</button>
      <h3 id="lb-help-title" class="lb-help-title"></h3>
      <button type="button" class="lb-help-nav-btn" id="lb-help-close" aria-label="Close">✕</button>
    </div>
    <div class="lb-help-body">
      <div class="lb-help-section">
        <p class="lb-help-label">🎯 How to get on this list</p>
        <p class="lb-help-text" id="lb-help-criteria"></p>
      </div>
      <div class="lb-help-section">
        <p class="lb-help-label">📊 How the ranking works</p>
        <p class="lb-help-text" id="lb-help-mechanics"></p>
      </div>
    </div>
    <button type="button" class="lb-help-done" id="lb-help-done">Got it</button>
  </div>
</div>

<script id="lb-help-data-script">window.LB_HELP = %s;</script>

<img src="data:," alt="" style="display:none" width="0" height="0"
     onerror="this.remove(); (function(){
       if (window.__lbBound) return;
       ['lb-help-data-script','lb-boot-script'].forEach(function(sid){
         var s = document.getElementById(sid);
         if (!s) return;
         var ns = document.createElement('script');
         ns.textContent = s.textContent;
         document.body.appendChild(ns);
         document.body.removeChild(ns);
       });
     })();">

%s`,
		leaderboardCSS(),
		tabsHTML.String(),
		panelsHTML.String(),
		helpJSON,
		lbInitScript(),
	)

	return c.Type("html").SendString(Layout("🏆 Leaderboard", body, currentUser))
}

// leaderboardRedirect preserves old bookmarked/shared links.
func leaderboardRedirect(view string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		return c.Redirect("/leaderboard?view="+view, fiber.StatusMovedPermanently)
	}
}

// LeaderboardSectionHandler serves one tab's HTML fragment on demand — see
// lbInitScript's lbSwitch, which fetches this the first time a tab other
// than the initially-visible one is opened.
func LeaderboardSectionHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Status(fiber.StatusUnauthorized).SendString("")
	}
	id := c.Query("view", "scorers")
	if _, ok := lbTabByID(id); !ok {
		return c.Status(fiber.StatusBadRequest).SendString("")
	}
	return c.Type("html").SendString(lbRenderSection(id, currentUser))
}

const lbSectionCacheTTL = 30 * time.Second

// lbRenderSection dispatches to the right query/renderer for a tab id,
// cached briefly per (tab, viewer). Every renderer below runs a
// full-table scan/join — without this, switching tabs (or just
// refreshing) re-ran that query from scratch every time. singleflight
// collapses concurrent misses onto one query, same pattern already used
// by getCachedUserRanks/getCachedAuthorMetaMap elsewhere in this codebase.
func lbRenderSection(id, currentUser string) string {
	cacheKey := "lb_section:" + id + ":" + currentUser
	if val, ok := CacheGet(cacheKey); ok {
		return val
	}
	return sfGroup.do(cacheKey, func() string {
		html := lbRenderSectionUncached(id, currentUser)
		ttl := lbSectionCacheTTL
		if id == "balloons" || id == "cake" {
			ttl = 5 * time.Second
		}
		CacheSet(cacheKey, html, ttl)
		return html
	})
}

func lbRenderSectionUncached(id, currentUser string) string {
	switch id {
	case "scorers":
		return renderTopScorersSection(currentUser)
	case "stars":
		return renderTopStarsSection(currentUser)
	case "posters":
		return renderTopPostersSection(currentUser)
	case "liked":
		return renderMostLikedSection(currentUser)
	case "chatters":
		return renderTopChattersSection(currentUser)
	case "dm_chatters":
		return renderDMChattersSection(currentUser)
	case "private_chats":
		return renderPrivateChatsSection(currentUser)
	case "disliked":
		return renderMostDislikedSection(currentUser)
	case "followers":
		return renderMostFollowersSection(currentUser)
	case "following":
		return renderMostFollowingSection(currentUser)
	case "mehfils":
		return renderTopMehfilsSection(currentUser)
	case "cake":
		return renderCakeEatersSection(currentUser)
	case "balloons":
		return renderBalloonPoppersSection(currentUser)
	case "oldest":
		return renderOldestUsersSection(currentUser)
	case "newest":
		return renderNewestUsersSection(currentUser)
	default:
		return lbErrorState("Unknown leaderboard category.")
	}
}

// ── TOP SCORERS (all-time points) ───────────────────────────────────────

func renderTopScorersSection(currentUser string) string {
	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()

	rows, err := db.Query(rctx,
		`SELECT username, points FROM users
		 WHERE is_banned = false AND COALESCE(user_type, '') != 'admin' AND LOWER(username) != 'heera'
		 ORDER BY points DESC LIMIT $1`, leaderboardRowLimit)
	if err != nil {
		return lbErrorState("Couldn't load top scorers right now.")
	}
	defer rows.Close()

	var listHTML strings.Builder
	rank := 1
	for rows.Next() {
		var username string
		var points int
		if err := rows.Scan(&username, &points); err != nil {
			continue
		}

		creature, animClass := getCreatureAndAnim(rank)
		theme := GetRankTheme(rank)
		wrapperStyle := theme.LeaderboardStyle(rank == 1)

		listHTML.WriteString(fmt.Sprintf(`
            <div class="lb-row" style="%[1]s animation-delay:%[2].2fs;">
                <div style="display:flex;align-items:center;gap:14px;min-width:0;">
                    <span style="font-size:20px;font-weight:900;color:rgba(255,255,255,.65);min-width:34px;flex-shrink:0;">#%[3]d</span>
                    <div style="display:flex;flex-direction:column;min-width:0;">
                        <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;">
                            <a href="/profile/%[4]s" style="font-weight:900;font-size:15px;color:#fff;text-decoration:none;
                               text-shadow:0 1px 2px rgba(0,0,0,.25);overflow-wrap:anywhere;"
                               onmouseover="this.style.textDecoration='underline'"
                               onmouseout="this.style.textDecoration='none'">%[5]s</a>%[9]s
                            <span class="%[6]s" style="font-size:18px;cursor:default;">%[7]s</span>
                        </div>
                        <p style="margin:2px 0 0;font-size:9px;font-weight:900;letter-spacing:.14em;text-transform:uppercase;color:rgba(255,255,255,.75);">%[8]s</p>
                    </div>
                </div>
                <div style="text-align:right;flex-shrink:0;">
                    <p style="margin:0;font-size:17px;font-weight:900;color:#fff;">%[10]d</p>
                    <p style="margin:0;font-size:9px;font-weight:800;text-transform:uppercase;color:rgba(255,255,255,.7);">Points</p>
                </div>
            </div>`,
			wrapperStyle, float64(rank)*0.05, rank, username, username,
			animClass, creature, getRankTitle(rank), TrendingStarHTML(username), points,
		))
		rank++
	}

	if rank == 1 {
		return lbEmptyState("🥇", "No scorers yet", "Be the first to earn points and claim the top spot!")
	}
	return `<div class="lb-list">` + listHTML.String() + `</div>`
}

func getRankTitle(rank int) string { return GetRankTitle(rank) }

func getCreatureAndAnim(rank int) (string, string) {
	t := GetRankTheme(rank)
	if t.Hex == "" {
		return "⭐", ""
	}
	return t.Creature, t.AnimClass
}

// ── AVATARS ──────────────────────────────────────────────────────────
// Small circular profile-pic icon shown left of the username on every
// leaderboard row. Falls back to the same ui-avatars.com initials image
// used elsewhere on the site (see createOnlineSlider) when the user has
// no profile_image set.

func lbAvatarHTML(username, profileImage string) string {
	if strings.TrimSpace(profileImage) == "" {
		profileImage = "https://ui-avatars.com/api/?name=" + username + "&background=random"
	}
	return fmt.Sprintf(`
              <span class="lb-avatar-wrap">
                <img src="%s" alt="%s" class="lb-avatar-img" loading="lazy" decoding="async"
                     onerror="this.onerror=null;this.src='https://ui-avatars.com/api/?name='+encodeURIComponent(this.alt)+'&background=random'">
              </span>`, profileImage, username)
}

// lbFetchAvatars batch-loads profile_image for a set of usernames in one
// query, for renderers whose primary query doesn't already join users
// (e.g. Top Stars, which reads from the trending-cache board instead of
// hitting Postgres directly).
func lbFetchAvatars(usernames []string) map[string]string {
	out := make(map[string]string, len(usernames))
	if len(usernames) == 0 {
		return out
	}
	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()

	rows, err := db.Query(rctx,
		`SELECT username, COALESCE(profile_image,'') FROM users WHERE username = ANY($1)`, usernames)
	if err != nil {
		return out
	}
	defer rows.Close()

	for rows.Next() {
		var u, img string
		if rows.Scan(&u, &img) == nil {
			out[u] = img
		}
	}
	return out
}

// ── TOP STARS (weekly trending creators) ─────────────────────────────────

func renderTopStarsSection(currentUser string) string {
	board := GetCachedLeaderboard()

	if len(board) == 0 {
		return lbEmptyState("⭐", "No trending creators yet this week",
			"Get your posts trending to appear here! Resets in "+strconv.Itoa(daysUntilWeeklyReset())+" day(s).")
	}

	usernames := make([]string, 0, len(board))
	for _, u := range board {
		usernames = append(usernames, u.Username)
	}
	avatars := lbFetchAvatars(usernames)

	medals := []string{"🥇", "🥈", "🥉"}
	medalColors := []string{"#fbbf24", "#9ca3af", "#d97706"}

	var rowsHTML strings.Builder
	for i, u := range board {
		medal, color := "🔥", "#ef4444"
		if i < 3 {
			medal, color = medals[i], medalColors[i]
		}
		rowsHTML.WriteString(lbFlatRow(u.Rank, u.Username, avatars[u.Username], medal, color,
			fmt.Sprintf(`<span style="font-size:11px;font-weight:900;color:#ef4444;display:flex;align-items:center;gap:3px;flex-shrink:0;">🔥 %d</span>`, u.TrendingPostCount),
			TrendingStarHTML(u.Username), u.Username == currentUser))
	}

	footer := fmt.Sprintf(`<p style="text-align:center;font-size:11px;color:#9ca3af;margin:18px 0 4px;">
		Updated daily · Resets in %d day(s) (PKT)</p>`, daysUntilWeeklyReset())

	return `<div class="lb-list">` + rowsHTML.String() + `</div>` + footer
}

// ── TOP POSTERS ────────────────────────────────────────────────────────

func renderTopPostersSection(currentUser string) string {
	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()

	rows, err := db.Query(rctx, `
		SELECT p.author, COUNT(*) AS post_count, COALESCE(u.profile_image,'')
		FROM posts p
		JOIN users u ON u.username = p.author
		WHERE u.is_banned = false AND COALESCE(u.user_type,'') != 'admin'
		GROUP BY p.author, u.profile_image
		ORDER BY post_count DESC
		LIMIT $1`, leaderboardRowLimit)
	if err != nil {
		return lbErrorState("Couldn't load top posters right now.")
	}
	defer rows.Close()

	var listHTML strings.Builder
	rank := 1
	for rows.Next() {
		var username, profileImage string
		var count int
		if err := rows.Scan(&username, &count, &profileImage); err != nil {
			continue
		}
		listHTML.WriteString(lbFlatRow(rank, username, profileImage, "📝", "#2563eb",
			fmt.Sprintf(`<span style="font-size:11px;font-weight:900;color:#2563eb;flex-shrink:0;">%d posts</span>`, count),
			"", username == currentUser))
		rank++
	}

	if rank == 1 {
		return lbEmptyState("📝", "No posts yet", "Be the first to post and top this list!")
	}
	return `<div class="lb-list">` + listHTML.String() + `</div>`
}

// ── MOST LIKED (aggregate Taliyaan reactions) ─────────────────────────

func renderMostLikedSection(currentUser string) string {
	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()

	rows, err := db.Query(rctx, `
		SELECT p.author, COUNT(*) AS likes, COALESCE(u.profile_image,'')
		FROM interactions i
		JOIN posts p ON p.id = i.post_id
		JOIN users u ON u.username = p.author
		WHERE i.type NOT IN ('dislike','disgusted','angry','chupair')
		  AND u.is_banned = false AND COALESCE(u.user_type,'') != 'admin'
		GROUP BY p.author, u.profile_image
		ORDER BY likes DESC
		LIMIT $1`, leaderboardRowLimit)
	if err != nil {
		return lbErrorState("Couldn't load most-liked users right now.")
	}
	defer rows.Close()

	var listHTML strings.Builder
	rank := 1
	for rows.Next() {
		var username, profileImage string
		var likes int
		if err := rows.Scan(&username, &likes, &profileImage); err != nil {
			continue
		}
		listHTML.WriteString(lbFlatRow(rank, username, profileImage, "❤️", "#e11d48",
			fmt.Sprintf(`<span style="font-size:11px;font-weight:900;color:#e11d48;flex-shrink:0;">👏 %d Reactions</span>`, likes),
			"", username == currentUser))
		rank++
	}

	if rank == 1 {
		return lbEmptyState("❤️", "No reactions yet", "React to posts to help kick this list off!")
	}
	return `<div class="lb-list">` + listHTML.String() + `</div>`
}

// ── MOST DISLIKED (aggregate Chupair reactions) ────────────────────────

func renderMostDislikedSection(currentUser string) string {
	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()

	rows, err := db.Query(rctx, `
		SELECT p.author, COUNT(*) AS dislikes, COALESCE(u.profile_image,'')
		FROM interactions i
		JOIN posts p ON p.id = i.post_id
		JOIN users u ON u.username = p.author
		WHERE i.type = 'chupair' AND u.is_banned = false AND COALESCE(u.user_type,'') != 'admin'
		GROUP BY p.author, u.profile_image
		ORDER BY dislikes DESC
		LIMIT $1`, leaderboardRowLimit)
	if err != nil {
		return lbErrorState("Couldn't load most-disliked users right now.")
	}
	defer rows.Close()

	var listHTML strings.Builder
	rank := 1
	for rows.Next() {
		var username, profileImage string
		var dislikes int
		if err := rows.Scan(&username, &dislikes, &profileImage); err != nil {
			continue
		}
		listHTML.WriteString(lbFlatRow(rank, username, profileImage, "💢", "#dc2626",
			fmt.Sprintf(`<span style="font-size:11px;font-weight:900;color:#dc2626;flex-shrink:0;">👎 %d</span>`, dislikes),
			"", username == currentUser))
		rank++
	}

	if rank == 1 {
		return lbEmptyState("💢", "No dislikes yet", "Nothing to see here — that's a good thing!")
	}
	return `<div class="lb-list">` + listHTML.String() + `</div>`
}

// ── TOP CHATTERS (post comments/replies ONLY — no DM messages) ────────

func renderTopChattersSection(currentUser string) string {
	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()

	rows, err := db.Query(rctx, `
		SELECT c.author, COUNT(*) AS cnt, COALESCE(u.profile_image,'')
		FROM comments c
		JOIN users u ON u.username = c.author
		WHERE u.is_banned = false AND COALESCE(u.user_type,'') != 'admin'
		GROUP BY c.author, u.profile_image
		ORDER BY cnt DESC
		LIMIT $1`, leaderboardRowLimit)
	if err != nil {
		return lbErrorState("Couldn't load top chatters right now.")
	}
	defer rows.Close()

	var listHTML strings.Builder
	rank := 1
	for rows.Next() {
		var username, profileImage string
		var cnt int
		if err := rows.Scan(&username, &cnt, &profileImage); err != nil {
			continue
		}
		listHTML.WriteString(lbFlatRow(rank, username, profileImage, "💬", "#0891b2",
			fmt.Sprintf(`<span style="font-size:11px;font-weight:900;color:#0891b2;flex-shrink:0;">%d replies</span>`, cnt),
			"", username == currentUser))
		rank++
	}

	if rank == 1 {
		return lbEmptyState("💬", "No comments yet", "Reply to a post to appear here!")
	}
	return `<div class="lb-list">` + listHTML.String() + `</div>`
}

// ── TOP DM SENDERS (private 1-on-1 chat message volume) ────────────────

func renderDMChattersSection(currentUser string) string {
	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()

	rows, err := db.Query(rctx, `
		SELECT m.sender, COUNT(*) AS cnt, COALESCE(u.profile_image,'')
		FROM messages m
		JOIN users u ON u.username = m.sender
		WHERE u.is_banned = false AND COALESCE(u.user_type,'') != 'admin'
		GROUP BY m.sender, u.profile_image
		ORDER BY cnt DESC
		LIMIT $1`, leaderboardRowLimit)
	if err != nil {
		return lbErrorState("Couldn't load top DM senders right now.")
	}
	defer rows.Close()

	var listHTML strings.Builder
	rank := 1
	for rows.Next() {
		var username, profileImage string
		var cnt int
		if err := rows.Scan(&username, &cnt, &profileImage); err != nil {
			continue
		}
		listHTML.WriteString(lbFlatRow(rank, username, profileImage, "✉️", "#7c3aed",
			fmt.Sprintf(`<span style="font-size:11px;font-weight:900;color:#7c3aed;flex-shrink:0;">%d DMs</span>`, cnt),
			"", username == currentUser))
		rank++
	}

	if rank == 1 {
		return lbEmptyState("✉️", "No DMs yet", "Send a private message to appear here!")
	}
	return `<div class="lb-list">` + listHTML.String() + `</div>`
}

// ── MOST CHAT PARTNERS (distinct 1-on-1 conversation count) ───────────

func renderPrivateChatsSection(currentUser string) string {
	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()

	// Counts each user once per DISTINCT other party they've messaged
	// with or received a message from — a lot of messages with one
	// person only counts as one partner, so this measures breadth of
	// conversations, not raw message volume (that's dm_chatters above).
	rows, err := db.Query(rctx, `
		SELECT t.username, COUNT(DISTINCT t.partner) AS cnt, COALESCE(u.profile_image,'')
		FROM (
			SELECT sender AS username, recipient AS partner FROM messages
			UNION
			SELECT recipient AS username, sender AS partner FROM messages
		) t
		JOIN users u ON u.username = t.username
		WHERE u.is_banned = false AND COALESCE(u.user_type,'') != 'admin'
		GROUP BY t.username, u.profile_image
		ORDER BY cnt DESC
		LIMIT $1`, leaderboardRowLimit)
	if err != nil {
		return lbErrorState("Couldn't load most chat partners right now.")
	}
	defer rows.Close()

	var listHTML strings.Builder
	rank := 1
	for rows.Next() {
		var username, profileImage string
		var cnt int
		if err := rows.Scan(&username, &cnt, &profileImage); err != nil {
			continue
		}
		listHTML.WriteString(lbFlatRow(rank, username, profileImage, "🗨️", "#0d9488",
			fmt.Sprintf(`<span style="font-size:11px;font-weight:900;color:#0d9488;flex-shrink:0;">%d chats</span>`, cnt),
			"", username == currentUser))
		rank++
	}

	if rank == 1 {
		return lbEmptyState("🗨️", "No private chats yet", "Start a conversation with someone to appear here!")
	}
	return `<div class="lb-list">` + listHTML.String() + `</div>`
}

// ── OLDEST MEMBERS (active, not banned) ───────────────────────────────

func renderOldestUsersSection(currentUser string) string {
	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()

	rows, err := db.Query(rctx, `
		SELECT username, created_at, COALESCE(profile_image,'') FROM users
		WHERE is_banned = false AND COALESCE(user_type,'') != 'admin'
		ORDER BY created_at ASC
		LIMIT $1`, leaderboardRowLimit)
	if err != nil {
		return lbErrorState("Couldn't load oldest members right now.")
	}
	defer rows.Close()

	var listHTML strings.Builder
	rank := 1
	for rows.Next() {
		var username, profileImage string
		var createdAt time.Time
		if err := rows.Scan(&username, &createdAt, &profileImage); err != nil {
			continue
		}
		listHTML.WriteString(lbFlatRow(rank, username, profileImage, "🌱", "#16a34a",
			fmt.Sprintf(`<span style="font-size:11px;font-weight:900;color:#16a34a;flex-shrink:0;">since %s</span>`, createdAt.Format("Jan 2006")),
			"", username == currentUser))
		rank++
	}

	if rank == 1 {
		return lbEmptyState("🌱", "No members yet", "Check back once the community grows!")
	}
	return `<div class="lb-list">` + listHTML.String() + `</div>`
}

// ── NEWEST MEMBERS (approved, not banned) ─────────────────────────────

func renderNewestUsersSection(currentUser string) string {
	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()

	rows, err := db.Query(rctx, `
		SELECT username, created_at, COALESCE(profile_image,'') FROM users
		WHERE is_banned = false
		  AND COALESCE(account_status,'approved') = 'approved'
		  AND COALESCE(user_type,'') != 'admin'
		ORDER BY created_at DESC
		LIMIT $1`, leaderboardRowLimit)
	if err != nil {
		return lbErrorState("Couldn't load newest members right now.")
	}
	defer rows.Close()

	var listHTML strings.Builder
	rank := 1
	for rows.Next() {
		var username, profileImage string
		var createdAt time.Time
		if err := rows.Scan(&username, &createdAt, &profileImage); err != nil {
			continue
		}
		listHTML.WriteString(lbFlatRow(rank, username, profileImage, "🆕", "#8b5cf6",
			fmt.Sprintf(`<span style="font-size:11px;font-weight:900;color:#8b5cf6;flex-shrink:0;">joined %s</span>`, createdAt.Format("Jan 2, 2006")),
			"", username == currentUser))
		rank++
	}

	if rank == 1 {
		return lbEmptyState("🆕", "No new members yet", "New approved sign-ups will show up here!")
	}
	return `<div class="lb-list">` + listHTML.String() + `</div>`
}

// ── MOST FOLLOWED (highest follower count) ──────────────────────────────

func renderMostFollowersSection(currentUser string) string {
	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()

	rows, err := db.Query(rctx, `
		SELECT f.followee, COUNT(*) AS cnt, COALESCE(u.profile_image,'')
		FROM followers f
		JOIN users u ON u.username = f.followee
		WHERE u.is_banned = false AND COALESCE(u.user_type,'') != 'admin'
		GROUP BY f.followee, u.profile_image
		ORDER BY cnt DESC
		LIMIT $1`, leaderboardRowLimit)
	if err != nil {
		return lbErrorState("Couldn't load most-followed users right now.")
	}
	defer rows.Close()

	var listHTML strings.Builder
	rank := 1
	for rows.Next() {
		var username, profileImage string
		var cnt int
		if err := rows.Scan(&username, &cnt, &profileImage); err != nil {
			continue
		}
		listHTML.WriteString(lbFlatRow(rank, username, profileImage, "👥", "#0284c7",
			fmt.Sprintf(`<span style="font-size:11px;font-weight:900;color:#0284c7;flex-shrink:0;">%d followers</span>`, cnt),
			"", username == currentUser))
		rank++
	}

	if rank == 1 {
		return lbEmptyState("👥", "No followers yet", "Follow-worthy profiles will show up here!")
	}
	return `<div class="lb-list">` + listHTML.String() + `</div>`
}

// ── MOST FOLLOWING (highest count of accounts followed) ─────────────────

func renderMostFollowingSection(currentUser string) string {
	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()

	rows, err := db.Query(rctx, `
		SELECT f.follower, COUNT(*) AS cnt, COALESCE(u.profile_image,'')
		FROM followers f
		JOIN users u ON u.username = f.follower
		WHERE u.is_banned = false AND COALESCE(u.user_type,'') != 'admin'
		GROUP BY f.follower, u.profile_image
		ORDER BY cnt DESC
		LIMIT $1`, leaderboardRowLimit)
	if err != nil {
		return lbErrorState("Couldn't load most-following users right now.")
	}
	defer rows.Close()

	var listHTML strings.Builder
	rank := 1
	for rows.Next() {
		var username, profileImage string
		var cnt int
		if err := rows.Scan(&username, &cnt, &profileImage); err != nil {
			continue
		}
		listHTML.WriteString(lbFlatRow(rank, username, profileImage, "➕", "#059669",
			fmt.Sprintf(`<span style="font-size:11px;font-weight:900;color:#059669;flex-shrink:0;">following %d</span>`, cnt),
			"", username == currentUser))
		rank++
	}

	if rank == 1 {
		return lbEmptyState("➕", "Nobody's followed anyone yet", "Follow a profile to appear here!")
	}
	return `<div class="lb-list">` + listHTML.String() + `</div>`
}

// ── TOP MEHFILS (most members, one row per mehfil) ──────────────────────

func renderTopMehfilsSection(currentUser string) string {
	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()

	rows, err := db.Query(rctx, `
		SELECT m.id, m.topic, m.admin_username, COUNT(mm.username) AS member_count
		FROM mehfils m
		JOIN mehfil_members mm ON mm.mehfil_id = m.id AND mm.is_kicked = false
		JOIN users u ON u.username = m.admin_username
		WHERE u.is_banned = false AND COALESCE(u.user_type,'') != 'admin'
		GROUP BY m.id, m.topic, m.admin_username
		ORDER BY member_count DESC
		LIMIT $1`, leaderboardRowLimit)
	if err != nil {
		return lbErrorState("Couldn't load top mehfils right now.")
	}
	defer rows.Close()

	var listHTML strings.Builder
	rank := 1
	for rows.Next() {
		var mehfilID, memberCount int
		var topic, adminUsername string
		if err := rows.Scan(&mehfilID, &topic, &adminUsername, &memberCount); err != nil {
			continue
		}

		medal, color := "🏛️", "#9333ea"
		switch rank {
		case 1:
			medal, color = "🥇", "#fbbf24"
		case 2:
			medal, color = "🥈", "#9ca3af"
		case 3:
			medal, color = "🥉", "#d97706"
		}

		youBadge := ""
		if adminUsername == currentUser {
			youBadge = `<span style="font-size:9px;font-weight:900;color:#10b981;background:rgba(16,185,129,.14);
				padding:2px 7px;border-radius:999px;vertical-align:middle;margin-left:5px;">YOU</span>`
		}

		listHTML.WriteString(fmt.Sprintf(`
            <div class="lb-row-flat" style="align-items:flex-start;">
              <span style="font-size:22px;width:34px;text-align:center;flex-shrink:0;padding-top:1px;">%[1]s</span>
              <span style="font-size:15px;font-weight:900;color:%[2]s;min-width:30px;flex-shrink:0;padding-top:2px;">#%[3]d</span>
              <div style="flex:1;min-width:0;">
                <a href="/view/%[4]d" style="display:block;font-weight:900;font-size:14px;color:var(--post-text);
                   text-decoration:none;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">%[5]s</a>
                <a href="/profile/%[6]s" style="font-size:11px;font-weight:700;color:#9ca3af;text-decoration:none;">by @%[6]s</a>%[7]s
              </div>
              <span style="font-size:11px;font-weight:900;color:%[2]s;flex-shrink:0;padding-top:2px;">👥 %[8]d</span>
            </div>`,
			medal, color, rank, mehfilID, topic, adminUsername, youBadge, memberCount,
		))
		rank++
	}

	if rank == 1 {
		return lbEmptyState("🏛️", "No mehfils yet", "Create a mehfil and invite people to join!")
	}
	return `<div class="lb-list">` + listHTML.String() + `</div>`
}

// ── SHARED ROW / EMPTY / ERROR STATES ─────────────────────────────────

// lbFlatRow renders one consistent row used by every non-scorers tab.
// crownForOne shows a small 👑 over the avatar when rank == 1, since
// with 13 different lists a plain "#1" text badge got lost in the
// scroll — the crown makes the top spot pop at a glance in every tab.
func lbFlatRow(rank int, username, profileImage, medal, color, valueHTML, starHTML string, isYou bool) string {
	youBadge := ""
	if isYou {
		youBadge = `<span style="font-size:9px;font-weight:900;color:#10b981;background:rgba(16,185,129,.14);
			padding:2px 7px;border-radius:999px;vertical-align:middle;margin-left:5px;">YOU</span>`
	}
	crown := ""
	ringStyle := ""
	if rank == 1 {
		crown = `<span class="lb-crown">👑</span>`
		ringStyle = ` style="box-shadow:0 0 0 2px ` + color + `;"`
	}
	return fmt.Sprintf(`
            <div class="lb-row-flat lb-row-r%[9]d">
              <span class="lb-row-medal">%[1]s</span>
              <span class="lb-row-rank" style="color:%[2]s;">#%[3]d</span>
              <span class="lb-avatar-wrap"%[10]s>%[11]s%[4]s</span>
              <a href="/profile/%[5]s" class="lb-flat-link">@%[6]s%[7]s%[8]s</a>
              %[12]s
            </div>`,
		medal, color, rank, lbAvatarInner(username, profileImage), username, username, starHTML, youBadge,
		func() int {
			if rank <= 3 {
				return rank
			}
			return 0
		}(), ringStyle, crown, valueHTML,
	)
}

// lbAvatarInner is the bare <img> markup — used inside lbFlatRow's own
// .lb-avatar-wrap span (which needs the optional ring style attached to
// it directly), while lbAvatarHTML above wraps it for standalone use
// (e.g. the Top Mehfils and Top Scorers rows, which build their own
// layout).
func lbAvatarInner(username, profileImage string) string {
	if strings.TrimSpace(profileImage) == "" {
		profileImage = "https://ui-avatars.com/api/?name=" + username + "&background=random"
	}
	return fmt.Sprintf(`<img src="%s" alt="%s" class="lb-avatar-img" loading="lazy" decoding="async"
                     onerror="this.onerror=null;this.src='https://ui-avatars.com/api/?name='+encodeURIComponent(this.alt)+'&background=random'">`,
		profileImage, username)
}

func lbEmptyState(icon, title, subtitle string) string {
	return fmt.Sprintf(`
<div style="text-align:center;padding:64px 20px;">
  <div style="font-size:46px;margin-bottom:14px;">%s</div>
  <p style="font-weight:900;font-size:16px;color:var(--post-text);margin:0;">%s</p>
  <p style="font-size:13px;color:#9ca3af;margin:6px 0 0;">%s</p>
</div>`, icon, title, subtitle)
}

func lbErrorState(msg string) string {
	return fmt.Sprintf(`
<div style="text-align:center;padding:48px 20px;">
  <div style="font-size:40px;margin-bottom:10px;">⚠️</div>
  <p style="font-weight:800;font-size:14px;color:var(--post-text);margin:0;">%s</p>
</div>`, msg)
}

// ── HELP DATA (for the modal) ─────────────────────────────────────────

func lbHelpJSON() string {
	type helpEntry struct {
		Label     string `json:"label"`
		Icon      string `json:"icon"`
		Criteria  string `json:"criteria"`
		Mechanics string `json:"mechanics"`
	}
	m := make(map[string]helpEntry, len(lbTabs))
	for _, t := range lbTabs {
		m[t.ID] = helpEntry{Label: t.Label, Icon: t.Icon, Criteria: t.Criteria, Mechanics: t.Mechanics}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// ── STYLE (strict inline CSS, no external deps) ───────────────────────────

func leaderboardCSS() string {
	return `<style>
@keyframes lbSlideIn { from { opacity:0; transform:translateY(12px); } to { opacity:1; transform:translateY(0); } }
@keyframes lbFadeIn  { from { opacity:0; transform:translateY(6px);  } to { opacity:1; transform:translateY(0); } }
@keyframes lbModalIn { from { opacity:0; transform:translateY(14px) scale(.97); } to { opacity:1; transform:translateY(0) scale(1); } }

.lb-tabbar {
  display:flex; gap:6px; padding:6px; margin-bottom:22px;
  border-radius:16px; background:var(--post-bg); border:1px solid var(--post-border);
  overflow-x:auto; -webkit-overflow-scrolling:touch; scrollbar-width:none;
}
.lb-tabbar::-webkit-scrollbar { display:none; }

.lb-tab {
  position:relative;
  flex:0 0 auto; display:flex; align-items:center; justify-content:center; gap:6px;
  padding:11px 14px 11px 16px; border-radius:12px; border:none; cursor:pointer;
  font-family:inherit; font-size:12.5px; font-weight:900; letter-spacing:.01em;
  white-space:nowrap;
  background:transparent; color:var(--post-text); opacity:.6;
  transition:background .2s ease, color .2s ease, opacity .2s ease, transform .15s ease;
}
.lb-tab:hover { opacity:.9; }
.lb-tab:active { transform:scale(.97); }
.lb-tab:focus-visible { outline:2px solid #8b5cf6; outline-offset:2px; }
.lb-tab-icon { font-size:15px; line-height:1; }
.lb-tab-active {
  opacity:1; color:#fff;
  background:linear-gradient(135deg,#f59e0b,#ec4899,#8b5cf6);
  box-shadow:0 4px 14px rgba(139,92,246,.35);
}

.lb-help-btn {
  display:inline-flex; align-items:center; justify-content:center;
  width:16px; height:16px; margin-left:2px; border-radius:50%;
  font-size:10px; font-weight:900; line-height:1; cursor:pointer;
  background:rgba(255,255,255,.28); color:inherit; flex-shrink:0;
  transition:background .15s ease, transform .15s ease;
}
.lb-tab:not(.lb-tab-active) .lb-help-btn { background:rgba(139,92,246,.16); color:#8b5cf6; }
.lb-help-btn:hover { transform:scale(1.15); }
.lb-help-btn:focus-visible { outline:2px solid #8b5cf6; outline-offset:2px; }

.lb-panel { display:none; }
.lb-panel-active { display:block; animation:lbFadeIn .35s ease both; }
.lb-loading { text-align:center; padding:48px 20px; font-size:13px; font-weight:700; color:#9ca3af; }

.lb-list { display:flex; flex-direction:column; gap:10px; }

.lb-row {
  display:flex; align-items:center; justify-content:space-between;
  padding:16px 18px; border-radius:20px; border:2px solid transparent;
  box-shadow:0 4px 14px rgba(0,0,0,.12);
  animation:lbSlideIn .5s cubic-bezier(.16,1,.3,1) both;
  transition:transform .18s ease, box-shadow .18s ease;
}
.lb-row:hover { transform:translateY(-2px); box-shadow:0 8px 22px rgba(0,0,0,.18); }

.lb-row-flat {
  display:flex; align-items:center; gap:12px; padding:14px 18px;
  border-radius:16px; background:var(--post-bg); border:1px solid var(--post-border);
  transition:transform .15s ease, border-color .15s ease;
  animation:lbSlideIn .4s cubic-bezier(.16,1,.3,1) both;
}
.lb-row-flat:hover { transform:translateY(-1px); border-color:#8b5cf6; }

.lb-flat-link {
  font-weight:900; font-size:14px; color:var(--post-text); text-decoration:none;
  flex:1; min-width:0; overflow:visible; text-overflow:clip; white-space:normal;
  word-break:break-word; line-height:1.3;
}
.lb-flat-link:hover { text-decoration:underline; }

/* ── Help modal ── */
.lb-help-backdrop {
  display:none; position:fixed; inset:0; z-index:200;
  background:rgba(15,23,42,.55); backdrop-filter:blur(2px);
  align-items:flex-end; justify-content:center;
}
.lb-help-backdrop.lb-help-open { display:flex; }
.lb-help-modal {
  width:100%; max-width:460px; background:var(--post-bg); color:var(--post-text);
  border:1px solid var(--post-border); border-radius:22px 22px 0 0;
  padding:18px 18px 22px; box-shadow:0 -8px 32px rgba(0,0,0,.28);
  animation:lbModalIn .28s cubic-bezier(.16,1,.3,1) both;
}
@media (min-width:520px){
  .lb-help-backdrop { align-items:center; }
  .lb-help-modal { border-radius:22px; }
}
.lb-help-modal-header { display:flex; align-items:center; gap:10px; margin-bottom:12px; }
.lb-help-title { flex:1; margin:0; font-size:16px; font-weight:900; text-align:center; }
.lb-help-nav-btn {
  width:30px; height:30px; border-radius:50%; border:1px solid var(--post-border);
  background:transparent; color:var(--post-text); font-size:13px; cursor:pointer;
  display:flex; align-items:center; justify-content:center; flex-shrink:0;
  transition:background .15s ease;
}
.lb-help-nav-btn:hover { background:rgba(139,92,246,.12); }
.lb-help-section { margin-bottom:14px; }
.lb-help-label { margin:0 0 4px; font-size:11px; font-weight:900; letter-spacing:.04em; text-transform:uppercase; color:#8b5cf6; }
.lb-help-text { margin:0; font-size:13.5px; line-height:1.55; color:var(--post-text); opacity:.9; }
.lb-help-done {
  width:100%; margin-top:6px; padding:12px; border:none; border-radius:14px; cursor:pointer;
  font-size:13.5px; font-weight:900; color:#fff;
  background:linear-gradient(135deg,#f59e0b,#ec4899,#8b5cf6);
  box-shadow:0 4px 14px rgba(139,92,246,.35);
}

.lb-avatar-wrap {
  position:relative; flex-shrink:0; width:32px; height:32px; border-radius:50%;
  overflow:hidden; border:1.5px solid var(--post-border); background:var(--post-bg);
}
.lb-avatar-img { width:100%; height:100%; object-fit:cover; display:block; }
.lb-row-medal { font-size:20px; width:30px; text-align:center; flex-shrink:0; }
.lb-row-rank { font-size:14px; font-weight:900; min-width:26px; flex-shrink:0; }
.lb-row-r1 { background:linear-gradient(90deg,rgba(251,191,36,.12),transparent 60%); border-color:#fbbf24 !important; }
.lb-row-r2 { background:linear-gradient(90deg,rgba(156,163,175,.12),transparent 60%); }
.lb-row-r3 { background:linear-gradient(90deg,rgba(217,119,6,.10),transparent 60%); }
.lb-crown {
  position:absolute; top:-9px; left:50%; transform:translateX(-50%) rotate(-8deg);
  font-size:13px; line-height:1; pointer-events:none;
  filter:drop-shadow(0 1px 2px rgba(0,0,0,.35));
}

@media (prefers-reduced-motion: reduce) {
  .lb-row, .lb-row-flat, .lb-panel-active, .lb-help-modal { animation:none !important; }
}
</style>`
}

// ── SCRIPT (delegated events — robust to non-full-page re-insertion) ──────

func lbInitScript() string {
	return `<script id="lb-boot-script">
(function(){
  // Guard: if this markup is ever re-inserted (e.g. via someone else's
  // el.innerHTML = fetchedHTML pattern elsewhere on the site) this
  // <script> block runs again but must NOT attach a second set of
  // listeners on document — that would double-fire every click.
  if (window.__lbBound) return;
  window.__lbBound = true;

  // Delegated on document (not per-button onclick) so tab switching and
  // the help modal keep working even if a future code path swaps this
  // page's content via innerHTML instead of a full navigation — inline
  // <script> tags inserted that way never execute, but a listener
  // already bound to document from the real initial load stays alive.
  document.addEventListener('click', function(e){
    var helpBtn = e.target.closest('[data-lb-help]');
    if (helpBtn) {
      e.preventDefault();
      e.stopPropagation();
      lbOpenHelp(helpBtn.getAttribute('data-lb-help'));
      return;
    }
    var tabBtn = e.target.closest('[data-lb-tab]');
    if (tabBtn) {
      lbSwitch(tabBtn.getAttribute('data-lb-tab'));
      return;
    }
    if (e.target.id === 'lb-help-close' || e.target.id === 'lb-help-done' || e.target.id === 'lb-help-back') {
      lbCloseHelp();
      return;
    }
    if (e.target.id === 'lb-help-backdrop') {
      lbCloseHelp();
    }
  });

  document.addEventListener('keydown', function(e){
    var helpBtn = e.target.closest('[data-lb-help]');
    if (helpBtn && (e.key === 'Enter' || e.key === ' ')) {
      e.preventDefault();
      lbOpenHelp(helpBtn.getAttribute('data-lb-help'));
    }
    if (e.key === 'Escape') lbCloseHelp();
  });

  function lbSwitch(view){
    var tabs = document.querySelectorAll('.lb-tab[data-lb-tab]');
    tabs.forEach(function(btn){
      var id = btn.getAttribute('data-lb-tab');
      var isActive = id === view;
      btn.classList.toggle('lb-tab-active', isActive);
      btn.setAttribute('aria-selected', isActive ? 'true' : 'false');
      var panel = document.getElementById('lb-panel-' + id);
      if (panel) panel.classList.toggle('lb-panel-active', isActive);
    });
    var activeBtn = document.getElementById('lb-tab-' + view);
    if (activeBtn && activeBtn.scrollIntoView) {
      activeBtn.scrollIntoView({ behavior:'smooth', inline:'center', block:'nearest' });
    }
    if (window.history && window.history.replaceState) {
      var url = new URL(window.location.href);
      url.searchParams.set('view', view);
      window.history.replaceState(null, '', url.toString());
    }

    // Every panel except the one shown on first paint starts empty
    // (data-lb-loaded="false"). Fetch and fill it the first time it's
    // opened, then leave the result cached in the DOM so switching back
    // to it later is instant with no re-query.
    var panel = document.getElementById('lb-panel-' + view);
    if (panel && (panel.getAttribute('data-lb-loaded') !== 'true' || view === 'balloons' || view === 'cake')) {
      panel.setAttribute('data-lb-loaded', 'true');
      fetch('/leaderboard/section?view=' + encodeURIComponent(view))
        .then(function(r){ return r.text(); })
        .then(function(html){ panel.innerHTML = html; })
        .catch(function(){
          panel.innerHTML = '<div style="text-align:center;padding:40px 20px;color:#ef4444;font-size:13px;">Could not load this list — try again.</div>';
          panel.setAttribute('data-lb-loaded', 'false');
        });
    }
  }

  function lbOpenHelp(id){
    var data = (window.LB_HELP || {})[id];
    if (!data) return;
    document.getElementById('lb-help-title').textContent = data.icon + ' ' + data.label;
    document.getElementById('lb-help-criteria').textContent = data.criteria;
    document.getElementById('lb-help-mechanics').textContent = data.mechanics;
    var backdrop = document.getElementById('lb-help-backdrop');
    backdrop.classList.add('lb-help-open');
    backdrop.setAttribute('aria-hidden', 'false');
  }

  function lbCloseHelp(){
    var backdrop = document.getElementById('lb-help-backdrop');
    backdrop.classList.remove('lb-help-open');
    backdrop.setAttribute('aria-hidden', 'true');
  }

  window.lbSwitch = lbSwitch; // kept for any external callers/back-compat
})();
</script>`
}
func init() {
	lbTabs = append(lbTabs,
		lbTabDef{
			ID: "cake", Label: "Cake Eaters", Icon: "🍰",
			Criteria:  "Eat slices of the birthday cake — one slice per user every 2 hours while the cake is open.",
			Mechanics: "Ranked by total slices eaten, highest first. Also shows points won. Banned accounts and admins are excluded.",
		},
		lbTabDef{
			ID: "balloons", Label: "Balloon Poppers", Icon: "🎈",
			Criteria:  "Tap the floating balloons to pop them during the celebration.",
			Mechanics: "Ranked by total balloons popped, highest first. Banned accounts and admins are excluded.",
		},
	)
}

func renderCakeEatersSection(currentUser string) string {
	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()
	rows, err := db.Query(rctx, `
		SELECT e.username, e.total_slices, e.total_points, COALESCE(u.profile_image,'')
		FROM cake_eaters e JOIN users u ON u.username = e.username
		WHERE u.is_banned = false AND COALESCE(u.user_type,'') != 'admin'
		ORDER BY e.total_slices DESC, e.last_eaten ASC
		LIMIT $1`, leaderboardRowLimit)
	if err != nil {
		return lbErrorState("Couldn't load cake eaters right now.")
	}
	defer rows.Close()

	var listHTML strings.Builder
	rank := 1
	for rows.Next() {
		var username, img string
		var slices int
		var pts int64
		if err := rows.Scan(&username, &slices, &pts, &img); err != nil {
			continue
		}
		listHTML.WriteString(lbFlatRow(rank, username, img, "🍰", "#ec4899",
			fmt.Sprintf(`<span style="font-size:11px;font-weight:900;color:#ec4899;flex-shrink:0;">🍰 %d · ⭐ %d</span>`, slices, pts),
			"", username == currentUser))
		rank++
	}
	if rank == 1 {
		return lbEmptyState("🍰", "No slices eaten yet", "Eat a slice of the birthday cake to appear here!")
	}
	return `<div class="lb-list">` + listHTML.String() + `</div>`
}

func lbBalloonMineCard(user string) string {
	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()
	var n, rank int
	err := db.QueryRow(rctx, `
		SELECT b.balloons_popped,
		       (SELECT COUNT(*)+1 FROM bday_stats x JOIN users xu ON xu.username = x.username
		        WHERE xu.is_banned = false AND x.balloons_popped > b.balloons_popped)
		FROM bday_stats b WHERE b.username = $1`, user).Scan(&n, &rank)

	inner := `<span style="font-size:12.5px;font-weight:800;">🎈 You haven't popped any balloons yet. Tap the falling balloons on any page!</span>`
	if err == nil && n > 0 {
		inner = fmt.Sprintf(`
		<div style="display:flex;align-items:center;justify-content:space-between;gap:10px;width:100%%;">
		  <div>
		    <div style="font-size:10px;font-weight:900;letter-spacing:.12em;text-transform:uppercase;opacity:.85;">Your score</div>
		    <div style="font-size:22px;font-weight:900;line-height:1.1;">🎈 %d popped</div>
		  </div>
		  <div style="text-align:right;">
		    <div style="font-size:10px;font-weight:900;letter-spacing:.12em;text-transform:uppercase;opacity:.85;">Your rank</div>
		    <div style="font-size:22px;font-weight:900;line-height:1.1;">#%d</div>
		  </div>
		</div>`, n, rank)
	}
	return `<div style="margin-bottom:12px;padding:14px 16px;border-radius:18px;color:#fff;display:flex;align-items:center;
		background:linear-gradient(135deg,#f43f5e,#ec4899 55%,#8b5cf6);box-shadow:0 6px 18px rgba(236,72,153,.35);">` + inner + `</div>`
}

func renderBalloonPoppersSection(currentUser string) string {
	mine := lbBalloonMineCard(currentUser)

	rctx, cancel := context.WithTimeout(context.Background(), dbLeaderboardTimeout)
	defer cancel()
	rows, err := db.Query(rctx, `
		SELECT b.username, b.balloons_popped, COALESCE(u.profile_image,'')
		FROM bday_stats b JOIN users u ON u.username = b.username
		WHERE u.is_banned = false AND b.balloons_popped > 0
		ORDER BY b.balloons_popped DESC, b.updated_at ASC
		LIMIT $1`, leaderboardRowLimit)
	if err != nil {
		return mine + lbErrorState("Couldn't load balloon poppers right now.")
	}
	defer rows.Close()

	var listHTML strings.Builder
	rank := 1
	for rows.Next() {
		var username, img string
		var n int
		if err := rows.Scan(&username, &n, &img); err != nil {
			continue
		}
		listHTML.WriteString(lbFlatRow(rank, username, img, "🎈", "#f43f5e",
			fmt.Sprintf(`<span style="font-size:11px;font-weight:900;color:#f43f5e;flex-shrink:0;">🎈 %d popped</span>`, n),
			"", username == currentUser))
		rank++
	}
	if rank == 1 {
		return mine + lbEmptyState("🎈", "No balloons popped yet", "Pop the floating balloons to get on this list!")
	}
	return mine + `<div class="lb-list">` + listHTML.String() + `</div>`
}
package main

import (
	"fmt"
	"html"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// 1. UNIQUE NAMES FOR SEARCH HELPERS (To avoid errors with TopScorer page)
func getLegendIcon(rank int) (string, string) {
	switch rank {
	case 1:
		return "🦁", "anim-bounce"
	case 2:
		return "🐯", "anim-wiggle"
	case 3:
		return "🐆", "anim-peek"
	case 4:
		return "🐺", "anim-bounce"
	case 5:
		return "🦅", "anim-float"
	case 6:
		return "🐻", "anim-wiggle"
	case 7:
		return "🦈", "anim-float"
	case 8:
		return "🦍", "anim-bounce"
	case 9:
		return "🐲", "anim-peek"
	case 10:
		return "🦊", "anim-wiggle"
	default:
		return "", ""
	}
}

func getLegendStyle(rank int) string {
	base := "display:flex; align-items:center; justify-content:between; padding:20px; border-radius:28px; margin-bottom:15px; text-decoration:none; box-shadow:0 10px 15px -3px rgba(0,0,0,0.1); border:2px solid "
	switch rank {
	case 1:
		return base + "#fef3c7; background:linear-gradient(to right, #f59e0b, #fbbf24); transform:scale(1.03);"
	case 2:
		return base + "#e2e8f0; background:linear-gradient(to right, #94a3b8, #cbd5e1);"
	case 3:
		return base + "#ffedd5; background:linear-gradient(to right, #f97316, #fb923c);"
	case 4:
		return base + "#e0e7ff; background:linear-gradient(to right, #4f46e5, #7c3aed);"
	case 5:
		return base + "#e0f2fe; background:linear-gradient(to right, #2563eb, #06b6d4);"
	case 6:
		return base + "#d1fae5; background:linear-gradient(to right, #059669, #14b8a6);"
	case 7:
		return base + "#ecfccb; background:linear-gradient(to right, #16a34a, #84cc16);"
	case 8:
		return base + "#f5f5f4; background:linear-gradient(to right, #57534e, #78716c);"
	case 9:
		return base + "#ffe4e6; background:linear-gradient(to right, #e11d48, #db2777);"
	case 10:
		return base + "#fae8ff; background:linear-gradient(to right, #c026d3, #9333ea);"
	default:
		return "display:flex; align-items:center; gap:20px; padding:20px; background:white; border:2px solid #f1f5f9; border-radius:28px; text-decoration:none; margin-bottom:15px;"
	}
}

func renderSearchUserCard(username string, points int, profileImage string, rank int, isAdmin bool) string {
	// Admins are searchable but never ranked — force them onto the plain
	// default card regardless of where they'd fall by points.
	effectiveRank := rank
	if isAdmin {
		effectiveRank = 0
	}

	creature, animClass := getLegendIcon(effectiveRank)
	styles := getLegendStyle(effectiveRank)

	nameColor := "#0f172a"
	ptsColor := "#10b981"
	if effectiveRank <= 10 && effectiveRank >= 1 {
		nameColor = "#ffffff"
		ptsColor = "#ffffffcc"
	}

	avatar := ""
	if profileImage != "" {
		avatar = fmt.Sprintf(`<img src="%s" style="width:100%%; height:100%%; object-fit:cover;">`, profileImage)
	} else {
		initial := "👤"
		if len(username) > 0 {
			initial = string(username[0])
		}
		avatar = fmt.Sprintf(`<div style="width:100%%; height:100%%; background:rgba(255,255,255,0.2); display:flex; align-items:center; justify-content:center; color:white; font-size:24px; font-weight:bold; text-transform:uppercase;">%s</div>`, initial)
	}

	return fmt.Sprintf(`
		<a href="/profile/%[1]s" hx-boost="true" style="%[2]s">
			<div style="width:65px; height:65px; border-radius:22px; overflow:hidden; flex-shrink:0; background:rgba(0,0,0,0.05); border:1px solid rgba(255,255,255,0.2);">%[3]s</div>
			<div style="flex-grow:1; min-width:0; margin-left:15px;">
				<div style="font-weight:900; color:%[4]s; font-size:19px; display:flex; align-items:center; gap:8px;">
					<span class="truncate">%[1]s</span>
					<span class="text-2xl %[5]s">%[6]s</span>
				</div>
				<div style="font-size:13px; color:%[7]s; font-weight:800; letter-spacing:0.5px;">⭐ %[8]d POINTS</div>
			</div>
			<div style="color:%[4]s; opacity:0.5; font-size:20px;">❯</div>
		</a>`, username, styles, avatar, nameColor, animClass, creature, ptsColor, points)
}

// renderSearchPrompt is the default state — no one is listed until a
// query is typed, so there's nothing to browse and nothing to paginate.
func renderSearchPrompt() string {
	return `
		<div style="text-align:center; padding:70px 20px; color:#94a3b8;">
			<div style="font-size:42px; margin-bottom:12px;">🔍</div>
			<div style="font-weight:800; font-size:16px; color:#475569;">Search for someone</div>
			<div style="font-size:13px; margin-top:6px;">Start typing a username above to find people.</div>
		</div>`
}

func renderSearchNoResults(query string) string {
	return fmt.Sprintf(`
		<div style="text-align:center; padding:70px 20px; color:#94a3b8;">
			<div style="font-size:42px; margin-bottom:12px;">🔍</div>
			<div style="font-weight:800; font-size:16px; color:#475569;">No users found for "%s"</div>
			<div style="font-size:13px; margin-top:6px;">Try a different spelling or username.</div>
		</div>`, html.EscapeString(query))
}

// renderSearchResultsHTML runs the lookup for one query and renders the
// matching cards, or the prompt/no-results state. An empty query (first
// load, or the ✕ clear button) always returns the prompt — the full user
// directory is never dumped into the page.
func renderSearchResultsHTML(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return renderSearchPrompt()
	}

	// Admins ARE findable by name here, just never shown with rank styling
	// (renderSearchUserCard forces effectiveRank=0 for them).
	rows, err := db.Query(ctx,
		"SELECT username, points, profile_image, COALESCE(user_type, '') FROM users WHERE is_banned = false AND username ILIKE $1 ORDER BY points DESC LIMIT 50",
		"%"+query+"%")
	if err != nil {
		return renderSearchNoResults(query)
	}
	defer rows.Close()

	var listHTML strings.Builder
	rank := 1
	for rows.Next() {
		var uName, pImg, uType string
		var pts int
		if rows.Scan(&uName, &pts, &pImg, &uType) != nil {
			continue
		}
		listHTML.WriteString(renderSearchUserCard(uName, pts, pImg, rank, uType == "admin"))
		rank++
	}

	if rank == 1 {
		return renderSearchNoResults(query)
	}
	return listHTML.String()
}

func SearchPage(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	searchQuery := c.Query("q")
	resultsHTML := renderSearchResultsHTML(searchQuery)

	// Every keystroke (and the ✕ clear button, which re-triggers with an
	// empty value) just swaps the results area — never the whole page.
	if c.Get("HX-Request") == "true" {
		return c.SendString(fmt.Sprintf(`<div id="user-results" style="min-height:400px;">%s</div>`, resultsHTML))
	}

	var totalUsers int
	db.QueryRow(ctx, "SELECT COUNT(*) FROM users WHERE is_banned = false AND COALESCE(user_type, '') != 'admin'").Scan(&totalUsers)

	clearDisplay := "none"
	if strings.TrimSpace(searchQuery) != "" {
		clearDisplay = "flex"
	}

	fullBody := fmt.Sprintf(`
		<style>
			@keyframes bounceCustom { 0%%, 100%% { transform: translateY(0); } 50%% { transform: translateY(-8px); } }
			@keyframes wiggle { 0%%, 100%% { transform: rotate(-8deg); } 50%% { transform: rotate(8deg); } }
			@keyframes peek { 0%%, 100%% { transform: scale(1); } 50%% { transform: scale(1.15) translateY(-3px); } }
			@keyframes float { 0%%, 100%% { transform: translateY(0) translateX(0); } 50%% { transform: translateY(-4px) translateX(4px); } }
			@keyframes spin { to { transform: translateY(-50%%) rotate(360deg); } }
			.anim-bounce { animation: bounceCustom 1s infinite; }
			.anim-wiggle { animation: wiggle 0.8s infinite ease-in-out; }
			.anim-peek { animation: peek 1.2s infinite ease-in-out; }
			.anim-float { animation: float 2s infinite ease-in-out; }

			.search-wrap { width:100%%; max-width:600px; margin:0 auto; padding:15px; box-sizing:border-box; }
			.search-input {
				width:100%%; box-sizing:border-box; padding:20px 58px 20px 52px; border-radius:22px;
				border:3px solid #f1f5f9; font-size:16px; font-weight:700; outline:none;
				box-shadow:0 15px 30px -10px rgba(0,0,0,0.1); background:white;
				transition:border-color 0.15s ease, box-shadow 0.15s ease;
			}
			.search-input:focus { border-color:#0f172a; box-shadow:0 15px 30px -10px rgba(15,23,42,0.25); }
			.search-icon { position:absolute; left:20px; top:50%%; transform:translateY(-50%%); font-size:18px; opacity:0.4; pointer-events:none; }
			.search-spinner {
				position:absolute; right:20px; top:50%%; width:20px; height:20px;
				border:3px solid #e2e8f0; border-top-color:#0f172a; border-radius:50%%;
				opacity:0; transition:opacity 150ms ease; animation:spin 0.6s linear infinite;
			}
			.htmx-request .search-spinner { opacity:1; }
			.search-clear {
				display:none; position:absolute; right:18px; top:50%%; transform:translateY(-50%%);
				width:26px; height:26px; border-radius:50%%; border:none; background:#f1f5f9;
				color:#64748b; font-weight:900; cursor:pointer; align-items:center; justify-content:center; font-size:13px;
			}

			/* Tablet and up: roomier layout, no need to cram like a phone */
			@media (min-width: 768px) {
				.search-wrap { max-width:680px; padding:20px; }
				.search-input { padding:24px 65px 24px 58px; font-size:18px; }
			}
			/* Small phones: tighten padding/radius so it doesn't feel oversized */
			@media (max-width: 380px) {
				.search-input { padding:16px 48px 16px 44px; font-size:15px; border-radius:16px; }
			}
		</style>
		<div class="search-wrap">

			<div style="background:#0f172a; border-radius:35px; padding:35px 15px; color:white; margin-bottom:25px; text-align:center;">
				<div style="font-size:10px; text-transform:uppercase; letter-spacing:3px; opacity:0.6; font-weight:900; margin-bottom:5px;">Dostana Registry</div>
				<div style="font-size:48px; font-weight:900; letter-spacing:-1px;">%d</div>
				<div style="font-size:13px; color:#38bdf8; font-weight:700;">Registered Members</div>
			</div>

			<div style="position:sticky; top:10px; z-index:100; margin-bottom:30px; position:relative;">
				<span class="search-icon">🔍</span>
				<input type="text" name="q" id="search-input" class="search-input" value="%s"
					placeholder="Search by name..." autocomplete="off" enterkeyhint="search"
					hx-get="/search" hx-trigger="keyup changed delay:250ms, search"
					hx-target="#search-area" hx-indicator="closest div"
					oninput="document.getElementById('search-clear').style.display = this.value ? 'flex' : 'none';">
				<div class="search-spinner"></div>
				<button type="button" id="search-clear" class="search-clear" style="display:%s;"
					onclick="const i=document.getElementById('search-input'); i.value=''; i.dispatchEvent(new Event('keyup')); this.style.display='none'; i.focus();">✕</button>
			</div>

			<div id="search-area">
				<div id="user-results" style="min-height:400px;">%s</div>
			</div>
		</div>`, totalUsers, html.EscapeString(searchQuery), clearDisplay, resultsHTML)

	return c.Type("html").SendString(Layout("Search", fullBody, currentUser))
}

package main

import (
	"context"
    "log"
	"fmt"
	"strings"
	"time"
	"mysocialmediawebsite/pkg/platform"
	"github.com/gofiber/fiber/v2"
	
)

func getOnlineUsers() []string {
	ctx, cancel := context.WithTimeout(platform.Ctx, 2*time.Second)
	defer cancel()

	threshold := time.Now().Unix() - 60 // 1 minute inactivity timeout

	// Pipeline: Cleanup and Fetch in one network round-trip
	pipe := platform.RDB.Pipeline()
	pipe.ZRemRangeByScore(ctx, "active_sessions", "-inf", fmt.Sprintf("%d", threshold))
	// Fetch top 30 most recently active users
	cmd := pipe.ZRevRange(ctx, "active_sessions", 0, 29)

	_, err := pipe.Exec(ctx)
	if err != nil {
		log.Printf("Redis error (getOnlineUsers): %v", err)
		return []string{}
	}

	return cmd.Val()
}

func createOnlineSlider(onlineUsers []string) string {
	if len(onlineUsers) == 0 {
		return `<div class="text-gray-400 italic text-sm py-6 w-full text-center">No friends online right now...</div>`
	}

	// Industry Standard: Hydrate Redis IDs with SQL Data
	// Using ILIKE ANY for case-insensitive matching
	rows, err := db.Query(ctx, `
		SELECT username, COALESCE(points, 0), COALESCE(profile_image, '') 
		FROM users 
		WHERE username ILIKE ANY($1)
		ORDER BY points DESC 
		LIMIT 20`, onlineUsers)

	if err != nil {
		log.Printf("SQL Error (OnlineSlider): %v", err)
		return `<div class="text-red-400 text-sm py-4 text-center">Connection trouble...</div>`
	}
	defer rows.Close()

	var html strings.Builder
	hasRows := false

	for rows.Next() {
		var username, profileImg string
		var points int
		if err := rows.Scan(&username, &points, &profileImg); err != nil {
			continue
		}
		hasRows = true

		// Fallback for empty profile images
		displayImg := profileImg
		if displayImg == "" {
			displayImg = "/static/img/default-avatar.png" 
		}

		// Building the UI with strings.Builder (Memory Efficient)
		fmt.Fprintf(&html, `
			<a href="/profile/%s" class="flex-shrink-0 snap-center text-center w-20 group">
				<div class="w-16 h-16 mx-auto rounded-3xl shadow-md relative overflow-hidden transition-transform group-hover:scale-105 active:scale-95 bg-gray-100 dark:bg-gray-800">
					<img src="%s" 
						 class="w-full h-full object-cover rounded-3xl" 
						 loading="lazy" 
						 onerror="this.src='/static/img/default-avatar.png'"
						 alt="%s">
					<div class="absolute -bottom-0.5 -right-0.5 w-5 h-5 bg-emerald-500 border-2 border-white dark:border-gray-900 rounded-full"></div>
				</div>
				<p class="text-sm font-semibold mt-2 truncate w-20 text-gray-800 dark:text-gray-200">@%s</p>
				<p class="text-[10px] text-emerald-600 dark:text-emerald-400 font-bold uppercase tracking-tighter">%d PTS</p>
			</a>`, username, displayImg, username, username, points)
	}

	if !hasRows {
		return `<div class="text-gray-400 italic text-sm py-6 w-full text-center">No friends online right now...</div>`
	}

	return html.String()
}

func onlineSliderHandler(c *fiber.Ctx) error {
	// Disable browser caching so the status is always fresh
	c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	
	// Data flow: Redis IDs -> SQL Hydration -> HTML String
	return c.SendString(createOnlineSlider(getOnlineUsers()))
}



// HTMX Handler: Feed
func feedHandler(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	currentUser := c.Cookies("user")

	cacheKey := fmt.Sprintf("feed:u:%s:p:%d", currentUser, page)

	cachedHTML, err := platform.RDB.Get(platform.Ctx, cacheKey).Result()
	if err == nil {
		c.Set("X-Cache", "HIT")
		return c.SendString(cachedHTML)
	}

	feedHTML := loadFeedFragment(page, 25, currentUser)

	go func(key string, data string) {
		platform.RDB.Set(platform.Ctx, key, data, 30*time.Second)
	}(cacheKey, feedHTML)

	c.Set("X-Cache", "MISS")
	return c.SendString(feedHTML)
}

// Load Feed logic with Infinite Scroll
func loadFeedFragment(page, limit int, currentUser string) string {
	offset := (page - 1) * limit
	rows, err := db.Query(ctx, `SELECT id, author, content, created_at FROM posts ORDER BY id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return `<div class="text-red-500 p-4">Error loading feed</div>`
	}
	defer rows.Close()

	var postsHTML strings.Builder
	postCount := 0
	for rows.Next() {
		var id int
		var author, content string
		var createdAt time.Time
		rows.Scan(&id, &author, &content, &createdAt)
		var points int
		db.QueryRow(ctx, "SELECT COALESCE(points, 0) FROM users WHERE username = $1", author).Scan(&points)
		
		// This calls your renderPostHTMLWithReply function from post_renderer.go
		postsHTML.WriteString(renderPostHTMLWithReply(id, author, content, createdAt, currentUser, points))
		postCount++
	}

	trigger := ""
	if postCount == limit {
		trigger = fmt.Sprintf(`
            <div id="load-more-feed" 
                 hx-get="/feed?page=%d" 
                 hx-trigger="intersect once" 
                 hx-swap="outerHTML" 
                 hx-target="#load-more-feed" 
                 class="py-8 text-center text-gray-500 font-medium">
                 Loading more posts...
            </div>`, page+1)
	}
	return postsHTML.String() + trigger
}

// Main Page Route
func homeGet(c *fiber.Ctx) error {
	user := c.Cookies("user")
	if user == "" {
		return c.Redirect("/signup")
	}

	// Update online status in Redis
	rdb.Set(ctx, "online:"+user, "active", 5*time.Minute)
	
	onlineUsers := getOnlineUsers()
	feedFragment := loadFeedFragment(1, 25, user)

	c.Set("Content-Type", "text/html")
	
	// Added responsive classes: flex-col on mobile, flex-row on desktop for the post form
	homeContent := fmt.Sprintf(`
        <script>
            window.isTyping = false;
            document.addEventListener('htmx:beforeRequest', function(evt) {
                // If user is typing in a reply box, stop the automatic feed refresh
                if (window.isTyping && evt.detail.target.id === "feed") {
                    evt.preventDefault();
                    console.log("Feed refresh paused: User is typing.");
                }
            });
        </script>

        <div class="bg-white dark:bg-gray-900 rounded-3xl shadow-xl p-4 sm:p-8 border border-gray-100 dark:border-gray-700 max-w-full overflow-hidden">
            <h1 class="text-2xl sm:text-3xl font-bold text-gray-900 dark:text-white mb-6">Hey %s 👋</h1>
            
            <div id="online-slider" class="flex gap-4 overflow-x-auto pb-4 mb-8 scrollbar-hide snap-x" 
                 hx-get="/online-slider" hx-trigger="every 5s" hx-swap="innerHTML">
                %s
            </div>

            <form hx-post="/post" hx-target="#feed" hx-swap="afterbegin" onsubmit="setTimeout(() => this.reset(), 100)" 
                  class="flex flex-col sm:flex-row gap-3 mb-10 w-full">
                <input name="content" autocomplete="off" placeholder="What's happening?" 
                       class="flex-1 px-5 py-3 sm:px-6 sm:py-4 bg-gray-50 dark:bg-gray-800 rounded-2xl outline-none focus:border-blue-500 border border-transparent transition min-w-0 text-gray-900 dark:text-white shadow-inner">
                <button type="submit" 
                        class="bg-blue-600 text-white px-6 py-3 sm:px-8 sm:py-4 rounded-2xl font-bold hover:bg-blue-700 transition w-full sm:w-auto shrink-0 shadow-lg active:scale-95">
                    Post
                </button>
            </form>

            <div id="feed" class="space-y-6" hx-get="/feed" hx-trigger="every 5s" hx-swap="innerHTML">
                %s
            </div>
        </div>
    `, user, createOnlineSlider(onlineUsers), feedFragment)

	return c.SendString(Layout("Home", homeContent, user))
}
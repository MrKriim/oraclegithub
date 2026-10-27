package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

func getCategoryIcon(name string) string {
	// Pre-defined map for instant lookup
	icons := map[string]string{
		// Trending & Original
		"General": "🌍", "Gaming": "🎮", "Motovlog": "🏍️", "Aseel": "🐓", "Photography": "📸",

		// Poetry (Urdu/Regional)
		"Sad Poetry": "🥺", "Love Poetry": "❤️", "Urdu Ghazal": "📜", "Sufi Poetry": "🕌",
		"Deep Lines": "🖋️", "Attitude Poetry": "😎", "Funny Poetry": "😂", "Punjabi Poetry": "🚜",
		"Islamic Poetry": "🌙", "Literature": "📖", "Parveen Shakir": "🌹", "Jaun Elia": "🚬",
		"Allama Iqbal": "🦅", "Mirza Ghalib": "🍷", "Pashto Poetry": "🏔️", "Sindhi Poetry": "🌊",

		// Entertainment
		"Memes": "😂", "Movies": "🎬", "Music": "🎵", "Art": "🎨", "Drama": "📺", "Anime": "⛩️",
		"Bollywood": "💃", "Hollywood": "🎥", "K-Pop": "🎤", "Comedy": "🎭",

		// Tech & Work
		"Tech": "💻", "AI": "🤖", "Coding": "⚙️", "Education": "📚", "Business": "💼",
		"Freelancing": "👨‍💻", "Crypto": "💰", "Science": "🧪", "Space": "🚀", "Gadgets": "📱",

		// Lifestyle & Hobbies
		"Travel": "✈️", "Food": "🍱", "Fashion": "✨", "Fitness": "💪", "Gym": "🏋️",
		"Vlog": "🎥", "Pets": "🐾", "Gardening": "🌻", "Cooking": "🍳", "Home Decor": "🏠",
		"Health": "🏥", "Self Care": "🛁", "Beauty": "💄",

		// Community & News
		"News": "📰", "Sports": "🏏", "Cricket": "🏏", "Football": "⚽", "Automotive": "🚗",
		"Politics": "⚖️", "Religion": "🕋", "History": "🏺", "Psychology": "🧠",
		"Motivation": "🔥", "Quotes": "💬", "Weather": "☁️", "Jobs": "👔",
	}

	// Check for exact match
	if icon, ok := icons[name]; ok {
		return icon
	}

	// Smart Fallback for sub-categories
	lowerName := strings.ToLower(name)
	if strings.Contains(lowerName, "poetry") {
		return "🖋️"
	}
	if strings.Contains(lowerName, "bike") {
		return "🏍️"
	}
	if strings.Contains(lowerName, "bird") {
		return "🐓"
	}

	return "📂" // Default folder icon
}

func categoriesPageHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)

	rows, err := db.Query(ctx, `
		SELECT DISTINCT category 
		FROM posts 
		WHERE category != '' AND image_url != '' 
		ORDER BY category ASC`)

	if err != nil {
		return c.SendString("Error fetching categories")
	}
	defer rows.Close()

	var catCards strings.Builder
	for rows.Next() {
		var catName string
		if err := rows.Scan(&catName); err != nil {
			continue
		}
		icon := getCategoryIcon(catName)

		catCards.WriteString(fmt.Sprintf(`
			<button hx-get="/categories/filter?name=%s&page=1" 
			     hx-target="#category-results" 
                 hx-push-url="true"
                 hx-indicator="#loading-spinner"
			     class="flex-none flex items-center gap-2 px-6 py-2 rounded-full bg-gray-100 dark:bg-gray-800 border border-transparent hover:border-emerald-500 dark:hover:border-emerald-500 hover:bg-white dark:hover:bg-gray-900 text-gray-700 dark:text-gray-300 transition-all active:scale-95 whitespace-nowrap scroll-ml-6">
				<span class="text-base">%s</span>
				<span class="font-bold text-sm">%s</span>
			</button>`, catName, icon, catName))
	}

	html := fmt.Sprintf(`
		<div class="max-w-2xl mx-auto min-h-screen">
			<div class="sticky top-0 z-40 bg-white/80 dark:bg-gray-900/80 backdrop-blur-md border-b border-gray-100 dark:border-gray-800">
				<div class="px-4 pt-6 pb-2">
					<h1 class="text-2xl font-black text-gray-900 dark:text-white tracking-tight">Explore Categories</h1>
				</div>
				<div class="flex gap-2 overflow-x-auto px-4 pb-4 no-scrollbar snap-x">
					%s
				</div>
                <div id="loading-spinner" class="htmx-indicator h-1 bg-emerald-500 w-full animate-pulse"></div>
			</div>

			<div id="category-results" class="p-4 space-y-5">
				<div class="flex flex-col items-center justify-center py-32 text-center">
					<div class="w-20 h-20 bg-emerald-50 dark:bg-emerald-900/20 rounded-full flex items-center justify-center mb-6 animate-bounce">
						<span class="text-3xl">🚀</span>
					</div>
					<h3 class="text-xl font-bold text-gray-900 dark:text-white">Ready to Browse?</h3>
					<p class="text-gray-500 dark:text-gray-400 text-sm mt-2 max-w-[250px]">
						Select a category above to see the best shots from the community.
					</p>
				</div>
			</div>
		</div>`, catCards.String())

	c.Set("Content-Type", "text/html")
	return c.SendString(Layout("Explore Categories", html, currentUser))
}

func filterCategoryHandler(c *fiber.Ctx) error {
	categoryName := c.Query("name")
	currentUser := getCurrentUser(c)
	page := c.QueryInt("page", 1)
	const limit = 25
	offset := (page - 1) * limit

	rows, err := db.Query(ctx, `
        SELECT id, author, content, created_at, allow_jhappi, allow_chupair, 
               COALESCE(image_url, ''), COALESCE(category, ''), COALESCE(voice_url, ''),
               COALESCE(allow_comments, true), COALESCE(followers_only, false), expires_at,
               COALESCE(dp_competition_id, 0)
        FROM posts 
        WHERE category = $1 AND image_url != '' 
        ORDER BY id DESC
        LIMIT $2 OFFSET $3`, categoryName, limit, offset)

	if err != nil {
		return c.SendString("<div class='p-10 text-center text-red-500'>Database connection error.</div>")
	}
	defer rows.Close()

	var results strings.Builder
	postCount := 0
	for rows.Next() {
		var id, dpCompID int
		var author, content, img, cat, voice string
		var createdAt time.Time
		var allowJ, allowC, allowComments, followersOnly bool
		var expiresAt *time.Time

		if err := rows.Scan(&id, &author, &content, &createdAt, &allowJ, &allowC, &img, &cat, &voice, &allowComments, &followersOnly, &expiresAt, &dpCompID); err != nil {
			continue
		}

		var points int
		db.QueryRow(ctx, "SELECT COALESCE(points, 0) FROM users WHERE username = $1", author).Scan(&points)

		results.WriteString(renderPostHTML(id, author, content, createdAt, currentUser, points, allowJ, allowC, img, cat, voice, allowComments, followersOnly, expiresAt, dpCompID))
		postCount++
	}

	if postCount == limit {
		results.WriteString(fmt.Sprintf(`
            <div id="load-more-cat"
                 hx-get="/categories/filter?name=%s&page=%d"
                 hx-trigger="intersect once"
                 hx-target="#load-more-cat"
                 hx-swap="outerHTML"
                 class="py-12 flex flex-col items-center justify-center">
                <div class="w-8 h-8 border-4 border-emerald-500/20 border-t-emerald-500 rounded-full animate-spin"></div>
            </div>`, categoryName, page+1))
	}

	if results.Len() == 0 && page == 1 {
		return c.SendString(`
            <div class="py-20 text-center">
                <div class="text-5xl mb-4">🏜️</div>
                <p class="text-gray-500 dark:text-gray-400 font-bold">No images here yet.</p>
                <p class="text-gray-400 text-xs mt-1">Be the first to post in this category!</p>
            </div>`)
	}

	c.Set("Content-Type", "text/html")
	return c.SendString(results.String())
}

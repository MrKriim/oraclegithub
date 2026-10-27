package main

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
)

func ForMePage(c *fiber.Ctx) error {
	// 1. Get current user
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Redirect("/signup")
	}

	// MANDATORY: Tell the browser this is an HTML page
	c.Set("Content-Type", "text/html")

	page := c.QueryInt("page", 1)
	limit := 25
	offset := (page - 1) * limit

	// 2. Fetch Posts (UPDATED: Added COALESCE(p.voice_url, ''))
	query := `
        SELECT p.id, p.author, p.content, p.created_at, p.allow_jhappi, p.allow_chupair, 
               COALESCE(p.image_url, ''), COALESCE(p.category, 'General'), u.points, COALESCE(p.voice_url, ''), COALESCE(p.dp_competition_id, 0)
        FROM posts p
        JOIN followers f ON p.author = f.followee
        JOIN users u ON p.author = u.username
        WHERE f.follower = $1
        ORDER BY p.id DESC
        LIMIT $2 OFFSET $3`

	rows, err := db.Query(ctx, query, currentUser, limit, offset)
	if err != nil {
		return c.Status(500).SendString("Database error")
	}
	defer rows.Close()

	var postsHTML string
	postCount := 0
	for rows.Next() {
		var id, dpCompID int
		var author, content, img, cat, voice string // Added voice string variable
		var createdAt time.Time
		var allowJ, allowC bool
		var points int

		// Added &voice to match the updated SELECT layout
		rows.Scan(&id, &author, &content, &createdAt, &allowJ, &allowC, &img, &cat, &points, &voice, &dpCompID)

		// Pass all 11 arguments including voice
		postsHTML += renderPostHTML(id, author, content, createdAt, currentUser, points, allowJ, allowC, img, cat, voice, true, false, nil, dpCompID)
		postCount++
	}

	// 3. Handle Empty State (Matches your current screenshot)
	if postCount == 0 {
		postsHTML = `
        <div class="p-12 text-center bg-white dark:bg-gray-900 rounded-[2.5rem] border-2 border-dashed border-gray-200 dark:border-gray-800 mx-4 mt-6">
            <div class="text-5xl mb-4">✨</div>
            <h2 class="text-xl font-black mb-2 dark:text-white">Aap ki list khali hai!</h2>
            <p class="text-gray-500 font-bold mb-6">Un logo ki posts yahan nazar ayengi jinhein aap follow karte hain.</p>
            <a href="/search" class="inline-block px-6 py-3 bg-blue-600 text-white rounded-xl font-black shadow-lg shadow-blue-500/30">Naye Dost Banayein</a>
        </div>`
	}

	// 4. Assemble Content
	forMeContent := fmt.Sprintf(`
        <div class="max-w-2xl mx-auto">
            <div class="px-4 py-4 border-b border-gray-100 dark:border-gray-800 bg-white dark:bg-gray-900 sticky top-[56px] z-40">
                <h1 class="text-xl font-black text-blue-600 dark:text-blue-400 uppercase tracking-tighter">FOR ME ✨</h1>
                <p class="text-[10px] font-bold text-gray-400 uppercase tracking-widest">Following Feed</p>
            </div>
            <div id="feed" class="space-y-6 mt-4 px-2">
                %s
            </div>
        </div>
    `, postsHTML)

	// 5. Final Output
	return c.SendString(Layout("For Me", forMeContent, currentUser))
}

package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// --- 1. HELPER: Get Unread Count ---
func getNotificationCount(username string) int {
	var count int
	err := db.QueryRow(ctx, "SELECT COUNT(*) FROM notifications WHERE recipient = $1 AND is_read = FALSE", username).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

// --- 2. HELPER: Mark Read & Redirect ---
func notificationViewHandler(c *fiber.Ctx) error {
	notificationID := c.Params("id")
	currentUser := c.Cookies("user")
	if currentUser == "" {
		return c.Redirect("/login")
	}

	var postID int
	db.QueryRow(ctx, `UPDATE notifications SET is_read = TRUE WHERE id = $1 AND recipient = $2 RETURNING post_id`,
		notificationID, currentUser).Scan(&postID)

	return c.Redirect(fmt.Sprintf("/post/%d", postID))
}

// --- 3. HELPER: Skip Notification ---
func skipNotificationHandler(c *fiber.Ctx) error {
	notificationID := c.Params("id")
	currentUser := c.Cookies("user")
	if currentUser == "" {
		return c.Redirect("/login")
	}

	db.Exec(ctx, "UPDATE notifications SET is_read = TRUE WHERE id = $1 AND recipient = $2", notificationID, currentUser)
	return c.Redirect("/notifications")
}

// --- 4. MAIN HANDLER: Notifications Feed ---
func notificationsHandler(c *fiber.Ctx) error {
	currentUser := c.Cookies("user")
	if currentUser == "" {
		return c.Redirect("/login")
	}

	// Mark all as read when they view the page
	_, _ = db.Exec(ctx, "UPDATE notifications SET is_read = TRUE WHERE recipient = $1", currentUser)

	rows, err := db.Query(ctx, `
        SELECT 
            n.id, n.sender, n.content, n.created_at, n.post_id, n.comment_id,
            COALESCE(u.profile_image, ''), 
            COALESCE(p.content, '') as post_content
        FROM notifications n
        LEFT JOIN users u ON n.sender = u.username
        LEFT JOIN posts p ON n.post_id = p.id
        WHERE n.recipient = $1
        ORDER BY n.created_at DESC LIMIT 30`, currentUser)

	if err != nil {
		return c.SendString("Error loading notifications")
	}
	defer rows.Close()

	var notificationsHTML strings.Builder
	hasNotifications := false

	for rows.Next() {
		hasNotifications = true
		var id, postID, commentID int
		var sender, replyContent, profileImage, postContent string
		var createdAt time.Time

		rows.Scan(&id, &sender, &replyContent, &createdAt, &postID, &commentID, &profileImage, &postContent)

		// UI: Profile Pic
		avatar := `<div class="w-10 h-10 sm:w-12 sm:h-12 rounded-full bg-blue-600 shrink-0 flex items-center justify-center text-white font-bold text-xs sm:text-base">@</div>`
		if profileImage != "" {
			avatar = fmt.Sprintf(`<img src="%s" class="w-10 h-10 sm:w-12 sm:h-12 rounded-full object-cover shadow-sm shrink-0">`, profileImage)
		}

		// UI: Shorten Post Content for context
		displayPost := postContent
		if len(displayPost) > 40 {
			displayPost = displayPost[:40] + "..."
		}

		notificationsHTML.WriteString(fmt.Sprintf(`
            <div class="bg-white dark:bg-gray-800 rounded-2xl sm:rounded-3xl p-4 sm:p-6 mb-4 border border-gray-100 dark:border-gray-700 shadow-sm overflow-hidden">
                
                <div class="flex items-start gap-3 sm:gap-4 mb-3 sm:mb-4">
                    %s
                    <div class="flex-1 min-w-0">
                        <h3 class="text-xs sm:text-sm font-black text-gray-900 dark:text-white truncate">
                            @%s <span class="font-normal text-gray-500">replied</span>
                        </h3>
                        <p class="text-[9px] sm:text-[10px] font-bold text-gray-400 uppercase tracking-widest">%v</p>
                    </div>
                    <a href="/post/%d" class="text-[10px] sm:text-xs font-bold text-blue-600 hover:underline shrink-0 px-2 py-1 bg-blue-50 dark:bg-blue-900/30 rounded-lg">VIEW POST</a>
                </div>

                <div class="ml-1 sm:ml-2 mb-3 px-3 sm:px-4 py-1.5 border-l-2 border-gray-200 dark:border-gray-600">
                    <p class="text-[11px] sm:text-xs text-gray-500 italic truncate">On: "%s"</p>
                </div>

                <div class="inline-block bg-blue-600 text-white px-4 sm:px-5 py-2 sm:py-3 rounded-2xl rounded-tl-none shadow-sm ml-1 sm:ml-2 max-w-full">
                    <p class="text-xs sm:text-sm font-medium break-words">%s</p>
                </div>

                <div class="mt-4 sm:mt-6 pt-4 border-t border-gray-50 dark:border-gray-700">
                    <form hx-post="/comment/%d/reply" 
                          hx-target="#status-%d" 
                          hx-swap="innerHTML"
                          onsubmit="setTimeout(() => this.reset(), 100)" 
                          class="flex gap-2 items-center">
                        <input name="content" placeholder="Reply..." 
                               class="flex-1 min-w-0 px-3 sm:px-4 py-2 bg-gray-100 dark:bg-gray-900 border-none rounded-xl text-xs sm:text-sm focus:ring-2 focus:ring-blue-500 outline-none">
                        <button type="submit" class="bg-gray-900 dark:bg-white dark:text-black text-white px-3 sm:px-4 py-2 rounded-xl text-[10px] sm:text-xs font-bold shrink-0">SEND</button>
                    </form>
                    <div id="status-%d" class="mt-2 text-[10px] font-bold text-emerald-500 text-center"></div>
                </div>
            </div>
        `, avatar, sender, formatTwitterTime(createdAt), postID, displayPost, replyContent, commentID, id, id))
	}

	// FIXED: Removed py-10 for mobile (py-4), changed "Activity" to "Notifications"
	finalPage := fmt.Sprintf(`
        <div class="max-w-xl mx-auto py-4 sm:py-10 px-3 sm:px-4">
            <div class="flex items-center justify-between mb-6 sm:mb-8">
                <h1 class="text-2xl sm:text-3xl font-black text-gray-900 dark:text-white tracking-tighter">Notifications</h1>
                <a href="/" class="text-xs sm:text-sm font-bold text-gray-400 hover:text-gray-900 dark:hover:text-white transition">Back</a>
            </div>
            %s
        </div>
    `, func() string {
		if !hasNotifications {
			return `<p class="text-center text-gray-400 py-20 font-medium">Nothing here yet!</p>`
		}
		return notificationsHTML.String()
	}())

	return c.SendString(Layout("Notifications", finalPage, currentUser))
}
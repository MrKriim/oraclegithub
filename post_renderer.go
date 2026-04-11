package main

import (
	"fmt"
	"time"
)

// Helper: Twitter-style relative time
func formatTwitterTime(t time.Time) string {
	diff := time.Since(t)
	switch {
	case diff < time.Second:
		return "just now"
	case diff < 60*time.Second:
		return fmt.Sprintf("%ds", int(diff.Seconds()))
	case diff < time.Hour:
		return fmt.Sprintf("%dm", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%dh", int(diff.Hours()))
	case diff < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(diff.Hours()/24))
	case diff < 365*24*time.Hour:
		months := int(diff.Hours() / (24 * 30))
		if months == 0 {
			months = 1
		}
		return fmt.Sprintf("%dmo", months)
	default:
		years := int(diff.Hours() / (24 * 365))
		return fmt.Sprintf("%dy", years)
	}
}

// Get user profile image for post avatar
func getUserProfileImage(username string) string {
	var profileImage string
	err := db.QueryRow(ctx, "SELECT COALESCE(profile_image, '') FROM users WHERE username = $1", username).Scan(&profileImage)
	if err != nil || profileImage == "" {
		return `<div class="w-full h-full bg-gradient-to-br from-blue-500 to-emerald-500 flex items-center justify-center text-2xl sm:text-3xl text-white shadow-inner">👤</div>`
	}
	return fmt.Sprintf(`<img src="%s" class="w-full h-full object-cover rounded-3xl">`, profileImage)
}

// Get comment count for a post
func getCommentCount(postID int) int {
	var count int
	db.QueryRow(ctx, "SELECT COUNT(*) FROM comments WHERE post_id = $1", postID).Scan(&count)
	return count
}

// Basic post render (used in single post page)
func renderPostHTML(id int, author, content string, createdAt time.Time, currentUser string, points int) string {
	deleteBtn := ""
	if currentUser == author {
		deleteBtn = fmt.Sprintf(`
            <button hx-delete="/post/%d"
                    hx-target="#post-%d"
                    hx-swap="outerHTML"
                    hx-confirm="Delete this post?"
                    class="text-red-500 hover:text-red-600 text-sm font-medium">
                Delete
            </button>`, id, id)
	}
	commentCount := getCommentCount(id)
	commentText := "Reply"
	if commentCount > 0 {
		commentText = fmt.Sprintf("%d", commentCount)
	}

	return fmt.Sprintf(`
        <div id="post-%d" class="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-3xl p-4 sm:p-5 hover:bg-gray-50 dark:hover:bg-gray-800 transition-all">
            <div class="flex gap-3 sm:gap-4">
                <div class="flex-shrink-0">
                    <div class="w-10 h-10 sm:w-12 sm:h-12 rounded-3xl overflow-hidden shadow-inner">
                        %s
                    </div>
                </div>
                <div class="flex-1 min-w-0">
                    <div class="flex flex-wrap items-center gap-x-2 gap-y-0.5">
                        <a href="/profile/%s" class="font-bold text-gray-900 dark:text-white hover:underline truncate max-w-[150px] sm:max-w-none">@%s</a>
                        <span class="text-emerald-600 dark:text-emerald-400 font-medium text-sm shrink-0">(%d)</span>
                        <span class="text-gray-500 dark:text-gray-400 text-sm shrink-0">·</span>
                        <span class="text-gray-500 dark:text-gray-400 text-sm shrink-0">%s</span>
                    </div>
                </div>
            </div>
            <div class="mt-3">
                <p class="text-gray-800 dark:text-gray-200 leading-relaxed break-words whitespace-pre-wrap">%s</p>
                <div class="flex items-center justify-between mt-3">
                    <div class="flex items-center gap-4">
                        <a href="/post/%d" class="flex items-center gap-1 text-gray-500 dark:text-gray-400 hover:text-blue-500 dark:hover:text-blue-400 transition text-sm">
                            <span>💬</span>
                            <span>%s</span>
                        </a>
                        %s
                    </div>
                </div>
            </div>
        </div>`,
		id, getUserProfileImage(author), author, author, points, formatTwitterTime(createdAt), content, id, commentText, deleteBtn)
}

// ==================== MAIN FUNCTION USED BY HOME FEED ====================
func renderPostHTMLWithReply(id int, author, content string, createdAt time.Time, currentUser string, points int) string {
	deleteBtn := ""
	if currentUser == author {
		deleteBtn = fmt.Sprintf(`
            <button hx-delete="/post/%d"
                    hx-target="#post-%d"
                    hx-swap="outerHTML"
                    hx-confirm="Delete this post?"
                    class="text-red-500 hover:text-red-600 text-sm font-medium">
                Delete
            </button>`, id, id)
	}

	commentCount := getCommentCount(id)
	commentText := "Reply"
	if commentCount > 0 {
		commentText = fmt.Sprintf("%d", commentCount)
	}

	timeHTML := fmt.Sprintf(`<time class="relative-time text-gray-500 dark:text-gray-400 text-sm shrink-0" datetime="%s">%s</time>`,
		createdAt.Format(time.RFC3339),
		formatTwitterTime(createdAt))

	return fmt.Sprintf(`
        <div id="post-%d" class="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-3xl p-4 sm:p-5 hover:bg-gray-50 dark:hover:bg-gray-800 transition-all">
            <div class="flex gap-3 sm:gap-4">
                <div class="flex-shrink-0">
                    <div class="w-10 h-10 sm:w-12 sm:h-12 rounded-3xl overflow-hidden shadow-inner">
                        %s
                    </div>
                </div>
                <div class="flex-1 min-w-0">
                    <div class="flex flex-wrap items-center gap-x-2 gap-y-0.5">
                        <a href="/profile/%s" class="font-bold text-gray-900 dark:text-white hover:underline">@%s</a>
                        <span class="text-emerald-600 dark:text-emerald-400 font-medium text-sm">(%d)</span>
                        <span class="text-gray-500 dark:text-gray-400 text-sm">·</span>
                        %s
                    </div>
                </div>
            </div>

            <div class="mt-3">
                <p class="text-gray-800 dark:text-gray-200 leading-relaxed break-words whitespace-pre-wrap">%s</p>
            </div>

            <div class="flex items-center justify-between mt-4">
                <div class="flex items-center gap-4">
                    <button onclick="document.getElementById('reply-box-%d').classList.toggle('hidden')"
                            class="flex items-center gap-1 text-gray-500 dark:text-gray-400 hover:text-blue-500 transition text-sm font-medium">
                        <span>💬</span>
                        <span>%s</span>
                    </button>
                    %s
                </div>
                <a href="/post/%d" class="text-gray-400 hover:text-gray-600 text-sm">View all →</a>
            </div>

            <div id="reply-box-%d" class="hidden mt-4">
                <form hx-post="/comment/%d"
                      hx-target="#post-%d"
                      hx-swap="outerHTML"
                      onsubmit="window.isTyping = false; setTimeout(() => document.getElementById('reply-box-%d').classList.add('hidden'), 100)"
                      class="flex flex-col gap-3">
                    <textarea name="content" rows="3" placeholder="Write a reply..."
                              onfocus="window.isTyping = true" 
                              onblur="if(this.value==='') window.isTyping = false"
                              class="w-full px-4 py-3 bg-gray-50 dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded-2xl focus:outline-none focus:border-blue-500 resize-y"></textarea>
                    <div class="flex gap-2 justify-end">
                        <button type="button" onclick="document.getElementById('reply-box-%d').classList.add('hidden'); window.isTyping = false;"
                                class="px-5 py-2 text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 rounded-xl transition">
                            Cancel
                        </button>
                        <button type="submit" class="bg-blue-600 hover:bg-blue-700 text-white px-6 py-2 rounded-xl transition">
                            Reply
                        </button>
                    </div>
                </form>
            </div>
        </div>`,
		id, getUserProfileImage(author), author, author, points, timeHTML, content,
		id, commentText, deleteBtn, id, id, id, id, id, id)
}

// Other helper functions
func calculateAge(birth time.Time) int {
	return int(time.Since(birth).Hours() / (24 * 365))
}

func calculateStarSign(birth time.Time) string {
	monthDay := birth.Format("01-02")
	switch {
	case monthDay >= "03-21" && monthDay <= "04-19":
		return "♈ Aries"
	case monthDay >= "04-20" && monthDay <= "05-20":
		return "♉ Taurus"
	case monthDay >= "05-21" && monthDay <= "06-20":
		return "♊ Gemini"
	case monthDay >= "06-21" && monthDay <= "07-22":
		return "♋ Cancer"
	case monthDay >= "07-23" && monthDay <= "08-22":
		return "♌ Leo"
	case monthDay >= "08-23" && monthDay <= "09-22":
		return "♍ Virgo"
	case monthDay >= "09-23" && monthDay <= "10-22":
		return "♎ Libra"
	case monthDay >= "10-23" && monthDay <= "11-21":
		return "♏ Scorpio"
	case monthDay >= "11-22" && monthDay <= "12-21":
		return "♐ Sagittarius"
	case monthDay >= "12-22" || monthDay <= "01-19":
		return "♑ Capricorn"
	case monthDay >= "01-20" && monthDay <= "02-18":
		return "♒ Aquarius"
	default:
		return "♓ Pisces"
	}
}

func getProfileImageHTML(path string) string {
	if path == "" {
		return `<div class="w-full h-full bg-gradient-to-br from-blue-500 to-emerald-500 flex items-center justify-center text-6xl">👤</div>`
	}
	return fmt.Sprintf(`<img src="%s" class="w-full h-full object-cover rounded-3xl">`, path)
}
package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

func InvitesPageHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Redirect("/login")
	}

	// 1. Clear the notification badge
	_, err := db.Exec(ctx, `
		UPDATE chat_invites 
		SET is_seen = TRUE 
		WHERE recipient = $1 AND is_seen = FALSE`, currentUser)
	if err != nil {
		fmt.Println("Error updating seen status:", err)
	}

	// 2. Fetch Pending Invites (sent TO you)
	inviteRows, err := db.Query(ctx, `
		SELECT sender, created_at 
		FROM chat_invites 
		WHERE recipient = $1 AND status = 'pending'
		ORDER BY created_at DESC`, currentUser)
	if err != nil {
		return c.Status(500).SendString("Error fetching invites")
	}
	defer inviteRows.Close()

	var pendingHTML strings.Builder
	for inviteRows.Next() {
		var sender string
		var createdAt time.Time

		err = inviteRows.Scan(&sender, &createdAt)
		if err != nil {
			continue
		}

		timeAgo := formatTimeAgo(createdAt)

		pendingHTML.WriteString(fmt.Sprintf(`
			<div id="invite-%s" class="flex items-center justify-between p-4 bg-white dark:bg-[#000000] rounded-3xl border border-gray-100 dark:border-white/5 shadow-sm">
				<div class="flex items-center gap-3">
					<div class="w-10 h-10 sm:w-11 sm:h-11 rounded-2xl overflow-hidden shadow-inner">%s</div>
					<div>
						<p class="font-bold text-emerald-600 dark:text-emerald-400 text-sm">%s%s</p>
						<p class="text-[9px] text-gray-400 uppercase font-black">%s</p>
					</div>
				</div>
				<div class="flex gap-2">
					<button hx-post="/chat/accept/%s" 
							class="bg-blue-600 text-white px-4 py-2 rounded-xl text-[10px] font-black uppercase tracking-widest active:scale-95 transition-all">
						Accept
					</button>
					<button hx-post="/chat/reject/%s" 
							hx-target="#invite-%s" 
							hx-swap="outerHTML" 
							class="bg-gray-100 dark:bg-white/5 text-gray-500 px-4 py-2 rounded-xl text-[10px] font-black uppercase tracking-widest active:scale-95 transition-all">
						Ignore
					</button>
				</div>
			</div>`,
			sender, getUserProfileImage(sender), sender, TrendingStarHTML(sender), timeAgo, sender, sender, sender))
	}

	// 3. Fetch Active Chats
	chatRows, err := db.Query(ctx, `
		SELECT CASE WHEN sender = $1 THEN recipient ELSE sender END AS other_user
		FROM chat_invites 
		WHERE (sender = $1 OR recipient = $1) AND status = 'accepted'`, currentUser)
	if err != nil {
		return c.Status(500).SendString("Error fetching chats")
	}
	defer chatRows.Close()

	var activeChatsHTML strings.Builder
	for chatRows.Next() {
		var otherUser string
		chatRows.Scan(&otherUser)
		activeChatsHTML.WriteString(fmt.Sprintf(`
			<div id="chat-row-%s" class="flex items-center justify-between p-4 bg-white dark:bg-[#000000] rounded-3xl border border-gray-100 dark:border-white/5 group">
				<a href="/chat/view/%s" class="flex items-center gap-3 flex-1">
					<div class="w-10 h-10 rounded-2xl overflow-hidden border border-gray-200 dark:border-white/10">%s</div>
					<div>
						<p class="font-bold text-gray-900 dark:text-gray-100 text-sm">Chat with %s%s</p>
						<div hx-get="/user/status/%s" 
							 hx-trigger="load" 
							 class="flex items-center gap-1">
							<span class="text-[9px] text-gray-400 font-black uppercase">Loading...</span>
						</div>
					</div>
				</a>
				<button hx-delete="/chat/leave/%s" 
						hx-target="#chat-row-%s" 
						hx-swap="outerHTML"
						hx-confirm="Delete chat with %s?"
						class="text-gray-400 hover:text-red-500 p-2 rounded-xl transition">
					<span class="text-[10px] font-black uppercase">Leave</span>
				</button>
			</div>`,
			otherUser, otherUser, getUserProfileImage(otherUser),
			otherUser, TrendingStarHTML(otherUser),
			otherUser, otherUser, otherUser, otherUser))
	}

	// Assembly Logic
	pendingContent := pendingHTML.String()
	if pendingContent == "" {
		pendingContent = `<p class="text-center py-6 text-gray-400 text-[11px] font-bold uppercase tracking-widest border border-dashed border-gray-100 dark:border-white/5 rounded-3xl">No requests</p>`
	}

	activeContent := activeChatsHTML.String()
	if activeContent == "" {
		activeContent = `<p class="text-center py-6 text-gray-400 text-[11px] font-bold uppercase tracking-widest border border-dashed border-gray-100 dark:border-white/5 rounded-3xl">No chats</p>`
	}

	pageHTML := fmt.Sprintf(`
		<div class="max-w-xl mx-auto mt-6 px-4 pb-24">
			<h1 class="text-2xl font-black text-gray-900 dark:text-white mb-8 tracking-tighter">Messages</h1>
			
			<section class="mb-10">
				<div class="flex items-center gap-2 mb-4">
					<span class="text-[10px] font-black text-emerald-500 uppercase tracking-[0.2em]">Pending</span>
					<span class="h-[1px] flex-1 bg-emerald-500/10"></span>
				</div>
				<div class="space-y-3">%s</div>
			</section>

			<section>
				<div class="flex items-center gap-2 mb-4">
					<span class="text-[10px] font-black text-gray-400 uppercase tracking-[0.2em]">Active</span>
					<span class="h-[1px] flex-1 bg-gray-100 dark:bg-white/5"></span>
				</div>
				<div class="space-y-3">%s</div>
			</section>
		</div>`,
		pendingContent,
		activeContent)

	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(Layout("Messages", pageHTML, currentUser))
}
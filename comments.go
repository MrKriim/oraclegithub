package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

type FlatComment struct {
	ID            int
	Author        string
	Content       string
	CreatedAt     time.Time
	ParentID      *int
	ParentAuthor  *string
	ParentSnippet *string
	Points        int
	ProfileImage  string
}

// ====================== POST VIEW HANDLER ======================
func postViewHandler(c *fiber.Ctx) error {
	postIDStr := c.Params("id")
	currentUser := c.Cookies("user")
	postID, _ := strconv.Atoi(postIDStr)

	var id int
	var author, content string
	var createdAt time.Time
	var points int

	err := db.QueryRow(ctx, `
        SELECT p.id, p.author, p.content, p.created_at, COALESCE(u.points, 0)
        FROM posts p
        LEFT JOIN users u ON p.author = u.username
        WHERE p.id = $1`, postID).
		Scan(&id, &author, &content, &createdAt, &points)

	if err != nil {
		return c.Status(404).SendString("Post Not Found")
	}

	postHTML := renderPostHTML(id, author, content, createdAt, currentUser, points)
	commentsHTML := renderCommentsThreaded(postID, currentUser)

	fullHTML := fmt.Sprintf(`
        <div class="bg-white dark:bg-gray-900 rounded-2xl shadow-xl p-4 sm:p-8 border border-gray-100 dark:border-gray-700 max-w-full overflow-hidden">
            <a href="/" class="inline-block mb-4 text-blue-600 dark:text-blue-400 hover:underline text-sm font-bold">← Back</a>
            %s
            <div class="mt-6 sm:mt-8" id="comments-section">
                %s
            </div>
        </div>
        <script>
            // Store the interval globally so we can restore it exactly
            let pollInterval = "every 5s";

            function toggleReply(id) {
                const form = document.getElementById('reply-form-' + id);
                const isHidden = form.classList.contains('hidden');
                const poller = document.getElementById('poll-trigger');
                
                // Close all other open reply forms first
                document.querySelectorAll('.reply-form').forEach(f => f.classList.add('hidden'));
                
                if (isHidden) {
                    // 1. Show the targeted reply box
                    form.classList.remove('hidden');
                    
                    // 2. DISABLE POLLING IMMEDIATELY
                    // We remove the attribute so HTMX has no trigger to follow
                    poller.removeAttribute('hx-trigger');
                    htmx.process(poller); 
                    
                    const input = form.querySelector('input');
                    if(input) input.focus();
                    console.log("HTMX Polling: KILLED (Reply in progress)");
                } else {
                    // 3. Hide box and Restore Polling
                    form.classList.add('hidden');
                    poller.setAttribute('hx-trigger', pollInterval);
                    htmx.process(poller); 
                    console.log("HTMX Polling: RESTORED");
                }
            }

            // Safety Interceptor: Block any background list refresh if a reply form is open
            document.addEventListener('htmx:beforeRequest', function(evt) {
                const activeForm = document.querySelector('.reply-form:not(.hidden)');
                if (activeForm && evt.detail.requestConfig.method === 'GET') {
                    if (evt.detail.target.id === 'comments-list') {
                        evt.preventDefault();
                    }
                }
            });

            // Restore polling after a successful form submission
            document.addEventListener('htmx:afterRequest', function(evt) {
                if (evt.detail.successful && evt.detail.requestConfig.method === 'POST') {
                    const poller = document.getElementById('poll-trigger');
                    poller.setAttribute('hx-trigger', pollInterval);
                    htmx.process(poller);
                }
            });
        </script>
    `, postHTML, commentsHTML)

	c.Set("Content-Type", "text/html")
	return c.SendString(Layout("Post • @"+author, fullHTML, currentUser))
}

// ====================== COMMENTS THREAD UI ======================
func renderCommentsThreaded(postID int, currentUser string) string {
	var html strings.Builder

	html.WriteString(fmt.Sprintf(`
        <div class="space-y-4">
            <h3 class="text-base sm:text-lg font-semibold text-gray-800 dark:text-gray-200 mb-4 flex items-center gap-2">
                <span>💬</span> Conversation
            </h3>
            
            <form hx-post="/comment/%d"
                  hx-target="#comments-list"
                  hx-swap="afterbegin"
                  onsubmit="setTimeout(() => {this.reset();}, 100)"
                  class="flex gap-2 mb-6">
                <input name="content" autocomplete="off" placeholder="Write a reply..."
                       class="flex-1 min-w-0 px-3 py-2 bg-gray-50 dark:bg-gray-800 border border-transparent rounded-xl focus:bg-white dark:focus:bg-gray-700 outline-none text-sm">
                <button type="submit" class="bg-blue-600 text-white font-medium px-4 rounded-xl text-sm">Post</button>
            </form>

            <div id="poll-trigger" 
                 hx-get="/post/%d/comments-only" 
                 hx-trigger="every 5s" 
                 hx-target="#comments-list" 
                 hx-swap="innerHTML" 
                 class="hidden"></div>

            <div id="comments-list" class="space-y-3">
    `, postID, postID))

	rows, _ := db.Query(ctx, `
		SELECT c.id, c.author, c.content, c.created_at, c.parent_comment_id,
			   p.author AS parent_author, LEFT(p.content, 40) AS parent_snippet,
			   COALESCE(u.points, 0), COALESCE(u.profile_image, '')
		FROM comments c
		LEFT JOIN comments p ON c.parent_comment_id = p.id
		LEFT JOIN users u ON c.author = u.username
		WHERE c.post_id = $1
		ORDER BY c.created_at DESC`, postID)
	defer rows.Close()

	for rows.Next() {
		var c FlatComment
		rows.Scan(&c.ID, &c.Author, &c.Content, &c.CreatedAt, &c.ParentID, &c.ParentAuthor, &c.ParentSnippet, &c.Points, &c.ProfileImage)
		renderFlatComment(&html, c, currentUser)
	}

	html.WriteString(`</div></div>`)
	return html.String()
}

// ====================== SINGLE COMMENT RENDER ======================
func renderFlatComment(html *strings.Builder, c FlatComment, currentUser string) {
	deleteBtn := ""
	if currentUser == c.Author {
		deleteBtn = fmt.Sprintf(`<button hx-delete="/comment/%d" hx-target="#comment-%d" hx-swap="outerHTML" hx-confirm="Delete?" class="text-red-400 hover:text-red-600 transition p-1">
            <svg xmlns="http://www.w3.org/2000/svg" class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" /></svg>
        </button>`, c.ID, c.ID)
	}

	avatarHTML := ""
	if c.ProfileImage == "" {
		avatarHTML = `<div class="w-full h-full bg-gray-200 dark:bg-gray-700 flex items-center justify-center text-[10px] text-gray-500">👤</div>`
	} else {
		avatarHTML = fmt.Sprintf(`<img src="%s" class="w-full h-full object-cover">`, c.ProfileImage)
	}

	referenceContent := ""
	if c.ParentID != nil {
		pAuth := "User"; if c.ParentAuthor != nil { pAuth = *c.ParentAuthor }
		pSnip := ""; if c.ParentSnippet != nil { pSnip = *c.ParentSnippet }
		referenceContent = fmt.Sprintf(`
			<div class="flex items-center gap-1 text-[11px] text-gray-500 dark:text-gray-400 mt-1 italic">
				<span class="font-bold text-blue-400">➜</span>
				<span class="font-medium">@%s:</span>
				<span class="truncate max-w-[150px]">"%s..."</span>
			</div>`, pAuth, pSnip)
	}

	html.WriteString(fmt.Sprintf(`
        <div id="comment-%d" class="group bg-gray-50 dark:bg-gray-800/40 rounded-xl p-3 sm:p-4 border border-transparent hover:border-gray-200 dark:hover:border-gray-700 transition-all mb-3 relative">
            <div class="absolute top-3 right-3 flex items-center gap-2">
                <span class="text-[10px] text-gray-400">%s</span>
                %s
            </div>
            <div class="flex gap-3">
                <div class="shrink-0 w-9 h-9 rounded-lg overflow-hidden shadow-sm">%s</div>
                <div class="flex-1 min-w-0">
                    <div class="flex items-center gap-1.5 flex-wrap">
                        <span class="font-bold text-gray-900 dark:text-white text-xs">@%s</span>
                        <span class="text-emerald-600 dark:text-emerald-400 font-medium text-[11px] sm:text-xs">(%d)</span>
                    </div>
                    %s
                    <div class="text-gray-700 dark:text-gray-300 text-sm mt-1.5 break-words leading-relaxed">%s</div>
                    
                    <div class="mt-3">
                        <button onclick="toggleReply(%d)" class="flex items-center gap-1 text-blue-500 font-bold text-[10px] uppercase tracking-wider hover:text-blue-600 transition">
                            <svg xmlns="http://www.w3.org/2000/svg" class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 10h10a8 8 0 018 8v2M3 10l6 6m-6-6l6-6" /></svg>
                            Reply
                        </button>
                    </div>

                    <div id="reply-form-%d" class="reply-form hidden mt-3 bg-white dark:bg-gray-900 p-2 rounded-lg border border-gray-200 dark:border-gray-700 shadow-sm">
                        <form hx-post="/comment/%d/reply" 
                              hx-target="#comments-list" 
                              hx-swap="afterbegin" 
                              onsubmit="setTimeout(() => {this.reset(); document.getElementById('reply-form-%d').classList.add('hidden'); toggleReply(%d);}, 100)" 
                              class="flex gap-2">
                            <input name="content" autocomplete="off" placeholder="Reply to @%s..." 
                                   class="flex-1 min-w-0 px-3 py-1.5 bg-gray-50 dark:bg-gray-800 border-none rounded-md text-xs outline-none">
                            <button type="submit" class="bg-blue-600 text-white text-[10px] px-4 rounded-md font-bold">Send</button>
                        </form>
                    </div>
                </div>
            </div>
        </div>
    `, c.ID, formatTwitterTime(c.CreatedAt), deleteBtn, avatarHTML, c.Author, c.Points, referenceContent, c.Content, c.ID, c.ID, c.ID, c.ID, c.ID, c.Author))
}

// ====================== BACKEND HANDLERS ======================

func postCommentHandler(c *fiber.Ctx) error {
	postID, _ := strconv.Atoi(c.Params("id"))
	author := c.Cookies("user")
	content := c.FormValue("content")
	if author == "" || content == "" { return c.Status(400).SendString("Bad Request") }

	var commentID int
	var postAuthor string
	_ = db.QueryRow(ctx, "SELECT author FROM posts WHERE id = $1", postID).Scan(&postAuthor)
	
	err := db.QueryRow(ctx, "INSERT INTO comments (post_id, author, content) VALUES ($1, $2, $3) RETURNING id", postID, author, content).Scan(&commentID)
	if err != nil { return c.Status(500).SendString("DB Error") }

	if postAuthor != "" && postAuthor != author { 
		CreateNotification(postAuthor, author, "reply", postID, commentID, content) 
	}
	
	return returnSingleComment(c, commentID, author)
}

func replyToCommentHandler(c *fiber.Ctx) error {
	parentID, _ := strconv.Atoi(c.Params("id"))
	author := c.Cookies("user")
	content := c.FormValue("content")
	if author == "" || content == "" { return c.Status(400).SendString("Bad Request") }

	var postID int
	var pAuth string
	err := db.QueryRow(ctx, "SELECT post_id, author FROM comments WHERE id = $1", parentID).Scan(&postID, &pAuth)
	if err != nil { return c.Status(404).SendString("Not Found") }

	var newID int
	err = db.QueryRow(ctx, "INSERT INTO comments (post_id, author, content, parent_comment_id) VALUES ($1, $2, $3, $4) RETURNING id", postID, author, content, parentID).Scan(&newID)
	if err != nil { return c.Status(500).SendString("DB Error") }

	if pAuth != "" && pAuth != author { 
		CreateNotification(pAuth, author, "reply", postID, newID, content) 
	}
	
	return returnSingleComment(c, newID, author)
}

func deleteCommentHandler(c *fiber.Ctx) error {
	id, _ := strconv.Atoi(c.Params("id"))
	user := c.Cookies("user")
	var author string
	_ = db.QueryRow(ctx, "SELECT author FROM comments WHERE id = $1", id).Scan(&author)
	if author != user { return c.Status(401).SendString("Unauthorized") }
	_, _ = db.Exec(ctx, "DELETE FROM comments WHERE id = $1", id)
	return c.SendString("")
}

func commentsOnlyHandler(c *fiber.Ctx) error {
	postID, _ := strconv.Atoi(c.Params("id"))
	currentUser := c.Cookies("user")
	rows, err := db.Query(ctx, `
		SELECT c.id, c.author, c.content, c.created_at, c.parent_comment_id, 
		       p.author, LEFT(p.content, 40), 
		       COALESCE(u.points, 0), COALESCE(u.profile_image, '') 
		FROM comments c 
		LEFT JOIN comments p ON c.parent_comment_id = p.id 
		LEFT JOIN users u ON c.author = u.username 
		WHERE c.post_id = $1 
		ORDER BY c.created_at DESC`, postID)
	if err != nil { return c.Status(500).SendString("DB Error") }
	defer rows.Close()

	var html strings.Builder
	for rows.Next() {
		var f FlatComment
		_ = rows.Scan(&f.ID, &f.Author, &f.Content, &f.CreatedAt, &f.ParentID, &f.ParentAuthor, &f.ParentSnippet, &f.Points, &f.ProfileImage)
		renderFlatComment(&html, f, currentUser)
	}
	c.Set("Content-Type", "text/html")
	return c.SendString(html.String())
}

func returnSingleComment(c *fiber.Ctx, id int, user string) error {
	var f FlatComment
	err := db.QueryRow(ctx, `
		SELECT c.id, c.author, c.content, c.created_at, c.parent_comment_id, 
		       p.author, LEFT(p.content, 40), 
		       COALESCE(u.points, 0), COALESCE(u.profile_image, '') 
		FROM comments c 
		LEFT JOIN comments p ON c.parent_comment_id = p.id 
		LEFT JOIN users u ON c.author = u.username 
		WHERE c.id = $1`, id).
		Scan(&f.ID, &f.Author, &f.Content, &f.CreatedAt, &f.ParentID, &f.ParentAuthor, &f.ParentSnippet, &f.Points, &f.ProfileImage)
	
	if err != nil { return c.Status(500).SendString("Render Error") }
	
	var html strings.Builder
	renderFlatComment(&html, f, user)
	c.Set("Content-Type", "text/html")
	return c.SendString(html.String())
}
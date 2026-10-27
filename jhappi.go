package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// HandleReaction is the single endpoint behind every reaction button
// (like, love, taliyan, wow, follow, dislike, disgusted, angry, chupair).
// Route: POST /react/:type/:id
func HandleReaction(c *fiber.Ctx) error {
	reactType := c.Params("type")
	postID, err := c.ParamsInt("id")
	if err != nil {
		return c.SendString("Invalid post id")
	}
	sender := getCurrentUser(c)
	if sender == "" {
		return c.Redirect("/login")
	}

	valid := false
	for _, r := range reactionList {
		if r.Key == reactType {
			valid = true
			break
		}
	}
	if !valid {
		return c.SendString("Unknown reaction")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var createdAt time.Time
	if err := db.QueryRow(ctx, "SELECT created_at FROM users WHERE username=$1", sender).Scan(&createdAt); err != nil {
		return c.Status(500).SendString("User not found")
	}
	if time.Since(createdAt) < 7*24*time.Hour {
		daysLeft := 7 - int(time.Since(createdAt).Hours()/24)
		return c.Type("html").SendString(`
<div style="background:#fefce8;border:1.5px solid #fbbf24;border-radius:14px;padding:14px 16px;margin:8px 0;display:flex;align-items:center;gap:10px;">
  <span style="font-size:22px;">⏳</span>
  <div>
    <div style="font-weight:900;font-size:13px;color:#92400e;">Account Naya Hai!</div>
    <div style="font-size:11px;color:#b45309;margin-top:2px;">React karne ke liye account kam az kam 7 din purana hona chahiye. ` + fmt.Sprintf("%d", daysLeft) + ` din aur wait karein.</div>
  </div>
</div>`)
	}

	var authorUsername, authorType, senderType, disabledReactions string
	err = db.QueryRow(ctx, `
        SELECT p.author, u.user_type, s.user_type, COALESCE(p.disabled_reactions, '')
        FROM posts p
        JOIN users u ON p.author = u.username
        JOIN users s ON s.username = $2
        WHERE p.id = $1`,
		postID, sender,
	).Scan(&authorUsername, &authorType, &senderType, &disabledReactions)
	if err != nil {
		return c.SendString("Post not found")
	}

	var noticeHTML string
	isNegative := isNegativeReaction(reactType)

	switch {
	case isReactionDisabled(disabledReactions, reactType):
		noticeHTML = fmt.Sprintf(`
<div style="background:#fef2f2;border:1.5px solid #fca5a5;border-radius:14px;padding:12px 14px;margin:8px 0;font-size:12px;font-weight:800;color:#b91c1c;text-align:center;">
  🔒 %s is turned off for this post.
</div>`, getReactionDef(reactType).Label)

	case isNegative && authorType == "admin":
		noticeHTML = `
<div style="background:#fef2f2;border:1.5px solid #fca5a5;border-radius:14px;padding:12px 14px;margin:8px 0;font-size:12px;font-weight:800;color:#b91c1c;text-align:center;">
  🚫 You cannot react negatively to an admin's post.
</div>`

	default:
		var authorRank int64
		if isNegative && senderType != "admin" {
			db.QueryRow(ctx, `
                SELECT r.rank FROM (
                    SELECT username, RANK() OVER (ORDER BY points DESC) as rank
                    FROM users WHERE COALESCE(user_type, '') != 'admin'
                ) r WHERE r.username = $1`, authorUsername).Scan(&authorRank)
		}

		if isNegative && senderType != "admin" && authorRank >= 1 && authorRank <= 3 {
			noticeHTML = `
<div style="background:#fef2f2;border:1.5px solid #fca5a5;border-radius:14px;padding:12px 14px;margin:8px 0;font-size:12px;font-weight:800;color:#b91c1c;text-align:center;">
  🚫 Ap is user ko is tarah react nahi kar saktay kyunke ye top 3 scorer me se aik hai.
</div>`
			break
		}

		var topScorers []string
		if tRows, qErr := db.Query(ctx, "SELECT username FROM users ORDER BY points DESC LIMIT 3"); qErr == nil {
			for tRows.Next() {
				var uname string
				if tRows.Scan(&uname) == nil {
					topScorers = append(topScorers, uname)
				}
			}
			tRows.Close()
		}
		isTopScorer := false
		for _, t := range topScorers {
			if t == sender {
				isTopScorer = true
				break
			}
		}
		isTrending := TrendingStarHTML(sender) != ""
		isAdminSender := senderType == "admin"
		isAdminAuthor := authorType == "admin"

		pts, _ := calcReactionPoints(reactType, isAdminSender, isAdminAuthor, isTrending, isTopScorer)
		delta := pts
		if isNegative {
			delta = -pts
		}

		tx, txErr := db.Begin(ctx)
		if txErr == nil {
			var existingType string
			lockErr := tx.QueryRow(ctx,
				"SELECT type FROM interactions WHERE post_id=$1 AND sender=$2 FOR UPDATE",
				postID, sender).Scan(&existingType)

			switch {
			case lockErr == nil && existingType == reactType:
				// Tapping the exact same reaction again is a no-op —
				// prevents accidental double-charging of points, but
				// still lets the user pick a DIFFERENT reaction below.
				tx.Rollback(ctx)

			case lockErr == nil:
				// Switching reactions: revert the old delta, apply the new one.
				oldPts, _ := calcReactionPoints(existingType, isAdminSender, isAdminAuthor, isTrending, isTopScorer)
				oldDelta := oldPts
				if isNegativeReaction(existingType) {
					oldDelta = -oldPts
				}
				if _, err := tx.Exec(ctx, "UPDATE interactions SET type=$1, created_at=NOW() WHERE post_id=$2 AND sender=$3", reactType, postID, sender); err == nil {
					tx.Exec(ctx, "UPDATE users SET points = GREATEST(0, points - $1 + $2) WHERE username = $3", oldDelta, delta, authorUsername)
					tx.Commit(ctx)
					goSafe("EvaluatePostTrending", func() { EvaluatePostTrending(postID) })
				} else {
					tx.Rollback(ctx)
				}

			default:
				if _, err := tx.Exec(ctx,
					"INSERT INTO interactions (post_id, sender, type) VALUES ($1, $2, $3)",
					postID, sender, reactType); err == nil {
					tx.Exec(ctx, "UPDATE users SET points = GREATEST(0, points + $1) WHERE username = $2", delta, authorUsername)
					tx.Commit(ctx)
					goSafe("EvaluatePostTrending", func() { EvaluatePostTrending(postID) })
					if authorUsername != sender {
						reactMsg := fmt.Sprintf("reacted %s to your post", getReactionDef(reactType).Label)
						goSafe("reaction notif", func() {
							nctx, ncancel := context.WithTimeout(context.Background(), 3*time.Second)
							defer ncancel()
							db.Exec(nctx, `INSERT INTO notifications (recipient, sender, type, content, post_id) VALUES ($1,$2,'reaction',$3,$4)`,
								authorUsername, sender, reactMsg, postID)
						})
						Push(authorUsername, sender, reactMsg, postLink(postID))
					}
				} else {
					tx.Rollback(ctx)
				}
			}
		}
	}

	var p struct {
		Author        string
		Content       string
		CreatedAt     time.Time
		Points        int
		AllowJhappi   bool
		AllowChupair  bool
		ImageUrl      string
		Category      string
		VoiceUrl      string
		IsTrending    bool
		AllowComments bool
		FollowersOnly bool
		ExpiresAt     *time.Time
		DPCompID      int
	}
	err = db.QueryRow(ctx, `
        SELECT p.author, p.content, p.created_at, u.points,
               p.allow_jhappi, p.allow_chupair,
               COALESCE(p.image_url, ''), COALESCE(p.category, 'General'),
               COALESCE(p.voice_url, ''), COALESCE(p.is_trending, false),
               COALESCE(p.allow_comments, true), COALESCE(p.followers_only, false), p.expires_at,
               COALESCE(p.dp_competition_id, 0)
        FROM posts p
        JOIN users u ON p.author = u.username
        WHERE p.id = $1`, postID,
	).Scan(
		&p.Author, &p.Content, &p.CreatedAt, &p.Points,
		&p.AllowJhappi, &p.AllowChupair,
		&p.ImageUrl, &p.Category, &p.VoiceUrl, &p.IsTrending,
		&p.AllowComments, &p.FollowersOnly, &p.ExpiresAt, &p.DPCompID,
	)
	if err != nil {
		return c.Status(500).SendString("Error refreshing post data")
	}

	var htmlOut string
	if p.IsTrending {
		htmlOut = RenderTrendingPost(
			postID, p.Author, p.Content, p.CreatedAt, sender,
			p.Points, p.AllowJhappi, p.AllowChupair,
			p.ImageUrl, p.Category, p.VoiceUrl,
			p.AllowComments, p.FollowersOnly, p.ExpiresAt, p.DPCompID,
		)
	} else {
		htmlOut = renderPostHTML(
			postID, p.Author, p.Content, p.CreatedAt, sender,
			p.Points, p.AllowJhappi, p.AllowChupair,
			p.ImageUrl, p.Category, p.VoiceUrl,
			p.AllowComments, p.FollowersOnly, p.ExpiresAt, p.DPCompID,
		)
	}

	oobNotice := fmt.Sprintf(`<div id="post-notice-%d" hx-swap-oob="true">%s</div>`, postID, noticeHTML)
	return c.Type("html").SendString(oobNotice + htmlOut)
}

// ====================== COMMENT REACTIONS (engagement only — never touches points) ======================
// Route: POST /react-comment/:type/:id  (id = comment id)
func HandleCommentReaction(c *fiber.Ctx) error {
	reactType := c.Params("type")
	commentID, err := c.ParamsInt("id")
	if err != nil {
		return c.SendString("Invalid comment id")
	}
	sender := getCurrentUser(c)
	if sender == "" {
		return c.Redirect("/login")
	}

	valid := false
	for _, r := range reactionList {
		if r.Key == reactType {
			valid = true
			break
		}
	}
	if !valid {
		return c.SendString("Unknown reaction")
	}

	rCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var existingType string
	lookupErr := db.QueryRow(rCtx,
		"SELECT type FROM comment_reactions WHERE comment_id=$1 AND sender=$2", commentID, sender,
	).Scan(&existingType)

	switch {
	case lookupErr == nil && existingType == reactType:
		db.Exec(rCtx, "DELETE FROM comment_reactions WHERE comment_id=$1 AND sender=$2", commentID, sender)
	case lookupErr == nil:
		db.Exec(rCtx, "UPDATE comment_reactions SET type=$1, created_at=NOW() WHERE comment_id=$2 AND sender=$3", reactType, commentID, sender)
	default:
		db.Exec(rCtx, "INSERT INTO comment_reactions (comment_id, sender, type) VALUES ($1, $2, $3)", commentID, sender, reactType)
	}

	c.Set("Content-Type", "text/html")
	return c.SendString(renderCommentReactionBar(commentID, sender))
}

func commentReactionCounts(commentID int) map[string]int {
	counts := map[string]int{}
	rCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := db.Query(rCtx, "SELECT type, COUNT(*) FROM comment_reactions WHERE comment_id=$1 GROUP BY type", commentID)
	if err != nil {
		return counts
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		var n int
		if rows.Scan(&t, &n) == nil {
			counts[t] = n
		}
	}
	return counts
}

func userCommentReaction(commentID int, username string) string {
	if username == "" {
		return ""
	}
	var t string
	rCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	db.QueryRow(rCtx, "SELECT type FROM comment_reactions WHERE comment_id=$1 AND sender=$2", commentID, username).Scan(&t)
	return t
}

// buildCommentReactButton renders a single React button — identical UX to
// buildReactButton used on posts — plus its hidden 9-GIF picker for one
// comment. Tap = quick "like", hover/long-press = reveal all. Same
// reactionList / same GIF paths as posts, just wired to
// /react-comment/:type/:id instead of /react/:type/:id, and namespaced
// under separate globals/ids (creact-*, commentReact*) so it can't collide
// with the post React button's DOM ids or JS globals when both render on
// the same page (e.g. the single-post view).
func buildCommentReactButton(commentID int, myReaction string) string {
	mainInner := `<span>👍</span><span>React</span>`
	if myReaction != "" {
		if d := getReactionDef(myReaction); d.Gif != "" {
			mainInner = fmt.Sprintf(`<img src="%s" loading="lazy" style="width:16px;height:16px;object-fit:contain;" alt="">%s`, d.Gif, d.Label)
		}
	}

	var opts strings.Builder
	for _, r := range reactionList {
		mine := ""
		if r.Key == myReaction {
			mine = " reaction-mine"
		}
		opts.WriteString(fmt.Sprintf(
			`<button type="button" class="reaction-opt%s" title="%s" onclick="event.stopPropagation();commentReactPick(%d,'%s')"><img src="%s" loading="lazy" alt="%s"></button>`,
			mine, r.Label, commentID, r.Key, r.Gif, r.Label,
		))
	}

	return fmt.Sprintf(`
<div class="reaction-wrap" id="creact-wrap-%d">
  <button type="button" class="action-btn btn-react" onclick="event.stopPropagation();commentReactToggle(%d)">%s</button>
  <div class="reaction-picker" id="creact-picker-%d">%s</div>
</div>
<script>
(function(){
  if (window._commentReactJsReady) return;
  window._commentReactJsReady = true;

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
    var wrap = document.getElementById('creact-wrap-'+id);
    var picker = document.getElementById('creact-picker-'+id);
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

  window.commentReactToggle = function(id){
    var wasOpenForThisId = openState && openState.id === id;
    closeAllPickers();
    if (!wasOpenForThisId) openPicker(id);
  };

  window.commentReactPick = function(id, type){
    closeAllPickers();
    htmx.ajax('POST', '/react-comment/'+type+'/'+id, {target:'#comment-reactions-'+id, swap:'outerHTML'});
  };

  document.addEventListener('click', function(e){
    if (!openState) return;
    var wrap = document.getElementById('creact-wrap-'+openState.id);
    if (openState.picker.contains(e.target)) return;
    if (wrap && wrap.contains(e.target)) return;
    closeAllPickers();
  });
})();
</script>`, commentID, commentID, mainInner, commentID, opts.String())
}

// renderCommentReactionBar renders the reaction row for one comment: the
// single React button (same style/behavior as the post's React button)
// plus a small total-count badge when the comment has any reactions.
func renderCommentReactionBar(commentID int, currentUser string) string {
	counts := commentReactionCounts(commentID)
	myReaction := userCommentReaction(commentID, currentUser)

	total := 0
	for _, n := range counts {
		total += n
	}
	countBadge := ""
	if total > 0 {
		countBadge = fmt.Sprintf(`<span style="font-size:10px;font-weight:700;color:#6b7280;">%d</span>`, total)
	}

	return fmt.Sprintf(`<div id="comment-reactions-%d" class="flex items-center gap-1.5 mt-2">%s%s</div>`,
		commentID, buildCommentReactButton(commentID, myReaction), countBadge)
}

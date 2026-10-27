package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"log"
	mathrand "math/rand"
	"strings"
	"time"

	"mysocialmediawebsite/pkg/platform"
)

// ─────────────────────────────────────────────────────────────────────────────
// WORD GAME — "Complete the Word". Fully automatic: a background worker
// spawns these as posts from a system account, sized to how quiet the real
// feed is, and lets the existing expiry sweep (posts.expires_at) delete them
// if nobody solves it in time — no manual creation, no new cleanup jobs.
// ─────────────────────────────────────────────────────────────────────────────

const (
	// Fixed repeat cadence: a new word game challenge spawns every
	// wordGameCheckInterval, unconditionally, forever — no longer gated
	// on feed activity or on whether the previous game was solved.
	wordGameCheckInterval  = 1 * time.Hour
	wordGameDuration       = 12 * time.Minute
	wordGamePrizePointsMin = 5000  // busy feed
	wordGamePrizePointsMax = 15000 // dead-quiet feed — reward for showing up alone
	wordGameSystemAuthor   = "Dostana"
	wordGameEventChannel   = "dostana:wordgame_event"
)

type wordGame struct {
	ID          int
	PostID      int
	Creator     string
	Word        string
	Hint        string
	PrizePoints int
	Winner      string
	SolvedAt    *time.Time
	ExpiresAt   *time.Time
}

type wordGameWord struct{ Word, Hint string }

var wordGameBank = []wordGameWord{
	{"dostana", "Urdu word for friendship"},
	{"mohabbat", "Urdu word for love"},
	{"pakistan", "The country this app is made for"},
	{"cricket", "Pakistan's most-loved sport"},
	{"biryani", "A spicy rice dish everyone fights over"},
	{"chai", "You probably drink this every morning"},
	{"lahore", "City known as the heart of Punjab"},
	{"karachi", "Pakistan's largest city, by the sea"},
	{"ramadan", "The holy month of fasting"},
	{"eid", "The festival after a month of fasting"},
	{"ghazal", "A poetic form popular in Urdu literature"},
	{"shayari", "Urdu word for poetry"},
	{"dosti", "Another Urdu word close to friendship"},
	{"khushi", "Urdu word for happiness"},
	{"sabr", "Urdu word for patience"},
	{"ustad", "Urdu word for teacher or master"},
	{"safar", "Urdu word for journey"},
	{"kahani", "Urdu word for story"},
	{"mehfil", "A gathering, also a feature name on this app"},
	{"trending", "What a hot post becomes on this app"},
	{"reaction", "You tap this to like or dislike a post"},
	{"follower", "Someone who follows your profile"},
	{"leaderboard", "Where top scorers are ranked"},
	{"points", "What you earn from reactions and games"},
	{"gaming", "A popular post category on this app"},
	{"anime", "A popular Japanese animation category here"},
	{"fitness", "Category for gym and workout posts"},
	{"gardening", "Category for people who love plants"},
	{"sufi", "A mystical branch of Islamic poetry"},
	{"attitude", "A poetry category for bold, confident lines"},
}

// ─────────────────────────────────────────────────────────────────────────────
// SCHEMA + SYSTEM AUTHOR
// ─────────────────────────────────────────────────────────────────────────────

func initWordGameSchema() {
	mustExec("create word_games", `
		CREATE TABLE IF NOT EXISTS word_games (
			id           SERIAL PRIMARY KEY,
			post_id      INT REFERENCES posts(id) ON DELETE CASCADE,
			creator      TEXT NOT NULL,
			word         TEXT NOT NULL,
			hint         TEXT DEFAULT '',
			prize_points INT  DEFAULT 5000,
			winner       TEXT DEFAULT '',
			solved_at    TIMESTAMPTZ,
			expires_at   TIMESTAMPTZ,
			created_at   TIMESTAMPTZ DEFAULT NOW()
		)`)
	softExec("migrate word_games.expires_at", `ALTER TABLE word_games ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ`)
	softExec("index idx_word_games_post_id", `CREATE INDEX IF NOT EXISTS idx_word_games_post_id ON word_games(post_id)`)
	softExec("index idx_word_games_expires_at", `CREATE INDEX IF NOT EXISTS idx_word_games_expires_at ON word_games(expires_at)`)
	log.Println("✅ Word Game schema ready")
}

// ensureSystemAuthor creates the "Dostana" account used to author auto-spawned
// games, with a random unguessable password — it can never actually log in,
// it just needs to satisfy posts.author's FK to users(username).
func ensureSystemAuthor(username string) {
	var exists bool
	_ = db.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM users WHERE username=$1)`, username).Scan(&exists)
	if exists {
		return
	}
	buf := make([]byte, 32)
	_, _ = cryptorand.Read(buf)
	placeholder := hex.EncodeToString(buf)
	if _, err := db.Exec(context.Background(), `
		INSERT INTO users (username, password, user_type, is_banned, account_status, points)
		VALUES ($1, $2, 'normal', false, 'approved', 0)
		ON CONFLICT (username) DO NOTHING`, username, placeholder); err != nil {
		log.Printf("ensureSystemAuthor error: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// WORKER — decides how many games should be live right now, based on how
// much real posting activity there's been in the last 5 minutes.
// ─────────────────────────────────────────────────────────────────────────────

func StartWordGameWorker() {
	goSafe("WordGameWorker", func() {
		ensureSystemAuthor(wordGameSystemAuthor)

		// No immediate spawn on startup — the first word game now
		// appears only once wordGameCheckInterval has fully elapsed,
		// then again every wordGameCheckInterval after that,
		// indefinitely (1h, 1h, 1h, …). Previously this spawned one
		// immediately (after a 20s startup buffer) and only started
		// the hourly wait afterward, so the very first game arrived
		// almost instantly instead of a full hour in.
		ticker := time.NewTicker(wordGameCheckInterval)
		defer ticker.Stop()
		cleanupTicker := time.NewTicker(10 * time.Minute)
		defer cleanupTicker.Stop()
		for {
			select {
			case <-ticker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("panic in wordgame worker tick: %v", r)
						}
					}()
					maybeSpawnWordGame()
				}()
			case <-cleanupTicker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("panic in wordgame cleanup tick: %v", r)
						}
					}()
					cleanupEndedWordGames()
				}()
			}
		}
	})
}

func cleanupEndedWordGames() {
	qctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tag, err := db.Exec(qctx, `
		DELETE FROM posts WHERE id IN (
			SELECT post_id FROM word_games
			WHERE winner = '' AND expires_at IS NOT NULL AND expires_at <= NOW() - INTERVAL '10 minutes'
		)`)
	if err != nil {
		log.Printf("cleanupEndedWordGames error: %v", err)
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		log.Printf("🧩 Cleaned up %d ended word game post(s)", n)
	}
}

// maybeSpawnWordGame spawns a fresh challenge on every tick,
// unconditionally — every wordGameCheckInterval (1 hour), forever. The
// previous version gated spawning on feed activity and skipped ticks
// while a game was already active; that gating is intentionally
// removed. Since wordGameDuration (12 min) is well under the 1-hour
// repeat cadence, only one word game is ever live on the feed at a
// time under this schedule.
func maybeSpawnWordGame() {
	spawnWordGame()
}

func prizeForActivity(recentPosts int) int {
	switch {
	case recentPosts == 0:
		return wordGamePrizePointsMax
	case recentPosts <= 3:
		return 10000
	case recentPosts <= 8:
		return 7000
	default:
		return wordGamePrizePointsMin
	}
}

func timeSinceLastRealPost() time.Duration {
	var last time.Time
	qctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := db.QueryRow(qctx, `SELECT COALESCE(MAX(created_at), NOW() - INTERVAL '1 hour') FROM posts WHERE author <> $1`, wordGameSystemAuthor).Scan(&last)
	if err != nil {
		return 0
	}
	return time.Since(last)
}

func countRecentPosts(window time.Duration) int {
	var n int
	qctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	since := time.Now().Add(-window)
	_ = db.QueryRow(qctx, `SELECT COUNT(*) FROM posts WHERE created_at > $1 AND author <> $2`,
		since, wordGameSystemAuthor).Scan(&n)
	return n
}

func countActiveWordGames() int {
	var n int
	qctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = db.QueryRow(qctx, `
		SELECT COUNT(*) FROM word_games
		WHERE winner = '' AND (expires_at IS NULL OR expires_at > NOW())`).Scan(&n)
	return n
}

// dynamicWordCandidates pulls real, currently-true facts from the database
// and turns each into a guessable word + hint, the same pattern trivia.go
// uses for its per-user questions. Deliberately limited to vocabulary
// about the SITE itself (categories) rather than about any specific
// person — usernames are intentionally excluded so the word game never
// turns into "guess who this is" about a real user. A candidate that
// can't currently be resolved is simply omitted, never a placeholder.
func dynamicWordCandidates() []wordGameWord {
	var out []wordGameWord

	if cat, ok := triviaBiggestCategory(); ok {
		out = append(out, wordGameWord{cat, "Dostana's most-posted-in category right now"})
	}

	return out
}

// pickFreshWord draws from the fixed wordGameBank plus a batch of live
// dynamic candidates (see dynamicWordCandidates), filtering out anything
// used in the last 40 spawns so the same word doesn't repeat back-to-back
// even as the combined pool grows. The window is wider than the old
// static-only version (15) because the pool itself is now much larger.
func pickFreshWord() wordGameWord {
	used := map[string]bool{}
	if rows, err := db.Query(context.Background(), `SELECT word FROM word_games ORDER BY id DESC LIMIT 40`); err == nil {
		for rows.Next() {
			var w string
			if rows.Scan(&w) == nil {
				used[strings.ToLower(w)] = true
			}
		}
		rows.Close()
	}

	var pool []wordGameWord
	for _, w := range wordGameBank {
		if !used[strings.ToLower(w.Word)] {
			pool = append(pool, w)
		}
	}
	for _, w := range dynamicWordCandidates() {
		w.Word = strings.TrimSpace(w.Word)
		if w.Word == "" {
			continue
		}
		if !used[strings.ToLower(w.Word)] {
			pool = append(pool, w)
		}
	}

	if len(pool) == 0 {
		pool = wordGameBank
	}
	if len(pool) == 0 {
		return wordGameWord{}
	}
	return pool[mathrand.Intn(len(pool))]
}

// spawnWordGame creates the post + game row together, then broadcasts it
// live over the same SSE pipe real posts use — it appears between posts on
// everyone's feed instantly, and self-deletes via the existing expiry sweep
// (posts.expires_at) if nobody solves it in time — cascading to word_games.
func spawnWordGame() {
	word := pickFreshWord()
	if word.Word == "" {
		return
	}

	prize := prizeForActivity(countRecentPosts(5 * time.Minute))

	wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tx, err := db.Begin(wctx)
	if err != nil {
		log.Printf("spawnWordGame begin error: %v", err)
		return
	}
	defer tx.Rollback(wctx)

	expiresAt := time.Now().Add(wordGameDuration)
	var postID int
	if err := tx.QueryRow(wctx, `
		INSERT INTO posts (content, author, category)
		VALUES ($1, $2, 'General') RETURNING id`,
		"🧩 Complete the Word — guess it first and win points!", wordGameSystemAuthor,
	).Scan(&postID); err != nil {
		log.Printf("spawnWordGame post insert error: %v", err)
		return
	}
	if _, err := tx.Exec(wctx, `
		INSERT INTO word_games (post_id, creator, word, hint, prize_points, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		postID, wordGameSystemAuthor, word.Word, word.Hint, prize, expiresAt,
	); err != nil {
		log.Printf("spawnWordGame game insert error: %v", err)
		return
	}
	if err := tx.Commit(wctx); err != nil {
		log.Printf("spawnWordGame commit error: %v", err)
		return
	}

	log.Printf("🧩 Word game spawned: post %d (%d-letter word)", postID, len(word.Word))
	publishNewPost(postID, "", wordGameSystemAuthor)
}

// ─────────────────────────────────────────────────────────────────────────────
// LOOKUP — called once per post render (same cost class as the comment-count
// / trending lookups renderPostHTML already does), returns nil for the
// overwhelming majority of ordinary posts.
// ─────────────────────────────────────────────────────────────────────────────

func getWordGameForPost(postID int) *wordGame {
	qctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	var g wordGame
	err := db.QueryRow(qctx, `
		SELECT id, post_id, creator, word, hint, prize_points, winner, solved_at, expires_at
		FROM word_games
		WHERE post_id = $1`, postID,
	).Scan(&g.ID, &g.PostID, &g.Creator, &g.Word, &g.Hint, &g.PrizePoints, &g.Winner, &g.SolvedAt, &g.ExpiresAt)
	if err != nil {
		return nil
	}
	return &g
}

// ─────────────────────────────────────────────────────────────────────────────
// GUESS — first correct guess wins, atomically (row-locked so two
// simultaneous correct guesses can't both win).
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// GUESS — players guess by posting the word as a normal post. checkWordGameGuess
// is called from the post-creation handler right after a post is inserted
// (same spot that already calls publishNewPost). Nothing about the guess is
// ever trusted from the client: there is no guess form, no endpoint a person
// could call directly, and no client-supplied "I got it right" signal — the
// only input is the post content already committed to Postgres, checked
// against the DB-held answer inside a single row-locked transaction, so two
// simultaneous winning posts can't both win.
// ─────────────────────────────────────────────────────────────────────────────

func checkWordGameGuess(postID int, author, content string) {
	guess := strings.ToLower(strings.TrimSpace(content))
	guess = strings.Trim(guess, " .,!?\n\t")
	if guess == "" {
		return
	}

	gctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tx, err := db.Begin(gctx)
	if err != nil {
		return
	}
	defer tx.Rollback(gctx)

	var g wordGame
	err = tx.QueryRow(gctx, `
		SELECT wg.id, wg.post_id, wg.creator, wg.word, wg.hint, wg.prize_points, wg.winner, wg.solved_at, p.expires_at
		FROM word_games wg JOIN posts p ON p.id = wg.post_id
		WHERE wg.word = $1 AND wg.winner = '' AND (p.expires_at IS NULL OR p.expires_at > NOW())
		ORDER BY wg.id ASC LIMIT 1 FOR UPDATE`, guess,
	).Scan(&g.ID, &g.PostID, &g.Creator, &g.Word, &g.Hint, &g.PrizePoints, &g.Winner, &g.SolvedAt, &g.ExpiresAt)
	if err != nil {
		return
	}

	totalPrize := g.PrizePoints
	if g.ExpiresAt != nil {
		createdAt := g.ExpiresAt.Add(-wordGameDuration)
		fracRemaining := float64(g.ExpiresAt.Sub(time.Now())) / float64(wordGameDuration)
		if fracRemaining > 1 {
			fracRemaining = 1
		} else if fracRemaining < 0 {
			fracRemaining = 0
		}
		_ = createdAt
		totalPrize = g.PrizePoints + int(float64(g.PrizePoints)*0.5*fracRemaining)
	}

	if _, err := tx.Exec(gctx, `UPDATE word_games SET winner = $1, solved_at = NOW(), prize_points = $2 WHERE id = $3`, author, totalPrize, g.ID); err != nil {
		log.Printf("checkWordGameGuess update error: %v", err)
		return
	}
	if _, err := tx.Exec(gctx, `UPDATE users SET points = points + $1 WHERE username = $2`, totalPrize, author); err != nil {
		log.Printf("checkWordGameGuess points error: %v", err)
		return
	}

	if err := tx.Commit(gctx); err != nil {
		log.Printf("checkWordGameGuess commit error: %v", err)
		return
	}

	log.Printf("🧩 Word game %d solved by %s via post %d (base %d, total %d)", g.ID, author, postID, g.PrizePoints, totalPrize)
	publishWordGameWin(g.PostID, author, totalPrize)
}

func publishWordGameWin(postID int, winner string, prize int) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in publishWordGameWin: %v", r)
			}
		}()
		pctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		payload := fmt.Sprintf("WIN|%d|%s|%d", postID, winner, prize)
		if err := platform.RDB.Publish(pctx, wordGameEventChannel, payload).Err(); err != nil {
			log.Printf("publishWordGameWin error: %v", err)
		}
	}()
}

func wordGameOOBNotice(postID int, msg string) string {
	return fmt.Sprintf(`<div id="post-notice-%d" hx-swap-oob="true"><div style="background:#eff6ff;border:1.5px solid #93c5fd;border-radius:14px;padding:10px 14px;margin:8px 0;font-size:12px;font-weight:800;color:#1d4ed8;text-align:center;">%s</div></div>
<script>
(function(){
  var card = document.getElementById('wordgame-%d');
  if (!card) return;
  card.classList.remove('wg-shake');
  void card.offsetWidth;
  card.classList.add('wg-shake');
})();
</script>`, postID, msg, postID)
}

// ─────────────────────────────────────────────────────────────────────────────
// RENDER
// ─────────────────────────────────────────────────────────────────────────────

const wordGameCSS = `<style id="wordgame-styles">
.wg-card{border-radius:12px;overflow:hidden;background:linear-gradient(135deg,#1e1b4b,#4c1d95);box-shadow:0 3px 10px rgba(76,29,149,.3);margin-bottom:6px;}
.wg-card-ended{background:linear-gradient(135deg,#3f3f46,#71717a);}
.wg-card-won{background:linear-gradient(135deg,#064e3b,#059669);}
.wg-row{display:flex;align-items:center;gap:6px;padding:6px 8px;flex-wrap:wrap;}
.wg-badge{display:inline-flex;align-items:center;gap:3px;padding:2px 6px;border-radius:999px;background:rgba(255,255,255,.15);color:#fff;font-size:7.5px;font-weight:900;letter-spacing:.02em;text-transform:uppercase;white-space:nowrap;}
.wg-prize{display:inline-flex;align-items:center;gap:3px;padding:2px 7px;border-radius:999px;background:linear-gradient(90deg,#fbbf24,#f59e0b);color:#451a03;font-size:8.5px;font-weight:900;white-space:nowrap;}
.wg-time{margin-left:auto;display:inline-flex;align-items:center;gap:3px;font-size:9px;font-weight:900;color:#e0e7ff;font-variant-numeric:tabular-nums;white-space:nowrap;}
.wg-timebar{height:3px;background:rgba(255,255,255,.15);}
.wg-timebar-fill{height:100%;width:100%;background:linear-gradient(90deg,#34d399,#10b981);animation-name:wg-drain;animation-timing-function:linear;animation-fill-mode:forwards;}
@keyframes wg-drain{from{width:var(--wg-start,100%);}to{width:0%;}}.wg-timebar-fill.wg-warn{background:linear-gradient(90deg,#fbbf24,#f59e0b);}
.wg-timebar-fill.wg-danger{background:linear-gradient(90deg,#f87171,#dc2626);}
.wg-tiles{display:flex;justify-content:center;flex-wrap:wrap;gap:4px;padding:6px 10px 2px;}
.wg-tile{width:20px;height:24px;display:flex;align-items:center;justify-content:center;border-radius:5px;background:rgba(255,255,255,.12);border:1px solid rgba(255,255,255,.25);color:#a5b4fc;font-size:11px;font-weight:900;transform:scale(.6);opacity:0;animation:wg-pop .3s ease-out forwards;}
.wg-tile-on{background:linear-gradient(135deg,#fbbf24,#f59e0b);border-color:#fbbf24;color:#451a03;}
.wg-tile-gap{width:8px;}
@keyframes wg-pop{to{transform:scale(1);opacity:1;}}
.wg-pulse{animation:wg-pulse-anim 1.8s ease-in-out infinite;}
@keyframes wg-pulse-anim{0%,100%{opacity:1;}50%{opacity:.55;}}
.wg-card-urgent{animation:wg-shake-urgent 2.5s ease-in-out infinite;box-shadow:0 0 14px rgba(220,38,38,.55);}
@keyframes wg-shake-urgent{0%,100%{transform:translateX(0);}25%{transform:translateX(-1px);}75%{transform:translateX(1px);}}
.wg-clue{padding:0 10px 8px;color:#c7d2fe;font-size:10px;font-weight:600;text-align:center;}
.wg-note{padding:0 10px 8px;text-align:center;color:#a5b4fc;font-size:9px;font-weight:700;}
.wg-solved{padding:8px 10px;text-align:center;position:relative;overflow:hidden;}
.wg-solved .wg-trophy{font-size:20px;display:inline-block;animation:wg-bounce .6s ease-out;}
@keyframes wg-bounce{0%{transform:translateY(-14px) scale(.5);opacity:0;}60%{transform:translateY(2px) scale(1.15);opacity:1;}100%{transform:translateY(0) scale(1);}}
.wg-solved .wg-win-msg{color:#fff;font-size:11px;font-weight:900;line-height:1.3;margin-top:2px;}
.wg-solved .wg-answer{margin-top:2px;color:#d1fae5;font-size:9px;font-weight:700;letter-spacing:.03em;text-transform:uppercase;}
.wg-ended{padding:8px 10px;text-align:center;}
.wg-ended .wg-ended-icon{font-size:18px;}
.wg-ended .wg-ended-msg{color:#f4f4f5;font-size:11px;font-weight:900;line-height:1.3;margin-top:2px;}
.wg-ended .wg-answer{margin-top:2px;color:#d4d4d8;font-size:9px;font-weight:700;letter-spacing:.03em;text-transform:uppercase;}
</style>`

func wordGameTiles(word string, elapsedFrac float64) string {
	letters := []rune(strings.ToUpper(word))
	total := 0
	for _, r := range letters {
		if r != ' ' {
			total++
		}
	}
	if total == 0 {
		return ""
	}
	if elapsedFrac < 0 {
		elapsedFrac = 0
	} else if elapsedFrac > 1 {
		elapsedFrac = 1
	}
	maxRevealable := total - 1
	if maxRevealable < 1 {
		maxRevealable = 1
	}
	revealCount := 1 + int(elapsedFrac*float64(maxRevealable))
	if revealCount > maxRevealable {
		revealCount = maxRevealable
	}

	var sb strings.Builder
	shown, delay := 0, 0
	for _, r := range letters {
		if r == ' ' {
			sb.WriteString(`<span class="wg-tile-gap"></span>`)
			continue
		}
		if shown < revealCount {
			sb.WriteString(fmt.Sprintf(`<span class="wg-tile wg-tile-on" style="animation-delay:%dms">%c</span>`, delay*60, r))
		} else {
			sb.WriteString(fmt.Sprintf(`<span class="wg-tile" style="animation-delay:%dms">?</span>`, delay*60))
		}
		shown++
		delay++
	}
	return sb.String()
}
func renderWordGamePostCard(g *wordGame, currentUser string) string {
	isExpired := g.Winner == "" && g.ExpiresAt != nil && time.Now().After(*g.ExpiresAt)

	if g.Winner != "" {
		return wordGameCSS + fmt.Sprintf(`
<div id="post-notice-%d"></div>
<div id="wordgame-%d" class="wg-card wg-card-won">
  <div class="wg-row"><span class="wg-badge">🧩 Word Game</span><span class="wg-prize">🏆 %d points</span></div>
  <div class="wg-solved">
    <span class="wg-trophy">🏆</span>
    <div class="wg-win-msg">%s guessed it and won %d points!</div>
    <div class="wg-answer">The word was: %s</div>
  </div>
</div>`, g.PostID, g.PostID, g.PrizePoints,
			html.EscapeString(g.Winner), g.PrizePoints, strings.ToUpper(html.EscapeString(g.Word)))
	}

	if isExpired {
		return wordGameCSS + fmt.Sprintf(`
<div id="post-notice-%d"></div>
<div id="wordgame-%d" class="wg-card wg-card-ended">
  <div class="wg-row"><span class="wg-badge">🧩 Word Game</span><span class="wg-time">⏰ Ended</span></div>
  <div class="wg-ended">
    <span class="wg-ended-icon">⌛</span>
    <div class="wg-ended-msg">Time's up — nobody guessed it!</div>
    <div class="wg-answer">The word was: %s</div>
  </div>
</div>`, g.PostID, g.PostID, strings.ToUpper(html.EscapeString(g.Word)))
	}

	clueHTML := ""
	if g.Hint != "" {
		clueHTML = fmt.Sprintf(`<div class="wg-clue">%s</div>`, html.EscapeString(g.Hint))
	}

	var remaining time.Duration
	if g.ExpiresAt != nil {
		remaining = time.Until(*g.ExpiresAt)
		if remaining < 0 {
			remaining = 0
		}
	}
	remainingSecs := int64(remaining / time.Second)
	pct := 0.0
	if wordGameDuration > 0 {
		pct = float64(remaining) / float64(wordGameDuration) * 100
		if pct > 100 {
			pct = 100
		} else if pct < 0 {
			pct = 0
		}
	}
	barClass := ""
	if pct < 15 {
		barClass = " wg-danger"
	} else if pct < 40 {
		barClass = " wg-warn"
	}

	elapsedFrac := (100 - pct) / 100
	glowClass := ""
	if pct < 15 {
		glowClass = " wg-card-urgent"
	}

	timerHTML := fmt.Sprintf(`
<div class="wg-timebar"><div class="wg-timebar-fill%s" id="wg-fill-%d" style="--wg-start:%.1f%%;animation-duration:%ds;"></div></div>`,
		barClass, g.PostID, pct, remainingSecs)
	return wordGameCSS + fmt.Sprintf(`
<div id="post-notice-%d"></div>
<div id="wordgame-%d" class="wg-card%s">
  <div class="wg-row">
    <span class="wg-badge wg-pulse">🧩 Word Game</span>
    <span class="wg-prize">🏆 %d points</span>
    <span class="wg-time" id="wg-time-%d">--:--</span>
  </div>
  %s
  <div class="wg-tiles">%s</div>
  %s
  <div class="wg-note">💬 Post the answer to win! Faster guesses earn a bonus ⚡</div>
</div>`, g.PostID, g.PostID, glowClass, g.PrizePoints, g.PostID, timerHTML, wordGameTiles(g.Word, elapsedFrac), clueHTML)
}

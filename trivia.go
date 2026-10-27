package main

import (
	"context"
	"fmt"
	"html"
	"log"
	mathrand "math/rand"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// SITE TRIVIA — separate from the Word Game. Spawns a question about the
// website itself every triviaCheckInterval (1 min), forever. Some
// questions are static (hardest, fixed trivia about how the site works);
// some are dynamic — computed at spawn time from live data (e.g. who is
// currently the #1 scorer) and the correct answer is snapshotted into the
// DB row at that moment, so a guess is always checked against the answer
// as it stood when the question was posted, not whatever the live state
// has drifted to by the time someone answers.
// ─────────────────────────────────────────────────────────────────────────────

const (
	triviaCheckInterval = 1 * time.Hour
	triviaDuration      = 5 * time.Minute
	triviaPrizePoints   = 8000
	triviaSystemAuthor  = "Dostana"
)

type triviaGame struct {
	ID          int
	PostID      int
	Question    string
	Answer      string
	Difficulty  string
	PrizePoints int
	Winner      string
	SolvedAt    *time.Time
	ExpiresAt   *time.Time
}

// ─────────────────────────────────────────────────────────────────────────────
// SCHEMA
// ─────────────────────────────────────────────────────────────────────────────

func initTriviaSchema() {
	mustExec("create site_trivia", `
		CREATE TABLE IF NOT EXISTS site_trivia (
			id           SERIAL PRIMARY KEY,
			post_id      INT REFERENCES posts(id) ON DELETE CASCADE,
			question     TEXT NOT NULL,
			answer       TEXT NOT NULL,
			difficulty   TEXT DEFAULT 'hard',
			prize_points INT  DEFAULT 8000,
			winner       TEXT DEFAULT '',
			solved_at    TIMESTAMPTZ,
			expires_at   TIMESTAMPTZ,
			created_at   TIMESTAMPTZ DEFAULT NOW()
		)`)
	softExec("index idx_site_trivia_post_id", `CREATE INDEX IF NOT EXISTS idx_site_trivia_post_id ON site_trivia(post_id)`)
	softExec("index idx_site_trivia_expires_at", `CREATE INDEX IF NOT EXISTS idx_site_trivia_expires_at ON site_trivia(expires_at)`)
	log.Println("✅ Site Trivia schema ready")
}

// ─────────────────────────────────────────────────────────────────────────────
// WORKER — fires every triviaCheckInterval, unconditionally, forever.
// ─────────────────────────────────────────────────────────────────────────────

// ensureTriviaSystemUser guarantees the "Dostana" system account exists
// before spawnTrivia ever tries to INSERT a post authored by it. posts.author
// has a hard foreign key to users(username) — if this row is missing, every
// single spawnTrivia() insert fails silently (logged, but nothing ever
// appears on the site), which is indistinguishable from "trivia isn't
// working" with no other symptom. This is intentionally independent of
// ensureSystemAuthor (defined elsewhere) as a belt-and-braces guarantee.
func ensureTriviaSystemUser() {
	qctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := db.Exec(qctx, `
		INSERT INTO users (username, password, account_status, user_type, is_banned)
		VALUES ($1, $2, 'approved', 'normal', false)
		ON CONFLICT (username) DO NOTHING`,
		triviaSystemAuthor, "!disabled-login-"+triviaSystemAuthor)
	if err != nil {
		log.Printf("ensureTriviaSystemUser error: %v", err)
		return
	}
	log.Println("✅ Trivia system user ensured:", triviaSystemAuthor)
}

func StartTriviaWorker() {
	goSafe("TriviaWorker", func() {
		ensureSystemAuthor(triviaSystemAuthor)
		ensureTriviaSystemUser()

		// No immediate spawn on startup — same reasoning as the word
		// game worker: the first trivia question now appears only
		// after a full triviaCheckInterval (1h) has elapsed, then
		// every 1h after that, indefinitely.
		ticker := time.NewTicker(triviaCheckInterval)
		defer ticker.Stop()
		cleanupTicker := time.NewTicker(10 * time.Minute)
		defer cleanupTicker.Stop()
		for {
			select {
			case <-ticker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("panic in trivia worker tick: %v", r)
						}
					}()
					spawnTrivia()
				}()
			case <-cleanupTicker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("panic in trivia cleanup tick: %v", r)
						}
					}()
					cleanupEndedTrivia()
				}()
			}
		}
	})
}

func cleanupEndedTrivia() {
	qctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tag, err := db.Exec(qctx, `
		DELETE FROM posts WHERE id IN (
			SELECT post_id FROM site_trivia
			WHERE winner = '' AND expires_at IS NOT NULL AND expires_at <= NOW() - INTERVAL '10 minutes'
		)`)
	if err != nil {
		log.Printf("cleanupEndedTrivia error: %v", err)
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		log.Printf("🧠 Cleaned up %d ended trivia post(s)", n)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// QUESTION BANK — a mix of static "hardest" fixed trivia about how the
// site works, and dynamic questions resolved from live data at spawn
// time. Each entry returns (question, answer, difficulty) or ok=false if
// it can't currently be answered (e.g. no online users yet).
// ─────────────────────────────────────────────────────────────────────────────

type triviaGenerator func() (question, answer string, ok bool)

// ── helper queries backing the dynamic generators below. Each is a small,
// short-timeout read so a spawn tick never blocks on a slow query — if a
// query fails or returns nothing usable, its generator just reports
// ok=false and spawnTrivia falls through to the next shuffled candidate.
func triviaTotalUsers() (int, bool) {
	var n int
	qctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if err := db.QueryRow(qctx, `SELECT COUNT(*) FROM users WHERE account_status='approved'`).Scan(&n); err != nil {
		return 0, false
	}
	return n, true
}

func triviaTotalPosts() (int, bool) {
	var n int
	qctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if err := db.QueryRow(qctx, `SELECT COUNT(*) FROM posts`).Scan(&n); err != nil {
		return 0, false
	}
	return n, true
}

func triviaMostFollowedUser() (string, int, bool) {
	var uname string
	var n int
	qctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	err := db.QueryRow(qctx, `
		SELECT followee, COUNT(*) c FROM followers
		GROUP BY followee ORDER BY c DESC LIMIT 1`).Scan(&uname, &n)
	if err != nil || uname == "" {
		return "", 0, false
	}
	return uname, n, true
}

func triviaNewestUser() (string, bool) {
	var uname string
	qctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	err := db.QueryRow(qctx, `
		SELECT username FROM users WHERE account_status='approved'
		ORDER BY created_at DESC LIMIT 1`).Scan(&uname)
	if err != nil || uname == "" {
		return "", false
	}
	return uname, true
}

func triviaBiggestCategory() (string, bool) {
	var cat string
	qctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	err := db.QueryRow(qctx, `
		SELECT COALESCE(category,'General') c FROM posts
		GROUP BY c ORDER BY COUNT(*) DESC LIMIT 1`).Scan(&cat)
	if err != nil || cat == "" {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(cat)), true
}

func triviaMostActivePoster() (string, bool) {
	var uname string
	qctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	err := db.QueryRow(qctx, `
		SELECT author FROM posts WHERE author <> $1
		GROUP BY author ORDER BY COUNT(*) DESC LIMIT 1`, triviaSystemAuthor).Scan(&uname)
	if err != nil || uname == "" {
		return "", false
	}
	return uname, true
}

func triviaRandomOnlineUser() (string, bool) {
	online := getOnlineUsers()
	if len(online) == 0 {
		return "", false
	}
	return online[mathrand.Intn(len(online))], true
}

// ─────────────────────────────────────────────────────────────────────────────
// PER-USER DYNAMIC QUESTIONS — picks a random real (approved, non-admin,
// non-banned) user and a random real stat about them every time it fires.
// Every user × every template below is a distinct, freshly-computed
// question, so this single generator produces effectively unlimited
// variety instead of drawing from a fixed list — and it always reflects
// whatever the database currently says, because the facts are queried
// live at spawn time and the resulting answer is snapshotted into
// site_trivia.answer exactly like every other dynamic question here.
// ─────────────────────────────────────────────────────────────────────────────

type userTriviaFacts struct {
	Username       string
	City           string
	Points         int
	PostCount      int
	FollowerCount  int
	FollowingCount int
	TopCategory    string
}

func triviaRandomApprovedUser() (string, bool) {
	var uname string
	qctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	err := db.QueryRow(qctx, `
		SELECT username FROM users
		WHERE account_status = 'approved'
		  AND COALESCE(user_type,'') != 'admin'
		  AND is_banned = false
		ORDER BY random() LIMIT 1`).Scan(&uname)
	if err != nil || uname == "" {
		return "", false
	}
	return uname, true
}

func getUserTriviaFacts(username string) (userTriviaFacts, bool) {
	f := userTriviaFacts{Username: username}
	qctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	err := db.QueryRow(qctx, `
		SELECT COALESCE(city,''), COALESCE(points,0),
		       (SELECT COUNT(*) FROM posts WHERE author = $1),
		       (SELECT COUNT(*) FROM followers WHERE followee = $1),
		       (SELECT COUNT(*) FROM followers WHERE follower = $1)
		FROM users WHERE username = $1`, username,
	).Scan(&f.City, &f.Points, &f.PostCount, &f.FollowerCount, &f.FollowingCount)
	if err != nil {
		return f, false
	}

	// Best-effort — an empty result just means this template gets
	// skipped below, not a failure of the whole lookup.
	_ = db.QueryRow(qctx, `
		SELECT COALESCE(category,'General') c FROM posts
		WHERE author = $1 GROUP BY c ORDER BY COUNT(*) DESC LIMIT 1`, username,
	).Scan(&f.TopCategory)

	return f, true
}

type userQuestionTemplate func(f userTriviaFacts) (question, answer string, ok bool)

func userTriviaTemplates() []userQuestionTemplate {
	return []userQuestionTemplate{
		func(f userTriviaFacts) (string, string, bool) {
			if strings.TrimSpace(f.City) == "" {
				return "", "", false
			}
			return fmt.Sprintf("📍 What city does %s list on their Dostana profile?", f.Username),
				strings.ToLower(strings.TrimSpace(f.City)), true
		},
		func(f userTriviaFacts) (string, string, bool) {
			if f.Points < 5 {
				// Below 5, "nearest 10" always rounds to 0 either way,
				// which reads as a broken/unanswerable question — skip
				// this template for very-low-point users.
				return "", "", false
			}
			// True round-to-nearest instead of floor division: floor
			// division (points/10)*10 always rounds DOWN, so e.g. 7
			// points would floor to 0 even though "nearest 10" should
			// round it up to 10 — that mismatch between the question's
			// wording and the stored answer was rejecting correct
			// guesses.
			rounded := ((f.Points + 5) / 10) * 10
			return fmt.Sprintf("⭐ How many points does %s currently have on Dostana? (nearest 10, e.g. '120')", f.Username),
				fmt.Sprintf("%d", rounded), true
		},
		func(f userTriviaFacts) (string, string, bool) {
			if f.PostCount == 0 {
				return "", "", false
			}
			return fmt.Sprintf("✍️ Exactly how many posts has %s made on Dostana?", f.Username),
				fmt.Sprintf("%d", f.PostCount), true
		},
		func(f userTriviaFacts) (string, string, bool) {
			if f.FollowerCount == 0 {
				return "", "", false
			}
			return fmt.Sprintf("👥 How many followers does %s currently have?", f.Username),
				fmt.Sprintf("%d", f.FollowerCount), true
		},
		func(f userTriviaFacts) (string, string, bool) {
			if f.FollowingCount == 0 {
				return "", "", false
			}
			return fmt.Sprintf("👣 How many people does %s follow on Dostana?", f.Username),
				fmt.Sprintf("%d", f.FollowingCount), true
		},
		func(f userTriviaFacts) (string, string, bool) {
			if strings.TrimSpace(f.TopCategory) == "" {
				return "", "", false
			}
			return fmt.Sprintf("📁 Which category does %s post in the most?", f.Username),
				strings.ToLower(strings.TrimSpace(f.TopCategory)), true
		},
		func(f userTriviaFacts) (string, string, bool) {
			ans := "no"
			if isUserOnline(f.Username) {
				ans = "yes"
			}
			return fmt.Sprintf("🟢 Is %s online on Dostana right now? (answer 'yes' or 'no')", f.Username), ans, true
		},
	}
}

// triviaUserQuestionGenerator conforms to the triviaGenerator signature so
// it can be dropped straight into the pool in triviaGenerators() below.
func triviaUserQuestionGenerator() (question, answer string, ok bool) {
	uname, ok := triviaRandomApprovedUser()
	if !ok {
		return "", "", false
	}
	facts, ok := getUserTriviaFacts(uname)
	if !ok {
		return "", "", false
	}

	templates := userTriviaTemplates()
	mathrand.Shuffle(len(templates), func(i, j int) { templates[i], templates[j] = templates[j], templates[i] })
	for _, t := range templates {
		if q, a, ok := t(facts); ok && strings.TrimSpace(a) != "" {
			return q, a, true
		}
	}
	return "", "", false
}

func triviaGenerators() []triviaGenerator {
	return []triviaGenerator{
		// ═══════════════════════════════════════════════════════════════
		// DYNAMIC — live leaderboard / site-data questions. Each answer
		// is snapshotted into the DB row at spawn time, so it's always
		// checked against the state as it stood when the question was
		// posted.
		// ═══════════════════════════════════════════════════════════════
		func() (string, string, bool) {
			ranks := getCachedUserRanks()
			for uname, e := range ranks {
				if e.Rank == 1 {
					return "🏆 HARDEST QUESTION: Who is currently ranked #1 on the Dostana leaderboard?", uname, true
				}
			}
			return "", "", false
		},
		func() (string, string, bool) {
			ranks := getCachedUserRanks()
			for uname, e := range ranks {
				if e.Rank == 2 {
					return "🥈 Who is currently ranked #2 on the Dostana leaderboard?", uname, true
				}
			}
			return "", "", false
		},
		func() (string, string, bool) {
			ranks := getCachedUserRanks()
			for uname, e := range ranks {
				if e.Rank == 3 {
					return "🥉 Who is currently ranked #3 on the Dostana leaderboard?", uname, true
				}
			}
			return "", "", false
		},
		func() (string, string, bool) {
			online := getOnlineUsers()
			if len(online) == 0 {
				return "", "", false
			}
			return "👥 Right now, how many users are online on Dostana? (a number)", fmt.Sprintf("%d", len(online)), true
		},
		func() (string, string, bool) {
			n, ok := triviaTotalUsers()
			if !ok || n == 0 {
				return "", "", false
			}
			return "📊 Roughly how many approved users does Dostana currently have? (nearest hundred, e.g. '200')", fmt.Sprintf("%d", (n/100)*100), true
		},
		func() (string, string, bool) {
			n, ok := triviaTotalPosts()
			if !ok || n == 0 {
				return "", "", false
			}
			return "📊 Roughly how many total posts exist on Dostana right now? (nearest hundred, e.g. '500')", fmt.Sprintf("%d", (n/100)*100), true
		},
		func() (string, string, bool) {
			uname, followers, ok := triviaMostFollowedUser()
			if !ok || followers == 0 {
				return "", "", false
			}
			return "👑 Who currently has the most followers on Dostana?", uname, true
		},
		func() (string, string, bool) {
			uname, ok := triviaNewestUser()
			if !ok {
				return "", "", false
			}
			return "🆕 Who is the most recently joined user on Dostana?", uname, true
		},
		func() (string, string, bool) {
			cat, ok := triviaBiggestCategory()
			if !ok {
				return "", "", false
			}
			return "📁 Which post category currently has the most posts on Dostana?", cat, true
		},
		func() (string, string, bool) {
			uname, ok := triviaMostActivePoster()
			if !ok {
				return "", "", false
			}
			return "✍️ Who has posted the most on Dostana (excluding the Dostana system account)?", uname, true
		},
		func() (string, string, bool) {
			uname, ok := triviaRandomOnlineUser()
			if !ok {
				return "", "", false
			}
			return "🟢 True or false trivia: is " + uname + " online on Dostana right now? (answer 'yes' or 'no')", "yes", true
		},

		// ═══════════════════════════════════════════════════════════════
		// STATIC — "how to use Dostana" tutorial questions. Teaches
		// features while testing knowledge. Answers match the site's
		// actual UI labels/behavior.
		// ═══════════════════════════════════════════════════════════════
		func() (string, string, bool) {
			return "🧠 HARDEST QUESTION: What is the name of the system account that posts Word Game and Trivia challenges?", triviaSystemAuthor, true
		},
		func() (string, string, bool) {
			return "🧠 Which reaction on Dostana carries the biggest negative penalty when sent by an Admin?", "chupair", true
		},
		func() (string, string, bool) {
			return "🧠 What is the name of the reaction used to show applause on a post?", "taliyan", true
		},
		func() (string, string, bool) {
			return "🧠 Which post category uses the 🕌 icon?", "sufi poetry", true
		},
		func() (string, string, bool) {
			return "🧠 Which post category uses the 🚜 icon?", "punjabi poetry", true
		},
		func() (string, string, bool) {
			return "🧠 What section of Dostana lets you battle with profile pictures?", "dp battle", true
		},
		func() (string, string, bool) {
			return "🧠 What is the maximum number of characters allowed in a custom display name on Dostana?", "10", true
		},
		func() (string, string, bool) {
			return "🧠 On Dostana's bottom menu, what is the label for the 1-on-1 messaging icon?", "1 on 1", true
		},
		func() (string, string, bool) {
			return "🧠 Which tab shows posts only from people you follow?", "for me", true
		},
		func() (string, string, bool) {
			return "🧠 What emoji marks a post from a currently Trending Creator on Dostana?", "🔥", true
		},
		func() (string, string, bool) {
			return "🧠 How do you react to a post with more than just a simple Like on Dostana — tap and hold, or double tap?", "tap and hold", true
		},
		func() (string, string, bool) {
			return "🧠 What text appears on the Block button when you try to block a top-3 leaderboard scorer?", "protected", true
		},
		func() (string, string, bool) {
			return "🧠 What section of Dostana shows the ranked list of top scorers?", "leaderboard", true
		},
		func() (string, string, bool) {
			return "🧠 What symbol appears under a user's custom display name to show their real username?", "@", true
		},
		func() (string, string, bool) {
			return "🧠 On Dostana, what do you tap to see who is currently active on the site?", "online slider", true
		},
		func() (string, string, bool) {
			return "🧠 What tab would you use to see posts sorted by what's hot right now?", "trending", true
		},
		func() (string, string, bool) {
			return "🧠 What is the icon used for the Categories page in the top navigation?", "🗂️", true
		},
		func() (string, string, bool) {
			return "🧠 Which page would you visit to change your dark mode setting — Settings or Categories?", "settings", true
		},
		func() (string, string, bool) {
			return "🧠 What is the name of Dostana's automatic word-guessing game?", "word game", true
		},
		func() (string, string, bool) {
			return "🧠 In the Word Game, how do you submit your guess — post it as a normal post, or use a special guess button?", "post it as a normal post", true
		},
	}
}

// triviaGeneratorsWithUsers wraps triviaGenerators, adding the per-user
// generator multiple times so user-specific questions (which alone cover
// unlimited combinations of user × stat) show up often instead of being
// drowned out by the ~30 fixed site-trivia questions above.
func triviaGeneratorsWithUsers() []triviaGenerator {
	gens := triviaGenerators()
	for i := 0; i < 12; i++ {
		gens = append(gens, triviaGenerator(triviaUserQuestionGenerator))
	}
	return gens
}

func spawnTrivia() {
	gens := triviaGeneratorsWithUsers()
	// Shuffle order so it's not always the same question first when
	// several generators are eligible.
	mathrand.Shuffle(len(gens), func(i, j int) { gens[i], gens[j] = gens[j], gens[i] })

	var question, answer string
	var ok bool
	for _, gen := range gens {
		question, answer, ok = gen()
		if ok && strings.TrimSpace(answer) != "" {
			break
		}
	}
	if !ok {
		log.Printf("🧠 spawnTrivia: no eligible question this tick")
		return
	}

	wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tx, err := db.Begin(wctx)
	if err != nil {
		log.Printf("spawnTrivia begin error: %v", err)
		return
	}
	defer tx.Rollback(wctx)

	expiresAt := time.Now().Add(triviaDuration)
	var postID int
	if err := tx.QueryRow(wctx, `
		INSERT INTO posts (content, author, category)
		VALUES ($1, $2, 'General') RETURNING id`,
		"🧠 Site Trivia — first correct answer wins!", triviaSystemAuthor,
	).Scan(&postID); err != nil {
		log.Printf("spawnTrivia post insert error: %v", err)
		return
	}
	if _, err := tx.Exec(wctx, `
		INSERT INTO site_trivia (post_id, question, answer, difficulty, prize_points, expires_at)
		VALUES ($1, $2, $3, 'hard', $4, $5)`,
		postID, question, strings.ToLower(strings.TrimSpace(answer)), triviaPrizePoints, expiresAt,
	); err != nil {
		log.Printf("spawnTrivia trivia insert error: %v", err)
		return
	}
	if err := tx.Commit(wctx); err != nil {
		log.Printf("spawnTrivia commit error: %v", err)
		return
	}

	log.Printf("🧠 Trivia spawned: post %d — %q", postID, question)
	publishNewPost(postID, "", triviaSystemAuthor)
}

// ─────────────────────────────────────────────────────────────────────────────
// LOOKUP — mirrors getWordGameForPost.
// ─────────────────────────────────────────────────────────────────────────────

func getTriviaForPost(postID int) *triviaGame {
	qctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	var g triviaGame
	err := db.QueryRow(qctx, `
		SELECT id, post_id, question, answer, difficulty, prize_points, winner, solved_at, expires_at
		FROM site_trivia
		WHERE post_id = $1`, postID,
	).Scan(&g.ID, &g.PostID, &g.Question, &g.Answer, &g.Difficulty, &g.PrizePoints, &g.Winner, &g.SolvedAt, &g.ExpiresAt)
	if err != nil {
		return nil
	}
	return &g
}

// ─────────────────────────────────────────────────────────────────────────────
// GUESS — same pattern as checkWordGameGuess: players answer by posting a
// normal post; the answer is checked against the snapshotted DB value
// inside a row-locked transaction so two simultaneous correct answers
// can't both win.
// ─────────────────────────────────────────────────────────────────────────────

func checkTriviaGuess(postID int, author, content string) {
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

	var g triviaGame
	err = tx.QueryRow(gctx, `
		SELECT wt.id, wt.post_id, wt.question, wt.answer, wt.difficulty, wt.prize_points, wt.winner, wt.solved_at, p.expires_at
		FROM site_trivia wt JOIN posts p ON p.id = wt.post_id
		WHERE wt.answer = $1 AND wt.winner = '' AND (p.expires_at IS NULL OR p.expires_at > NOW())
		ORDER BY wt.id ASC LIMIT 1 FOR UPDATE`, guess,
	).Scan(&g.ID, &g.PostID, &g.Question, &g.Answer, &g.Difficulty, &g.PrizePoints, &g.Winner, &g.SolvedAt, &g.ExpiresAt)
	if err != nil {
		return
	}

	if _, err := tx.Exec(gctx, `UPDATE site_trivia SET winner = $1, solved_at = NOW() WHERE id = $2`, author, g.ID); err != nil {
		log.Printf("checkTriviaGuess update error: %v", err)
		return
	}
	if _, err := tx.Exec(gctx, `UPDATE users SET points = points + $1 WHERE username = $2`, g.PrizePoints, author); err != nil {
		log.Printf("checkTriviaGuess points error: %v", err)
		return
	}

	if err := tx.Commit(gctx); err != nil {
		log.Printf("checkTriviaGuess commit error: %v", err)
		return
	}

	log.Printf("🧠 Trivia %d solved by %s via post %d (%d points)", g.ID, author, postID, g.PrizePoints)
	publishWordGameWin(g.PostID, author, g.PrizePoints)
}

func publishWordGameWinTrivia() {} // no-op placeholder (unused) — trivia reuses publishWordGameWin directly

func wordGameOOBNoticeTrivia(postID int, msg string) string {
	return fmt.Sprintf(`<div id="post-notice-%d" hx-swap-oob="true"><div style="background:#eff6ff;border:1.5px solid #93c5fd;border-radius:14px;padding:10px 14px;margin:8px 0;font-size:12px;font-weight:800;color:#1d4ed8;text-align:center;">%s</div></div>`, postID, msg)
}

// ─────────────────────────────────────────────────────────────────────────────
// RENDER — visually distinct from the word game card (indigo/violet vs
// purple) so the two game types read as different features at a glance.
// ─────────────────────────────────────────────────────────────────────────────

const triviaCSS = `<style id="trivia-styles">
.tv-card{border-radius:12px;overflow:hidden;background:linear-gradient(135deg,#0c4a6e,#0369a1);box-shadow:0 3px 10px rgba(3,105,161,.3);margin-bottom:6px;}
.tv-card-ended{background:linear-gradient(135deg,#3f3f46,#71717a);}
.tv-card-won{background:linear-gradient(135deg,#064e3b,#059669);}
.tv-row{display:flex;align-items:center;gap:6px;padding:6px 8px;flex-wrap:wrap;}
.tv-badge{display:inline-flex;align-items:center;gap:3px;padding:2px 6px;border-radius:999px;background:rgba(255,255,255,.15);color:#fff;font-size:7.5px;font-weight:900;letter-spacing:.02em;text-transform:uppercase;white-space:nowrap;}
.tv-prize{display:inline-flex;align-items:center;gap:3px;padding:2px 7px;border-radius:999px;background:linear-gradient(90deg,#fbbf24,#f59e0b);color:#451a03;font-size:8.5px;font-weight:900;white-space:nowrap;}
.tv-time{margin-left:auto;display:inline-flex;align-items:center;gap:3px;font-size:9px;font-weight:900;color:#e0f2fe;font-variant-numeric:tabular-nums;white-space:nowrap;}
.tv-question{padding:6px 12px 10px;color:#fff;font-size:12.5px;font-weight:800;text-align:center;line-height:1.4;}
.tv-note{padding:0 10px 8px;text-align:center;color:#bae6fd;font-size:9px;font-weight:700;}
.tv-solved{padding:8px 10px;text-align:center;}
.tv-solved .tv-trophy{font-size:20px;display:inline-block;}
.tv-solved .tv-win-msg{color:#fff;font-size:11px;font-weight:900;line-height:1.3;margin-top:2px;}
.tv-solved .tv-answer{margin-top:2px;color:#d1fae5;font-size:9px;font-weight:700;letter-spacing:.03em;text-transform:uppercase;}
.tv-ended{padding:8px 10px;text-align:center;}
.tv-ended .tv-ended-icon{font-size:18px;}
.tv-ended .tv-ended-msg{color:#f4f4f5;font-size:11px;font-weight:900;line-height:1.3;margin-top:2px;}
.tv-ended .tv-answer{margin-top:2px;color:#d4d4d8;font-size:9px;font-weight:700;letter-spacing:.03em;text-transform:uppercase;}
</style>`

func renderTriviaPostCard(g *triviaGame, currentUser string) string {
	isExpired := g.Winner == "" && g.ExpiresAt != nil && time.Now().After(*g.ExpiresAt)

	if g.Winner != "" {
		return triviaCSS + fmt.Sprintf(`
<div id="post-notice-%d"></div>
<div id="trivia-%d" class="tv-card tv-card-won">
  <div class="tv-row"><span class="tv-badge">🧠 Site Trivia</span><span class="tv-prize">🏆 %d points</span></div>
  <div class="tv-solved">
    <span class="tv-trophy">🏆</span>
    <div class="tv-win-msg">%s answered correctly and won %d points!</div>
    <div class="tv-answer">Answer: %s</div>
  </div>
</div>`, g.PostID, g.PostID, g.PrizePoints,
			html.EscapeString(g.Winner), g.PrizePoints, strings.ToUpper(html.EscapeString(g.Answer)))
	}

	if isExpired {
		return triviaCSS + fmt.Sprintf(`
<div id="post-notice-%d"></div>
<div id="trivia-%d" class="tv-card tv-card-ended">
  <div class="tv-row"><span class="tv-badge">🧠 Site Trivia</span><span class="tv-time">⏰ Ended</span></div>
  <div class="tv-ended">
    <span class="tv-ended-icon">⌛</span>
    <div class="tv-ended-msg">Time's up — nobody got it!</div>
    <div class="tv-answer">Answer: %s</div>
  </div>
</div>`, g.PostID, g.PostID, strings.ToUpper(html.EscapeString(g.Answer)))
	}

	var remaining time.Duration
	if g.ExpiresAt != nil {
		remaining = time.Until(*g.ExpiresAt)
		if remaining < 0 {
			remaining = 0
		}
	}

	return triviaCSS + fmt.Sprintf(`
<div id="post-notice-%d"></div>
<div id="trivia-%d" class="tv-card">
  <div class="tv-row">
    <span class="tv-badge">🧠 Site Trivia</span>
    <span class="tv-prize">🏆 %d points</span>
    <span class="tv-time" id="tv-time-%d">%s left</span>
  </div>
  <div class="tv-question">%s</div>
  <div class="tv-note">💬 Post your answer to win! First correct answer takes it ⚡</div>
</div>`, g.PostID, g.PostID, g.PrizePoints, g.PostID, remaining.Round(time.Second).String(), html.EscapeString(g.Question))
}

package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// ─────────────────────────────────────────────────────────────────────────────
// DP (Display Picture) COMPETITION
// ─────────────────────────────────────────────────────────────────────────────
// Two users go head-to-head with their profile pictures. Other users vote
// ("Rate") for whichever picture they like more. When the competition ends
// (24h after creation), whoever has more votes wins the stake: the loser's
// points are deducted by the stake amount and given to the winner. A tie
// exchanges nothing.
//
// RULES ENFORCED:
//   1. Challenger must specify an opponent who is not themselves.
//   2. Stake must be between 1 and 1000 points (inclusive).
//   3. BOTH users must already have at least 1000 points BEFORE a
//      competition can start (so neither side can end up unable to pay).
//   4. BOTH users must have an uploaded profile picture (profile_image != '').
//   5. One vote per (competition, voter) — enforced by a UNIQUE constraint.
//   6. A user cannot vote on their own competition.
//   7. Resolution happens via a background worker every 5 minutes, checking
//      for competitions whose ends_at has passed and are still 'active'.
//
// Wire-up needed elsewhere (not done here, since those files aren't in scope):
//   - In your routes setup:
//       app.Get ("/dpcompete",              DPCompetePage)
//       app.Post("/dpcompete/create",        StartDPCompetitionHandler)
//       app.Post("/dpcompete/vote/:id/:side", VoteDPCompetitionHandler)
//       app.Get ("/dpcompete/section",       DPCompetitionsSectionHandler)
//   - In main(), after initDatabase():
//       initDPCompetitionSchema()
//       StartDPCompetitionWorker()
//   - In home.go's homeGet(), render DPCompetitionsSectionHTML() somewhere
//     near the top of the page (see comment at the bottom of this file for
//     the exact snippet + a "Start a DP Competition" entry point).
// ─────────────────────────────────────────────────────────────────────────────

const (
	dpMinStake           = 1
	dpMaxStake           = 1000
	dpMinPointsToCompete = 1000
	dpWorkerInterval     = 5 * time.Minute
)

// dpAllowedDurations are the only competition lengths a user can pick.
var dpAllowedDurations = map[int]time.Duration{
	1:  1 * time.Minute,
	3:  3 * time.Minute,
	5:  5 * time.Minute,
	10: 10 * time.Minute,
}

// ─────────────────────────────────────────────────────────────────────────────
// SCHEMA
// ─────────────────────────────────────────────────────────────────────────────

func initDPCompetitionSchema() {
	mustExec("create dp_competitions", `
		CREATE TABLE IF NOT EXISTS dp_competitions (
			id               SERIAL PRIMARY KEY,
			challenger       TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			opponent         TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			stake_points     INT  NOT NULL CHECK (stake_points >= 1 AND stake_points <= 1000),
			challenger_image TEXT NOT NULL DEFAULT '',
			opponent_image   TEXT NOT NULL DEFAULT '',
			challenger_votes INT  NOT NULL DEFAULT 0,
			opponent_votes   INT  NOT NULL DEFAULT 0,
			status           TEXT NOT NULL DEFAULT 'active', -- active | completed
			winner           TEXT NOT NULL DEFAULT '',        -- '', challenger username, opponent username, or 'tie'
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			ends_at          TIMESTAMPTZ NOT NULL,
			resolved_at      TIMESTAMPTZ,
			CHECK (challenger <> opponent)
		)`)

	mustExec("create dp_competition_votes", `
		CREATE TABLE IF NOT EXISTS dp_competition_votes (
			competition_id INT  NOT NULL REFERENCES dp_competitions(id) ON DELETE CASCADE,
			voter          TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			side           TEXT NOT NULL, -- 'challenger' | 'opponent'
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (competition_id, voter)
		)`)

	softExec("idx_dp_competitions_status_ends", `CREATE INDEX IF NOT EXISTS idx_dp_competitions_status_ends ON dp_competitions(status, ends_at)`)
	softExec("idx_dp_competitions_active", `CREATE INDEX IF NOT EXISTS idx_dp_competitions_active ON dp_competitions(status, created_at DESC)`)

	mustExec("add dp_competition_id to posts", `
		ALTER TABLE posts ADD COLUMN IF NOT EXISTS dp_competition_id INT NOT NULL DEFAULT 0`)
	softExec("idx_posts_dp_competition", `CREATE INDEX IF NOT EXISTS idx_posts_dp_competition ON posts(dp_competition_id) WHERE dp_competition_id > 0`)

	log.Println("✅ DP Competition schema ready")
}

// ─────────────────────────────────────────────────────────────────────────────
// CREATE
// ─────────────────────────────────────────────────────────────────────────────

// DPCompetePage renders a tiny picker: opponent username + stake amount.
func DPCompetePage(c *fiber.Ctx) error {
	user := getCurrentUser(c)
	if user == "" {
		return c.Redirect("/signup")
	}
	c.Set("Content-Type", "text/html")

	body := `
<div style="max-width:420px;margin:20px auto;padding:0 14px;">
  <div style="background:var(--post-bg);border:1px solid var(--post-border);border-radius:20px;overflow:hidden;">
    <div style="padding:16px;border-bottom:1px solid var(--post-border);">
      <h1 style="font-size:17px;font-weight:900;color:var(--post-text);margin:0;">🖼️ Start a DP Competition</h1>
      <p style="font-size:11px;color:#9ca3af;margin:4px 0 0;">Challenge someone's profile picture. Winner takes the stake.</p>
    </div>
    <form hx-post="/dpcompete/create" hx-target="#dp-error" hx-swap="innerHTML" style="padding:16px;">
      <div id="dp-error"></div>
      <label style="font-size:11px;font-weight:800;color:var(--post-text);display:block;margin-bottom:4px;">Opponent's username</label>
      <input type="text" name="opponent" required placeholder="e.g. arthas"
        style="width:100%;box-sizing:border-box;padding:10px 12px;border-radius:10px;border:1.5px solid var(--post-border);background:transparent;color:var(--post-text);font-size:13px;margin-bottom:12px;">
      <label style="font-size:11px;font-weight:800;color:var(--post-text);display:block;margin-bottom:4px;">Stake (1–1000 points)</label>
      <input type="number" name="stake_points" required min="1" max="1000" value="100"
        style="width:100%;box-sizing:border-box;padding:10px 12px;border-radius:10px;border:1.5px solid var(--post-border);background:transparent;color:var(--post-text);font-size:13px;margin-bottom:12px;">
      <label style="font-size:11px;font-weight:800;color:var(--post-text);display:block;margin-bottom:4px;">Duration</label>
      <select name="duration_minutes" required
        style="width:100%;box-sizing:border-box;padding:10px 12px;border-radius:10px;border:1.5px solid var(--post-border);background:transparent;color:var(--post-text);font-size:13px;margin-bottom:14px;">
        <option value="1">1 minute</option>
        <option value="3">3 minutes</option>
        <option value="5" selected>5 minutes</option>
        <option value="10">10 minutes</option>
      </select>
      <button type="submit" style="width:100%;padding:11px;background:linear-gradient(135deg,#ff6b00,#ff0040);color:#fff;border:none;border-radius:12px;font-weight:800;font-size:13px;cursor:pointer;">
        🔥 Start Competition
      </button>
    </form>
  </div>
</div>`

	return c.Type("html").SendString(Layout("DP Competition", body, user))
}

// StartDPCompetitionHandler validates everything and creates the competition.
func StartDPCompetitionHandler(c *fiber.Ctx) error {
	challenger := getCurrentUser(c)
	if challenger == "" {
		return c.Status(401).SendString(`<p style="color:#dc2626;font-size:12px;">You must be logged in.</p>`)
	}

	opponent := strings.TrimSpace(c.FormValue("opponent"))
	stake, convErr := strconv.Atoi(strings.TrimSpace(c.FormValue("stake_points")))
	if convErr != nil {
		stake = 0
	}
	durationMinutes, durConvErr := strconv.Atoi(strings.TrimSpace(c.FormValue("duration_minutes")))
	if durConvErr != nil {
		durationMinutes = 0
	}

	errMsg := func(msg string) error {
		return c.Type("html").SendString(fmt.Sprintf(`<p style="color:#dc2626;font-size:12px;font-weight:700;padding:6px 0;">%s</p>`, msg))
	}

	if opponent == "" {
		return errMsg("Please enter an opponent's username.")
	}
	if strings.EqualFold(opponent, challenger) {
		return errMsg("You can't challenge yourself.")
	}
	if stake < dpMinStake || stake > dpMaxStake {
		return errMsg(fmt.Sprintf("Stake must be between %d and %d points.", dpMinStake, dpMaxStake))
	}
	duration, validDuration := dpAllowedDurations[durationMinutes]
	if !validDuration {
		return errMsg("Please choose a valid duration: 1, 3, 5, or 10 minutes.")
	}

	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var challengerPoints, opponentPoints int
	var challengerImg, opponentImg string

	if err := db.QueryRow(rctx,
		`SELECT points, COALESCE(profile_image,'') FROM users WHERE username = $1`, challenger,
	).Scan(&challengerPoints, &challengerImg); err != nil {
		return errMsg("Could not load your account.")
	}

	if err := db.QueryRow(rctx,
		`SELECT points, COALESCE(profile_image,'') FROM users WHERE username = $1`, opponent,
	).Scan(&opponentPoints, &opponentImg); err != nil {
		return errMsg(fmt.Sprintf("User @%s not found.", opponent))
	}

	if challengerImg == "" {
		return errMsg("You need to upload a profile picture before competing.")
	}
	if opponentImg == "" {
		return errMsg(fmt.Sprintf("@%s hasn't uploaded a profile picture yet, so they can't compete.", opponent))
	}
	if challengerPoints < dpMinPointsToCompete {
		return errMsg(fmt.Sprintf("You need at least %d points to start a DP competition (you have %d).", dpMinPointsToCompete, challengerPoints))
	}
	if opponentPoints < dpMinPointsToCompete {
		return errMsg(fmt.Sprintf("@%s needs at least %d points to be challenged (they have %d).", opponent, dpMinPointsToCompete, opponentPoints))
	}

	// Prevent duplicate active competitions between the same two users.
	var alreadyActive bool
	db.QueryRow(rctx, `
		SELECT EXISTS(
			SELECT 1 FROM dp_competitions
			WHERE status = 'active'
			  AND ((challenger = $1 AND opponent = $2) OR (challenger = $2 AND opponent = $1))
		)`, challenger, opponent).Scan(&alreadyActive)
	if alreadyActive {
		return errMsg(fmt.Sprintf("There's already an active competition between you and @%s.", opponent))
	}

	endsAt := time.Now().Add(duration)

	var newID int
	err := db.QueryRow(rctx, `
		INSERT INTO dp_competitions
			(challenger, opponent, stake_points, challenger_image, opponent_image, status, ends_at)
		VALUES ($1,$2,$3,$4,$5,'active',$6)
		RETURNING id`,
		challenger, opponent, stake, challengerImg, opponentImg, endsAt,
	).Scan(&newID)
	if err != nil {
		log.Printf("StartDPCompetitionHandler insert error: %v", err)
		return errMsg("Something went wrong creating the competition. Try again.")
	}

	// Create a real post on EACH user's profile so the competition shows
	// up in their feed/profile like any other post.
	postCaption := fmt.Sprintf("🖼️ DP Competition: @%s vs @%s — %d points on the line!", challenger, opponent, stake)

	var challengerPostID, opponentPostID int
	if err := db.QueryRow(rctx, `
		INSERT INTO posts (author, content, category, dp_competition_id)
		VALUES ($1, $2, 'DP Competition', $3) RETURNING id`,
		challenger, postCaption, newID,
	).Scan(&challengerPostID); err != nil {
		log.Printf("StartDPCompetitionHandler challenger post insert error: %v", err)
	}

	if err := db.QueryRow(rctx, `
		INSERT INTO posts (author, content, category, dp_competition_id)
		VALUES ($1, $2, 'DP Competition', $3) RETURNING id`,
		opponent, postCaption, newID,
	).Scan(&opponentPostID); err != nil {
		log.Printf("StartDPCompetitionHandler opponent post insert error: %v", err)
	}

	// Notify the opponent, linking to their copy of the post.
	db.Exec(rctx, `
		INSERT INTO notifications (recipient, sender, type, content, post_id)
		VALUES ($1, $2, 'dp_competition', $3, $4)`,
			opponent, challenger, fmt.Sprintf("started a DP competition with you for %d points!", stake), opponentPostID)

	Push(opponent, challenger,
		fmt.Sprintf("started a DP competition with you for %d points!", stake),
		postLink(opponentPostID))

	c.Set("HX-Redirect", "/")
	return c.SendStatus(fiber.StatusOK)
}

// ─────────────────────────────────────────────────────────────────────────────
// VOTE ("Rate")
// ─────────────────────────────────────────────────────────────────────────────

// VoteDPCompetitionHandler — POST /dpcompete/vote/:id/:side  (side = challenger|opponent)
func VoteDPCompetitionHandler(c *fiber.Ctx) error {
	voter := getCurrentUser(c)
	if voter == "" {
		return c.Status(401).SendString("Unauthorized")
	}

	id := c.Params("id")
	side := c.Params("side")
	if side != "challenger" && side != "opponent" {
		return c.Status(400).SendString("Invalid side")
	}

	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var challenger, opponent, status string
	if err := db.QueryRow(rctx,
		`SELECT challenger, opponent, status FROM dp_competitions WHERE id = $1`, id,
	).Scan(&challenger, &opponent, &status); err != nil {
		return c.Status(404).SendString("Competition not found")
	}

	if status != "active" {
		return c.Status(400).SendString("This competition has ended")
	}
	if voter == challenger || voter == opponent {
		return c.Status(403).SendString("You can't vote on your own competition")
	}

	_, err := db.Exec(rctx, `
		INSERT INTO dp_competition_votes (competition_id, voter, side)
		VALUES ($1, $2, $3)
		ON CONFLICT (competition_id, voter) DO NOTHING`,
		id, voter, side,
	)
	if err != nil {
		log.Printf("VoteDPCompetitionHandler insert error: %v", err)
		return c.Status(500).SendString("Error recording vote")
	}

	col := "challenger_votes"
	if side == "opponent" {
		col = "opponent_votes"
	}
	db.Exec(rctx, fmt.Sprintf(`
		UPDATE dp_competitions SET %s = (
			SELECT COUNT(*) FROM dp_competition_votes WHERE competition_id = $1 AND side = $2
		) WHERE id = $1`, col), id, side)

	// Re-render the card so the vote counts update in place — using the
	// exact same inner renderer as the initial post render, so the design
	// never changes after voting.
	comp, err := fetchDPCompetition(id)
	if err != nil {
		return c.SendStatus(200)
	}
	return c.Type("html").SendString(renderDPCompetitionInner(comp, voter))
}

// ─────────────────────────────────────────────────────────────────────────────
// FORFEIT ("Give Up") — a participant can concede before time runs out.
// The forfeiting side immediately loses the full stake to the other side;
// the competition is marked completed right away instead of waiting for
// the worker's next tick.
// ─────────────────────────────────────────────────────────────────────────────

func ForfeitDPCompetitionHandler(c *fiber.Ctx) error {
	user := getCurrentUser(c)
	if user == "" {
		return c.Status(401).SendString("Unauthorized")
	}

	id := c.Params("id")

	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var challenger, opponent, status string
	var stake int
	if err := db.QueryRow(rctx,
		`SELECT challenger, opponent, status, stake_points FROM dp_competitions WHERE id = $1`, id,
	).Scan(&challenger, &opponent, &status, &stake); err != nil {
		return c.Status(404).SendString("Competition not found")
	}

	if status != "active" {
		return c.Status(400).SendString("This competition already ended")
	}
	if user != challenger && user != opponent {
		return c.Status(403).SendString("You're not part of this competition")
	}

	loser := user
	winner := challenger
	if user == challenger {
		winner = opponent
	}

	db.Exec(rctx, `UPDATE users SET points = GREATEST(points - $1, 0) WHERE username = $2`, stake, loser)
	db.Exec(rctx, `UPDATE users SET points = points + $1 WHERE username = $2`, stake, winner)

	db.Exec(rctx, `
		UPDATE dp_competitions
		SET status = 'completed', winner = $1, resolved_at = NOW()
		WHERE id = $2`, winner, id)

	db.Exec(rctx, `
		INSERT INTO notifications (recipient, sender, type, content)
		VALUES ($1, $2, 'dp_competition_result', $3)`,
		winner, loser, fmt.Sprintf("@%s gave up! You won %d points", loser, stake))
	db.Exec(rctx, `
		INSERT INTO notifications (recipient, sender, type, content)
		VALUES ($1, $2, 'dp_competition_result', $3)`,
			loser, winner, fmt.Sprintf("You gave up the DP competition. -%d points", stake))

	Push(winner, loser, fmt.Sprintf("gave up! You won %d points 🏆", stake), siteBase+"/notifications")
	Push(loser, winner, fmt.Sprintf("You gave up the DP competition. -%d points", stake), siteBase+"/notifications")

	log.Printf("DP Competition #%s forfeited by @%s — @%s wins %d points", id, loser, winner, stake)

	comp, err := fetchDPCompetition(id)
	if err != nil {
		return c.SendStatus(200)
	}
	return c.Type("html").SendString(renderDPCompetitionInner(comp, user))
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE — either participant can permanently remove a competition, same as
// deletePostHandler does for a normal post. This is a hard delete: no points
// are transferred (unlike forfeit), the row and its votes are gone, and both
// announcement posts (challenger's + opponent's copies) are removed too.
// ─────────────────────────────────────────────────────────────────────────────

func DeleteDPCompetitionHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Status(401).SendString("Unauthorized")
	}

	id := c.Params("id")

	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var challenger, opponent string
	if err := db.QueryRow(rctx,
		`SELECT challenger, opponent FROM dp_competitions WHERE id = $1`, id,
	).Scan(&challenger, &opponent); err != nil {
		return c.Status(404).SendString("Competition not found")
	}

	if currentUser != challenger && currentUser != opponent {
		return c.Status(403).SendString("You can only delete your own DP competitions")
	}

	// Remove both announcement posts first (posts.dp_competition_id has no FK
	// to dp_competitions, so it won't cascade automatically).
	db.Exec(rctx, `DELETE FROM posts WHERE dp_competition_id = $1`, id)

	if _, err := db.Exec(rctx, `DELETE FROM dp_competitions WHERE id = $1`, id); err != nil {
		log.Printf("DeleteDPCompetitionHandler delete error: %v", err)
		return c.Status(500).SendString("Error deleting competition")
	}

	log.Printf("DP Competition #%s deleted by @%s", id, currentUser)
	return c.SendStatus(200)
}

// ─────────────────────────────────────────────────────────────────────────────
// RESOLUTION WORKER — transfers points when a competition's time is up
// ─────────────────────────────────────────────────────────────────────────────

func StartDPCompetitionWorker() {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("PANIC in DP competition worker: %v — restarting in 5s", r)
				time.Sleep(5 * time.Second)
				StartDPCompetitionWorker()
			}
		}()
		resolveExpiredDPCompetitions()
		ticker := time.NewTicker(dpWorkerInterval)
		defer ticker.Stop()
		for range ticker.C {
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("PANIC in resolveExpiredDPCompetitions: %v", r)
					}
				}()
				resolveExpiredDPCompetitions()
			}()
		}
	}()
	log.Println("DP Competition worker started (checks every 5 min)")
}

func resolveExpiredDPCompetitions() {
	rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	rows, err := db.Query(rctx, `
		SELECT id, challenger, opponent, stake_points, challenger_votes, opponent_votes
		FROM dp_competitions
		WHERE status = 'active' AND ends_at <= NOW()`)
	if err != nil {
		log.Printf("resolveExpiredDPCompetitions query error: %v", err)
		return
	}

	type row struct {
		id                            int
		challenger, opponent          string
		stake, challengerV, opponentV int
	}
	var toResolve []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.challenger, &r.opponent, &r.stake, &r.challengerV, &r.opponentV); err == nil {
			toResolve = append(toResolve, r)
		}
	}
	rows.Close()

	for _, r := range toResolve {
		winner, loser := "", ""
		switch {
		case r.challengerV > r.opponentV:
			winner, loser = r.challenger, r.opponent
		case r.opponentV > r.challengerV:
			winner, loser = r.opponent, r.challenger
		default:
			winner = "tie"
		}

		func() {
			rctx2, cancel2 := context.WithTimeout(ctx, 10*time.Second)
			defer cancel2()

			if winner != "tie" {
				// Deduct from loser (clamped at 0 via the existing points_non_negative
				// CHECK constraint — GREATEST guards against a negative UPDATE erroring out).
				db.Exec(rctx2, `UPDATE users SET points = GREATEST(points - $1, 0) WHERE username = $2`, r.stake, loser)
				db.Exec(rctx2, `UPDATE users SET points = points + $1 WHERE username = $2`, r.stake, winner)

				db.Exec(rctx2, `
					INSERT INTO notifications (recipient, sender, type, content)
					VALUES ($1, $2, 'dp_competition_result', $3)`,
					winner, loser, fmt.Sprintf("You won the DP competition! +%d points", r.stake))
				db.Exec(rctx2, `
					INSERT INTO notifications (recipient, sender, type, content)
					VALUES ($1, $2, 'dp_competition_result', $3)`,
					loser, winner, fmt.Sprintf("You lost the DP competition. -%d points", r.stake))

				Push(winner, loser, fmt.Sprintf("You won the DP competition! +%d points 🏆", r.stake), siteBase+"/notifications")
				Push(loser, winner, fmt.Sprintf("You lost the DP competition. -%d points", r.stake), siteBase+"/notifications")
			}

			db.Exec(rctx2, `
				UPDATE dp_competitions
				SET status = 'completed', winner = $1, resolved_at = NOW()
				WHERE id = $2`, winner, r.id)

			log.Printf("DP Competition #%d resolved: winner=%s (challenger=%d votes, opponent=%d votes, stake=%d)",
				r.id, winner, r.challengerV, r.opponentV, r.stake)
		}()
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// FETCH + RENDER
// ─────────────────────────────────────────────────────────────────────────────

type dpCompetition struct {
	id                             int
	challenger, opponent           string
	challengerImg, opponentImg     string
	stake                          int
	challengerVotes, opponentVotes int
	status, winner                 string
	createdAt, endsAt              time.Time
}

func fetchDPCompetition(id string) (dpCompetition, error) {
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var d dpCompetition
	err := db.QueryRow(rctx, `
		SELECT id, challenger, opponent, challenger_image, opponent_image,
		       stake_points, challenger_votes, opponent_votes, status, winner, created_at, ends_at
		FROM dp_competitions WHERE id = $1`, id,
	).Scan(&d.id, &d.challenger, &d.opponent, &d.challengerImg, &d.opponentImg,
		&d.stake, &d.challengerVotes, &d.opponentVotes, &d.status, &d.winner, &d.createdAt, &d.endsAt)
	return d, err
}

// DPCompetitionsSectionHandler — GET /dpcompete/section
// Returns the horizontal-scroll section of active competitions, meant to be
// polled/refreshed the same way the online-slider is on the home page.
func DPCompetitionsSectionHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	return c.Type("html").SendString(DPCompetitionsSectionHTML(currentUser))
}

func DPCompetitionsSectionHTML(currentUser string) string {
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := db.Query(rctx, `
		SELECT id, challenger, opponent, challenger_image, opponent_image,
		       stake_points, challenger_votes, opponent_votes, status, winner, created_at, ends_at
		FROM dp_competitions
		ORDER BY created_at DESC
		LIMIT 50`)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var cards strings.Builder
	count := 0
	for rows.Next() {
		var d dpCompetition
		if err := rows.Scan(&d.id, &d.challenger, &d.opponent, &d.challengerImg, &d.opponentImg,
			&d.stake, &d.challengerVotes, &d.opponentVotes, &d.status, &d.winner, &d.createdAt, &d.endsAt); err != nil {
			continue
		}
		cards.WriteString(fmt.Sprintf(
			`<div id="dpsection-post-%d" class="post-card mb-4 overflow-hidden rounded-2xl bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800">%s</div>`,
			d.id, renderDPCompetitionInner(d, currentUser),
		))
		count++
	}

	if count == 0 {
		return `
<div class="home-card" style="padding:14px;text-align:center;">
  <p style="font-size:11px;color:#9ca3af;font-weight:700;margin:0 0 8px;">No DP competitions running right now.</p>
  <a href="/dpcompete" style="display:inline-block;padding:8px 16px;background:linear-gradient(135deg,#ff6b00,#ff0040);color:#fff;border-radius:999px;font-size:11px;font-weight:800;text-decoration:none;">🖼️ Start a DP Competition</a>
</div>`
	}

	return cards.String()
}

// renderDPCompetitionCard renders one competition as a post-styled card:
// two profile pictures side by side, live vote counts, a stake badge, and a
// Rate button under each side. If the current viewer is one of the two
// competitors, both Rate buttons are disabled (can't vote on your own).
func renderDPCompetitionCard(d dpCompetition, currentUser string) string {
	timeLeftStr := computeDPTimeLeftStr(d)

	isParticipant := currentUser == d.challenger || currentUser == d.opponent
	canVote := currentUser != "" && !isParticipant && d.status == "active"

	voteBtn := func(side, username string) string {
		if !canVote {
			return `<div style="font-size:9px;font-weight:700;color:#9ca3af;padding:5px;">—</div>`
		}
		return fmt.Sprintf(
			`<button hx-post="/dpcompete/vote/%d/%s" hx-target="#dpcomp-%d" hx-swap="outerHTML" style="width:100%%;padding:5px 0;border-radius:8px;border:1.5px solid #ff6b00;background:transparent;color:#ff6b00;font-size:9px;font-weight:800;cursor:pointer;">⭐ Rate</button>`,
			d.id, side, d.id,
		)
	}

	totalVotes := d.challengerVotes + d.opponentVotes
	chalPct, oppPct := 50, 50
	if totalVotes > 0 {
		chalPct = d.challengerVotes * 100 / totalVotes
		oppPct = 100 - chalPct
	}

	winnerBanner := ""
	if d.status == "completed" {
		switch d.winner {
		case "tie":
			winnerBanner = `<div style="text-align:center;font-size:9px;font-weight:800;color:#9ca3af;padding:4px 0 2px;">🤝 Tie — no points exchanged</div>`
		default:
			winnerBanner = fmt.Sprintf(`<div style="text-align:center;font-size:9px;font-weight:800;color:#16a34a;padding:4px 0 2px;">🏆 @%s won %d points</div>`, d.winner, d.stake)
		}
	}

	giveUpHTML := ""
	if isParticipant && d.status == "active" {
		giveUpHTML = fmt.Sprintf(`
  <div style="padding:0 8px 8px;">
    <button hx-post="/dpcompete/forfeit/%d" hx-target="#dpcomp-%d" hx-swap="outerHTML"
            hx-confirm="Give up? You'll lose %d points to your opponent."
            style="width:100%%;padding:6px 0;border-radius:8px;border:1.5px solid #dc2626;background:transparent;color:#dc2626;font-size:9px;font-weight:800;cursor:pointer;">
      🏳️ Give Up
    </button>
  </div>`, d.id, d.id, d.stake)
	}

	// Live countdown — ticks client-side so short competitions count down in
	// seconds instead of sitting frozen. Never removes the card: once time's
	// up it just relabels the badge to "Expired" and leaves everything else
	// in place, since this card is now a permanent post, not a temporary
	// pinned home widget.
	timeLeftScript := ""
	if d.status == "active" {
		timeLeftScript = fmt.Sprintf(`
<script>
(function(){
  var endsAt = %d;
  var el = document.getElementById('dp-timeleft-%d');
  var iv = setInterval(render, 1000);
  function render(){
    if(!el){ clearInterval(iv); return; }
    var diff = endsAt - Date.now();
    if(diff <= 0){
      el.textContent = 'Expired';
      clearInterval(iv);
      return;
    }
    var totalSec = Math.floor(diff/1000);
    var h = Math.floor(totalSec/3600);
    var m = Math.floor((totalSec %% 3600)/60);
    var s = totalSec %% 60;
    if(h>0) el.textContent = h+'h '+m+'m left';
    else if(m>0) el.textContent = m+'m '+s+'s left';
    else el.textContent = s+'s left';
  }
  render();
})();
</script>`, d.endsAt.UnixMilli(), d.id)
	}

	return fmt.Sprintf(`
<div id="dpcomp-%d" style="flex-shrink:0;width:180px;background:var(--post-bg);border:1.5px solid var(--post-border);border-radius:16px;overflow:hidden;">
  <div style="padding:6px 8px;background:linear-gradient(135deg,#ff6b00,#ff0040);">
    <div style="display:flex;justify-content:space-between;align-items:center;">
      <span style="font-size:9px;font-weight:900;color:#fff;">🔥 %d PTS</span>
      <span id="dp-timeleft-%d" style="font-size:8px;font-weight:700;color:rgba(255,255,255,.85);">%s</span>
    </div>
    <span style="font-size:7.5px;font-weight:700;color:rgba(255,255,255,.8);">by <a href="/profile/%s" style="color:#fff;font-weight:800;text-decoration:underline;" onclick="event.stopPropagation();">%s</a></span>
  </div>  <div style="display:flex;">
    <div style="flex:1;position:relative;">
      <img src="%s" style="width:100%%;height:90px;object-fit:cover;display:block;" alt="%s">
      <div style="position:absolute;bottom:0;left:0;right:0;background:rgba(0,0,0,.55);padding:2px 4px;">
        <span style="font-size:8px;font-weight:800;color:#fff;">@%s</span>
      </div>
    </div>
    <div style="flex:1;position:relative;">
      <img src="%s" style="width:100%%;height:90px;object-fit:cover;display:block;" alt="%s">
      <div style="position:absolute;bottom:0;left:0;right:0;background:rgba(0,0,0,.55);padding:2px 4px;text-align:right;">
        <span style="font-size:8px;font-weight:800;color:#fff;">@%s</span>
      </div>
    </div>
  </div>
  <div style="display:flex;height:5px;">
    <div style="width:%d%%;background:#3b82f6;"></div>
    <div style="width:%d%%;background:#ec4899;"></div>
  </div>
  <div style="display:flex;padding:6px 8px 4px;gap:6px;">
    <div style="flex:1;text-align:center;font-size:9px;font-weight:800;color:var(--post-text);">%d votes</div>
    <div style="flex:1;text-align:center;font-size:9px;font-weight:800;color:var(--post-text);">%d votes</div>
  </div>
  <div style="display:flex;padding:0 8px 8px;gap:6px;">
    <div style="flex:1;">%s</div>
    <div style="flex:1;">%s</div>
  </div>
  %s
  %s
</div>
%s`,
		d.id,
		d.stake, d.id, timeLeftStr, d.challenger, d.challenger,
		d.challengerImg, d.challenger, d.challenger, d.opponentImg, d.opponent, d.opponent,
		chalPct, oppPct,
		d.challengerVotes, d.opponentVotes,
		voteBtn("challenger", d.challenger), voteBtn("opponent", d.opponent),
		giveUpHTML,
		winnerBanner,
		timeLeftScript,
	)
}

// computeDPTimeLeftStr is the server-rendered fallback shown before the
// client-side ticker takes over (and to non-JS clients). Anything under a
// minute counts down in seconds instead of sitting at "0m left".
func computeDPTimeLeftStr(d dpCompetition) string {
	if d.status != "active" {
		return "Expired"
	}
	timeLeft := time.Until(d.endsAt)
	if timeLeft <= 0 {
		return "Expired"
	}
	switch {
	case timeLeft < time.Minute:
		return fmt.Sprintf("%ds left", int(timeLeft.Seconds()))
	case timeLeft < time.Hour:
		m := int(timeLeft.Minutes())
		s := int(timeLeft.Seconds()) % 60
		return fmt.Sprintf("%dm %ds left", m, s)
	default:
		h := int(timeLeft.Hours())
		m := int(timeLeft.Minutes()) % 60
		return fmt.Sprintf("%dh %dm left", h, m)
	}
}

// buildDPVotersList lists every voter and which side (username) they voted
// for, most recent first. Shown as a collapsible list under the vote counts
// so the card stays compact until someone wants to see who voted for whom.
func buildDPVotersList(d dpCompetition) string {
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := db.Query(rctx, `
		SELECT voter, side FROM dp_competition_votes
		WHERE competition_id = $1
		ORDER BY created_at DESC`, d.id)
	if err != nil {
		return `<p style="font-size:10px;color:#9ca3af;font-style:italic;text-align:center;padding:6px;margin:0;">No votes yet</p>`
	}
	defer rows.Close()

	var sb strings.Builder
	hasItems := false
	for rows.Next() {
		var voter, side string
		if err := rows.Scan(&voter, &side); err != nil {
			continue
		}
		hasItems = true
		votedFor := d.opponent
		color := "#ec4899"
		if side == "challenger" {
			votedFor = d.challenger
			color = "#3b82f6"
		}
		sb.WriteString(fmt.Sprintf(`
<div style="display:flex;align-items:center;justify-content:center;gap:4px;font-size:10.5px;font-weight:700;color:var(--post-text);padding:3px 0;">
  <a href="/profile/%s" style="color:var(--post-text);font-weight:800;text-decoration:none;">@%s</a>
  <span style="color:#9ca3af;font-weight:600;">rated</span>
  <span style="color:%s;font-weight:800;">@%s</span>
</div>`, voter, voter, color, votedFor))
	}

	if !hasItems {
		return `<p style="font-size:10px;color:#9ca3af;font-style:italic;text-align:center;padding:6px;margin:0;">No votes yet</p>`
	}
	return sb.String()
}

// renderDPCompetitionInner renders just the #dpcomp-X content (everything
// that gets swapped when the Rate/Give Up buttons fire hx-swap="outerHTML"
// on that id). Both the initial full post render AND the vote/forfeit
// handlers use this exact function, so the card never "reverts" to a
// different layout after voting — there is only one design, used
// everywhere. Scripts live inside this div (not outside it) so they still
// run correctly after a partial outerHTML swap.
//
// UX notes:
//   - Images are square (aspect-ratio:1/1) and scale with card width, so on
//     mobile they always fill the screen width without a fixed pixel height.
//   - Tapping a profile picture opens it full-size in a lightbox; tapping
//     the username still navigates to that user's profile.
//   - Once the viewer has voted, both Rate buttons disappear (matching a
//     normal reaction button that hides after you've already reacted).
//   - Vote counts are backed by a "who voted for who" toggle.
func renderDPCompetitionInner(d dpCompetition, currentUser string) string {
	timeLeftStr := computeDPTimeLeftStr(d)

	isParticipant := currentUser == d.challenger || currentUser == d.opponent

	var hasVoted bool
	var votedSide string
	if currentUser != "" && !isParticipant {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		db.QueryRow(rctx, `SELECT side FROM dp_competition_votes WHERE competition_id=$1 AND voter=$2`, d.id, currentUser).Scan(&votedSide)
		cancel()
		hasVoted = votedSide != ""
	}

	canVote := currentUser != "" && !isParticipant && d.status == "active" && !hasVoted

	voteBtn := func(side string) string {
		if !canVote {
			return ""
		}
		return fmt.Sprintf(
			`<button hx-post="/dpcompete/vote/%d/%s" hx-target="#dpcomp-%d" hx-swap="outerHTML" class="action-btn" style="color:#ff6b00;border-color:#ff6b00;"><span>⭐</span><span>Rate</span></button>`,
			d.id, side, d.id,
		)
	}

	votedNoteHTML := ""
	if hasVoted {
		votedFor := d.opponent
		if votedSide == "challenger" {
			votedFor = d.challenger
		}
		votedNoteHTML = fmt.Sprintf(`<div style="font-size:10.5px;font-weight:800;color:#9ca3af;padding:4px 0;">✓ You rated @%s</div>`, votedFor)
	}

	totalVotes := d.challengerVotes + d.opponentVotes
	chalPct, oppPct := 50, 50
	if totalVotes > 0 {
		chalPct = d.challengerVotes * 100 / totalVotes
		oppPct = 100 - chalPct
	}

	winnerBanner := ""
	if d.status == "completed" {
		switch d.winner {
		case "tie":
			winnerBanner = `<div style="text-align:center;font-size:11px;font-weight:800;color:#9ca3af;padding:6px 0 2px;">🤝 Tie — no points exchanged</div>`
		default:
			winnerBanner = fmt.Sprintf(`<div style="text-align:center;font-size:11px;font-weight:800;color:#16a34a;padding:6px 0 2px;">🏆 @%s won %d points</div>`, d.winner, d.stake)
		}
	}

	giveUpHTML := ""
	if isParticipant && d.status == "active" {
		giveUpHTML = fmt.Sprintf(`
    <button hx-post="/dpcompete/forfeit/%d" hx-target="#dpcomp-%d" hx-swap="outerHTML"
            hx-confirm="Give up? You'll lose %d points to your opponent."
            class="action-btn" style="color:#dc2626;border-color:#dc2626;">
      <span>🏳️</span><span>Give Up</span>
    </button>`, d.id, d.id, d.stake)
	}

	// Delete — available to either participant regardless of status (active,
	// completed, or expired), same as a normal post's Delete button. Targets
	// "closest .post-card" via htmx so it removes the whole card no matter
	// which outer wrapper id is used (feed post-%d, profile dpprofile-post-%d,
	// or the DP Battle tab's dpsection-post-%d).
	deleteHTML := ""
	if isParticipant {
		deleteHTML = fmt.Sprintf(`
    <button hx-delete="/dpcompete/delete/%d" hx-target="closest .post-card" hx-swap="outerHTML"
            hx-confirm="Delete this DP competition? This cannot be undone."
            class="action-btn btn-delete">
      <span>🗑</span><span>Delete</span>
    </button>`, d.id)
	}

	votersSectionHTML := ""
	if totalVotes > 0 {
		votersSectionHTML = fmt.Sprintf(`
    <div style="padding-top:8px;margin-top:4px;border-top:1px solid var(--post-border);text-align:center;">
      <button type="button" onclick="var v=document.getElementById('dp-voters-%d');v.classList.toggle('hidden');" style="display:inline-flex;align-items:center;gap:8px;padding:5px 14px;border-radius:999px;background:linear-gradient(180deg,rgba(0,0,0,0.02),rgba(0,0,0,0.05));border:1px solid var(--post-border);cursor:pointer;font-size:10.5px;font-weight:800;color:var(--post-text);">
        <span>👥 %d votes</span><span style="opacity:.6;">tap to see who</span>
      </button>
      <div id="dp-voters-%d" class="hidden" style="margin-top:6px;">%s</div>
    </div>`, d.id, totalVotes, d.id, buildDPVotersList(d))
	}

	timeLeftScript := ""
	if d.status == "active" {
		timeLeftScript = fmt.Sprintf(`
<script>
(function(){
  var endsAt = %d;
  var el = document.getElementById('dp-timeleft-%d');
  var iv = setInterval(render, 1000);
  function render(){
    if(!el){ clearInterval(iv); return; }
    var diff = endsAt - Date.now();
    if(diff <= 0){
      el.textContent = 'Expired';
      clearInterval(iv);
      return;
    }
    var totalSec = Math.floor(diff/1000);
    var h = Math.floor(totalSec/3600);
    var m = Math.floor((totalSec %% 3600)/60);
    var s = totalSec %% 60;
    if(h>0) el.textContent = h+'h '+m+'m left';
    else if(m>0) el.textContent = m+'m '+s+'s left';
    else el.textContent = s+'s left';
  }
  render();
})();
</script>`, d.endsAt.UnixMilli(), d.id)
	}

	lightboxScript := `
<script>
window.dpShowFullImage = window.dpShowFullImage || function(src, alt){
  var ov = document.createElement('div');
  ov.setAttribute('style','position:fixed;inset:0;z-index:9999;background:rgba(0,0,0,.9);display:flex;align-items:center;justify-content:center;padding:20px;box-sizing:border-box;cursor:zoom-out;');
  var img = document.createElement('img');
  img.src = src;
  img.alt = alt || '';
  img.setAttribute('style','max-width:100%;max-height:100%;object-fit:contain;border-radius:12px;box-shadow:0 10px 40px rgba(0,0,0,.5);');
  ov.appendChild(img);
  ov.onclick = function(){ document.body.removeChild(ov); };
  document.body.appendChild(ov);
};
</script>`

	return fmt.Sprintf(`
<div id="dpcomp-%d">
  <div style="display:flex;flex-direction:column;gap:2px;padding:8px 14px;background:linear-gradient(90deg,#ff6b00,#ff0040);">
      <div style="display:flex;justify-content:space-between;align-items:center;">
        <span style="font-size:11px;font-weight:900;color:#fff;text-transform:uppercase;letter-spacing:.03em;">🖼️ DP Competition · %d Points</span>
        <span id="dp-timeleft-%d" style="font-size:10px;font-weight:800;color:rgba(255,255,255,.9);white-space:nowrap;">%s</span>
      </div>
      <span style="font-size:9px;font-weight:700;color:rgba(255,255,255,.85);">Started by <a href="/profile/%s" style="color:#fff;font-weight:800;text-decoration:underline;">%s</a></span>
    </div>    <div style="display:flex;width:100%%;">
      <div style="flex:1;min-width:0;position:relative;">
        <img src="%s" onclick="window.dpShowFullImage('%s','%s')" loading="lazy"
             style="width:100%%;aspect-ratio:1/1;object-fit:cover;display:block;cursor:zoom-in;" alt="%s">
        <div style="position:absolute;bottom:0;left:0;right:0;background:linear-gradient(0deg,rgba(0,0,0,.75),rgba(0,0,0,0));padding:16px 8px 6px;">
          <a href="/profile/%s" style="font-size:12px;font-weight:800;color:#fff;text-decoration:none;">@%s</a>
        </div>
      </div>
      <div style="flex:1;min-width:0;position:relative;border-left:2px solid var(--post-bg);">
        <img src="%s" onclick="window.dpShowFullImage('%s','%s')" loading="lazy"
             style="width:100%%;aspect-ratio:1/1;object-fit:cover;display:block;cursor:zoom-in;" alt="%s">
        <div style="position:absolute;bottom:0;left:0;right:0;background:linear-gradient(0deg,rgba(0,0,0,.75),rgba(0,0,0,0));padding:16px 8px 6px;text-align:right;">
          <a href="/profile/%s" style="font-size:12px;font-weight:800;color:#fff;text-decoration:none;">@%s</a>
        </div>
      </div>
    </div>
    <div style="display:flex;height:6px;">
      <div style="width:%d%%;background:#3b82f6;transition:width .3s;"></div>
      <div style="width:%d%%;background:#ec4899;transition:width .3s;"></div>
    </div>
    <div style="display:flex;padding:8px 14px 4px;gap:8px;">
      <div style="flex:1;text-align:center;font-size:11px;font-weight:800;color:#3b82f6;">%d votes</div>
      <div style="flex:1;text-align:center;font-size:11px;font-weight:800;color:#ec4899;">%d votes</div>
    </div>
    <div class="no-scrollbar" style="display:flex;justify-content:center;align-items:center;gap:6px;padding:6px 14px 12px;flex-wrap:wrap;">
      %s %s %s %s
    </div>
    %s
    %s
    %s
    %s
</div>`,
		d.id,
		d.stake, d.id, timeLeftStr, d.challenger, d.challenger,
		d.challengerImg, d.challengerImg, d.challenger, d.challenger,
		d.challenger, d.challenger,
		d.opponentImg, d.opponentImg, d.opponent, d.opponent,
		d.opponent, d.opponent,
		chalPct, oppPct,
		d.challengerVotes, d.opponentVotes,
		voteBtn("challenger"), voteBtn("opponent"), giveUpHTML, deleteHTML,
		votedNoteHTML,
		winnerBanner,
		votersSectionHTML,
		timeLeftScript+lightboxScript,
	)
}

// renderDPCompetitionPostCard wraps renderDPCompetitionInner in the same
// post-card shell used by normal posts, so it flows into the feed and
// profile like any other post — instead of the compact 180px strip card
// that was used for the (now removed) home slider.
func renderDPCompetitionPostCard(d dpCompetition, currentUser string, postID int) string {
	return fmt.Sprintf(`
<div id="post-%d" class="post-card mb-4 overflow-hidden rounded-2xl bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800" style="width:100%%;">
  %s
</div>`, postID, renderDPCompetitionInner(d, currentUser))
}

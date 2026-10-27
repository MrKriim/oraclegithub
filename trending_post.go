package main

// trending_posts.go — weekly trending system for dostana.pk
//
// A post crosses into trending once (weighted jhappi score + unique senders
// clear its tier's threshold) and that is its ONE trending turn, forever.
// trending_at is the permanent marker: once set, the post is never
// re-evaluated for trending again — not this week, not next week — even if
// it un-trends via age-out or a weekly reset. This is what stops the fire
// badge from flapping (a post is never un-marked just because its score
// dipped) and stops old posts from being farmed back into trending
// repeatedly.
//
// TRENDING WINDOW = THE CURRENT CALENDAR WEEK (Monday 00:00 Asia/Karachi to
// the following Monday 00:00), not a rolling 24h day. Every eligibility,
// refresh, and expiry check below is scoped to that boundary via
// startOfWeekKarachi() in Go and DATE_TRUNC('week', NOW()) in SQL — both
// truncate to Monday 00:00 in Asia/Karachi (the DB session timezone is
// already set to Asia/Karachi in initDatabase), so they always agree.
//
// ELIGIBILITY
//   1. Category must not be "General".
//   2. Image posts: any caption length. Text-only posts: >= minTextOnly runes.
//   3. Image posts trend at a lower threshold than text-only posts.
//
// A post only ever LEAVES trending via:
//   - aging out of the current week, or
//   - the weekly reset (clears is_trending for the leaderboard, but
//     trending_at stays set, so the post can never trend again).
//
// CONCURRENCY / DEADLOCK SAFETY
// Every function that does a bulk multi-row UPDATE (recalculateAllTrending,
// checkWeeklyReset's heavy path, rebuildLeaderboard) runs inside its own
// Postgres transaction guarded by pg_try_advisory_xact_lock. That's a real,
// structural guarantee — not timing luck — that only one of these can ever
// be mid-write at a time, which is what actually rules out two overlapping
// bulk UPDATEs deadlocking against each other. It's an xact-scoped lock
// (not the session-scoped pg_try_advisory_lock) specifically because it's
// tied to the TRANSACTION, not the physical connection — correct under
// pgxpool's connection multiplexing, where two calls on the same
// *pgxpool.Pool can silently land on two different physical connections,
// and safe under this app's existing PgBouncer transaction-pooling setup
// (see db.go) since it's one clean Begin...Commit/Rollback cycle with no
// cross-transaction assumptions.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

const (
	trendingWindowDays = 7
	trendingMaxAgeDays = 7
	leaderboardSize    = 10

	// Three tiers of history here, in case these need tuning again:
	//   original/strict : image 3.0/2-unique, text 5.0/3-unique, 80-rune min
	//   loosened         : image 2.0/2-unique, text 3.0/2-unique, 50-rune min
	//   this change      : moved back up partway per explicit request to
	//                      make trending "a little" harder — not all the
	//                      way back to original, just enough that it's not
	//                      trivial. Unique-sender minimums deliberately
	//                      left untouched so only ONE dial (score/length)
	//                      moved, instead of compounding several increases
	//                      at once into something harsher than "a little."
	trendingThresholdImage = 2.5
	minUniqueTrendorsImage = 2
	minTextWithImage       = 0

	trendingThresholdText = 4.0
	minUniqueTrendorsText = 2
	minTextOnly           = 65

	rKeyTrendingLeaderboard     = "trending:leaderboard"
	rKeyStarUsers               = "trending:star_usernames"
	rKeyTrendingLeaderboardPrev = "trending:leaderboard:prev"
	rKeyStarUsersPrev           = "trending:star_usernames:prev"
	rKeyTrendingWeek            = "trending:current_week"
	rKeyTrendingDay             = "trending:current_day"

	// Cached, ranked snapshot of currently-live trending posts, one per
	// post type. This is what makes GET /trending cheap under real
	// traffic: without it, every single pageview — from every visitor,
	// on every tab, on every filter/category click — ran up to 4
	// separate Postgres queries (categories, two counts, the page
	// itself). With it, the vast majority of requests touch Postgres
	// zero times; pagination, category filtering, and the "X trending"
	// count badges are all derived in Go from this one cached list.
	rKeyTrendingPostsPics = "trending:posts:pics"
	rKeyTrendingPostsText = "trending:posts:text"
	trendingPostsCacheTTL = 90 * time.Second
	trendingPostsCacheCap = 200 // generous — well past any real page depth

	workerInterval  = 5 * time.Minute
	leaderboardTTL  = 26 * time.Hour
	prevSnapshotTTL = 9 * 24 * time.Hour
	redisOpTimeout  = 500 * time.Millisecond
	dbShortTimeout  = 3 * time.Second
	dbScoreTimeout  = 5 * time.Second
	dbLeaderTimeout = 10 * time.Second

	// Lowered from 60s: the recalculation query now does fewer round
	// trips (fetchLiveTrendingIDs was folded directly into the main
	// candidate query instead of being a separate fetch), so it no
	// longer needs as much headroom. Still generous relative to how
	// bounded the candidate set actually is in practice.
	dbRecalcTimeout = 25 * time.Second

	// Postgres advisory-lock key for the trending worker's write paths.
	// Arbitrary but must stay stable, and must not collide with any
	// other pg_advisory_lock key used elsewhere in the codebase.
	trendingAdvisoryLockKey = 918_273_645
)

// ── REDIS TRENDING CACHE ────────────────────────────────────────────────────

const trustCacheTTL = 6 * time.Hour

func trustCacheKey(username string) string { return "trust:" + username }

func CacheUserTrust(username string, si senderInfo) {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	key := trustCacheKey(username)
	rdb.HSet(rctx, key, map[string]interface{}{
		"subnet": si.subnet, "device_key": si.deviceKey, "ip_address": si.ipAddress,
		"created_at": si.createdAt.Unix(),
		"is_banned":  strconv.FormatBool(si.isBanned),
		"status":     si.accountStat,
	})
	rdb.Expire(rctx, key, trustCacheTTL)
}

func InvalidateUserTrust(username string) { CacheDel(trustCacheKey(username)) }

// fetchSenderInfoBatchCached checks Redis first for each username's trust
// attributes (subnet/device/ip/created_at/ban/status — all slow-changing)
// and only falls through to Postgres for whatever's missing, caching the
// result afterward. This existed in the file already but was never
// actually called anywhere — calcTrendingScore and recalculateAllTrending
// both called fetchSenderInfoBatch directly, bypassing it entirely. Now
// wired in on both paths: as the same reactors show up across many posts
// and many worker ticks, more and more of this increasingly comes from
// Redis instead of Postgres.
func fetchSenderInfoBatchCached(usernames []string) map[string]senderInfo {
	result := make(map[string]senderInfo, len(usernames))
	if len(usernames) == 0 {
		return result
	}
	var misses []string

	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	pipe := rdb.Pipeline()
	cmds := make(map[string]*redis.MapStringStringCmd, len(usernames))
	for _, u := range usernames {
		cmds[u] = pipe.HGetAll(rctx, trustCacheKey(u))
	}
	pipe.Exec(rctx)
	cancel()

	for u, cmd := range cmds {
		m, err := cmd.Result()
		if err != nil || len(m) == 0 {
			misses = append(misses, u)
			continue
		}
		createdUnix, _ := strconv.ParseInt(m["created_at"], 10, 64)
		result[u] = senderInfo{
			exists: true, subnet: m["subnet"], deviceKey: m["device_key"],
			ipAddress: m["ip_address"], createdAt: time.Unix(createdUnix, 0),
			isBanned: m["is_banned"] == "true", accountStat: m["status"],
		}
	}

	if len(misses) > 0 {
		dbResult := fetchSenderInfoBatch(misses)
		for u, si := range dbResult {
			result[u] = si
			CacheUserTrust(u, si)
		}
	}
	return result
}

func postMetaKey(postID int) string { return fmt.Sprintf("td:postmeta:%d", postID) }

type postMeta struct {
	author, content, imageURL, category string
	createdAt                           time.Time
}

func CachePostMeta(postID int, author, content, imageURL, category string, createdAt time.Time) {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	rdb.HSet(rctx, postMetaKey(postID), map[string]interface{}{
		"author": author, "content": content, "image_url": imageURL,
		"category": category, "created_at": createdAt.Unix(),
	})
	rdb.Expire(rctx, postMetaKey(postID), 26*time.Hour)
}

func getPostMetaFromRedis(postID int) (postMeta, bool) {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	m, err := rdb.HGetAll(rctx, postMetaKey(postID)).Result()
	if err != nil || len(m) == 0 {
		return postMeta{}, false
	}
	createdUnix, _ := strconv.ParseInt(m["created_at"], 10, 64)
	return postMeta{
		author: m["author"], content: m["content"], imageURL: m["image_url"],
		category: m["category"], createdAt: time.Unix(createdUnix, 0),
	}, true
}

func senderSetKey(postID int) string { return fmt.Sprintf("td:senders:%d", postID) }

func RecordJhappiSender(postID int, sender string) {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	key := senderSetKey(postID)
	rdb.SAdd(rctx, key, sender)
	rdb.Expire(rctx, key, 26*time.Hour)
}

func getSendersFromRedis(postID int) []string {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	senders, _ := rdb.SMembers(rctx, senderSetKey(postID)).Result()
	return senders
}

// ── LIVE TRENDING POSTS CACHE ────────────────────────────────────────────
// Snapshot of every currently-live trending post for one type (pics or
// text), cached as JSON in Redis. This is what makes GET /trending cheap
// under real traffic: without it, every pageview ran up to 4 separate
// Postgres queries (distinct categories, two type counts, the paginated
// rows) — and did it again for every filter click and page turn, from
// every visitor. With it, pagination, category filtering, and the "X
// trending" count badges are all derived in Go from one shared,
// short-lived cached list; Postgres is only touched once per TTL window
// per post type, no matter how many people are looking at Trending at
// once. Mirrors the same cache-the-data/derive-the-rest pattern already
// used by the main feed (loadFeedFragment + prefetchPostRenderStates).

type cachedTrendPost struct {
	ID            int        `json:"id"`
	Author        string     `json:"author"`
	Content       string     `json:"content"`
	CreatedAt     time.Time  `json:"created_at"`
	Points        int        `json:"points"`
	AllowJhappi   bool       `json:"allow_jhappi"`
	AllowChupair  bool       `json:"allow_chupair"`
	ImageURL      string     `json:"image_url"`
	Category      string     `json:"category"`
	VoiceURL      string     `json:"voice_url"`
	AllowComments bool       `json:"allow_comments"`
	FollowersOnly bool       `json:"followers_only"`
	ExpiresAt     *time.Time `json:"expires_at"`
	DPCompID      int        `json:"dp_comp_id"`
}

func trendingPostsCacheKey(postType string) string {
	if postType == "text" {
		return rKeyTrendingPostsText
	}
	return rKeyTrendingPostsPics
}

// fetchTrendingPostsFromDB is the ONLY place that queries Postgres for the
// trending feed's post rows. liveOnly=true is the normal path (used to
// populate the cache); liveOnly=false is the rare "new week just started,
// nothing has trended yet" fallback in TrendingFeedHandler, which
// intentionally bypasses the cache since it only fires right after a
// weekly reset and needs last week's data specifically.
func fetchTrendingPostsFromDB(postType string, liveOnly bool) []cachedTrendPost {
	rctx, cancel := context.WithTimeout(ctx, dbScoreTimeout)
	defer cancel()

	liveClause := ""
	if liveOnly {
		liveClause = "AND p.created_at >= DATE_TRUNC('week', NOW())"
	}
	typeClause := "AND COALESCE(p.image_url,'') != ''"
	if postType == "text" {
		typeClause = fmt.Sprintf("AND COALESCE(p.image_url,'') = '' AND CHAR_LENGTH(TRIM(p.content)) >= %d", minTextOnly)
	}

	q := fmt.Sprintf(`
		SELECT p.id, p.author, p.content, p.created_at,
		       COALESCE(u.points,0), p.allow_jhappi, p.allow_chupair,
		       COALESCE(p.image_url,''), COALESCE(p.category,'General'),
		       COALESCE(p.voice_url,''),
		       COALESCE(p.allow_comments, true), COALESCE(p.followers_only, false), p.expires_at,
		       COALESCE(p.dp_competition_id, 0)
		FROM posts p
		JOIN users u ON u.username = p.author
		WHERE p.is_trending = true
		  AND u.is_banned = false
		  AND COALESCE(u.account_status,'approved') = 'approved'
		  AND LOWER(COALESCE(p.category,'General')) != 'general'
		  AND COALESCE(p.category,'') != ''
		  %s %s
		ORDER BY p.trending_score DESC, p.created_at DESC
		LIMIT %d`,
		liveClause, typeClause, trendingPostsCacheCap,
	)

	rows, err := db.Query(rctx, q)
	if err != nil {
		log.Printf("[trending] fetchTrendingPostsFromDB (%s) error: %v", postType, err)
		return nil
	}
	defer rows.Close()

	var out []cachedTrendPost
	for rows.Next() {
		var p cachedTrendPost
		if err := rows.Scan(
			&p.ID, &p.Author, &p.Content, &p.CreatedAt,
			&p.Points, &p.AllowJhappi, &p.AllowChupair,
			&p.ImageURL, &p.Category, &p.VoiceURL,
			&p.AllowComments, &p.FollowersOnly, &p.ExpiresAt, &p.DPCompID,
		); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// getCachedTrendingPosts returns the live-trending snapshot for one post
// type, rebuilding it from Postgres at most once per trendingPostsCacheTTL
// — and even then, singleflight-protected so a burst of simultaneous
// requests arriving right as the cache expires triggers exactly one
// rebuild, not one per request. Same stampede fix already used elsewhere
// in this codebase (getCachedUserRanks, getCachedPostCount).
func getCachedTrendingPosts(postType string) []cachedTrendPost {
	key := trendingPostsCacheKey(postType)
	if val, ok := CacheGet(key); ok {
		var posts []cachedTrendPost
		if json.Unmarshal([]byte(val), &posts) == nil {
			return posts
		}
	}

	raw := sfGroup.do(key, func() string {
		posts := fetchTrendingPostsFromDB(postType, true)
		b, err := json.Marshal(posts)
		if err != nil {
			return "[]"
		}
		CacheSet(key, string(b), trendingPostsCacheTTL)
		return string(b)
	})

	var posts []cachedTrendPost
	if json.Unmarshal([]byte(raw), &posts) != nil {
		return nil
	}
	return posts
}

// invalidateTrendingPostsCache drops both cached post-type snapshots so
// the next request rebuilds from current data instead of waiting out the
// full TTL. Cheap and rare enough (only called when a post's trending
// status or score actually changes) that invalidating just the one
// affected type isn't worth the extra bookkeeping.
func invalidateTrendingPostsCache() {
	CacheDel(rKeyTrendingPostsPics, rKeyTrendingPostsText)
}

// ── TRENDING CANDIDATE SET ──────────────────────────────────────────────
// A cheap running list (in Redis) of post IDs that are eligible-category
// and worth checking. The recalculation worker only re-checks IDs that are
// either still pending their one trending turn or already trending (for
// score refresh) — instead of scanning every post created this week. The
// set is reset once per week (see ensureCandidateSetFresh) so it can never
// grow unbounded, and entries are removed as soon as a post is
// permanently resolved (trended, aged out, or deleted).

const candidateSetKey = "td:candidates"
const candidateWeekKey = "td:candidates:week"

func RegisterTrendingCandidate(postID int, category string) {
	if strings.EqualFold(strings.TrimSpace(category), "General") || strings.TrimSpace(category) == "" {
		return
	}
	ensureCandidateSetFresh()
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	rdb.SAdd(rctx, candidateSetKey, postID)
}

func removeTrendingCandidate(postID int) {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	rdb.SRem(rctx, candidateSetKey, postID)
}

// ensureCandidateSetFresh clears the candidate set once per calendar week
// (same Monday Asia/Karachi boundary as everything else in this file), so
// stale IDs from a previous week's posts never accumulate forever.
func ensureCandidateSetFresh() {
	week := isoWeekKey()
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	stored, err := rdb.Get(rctx, candidateWeekKey).Result()
	if err == nil && stored == week {
		return
	}
	rdb.Del(rctx, candidateSetKey)
	rdb.Set(rctx, candidateWeekKey, week, 9*24*time.Hour)
}

func fetchCandidateIDs() []int {
	ensureCandidateSetFresh()
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	members, err := rdb.SMembers(rctx, candidateSetKey).Result()
	if err != nil {
		return nil
	}
	ids := make([]int, 0, len(members))
	for _, m := range members {
		if id, convErr := strconv.Atoi(m); convErr == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// startOfTodayKarachi returns midnight of the current day in Asia/Karachi.
// Kept for anything else in the package that still wants a calendar-day
// boundary; trending logic itself uses startOfWeekKarachi below.
func startOfTodayKarachi() time.Time {
	loc, err := time.LoadLocation("Asia/Karachi")
	if err != nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
}

// startOfWeekKarachi returns midnight Monday (ISO week start) of the
// current week in Asia/Karachi — the boundary used for every trending
// eligibility/expiry check in Go. SQL-side queries use
// DATE_TRUNC('week', NOW()) instead, which also truncates to Monday 00:00
// in the database's session timezone (already Asia/Karachi — see
// initDatabase's ALTER DATABASE), so the two always agree.
func startOfWeekKarachi() time.Time {
	loc, err := time.LoadLocation("Asia/Karachi")
	if err != nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	offset := (int(now.Weekday()) + 6) % 7 // days since Monday (Mon=0..Sun=6)
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	return startOfToday.AddDate(0, 0, -offset)
}

// daysUntilWeeklyReset returns how many days remain until the next weekly
// trending reset (the Monday 00:00 Asia/Karachi boundary used by
// checkWeeklyReset) — display purposes only.
func daysUntilWeeklyReset() int {
	nextReset := startOfWeekKarachi().AddDate(0, 0, 7)
	d := int(time.Until(nextReset).Hours() / 24)
	if d < 0 {
		d = 0
	}
	return d + 1
}

// ── DEBOUNCE ─────────────────────────────────────────────────────────────

var (
	evalInFlight   = make(map[int]struct{})
	evalInFlightMu sync.Mutex
)

func acquireEval(postID int) bool {
	evalInFlightMu.Lock()
	defer evalInFlightMu.Unlock()
	if _, running := evalInFlight[postID]; running {
		return false
	}
	evalInFlight[postID] = struct{}{}
	return true
}

func releaseEval(postID int) {
	evalInFlightMu.Lock()
	delete(evalInFlight, postID)
	evalInFlightMu.Unlock()
}

// ── ELIGIBILITY ──────────────────────────────────────────────────────────

// postTrendingEligible returns (eligible, isImagePost). alreadyTrended posts
// are permanently ineligible — a post gets exactly one trending turn.
func postTrendingEligible(category, content, imageURL string, alreadyTrended bool) (bool, bool) {
	if alreadyTrended {
		return false, false
	}
	if strings.EqualFold(strings.TrimSpace(category), "General") || strings.TrimSpace(category) == "" {
		return false, false
	}

	isImage := strings.TrimSpace(imageURL) != ""

	if isImage {
		captionLen := utf8.RuneCountInString(strings.TrimSpace(content))
		if captionLen < minTextWithImage {
			return false, true
		}
		return true, true
	}

	textLen := utf8.RuneCountInString(strings.TrimSpace(content))
	if textLen < minTextOnly {
		return false, false
	}
	return true, false
}

func thresholdsFor(isImagePost bool) (scoreThreshold float64, minUnique int) {
	if isImagePost {
		return trendingThresholdImage, minUniqueTrendorsImage
	}
	return trendingThresholdText, minUniqueTrendorsText
}

// ── TRUST WEIGHT ─────────────────────────────────────────────────────────

type senderInfo struct {
	exists      bool
	subnet      string
	deviceKey   string
	ipAddress   string
	createdAt   time.Time
	isBanned    bool
	accountStat string
}

func fetchSenderInfoBatch(senders []string) map[string]senderInfo {
	result := make(map[string]senderInfo, len(senders))
	if len(senders) == 0 {
		return result
	}

	rctx, cancel := context.WithTimeout(ctx, dbScoreTimeout)
	defer cancel()

	rows, err := db.Query(rctx,
		`SELECT username, subnet, device_key, ip_address, created_at,
		        COALESCE(is_banned,false), COALESCE(account_status,'approved')
		 FROM users
		 WHERE username = ANY($1)`,
		senders,
	)
	if err != nil {
		return result
	}
	defer rows.Close()

	for rows.Next() {
		var u, s, d, ip, status string
		var createdAt time.Time
		var banned bool
		if err := rows.Scan(&u, &s, &d, &ip, &createdAt, &banned, &status); err == nil {
			result[u] = senderInfo{
				exists: true, subnet: s, deviceKey: d, ipAddress: ip,
				createdAt: createdAt, isBanned: banned, accountStat: status,
			}
		}
	}
	return result
}

// trustWeightedSenders decides, per sender, how much their jhappi counts
// toward a post's trending score. Every existing, non-banned, approved
// account's reaction counts — full stop. There is no minimum-account-age
// cutoff and no IP/subnet/device-key identity clustering: subnet and
// device_key are derived from (or equal to) the caller's /24 subnet, and
// Pakistani mobile carriers run heavy CGNAT — thousands of unrelated real
// users share one subnet (same reasoning already applied to
// checkAccountLimit and the rate limiters elsewhere in this codebase).
// Clustering on those signals was silently discarding almost every
// genuine reactor's jhappi as a "duplicate voter," which is why posts
// almost never crossed the trending threshold and the leaderboard/fire
// badge stayed stuck on the same one or two accounts.
//
// Instead, older accounts simply count for MORE than brand-new ones (see
// trendingSenderWeight) — this only changes how much a reaction is
// worth, never whether it counts.
func trustWeightedSenders(authorUsername string, senders []string, info map[string]senderInfo) map[string]float64 {
	result := make(map[string]float64, len(senders))
	if len(senders) == 0 {
		return result
	}

	for _, s := range senders {
		si, ok := info[s]
		if !ok || !si.exists {
			result[s] = 0
			continue
		}
		if si.isBanned || si.accountStat != "approved" {
			result[s] = 0
			continue
		}
		result[s] = trendingSenderWeight(si.createdAt)
	}
	return result
}

// trendingSenderWeight scales a reactor's contribution by account age:
// 1.0x for a brand-new account, ramping linearly up to 3.0x for an
// account 2+ years old. No account is ever excluded on age grounds.
func trendingSenderWeight(createdAt time.Time) float64 {
	if createdAt.IsZero() {
		return 1.0
	}
	const maxAgeDays = 730 // 2 years
	ageDays := time.Since(createdAt).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	if ageDays > maxAgeDays {
		ageDays = maxAgeDays
	}
	return 1.0 + 2.0*(ageDays/maxAgeDays)
}

// ── SCORE ────────────────────────────────────────────────────────────────

func calcTrendingScore(postID int, authorUsername string) (score float64, uniqueSenders int) {
	rctx, cancel := context.WithTimeout(ctx, dbScoreTimeout)
	defer cancel()

	rows, err := db.Query(rctx, `
		SELECT sender FROM interactions
		WHERE post_id    = $1
		  AND type NOT IN ('dislike','disgusted','angry','chupair')
		  AND created_at > NOW() - INTERVAL '7 days'`,
		postID,
	)
	if err != nil {
		return 0, 0
	}
	defer rows.Close()

	var senders []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err == nil {
			senders = append(senders, s)
		}
	}

	all := append([]string{authorUsername}, senders...)
	info := fetchSenderInfoBatchCached(all)

	weights := trustWeightedSenders(authorUsername, senders, info)
	for _, w := range weights {
		if w > 0 {
			score += w
			uniqueSenders++
		}
	}
	return score, uniqueSenders
}

// fetchSendersBatch fetches jhappi senders for MANY posts in a single
// query, grouped by post_id. Used by recalculateAllTrending to avoid
// firing one query per post inside a loop.
func fetchSendersBatch(rctx context.Context, postIDs []int32) map[int][]string {
	result := make(map[int][]string, len(postIDs))
	if len(postIDs) == 0 {
		return result
	}

	rows, err := db.Query(rctx, `
		SELECT post_id, sender FROM interactions
		WHERE post_id = ANY($1)
		  AND type NOT IN ('dislike','disgusted','angry','chupair')
		  AND created_at > NOW() - INTERVAL '7 days'`,
		postIDs,
	)
	if err != nil {
		return result
	}
	defer rows.Close()

	for rows.Next() {
		var postID int
		var sender string
		if err := rows.Scan(&postID, &sender); err == nil {
			result[postID] = append(result[postID], sender)
		}
	}
	return result
}

// scoreFromSenders applies the same trust-weighting rules as
// calcTrendingScore, but works off an already-fetched senderInfo map
// instead of issuing its own DB query — letting recalculateAllTrending
// batch that lookup once across every candidate post instead of once per
// post.
func scoreFromSenders(authorUsername string, senders []string, info map[string]senderInfo) (score float64, uniqueSenders int) {
	weights := trustWeightedSenders(authorUsername, senders, info)
	for _, w := range weights {
		if w > 0 {
			score += w
			uniqueSenders++
		}
	}
	return score, uniqueSenders
}

// ── EVALUATION ───────────────────────────────────────────────────────────

// EvaluatePostTrending runs after every jhappi. A post that already had its
// trending turn (trending_at set, currently not trending) is never touched
// again. A post currently trending only gets its display score refreshed —
// it is never un-marked here just because the score dipped.
func EvaluatePostTrending(postID int) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC in EvaluatePostTrending (postID=%d): %v", postID, r)
		}
	}()
	if !acquireEval(postID) {
		return
	}
	defer releaseEval(postID)

	rctx, cancel := context.WithTimeout(ctx, dbScoreTimeout)
	defer cancel()

	var author, content, imageURL, category string
	var createdAt time.Time
	var trendingAt *time.Time
	var wasTrending bool
	var authorBanned bool
	var authorStatus string

	err := db.QueryRow(rctx, `
		SELECT p.author, p.content, COALESCE(p.image_url,''), COALESCE(p.category,'General'),
		       p.created_at, p.trending_at, COALESCE(p.is_trending, false),
		       COALESCE(u.is_banned, true), COALESCE(u.account_status, 'pending')
		FROM posts p
		LEFT JOIN users u ON u.username = p.author
		WHERE p.id = $1`,
		postID,
	).Scan(&author, &content, &imageURL, &category, &createdAt, &trendingAt, &wasTrending, &authorBanned, &authorStatus)
	if err != nil {
		return
	}

	// A banned or non-approved (pending/rejected) account's posts never
	// enter trending, and lose it immediately if they already had it.
	if authorBanned || authorStatus != "approved" {
		if wasTrending {
			unmarkTrending(postID)
		}
		// The author's own trust-cache entry (used when THEY show up as
		// a sender on someone else's post) may now be stale — purge it
		// so the next lookup reflects the ban immediately rather than
		// waiting out trustCacheTTL. This doesn't cover every path that
		// can change ban/account status — just the one this function
		// already happens to detect firsthand; ban/unban toggling lives
		// outside this file.
		InvalidateUserTrust(author)
		return
	}

	alreadyTrended := trendingAt != nil && !wasTrending
	if alreadyTrended {
		return
	}

	// Eligibility is scoped to the current week only (Asia/Karachi,
	// Monday-start) — a post from a previous week is un-trended here.
	if createdAt.Before(startOfWeekKarachi()) {
		if wasTrending {
			unmarkTrending(postID)
		}
		return
	}

	eligible, isImagePost := postTrendingEligible(category, content, imageURL, alreadyTrended)
	if !eligible {
		return
	}

	if wasTrending {
		// Score-only refresh on an already-trending post. Deliberately
		// does NOT invalidate the trending-posts-list cache here — that
		// cache exists specifically to absorb high-frequency writes like
		// this one; invalidating it on every single jhappi would defeat
		// the entire point. A few seconds of stale relative ordering
		// within the cache's TTL is the intended, accepted trade-off.
		score, _ := calcTrendingScore(postID, author)
		updateCtx, updateCancel := context.WithTimeout(ctx, dbShortTimeout)
		db.Exec(updateCtx, `UPDATE posts SET trending_score = $1 WHERE id = $2`, score, postID)
		updateCancel()
		return
	}

	scoreThreshold, minUnique := thresholdsFor(isImagePost)
	score, unique := calcTrendingScore(postID, author)
	if score >= scoreThreshold && unique >= minUnique {
		markTrending(postID, score)
		removeTrendingCandidate(postID) // had its turn — no more re-checking needed
	}
}

func markTrending(postID int, score float64) {
	rctx, cancel := context.WithTimeout(ctx, dbShortTimeout)
	defer cancel()
	_, err := db.Exec(rctx, `
		UPDATE posts
		SET is_trending = true, trending_score = $1, trending_at = NOW()
		WHERE id = $2`,
		score, postID,
	)
	if err != nil {
		return
	}
	invalidateCache()
}

func unmarkTrending(postID int) {
	rctx, cancel := context.WithTimeout(ctx, dbShortTimeout)
	defer cancel()
	db.Exec(rctx,
		`UPDATE posts SET is_trending = false, trending_score = 0 WHERE id = $1`, postID)
	invalidateCache()
}

func invalidateCache() {
	rctx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	defer cancel()
	// Only drop the leaderboard + trending-posts caches here. rKeyStarUsers
	// (the Top Star fire-badge set) used to be deleted on EVERY post that
	// crossed into trending, which meant every existing Top Star's fire
	// badge went dark site-wide the instant any post trended, and stayed
	// dark until something else happened to call rebuildLeaderboard (at
	// most once/day via checkDailyUpdate). rKeyStarUsers is now only ever
	// touched inside rebuildLeaderboard itself (delete+recreate
	// atomically), and we kick off a rebuild right here so newly
	// qualifying Top Stars — and the has_trending_star DB column the
	// online-users slider's fire ring reads — update within moments
	// instead of up to a day later.
	rdb.Del(rctx, rKeyTrendingLeaderboard)
	invalidateTrendingPostsCache()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in invalidateCache leaderboard refresh: %v", r)
			}
		}()
		rebuildLeaderboard()
	}()
}

// ── WEEKLY RESET ─────────────────────────────────────────────────────────

func isoWeekKey() string {
	loc, _ := time.LoadLocation("Asia/Karachi")
	year, week := time.Now().In(loc).ISOWeek()
	return fmt.Sprintf("%d-W%02d", year, week)
}

// isoDayKey returns the current calendar date in Asia/Karachi, e.g.
// "2026-09-09" — the boundary checkDailyUpdate uses to run at most once
// per day.
func isoDayKey() string {
	loc, _ := time.LoadLocation("Asia/Karachi")
	return time.Now().In(loc).Format("2006-01-02")
}

// checkDailyUpdate recomputes trending post scores/counts
// (recalculateAllTrending) and the Top Stars leaderboard's top-10
// positions (rebuildLeaderboard) at most once per calendar day
// (Asia/Karachi) — short-circuiting every other 5-minute worker tick,
// the same short-circuit pattern checkWeeklyReset uses for its 7-day
// boundary. Individual posts still flip into trending immediately on
// reaction via EvaluatePostTrending; this daily pass is what keeps
// scores, ordering, and Top Stars positions in sync without recomputing
// them on every worker tick.
func checkDailyUpdate() {
	today := isoDayKey()

	getCtx, getCancel := context.WithTimeout(ctx, redisOpTimeout)
	storedDay, err := rdb.Get(getCtx, rKeyTrendingDay).Result()
	getCancel()
	if err == nil && storedDay == today {
		return
	}

	recalculateAllTrending()
	rebuildLeaderboard()

	setCtx, setCancel := context.WithTimeout(ctx, redisOpTimeout)
	rdb.Set(setCtx, rKeyTrendingDay, today, 0)
	setCancel()
}

// checkWeeklyReset clears is_trending (so the leaderboard/counts reset) but
// deliberately does NOT touch trending_at — that stays as the permanent
// lock so a post that already trended can never trend again in a later
// week. The ISO-week check below short-circuits every tick except the one
// per week where the week actually rolls over, so the advisory-lock
// transaction wrapping the heavy path only ever runs once a week — the
// overhead of that machinery is negligible in practice.
func checkWeeklyReset() {
	currentWeek := isoWeekKey()

	getCtx, getCancel := context.WithTimeout(ctx, redisOpTimeout)
	storedWeek, err := rdb.Get(getCtx, rKeyTrendingWeek).Result()
	getCancel()
	if err != nil {
		setCtx, setCancel := context.WithTimeout(ctx, redisOpTimeout)
		rdb.Set(setCtx, rKeyTrendingWeek, currentWeek, 0)
		setCancel()
		return
	}
	if storedWeek == currentWeek {
		return
	}

	rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	tx, err := db.Begin(rctx)
	if err != nil {
		log.Printf("[trending] weekly reset: begin tx: %v", err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback(context.Background())
		}
	}()

	var locked bool
	if err := tx.QueryRow(rctx, `SELECT pg_try_advisory_xact_lock($1)`, int64(trendingAdvisoryLockKey)).Scan(&locked); err != nil {
		log.Printf("[trending] weekly reset advisory lock check failed: %v", err)
		return
	}
	if !locked {
		log.Printf("[trending] weekly reset skipped — another instance holds the lock")
		return
	}

	snapCtx, snapCancel := context.WithTimeout(ctx, redisOpTimeout)
	if liveVal, e := rdb.Get(snapCtx, rKeyTrendingLeaderboard).Result(); e == nil && liveVal != "" {
		rdb.Set(snapCtx, rKeyTrendingLeaderboardPrev, liveVal, prevSnapshotTTL)
	}
	snapCancel()

	starCtx, starCancel := context.WithTimeout(ctx, redisOpTimeout)
	if prevStars, e := rdb.SMembers(starCtx, rKeyStarUsers).Result(); e == nil && len(prevStars) > 0 {
		rdb.Del(starCtx, rKeyStarUsersPrev)
		members := make([]interface{}, len(prevStars))
		for i, s := range prevStars {
			members[i] = s
		}
		rdb.SAdd(starCtx, rKeyStarUsersPrev, members...)
		rdb.Expire(starCtx, rKeyStarUsersPrev, prevSnapshotTTL)
	}
	starCancel()

	if _, err := tx.Exec(rctx, `UPDATE posts SET is_trending = false, trending_score = 0 WHERE is_trending = true`); err != nil {
		log.Printf("[trending] weekly reset posts UPDATE failed: %v", err)
		return
	}
	if _, err := tx.Exec(rctx, `UPDATE users SET trending_post_count = 0, has_trending_star = false`); err != nil {
		log.Printf("[trending] weekly reset users UPDATE failed: %v", err)
		return
	}

	if err := tx.Commit(rctx); err != nil {
		log.Printf("[trending] weekly reset commit failed: %v", err)
		return
	}
	committed = true

	invalidateCache()

	setCtx2, setCancel2 := context.WithTimeout(ctx, redisOpTimeout)
	rdb.Set(setCtx2, rKeyTrendingWeek, currentWeek, 0)
	setCancel2()
}

// ── WORKER ───────────────────────────────────────────────────────────────

func StartTrendingWorker() {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("PANIC in trending worker goroutine: %v — restarting in 5s", r)
				time.Sleep(5 * time.Second)
				StartTrendingWorker()
			}
		}()

		runCycle := func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("PANIC recovered in trending worker cycle: %v", r)
				}
			}()
			cycleStart := time.Now()
			log.Printf("[trending] cycle start")

			t0 := time.Now()
			checkWeeklyReset()
			log.Printf("[trending] checkWeeklyReset done in %s", time.Since(t0))

			// recalculateAllTrending (trending post scores/counts) and
			// rebuildLeaderboard (Top Stars top-10 positions) both now
			// only actually do work once per calendar day — see
			// checkDailyUpdate's own internal gating. The worker still
			// ticks every 5 minutes so that gate — and checkWeeklyReset's
			// 7-day gate above — stay responsive close to their boundary.
			t1 := time.Now()
			checkDailyUpdate()
			log.Printf("[trending] checkDailyUpdate done in %s", time.Since(t1))

			log.Printf("[trending] cycle total: %s", time.Since(cycleStart))
		}

		runCycle()

		ticker := time.NewTicker(workerInterval)
		defer ticker.Stop()
		for range ticker.C {
			runCycle()
		}
	}()
}

// expireOldTrending un-marks posts from a previous week (or whose author
// got banned/rejected). Their trending_at stays set, so they remain
// permanently locked out of trending again. Runs inside the same
// advisory-locked transaction as the rest of recalculateAllTrending —
// returns whether it actually changed anything, so the caller knows
// whether a cache invalidation is warranted.
func expireOldTrending(rctx context.Context, tx pgx.Tx) bool {
	tag, err := tx.Exec(rctx, `
		UPDATE posts p
		SET is_trending = false, trending_score = 0
		FROM users u
		WHERE p.author = u.username
		  AND p.is_trending = true
		  AND (
		        p.created_at < DATE_TRUNC('week', NOW())
		     OR u.is_banned = true
		     OR COALESCE(u.account_status,'approved') != 'approved'
		      )`)
	if err != nil {
		log.Printf("[trending] expireOldTrending: %v", err)
		return false
	}
	if tag.RowsAffected() > 0 {
		log.Printf("[trending] expired %d stale/banned trending post(s)", tag.RowsAffected())
		return true
	}
	return false
}

// recalculateAllTrending re-checks only posts that can still change: the
// Redis candidate set (populated at post-creation time by
// RegisterTrendingCandidate — posts that haven't had their one trending
// turn yet) plus every post currently trending, whose live score needs
// refreshing — folded directly into the same query via
// "OR p.is_trending = true" instead of a separate round trip.
//
// The whole cycle runs inside one Postgres transaction guarded by
// pg_try_advisory_xact_lock (see the file-level comment for why). Two
// things that buys:
//   - Atomicity: if anything fails partway through, nothing partial gets
//     committed — a half-updated trending state is worse than none.
//   - No deadlock, by construction: only one process can ever be mid-cycle
//     at a time, so two overlapping bulk UPDATEs racing each other — the
//     classic way to deadlock Postgres — becomes structurally impossible.
//
// Redis-side effects (removing settled posts from the candidate set,
// invalidating caches) are deliberately held until AFTER a successful
// commit — Redis isn't part of the Postgres transaction, so mutating it
// before the commit is confirmed risks a candidate being dropped from
// tracking even though its Postgres row never actually changed.
//
// fetchSendersBatch / fetchSenderInfoBatchCached deliberately still query
// via the package-level db (not tx): they're pure reads, reused by the
// non-transactional single-post path (calcTrendingScore) too, and reads
// can't participate in a write-write deadlock regardless of which
// connection they land on. Only the actual writes need to be inside the
// locked transaction.
func recalculateAllTrending() {
	rctx, cancel := context.WithTimeout(ctx, dbRecalcTimeout)
	defer cancel()

	tx, err := db.Begin(rctx)
	if err != nil {
		log.Printf("[trending] recalculateAllTrending: begin tx: %v", err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback(context.Background())
		}
	}()

	var locked bool
	if err := tx.QueryRow(rctx, `SELECT pg_try_advisory_xact_lock($1)`, int64(trendingAdvisoryLockKey)).Scan(&locked); err != nil {
		log.Printf("[trending] advisory lock check failed: %v", err)
		return
	}
	if !locked {
		log.Printf("[trending] cycle skipped — another instance holds the lock")
		return
	}

	expired := expireOldTrending(rctx, tx)

	candidateIDs := fetchCandidateIDs()
	ids := make([]int32, len(candidateIDs))
	for i, id := range candidateIDs {
		ids[i] = int32(id)
	}

	rows, err := tx.Query(rctx, `
		SELECT p.id, p.author, p.content, COALESCE(p.image_url,''), COALESCE(p.category,'General'),
		       p.created_at, p.trending_at, COALESCE(p.is_trending, false)
		FROM posts p
		JOIN users u ON u.username = p.author
		WHERE (p.id = ANY($1) OR p.is_trending = true)
		  AND p.created_at >= DATE_TRUNC('week', NOW())
		  AND (p.trending_at IS NULL OR p.is_trending = true)
		  AND u.is_banned = false
		  AND COALESCE(u.account_status,'approved') = 'approved'
		ORDER BY p.created_at DESC`,
		ids)
	if err != nil {
		log.Printf("[trending] candidate select failed: %v", err)
		return
	}

	type postRow struct {
		id         int
		author     string
		content    string
		imageURL   string
		category   string
		createdAt  time.Time
		trendingAt *time.Time
		isTrending bool
	}
	var posts []postRow
	for rows.Next() {
		var p postRow
		if err := rows.Scan(&p.id, &p.author, &p.content, &p.imageURL, &p.category, &p.createdAt, &p.trendingAt, &p.isTrending); err == nil {
			posts = append(posts, p)
		}
	}
	rows.Close()

	// IDs collected here get dropped from the Redis candidate set, but
	// only AFTER a successful commit — see the function comment.
	var candidatesToRemove []int
	seen := make(map[int]struct{}, len(posts))
	for _, p := range posts {
		seen[p.id] = struct{}{}
	}
	for _, id := range candidateIDs {
		if _, ok := seen[id]; !ok {
			candidatesToRemove = append(candidatesToRemove, id)
		}
	}

	type candidate struct {
		postRow
		isImagePost bool
	}
	var candidates []candidate
	for _, p := range posts {
		if p.isTrending {
			candidates = append(candidates, candidate{p, strings.TrimSpace(p.imageURL) != ""})
			continue
		}
		eligible, isImagePost := postTrendingEligible(p.category, p.content, p.imageURL, false)
		if eligible {
			candidates = append(candidates, candidate{p, isImagePost})
		} else if p.trendingAt != nil {
			candidatesToRemove = append(candidatesToRemove, p.id)
		}
	}

	finish := func(newlyTrendingCount, refreshesCount int) {
		if err := tx.Commit(rctx); err != nil {
			log.Printf("[trending] commit failed: %v", err)
			return
		}
		committed = true

		for _, id := range candidatesToRemove {
			removeTrendingCandidate(id)
		}
		switch {
		case newlyTrendingCount > 0 || expired:
			invalidateCache() // leaderboard composition may have changed too
		case refreshesCount > 0:
			invalidateTrendingPostsCache() // only feed ordering changed
		}
	}

	if len(candidates) == 0 {
		finish(0, 0)
		return
	}

	postIDs := make([]int32, len(candidates))
	for i, c := range candidates {
		postIDs[i] = int32(c.id)
	}

	sendersCtx, sendersCancel := context.WithTimeout(ctx, dbScoreTimeout)
	sendersByPost := fetchSendersBatch(sendersCtx, postIDs)
	sendersCancel()

	userSet := make(map[string]struct{})
	for _, c := range candidates {
		userSet[c.author] = struct{}{}
	}
	for _, senders := range sendersByPost {
		for _, s := range senders {
			userSet[s] = struct{}{}
		}
	}
	allUsers := make([]string, 0, len(userSet))
	for u := range userSet {
		allUsers = append(allUsers, u)
	}
	senderInfo := fetchSenderInfoBatchCached(allUsers)

	type refreshRow struct {
		id    int
		score float64
	}
	type newTrendRow struct {
		id    int
		score float64
	}
	var refreshes []refreshRow
	var newlyTrending []newTrendRow

	for _, c := range candidates {
		score, unique := scoreFromSenders(c.author, sendersByPost[c.id], senderInfo)

		if c.isTrending {
			refreshes = append(refreshes, refreshRow{c.id, score})
			continue
		}

		scoreThreshold, minUnique := thresholdsFor(c.isImagePost)
		if score >= scoreThreshold && unique >= minUnique {
			newlyTrending = append(newlyTrending, newTrendRow{c.id, score})
		}
	}

	// Sorted by id before building each VALUES list — a canonical,
	// predictable row-lock acquisition order. The advisory lock is what
	// actually rules out overlapping cycles; this is cheap defense in
	// depth on top of that, not the primary safety mechanism.
	sort.Slice(refreshes, func(i, j int) bool { return refreshes[i].id < refreshes[j].id })
	sort.Slice(newlyTrending, func(i, j int) bool { return newlyTrending[i].id < newlyTrending[j].id })

	if len(refreshes) > 0 {
		valueParts := make([]string, len(refreshes))
		args := make([]interface{}, 0, len(refreshes)*2)
		for i, r := range refreshes {
			p1, p2 := i*2+1, i*2+2
			valueParts[i] = fmt.Sprintf("($%d::int,$%d::float8)", p1, p2)
			args = append(args, r.id, r.score)
		}
		if _, err := tx.Exec(rctx,
			`UPDATE posts SET trending_score = v.score
			 FROM (VALUES `+strings.Join(valueParts, ",")+`) AS v(id,score)
			 WHERE posts.id = v.id`,
			args...,
		); err != nil {
			log.Printf("[trending] refresh UPDATE failed: %v", err)
			return
		}
	}

	if len(newlyTrending) > 0 {
		valueParts := make([]string, len(newlyTrending))
		args := make([]interface{}, 0, len(newlyTrending)*2)
		for i, r := range newlyTrending {
			p1, p2 := i*2+1, i*2+2
			valueParts[i] = fmt.Sprintf("($%d::int,$%d::float8)", p1, p2)
			args = append(args, r.id, r.score)
		}
		if _, err := tx.Exec(rctx,
			`UPDATE posts
			 SET is_trending = true, trending_score = v.score, trending_at = NOW()
			 FROM (VALUES `+strings.Join(valueParts, ",")+`) AS v(id,score)
			 WHERE posts.id = v.id`,
			args...,
		); err != nil {
			log.Printf("[trending] mark-trending UPDATE failed: %v", err)
			return
		}
		for _, r := range newlyTrending {
			candidatesToRemove = append(candidatesToRemove, r.id) // had its turn
		}
	}

	finish(len(newlyTrending), len(refreshes))
}

// ── LEADERBOARD ──────────────────────────────────────────────────────────

type TrendingUser struct {
	Username          string `json:"username"`
	TrendingPostCount int    `json:"trending_post_count"`
	Rank              int    `json:"rank"`
}

// rebuildLeaderboard runs the same advisory-lock-transaction pattern as
// recalculateAllTrending, for the same reason: it does three bulk UPDATEs
// across the users table, and only one instance of this app should ever
// be mid-rebuild at a time.
func rebuildLeaderboard() {
	rctx, cancel := context.WithTimeout(ctx, dbLeaderTimeout)
	defer cancel()

	tx, err := db.Begin(rctx)
	if err != nil {
		log.Printf("[trending] rebuildLeaderboard: begin tx: %v", err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback(context.Background())
		}
	}()

	var locked bool
	if err := tx.QueryRow(rctx, `SELECT pg_try_advisory_xact_lock($1)`, int64(trendingAdvisoryLockKey)).Scan(&locked); err != nil {
		log.Printf("[trending] rebuildLeaderboard advisory lock check failed: %v", err)
		return
	}
	if !locked {
		log.Printf("[trending] rebuildLeaderboard skipped — another instance holds the lock")
		return
	}

	// NOTE: this text-length check MUST match minTextOnly (the same
	// constant EvaluatePostTrending/recalculateAllTrending use to decide
	// eligibility) — kept as an fmt.Sprintf'd constant, not a $-parameter,
	// same as the rest of this file; minTextOnly is a compile-time Go
	// constant, not user input, so there's no injection risk.
	rows, err := tx.Query(rctx, fmt.Sprintf(`
		SELECT p.author, COUNT(*) AS cnt
		FROM posts p
		JOIN users u ON u.username = p.author
		WHERE p.is_trending = true
		  AND u.is_banned = false
		  AND COALESCE(u.account_status,'approved') = 'approved'
		  AND p.created_at >= DATE_TRUNC('week', NOW())
		  AND LOWER(COALESCE(p.category,'General')) != 'general'
		  AND COALESCE(p.category,'') != ''
		  AND (
		        (COALESCE(p.image_url,'') != '')
		        OR
		        (COALESCE(p.image_url,'') = '' AND CHAR_LENGTH(TRIM(p.content)) >= %d)
		      )
		GROUP BY p.author
		ORDER BY cnt DESC
		LIMIT $1`, minTextOnly), leaderboardSize)
	if err != nil {
		log.Printf("[trending] rebuildLeaderboard select failed: %v", err)
		return
	}

	var board []TrendingUser
	var starMembers []interface{}
	rank := 1
	for rows.Next() {
		var u TrendingUser
		if err := rows.Scan(&u.Username, &u.TrendingPostCount); err != nil {
			continue
		}
		u.Rank = rank
		board = append(board, u)
		starMembers = append(starMembers, u.Username)
		rank++
	}
	rows.Close()

	if len(board) == 0 {
		if err := tx.Commit(rctx); err == nil {
			committed = true
		}
		// Nothing trending right now — clear the live snapshot instead of
		// leaving a stale one sitting in Redis with time still left on
		// its TTL. Previously this branch returned without touching
		// Redis at all, so once the last currently-trending post aged
		// out, rKeyTrendingLeaderboard/rKeyStarUsers kept whatever
		// content they last held until their TTL happened to lapse —
		// GetCachedLeaderboard would then fall through to the
		// once-a-week Prev snapshot and could look "stuck" on old
		// entries indefinitely in between resets.
		clearCtx, clearCancel := context.WithTimeout(ctx, redisOpTimeout)
		rdb.Del(clearCtx, rKeyTrendingLeaderboard, rKeyStarUsers)
		clearCancel()
		return
	}

	if _, err := tx.Exec(rctx, `UPDATE users SET trending_post_count = 0`); err != nil {
		log.Printf("[trending] rebuildLeaderboard reset counts failed: %v", err)
		return
	}

	// Sorted by username before building the VALUES list — same canonical
	// lock-ordering convention as recalculateAllTrending's bulk updates,
	// independent of the leaderboard's own cnt-DESC display ordering.
	sortedForUpdate := append([]TrendingUser(nil), board...)
	sort.Slice(sortedForUpdate, func(i, j int) bool { return sortedForUpdate[i].Username < sortedForUpdate[j].Username })

	valueParts := make([]string, len(sortedForUpdate))
	args := make([]interface{}, 0, len(sortedForUpdate)*2)
	for i, u := range sortedForUpdate {
		p1, p2 := i*2+1, i*2+2
		valueParts[i] = fmt.Sprintf("($%d::text,$%d::int)", p1, p2)
		args = append(args, u.Username, u.TrendingPostCount)
	}
	if _, err := tx.Exec(rctx,
		`UPDATE users SET trending_post_count = v.cnt
		 FROM (VALUES `+strings.Join(valueParts, ",")+`) AS v(uname,cnt)
		 WHERE users.username = v.uname`,
		args...,
	); err != nil {
		log.Printf("[trending] rebuildLeaderboard apply counts failed: %v", err)
		return
	}

	if _, err := tx.Exec(rctx, `UPDATE users SET has_trending_star = false WHERE has_trending_star = true`); err != nil {
		log.Printf("[trending] rebuildLeaderboard clear stars failed: %v", err)
		return
	}

	placeholders := make([]string, len(board))
	args2 := make([]interface{}, len(board))
	for i, u := range board {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args2[i] = u.Username
	}
	if _, err := tx.Exec(rctx,
		`UPDATE users SET has_trending_star = true WHERE username IN (`+strings.Join(placeholders, ",")+`)`,
		args2...,
	); err != nil {
		log.Printf("[trending] rebuildLeaderboard set stars failed: %v", err)
		return
	}

	if err := tx.Commit(rctx); err != nil {
		log.Printf("[trending] rebuildLeaderboard commit failed: %v", err)
		return
	}
	committed = true

	if jsonBytes, err := json.Marshal(board); err == nil {
		cacheCtx, cacheCancel := context.WithTimeout(ctx, redisOpTimeout)
		defer cacheCancel()
		rdb.Set(cacheCtx, rKeyTrendingLeaderboard, string(jsonBytes), leaderboardTTL)
	}

	starDelCtx, starDelCancel := context.WithTimeout(ctx, redisOpTimeout)
	defer starDelCancel()
	rdb.Del(starDelCtx, rKeyStarUsers)
	if len(starMembers) > 0 {
		starAddCtx, starAddCancel := context.WithTimeout(ctx, redisOpTimeout)
		defer starAddCancel()
		rdb.SAdd(starAddCtx, rKeyStarUsers, starMembers...)
		rdb.Expire(starAddCtx, rKeyStarUsers, leaderboardTTL)
	}

	// has_trending_star just changed for potentially many users — drop the
	// two 90s-TTL derived caches that read that column (author-meta map
	// used by post rendering, user-ranks map used by the online-users
	// slider) so the fire badge/fire-ring shows up on the very next
	// request instead of waiting out their TTL.
	CacheDel(authorMetaCacheKey, rankCacheKey)
}

const leaderboardBuildKey = "trending:leaderboard:build"

// GetCachedLeaderboard — singleflight-protected: without this, a burst of
// concurrent requests landing right as the 6-minute cache TTL lapses would
// each independently call rebuildLeaderboard() — a SELECT plus three bulk
// UPDATEs — at the same moment. Same thundering-herd shape already fixed
// elsewhere in this codebase (getCachedUserRanks, getCachedPostCount);
// this function just hadn't been wired up to the shared singleflight
// group yet.
func GetCachedLeaderboard() []TrendingUser {
	if val, err := rdb.Get(ctx, rKeyTrendingLeaderboard).Result(); err == nil && val != "" {
		var board []TrendingUser
		if json.Unmarshal([]byte(val), &board) == nil && len(board) > 0 {
			return board
		}
	}

	sfGroup.do(leaderboardBuildKey, func() string {
		rebuildLeaderboard()
		return ""
	})

	if val, _ := rdb.Get(ctx, rKeyTrendingLeaderboard).Result(); val != "" {
		var board []TrendingUser
		if json.Unmarshal([]byte(val), &board) == nil && len(board) > 0 {
			return board
		}
	}
	if val, err := rdb.Get(ctx, rKeyTrendingLeaderboardPrev).Result(); err == nil && val != "" {
		var board []TrendingUser
		if json.Unmarshal([]byte(val), &board) == nil && len(board) > 0 {
			return board
		}
	}
	return nil
}

func trendingStarMember(username string) bool {
	if username == "" {
		return false
	}
	rctx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	defer cancel()
	if ok, err := rdb.SIsMember(rctx, rKeyStarUsers, username).Result(); err == nil && ok {
		return true
	}
	rctx2, cancel2 := context.WithTimeout(ctx, redisOpTimeout)
	defer cancel2()
	ok, _ := rdb.SIsMember(rctx2, rKeyStarUsersPrev, username).Result()
	return ok
}

// ── DISPLAY HELPERS ──────────────────────────────────────────────────────

func TrendingFireBadge(isTrending bool) string {
	if !isTrending {
		return ""
	}
	return `<span title="Trending" style="` +
		`display:inline-flex;align-items:center;gap:3px;` +
		`padding:1px 7px;border-radius:999px;` +
		`background:linear-gradient(135deg,#ff6b00,#ff0040);` +
		`color:#fff;font-size:9px;font-weight:900;letter-spacing:.06em;` +
		`text-transform:uppercase;` +
		`box-shadow:0 0 8px rgba(255,80,0,.5),0 2px 6px rgba(255,0,64,.3);` +
		`animation:firePulse 1.8s ease-in-out infinite;` +
		`vertical-align:middle;margin-left:4px;white-space:nowrap;">` +
		`🔥 TRENDING</span>`
}

func TrendingStarHTML(username string) string {
	if !trendingStarMember(username) {
		return ""
	}
	return `<span title="Top Trending Creator" style="` +
		`font-size:11px;line-height:1;vertical-align:middle;margin-left:3px;` +
		`filter:drop-shadow(0 0 3px #ef4444);` +
		`animation:starGlow 2s ease-in-out infinite;display:inline-block;">` +
		`🔥</span>`
}

func TrendingStarCount(username string) string {
	var count int
	rctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	db.QueryRow(rctx, `
		SELECT COUNT(*) FROM posts
		WHERE author       = $1
		  AND is_trending  = true
		  AND created_at   > NOW() - INTERVAL '7 days'`,
		username,
	).Scan(&count)
	if count == 0 {
		return ""
	}
	return fmt.Sprintf(
		`<span style="display:inline-flex;align-items:center;gap:3px;font-size:10px;font-weight:800;color:#ef4444;vertical-align:middle;">🔥 %d</span>`,
		count,
	)
}

func TrendingCSS() string {
	return `<style>
@keyframes firePulse {
  0%,100% { box-shadow:0 0 8px rgba(255,80,0,.5),0 2px 6px rgba(255,0,64,.3); transform:scale(1); }
  50%      { box-shadow:0 0 18px rgba(255,80,0,.9),0 4px 12px rgba(255,0,64,.6); transform:scale(1.04); }
}
@keyframes starGlow {
  0%,100% { filter:drop-shadow(0 0 3px rgba(239,68,68,.5)); }
  50%      { filter:drop-shadow(0 0 8px rgba(239,68,68,1)) drop-shadow(0 0 14px rgba(251,191,36,.6)); }
}
.tt-btn {
  flex:1;display:flex;flex-direction:column;align-items:center;justify-content:center;
  gap:6px;padding:14px 10px;border-radius:16px;border:2px solid transparent;
  cursor:pointer;transition:all .2s ease;font-family:inherit;
  background:var(--post-bg);border-color:var(--post-border);
  color:var(--post-text);
}
.tt-btn.active-pics {
  background:linear-gradient(135deg,rgba(255,107,0,.12),rgba(255,0,64,.12));
  border-color:#ff6b00;color:#ff6b00;
}
.tt-btn.active-text {
  background:linear-gradient(135deg,rgba(99,102,241,.12),rgba(139,92,246,.12));
  border-color:#6366f1;color:#6366f1;
}
.tt-btn .tt-icon { font-size:28px;line-height:1; }
.tt-btn .tt-label { font-size:12px;font-weight:900;letter-spacing:.04em;text-transform:uppercase; }
.tt-btn .tt-count { font-size:10px;font-weight:700;opacity:.7; }
.cat-pills { display:flex;flex-wrap:nowrap;gap:8px;overflow-x:auto;padding:4px 0 8px;
             scrollbar-width:none;-ms-overflow-style:none; }
.cat-pills::-webkit-scrollbar { display:none; }
.cat-pill {
  display:inline-flex;align-items:center;gap:4px;
  padding:5px 13px;border-radius:999px;white-space:nowrap;
  font-size:11px;font-weight:800;cursor:pointer;border:none;
  background:var(--post-bg);border:1.5px solid var(--post-border);
  color:var(--post-text);transition:all .15s ease;font-family:inherit;
}
.cat-pill:hover { opacity:.85; }
.cat-pill.active-pics { background:#ff6b00;border-color:#ff6b00;color:#fff; }
.cat-pill.active-text { background:#6366f1;border-color:#6366f1;color:#fff; }
.tt-section { display:none; }
.tt-section.visible { display:block; }
</style>`
}

func GetDecoratedUsernameWithStar(username string) string {
	return GetDecoratedUsername(username) + TrendingStarHTML(username)
}

// RenderTrendingPost is kept for compatibility with any other caller —
// TrendingFeedHandler below no longer uses it, since it batches
// viewer-specific state (follow/block/reaction) via prefetchPostRenderStates
// instead of fetching it per-post via this wrapper's single-post
// renderPostHTML call.
func RenderTrendingPost(
	id int, author, content string, createdAt time.Time, currentUser string,
	points int, allowJhappi bool, allowChupair bool,
	imageUrl, category, voiceUrl string, allowComments, followersOnly bool, expiresAt *time.Time, dpCompID int,
) string {
	return renderPostHTML(
		id, author, content, createdAt, currentUser,
		points, allowJhappi, allowChupair, imageUrl, category, voiceUrl,
		allowComments, followersOnly, expiresAt, dpCompID,
	)
}

// ── TRENDING FEED HANDLER — GET /trending?page=1&type=pics&cat=Sports ─────

const trendingPageSize = 10

func TrendingFeedHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Redirect("/login")
	}

	postType := c.Query("type", "pics")
	if postType != "pics" && postType != "text" {
		postType = "pics"
	}
	catFilter := strings.TrimSpace(c.Query("cat", ""))
	page := c.QueryInt("page", 1)
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * trendingPageSize

	// Toggle-button badges are always LIVE counts, independent of
	// whichever type/category is currently selected — matches the
	// original behavior exactly, just now derived from the shared cache
	// instead of two separate COUNT(*) queries per request.
	picsAll := getCachedTrendingPosts("pics")
	textAll := getCachedTrendingPosts("text")
	picsCount := len(picsAll)
	textCount := len(textAll)

	all := picsAll
	if postType == "text" {
		all = textAll
	}

	isFallback := false
	if len(all) == 0 && page == 1 {
		// New week just started and nothing has trended yet — the one
		// path that still queries Postgres directly on every request,
		// but it only fires in the narrow window right after a weekly
		// reset, so the extra load is negligible in practice.
		all = fetchTrendingPostsFromDB(postType, false)
		if len(all) > 0 {
			isFallback = true
		}
	}

	var categories []string
	seenCat := map[string]bool{}
	var filtered []cachedTrendPost
	for _, p := range all {
		cat := strings.TrimSpace(p.Category)
		if !seenCat[cat] {
			seenCat[cat] = true
			categories = append(categories, cat)
		}
		if catFilter == "" || strings.EqualFold(cat, catFilter) {
			filtered = append(filtered, p)
		}
	}
	sort.Strings(categories)

	total := len(filtered)
	start := offset
	if start > total {
		start = total
	}
	end := start + trendingPageSize
	if end > total {
		end = total
	}
	pagePosts := filtered[start:end]
	hasNext := end < total

	weekResetMsg := fmt.Sprintf("Resets in %d day(s) · Updated daily", daysUntilWeeklyReset())

	picsActive := ""
	textActive := ""
	if postType == "pics" {
		picsActive = "active-pics"
	} else {
		textActive = "active-text"
	}

	typeToggleHTML := fmt.Sprintf(`
<div style="display:flex;gap:10px;margin-bottom:14px;">
  <button class="tt-btn %s" onclick="location.href='/trending?type=pics&cat=&page=1'">
    <span class="tt-icon">📸</span>
    <span class="tt-label">Pics Posts</span>
    <span class="tt-count">%d trending</span>
  </button>
  <button class="tt-btn %s" onclick="location.href='/trending?type=text&cat=&page=1'">
    <span class="tt-icon">✍️</span>
    <span class="tt-label">Text Posts</span>
    <span class="tt-count">%d trending</span>
  </button>
</div>`,
		picsActive, picsCount,
		textActive, textCount,
	)

	activeClass := ""
	if postType == "pics" {
		activeClass = "active-pics"
	} else {
		activeClass = "active-text"
	}

	var pillsHTML strings.Builder
	if len(categories) > 0 {
		pillsHTML.WriteString(`<div class="cat-pills">`)
		allActive := ""
		if catFilter == "" {
			allActive = activeClass
		}
		pillsHTML.WriteString(fmt.Sprintf(
			`<button class="cat-pill %s" onclick="location.href='/trending?type=%s&cat=&page=1'">All</button>`,
			allActive, postType,
		))
		for _, cat := range categories {
			catActive := ""
			if strings.EqualFold(cat, catFilter) {
				catActive = activeClass
			}
			pillsHTML.WriteString(fmt.Sprintf(
				`<button class="cat-pill %s" onclick="location.href='/trending?type=%s&cat=%s&page=1'">%s</button>`,
				catActive, postType, cat, cat,
			))
		}
		pillsHTML.WriteString(`</div>`)
	}

	var postsHTML strings.Builder
	if isFallback {
		postsHTML.WriteString(`
<div style="text-align:center;padding:10px 16px 4px;">
  <span style="font-size:11px;color:#9ca3af;font-weight:700;">
    🕐 New week just started — showing last week's top posts until new ones trend
  </span>
</div>`)
	}
	if len(pagePosts) == 0 {
		icon := "📸"
		label := "No trending pics yet this week"
		if postType == "text" {
			icon = "✍️"
			label = "No trending text posts yet this week"
		}
		if catFilter != "" {
			label = fmt.Sprintf("No trending posts in \"%s\" yet", catFilter)
		}
		postsHTML.WriteString(fmt.Sprintf(`
<div style="text-align:center;padding:60px 16px;">
  <div style="font-size:48px;margin-bottom:12px;">%s</div>
  <p style="font-weight:800;font-size:16px;color:var(--post-text);">%s</p>
  <p style="font-size:13px;color:#9ca3af;margin-top:4px;">Be the first to spark a conversation!</p>
</div>`, icon, label))
	} else {
		// Batched, like the main feed (loadFeedFragment +
		// prefetchPostRenderStates) instead of the old per-post
		// renderPostHTML path, which fired its own individual DB round
		// trip per post for follow/block/reaction state.
		ids := make([]int, len(pagePosts))
		authors := make([]string, len(pagePosts))
		for i, p := range pagePosts {
			ids[i] = p.ID
			authors[i] = p.Author
		}
		states := prefetchPostRenderStates(currentUser, ids, authors)
		for _, p := range pagePosts {
			st := states[p.ID]
			postsHTML.WriteString(renderPostHTMLWithState(
				p.ID, p.Author, p.Content, p.CreatedAt, currentUser,
				p.Points, p.AllowJhappi, p.AllowChupair,
				p.ImageURL, p.Category, p.VoiceURL,
				p.AllowComments, p.FollowersOnly, p.ExpiresAt, p.DPCompID, &st,
			))
		}
	}

	var pagination strings.Builder
	pagination.WriteString(`<div style="display:flex;align-items:center;justify-content:center;gap:12px;padding:24px 16px;">`)
	if page > 1 {
		pagination.WriteString(fmt.Sprintf(
			`<a href="/trending?type=%s&cat=%s&page=%d" style="display:inline-flex;align-items:center;gap:6px;`+
				`padding:10px 20px;border-radius:12px;font-weight:800;font-size:13px;`+
				`background:var(--post-bg);border:1px solid var(--post-border);`+
				`color:var(--post-text);text-decoration:none;">Prev</a>`,
			postType, catFilter, page-1,
		))
	}
	pagination.WriteString(fmt.Sprintf(
		`<span style="font-size:12px;font-weight:700;color:#9ca3af;">Page %d</span>`, page,
	))
	if hasNext {
		pagination.WriteString(fmt.Sprintf(
			`<a href="/trending?type=%s&cat=%s&page=%d" style="display:inline-flex;align-items:center;gap:6px;`+
				`padding:10px 20px;border-radius:12px;font-weight:800;font-size:13px;`+
				`background:linear-gradient(135deg,#ff6b00,#ff0040);`+
				`color:#fff;text-decoration:none;border:none;">Next</a>`,
			postType, catFilter, page+1,
		))
	}
	pagination.WriteString(`</div>`)

	body := fmt.Sprintf(`
%s
<div style="max-width:640px;margin:0 auto;padding:16px 12px;">
  <div style="display:flex;align-items:center;justify-content:space-between;
    padding:14px 18px;margin-bottom:16px;
    background:linear-gradient(135deg,#ff6b00,#ff0040);
    border-radius:18px;color:#fff;
    box-shadow:0 4px 20px rgba(255,80,0,.3);">
    <div style="display:flex;align-items:center;gap:8px;">
      <span style="font-size:22px;">🔥</span>
      <div>
        <div style="font-size:16px;font-weight:900;letter-spacing:-.01em;">Trending This Week</div>
        <div style="font-size:10px;font-weight:700;opacity:.8;">%s</div>
      </div>
    </div>
    <a href="/trending-leaderboard"
       style="font-size:11px;font-weight:800;color:rgba(255,255,255,.9);text-decoration:none;
              background:rgba(255,255,255,.18);padding:6px 12px;border-radius:999px;
              border:1px solid rgba(255,255,255,.25);">
      Leaderboard
    </a>
  </div>
  %s
  %s
  <div id="trending-posts">%s</div>
  %s
</div>`,
		TrendingCSS(),
		weekResetMsg,
		typeToggleHTML,
		pillsHTML.String(),
		postsHTML.String(),
		pagination.String(),
	)

	return c.Type("html").SendString(Layout("🔥 Trending", body, currentUser))
}
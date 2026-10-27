package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"mysocialmediawebsite/pkg/platform"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/valyala/fasthttp"
)

// ─────────────────────────────────────────────────────────────────────────────
// SINGLEFLIGHT — prevents thundering herd on cache miss
// ─────────────────────────────────────────────────────────────────────────────

type call struct {
	wg  sync.WaitGroup
	val string
}

type singleflightGroup struct {
	mu sync.Mutex
	m  map[string]*call
}

var sfGroup = &singleflightGroup{m: make(map[string]*call)}

func (g *singleflightGroup) do(key string, fn func() string) string {
	g.mu.Lock()
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val
	}
	c := &call{}
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	c.val = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()
	return c.val
}

// ─────────────────────────────────────────────────────────────────────────────
// ONLINE USERS
// ─────────────────────────────────────────────────────────────────────────────

func getOnlineUsers() []string {
	ctx, cancel := context.WithTimeout(platform.Ctx, 2*time.Second)
	defer cancel()

	threshold := time.Now().Unix() - 300

	pipe := platform.RDB.Pipeline()
	pipe.ZRemRangeByScore(ctx, "active_sessions", "-inf", fmt.Sprintf("%d", threshold))
	cmd := pipe.ZRevRange(ctx, "active_sessions", 0, -1)

	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		log.Printf("Redis error (getOnlineUsers): %v", err)
		return []string{}
	}

	members, err := cmd.Result()
	if err != nil && err != redis.Nil {
		log.Printf("Redis ZRevRange error: %v", err)
		return []string{}
	}
	return members
}

func trackSession(username string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in trackSession for %s: %v", username, r)
		}
	}()
	ctx, cancel := context.WithTimeout(platform.Ctx, 2*time.Second)
	defer cancel()
	if err := platform.RDB.ZAdd(ctx, "active_sessions", redis.Z{
		Score:  float64(time.Now().Unix()),
		Member: username,
	}).Err(); err != nil {
		log.Printf("trackSession Redis error: %v", err)
	}
}

func createOnlineSlider(onlineUsers []string) string {
	count := len(onlineUsers)

	if count == 0 {
		return fmt.Sprintf(
			`<div class="os-empty" data-online-count="0">No one online right now</div>`,
		)
	}

	rankMap := getCachedUserRanks()

	type slideUser struct {
		username   string
		points     int
		profileImg string
		rank       int
		hasFire    bool
	}
	var users []slideUser
	for _, u := range onlineUsers {
		e, ok := rankMap[u]
		if !ok {
			// Admins are excluded from the rank cache; still show the birthday user.
			if !(isDenzBirthday() && isBirthdayUser(u)) {
				continue
			}
			m := getCachedAuthorMeta(u)
			e = rankEntry{ProfileImage: m.ProfileImage, HasTrendingStar: m.HasTrendingStar}
		}
		users = append(users, slideUser{u, e.Points, e.ProfileImage, e.Rank, e.HasTrendingStar})
	}
	sort.Slice(users, func(i, j int) bool {
		bi := isDenzBirthday() && isBirthdayUser(users[i].username)
		bj := isDenzBirthday() && isBirthdayUser(users[j].username)
		if bi != bj {
			return bi // birthday user always first
		}
		return users[i].points > users[j].points
	})

	var html strings.Builder
	html.WriteString(fmt.Sprintf(`<div class="os-track" id="online-slider-track" data-online-count="%d">`, count))

	for _, su := range users {
		username, profileImg, points, rank := su.username, su.profileImg, su.points, su.rank
		isElite := rank >= 1 && rank <= 10

		creature, ringCls, nameCls, ptCls, badgeBg, fireCls := "", "ring-default", "name-default", "pts-default", "", ""
		if isElite {
			creature = onlineCreature(rank)
			ringCls, nameCls, ptCls = onlineRankStyles(rank)
			badgeBg = badgeBgClass(rank)
		}
		if isElite || su.hasFire {
			fireCls = " fire-ring"
		}

		// Star badge next to the name — gated on the same su.hasFire
		// flag that already drives fireCls (both come from the cached
		// rankEntry.HasTrendingStar, refreshed at most once every
		// rankCacheTTL). Previously this called the live
		// TrendingStarHTML(username) check independently of fireCls, so
		// the ring and the star badge could disagree, and the star
		// could flicker in and out between polls even while the ring
		// stayed lit.
		trendingStarHTML := ""
		if su.hasFire {
			trendingStarHTML = TrendingStarHTML(username)
		}

		if profileImg == "" {
			profileImg = "https://ui-avatars.com/api/?name=" + username + "&background=random"
		}

		creatureHTML := ""
		if creature != "" {
			creatureHTML = fmt.Sprintf(`<span class="os-creature act-wiggle">%s</span>`, creature)
		}

		badgeHTML := ""
		if badgeBg != "" {
			badgeHTML = fmt.Sprintf(`<span class="os-badge %s">%d</span>`, badgeBg, rank)
		}

		onlineDot := `<span class="os-online-dot"></span>`

		bdayCls, bdayHat := "", ""
		if isDenzBirthday() && isBirthdayUser(username) {
			bdayCls = " os-bday"
			bdayHat = `<span class="os-bday-hat">` + birthdayHatSVG() + `</span>`
		}

		fmt.Fprintf(&html, `
<a href="/profile/%s" class="os-card%s">
  <div class="os-avatar-wrap">
    %s
    %s
    <div class="os-avatar-ring %s%s">
      <img src="%s" alt="%s" class="os-avatar-img" loading="lazy" decoding="async"
           onerror="this.onerror=null;this.src='https://ui-avatars.com/api/?name='+encodeURIComponent(this.alt)+'&background=random'">
    </div>
    %s
    %s
  </div>
  <p class="os-name %s"><span class="os-name-text">%s</span>%s</p>
  <p class="os-pts %s">%d pts</p>
</a>`,
			username, bdayCls, creatureHTML, bdayHat,
			ringCls, fireCls, profileImg, username,
			badgeHTML, onlineDot,
			nameCls, username, trendingStarHTML,
			ptCls, points,
		)
	}

	html.WriteString(`</div>`)
	return html.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// USER RANK CACHE — avoids re-running a full-table ROW_NUMBER() sort on
// every 8s online-slider poll from every open tab. Rebuilt at most once
// per rankCacheTTL, shared across all users/tabs via Redis.
// ─────────────────────────────────────────────────────────────────────────────

type rankEntry struct {
	Points          int    `json:"points"`
	ProfileImage    string `json:"profile_image"`
	Rank            int    `json:"rank"`
	HasTrendingStar bool   `json:"has_trending_star"`
}

const rankCacheKey = "user_ranks_cache"
const rankCacheTTL = 90 * time.Second

func getCachedUserRanks() map[string]rankEntry {
	if val, ok := CacheGet(rankCacheKey); ok {
		var m map[string]rankEntry
		if json.Unmarshal([]byte(val), &m) == nil {
			return m
		}
	}

	// CRASH FIX: this used to call rebuildUserRanks() directly and
	// unprotected. It does a full ROW_NUMBER() scan over the users table,
	// and this function is hit by EVERY open tab's 8-second online-slider
	// poll plus every homepage load. The instant the 90s cache TTL
	// lapsed, every one of those concurrent requests missed the cache and
	// fired that same expensive query at once — a cache stampede that
	// saturated the Postgres pool and cascaded into "context deadline
	// exceeded" on completely unrelated queries elsewhere on the site.
	// This matches the production logs exactly: dozens of
	// "rebuildUserRanks DB error" lines per second. Routing it through
	// the existing singleflight group means only ONE concurrent miss
	// actually queries Postgres — every other caller in that window waits
	// for and reuses its result.
	raw := sfGroup.do(rankCacheKey, func() string {
		m := rebuildUserRanks()
		b, err := json.Marshal(m)
		if err != nil {
			return "{}"
		}
		return string(b)
	})

	var m map[string]rankEntry
	if json.Unmarshal([]byte(raw), &m) != nil {
		return map[string]rankEntry{}
	}
	return m
}

var (
	rankMemoryCache   map[string]rankEntry
	rankMemoryMutex   sync.RWMutex
	rankMemoryUpdated time.Time
)

func rebuildUserRanks() map[string]rankEntry {
	// 1. Check Redis cache first
	if cachedStr, ok := CacheGet(rankCacheKey); ok && cachedStr != "" {
		var m map[string]rankEntry
		if json.Unmarshal([]byte(cachedStr), &m) == nil && len(m) > 0 {
			return m
		}
	}

	// 2. Check local RAM memory cache to prevent stampedes if multiple requests hit at once
	rankMemoryMutex.RLock()
	if len(rankMemoryCache) > 0 && time.Since(rankMemoryUpdated) < 10*time.Second {
		m := rankMemoryCache
		rankMemoryMutex.RUnlock()
		return m
	}
	rankMemoryMutex.RUnlock()

	// 3. Safely query PostgreSQL using context.Background() with a strict timeout
	rctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rows, err := db.Query(rctx, `
		SELECT u.username, COALESCE(u.points,0), COALESCE(u.profile_image,''),
		       COALESCE(u.has_trending_star,false),
		       COALESCE(r.site_rank, 0)
		FROM users u
		LEFT JOIN (
			SELECT username, ROW_NUMBER() OVER (ORDER BY COALESCE(points,0) DESC, id DESC) AS site_rank
			FROM users
			WHERE is_banned = false
			  AND COALESCE(user_type,'') != 'admin'
			  AND COALESCE(account_status,'approved') = 'approved'
			  AND LOWER(username) != 'heera'
		) r ON r.username = u.username
		WHERE u.is_banned = false
		  AND COALESCE(u.user_type,'') != 'admin'
		  AND COALESCE(u.account_status,'approved') = 'approved'`)
	if err != nil {
		log.Printf("rebuildUserRanks DB error: %v", err)
		// Fallback to stale RAM cache if DB fails so it never breaks
		rankMemoryMutex.RLock()
		if len(rankMemoryCache) > 0 {
			m := rankMemoryCache
			rankMemoryMutex.RUnlock()
			return m
		}
		rankMemoryMutex.RUnlock()
		return map[string]rankEntry{}
	}
	defer rows.Close()

	m := make(map[string]rankEntry)
	for rows.Next() {
		var uname, img string
		var pts int
		var hasStar bool
		var rank int64
		if rows.Scan(&uname, &pts, &img, &hasStar, &rank) == nil {
			m[uname] = rankEntry{Points: pts, ProfileImage: img, Rank: int(rank), HasTrendingStar: hasStar}
		}
	}

	// Save to Redis asynchronously or safely
	if b, err := json.Marshal(m); err == nil {
		CacheSet(rankCacheKey, string(b), rankCacheTTL)
	}

	// Save to local RAM cache
	rankMemoryMutex.Lock()
	rankMemoryCache = m
	rankMemoryUpdated = time.Now()
	rankMemoryMutex.Unlock()

	return m
}

// ─────────────────────────────────────────────────────────────────────────────
// RANK HELPERS
// ─────────────────────────────────────────────────────────────────────────────

func onlineCreature(rank int) string {
	return GetRankTheme(rank).Creature
}

func onlineRankStyles(rank int) (ring, name, pts string) {
	type s struct{ ring, name, pts string }
	styles := [...]s{
		{},
		{"ring-amber", "name-amber", "pts-amber"},
		{"ring-blue", "name-blue", "pts-blue"},
		{"ring-rose", "name-rose", "pts-rose"},
		{"ring-indigo", "name-indigo", "pts-indigo"},
		{"ring-cyan", "name-cyan", "pts-cyan"},
		{"ring-emerald", "name-emerald", "pts-emerald"},
		{"ring-green", "name-green", "pts-green"},
		{"ring-stone", "name-stone", "pts-stone"},
		{"ring-pink", "name-pink", "pts-pink"},
		{"ring-purple", "name-purple", "pts-purple"},
	}
	if rank >= 1 && rank <= 10 {
		return styles[rank].ring, styles[rank].name, styles[rank].pts
	}
	return "ring-default", "name-default", "pts-default"
}

func badgeBgClass(rank int) string {
	bg := [...]string{"", "bg-amber", "bg-blue", "bg-rose", "bg-indigo", "bg-cyan", "bg-emerald", "bg-green", "bg-stone", "bg-pink", "bg-purple"}
	if rank >= 1 && rank <= 10 {
		return bg[rank]
	}
	return ""
}

// ─────────────────────────────────────────────────────────────────────────────
// POST COUNT — cached in Redis, refreshed in background
// ─────────────────────────────────────────────────────────────────────────────

const postCountTTL = 60 * time.Second

func getCachedPostCount(filter string) int {
	key := "post_count:" + filter
	if val, ok := CacheGet(key); ok {
		if n, err := strconv.Atoi(val); err == nil {
			return n
		}
	}

	// Singleflight-protected for the same reason as getCachedUserRanks —
	// COUNT(*) over the full posts table isn't free, and without this a
	// burst of concurrent requests landing right as postCountTTL expires
	// would each run it independently instead of sharing one result.
	raw := sfGroup.do(key, func() string {
		count := fetchPostCountFromDB(filter)
		CacheSet(key, strconv.Itoa(count), postCountTTL)
		return strconv.Itoa(count)
	})

	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return n
}

func fetchPostCountFromDB(filter string) int {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var count int
	var err error
	switch filter {
	case "pics":
		err = db.QueryRow(ctx, `SELECT COUNT(*) FROM posts WHERE COALESCE(image_url,'') != '' AND COALESCE(image_url,'') !~* '\.(mp4|webm|mov|mkv)$'`).Scan(&count)
	case "videos":
		err = db.QueryRow(ctx, `SELECT COUNT(*) FROM posts WHERE COALESCE(image_url,'') ~* '\.(mp4|webm|mov|mkv)$'`).Scan(&count)
	case "text":
		err = db.QueryRow(ctx, `SELECT COUNT(*) FROM posts WHERE COALESCE(image_url,'') = ''`).Scan(&count)
	default:
		err = db.QueryRow(ctx, `SELECT COUNT(*) FROM posts`).Scan(&count)
	}
	if err != nil {
		log.Printf("fetchPostCountFromDB error (filter=%s): %v", filter, err)
		return 0
	}
	return count
}

// ─────────────────────────────────────────────────────────────────────────────
// NEW POSTS PEEK — fallback safety-net polling endpoint (kept as backup in
// case a browser's SSE connection drops silently through a flaky mobile
// network; the real-time path below is what actually delivers instant
// updates now).
// ─────────────────────────────────────────────────────────────────────────────

const latestPostIDTTL = 2 * time.Second

// getLatestPostID is polled constantly: once per homepage load, once per
// /feed/new-count call (every 25s from every open tab's safety-net poll,
// plus on every filter switch and SSE reconnect) — and used to hit
// Postgres directly every single time with zero caching. Actual real-time
// delivery of new posts is already handled independently by the SSE
// pub/sub system below (publishNewPost/feedStreamHandler); this value is
// only a bookkeeping baseline, so a short 2s singleflight-protected cache
// collapses request bursts without making anything feel less real-time.
func getLatestPostID(filter string) int {
	cacheKey := "latest_post_id:" + filter
	if val, ok := CacheGet(cacheKey); ok {
		if n, err := strconv.Atoi(val); err == nil {
			return n
		}
	}

	raw := sfGroup.do(cacheKey, func() string {
		id := fetchLatestPostIDFromDB(filter)
		// FIX 1: Only cache valid IDs (> 0). Never cache DB errors or timeouts.
		if id > 0 {
			CacheSet(cacheKey, strconv.Itoa(id), latestPostIDTTL)
		}
		return strconv.Itoa(id)
	})

	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return n
}

func fetchLatestPostIDFromDB(filter string) int {
	// FIX 2: Use context.Background() with a strict 1.5s timeout.
	dbCtx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	var id int
	var err error

	// FIX 3: Replaced COALESCE MAX() with index-backed ORDER BY id DESC LIMIT 1
	switch filter {
	case "pics":
		err = db.QueryRow(dbCtx, `
			SELECT id FROM posts 
			WHERE image_url IS NOT NULL AND image_url != ''
			  AND image_url !~* '\.(mp4|webm|mov|mkv)$'
			ORDER BY id DESC LIMIT 1`).Scan(&id)
	case "videos":
		err = db.QueryRow(dbCtx, `
			SELECT id FROM posts 
			WHERE image_url ~* '\.(mp4|webm|mov|mkv)$'
			ORDER BY id DESC LIMIT 1`).Scan(&id)
	case "text":
		err = db.QueryRow(dbCtx, `
			SELECT id FROM posts 
			WHERE (image_url IS NULL OR image_url = '') 
			ORDER BY id DESC LIMIT 1`).Scan(&id)
	default:
		err = db.QueryRow(dbCtx, `SELECT id FROM posts ORDER BY id DESC LIMIT 1`).Scan(&id)
	}

	if err != nil {
		if err != pgx.ErrNoRows {
			log.Printf("getLatestPostID error (filter=%s): %v", filter, err)
		}
		return 0
	}
	return id
}

func newPostsPeekHandler(c *fiber.Ctx) error {
	filter := c.Query("filter", "all")
	switch filter {
	case "pics", "videos", "text", "all":
	default:
		filter = "all"
	}
	sinceID := c.QueryInt("since", 0)

	latest := getLatestPostID(filter)
	newCount := 0

	// FIX 4: Only query if sinceID is valid and behind latest.
	if sinceID > 0 && latest > sinceID {
		dbCtx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()

		var err error
		// FIX 5: Cap the scan range with a subquery LIMIT 100.
		// UI polling only needs to know if there are new posts (e.g., "99+"),
		// counting past 100 scans unnecessary index tuples for stale clients.
		switch filter {
		case "pics":
			err = db.QueryRow(dbCtx, `
				SELECT COUNT(*) FROM (
					SELECT 1 FROM posts 
					WHERE id > $1 AND image_url IS NOT NULL AND image_url != ''
					  AND image_url !~* '\.(mp4|webm|mov|mkv)$'
					LIMIT 100
				) sub`, sinceID).Scan(&newCount)
		case "videos":
			err = db.QueryRow(dbCtx, `
				SELECT COUNT(*) FROM (
					SELECT 1 FROM posts 
					WHERE id > $1 AND image_url ~* '\.(mp4|webm|mov|mkv)$'
					LIMIT 100
				) sub`, sinceID).Scan(&newCount)
		case "text":
			err = db.QueryRow(dbCtx, `
				SELECT COUNT(*) FROM (
					SELECT 1 FROM posts 
					WHERE id > $1 AND (image_url IS NULL OR image_url = '') 
					LIMIT 100
				) sub`, sinceID).Scan(&newCount)
		default:
			err = db.QueryRow(dbCtx, `
				SELECT COUNT(*) FROM (
					SELECT 1 FROM posts 
					WHERE id > $1 
					LIMIT 100
				) sub`, sinceID).Scan(&newCount)
		}

		if err != nil {
			log.Printf("newPostsPeekHandler DB error (filter=%s, since=%d): %v", filter, sinceID, err)
			newCount = 0 // Gracefully degrade on DB bottleneck
		}
	}

	return c.JSON(fiber.Map{"count": newCount, "latest": latest})
}

// ─────────────────────────────────────────────────────────────────────────────
// REAL-TIME POST BROADCAST — Redis pub/sub + Server-Sent Events
//
// When any user creates a post, publishNewPost() fires a tiny message on a
// Redis channel. Every browser holding an open /feed/stream connection
// (one per tab) receives that message in milliseconds and either inserts
// the new post at the top of the feed instantly, or bumps the "new posts"
// pill — no polling delay, no manual refresh.
// ─────────────────────────────────────────────────────────────────────────────

const newPostChannel = "dostana:new_post"

// One process-wide Redis subscription, fanned out in memory to every SSE
// client. The previous design called Subscribe() per browser tab, and each
// of those holds a dedicated connection from the Redis pool (size 50). A
// few dozen open home-page tabs starved session lookups, caching, and
// everything else that shares that pool — the site then looked "crashed"
// until the process was killed.
var (
	feedHubOnce sync.Once
	feedSubsMu  sync.Mutex
	feedSubs    = map[chan string]struct{}{}
)

func startFeedHub() {
	feedHubOnce.Do(func() {
		go runFeedHub()
	})
}

func runFeedHub() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC in feed hub: %v — restarting in 2s", r)
			time.Sleep(2 * time.Second)
			go runFeedHub()
		}
	}()

	for {
		if platform.RDB == nil {
			time.Sleep(2 * time.Second)
			continue
		}
		pubsub := platform.RDB.Subscribe(context.Background(), newPostChannel, wordGameEventChannel)
		ch := pubsub.Channel()
		for msg := range ch {
			if msg == nil {
				continue
			}
			payload := msg.Payload
			feedSubsMu.Lock()
			for sub := range feedSubs {
				select {
				case sub <- payload:
				default:
					// slow client — drop this event rather than blocking the hub
				}
			}
			feedSubsMu.Unlock()
		}
		_ = pubsub.Close()
		log.Printf("feed hub Redis subscription ended — reconnecting in 1s")
		time.Sleep(time.Second)
	}
}

func subscribeFeedEvents() (chan string, bool) {
	startFeedHub()
	ch := make(chan string, 8)
	feedSubsMu.Lock()
	if len(feedSubs) >= 80 {
		feedSubsMu.Unlock()
		return nil, false
	}
	feedSubs[ch] = struct{}{}
	feedSubsMu.Unlock()
	return ch, true
}

func unsubscribeFeedEvents(ch chan string) {
	feedSubsMu.Lock()
	delete(feedSubs, ch)
	feedSubsMu.Unlock()
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// publishNewPost must be called right after a post is successfully
// inserted (in post.go's create-post handler). hasImage decides which
// filter tab ("pics" vs "text") the post belongs to, matching the same
// classification used everywhere else in this file.
func publishNewPost(postID int, mediaURL string, author string) {
	filterTag := "text"
	if mediaURL != "" {
		lower := strings.ToLower(mediaURL)
		switch {
		case strings.HasSuffix(lower, ".mp4"), strings.HasSuffix(lower, ".webm"),
			strings.HasSuffix(lower, ".mov"), strings.HasSuffix(lower, ".mkv"):
			filterTag = "videos"
		default:
			filterTag = "pics"
		}
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in publishNewPost goroutine (postID=%d): %v", postID, r)
			}
		}()

		// Post ids are monotonically increasing (SERIAL), so the new
		// latest-id cache entries can be written directly here instead of
		// waiting out latestPostIDTTL or re-querying Postgres — the
		// person who just posted (and anyone else) sees the fresh id
		// immediately instead of up to 2s later.
		idStr := strconv.Itoa(postID)
		CacheSet("latest_post_id:all", idStr, latestPostIDTTL)
		CacheSet("latest_post_id:"+filterTag, idStr, latestPostIDTTL)

		pctx, cancel := context.WithTimeout(platform.Ctx, 2*time.Second)
		defer cancel()
		payload := fmt.Sprintf("%d|%s|%s", postID, filterTag, author)
		if err := platform.RDB.Publish(pctx, newPostChannel, payload).Err(); err != nil {
			log.Printf("publishNewPost error: %v", err)
		}
	}()
}

// feedStreamHandler holds a long-lived SSE connection open and forwards
// every matching new-post event to the browser the instant it's published.
func feedStreamHandler(c *fiber.Ctx) error {
	filter := c.Query("filter", "all")
	switch filter {
	case "pics", "text", "all":
	default:
		filter = "all"
	}
	if key, ok := c.Locals("sseDecrKey").(string); ok {
		defer func() {
			decrCtx, decrCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			platform.RDB.Decr(decrCtx, key)
			decrCancel()
		}()
	}

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no") // stop nginx-style proxies from buffering SSE

	events, ok := subscribeFeedEvents()
	if !ok {
		return c.Status(fiber.StatusServiceUnavailable).SendString("too many live connections")
	}

	c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
		defer func() {
			unsubscribeFeedEvents(events)
			if r := recover(); r != nil {
				log.Printf("PANIC in SSE writer: %v", r)
			}
		}()

		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		lifetime := time.NewTimer(3 * time.Minute)
		defer lifetime.Stop()

		for {
			select {
			case payload, ok := <-events:
				if !ok {
					return
				}
				if strings.HasPrefix(payload, "WIN|") {
					// Word-game win broadcast — small toast event, not tied to
					// a filter tab, forwarded to every open tab as-is.
					if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
						return
					}
					if err := w.Flush(); err != nil {
						return
					}
					continue
				}
				parts := strings.SplitN(payload, "|", 3)
				if len(parts) != 3 {
					continue
				}
				postID, postFilter, postAuthor := parts[0], parts[1], parts[2]
				if filter != "all" && filter != postFilter {
					continue
				}
				if _, err := fmt.Fprintf(w, "data: %s|%s\n\n", postID, postAuthor); err != nil {
					return
				}
				if err := w.Flush(); err != nil {
					return
				}
			case <-ticker.C:
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return
				}
				if err := w.Flush(); err != nil {
					return
				}
			case <-lifetime.C:
				return
			}
		}
	}))

	return nil
}

// postFragmentHandler renders exactly one post by id — used by the
// real-time client to fetch and instantly prepend a single new post
// without reloading the whole feed. Reuses renderPostHTML, so blocking
// rules, follow state, and reaction buttons are all correctly personalized
// per viewer, and it silently returns nothing if the post was deleted or
// the viewer is blocked from seeing its author.
func postFragmentHandler(c *fiber.Ctx) error {
	idStr := c.Params("id")
	currentUser := getCurrentUser(c)

	var id, points, dpCompID int
	var author, content, img, cat, voice string
	var createdAt time.Time
	var allowJ, allowC bool

	var allowComments, followersOnly bool
	var expiresAt *time.Time

	fragCtx, fragCancel := context.WithTimeout(ctx, 3*time.Second)
	defer fragCancel()

	err := db.QueryRow(fragCtx, `
		SELECT p.id, p.author, p.content, p.created_at,
		       p.allow_jhappi, p.allow_chupair,
		       COALESCE(p.image_url,''), COALESCE(p.category,'General'),
		       COALESCE(u.points,0), COALESCE(p.voice_url,''),
		       COALESCE(p.allow_comments, true), COALESCE(p.followers_only, false), p.expires_at,
		       COALESCE(p.dp_competition_id,0)
		FROM posts p
		LEFT JOIN users u ON u.username = p.author
		WHERE p.id = $1`, idStr,
	).Scan(&id, &author, &content, &createdAt, &allowJ, &allowC, &img, &cat, &points, &voice, &allowComments, &followersOnly, &expiresAt, &dpCompID)
	if err != nil {
		return c.SendString("")
	}

	return c.SendString(renderPostHTML(id, author, content, createdAt, currentUser, points, allowJ, allowC, img, cat, voice, allowComments, followersOnly, expiresAt, dpCompID))
}

// ─────────────────────────────────────────────────────────────────────────────
// HANDLERS
// ─────────────────────────────────────────────────────────────────────────────

func onlineSliderHandler(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	return c.SendString(createOnlineSlider(getOnlineUsers()))
}

func feedHandler(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	filter := c.Query("filter", "all")
	currentUser := getCurrentUser(c)

	if currentUser == "" {
		return c.SendString(loadFeedFragment(page, 25, "", filter))
	}

	go trackSession(currentUser)

	cacheKey := fmt.Sprintf("feed:u:%s:p:%d:f:%s", currentUser, page, filter)

	feedCacheCtx, feedCacheCancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	cachedHTML, cacheErr := platform.RDB.Get(feedCacheCtx, cacheKey).Result()
	feedCacheCancel()
	if cacheErr == nil {
		c.Set("X-Cache", "HIT")
		return c.SendString(cachedHTML)
	}

	feedHTML := sfGroup.do(cacheKey, func() string {
		html := loadFeedFragment(page, 25, currentUser, filter)
		go func() {
			ctx2, cancel := context.WithTimeout(platform.Ctx, 2*time.Second)
			defer cancel()
			platform.RDB.Set(ctx2, cacheKey, html, 15*time.Second)
		}()
		return html
	})

	c.Set("X-Cache", "MISS")
	return c.SendString(feedHTML)
}

// ─────────────────────────────────────────────────────────────────────────────
// FOR ME — INLINE FRAGMENT (reuses forme.go's query logic, returns posts only)
// ─────────────────────────────────────────────────────────────────────────────

func forMeInlineHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Status(fiber.StatusUnauthorized).SendString(`<div class="feed-err">Please log in.</div>`)
	}

	page := c.QueryInt("page", 1)
	limit := 25
	offset := (page - 1) * limit

	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	query := `
		SELECT p.id, p.author, p.content, p.created_at, p.allow_jhappi, p.allow_chupair,
		       COALESCE(p.image_url, ''), COALESCE(p.category, 'General'), COALESCE(u.points, 0), COALESCE(p.voice_url, ''),
		       COALESCE(p.allow_comments, true), COALESCE(p.followers_only, false), p.expires_at,
		       COALESCE(p.dp_competition_id, 0)
		FROM posts p
		JOIN followers f ON p.author = f.followee
		JOIN users u ON p.author = u.username
		WHERE f.follower = $1
		  AND (p.dp_competition_id = 0 OR p.id = (
		      SELECT MIN(id) FROM posts WHERE dp_competition_id = p.dp_competition_id
		  ))
		ORDER BY p.id DESC
		LIMIT $2 OFFSET $3`

	rows, err := db.Query(queryCtx, query, currentUser, limit, offset)
	if err != nil {
		log.Printf("forMeInlineHandler DB error: %v", err)
		return c.SendString(`<div class="feed-err">Error loading your feed</div>`)
	}
	defer rows.Close()

	var postsHTML strings.Builder
	postCount := 0
	for rows.Next() {
		var id, points, dpCompID int
		var author, content, img, cat, voice string
		var createdAt time.Time
		var allowJ, allowC, allowComments, followersOnly bool
		var expiresAt *time.Time

		if err := rows.Scan(&id, &author, &content, &createdAt, &allowJ, &allowC, &img, &cat, &points, &voice, &allowComments, &followersOnly, &expiresAt, &dpCompID); err != nil {
			log.Printf("forMeInlineHandler scan error: %v", err)
			continue
		}
		postsHTML.WriteString(renderPostHTML(id, author, content, createdAt, currentUser, points, allowJ, allowC, img, cat, voice, allowComments, followersOnly, expiresAt, dpCompID))
		postCount++
	}

	if postCount == 0 {
		return c.SendString(`
<div style="text-align:center;padding:40px 16px;color:var(--col-muted);">
  <div style="font-size:36px;margin-bottom:10px;">✨</div>
  <p style="font-weight:800;font-size:14px;color:var(--col-text);margin:0 0 4px;">Aap ki list khali hai!</p>
  <p style="font-size:11px;margin:0 0 14px;">Un logo ki posts yahan nazar ayengi jinhein aap follow karte hain.</p>
  <a href="/search" style="display:inline-block;padding:8px 16px;background:var(--col-accent);color:#fff;border-radius:10px;font-size:11px;font-weight:800;text-decoration:none;">Naye Dost Banayein</a>
</div>`)
	}

	return c.SendString(postsHTML.String())
}

// ─────────────────────────────────────────────────────────────────────────────
// VOICE MESSAGE UPLOAD HANDLER
// ─────────────────────────────────────────────────────────────────────────────

func voiceMessageHandler(c *fiber.Ctx) error {
	author := getCurrentUser(c)
	if author == "" {
		return c.Status(fiber.StatusUnauthorized).SendString("Not logged in")
	}

	file, err := c.FormFile("voice")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("No voice file received")
	}

	const maxSize = 5 << 20
	if file.Size > maxSize {
		return c.Status(fiber.StatusRequestEntityTooLarge).SendString("Voice message too large (max 5 MB)")
	}

	mimeToExt := map[string]string{
		"audio/webm":  ".webm",
		"audio/ogg":   ".ogg",
		"audio/mp4":   ".mp4",
		"audio/mpeg":  ".mp3",
		"audio/wav":   ".wav",
		"audio/x-wav": ".wav",
	}
	ext := ".webm"
	if e, ok := mimeToExt[file.Header.Get("Content-Type")]; ok {
		ext = e
	}

	filename := fmt.Sprintf("voice_%d_%s%s", time.Now().UnixNano(), author, ext)
	savePath := "./uploads/home_voice_chat/" + filename
	publicURL := "/uploads/home_voice_chat/" + filename

	if err := c.SaveFile(file, savePath); err != nil {
		log.Printf("ERROR saving voice file [%s]: %v", savePath, err)
		return c.Status(fiber.StatusInternalServerError).SendString("Failed to save voice message")
	}

	var insertedID int
	voiceCtx, voiceCancel := context.WithTimeout(ctx, 5*time.Second)
	defer voiceCancel()
	err = db.QueryRow(voiceCtx, `
		INSERT INTO home_voice_messages (author, file_url, file_size, duration_seconds)
		VALUES ($1, $2, $3, $4)
		RETURNING id`,
		author, publicURL, file.Size, 0,
	).Scan(&insertedID)
	if err != nil {
		log.Printf("ERROR inserting voice message: %v", err)
		return c.Status(fiber.StatusInternalServerError).
			JSON(fiber.Map{"ok": false, "url": publicURL, "err": "db insert failed"})
	}

	return c.JSON(fiber.Map{
		"id":     insertedID,
		"url":    publicURL,
		"author": author,
		"ok":     true,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// FEED FRAGMENT
// ─────────────────────────────────────────────────────────────────────────────

func loadFeedFragment(page, limit int, currentUser, filter string) string {
	offset := (page - 1) * limit

	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	base := `
		SELECT p.id, p.author, p.content, p.created_at,
		       p.allow_jhappi, p.allow_chupair,
		       COALESCE(p.image_url,''),
		       COALESCE(p.category,'General'),
		       COALESCE(u.points, 0),
		       COALESCE(p.voice_url,''),
		       COALESCE(p.allow_comments, true),
		       COALESCE(p.followers_only, false),
		       p.expires_at,
		       COALESCE(p.dp_competition_id, 0)
		FROM posts p
		LEFT JOIN users u ON u.username = p.author
		WHERE (p.dp_competition_id = 0 OR p.id = (
			SELECT MIN(id) FROM posts WHERE dp_competition_id = p.dp_competition_id
		)) `

	var rows pgx.Rows
	var err error
	switch filter {
	case "pics":
		rows, err = db.Query(queryCtx,
			base+`AND COALESCE(p.image_url,'') != '' AND COALESCE(p.image_url,'') !~* '\.(mp4|webm|mov|mkv)$' ORDER BY p.id DESC LIMIT $1 OFFSET $2`,
			limit, offset)
	case "videos":
		rows, err = db.Query(queryCtx,
			base+`AND COALESCE(p.image_url,'') ~* '\.(mp4|webm|mov|mkv)$' ORDER BY p.id DESC LIMIT $1 OFFSET $2`,
			limit, offset)
	case "text":
		rows, err = db.Query(queryCtx,
			base+`AND COALESCE(p.image_url,'') = '' ORDER BY p.id DESC LIMIT $1 OFFSET $2`,
			limit, offset)
	default:
		rows, err = db.Query(queryCtx,
			base+`ORDER BY p.id DESC LIMIT $1 OFFSET $2`,
			limit, offset)
	}
	if err != nil {
		log.Printf("Feed Query Error (filter=%s page=%d): %v", filter, page, err)
		return `<div class="feed-err">Error loading feed</div>`
	}
	defer rows.Close()

	type feedRow struct {
		id, points, dpCompID                         int
		author, content, img, cat, voice             string
		createdAt                                    time.Time
		allowJ, allowC, allowComments, followersOnly bool
		expiresAt                                    *time.Time
	}
	var feedRows []feedRow
	for rows.Next() {
		var r feedRow
		if err := rows.Scan(&r.id, &r.author, &r.content, &r.createdAt, &r.allowJ, &r.allowC, &r.img, &r.cat, &r.points, &r.voice, &r.allowComments, &r.followersOnly, &r.expiresAt, &r.dpCompID); err != nil {
			log.Printf("Feed Scan Error: %v", err)
			continue
		}
		feedRows = append(feedRows, r)
	}

	ids := make([]int, 0, len(feedRows))
	authors := make([]string, 0, len(feedRows))
	for _, r := range feedRows {
		ids = append(ids, r.id)
		authors = append(authors, r.author)
	}
	batch := prefetchPostRenderStates(currentUser, ids, authors)

	var postsHTML strings.Builder
	for _, r := range feedRows {
		st := batch[r.id]
		postsHTML.WriteString(renderPostHTMLWithState(r.id, r.author, r.content, r.createdAt, currentUser, r.points, r.allowJ, r.allowC, r.img, r.cat, r.voice, r.allowComments, r.followersOnly, r.expiresAt, r.dpCompID, &st))
	}
	if err := rows.Err(); err != nil {
		log.Printf("Feed rows.Err: %v", err)
	}

	totalPosts := getCachedPostCount(filter)
	totalPages := (totalPosts + limit - 1) / limit
	if page >= totalPages {
		return postsHTML.String()
	}

	// Load More: a single button that, when tapped, hx-gets the next page
	// and replaces itself (outerHTML) with that page's posts followed by
	// its own new Load More button — so tapping repeatedly appends further
	// down the feed instead of navigating to a numbered page.
	//
	// hx-trigger="click consume" stops this button's own click from
	// bubbling up to any ancestor. This matters because #feed used to
	// carry a bare hx-get with no explicit trigger, which defaults to
	// "click" in htmx — so every tap inside the feed (this button
	// included) was silently re-triggering a full reload back to page 1
	// a moment after the real click handler ran. #feed no longer carries
	// hx-get at all (see homeGet), but "consume" is kept here as a second
	// line of defense against the same class of bug.
	nextPage := page + 1
	loadMoreID := fmt.Sprintf("load-more-%s-%d", filter, nextPage)
	loadMoreHTML := fmt.Sprintf(`
    <div id="%s" style="text-align:center;margin:18px 0;">
      <button type="button"
              hx-get="/feed?filter=%s&page=%d"
              hx-target="#%s"
              hx-swap="outerHTML"
              hx-trigger="click consume"
              style="display:inline-flex;align-items:center;gap:8px;padding:10px 24px;background:var(--col-surface);border:1.5px solid var(--col-border);border-radius:999px;font-size:12.5px;font-weight:800;color:var(--col-accent);cursor:pointer;">
        <span>Load More</span>
        <span>↓</span>
      </button>
    </div>`, loadMoreID, filter, nextPage, loadMoreID)

	return postsHTML.String() + loadMoreHTML
}

// ─────────────────────────────────────────────────────────────────────────────
// HOME PAGE
// ─────────────────────────────────────────────────────────────────────────────

func homeGet(c *fiber.Ctx) error {
	user := getCurrentUser(c)
	if user == "" {
		return c.Redirect("/signup")
	}

	page := c.QueryInt("page", 1)
	filter := c.Query("filter", "all")
	switch filter {
	case "pics", "videos", "text", "all":
	default:
		filter = "all"
	}

	trackSession(user)

	type sliderResult struct {
		html  string
		count int
	}
	type feedResult struct{ html string }

	sliderCh := make(chan sliderResult, 1)
	feedCh := make(chan feedResult, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in homeGet slider goroutine (user=%s): %v", user, r)
				sliderCh <- sliderResult{`<div class="os-empty" data-online-count="0">Connection trouble…</div>`, 0}
			}
		}()
		users := getOnlineUsers()
		sliderCh <- sliderResult{createOnlineSlider(users), len(users)}
	}()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in homeGet feed goroutine (user=%s, filter=%s): %v", user, filter, r)
				feedCh <- feedResult{`<div class="feed-err">Error loading feed</div>`}
			}
		}()
		feedCh <- feedResult{loadFeedFragment(page, 25, user, filter)}
	}()

	sliderRes := <-sliderCh
	sliderHTML := sliderRes.html
	onlineCount := sliderRes.count
	feedFragment := (<-feedCh).html

	latestID := getLatestPostID(filter)

	// pendingApproval is set by AccountGateCheck for accounts still
	// awaiting admin review. approvalNoticeHTML is a ONE-TIME banner —
	// the Redis flag is consumed (deleted) the instant it's read, so it
	// only ever shows on the first home-page load after an admin
	// approves the account (see adminApprovePendingHandler).
	pendingApproval, _ := c.Locals("pendingApproval").(bool)
	noticeHTML := ""
	if pendingApproval {
		noticeHTML = renderPendingApprovalBanner()
	} else if _, err := rdb.Get(ctx, "approval_notice:"+user).Result(); err == nil {
		rdb.Del(ctx, "approval_notice:"+user)
		noticeHTML = renderApprovalNoticeBanner()
	}

	c.Set("Content-Type", "text/html")

	// Toolbar and Latest/Trending sub-tabs are now real navigable links
	// (hx-boost="false", same pattern already used for DP Competition)
	// instead of onclick JS handlers that only ever swapped #feed's
	// innerHTML in place. A JS-only swap never changes the URL, so
	// refreshing, sharing a link, or hitting back always dropped the
	// person back on the plain "/" home feed regardless of which tab
	// was open — and any htmx auto-refresh elsewhere on the page (the
	// online slider's "every 8s" poll, badge polling, etc.) had nothing
	// to do with actual tab state, so it could look like the tab
	// silently reset. Real anchors make every tab its own bookmarkable,
	// refreshable, back-button-safe page.
	activeFilterFor := func(f string) string {
		if f == filter {
			return " f-active"
		}
		return ""
	}
	toolbarHTML := fmt.Sprintf(`
<div class="feed-toolbar">
  <a href="/for-me" hx-boost="false" id="sq-forme-btn" class="sq-btn sq-forme filter-btn" style="text-decoration:none;"><span>✨</span><span>For Me</span></a>
  <a href="/?filter=all" hx-boost="false" data-filter="all" class="sq-btn filter-btn%s" style="text-decoration:none;"><span>🌍</span><span>All</span></a>
  <a href="/?filter=text" hx-boost="false" data-filter="text" class="sq-btn filter-btn%s" style="text-decoration:none;"><span>📝</span><span>Text</span></a>
  <a href="/?filter=pics" hx-boost="false" data-filter="pics" class="sq-btn filter-btn%s" style="text-decoration:none;"><span>🖼️</span><span>Pics</span></a>
  <a href="/?filter=videos" hx-boost="false" data-filter="videos" class="sq-btn filter-btn%s" style="text-decoration:none;"><span>🎥</span><span>Videos</span></a>
  <a href="/dpcompete" hx-boost="false" id="sq-dpcompete-btn" class="sq-btn sq-dpcompete" style="text-decoration:none;"><span>🥊</span><span>DP Battle</span></a>
</div>`,
		activeFilterFor("all"), activeFilterFor("text"), activeFilterFor("pics"), activeFilterFor("videos"))

	ltSubbarHidden := "hidden"
	ltSubbarDisplay := "display:none;"
	if filter != "all" {
		ltSubbarHidden = ""
		ltSubbarDisplay = "display:flex;"
	}
	// Latest just links back to the same filter (equivalent to the old
	// loadLatestForType); Trending links to the real /trending page,
	// which already accepts the same ?type= values as the old inline
	// fetch did.
	ltSubbarHTML := fmt.Sprintf(`
<div id="lt-subbar" class="lt-tabs %s" style="%s">
  <a href="/?filter=%s" hx-boost="false" id="lt-latest-btn" class="lt-tab lt-active" style="text-decoration:none;"><span>🕒</span><span>Latest</span></a>
  <a href="/trending?type=%s" hx-boost="false" id="lt-trending-btn" class="lt-tab" style="text-decoration:none;"><span>🔥</span><span>Trending</span></a>
</div>`, ltSubbarHidden, ltSubbarDisplay, filter, filter)

	homeContent := fmt.Sprintf(`
<style>
/* ══════════════════════════════════════════════════════════
   DESIGN SYSTEM — 2026 Social Feed
   ══════════════════════════════════════════════════════════ */

:root {
  --home-radius: 16px;
  --home-radius-sm: 10px;
  --home-gap: 10px;
  --home-px: 14px;
  --col-bg: #ffffff;
  --col-border: #f0f0f0;
  --col-surface: #f8f9fb;
  --col-text: #0d0d0d;
  --col-muted: #6b7280;
  --col-accent: #2563eb;
  --col-accent-soft: #eff6ff;
  --col-green: #16a34a;
  --col-green-soft: #f0fdf4;
  --col-red: #dc2626;
  --col-red-soft: #fef2f2;
  --col-online: #22c55e;
}
.dark {
  --col-bg: #111827;
  --col-border: #1f2937;
  --col-surface: #1a2332;
  --col-text: #f9fafb;
  --col-muted: #9ca3af;
  --col-accent-soft: #1e3a5f;
  --col-green-soft: #052e16;
  --col-red-soft: #450a0a;
}

.home-card {
  background: var(--col-bg);
  border: 1px solid var(--col-border);
  border-radius: var(--home-radius);
  overflow: hidden;
  margin-bottom: var(--home-gap);
  box-shadow: 0 1px 2px rgba(0,0,0,.04), 0 2px 10px rgba(0,0,0,.03);
}

/* ══════════════════════════════════════════
   ONLINE SLIDER
   ══════════════════════════════════════════ */

.os-header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px var(--home-px) 4px;
}
.os-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 4px 10px;
  background: var(--col-green-soft);
  border: 1px solid #bbf7d0;
  border-radius: 999px;
  font-size: 10px;
  font-weight: 800;
  color: var(--col-green);
  letter-spacing: .03em;
  text-transform: uppercase;
  white-space: nowrap;
  flex-shrink: 0;
  transition: all .2s;
}
.dark .os-pill { border-color: #14532d; }
.os-pill-dot {
  width: 6px;
  height: 6px;
  background: var(--col-online);
  border-radius: 50%%;
  animation: pulse-dot 1.8s ease-in-out infinite;
}
@keyframes pulse-dot {
  0%%,100%% { opacity: 1; transform: scale(1); }
  50%%       { opacity: .5; transform: scale(.7); }
}
.os-search-wrap {
  flex: 1;
  min-width: 0;
  position: relative;
}
.os-search-icon {
  position: absolute;
  left: 9px;
  top: 50%%;
  transform: translateY(-50%%);
  font-size: 11px;
  opacity: .45;
  pointer-events: none;
}
.os-search-input {
  width: 100%%;
  box-sizing: border-box;
  padding: 6px 10px 6px 28px;
  font-size: 11px;
  font-weight: 600;
  border: 1.5px solid var(--col-border);
  border-radius: 999px;
  background: var(--col-surface);
  color: var(--col-text);
  outline: none;
  -webkit-appearance: none;
  transition: border-color .15s;
}
.os-search-input:focus { border-color: var(--col-accent); }

.os-toggle-btn {
  flex-shrink: 0;
  width: 26px;
  height: 26px;
  border-radius: 50%%;
  border: 1.5px solid var(--col-border);
  background: var(--col-surface);
  color: var(--col-muted);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  transition: transform .15s, background .15s, color .15s, border-color .15s;
}
.os-toggle-btn:active { transform: scale(0.88); }
.os-toggle-btn.expanded { background: var(--col-accent-soft); color: var(--col-accent); border-color: var(--col-accent); }
.os-toggle-arrow { font-size: 11px; line-height: 1; display: inline-block; transition: transform .2s ease; }
.os-toggle-btn.expanded .os-toggle-arrow { transform: rotate(180deg); }

/* Collapsed by default — only the username track hides. The count pill
   and search box live in .os-header, outside this element, so they're
   never affected. hx-swap="innerHTML" only replaces this div's children
   on every 8s poll, never the div itself, so the expanded/collapsed
   class (and therefore the user's choice) survives every refresh. */
#online-slider {
  max-height: 0;
  overflow: hidden;
  border-bottom: 1px solid transparent;
  transition: max-height .25s ease, border-color .2s ease;
}
#online-slider.expanded {
  max-height: 220px;
  overflow-x: auto;
  overflow-y: hidden;
  border-bottom-color: var(--col-border);
}

.os-track {
  display: flex;
  flex-wrap: nowrap;
  gap: 4px;
  padding: 4px var(--home-px) 10px;
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
  overscroll-behavior-x: contain;
  scrollbar-width: none;
}
.os-track::-webkit-scrollbar { display: none; }

.os-empty {
  font-size: 11px;
  color: var(--col-muted);
  text-align: center;
  padding: 16px;
  font-style: italic;
}

.os-card {
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  width: 52px;
  text-decoration: none;
  -webkit-tap-highlight-color: transparent;
  padding-top: 18px;
  position: relative;
  border-radius: var(--home-radius-sm);
  transition: transform .15s;
}
.os-card:active { transform: scale(0.9); }

.os-avatar-wrap {
  position: relative;
  width: 40px;
  height: 40px;
  margin-bottom: 4px;
}
.os-avatar-ring {
  width: 40px;
  height: 40px;
  border-radius: 50%%;
  overflow: hidden;
  border: 2.5px solid transparent;
  box-shadow: 0 2px 8px rgba(0,0,0,.12);
  background-clip: padding-box;
}
.os-avatar-img {
  width: 100%%;
  height: 100%%;
  object-fit: cover;
  display: block;
}
.os-online-dot {
  position: absolute;
  bottom: 1px;
  right: 1px;
  width: 9px;
  height: 9px;
  background: var(--col-online);
  border-radius: 50%%;
  border: 2px solid var(--col-bg);
  z-index: 10;
}
.os-badge {
  position: absolute;
  top: -3px;
  right: -5px;
  width: 15px;
  height: 15px;
  border-radius: 50%%;
  border: 1.5px solid var(--col-bg);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 7px;
  font-weight: 900;
  color: white;
  z-index: 20;
}
.os-creature {
  position: absolute;
  top: -16px;
  left: 50%%;
  transform: translateX(-50%%);
  font-size: 14px;
  z-index: 30;
  pointer-events: none;
  line-height: 1;
}
.os-name {
  font-size: 8px;
  font-weight: 800;
  letter-spacing: .01em;
  text-align: center;
  width: 100%%;
  line-height: 1.2;
  margin: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 2px;
  flex-wrap: wrap;
  overflow: hidden;
}
.os-name-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 48px;
}
.os-pts {
  font-size: 7px;
  font-weight: 700;
  text-align: center;
  margin: 1px 0 0;
  color: var(--col-muted);
}

@keyframes creature-wiggle {
  0%%,100%% { transform: translateX(-50%%) rotate(0deg); }
  25%%       { transform: translateX(-52%%) rotate(-5deg) translateY(-1px); }
  75%%       { transform: translateX(-48%%) rotate(5deg) translateY(-1px); }
}
.act-wiggle { animation: creature-wiggle 1.8s ease-in-out infinite; }

.ring-default  { border-color: var(--col-border); }
.ring-amber    { border-color: #f59e0b; box-shadow: 0 0 0 2px rgba(245,158,11,.2); }
.ring-blue     { border-color: #f97316; box-shadow: 0 0 0 2px rgba(249,115,22,.2); }
.ring-rose     { border-color: #b45309; box-shadow: 0 0 0 2px rgba(180,83,9,.2); }
.ring-indigo   { border-color: #3b82f6; box-shadow: 0 0 0 2px rgba(59,130,246,.2); }
.ring-cyan     { border-color: #06b6d4; box-shadow: 0 0 0 2px rgba(6,182,212,.2); }
.ring-emerald  { border-color: #10b981; box-shadow: 0 0 0 2px rgba(16,185,129,.2); }
.ring-green    { border-color: #84cc16; box-shadow: 0 0 0 2px rgba(132,204,22,.2); }
.ring-stone    { border-color: #64748b; box-shadow: 0 0 0 2px rgba(100,116,139,.2); }
.ring-pink     { border-color: #f43f5e; box-shadow: 0 0 0 2px rgba(244,63,94,.2); }
.ring-purple   { border-color: #d946ef; box-shadow: 0 0 0 2px rgba(217,70,239,.2); }

/* ── Top Star (trending) border — premium gold, one slow drift, NO
   brightness/box-shadow pulsing (that combo was the flashing). ── */
@keyframes topStarDrift { 0%% { background-position: 0%% 50%%; } 100%% { background-position: 200%% 50%%; } }
.fire-ring {
  border: 3px solid transparent !important;
  background-image: linear-gradient(var(--col-bg), var(--col-bg)),
                     linear-gradient(115deg,#78350f,#d97706,#fde68a,#fff7cd,#fde68a,#d97706,#78350f);
  background-origin: border-box;
  background-clip: padding-box, border-box;
  background-size: 100%% 100%%, 260%% 100%%;
  animation: topStarDrift 8s linear infinite;
  box-shadow: 0 0 10px 1px rgba(217,119,6,.35);
}

.name-default { color: var(--col-text); }
.name-amber   { color: #b45309; } .dark .name-amber   { color: #fbbf24; }
.name-blue    { color: #c2410c; } .dark .name-blue    { color: #fb923c; }
.name-rose    { color: #92400e; } .dark .name-rose    { color: #d97706; }
.name-indigo  { color: #1d4ed8; } .dark .name-indigo  { color: #60a5fa; }
.name-cyan    { color: #0e7490; } .dark .name-cyan    { color: #22d3ee; }
.name-emerald { color: #047857; } .dark .name-emerald { color: #34d399; }
.name-green   { color: #4d7c0f; } .dark .name-green   { color: #a3e635; }
.name-stone   { color: #475569; } .dark .name-stone   { color: #94a3b8; }
.name-pink    { color: #be123c; } .dark .name-pink    { color: #fb7185; }
.name-purple  { color: #a21caf; } .dark .name-purple  { color: #e879f9; }

.pts-default { color: var(--col-muted); }
.pts-amber   { color: #92400e; } .dark .pts-amber   { color: #fcd34d; }
.pts-blue    { color: #9a3412; } .dark .pts-blue    { color: #fdba74; }
.pts-rose    { color: #78350f; } .dark .pts-rose    { color: #fbbf24; }
.pts-indigo  { color: #1e40af; } .dark .pts-indigo  { color: #93c5fd; }
.pts-cyan    { color: #155e75; } .dark .pts-cyan    { color: #67e8f9; }
.pts-emerald { color: #065f46; } .dark .pts-emerald { color: #6ee7b7; }
.pts-green   { color: #3f6212; } .dark .pts-green   { color: #bef264; }
.pts-stone   { color: #334155; } .dark .pts-stone   { color: #cbd5e1; }
.pts-pink    { color: #9f1239; } .dark .pts-pink    { color: #fda4af; }
.pts-purple  { color: #86198f; } .dark .pts-purple  { color: #f0abfc; }

.bg-amber   { background:#f59e0b; } .bg-blue    { background:#f97316; }
.bg-rose    { background:#b45309; } .bg-indigo  { background:#3b82f6; }
.bg-cyan    { background:#06b6d4; } .bg-emerald { background:#10b981; }
.bg-green   { background:#84cc16; } .bg-stone   { background:#64748b; }
.bg-pink    { background:#f43f5e; } .bg-purple  { background:#d946ef; }

/* ══════════════════════════════════════════
   POST COMPOSER CARD
   ══════════════════════════════════════════ */
.composer-card {
  background: var(--col-bg);
  border: 1px solid var(--col-border);
  border-radius: var(--home-radius);
  overflow: hidden;
  box-shadow: 0 1px 3px rgba(0,0,0,.06);
  margin-bottom: var(--home-gap);
}

.composer-row {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 6px 10px;
}

.cmp-mic {
  flex: 0 0 auto;
  width: 28px;
  height: 28px;
  border-radius: 50%%;
  border: 1.5px solid #bbf7d0;
  background: var(--col-green-soft);
  color: var(--col-green);
  font-size: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  padding: 0;
  -webkit-tap-highlight-color: transparent;
  transition: background .12s, border-color .12s, transform .1s;
}
.cmp-mic:active { transform: scale(0.9); }
.cmp-mic.recording {
  color: var(--col-red);
  background: var(--col-red-soft);
  border-color: #fecaca;
  animation: mic-pulse 1s ease-in-out infinite;
}
@keyframes mic-pulse {
  0%%,100%% { box-shadow: 0 0 0 0 rgba(220,38,38,.3); }
  50%%       { box-shadow: 0 0 0 5px rgba(220,38,38,0); }
}

#mic-timer {
  font-size: 9px;
  font-weight: 800;
  color: var(--col-red);
  min-width: 24px;
  display: none;
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}
#mic-timer.visible { display: inline-block; }

.composer-input-wrap {
  position: relative;
  flex: 1;
  min-width: 0;
}
.composer-textarea {
  display: block;
  width: 100%%;
  box-sizing: border-box;
  padding: 6px 30px 6px 10px;
  background: var(--col-surface);
  border: 1.5px solid var(--col-border);
  border-radius: 999px;
  outline: none;
  font-size: 12.5px;
  line-height: 1.25;
  resize: none;
  color: var(--col-text);
  min-height: 28px;
  max-height: 80px;
  -webkit-appearance: none;
  transition: border-color .15s;
}
.composer-textarea:focus { border-color: var(--col-accent); }
.composer-textarea::placeholder { color: var(--col-muted); }

.cmp-send-btn {
  position: absolute;
  right: 2px;
  top: 2px;
  width: 24px;
  height: 24px;
  border-radius: 50%%;
  border: none;
  background: var(--col-accent);
  color: #fff;
  font-size: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  padding: 0;
  -webkit-tap-highlight-color: transparent;
  box-shadow: 0 2px 6px rgba(37,99,235,.35);
  transition: transform .1s;
}
.cmp-send-btn:active { transform: scale(0.9); }
.cmp-send-btn.htmx-request { opacity: .55; pointer-events: none; }

#voice-preview {
  display: none;
  align-items: center;
  gap: 6px;
  margin: 4px 10px 0;
  padding: 5px 8px;
  background: var(--col-green-soft);
  border: 1px solid #bbf7d0;
  border-radius: var(--home-radius-sm);
}
#voice-preview.visible { display: flex; }
#voice-preview audio { height: 26px; flex: 1; min-width: 0; }
.voice-cancel-btn {
  background: #ef4444;
  color: white;
  border: none;
  border-radius: 50%%;
  width: 16px;
  height: 16px;
  font-size: 8px;
  cursor: pointer;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0;
}

.cmp-more-link {
  text-align: center;
  padding: 0 10px 5px;
}
.cmp-more-link a {
  font-size: 8.5px;
  font-weight: 800;
  color: var(--col-accent);
  text-decoration: none;
}

/* Placeholder card shown instead of the composer on the Pics tab, since
   this composer only posts text/voice and has no image field. */
.pics-composer-hint {
  display: none;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 12px;
  background: var(--col-bg);
  border: 1px solid var(--col-border);
  border-radius: var(--home-radius);
  margin-bottom: var(--home-gap);
  font-size: 11.5px;
  font-weight: 700;
  color: var(--col-muted);
}
.pics-composer-hint a {
  color: var(--col-accent);
  text-decoration: none;
  font-weight: 800;
}
.pics-composer-hint.show { display: flex; }

/* ══════════════════════════════════════════
   FEED TOOLBAR
   All-horizontal pill buttons — icon and label sit side-by-side in a
   row, never stacked. The bar itself scrolls horizontally on narrow
   screens instead of wrapping, so buttons never squeeze into vertical
   squares again.
   ══════════════════════════════════════════ */
.feed-toolbar {
  display: flex;
  flex-wrap: nowrap;
  align-items: center;
  gap: 6px;
  padding: 8px;
  background: var(--col-bg);
  border: 1px solid var(--col-border);
  border-radius: var(--home-radius);
  margin-bottom: var(--home-gap);
  box-shadow: 0 1px 3px rgba(0,0,0,.05);
  width: 100%%;
  box-sizing: border-box;
  overflow-x: auto;
  overflow-y: hidden;
  -webkit-overflow-scrolling: touch;
  scrollbar-width: none;
}
.feed-toolbar::-webkit-scrollbar { display: none; }

.sq-btn {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  flex: 0 0 64px;
  width: 64px;
  min-height: 40px;
  height: auto;
  border-radius: 14px;
  border: 1.5px solid var(--col-border);
  background: var(--col-surface);
  color: var(--col-muted);
  font-size: 9px;
  font-weight: 800;
  text-transform: uppercase;
  letter-spacing: .01em;
  white-space: normal;
  cursor: pointer;
  text-decoration: none;
  -webkit-tap-highlight-color: transparent;
  transition: all .15s;
  padding: 5px 4px;
  box-sizing: border-box;
}
.sq-btn span:first-child { font-size: 14px; line-height: 1; flex-shrink: 0; }
.sq-btn span:last-child {
  white-space: normal;
  overflow: visible;
  text-overflow: clip;
  text-align: center;
  line-height: 1.15;
  word-break: keep-all;
}
.sq-btn:hover { border-color: var(--col-accent); transform: translateY(-1px); }
.sq-btn:active { transform: scale(0.96); }
.sq-btn.f-active {
  background: var(--col-accent);
  border-color: var(--col-accent);
  color: #fff;
  box-shadow: 0 4px 12px rgba(37,99,235,.28);
  transform: translateY(0);
}
.sq-btn.sq-leaderboard { color: #7c3aed; }
.sq-btn.sq-leaderboard:hover { border-color: #a855f7; }
.sq-btn.sq-forme { color: #0891b2; }
.sq-btn.sq-forme:hover { border-color: #06b6d4; }
.sq-btn.sq-dpcompete { color: #ff0040; }
.sq-btn.sq-dpcompete:hover { border-color: #ff6b00; }

/* ══════════════════════════════════════════
   LATEST / TRENDING SEGMENTED CONTROL
   Pill-style row instead of two squares — reads correctly on narrow
   mobile widths where the old square buttons compressed awkwardly.
   ══════════════════════════════════════════ */
.lt-tabs {
  display: flex;
  gap: 4px;
  padding: 4px;
  background: var(--col-surface);
  border: 1px solid var(--col-border);
  border-radius: 999px;
  margin-bottom: var(--home-gap);
}
.lt-tab {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  height: 36px;
  border: none;
  border-radius: 999px;
  background: transparent;
  color: var(--col-muted);
  font-size: 12px;
  font-weight: 800;
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  touch-action: manipulation;
  transition: background .15s, color .15s;
}
.lt-tab span:first-child { font-size: 13px; line-height: 1; }
.lt-tab.lt-active {
  background: var(--col-bg);
  color: var(--col-accent);
  box-shadow: 0 2px 6px rgba(0,0,0,.08);
}

/* ══════════════════════════════════════════
   PAGINATION
   ══════════════════════════════════════════ */
.pagination {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  align-items: center;
  gap: 5px;
  margin: 24px 0 14px;
  padding: 0 12px;
}
.pg-btn {
  padding: 6px 12px;
  background: var(--col-surface);
  border-radius: 10px;
  font-size: 10px;
  font-weight: 800;
  text-transform: uppercase;
  color: var(--col-text);
  text-decoration: none;
  border: 1px solid var(--col-border);
}
.pg-num {
  width: 34px;
  height: 34px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 10px;
  font-size: 11px;
  font-weight: 800;
  background: var(--col-surface);
  color: var(--col-text);
  text-decoration: none;
  border: 1px solid var(--col-border);
}
.pg-active {
  background: var(--col-accent) !important;
  color: white !important;
  border-color: var(--col-accent) !important;
  box-shadow: 0 3px 10px rgba(37,99,235,.3);
}
.pg-dots { color: var(--col-muted); font-size: 12px; }

/* ══════════════════════════════════════════
   NEW POSTS PILL
   ══════════════════════════════════════════ */
#new-posts-pill {
  display: none;
  position: sticky;
  top: 8px;
  z-index: 40;
  margin: 0 auto 10px;
  width: fit-content;
}
#new-posts-pill.show {
  display: flex;
  animation: pillBounceIn .45s cubic-bezier(.34,1.56,.64,1);
}
#new-posts-pill button {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 16px;
  background: linear-gradient(135deg,#2563eb,#7c3aed);
  color: #fff;
  border: none;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 800;
  box-shadow: 0 4px 16px rgba(37,99,235,.35);
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
}
#new-posts-pill button:active { transform: scale(.93); }
#new-posts-pill .np-dot {
  width: 6px;
  height: 6px;
  background: #4ade80;
  border-radius: 50%%;
  animation: pulse-dot 1.4s ease-in-out infinite;
}
@keyframes pillBounceIn {
  0%%   { opacity: 0; transform: translateY(-14px) scale(.85); }
  60%%  { opacity: 1; transform: translateY(2px) scale(1.03); }
  100%% { transform: translateY(0) scale(1); }
}
@keyframes wgToastIn {
  0%%   { opacity: 0; transform: translateY(-16px) scale(.9); }
  60%%  { opacity: 1; transform: translateY(2px) scale(1.02); }
  100%% { transform: translateY(0) scale(1); }
}
@keyframes wgToastOut { to { opacity: 0; transform: translateY(-10px) scale(.95); } }
.wg-toast{
  position:sticky;top:8px;z-index:45;margin:0 auto 10px;max-width:420px;
  display:flex;align-items:center;gap:8px;padding:10px 16px;border-radius:999px;
  background:linear-gradient(135deg,#4c1d95,#7c3aed);color:#fff;font-size:12px;font-weight:800;
  box-shadow:0 6px 20px rgba(124,58,237,.4);animation:wgToastIn .4s cubic-bezier(.34,1.56,.64,1);
}
.wg-toast.wg-toast-out{animation:wgToastOut .3s ease forwards;}

/* ══════════════════════════════════════════
   POST CARD / ACTION BUTTON STYLES
   ══════════════════════════════════════════ */
.action-btn{display:inline-flex!important;flex-direction:column!important;flex-shrink:0!important;flex-grow:0!important;align-items:center;justify-content:center;gap:1px;width:auto;min-height:34px!important;min-width:0;border:1px solid;border-top-color:currentcolor;border-right-color:currentcolor;border-bottom-color:currentcolor;border-left-color:currentcolor;border-radius:7px;background:#fff;font-size:7.5px;font-weight:700;cursor:pointer;transition:.15s;padding:4px 6px!important;text-decoration:none;line-height:1.1;white-space:nowrap;box-sizing:border-box!important;}
.action-btn{transition:all .1s ease!important;cursor:pointer!important;}
.action-btn span:first-child{font-size:10px;line-height:1;}
.btn-taliyaan{color:#059669;}
.btn-chupair{color:#e11d48;}
.btn-reply{color:#2563eb;}
.btn-share{color:#475569;}
.btn-block{color:#d97706;}
.btn-delete{color:#dc2626;}
.follow-btn,.unfollow-btn{display:inline-flex!important;flex-direction:column!important;flex-shrink:0!important;flex-grow:0!important;align-items:center;justify-content:center;gap:1px;min-height:34px!important;border:1px solid;border-radius:7px;font-size:7.5px;font-weight:700;cursor:pointer;transition:.15s;padding:4px 6px!important;text-decoration:none;line-height:1.1;white-space:nowrap;box-sizing:border-box!important;background:#fff;}
.follow-btn{border-color:#c4b5fd;color:#7c3aed;}
.unfollow-btn{border-color:#c4b5fd;color:#7c3aed;}
.follow-btn span:first-child,.unfollow-btn span:first-child{font-size:10px;line-height:1;}
.follow-btn:active,.unfollow-btn:active{transform:scale(.92);}
.action-btn:active{transform:scale(.92);}
.dark .action-btn,.dark .follow-btn,.dark .unfollow-btn{background:#1f2937;border-color:currentcolor;}
.dark .btn-taliyaan{color:#34d399;}
.dark .btn-chupair{color:#fb7185;}
.dark .btn-reply{color:#60a5fa;}
.dark .btn-share{color:#94a3b8;}
.dark .btn-block{color:#fbbf24;}
.dark .btn-delete{color:#f87171;}
.dark .follow-btn,.dark .unfollow-btn{border-color:#a78bfa;color:#c4b5fd;}
.post-card{box-shadow:0 1px 2px rgba(0,0,0,0.04),0 4px 10px rgba(0,0,0,0.05);transition:box-shadow .2s ease,transform .15s ease;}
.post-card:active{transform:scale(0.997);}
.no-scrollbar::-webkit-scrollbar{display:none;}
.post-card:hover{box-shadow:0 4px 12px rgba(0,0,0,0.08),0 2px 4px rgba(0,0,0,0.04);}

/* New post insertion — quick, deliberate fade instead of a hard pop-in */
#feed > *:first-child { animation: postFadeIn .22s ease; }
@keyframes postFadeIn {
  from { opacity: 0; transform: translateY(-6px); }
  to   { opacity: 1; transform: translateY(0); }
}

/* ── MISC ── */
.feed-err { color: #ef4444; padding: 16px; font-size: 13px; text-align: center; }
.hidden { display: none !important; }
.no-sb { -ms-overflow-style:none; scrollbar-width:none; }
.no-sb::-webkit-scrollbar { display:none; }
button, a.sq-btn, .lt-tab, .os-card { touch-action: manipulation; }

.mention-dropdown { position:absolute; z-index:99999; display:none; background:var(--col-bg); border:1.5px solid var(--col-border); border-radius:10px; max-height:220px; overflow-y:auto; box-shadow:0 8px 24px rgba(0,0,0,.18); }
.mention-dropdown.open { display:block; }
.mention-suggestion { display:flex; align-items:center; gap:8px; padding:8px 10px; cursor:pointer; }
.mention-suggestion:hover { background:var(--col-surface); }
.mention-avatar { width:24px;height:24px;border-radius:50%%;object-fit:cover;flex-shrink:0; }
.mention-uname { font-weight:800; font-size:12px; color:var(--col-accent); }
.mention-display { font-size:11px; color:var(--col-muted); }
.mention-empty { padding:10px; font-size:11.5px; color:var(--col-muted); text-align:center; }
</style>

<script>
(function(){
  'use strict';

  var PAGE         = %d;
  var FILTER       = %q;
  var CURRENT_USER = %q;
  var LATEST_ID    = %d;
  var isTyping   = false;
  var sliderBusy = false;
  var feedBusy   = false;
  var inlineTabActive   = false;
  var currentFilterType = FILTER;
  var newPostsPollTimer = null;
  var sliderTimer, feedTimer;
  var evtSource = null;

  /* ── @mention autocomplete for the Quickthought composer ── */
  var mentionActiveTa = null;
  var mentionDebounce  = null;

  function getMentionToken(ta){
    var pos = ta.selectionStart;
    var val = ta.value.slice(0, pos);
    var m = val.match(/(?:^|[\s(])@([A-Za-z0-9_]{1,32})$/);
    return m ? m[1] : null;
  }

  function attachMentionWatcher(ta){
    if (!ta || ta.__mentionBound) return;
    ta.__mentionBound = true;
    ta.addEventListener('input', function(){
      var dd = document.getElementById('mention-dropdown');
      if (!dd) return;
      var token = getMentionToken(ta);
      if (token === null) { dd.classList.remove('open'); return; }
      mentionActiveTa = ta;
      clearTimeout(mentionDebounce);
      mentionDebounce = setTimeout(function(){
        htmx.ajax('GET', '/mention/search?q=' + encodeURIComponent(token), {target:'#mention-dropdown', swap:'innerHTML'})
          .then(function(){
            var rect = ta.getBoundingClientRect();
            dd.style.left  = (rect.left + window.scrollX) + 'px';
            dd.style.top   = (rect.bottom + window.scrollY + 4) + 'px';
            dd.style.width = rect.width + 'px';
            dd.classList.add('open');
          });
      }, 200);
    });
    ta.addEventListener('blur', function(){
      setTimeout(function(){ var dd = document.getElementById('mention-dropdown'); if (dd) dd.classList.remove('open'); }, 150);
    });
  }

  window.shareApplyMention = function(username){
    var ta = mentionActiveTa;
    if (!ta) return;
    var pos = ta.selectionStart;
    var val = ta.value;
    var before = val.slice(0, pos);
    var after  = val.slice(pos);
    var replaced = before.replace(/(?:^|[\s(])@([A-Za-z0-9_]{1,32})$/, function(full){
      var prefix = full[0] === '@' ? '' : full[0];
      return prefix + '@' + username + ' ';
    });
    ta.value = replaced + after;
    var newPos = replaced.length;
    ta.focus();
    ta.setSelectionRange(newPos, newPos);
    var dd = document.getElementById('mention-dropdown');
    if (dd) dd.classList.remove('open');
  };

  function lockFeed(){ feedBusy=true; clearTimeout(feedTimer); feedTimer=setTimeout(function(){ feedBusy=false; },3000); }
  function lockSlider(){ sliderBusy=true; clearTimeout(sliderTimer); sliderTimer=setTimeout(function(){ sliderBusy=false; },3000); }

  window.currentFilter = FILTER;

  function hideComposer(){
    var cc = document.getElementById('composer-card');
    if(cc) cc.style.display = 'none';
    var hint = document.getElementById('pics-composer-hint');
    if(hint) hint.classList.remove('show');
  }
   function showComposer(){
    var cc = document.getElementById('composer-card');
    var hint = document.getElementById('pics-composer-hint');
    if(currentFilterType === 'pics' || currentFilterType === 'videos'){
      // This composer only handles text/voice — no image field — so on
      // the Pics tab we point people to the real photo-posting flow
      // instead of showing a text box that can't do what they came to do.
      if(cc) cc.style.display = 'none';
      if(hint) hint.classList.add('show');
    } else {
      if(cc) cc.style.display = '';
      if(hint) hint.classList.remove('show');
    }
  }

  function hideLTSubbar(){
    var bar = document.getElementById('lt-subbar');
    if(bar){ bar.classList.add('hidden'); bar.style.display='none'; }
  }
  function showLTSubbar(){
    var bar = document.getElementById('lt-subbar');
    if(bar){ bar.classList.remove('hidden'); bar.style.display='flex'; }
  }
  function markLTMode(mode){
    var lb = document.getElementById('lt-latest-btn');
    var tb = document.getElementById('lt-trending-btn');
    if(lb) lb.classList.toggle('lt-active', mode==='latest');
    if(tb) tb.classList.toggle('lt-active', mode==='trending');
  }
  function hideNewPostsPill(){
    var pill = document.getElementById('new-posts-pill');
    if(pill) pill.classList.remove('show');
  }

  window.selectFilterType = function(type, btn){
    currentFilterType = type;
    document.querySelectorAll('.filter-btn[data-filter]').forEach(function(b){ b.classList.remove('f-active'); });
    if(btn) btn.classList.add('f-active');
    var lbBtn1 = document.getElementById('sq-leaderboard-btn');
    if(lbBtn1) lbBtn1.classList.remove('f-active');
    var formeBtn1 = document.getElementById('sq-forme-btn');
    if(formeBtn1) formeBtn1.classList.remove('f-active');
    var dpBtn1 = document.getElementById('sq-dpcompete-btn');
    if(dpBtn1) dpBtn1.classList.remove('f-active');
    // "All" is the unfiltered firehose — Latest/Trending only makes sense
    // once narrowed to Text or Pics.
    if(type === 'all'){
      hideLTSubbar();
      markLTMode('latest');
    } else {
      showLTSubbar();
    }
    hideNewPostsPill();
    window.loadLatestForType();
    connectFeedStream();
  };

  window.loadLatestForType = function(){
    markLTMode('latest');
    showComposer();
    inlineTabActive = false;
    hideNewPostsPill();
    window.currentFilter = currentFilterType;
    // #feed intentionally never carries an hx-get attribute (see the
    // #feed markup below) — a div's default htmx trigger is "click", so
    // leaving hx-get sitting on it meant every tap anywhere inside the
    // feed (reactions, follow, delete, Load More) silently re-fired a
    // full reload back to page 1 a moment later. Every load is instead
    // driven explicitly through htmx.ajax(), which needs no attribute
    // on the target element at all.
    var url = '/feed?filter=' + currentFilterType;
    htmx.ajax('GET', url, {target:'#feed', swap:'innerHTML'}).then(function(){
      refreshLatestId();
    });
  };

  window.loadTrendingForType = function(){
    markLTMode('trending');
    hideComposer();
    inlineTabActive = true;
    hideNewPostsPill();

    var feed = document.getElementById('feed');
    feed.innerHTML = '<div style="text-align:center;padding:40px;color:var(--col-muted);font-size:12px;">Loading trending…</div>';

    fetch('/trending?type=' + currentFilterType)
      .then(function(res){ return res.text(); })
      .then(function(html){
        var doc = new DOMParser().parseFromString(html, 'text/html');
        var section = doc.getElementById('trending-posts');
        feed.innerHTML = section ? section.innerHTML :
          '<div style="text-align:center;padding:40px;color:var(--col-muted);font-size:12px;">No trending posts found.</div>';
        htmx.process(feed);
      })
      .catch(function(err){
        console.error('Inline trending load failed:', err);
        feed.innerHTML = '<div style="text-align:center;padding:40px;color:#ef4444;font-size:12px;">Could not load trending posts.</div>';
      });
  };

  window.loadForMeInline = function(btn){
    hideLTSubbar();
    hideNewPostsPill();
    document.querySelectorAll('.filter-btn[data-filter]').forEach(function(b){ b.classList.remove('f-active'); });
    var lbBtn3 = document.getElementById('sq-leaderboard-btn');
    if(lbBtn3) lbBtn3.classList.remove('f-active');
    var dpBtn2 = document.getElementById('sq-dpcompete-btn');
    if(dpBtn2) dpBtn2.classList.remove('f-active');
    if(btn) btn.classList.add('f-active');
    hideComposer();
    document.getElementById('composer-card').style.display = 'none';
    var hint2 = document.getElementById('pics-composer-hint');
    if(hint2) hint2.classList.remove('show');
    inlineTabActive = true;

    var feed = document.getElementById('feed');
    feed.innerHTML = '<div style="text-align:center;padding:40px;color:var(--col-muted);font-size:12px;">Loading your feed…</div>';

    fetch('/forme-inline')
      .then(function(res){ return res.text(); })
      .then(function(html){ feed.innerHTML = html; htmx.process(feed); })
      .catch(function(err){
        console.error('Inline For Me load failed:', err);
        feed.innerHTML = '<div style="text-align:center;padding:40px;color:#ef4444;font-size:12px;">Could not load your feed.</div>';
      });
  };

  

  window.loadDPCompeteInline = function(btn){
    hideLTSubbar();
    hideNewPostsPill();
    document.querySelectorAll('.filter-btn[data-filter]').forEach(function(b){ b.classList.remove('f-active'); });
    var lbBtn5 = document.getElementById('sq-leaderboard-btn');
    if(lbBtn5) lbBtn5.classList.remove('f-active');
    var formeBtn3 = document.getElementById('sq-forme-btn');
    if(formeBtn3) formeBtn3.classList.remove('f-active');
    if(btn) btn.classList.add('f-active');
    document.getElementById('composer-card').style.display = 'none';
    var hint4 = document.getElementById('pics-composer-hint');
    if(hint4) hint4.classList.remove('show');
    inlineTabActive = true;

    var feed = document.getElementById('feed');
    feed.innerHTML = '<div style="text-align:center;padding:40px;color:var(--col-muted);font-size:12px;">Loading DP competitions…</div>';

    fetch('/dpcompete/section')
      .then(function(res){ return res.text(); })
      .then(function(html){ feed.innerHTML = html; htmx.process(feed); })
      .catch(function(err){
        console.error('Inline DP competitions load failed:', err);
        feed.innerHTML = '<div style="text-align:center;padding:40px;color:#ef4444;font-size:12px;">Could not load DP competitions.</div>';
      });
  };

  /* ══════════════════════════════════════════════════════════════════════
     REAL-TIME FEED — Server-Sent Events
     Holds one persistent connection to /feed/stream. The instant any user
     creates a post, the server pushes its id here — no polling delay.
     If the viewer is scrolled near the top of the normal Latest view, the
     post fades in immediately; otherwise it quietly bumps the pill so we
     never yank content out from under someone reading further down.
     ══════════════════════════════════════════════════════════════════════ */
  window.connectFeedStream = function(){
    if(typeof EventSource === 'undefined') return; // very old browser — polling fallback below still works
    if(evtSource) evtSource.close();
    evtSource = new EventSource('/feed/stream?filter=' + encodeURIComponent(currentFilterType));
   evtSource.onmessage = function(e){
      if(!e.data || e.data.charAt(0) === ':') return; // heartbeat ping
      if(e.data.indexOf('WIN|') === 0){ handleWordGameWin(e.data); return; }
      var parts = e.data.split('|');
      handleIncomingPost(parts[0], parts[1] || '');
    };

  function handleWordGameWin(data){
    var parts = data.split('|');
    var postId = parts[1], winner = parts[2] || '', prize = parts[3] || '';
    var feed = document.getElementById('feed');
    if(feed && feed.parentNode){
      var toast = document.createElement('div');
      toast.className = 'wg-toast';
      var icon = document.createElement('span'); icon.textContent = '🎉';
      var msg = document.createElement('span');
      msg.textContent = winner + ' guessed the word correctly and won ' + prize + ' points!';
      toast.appendChild(icon); toast.appendChild(msg);
      feed.parentNode.insertBefore(toast, feed);
      setTimeout(function(){
        toast.classList.add('wg-toast-out');
        setTimeout(function(){ toast.remove(); }, 320);
      }, 4500);
    }
    // Word Game cards render with id="wordgame-N", trivia cards render
    // with id="trivia-N" (see renderWordGamePostCard / renderTriviaPostCard).
    // This lookup only checked the word-game prefix, so every trivia win
    // broadcast found no matching element and silently skipped the
    // refresh — the toast fired, but the question card itself never
    // flipped to its "solved" state, making a correct answer look like
    // it was never registered.
    var existing = document.getElementById('wordgame-' + postId) || document.getElementById('trivia-' + postId);
    if(existing){
      fetch('/post/fragment/' + postId).then(function(r){ return r.text(); }).then(function(html){
        if(!html) return;
        existing.outerHTML = html;
        htmx.process(document.getElementById('feed'));
      }).catch(function(){});
    }
  }
    evtSource.onerror = function(){
      // Browsers auto-retry EventSource on drop; nothing to do here.
      // The polling safety-net below still covers us if it stays down.
    };
  };
  var connectFeedStream = window.connectFeedStream;

  function handleIncomingPost(postId, postAuthor){
    // This tab already showed its own post instantly via the composer's
    // own htmx swap. Inserting it again here would create a second DOM
    // node sharing the same id="post-<id>" — and that duplicate id is
    // exactly why Taliyaan/Chupair needed a second tap: hx-target="#post-N"
    // resolves to whichever copy the browser finds first, which may not
    // be the one you're actually looking at.
    if(postAuthor && CURRENT_USER && postAuthor === CURRENT_USER){
      var feedSelf = document.getElementById('feed');
      if(feedSelf){
        var curSelf = parseInt(feedSelf.dataset.latestId, 10) || 0;
        var incomingSelf = parseInt(postId, 10) || 0;
        feedSelf.dataset.latestId = Math.max(curSelf, incomingSelf);
      }
      return;
    }

    if(inlineTabActive) return; // For Me / Trending / Leaderboard / DP Battle open — don't disturb
    var tb = document.getElementById('lt-trending-btn');
    if(tb && tb.classList.contains('lt-active')) return; // don't nudge while on a Trending sub-tab

    var feed = document.getElementById('feed');
    if(!feed) return;

    fetch('/post/fragment/' + postId)
      .then(function(r){ return r.text(); })
      .then(function(html){
        if(!html) return; // deleted already, or blocked for this viewer

        // If the person has scrolled down to read something, don't let a
        // new post at the top shove their content downward — insert it,
        // then compensate scroll by exactly how much taller the page got,
        // so what they're reading stays pinned in place.
        var atTop = (window.scrollY || window.pageYOffset) < 4;
        var heightBefore = document.body.scrollHeight;

        feed.insertAdjacentHTML('afterbegin', html);
        htmx.process(feed);

        if(!atTop){
          var heightAfter = document.body.scrollHeight;
          window.scrollBy(0, heightAfter - heightBefore);
        }

        var cur = parseInt(feed.dataset.latestId, 10) || 0;
        var incoming = parseInt(postId, 10) || 0;
        feed.dataset.latestId = Math.max(cur, incoming);
      })
      .catch(function(){});
  }

  function refreshLatestId(){
    var feed = document.getElementById('feed');
    if(!feed) return;
    fetch('/feed/new-count?filter=' + encodeURIComponent(currentFilterType) + '&since=0')
      .then(function(r){ return r.json(); })
      .then(function(data){ feed.dataset.latestId = data.latest; })
      .catch(function(){});
  }

  // Safety-net poll — SSE handles the real-time path now, so this only
  // needs to catch the rare case of a dropped stream on a flaky mobile
  // network. Much slower interval, paused while the tab is backgrounded.
  function checkForNewPosts(){
    if(isTyping || feedBusy || inlineTabActive) return;
    var tb = document.getElementById('lt-trending-btn');
    if(tb && tb.classList.contains('lt-active')) return;
    if(document.hidden) return;
    var feed = document.getElementById('feed');
    if(!feed) return;
    var since = parseInt(feed.dataset.latestId, 10) || 0;

    fetch('/feed/new-count?filter=' + encodeURIComponent(currentFilterType) + '&since=' + since)
      .then(function(r){ return r.json(); })
      .then(function(data){
        if(data.count > 0){
          var pill = document.getElementById('new-posts-pill');
          if(!pill) return;
          pill.dataset.latestId = data.latest;
          var cEl = pill.querySelector('.np-count');
          if(cEl) cEl.textContent = data.count;
          pill.classList.add('show');
        }
      })
      .catch(function(){});
  }

  window.loadNewPosts = function(){
    hideNewPostsPill();
    var url = '/feed?filter=' + currentFilterType;
    htmx.ajax('GET', url, {target:'#feed', swap:'innerHTML'}).then(function(){
      window.scrollTo({top:0, behavior:'smooth'});
      refreshLatestId();
    });
  };

  function startNewPostsPolling(){
    clearInterval(newPostsPollTimer);
    if(PAGE !== 1) return;
    newPostsPollTimer = setInterval(checkForNewPosts, 25000);
  }

  /* ── Voice recorder ── */
  var mediaRecorder = null;
  var audioChunks   = [];
  var recordedBlob  = null;
  var timerInterval = null;
  var recSeconds    = 0;
  var MAX_REC_SECS  = 120;

  function fmtTime(s){ var m=Math.floor(s/60),sec=s%%60; return (m<10?'0':'')+m+':'+(sec<10?'0':'')+sec; }

  function startTimer(){
    recSeconds = 0;
    var el = document.getElementById('mic-timer');
    if(el){ el.textContent='0:00'; el.classList.add('visible'); }
    timerInterval = setInterval(function(){
      recSeconds++;
      var t = document.getElementById('mic-timer');
      if(t) t.textContent = fmtTime(recSeconds);
      if(recSeconds >= MAX_REC_SECS) stopRecording();
    }, 1000);
  }

  function stopTimer(){
    clearInterval(timerInterval);
    var el = document.getElementById('mic-timer');
    if(el) el.classList.remove('visible');
    recSeconds = 0;
  }

  function getMicBtn(){ return document.getElementById('mic-btn'); }

  function startRecording(){
    if(!navigator.mediaDevices||!navigator.mediaDevices.getUserMedia){
      alert('Your browser does not support audio recording.');
      return;
    }
    navigator.mediaDevices.getUserMedia({audio:true})
      .then(function(stream){
        audioChunks = []; recordedBlob = null;
        var mime = 'audio/webm;codecs=opus';
        if(!MediaRecorder.isTypeSupported(mime)) mime = 'audio/ogg;codecs=opus';
        if(!MediaRecorder.isTypeSupported(mime)) mime = '';
        mediaRecorder = new MediaRecorder(stream, mime ? {mimeType:mime} : {});
        mediaRecorder.ondataavailable = function(e){ if(e.data&&e.data.size>0) audioChunks.push(e.data); };
        mediaRecorder.onstop = function(){
          stream.getTracks().forEach(function(t){ t.stop(); });
          var finalMime = mediaRecorder.mimeType || 'audio/webm';
          recordedBlob  = new Blob(audioChunks, {type:finalMime});
          var ae = document.getElementById('voice-audio-preview');
          if(ae) ae.src = URL.createObjectURL(recordedBlob);
          var vp = document.getElementById('voice-preview');
          if(vp) vp.classList.add('visible');
          var ext  = finalMime.includes('ogg') ? '.ogg' : '.webm';
          var file = new File([recordedBlob], 'voice'+ext, {type:finalMime});
          try{
            var dt = new DataTransfer();
            dt.items.add(file);
            var vi = document.getElementById('voice-file-input');
            if(vi) vi.files = dt.files;
          } catch(e){ console.warn('DataTransfer not supported'); }
        };
        mediaRecorder.start(250);
        startTimer();
        var btn = getMicBtn();
        if(btn) btn.classList.add('recording');
      })
      .catch(function(err){
        console.error('Mic error:',err);
        alert('Could not access microphone. Please allow mic permission and try again.');
      });
  }

  function stopRecording(){
    if(mediaRecorder&&mediaRecorder.state!=='inactive') mediaRecorder.stop();
    stopTimer();
    var btn = getMicBtn();
    if(btn) btn.classList.remove('recording');
  }

  window.toggleMic = function(){
    if(!mediaRecorder||mediaRecorder.state==='inactive') startRecording();
    else stopRecording();
  };

  window.cancelVoice = function(){
    recordedBlob = null; audioChunks = [];
    var vi = document.getElementById('voice-file-input');
    if(vi) vi.value = '';
    var vp = document.getElementById('voice-preview');
    if(vp) vp.classList.remove('visible');
    var ae = document.getElementById('voice-audio-preview');
    if(ae) ae.src = '';
    if(mediaRecorder&&mediaRecorder.state!=='inactive') stopRecording();
    var btn = getMicBtn();
    if(btn) btn.classList.remove('recording');
  };

  window.handlePostSubmit = function(evt){
    var vi      = document.getElementById('voice-file-input');
    var hasFile = vi && vi.files && vi.files.length > 0;
    var hasBlob = !hasFile && recordedBlob;
    if(!hasFile && !hasBlob) return true;
    evt.preventDefault();
    var fd = new FormData();
    if(hasFile){ fd.append('voice', vi.files[0]); }
    else { var ext = recordedBlob.type.includes('ogg')?'.ogg':'.webm'; fd.append('voice', recordedBlob, 'voice'+ext); }
    fetch('/post/voice',{method:'POST',body:fd})
      .then(function(res){ if(!res.ok) return res.text().then(function(t){throw new Error(t);}); return res.json(); })
      .then(function(data){
        var hui = document.getElementById('voice-url-hidden');
        if(hui) hui.value = data.url;
        window.cancelVoice();
        htmx.trigger(document.getElementById('post-form'),'submit');
      })
      .catch(function(err){ console.error('Voice upload failed:',err); alert('Voice upload failed: '+err.message); });
    return false;
  };

  window._onPostSuccess = function(form){
    if(!form) form = document.getElementById('post-form');
    if(!form) return;
    form.reset();
    var ta = form.querySelector('textarea[name="content"]');
    if(ta){ ta.value = ''; ta.style.height = ''; }
    var err = document.getElementById('post-error');
    if(err) err.innerHTML = '';
    window.cancelVoice();
    refreshLatestId();
  };

  document.body.addEventListener('htmx:afterRequest', function(evt){
    var path = evt.detail && evt.detail.pathInfo ? evt.detail.pathInfo.requestPath : '';
    if(path !== '/post') return;
    var form = document.getElementById('post-form');
    if(evt.detail.successful){
      window._onPostSuccess(form);
      return;
    }
    var box = document.getElementById('post-error');
    var xhr = evt.detail.xhr;
    if(box && xhr && xhr.responseText){
      box.innerHTML = xhr.responseText;
    }
  });

  window._ytPlaying = false;
  window.addEventListener('message', function(evt){
    try {
      var d = JSON.parse(evt.data);
      if(d.event === 'onStateChange'){
        window._ytPlaying = (d.info === 1 || d.info === 3);
      }
    } catch(e){}
  });

  document.addEventListener('htmx:beforeRequest',function(evt){
    var target = evt.detail && evt.detail.target;
    if(!target) return;
    if(target.id==='online-slider' && sliderBusy){ evt.preventDefault(); }
    if(target.id==='feed'){ savedScrollY = window.scrollY||window.pageYOffset; }
  });

  document.addEventListener('htmx:beforeSwap',function(evt){
    if(evt.detail&&evt.detail.target&&evt.detail.target.id==='online-slider'){
      var track = document.getElementById('online-slider-track');
      if(track) evt.detail.target._savedScroll = track.scrollLeft;
    }
  });

  document.addEventListener('htmx:afterSettle',function(evt){
    if(evt.detail&&evt.detail.target&&evt.detail.target.id==='online-slider'){
      var saved = evt.detail.target._savedScroll;
      var track = document.getElementById('online-slider-track');
      if(track && saved != null) track.scrollLeft = saved;
      updateOnlinePill();
      var si = document.getElementById('os-search');
      if(si && si.value) osSearch(si.value);
    }
    if(evt.detail&&evt.detail.target&&evt.detail.target.id==='feed'){
      window.scrollTo({top:savedScrollY, behavior:'instant'});
    }
  });

  var savedScrollY = 0;

  window.toggleOnlineList = function(){
    var slider = document.getElementById('online-slider');
    var btn = document.getElementById('os-toggle-btn');
    if(!slider || !btn) return;
    var isExpanded = slider.classList.toggle('expanded');
    btn.classList.toggle('expanded', isExpanded);
    btn.title = isExpanded ? 'Collapse online users' : 'Expand online users';
  };

  function updateOnlinePill(){
    var pill = document.getElementById('os-count-label');
    if(!pill) return;
    var track = document.getElementById('online-slider-track');
    var emptyEl = document.querySelector('.os-empty[data-online-count]');
    var count = 0;
    if(track && track.dataset.onlineCount !== undefined){
      count = parseInt(track.dataset.onlineCount, 10) || 0;
    } else if(emptyEl){
      count = 0;
    }
    pill.textContent = count + ' Online';
  }

  function initHomePage(){
    if(window.__dostanaPollTimer) clearInterval(window.__dostanaPollTimer);
    if(window.__dostanaEvtSource) window.__dostanaEvtSource.close();
    if(window.__dostanaVisibilityHandler) document.removeEventListener('visibilitychange', window.__dostanaVisibilityHandler);

    var ta = document.querySelector('.composer-textarea');
    if(ta){
      ta.addEventListener('focus', function(){ isTyping=true; });
      ta.addEventListener('blur',  function(){ isTyping=false; });
      attachMentionWatcher(ta);
    }
    window.addEventListener('scroll',    lockFeed,   {passive:true});
    window.addEventListener('touchmove', lockFeed,   {passive:true});
    var slider = document.getElementById('online-slider');
    if(slider){
      slider.addEventListener('touchstart', lockSlider, {passive:true});
      slider.addEventListener('scroll',     lockSlider, {passive:true});
    }
   var activeBtn = document.querySelector('.filter-btn[data-filter="'+FILTER+'"]');
    if(activeBtn){
      document.querySelectorAll('.filter-btn').forEach(function(b){ b.classList.remove('f-active'); });
      activeBtn.classList.add('f-active');
      if(FILTER === 'pics' || FILTER === 'videos') hideComposer();
      if(FILTER !== 'all'){ showLTSubbar(); } else { hideLTSubbar(); }
      markLTMode('latest');
    }
    updateOnlinePill();
    startNewPostsPolling();
    connectFeedStream();
    window.__dostanaPollTimer = newPostsPollTimer;
    window.__dostanaEvtSource = evtSource;

    window.__dostanaVisibilityHandler = function(){
      if(!document.hidden){
        checkForNewPosts();
        connectFeedStream();
        window.__dostanaEvtSource = evtSource;
      }
    };
    document.addEventListener('visibilitychange', window.__dostanaVisibilityHandler);

    window.addEventListener('beforeunload', function(){
      if(evtSource) evtSource.close();
    });
  }

  if(document.readyState === 'loading'){
    document.addEventListener('DOMContentLoaded', initHomePage);
  } else {
    initHomePage();
  }

})();
</script>

<div id="mention-dropdown" class="mention-dropdown"></div>

__NOTICE_HTML__

<!-- ══════════════════════════════════════════════
     ONLINE USERS CARD
     ══════════════════════════════════════════════ -->
<div class="home-card">
  <div class="os-header">
    <span class="os-pill" id="os-pill">
      <span class="os-pill-dot"></span>
      <span id="os-count-label">%d Online</span>
    </span>
    <div class="os-search-wrap">
      <span class="os-search-icon">🔍</span>
      <input type="text"
             class="os-search-input"
             id="os-search"
             placeholder="Search online users…"
             oninput="osSearch(this.value)"
             autocomplete="off">
    </div>
    <button type="button" class="os-toggle-btn expanded" id="os-toggle-btn" onclick="toggleOnlineList()" title="Collapse online users">
      <span class="os-toggle-arrow">▾</span>
    </button>  </div>

  <div id="online-slider"
       class="no-sb expanded"
       hx-get="/online-slider"
       hx-trigger="every 8s [!document.hidden]"
       hx-swap="innerHTML">
    %s
  </div>
</div>

<!-- ══════════════════════════════════════════════
     APK DOWNLOAD NOTICE (small, non-intrusive)
     ══════════════════════════════════════════════ -->
<div style="text-align:center;font-size:10.5px;font-weight:600;color:var(--col-muted);margin:2px 0 10px;">
  📲 Dostana App coming soon to Play Store —
  <a href="/downloads/Dostana.apk" download hx-boost="false" style="color:var(--col-accent);font-weight:800;text-decoration:none;">Download now</a>
</div>

<!-- ══════════════════════════════════════════════
     FEED TOOLBAR
     ══════════════════════════════════════════════ -->
__TOOLBAR_HTML__

<!-- Latest / Trending — hidden on "All", shown for Text/Pics -->
__LT_SUBBAR_HTML__

<!-- Shown instead of the composer while on the Pics tab -->
<div class="pics-composer-hint" id="pics-composer-hint">
  <span>📸</span>
  <span>Want to post a photo or video? <a href="/share">Go to the Share page →</a></span>
</div>


<!-- ══════════════════════════════════════════════
     POST COMPOSER CARD
     ══════════════════════════════════════════════ -->
<div class="composer-card" id="composer-card">
  <div id="post-error" style="width:100%%;"></div>

  <form id="post-form"
        hx-post="/post"
        hx-target="#feed"
        hx-swap="afterbegin"
        hx-disabled-elt="find .cmp-send-btn"
        enctype="multipart/form-data"
        onsubmit="return handlePostSubmit(event)">

    <input type="hidden" name="category" value="General">
    <input type="hidden" name="source" value="quickthought">

    <div class="composer-row">
      <button type="button"
              id="mic-btn"
              class="cmp-mic"
              onclick="toggleMic()"
              title="Record voice (max 2 min)">
        🎙️
      </button>
      <span id="mic-timer">0:00</span>

      <div class="composer-input-wrap">
        <textarea name="content"
                  placeholder="Quick thought…"
                  rows="1"
                  onfocus="isTyping=true"
                  onblur="isTyping=false"
                  oninput="this.style.height='';this.style.height=Math.min(this.scrollHeight,90)+'px'"
                  class="composer-textarea"></textarea>
        <button type="submit" class="cmp-send-btn" title="Post">➤</button>
      </div>

      <input type="checkbox" id="toggle-jhappi" name="disable_jhappi" class="hidden">
      <input type="checkbox" id="toggle-chupair" name="disable_chupair" class="hidden">
    </div>

    <div id="voice-preview">
      <audio id="voice-audio-preview" controls preload="none"></audio>
      <button type="button" class="voice-cancel-btn" onclick="cancelVoice()" title="Remove">&#10005;</button>
    </div>

    <input type="file" id="voice-file-input" name="voice_ref" accept="audio/*" class="hidden">
    <input type="hidden" id="voice-url-hidden" name="voice_url" value="">

  </form>
</div><!-- /composer-card -->

<!-- ══════════════════════════════════════════════
     FEED

     Note: no hx-get / hx-trigger / hx-swap on this div. A div's default
     htmx trigger is "click", so attaching hx-get here without an
     explicit trigger meant every tap anywhere inside the feed (a
     reaction, follow, delete, the Load More button) ALSO bubbled up and
     silently re-fired a full reload of this element back to page 1 —
     which is what caused posts to reshuffle/appear "out of nowhere" and
     Load More to look broken or get instantly undone. All loading is
     now driven explicitly by JS via htmx.ajax()/fetch(), so a click can
     never accidentally trigger a reload here.
     ══════════════════════════════════════════════ -->
<div id="feed"
     class="space-y-4"
     data-latest-id="%d">
  %s
</div>

<script>
function osSearch(val){
  var cards = document.querySelectorAll('#online-slider-track .os-card');
  var v = val.trim().toLowerCase();
  var shown = 0;
  cards.forEach(function(c){
    var name = (c.getAttribute('href')||'').replace('/profile/','').toLowerCase();
    var visible = !v || name.indexOf(v) !== -1;
    c.style.display = visible ? '' : 'none';
    if(visible) shown++;
  });
  var lbl = document.getElementById('os-count-label');
  if(!lbl) return;
  if(v){
    lbl.textContent = shown + ' found';
  } else {
    var track = document.getElementById('online-slider-track');
    var count = track ? (parseInt(track.dataset.onlineCount,10)||0) : 0;
    lbl.textContent = count + ' Online';
  }
}
</script>
`,
		page, filter, user, latestID, onlineCount,
		sliderHTML,
		latestID,
		feedFragment,
	)

	homeContent = strings.Replace(homeContent, "__TOOLBAR_HTML__", toolbarHTML, 1)
	homeContent = strings.Replace(homeContent, "__LT_SUBBAR_HTML__", ltSubbarHTML, 1)
	homeContent = strings.Replace(homeContent, "__NOTICE_HTML__", noticeHTML, 1)

	return c.SendString(Layout("Home", homeContent, user))
}

func isUserOnline(username string) bool {
	if username == "" {
		return false
	}
	rctx, cancel := context.WithTimeout(platform.Ctx, 500*time.Millisecond)
	defer cancel()
	score, err := platform.RDB.ZScore(rctx, "active_sessions", username).Result()
	if err != nil {
		return false
	}
	threshold := float64(time.Now().Unix() - 300)
	return score >= threshold
}

func formatLastSeen(t time.Time) string {
	if t.IsZero() {
		return "a while ago"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	case d < 7*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	case d < 30*24*time.Hour:
		weeks := int(d.Hours() / (24 * 7))
		if weeks == 1 {
			return "1 week ago"
		}
		return fmt.Sprintf("%d weeks ago", weeks)
	case d < 365*24*time.Hour:
		months := int(d.Hours() / (24 * 30))
		if months == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	default:
		years := int(d.Hours() / (24 * 365))
		if years == 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", years)
	}
}

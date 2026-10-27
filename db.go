package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"mysocialmediawebsite/pkg/platform"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

var (
	ctx = context.Background()
	rdb *redis.Client
	db  *pgxpool.Pool
)

// goSafe runs fn in a new goroutine and recovers any panic so a bug in
// background work cannot take down the whole website process. In Go, an
// unrecovered panic in ANY goroutine kills the entire process — Fiber's
// recover middleware only covers HTTP handlers.
func goSafe(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in goroutine %s: %v", name, r)
			}
		}()
		fn()
	}()
}

func CacheGet(key string) (string, bool) {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	val, err := rdb.Get(rctx, key).Result()
	if err != nil {
		return "", false
	}
	return val, true
}

func CacheSet(key, value string, ttl time.Duration) {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if err := rdb.Set(rctx, key, value, ttl).Err(); err != nil {
		log.Printf("CacheSet warn [%s]: %v", key, err)
	}
}

func CacheDel(keys ...string) {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	rdb.Del(rctx, keys...)
}

func CacheSAdd(key, member string) {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if err := rdb.SAdd(rctx, key, member).Err(); err != nil {
		log.Printf("CacheSAdd warn [%s]: %v", key, err)
	}
}

func CacheSMembers(key string) map[string]bool {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	vals, err := rdb.SMembers(rctx, key).Result()
	if err != nil {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(vals))
	for _, v := range vals {
		out[v] = true
	}
	return out
}

func CacheZAdd(key string, score float64, member string) {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if err := rdb.ZAdd(rctx, key, redis.Z{Score: score, Member: member}).Err(); err != nil {
		log.Printf("CacheZAdd warn [%s]: %v", key, err)
	}
}

func CacheZRangeByScore(key, min, max string) []string {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	vals, err := rdb.ZRangeByScore(rctx, key, &redis.ZRangeBy{Min: min, Max: max}).Result()
	if err != nil {
		return nil
	}
	return vals
}

func CacheIncrBy(key string, delta int64) int64 {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	val, err := rdb.IncrBy(rctx, key, delta).Result()
	if err != nil {
		log.Printf("CacheIncrBy warn [%s +%d]: %v", key, delta, err)
		return 0
	}
	return val
}

func CacheGetDel(key string) (string, bool) {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	val, err := rdb.GetDel(rctx, key).Result()
	if err != nil {
		return "", false
	}
	return val, true
}

func CacheScanKeys(pattern string) []string {
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var keys []string
	iter := rdb.Scan(rctx, 0, pattern, 100).Iterator()
	for iter.Next(rctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		log.Printf("CacheScanKeys warn [%s]: %v", pattern, err)
	}
	return keys
}

const (
	ttlBanStatus     = 5 * time.Minute
	ttlBlockRel      = 5 * time.Minute
	ttlPostAuthor    = 10 * time.Minute
	ttlUserProfile   = 5 * time.Minute
	ttlNotifCount    = 30 * time.Second
	ttlPointsPending = 0
)

func BanCacheKey(username string) string { return "ban:" + username }

func BlockCacheKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return "block_rel:" + a + ":" + b
}

func PostAuthorCacheKey(postID int) string      { return fmt.Sprintf("post_author:%d", postID) }
func NotifCountCacheKey(username string) string { return "notif_count:" + username }
func PointsPendingKey(username string) string   { return "pts_pending:" + username }

func InvalidateBanCache(username string)   { CacheDel(BanCacheKey(username)) }
func InvalidateBlockCache(a, b string)     { CacheDel(BlockCacheKey(a, b)) }
func InvalidateNotifCount(username string) { CacheDel(NotifCountCacheKey(username)) }

func IsAdult(birthDate time.Time) bool {
	if birthDate.IsZero() {
		return false
	}
	now := time.Now()
	age := now.Year() - birthDate.Year()
	if now.Month() < birthDate.Month() || (now.Month() == birthDate.Month() && now.Day() < birthDate.Day()) {
		age--
	}
	return age >= 18
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func mustExec(label, sql string) {
	execCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := db.Exec(execCtx, sql); err != nil {
		log.Fatalf("FATAL [%s]: %v", label, err)
	}
}

func softExec(label, sql string) {
	execCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := db.Exec(execCtx, sql); err != nil {
		log.Printf("WARN  [%s]: %v", label, err)
	}
}

// softExecBatch runs a list of independent softExec statements concurrently
// using a bounded worker pool, cutting startup time for large DDL batches
// where statement order does not matter (e.g. ADD COLUMN IF NOT EXISTS,
// CREATE INDEX IF NOT EXISTS). Keep at a modest concurrency so it doesn't
// starve the pgx pool's MinConns/MaxConns headroom during boot.
func softExecBatch(items []struct{ label, sql string }) {
	const workers = 8
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for _, it := range items {
		it := it
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			softExec(it.label, it.sql)
		}()
	}
	wg.Wait()
}

// dbUnderPressure reports whether the Postgres pool is close to exhausted.
// Cheap (in-memory pool stats only, no DB round-trip) — safe to call from
// hot-path middleware. Use it to shed load on non-essential endpoints
// BEFORE they queue for a connection, rather than after.
func dbUnderPressure() bool {
	s := db.Stat()
	if s.MaxConns() == 0 {
		return false
	}
	return float64(s.AcquiredConns()) >= float64(s.MaxConns())*0.9
}

func startHealthMonitor() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("WARN [healthMonitor] recovered from panic: %v", r)
					}
				}()
				s := db.Stat()
				log.Printf("📊 PG pool — total:%d idle:%d acquired:%d constructing:%d",
					s.TotalConns(), s.IdleConns(), s.AcquiredConns(), s.ConstructingConns())
				if float64(s.AcquiredConns()) > float64(s.MaxConns())*0.8 {
					log.Printf("⚠️  PG pool pressure: %d/%d connections in use", s.AcquiredConns(), s.MaxConns())
				}
				rctx, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				if err := rdb.Ping(rctx).Err(); err != nil {
					log.Printf("⚠️  Redis ping failed: %v", err)
				}
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				g := runtime.NumGoroutine()
				allocMB := ms.Alloc / 1024 / 1024
				log.Printf("📊 Go heap — alloc:%dMB sys:%dMB goroutines:%d",
					allocMB, ms.Sys/1024/1024, g)
				if g > 800 {
					log.Printf("⚠️  goroutine pressure: %d", g)
				}
				if allocMB > 1200 {
					log.Printf("⚠️  heap %dMB — forcing GC", allocMB)
					runtime.GC()
					debug.FreeOSMemory()
				}
			}()
		}
	}()
}

func execWithRetry(label, sql string, args ...any) error {
	const maxRetries = 3
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		execCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, lastErr = db.Exec(execCtx, sql, args...)
		cancel()
		if lastErr == nil {
			return nil
		}
		log.Printf("WARN [%s] attempt %d failed: %v", label, i+1, lastErr)
		time.Sleep(time.Duration(i+1) * 200 * time.Millisecond)
	}
	return fmt.Errorf("[%s] gave up after %d retries: %w", label, maxRetries, lastErr)
}

func startConnectionWatchdog() {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("WARN [watchdog] recovered: %v", r)
					}
				}()
				pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				defer cancel()
				if err := db.Ping(pingCtx); err != nil {
					log.Printf("⚠️  DB watchdog ping failed: %v — pool will reconnect", err)
				}
			}()
		}
	}()
}

func startExpirySweep() {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("WARN [expirySweep] recovered: %v", r)
					}
				}()
				sCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				tag, err := db.Exec(sCtx, "DELETE FROM posts WHERE expires_at IS NOT NULL AND expires_at <= NOW()")
				if err != nil {
					log.Printf("WARN [expirySweep]: %v", err)
					return
				}
				if n := tag.RowsAffected(); n > 0 {
					log.Printf("⏰ Expiry sweep removed %d expired post(s)", n)
				}
			}()
		}
	}()
}

var (
	shutdownHooks []func()
	shutdownMu    sync.Mutex
)

func RegisterShutdownHook(fn func()) {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()
	shutdownHooks = append(shutdownHooks, fn)
}

func runShutdownHooks() {
	shutdownMu.Lock()
	hooks := make([]func(), len(shutdownHooks))
	copy(hooks, shutdownHooks)
	shutdownMu.Unlock()

	ctx15, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := len(hooks) - 1; i >= 0; i-- {
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("WARN [shutdown hook %d] recovered: %v", i, r)
					}
				}()
				hooks[i]()
			}()
		}
		close(done)
	}()

	select {
	case <-done:
		log.Println("✅ Graceful shutdown complete")
	case <-ctx15.Done():
		log.Println("⚠️  Shutdown timed out after 15 s — forcing exit")
	}
}

func initDatabase() {
	numCPU := runtime.NumCPU()
	runtime.GOMAXPROCS(numCPU)
	log.Printf("🖥️  GOMAXPROCS set to %d", numCPU)

	// This process shares an 8 GB box with PostgreSQL, Redis, and pgpool.
	// Without a soft memory cap the Go heap can grow until the kernel OOM
	// killer takes down Postgres or this app — which looks like the whole
	// website "crashing". 1.6 GiB leaves headroom for the rest of the stack.
	debug.SetMemoryLimit(1600 << 20)
	debug.SetGCPercent(50)
	log.Println("🧠 Go soft memory limit 1.6 GiB, GC percent 50")

	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found — using system environment variables")
	}

	// Redis is initialized once in main.go's initRedis() against
	// platform.RDB. Reuse that single client here instead of dialing a
	// second, independent one — two separate clients meant every helper
	// in this file (CacheGet/Set/Del, login/signup rate-limiting) was
	// talking to a connection main.go never touched, and vice versa
	// (online-users tracking, feed cache, trending worker all live on
	// platform.RDB). This is what caused the online-slider/session-state
	// inconsistencies.
	rdb = platform.RDB

	dbName := getEnv("DB_NAME", "social_media_db")
	connStr := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		getEnv("DB_USER", "user"),
		getEnv("DB_PASSWORD", "password"),
		getEnv("DB_HOST", "db"),
		getEnv("DB_PORT", "5432"),
		dbName,
	)

	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		log.Fatal("Unable to parse DB config:", err)
	}

	// PgBouncer in transaction pool mode hands the same physical Postgres
	// connection to different app requests between transactions. pgx's
	// default extended-protocol mode caches prepared statements on a
	// specific physical connection, which becomes unsafe under that
	// setup — a statement prepared by one request can go missing or
	// collide when a different request is routed to the same physical
	// connection. Simple protocol resends full query text every time
	// instead of relying on server-side prepared-statement state, which
	// is the required/safe mode behind transaction-mode PgBouncer. This
	// is what was causing intermittent, hard-to-reproduce DB errors.
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	// Kill runaway SQL so a stuck query cannot pin a pool slot forever.
	// Many handlers still use the package-level Background context, which
	// never times out on acquire/query — Postgres-side timeouts are the
	// backstop that actually returns those connections.
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = map[string]string{}
	}
	config.ConnConfig.RuntimeParams["statement_timeout"] = "4000"
	config.ConnConfig.RuntimeParams["lock_timeout"] = "2500"
	config.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "5000"
	config.ConnConfig.RuntimeParams["application_name"] = "dostana"

	// 4 vCPU / 8 GB, PgBouncer in front with default_pool_size=100 and
	// max_client_conn=1000 (confirmed Aug 22) — plenty of headroom above
	// the old 12-conn cap. That cap was saturating under normal traffic:
	// dostana-watchdog.log shows repeated total:12/acquired:12 events
	// correlating directly with HTTP 000/503 health-check failures and
	// auto-restarts from Aug 14 onward. Raised to 24; statement_timeout
	// (4s) and lock_timeout (2.5s) below still bound how long any one
	// slot can be held, so this is a safe 2x increase, not unbounded growth.
	config.MaxConns = int32(max(8, min(24, numCPU*6)))
	config.MinConns = 2
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second
	config.ConnConfig.ConnectTimeout = 5 * time.Second

	for i := 1; i <= 10; i++ {
		db, err = pgxpool.NewWithConfig(context.Background(), config)
		if err != nil {
			log.Printf("⏳ DB pool create attempt %d/10 failed: %v", i, err)
			time.Sleep(time.Duration(i*2) * time.Second)
			continue
		}
		pingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		pingErr := db.Ping(pingCtx)
		cancel()
		if pingErr == nil {
			log.Println("✅ PostgreSQL pool ready")
			break
		}
		db.Close()
		if i == 10 {
			log.Fatalf("FATAL: Cannot connect to PostgreSQL after 10 attempts: %v", pingErr)
		}
		log.Printf("⏳ DB ping attempt %d/10 failed, retrying in %ds…", i, i*2)
		time.Sleep(time.Duration(i*2) * time.Second)
	}

	// FIX: ALTER DATABASE does not accept a function call like
	// current_database() — it needs the literal database name.
	softExec("set default timezone", fmt.Sprintf(
		`ALTER DATABASE %s SET timezone TO 'Asia/Karachi'`, dbName,
	))

	softExecBatch([]struct{ label, sql string }{
		{"tz-migrate posts.created_at", `ALTER TABLE posts ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate comments.created_at", `ALTER TABLE comments ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate mehfil_messages.created_at", `ALTER TABLE mehfil_messages ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate mehfil_messages.edited_at", `ALTER TABLE mehfil_messages ALTER COLUMN edited_at TYPE TIMESTAMPTZ USING edited_at AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate users.created_at", `ALTER TABLE users ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate users.last_active", `ALTER TABLE users ALTER COLUMN last_active TYPE TIMESTAMPTZ USING last_active AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate users.last_profile_image_change", `ALTER TABLE users ALTER COLUMN last_profile_image_change TYPE TIMESTAMPTZ USING last_profile_image_change AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate users.last_points_claim", `ALTER TABLE users ALTER COLUMN last_points_claim TYPE TIMESTAMPTZ USING last_points_claim AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate notifications.created_at", `ALTER TABLE notifications ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate chat_notifications.created_at", `ALTER TABLE chat_notifications ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate chat_invites.created_at", `ALTER TABLE chat_invites ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate followers.created_at", `ALTER TABLE followers ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate interactions.created_at", `ALTER TABLE interactions ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate messages.created_at", `ALTER TABLE messages ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate mehfils.created_at", `ALTER TABLE mehfils ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate mehfil_members.joined_at", `ALTER TABLE mehfil_members ALTER COLUMN joined_at TYPE TIMESTAMPTZ USING joined_at AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate mehfil_activity.created_at", `ALTER TABLE mehfil_activity ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'Asia/Karachi'`},
		{"tz-migrate posts.allow_comments", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS allow_comments BOOLEAN DEFAULT TRUE`},
		{"tz-migrate posts.followers_only", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS followers_only BOOLEAN DEFAULT FALSE`},
		{"tz-migrate posts.expires_at", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ`},
		{"tz-migrate idx_posts_expires_at", `CREATE INDEX IF NOT EXISTS idx_posts_expires_at ON posts(expires_at) WHERE expires_at IS NOT NULL`},
		{"tz-migrate users.default_disabled_reactions", `ALTER TABLE users ADD COLUMN IF NOT EXISTS default_disabled_reactions TEXT DEFAULT ''`},
		{"tz-migrate users.default_disable_comments", `ALTER TABLE users ADD COLUMN IF NOT EXISTS default_disable_comments BOOLEAN DEFAULT FALSE`},
		{"tz-migrate users.default_expires_in", `ALTER TABLE users ADD COLUMN IF NOT EXISTS default_expires_in TEXT DEFAULT ''`},
		{"tz-migrate users.default_followers_only", `ALTER TABLE users ADD COLUMN IF NOT EXISTS default_followers_only BOOLEAN DEFAULT FALSE`},
		{"tz-migrate posts.disabled_reactions", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS disabled_reactions TEXT DEFAULT ''`},
		{"tz-migrate posts.repost_of", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS repost_of INT`},
		{"tz-migrate posts.repost_thought", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS repost_thought TEXT DEFAULT ''`},
		{"tz-migrate posts.repost_policy", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS repost_policy TEXT DEFAULT 'public'`},
		{"tz-migrate posts.thoughts_policy", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS thoughts_policy TEXT DEFAULT 'public'`},
		{"tz-migrate posts.source", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'share'`},
		{"tz-migrate posts.repost_count", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS repost_count INT DEFAULT 0`},
		{"tz-migrate users.default_repost_policy", `ALTER TABLE users ADD COLUMN IF NOT EXISTS default_repost_policy TEXT DEFAULT 'public'`},
		{"tz-migrate users.default_thoughts_policy", `ALTER TABLE users ADD COLUMN IF NOT EXISTS default_thoughts_policy TEXT DEFAULT 'public'`},
	})

	log.Println("✅ Connected to PostgreSQL and synchronized to Asia/Karachi")

	mustExec("create users", `
		CREATE TABLE IF NOT EXISTS users (
			id                        SERIAL PRIMARY KEY,
			username                  TEXT         UNIQUE NOT NULL,
			password                  TEXT         NOT NULL,
			bio                       TEXT,
			city                      TEXT,
			gender                    TEXT,
			birth_date                DATE,
			points                    INT          DEFAULT 0,
			profile_image             TEXT         DEFAULT '',
			user_type                 TEXT         DEFAULT 'normal',
			is_banned                 BOOLEAN      DEFAULT FALSE,
			is_private                BOOLEAN      DEFAULT FALSE,
			is_hardware_banned        BOOLEAN      DEFAULT FALSE,
			ip_address                TEXT         NOT NULL DEFAULT '',
			subnet                    TEXT         NOT NULL DEFAULT '',
			device_key                TEXT         NOT NULL DEFAULT '',
			hardware_id               TEXT         NOT NULL DEFAULT '',
			last_profile_image_change TIMESTAMPTZ,
			last_active               TIMESTAMPTZ  DEFAULT NOW(),
			last_points_claim         TIMESTAMPTZ,
			trending_post_count       INT          DEFAULT 0,
			has_trending_star         BOOLEAN      DEFAULT FALSE,
			had_sex_before            BOOLEAN,
			had_kissed_before         BOOLEAN,
			weight_kg                 FLOAT,
			height_cm                 FLOAT,
			marriage_pref             TEXT         DEFAULT '',
			family_type               TEXT         DEFAULT '',
			family_wealth             TEXT         DEFAULT '',
			family_marriage_policy    TEXT         DEFAULT '',
			mental_health             TEXT         DEFAULT '',
			health_score              SMALLINT,
			best_advice               TEXT         DEFAULT '',
			best_life_lesson          TEXT         DEFAULT '',
			happiness_level           SMALLINT,
			created_at                TIMESTAMPTZ  DEFAULT NOW()
		)`)

	mustExec("create posts", `
		CREATE TABLE IF NOT EXISTS posts (
			id             SERIAL PRIMARY KEY,
			content        TEXT,
			author         TEXT,
			allow_jhappi   BOOLEAN     DEFAULT TRUE,
			allow_chupair  BOOLEAN     DEFAULT TRUE,
			image_url      TEXT        DEFAULT '',
			category       TEXT        DEFAULT 'General',
			is_trending    BOOLEAN     DEFAULT FALSE,
			trending_score FLOAT       DEFAULT 0,
			trending_at    TIMESTAMPTZ,
			source         TEXT        NOT NULL DEFAULT 'share',
			created_at     TIMESTAMPTZ DEFAULT NOW()
		)`)

	mustExec("create comments", `
		CREATE TABLE IF NOT EXISTS comments (
			id                SERIAL PRIMARY KEY,
			post_id           INT REFERENCES posts(id) ON DELETE CASCADE,
			author            TEXT NOT NULL,
			content           TEXT NOT NULL,
			parent_comment_id INT REFERENCES comments(id) ON DELETE CASCADE,
			created_at        TIMESTAMPTZ DEFAULT NOW()
		)`)

	mustExec("create followers", `
		CREATE TABLE IF NOT EXISTS followers (
			follower   TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			followee   TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			PRIMARY KEY (follower, followee)
		)`)

	mustExec("create interactions", `
		CREATE TABLE IF NOT EXISTS interactions (
			id         SERIAL PRIMARY KEY,
			post_id    INT  REFERENCES posts(id) ON DELETE CASCADE,
			sender     TEXT REFERENCES users(username) ON DELETE CASCADE,
			type       TEXT NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			UNIQUE(post_id, sender)
		)`)

	mustExec("create messages", `
		CREATE TABLE IF NOT EXISTS messages (
			id         SERIAL PRIMARY KEY,
			sender     TEXT REFERENCES users(username) ON DELETE CASCADE,
			recipient  TEXT REFERENCES users(username) ON DELETE CASCADE,
			content    TEXT NOT NULL,
			image_url  TEXT,
			is_read    BOOLEAN     DEFAULT FALSE,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`)

	mustExec("create notifications", `
		CREATE TABLE IF NOT EXISTS notifications (
			id            SERIAL PRIMARY KEY,
			recipient     TEXT NOT NULL,
			sender        TEXT NOT NULL,
			type          TEXT NOT NULL,
			post_id       INT  REFERENCES posts(id) ON DELETE CASCADE,
			comment_id    INT  REFERENCES comments(id) ON DELETE CASCADE,
			mehfil_id     INT,
			mehfil_msg_id INT,
			content       TEXT,
			is_read       BOOLEAN     DEFAULT FALSE,
			is_seen       BOOLEAN     DEFAULT FALSE,
			created_at    TIMESTAMPTZ DEFAULT NOW()
		)`)

	mustExec("create chat_invites", `
		CREATE TABLE IF NOT EXISTS chat_invites (
			id         SERIAL PRIMARY KEY,
			sender     TEXT REFERENCES users(username) ON DELETE CASCADE,
			recipient  TEXT REFERENCES users(username) ON DELETE CASCADE,
			status     TEXT    DEFAULT 'pending',
			is_seen    BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			UNIQUE(sender, recipient)
		)`)

	mustExec("create chat_notifications", `
		CREATE TABLE IF NOT EXISTS chat_notifications (
			id         SERIAL PRIMARY KEY,
			recipient  TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			sender     TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			message_id INT  NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
			is_read    BOOLEAN     DEFAULT FALSE,
			is_seen    BOOLEAN     DEFAULT FALSE,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`)

	mustExec("create mehfils", `
		CREATE TABLE IF NOT EXISTS mehfils (
			id             SERIAL PRIMARY KEY,
			admin_username TEXT REFERENCES users(username) ON DELETE CASCADE,
			topic          TEXT NOT NULL,
			rules          TEXT,
			joining_fee    INTEGER DEFAULT 0,
			is_locked      BOOLEAN DEFAULT FALSE,
			welcome_msg    TEXT    DEFAULT '',
			mehfil_color   TEXT    DEFAULT '#10b981',
			max_members    INT     DEFAULT 0,
			created_at     TIMESTAMPTZ DEFAULT NOW()
		)`)

	mustExec("create mehfil_members", `
		CREATE TABLE IF NOT EXISTS mehfil_members (
			mehfil_id    INT REFERENCES mehfils(id) ON DELETE CASCADE,
			username     TEXT REFERENCES users(username) ON DELETE CASCADE,
			role         TEXT    DEFAULT 'member',
			custom_title TEXT    DEFAULT '',
			is_kicked    BOOLEAN DEFAULT FALSE,
			muted_until  TIMESTAMPTZ,
			joined_at    TIMESTAMPTZ DEFAULT NOW(),
			PRIMARY KEY (mehfil_id, username)
		)`)

	mustExec("create mehfil_messages", `
		CREATE TABLE IF NOT EXISTS mehfil_messages (
			id              SERIAL PRIMARY KEY,
			mehfil_id       INT REFERENCES mehfils(id) ON DELETE CASCADE,
			author          TEXT REFERENCES users(username) ON DELETE CASCADE,
			content         TEXT NOT NULL,
			image_url       TEXT    DEFAULT '',
			parent_id       INT  REFERENCES mehfil_messages(id) ON DELETE CASCADE,
			reactions       TEXT    DEFAULT '',
			is_pinned       BOOLEAN DEFAULT FALSE,
			is_announcement BOOLEAN DEFAULT FALSE,
			is_hidden       BOOLEAN DEFAULT FALSE,
			edited_at       TIMESTAMPTZ,
			created_at      TIMESTAMPTZ DEFAULT NOW()
		)`)

	mustExec("create mehfil_activity", `
		CREATE TABLE IF NOT EXISTS mehfil_activity (
			id            SERIAL PRIMARY KEY,
			mehfil_id     INT REFERENCES mehfils(id) ON DELETE CASCADE,
			username      TEXT,
			activity_type TEXT,
			created_at    TIMESTAMPTZ DEFAULT NOW()
		)`)

	mustExec("create mehfil_polls", `
		CREATE TABLE IF NOT EXISTS mehfil_polls (
			id         SERIAL PRIMARY KEY,
			mehfil_id  INT,
			question   TEXT,
			options    TEXT,
			votes      TEXT DEFAULT '{}',
			created_by TEXT,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`)

	mustExec("create mehfil_bookmarks", `
		CREATE TABLE IF NOT EXISTS mehfil_bookmarks (
			mehfil_id  INT,
			username   TEXT,
			msg_id     INT,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			PRIMARY KEY (mehfil_id, username, msg_id)
		)`)

	mustExec("create mehfil_invites", `
		CREATE TABLE IF NOT EXISTS mehfil_invites (
			id          SERIAL PRIMARY KEY,
			mehfil_id   INT  REFERENCES mehfils(id) ON DELETE CASCADE,
			inviter     TEXT REFERENCES users(username) ON DELETE CASCADE,
			invitee     TEXT REFERENCES users(username) ON DELETE CASCADE,
			status      TEXT DEFAULT 'pending',
			rejected_at TIMESTAMPTZ,
			created_at  TIMESTAMPTZ DEFAULT NOW(),
			UNIQUE(mehfil_id, invitee)
		)`)

	mustExec("create banned_identifiers", `
		CREATE TABLE IF NOT EXISTS banned_identifiers (
			id         BIGSERIAL PRIMARY KEY,
			kind       TEXT        NOT NULL,
			identifier TEXT        NOT NULL,
			reason     TEXT        DEFAULT '',
			created_at TIMESTAMPTZ DEFAULT NOW(),
			UNIQUE (kind, identifier)
		)`)

	mustExec("create banned_usernames", `
		CREATE TABLE IF NOT EXISTS banned_usernames (
			id         BIGSERIAL PRIMARY KEY,
			username   TEXT        NOT NULL UNIQUE,
			reason     TEXT        DEFAULT '',
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`)

	mustExec("create home_voice_messages", `
		CREATE TABLE IF NOT EXISTS home_voice_messages (
			id               SERIAL PRIMARY KEY,
			author           TEXT        NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			file_url         TEXT        NOT NULL,
			file_size        BIGINT      NOT NULL DEFAULT 0,
			duration_seconds INT         NOT NULL DEFAULT 0,
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)

	mustExec("create blocks", `
		CREATE TABLE IF NOT EXISTS blocks (
			blocker    TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			blocked    TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			PRIMARY KEY (blocker, blocked)
		)`)

	mustExec("create comment_reactions", `
		CREATE TABLE IF NOT EXISTS comment_reactions (
			comment_id INT  NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
			sender     TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			type       TEXT NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			PRIMARY KEY (comment_id, sender)
		)`)

	mustExec("create device_tokens", `
		CREATE TABLE IF NOT EXISTS device_tokens (
			id         SERIAL PRIMARY KEY,
			username   TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			fcm_token  TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (username, fcm_token)
		)`)
	softExec("index idx_device_tokens_username", `CREATE INDEX IF NOT EXISTS idx_device_tokens_username ON device_tokens(username)`)

	softExecBatch([]struct{ label, sql string }{
		{"migrate users.user_type", `ALTER TABLE users ADD COLUMN IF NOT EXISTS user_type TEXT DEFAULT 'normal'`},
		{"migrate users.is_banned", `ALTER TABLE users ADD COLUMN IF NOT EXISTS is_banned BOOLEAN DEFAULT FALSE`},
		{"migrate users.is_private", `ALTER TABLE users ADD COLUMN IF NOT EXISTS is_private BOOLEAN DEFAULT FALSE`},
		{"migrate users.is_hardware_banned", `ALTER TABLE users ADD COLUMN IF NOT EXISTS is_hardware_banned BOOLEAN DEFAULT FALSE`},
		{"migrate users.ip_address", `ALTER TABLE users ADD COLUMN IF NOT EXISTS ip_address TEXT NOT NULL DEFAULT ''`},
		{"migrate users.subnet", `ALTER TABLE users ADD COLUMN IF NOT EXISTS subnet TEXT NOT NULL DEFAULT ''`},
		{"migrate users.device_key", `ALTER TABLE users ADD COLUMN IF NOT EXISTS device_key TEXT NOT NULL DEFAULT ''`},
		{"migrate users.hardware_id", `ALTER TABLE users ADD COLUMN IF NOT EXISTS hardware_id TEXT NOT NULL DEFAULT ''`},
		{"migrate users.points", `ALTER TABLE users ADD COLUMN IF NOT EXISTS points INT DEFAULT 0`},
		{"migrate users.last_active", `ALTER TABLE users ADD COLUMN IF NOT EXISTS last_active TIMESTAMPTZ DEFAULT NOW()`},
		{"migrate users.last_points_claim", `ALTER TABLE users ADD COLUMN IF NOT EXISTS last_points_claim TIMESTAMPTZ`},
		{"migrate posts.allow_jhappi", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS allow_jhappi BOOLEAN DEFAULT TRUE`},
		{"migrate posts.voice_url", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS voice_url TEXT DEFAULT ''`},
		{"migrate posts.allow_chupair", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS allow_chupair BOOLEAN DEFAULT TRUE`},
		{"migrate posts.image_url", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS image_url TEXT DEFAULT ''`},
		{"migrate posts.category", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS category TEXT DEFAULT 'General'`},
		{"migrate messages.image_url", `ALTER TABLE messages ADD COLUMN IF NOT EXISTS image_url TEXT`},
		{"migrate messages.voice_url", `ALTER TABLE messages ADD COLUMN IF NOT EXISTS voice_url TEXT DEFAULT ''`},
		{"migrate messages.reply_to_id", `ALTER TABLE messages ADD COLUMN IF NOT EXISTS reply_to_id INT REFERENCES messages(id) ON DELETE SET NULL`},
		{"migrate notifications.is_read", `ALTER TABLE notifications ADD COLUMN IF NOT EXISTS is_read BOOLEAN DEFAULT FALSE`},
		{"migrate notifications.is_seen", `ALTER TABLE notifications ADD COLUMN IF NOT EXISTS is_seen BOOLEAN DEFAULT FALSE`},
		{"migrate notifications.mehfil_id", `ALTER TABLE notifications ADD COLUMN IF NOT EXISTS mehfil_id INT`},
		{"migrate notifications.mehfil_msg_id", `ALTER TABLE notifications ADD COLUMN IF NOT EXISTS mehfil_msg_id INT`},
		{"migrate chat_invites.is_seen", `ALTER TABLE chat_invites ADD COLUMN IF NOT EXISTS is_seen BOOLEAN DEFAULT FALSE`},
		{"migrate chat_notifications.is_read", `ALTER TABLE chat_notifications ADD COLUMN IF NOT EXISTS is_read BOOLEAN DEFAULT FALSE`},
		{"migrate chat_notifications.is_seen", `ALTER TABLE chat_notifications ADD COLUMN IF NOT EXISTS is_seen BOOLEAN DEFAULT FALSE`},
		{"migrate mehfils.joining_fee", `ALTER TABLE mehfils ADD COLUMN IF NOT EXISTS joining_fee INTEGER DEFAULT 0`},
		{"migrate mehfils.is_locked", `ALTER TABLE mehfils ADD COLUMN IF NOT EXISTS is_locked BOOLEAN DEFAULT FALSE`},
		{"migrate mehfils.welcome_msg", `ALTER TABLE mehfils ADD COLUMN IF NOT EXISTS welcome_msg TEXT DEFAULT ''`},
		{"migrate mehfils.mehfil_color", `ALTER TABLE mehfils ADD COLUMN IF NOT EXISTS mehfil_color TEXT DEFAULT '#10b981'`},
		{"migrate mehfils.max_members", `ALTER TABLE mehfils ADD COLUMN IF NOT EXISTS max_members INT DEFAULT 0`},
		{"migrate mehfil_members.custom_title", `ALTER TABLE mehfil_members ADD COLUMN IF NOT EXISTS custom_title TEXT DEFAULT ''`},
		{"migrate mehfil_members.muted_until", `ALTER TABLE mehfil_members ADD COLUMN IF NOT EXISTS muted_until TIMESTAMPTZ`},
		{"migrate mehfil_members.spam_banned_until", `ALTER TABLE mehfil_members ADD COLUMN IF NOT EXISTS spam_banned_until TIMESTAMPTZ`},
		{"migrate mehfil_messages.image_url", `ALTER TABLE mehfil_messages ADD COLUMN IF NOT EXISTS image_url TEXT DEFAULT ''`},
		{"migrate mehfil_messages.reactions", `ALTER TABLE mehfil_messages ADD COLUMN IF NOT EXISTS reactions TEXT DEFAULT ''`},
		{"migrate mehfil_messages.is_pinned", `ALTER TABLE mehfil_messages ADD COLUMN IF NOT EXISTS is_pinned BOOLEAN DEFAULT FALSE`},
		{"migrate mehfil_messages.is_announcement", `ALTER TABLE mehfil_messages ADD COLUMN IF NOT EXISTS is_announcement BOOLEAN DEFAULT FALSE`},
		{"migrate mehfil_messages.is_hidden", `ALTER TABLE mehfil_messages ADD COLUMN IF NOT EXISTS is_hidden BOOLEAN DEFAULT FALSE`},
		{"migrate mehfil_messages.edited_at", `ALTER TABLE mehfil_messages ADD COLUMN IF NOT EXISTS edited_at TIMESTAMPTZ`},
		{"migrate home_voice_messages.duration_seconds", `ALTER TABLE home_voice_messages ADD COLUMN IF NOT EXISTS duration_seconds INT NOT NULL DEFAULT 0`},
		{"migrate home_voice_messages.file_size", `ALTER TABLE home_voice_messages ADD COLUMN IF NOT EXISTS file_size BIGINT NOT NULL DEFAULT 0`},
		{"migrate posts.is_trending", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS is_trending BOOLEAN DEFAULT FALSE`},
		{"migrate posts.trending_score", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS trending_score FLOAT DEFAULT 0`},
		{"migrate posts.trending_at", `ALTER TABLE posts ADD COLUMN IF NOT EXISTS trending_at TIMESTAMPTZ`},
		{"migrate users.trending_post_count", `ALTER TABLE users ADD COLUMN IF NOT EXISTS trending_post_count INT DEFAULT 0`},
		{"migrate users.has_trending_star", `ALTER TABLE users ADD COLUMN IF NOT EXISTS has_trending_star BOOLEAN DEFAULT FALSE`},
		{"migrate users.fix_negative_points", `UPDATE users SET points = 0 WHERE points < 0`},
		{"migrate idx_posts_is_trending", `CREATE INDEX IF NOT EXISTS idx_posts_is_trending ON posts(is_trending, created_at DESC)`},
		{"migrate idx_posts_trending_score", `CREATE INDEX IF NOT EXISTS idx_posts_trending_score ON posts(trending_score DESC)`},
		{"migrate users.had_sex_before", `ALTER TABLE users ADD COLUMN IF NOT EXISTS had_sex_before BOOLEAN`},
		{"migrate users.had_kissed_before", `ALTER TABLE users ADD COLUMN IF NOT EXISTS had_kissed_before BOOLEAN`},
		{"migrate users.weight_kg", `ALTER TABLE users ADD COLUMN IF NOT EXISTS weight_kg FLOAT`},
		{"migrate users.height_cm", `ALTER TABLE users ADD COLUMN IF NOT EXISTS height_cm FLOAT`},
		{"migrate users.marriage_pref", `ALTER TABLE users ADD COLUMN IF NOT EXISTS marriage_pref TEXT DEFAULT ''`},
		{"migrate users.family_type", `ALTER TABLE users ADD COLUMN IF NOT EXISTS family_type TEXT DEFAULT ''`},
		{"migrate users.family_wealth", `ALTER TABLE users ADD COLUMN IF NOT EXISTS family_wealth TEXT DEFAULT ''`},
		{"migrate users.family_marriage_policy", `ALTER TABLE users ADD COLUMN IF NOT EXISTS family_marriage_policy TEXT DEFAULT ''`},
		{"migrate users.mental_health", `ALTER TABLE users ADD COLUMN IF NOT EXISTS mental_health TEXT DEFAULT ''`},
		{"migrate users.health_score", `ALTER TABLE users ADD COLUMN IF NOT EXISTS health_score SMALLINT`},
		{"migrate users.best_advice", `ALTER TABLE users ADD COLUMN IF NOT EXISTS best_advice TEXT DEFAULT ''`},
		{"migrate users.best_life_lesson", `ALTER TABLE users ADD COLUMN IF NOT EXISTS best_life_lesson TEXT DEFAULT ''`},
		{"migrate users.happiness_level", `ALTER TABLE users ADD COLUMN IF NOT EXISTS happiness_level SMALLINT`},
	})

	// FIX: `ALTER TABLE ... ADD CONSTRAINT IF NOT EXISTS` is not valid
	// PostgreSQL syntax for CHECK constraints. Guard with a DO block that
	// checks pg_constraint first, same pattern as the FK guards below.
	softExecBatch([]struct{ label, sql string }{
		{"fk posts.author", `
			DO $$ BEGIN
				IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='fk_posts_author' AND conrelid='posts'::regclass) THEN
					ALTER TABLE posts ADD CONSTRAINT fk_posts_author FOREIGN KEY (author) REFERENCES users(username) ON DELETE CASCADE;
				END IF;
			END $$`},
		{"fk comments.author", `
			DO $$ BEGIN
				IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='fk_comments_author' AND conrelid='comments'::regclass) THEN
					ALTER TABLE comments ADD CONSTRAINT fk_comments_author FOREIGN KEY (author) REFERENCES users(username) ON DELETE CASCADE;
				END IF;
			END $$`},
		{"chk users.points_non_negative", `
			DO $$ BEGIN
				IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='points_non_negative' AND conrelid='users'::regclass) THEN
					ALTER TABLE users ADD CONSTRAINT points_non_negative CHECK (points >= 0);
				END IF;
			END $$`},
		{"chk users.health_score_range", `
			DO $$ BEGIN
				IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='health_score_range' AND conrelid='users'::regclass) THEN
					ALTER TABLE users ADD CONSTRAINT health_score_range CHECK (health_score IS NULL OR (health_score >= 1 AND health_score <= 10));
				END IF;
			END $$`},
		{"chk users.happiness_level_range", `
			DO $$ BEGIN
				IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='happiness_level_range' AND conrelid='users'::regclass) THEN
					ALTER TABLE users ADD CONSTRAINT happiness_level_range CHECK (happiness_level IS NULL OR (happiness_level >= 1 AND happiness_level <= 10));
				END IF;
			END $$`},
				{"chk users.display_name_length", `
			DO $$ BEGIN
				IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='display_name_length' AND conrelid='users'::regclass) THEN
					ALTER TABLE users ADD CONSTRAINT display_name_length CHECK (char_length(display_name) <= 10);
				END IF;
			END $$`},
		{"fk posts.repost_of", `
			DO $$ BEGIN
				IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='fk_posts_repost_of' AND conrelid='posts'::regclass) THEN
					ALTER TABLE posts ADD CONSTRAINT fk_posts_repost_of FOREIGN KEY (repost_of) REFERENCES posts(id) ON DELETE SET NULL;
				END IF;
			END $$`},
	})

	// ── Trending star is a permanent, earned badge, not a live status ──────
	// The trending worker periodically recalculates who's CURRENTLY trending
	// and, by design, clears has_trending_star for anyone who falls out of
	// that rolling window — which is why the fire icon kept disappearing
	// from profiles/posts/online-slider. This trigger makes the flag
	// monotonic at the database level: once a user earns it (has_trending_star
	// flips to TRUE), NO update from ANY code path — the worker, an admin
	// action, a bad query, anything — can ever flip it back to FALSE again.
	// This is enforced by Postgres itself, so it holds even if the app-side
	// trending logic changes later or has bugs.
	mustExec("create fn lock_trending_star", `
		CREATE OR REPLACE FUNCTION lock_trending_star() RETURNS trigger AS $$
		BEGIN
			IF OLD.has_trending_star IS TRUE AND NEW.has_trending_star IS NOT TRUE THEN
				NEW.has_trending_star := TRUE;
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`)

	softExec("create trigger trg_lock_trending_star", `
		DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_lock_trending_star') THEN
				CREATE TRIGGER trg_lock_trending_star
				BEFORE UPDATE ON users
				FOR EACH ROW
				EXECUTE FUNCTION lock_trending_star();
			END IF;
		END $$`)

	// NOTE: idx_follow_requests_target / idx_follow_requests_requester need

	// NOTE: idx_follow_requests_target / idx_follow_requests_requester need
	// follow_requests to exist first, so they're created later, right after
	// that table is created below — kept out of this batch intentionally.
	softExecBatch([]struct{ label, sql string }{
		{"index idx_followers_follower", `CREATE INDEX IF NOT EXISTS idx_followers_follower ON followers(follower)`},
		{"index idx_followers_followee", `CREATE INDEX IF NOT EXISTS idx_followers_followee ON followers(followee)`},
		{"index idx_comments_post_id", `CREATE INDEX IF NOT EXISTS idx_comments_post_id ON comments(post_id)`},
		{"index idx_comments_parent_comment_id", `CREATE INDEX IF NOT EXISTS idx_comments_parent_comment_id ON comments(parent_comment_id)`},
		{"index idx_notifications_recipient", `CREATE INDEX IF NOT EXISTS idx_notifications_recipient ON notifications(recipient)`},
		{"index idx_notifications_is_read", `CREATE INDEX IF NOT EXISTS idx_notifications_is_read ON notifications(is_read)`},
		{"index idx_notifications_recipient_unread", `CREATE INDEX IF NOT EXISTS idx_notifications_recipient_unread ON notifications(recipient) WHERE is_read = FALSE`},
		{"index idx_chat_notifs_recipient_unread", `CREATE INDEX IF NOT EXISTS idx_chat_notifications_recipient_unread ON chat_notifications(recipient) WHERE is_read = FALSE`},
		{"index idx_users_ip", `CREATE INDEX IF NOT EXISTS idx_users_ip ON users(ip_address)`},
		{"index idx_users_subnet", `CREATE INDEX IF NOT EXISTS idx_users_subnet ON users(subnet)`},
		{"index idx_users_devkey", `CREATE INDEX IF NOT EXISTS idx_users_devkey ON users(device_key)`},
		{"index idx_users_hwid", `CREATE INDEX IF NOT EXISTS idx_users_hwid ON users(hardware_id)`},
		{"index idx_banned_kind_id", `CREATE INDEX IF NOT EXISTS idx_banned_kind_id ON banned_identifiers(kind, identifier)`},
		{"index idx_banned_usernames", `CREATE INDEX IF NOT EXISTS idx_banned_usernames ON banned_usernames(LOWER(username))`},
		{"index idx_mehfil_messages_mehfil_id", `CREATE INDEX IF NOT EXISTS idx_mehfil_messages_mehfil_id ON mehfil_messages(mehfil_id)`},
		{"index idx_mehfil_messages_created_at", `CREATE INDEX IF NOT EXISTS idx_mehfil_messages_created_at ON mehfil_messages(created_at DESC)`},
		{"index idx_mehfil_messages_parent_id", `CREATE INDEX IF NOT EXISTS idx_mehfil_messages_parent_id ON mehfil_messages(parent_id)`},
		{"index idx_mehfil_members_mehfil_id", `CREATE INDEX IF NOT EXISTS idx_mehfil_members_mehfil_id ON mehfil_members(mehfil_id)`},
				{"index idx_mehfil_invites_invitee", `CREATE INDEX IF NOT EXISTS idx_mehfil_invites_invitee ON mehfil_invites(invitee, status)`},
		{"index idx_posts_created_at", `CREATE INDEX IF NOT EXISTS idx_posts_created_at ON posts(created_at DESC)`},
		{"index idx_posts_author", `CREATE INDEX IF NOT EXISTS idx_posts_author ON posts(author)`},
		{"index idx_posts_author_source", `CREATE INDEX IF NOT EXISTS idx_posts_author_source ON posts(author, source)`},
		{"index idx_messages_recipient", `CREATE INDEX IF NOT EXISTS idx_messages_recipient ON messages(recipient)`},
		{"index idx_messages_sender", `CREATE INDEX IF NOT EXISTS idx_messages_sender ON messages(sender)`},
		{"index idx_messages_chat_pair_lower", `CREATE INDEX IF NOT EXISTS idx_messages_chat_pair_lower ON messages(LOWER(sender), LOWER(recipient), created_at)`},
		{"index idx_chat_invites_pair_lower", `CREATE INDEX IF NOT EXISTS idx_chat_invites_pair_lower ON chat_invites(LOWER(sender), LOWER(recipient))`},
		{"index idx_chat_notifications_recipient_sender_lower", `CREATE INDEX IF NOT EXISTS idx_chat_notifications_recipient_sender_lower ON chat_notifications(LOWER(recipient), LOWER(sender))`},
		{"index idx_users_username_lower", `CREATE INDEX IF NOT EXISTS idx_users_username_lower ON users(LOWER(username))`}, {"index idx_home_voice_messages_author", `CREATE INDEX IF NOT EXISTS idx_home_voice_messages_author ON home_voice_messages(author)`},
		{"index idx_home_voice_messages_created_at", `CREATE INDEX IF NOT EXISTS idx_home_voice_messages_created_at ON home_voice_messages(created_at DESC)`},
		{"index idx_blocks_blocker", `CREATE INDEX IF NOT EXISTS idx_blocks_blocker ON blocks(blocker)`},
		{"index idx_blocks_blocked", `CREATE INDEX IF NOT EXISTS idx_blocks_blocked ON blocks(blocked)`},
		{"index idx_comment_reactions_comment_id", `CREATE INDEX IF NOT EXISTS idx_comment_reactions_comment_id ON comment_reactions(comment_id)`},
		{"index idx_users_birth_date", `CREATE INDEX IF NOT EXISTS idx_users_birth_date ON users(birth_date)`},
		{"index idx_posts_repost_of", `CREATE INDEX IF NOT EXISTS idx_posts_repost_of ON posts(repost_of)`},
		{"migrate users.require_follow_approval", `ALTER TABLE users ADD COLUMN IF NOT EXISTS require_follow_approval BOOLEAN DEFAULT FALSE`},
		{"migrate users.session_version", `ALTER TABLE users ADD COLUMN IF NOT EXISTS session_version INT NOT NULL DEFAULT 0`},
		{"migrate users.display_name", `ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name TEXT DEFAULT ''`},
		{"migrate users.display_name_color", `ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name_color TEXT DEFAULT ''`},
		{"migrate users.display_name_font", `ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name_font TEXT DEFAULT ''`},
		{"migrate users.display_name_effects", `ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name_effects TEXT DEFAULT ''`},
		{"migrate users.comment_banned_until", `ALTER TABLE users ADD COLUMN IF NOT EXISTS comment_banned_until TIMESTAMPTZ`},
		{"migrate users.mention_policy", `ALTER TABLE users ADD COLUMN IF NOT EXISTS mention_policy TEXT NOT NULL DEFAULT 'everyone'`},
	})
	// depends on the column added just above, run after the batch completes
	softExec("index idx_users_require_follow_approval", `CREATE INDEX IF NOT EXISTS idx_users_require_follow_approval ON users(require_follow_approval)`)

	mustExec("create post_mentions", `
		CREATE TABLE IF NOT EXISTS post_mentions (
			id         SERIAL PRIMARY KEY,
			post_id    INT  NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
			mentioner  TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			mentioned  TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			UNIQUE(post_id, mentioned)
		)`)
	softExec("index idx_post_mentions_mentioned", `CREATE INDEX IF NOT EXISTS idx_post_mentions_mentioned ON post_mentions(mentioned)`)

	mustExec("create follow_requests", `
		CREATE TABLE IF NOT EXISTS follow_requests (
			id          SERIAL PRIMARY KEY,
			requester   TEXT        NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			target      TEXT        NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			status      TEXT        NOT NULL DEFAULT 'pending',
			created_at  TIMESTAMPTZ DEFAULT NOW(),
			resolved_at TIMESTAMPTZ,
			UNIQUE(requester, target)
		)`)
	mustExec("create user_ip_history", `
		CREATE TABLE IF NOT EXISTS user_ip_history (
			id         BIGSERIAL PRIMARY KEY,
			username   TEXT        NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			ip_address TEXT        NOT NULL,
			subnet     TEXT        NOT NULL DEFAULT '',
			user_agent TEXT        NOT NULL DEFAULT '',
			seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)

	softExecBatch([]struct{ label, sql string }{
		{"index idx_follow_requests_target", `CREATE INDEX IF NOT EXISTS idx_follow_requests_target ON follow_requests(target, status)`},
		{"index idx_follow_requests_requester", `CREATE INDEX IF NOT EXISTS idx_follow_requests_requester ON follow_requests(requester)`},
		{"index idx_ip_history_username", `CREATE INDEX IF NOT EXISTS idx_ip_history_username ON user_ip_history(username)`},
		{"index idx_ip_history_ip", `CREATE INDEX IF NOT EXISTS idx_ip_history_ip ON user_ip_history(ip_address)`},
		{"index idx_ip_history_seen_at", `CREATE INDEX IF NOT EXISTS idx_ip_history_seen_at ON user_ip_history(seen_at DESC)`},
	})

	// ── Pending ID system ─────────────────────────────────────────────────────
	softExec("users.account_status", `ALTER TABLE users ADD COLUMN IF NOT EXISTS account_status TEXT NOT NULL DEFAULT 'approved'`)
	softExec("idx_users_account_status", `CREATE INDEX IF NOT EXISTS idx_users_account_status ON users(account_status)`)
	// Mark all existing users as approved so nothing breaks
	softExec("users.backfill_account_status", `UPDATE users SET account_status = 'approved' WHERE account_status != 'rejected'`)

	// ── DP (profile picture) competition feature ────────────────────────────
	startConnectionWatchdog()
	startHealthMonitor()
	startExpirySweep() // ADD THIS LINE

	// ── Site Trivia (auto-spawned "learn the website" + live-data
	// questions, every 1 minute forever — see trivia.go) ───────────────────
	initTriviaSchema()
	StartTriviaWorker()
	initBanSchema()

	// One-time backfill of the Redis username-autocomplete index from
	// Postgres, so every pre-existing account (not just future signups,
	// which go through IndexUsername in signupPost) becomes mentionable.
	// Runs in its own goroutine (see goSafe inside backfillUsernameIndex),
	// idempotent — safe to run again on every restart.
	backfillUsernameIndex()

	RegisterShutdownHook(func() {
		log.Println("🔌 Closing PostgreSQL pool…")
		db.Close()
	})
	RegisterShutdownHook(func() {
		log.Println("🔌 Closing Redis client…")
		if err := rdb.Close(); err != nil {
			log.Printf("WARN: Redis close: %v", err)
		}
	})

	log.Println("✅ Database tables, indexes, migrations, and cleanup jobs initialized!")
}

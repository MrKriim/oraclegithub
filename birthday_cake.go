package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
)

const (
	cakeCooldownSecs = 2 * 60 * 60 // 1 slice per user per 2 hours
	cakeSlicePoints  = 1000000     // 10 lac
	cakeAlwaysOpen   = false       // true = cake stays open outside the birthday window
	cakeGlobalKey    = "cake_global_v3"
	cakeWishLockSecs = 45
)

func cakeOpen() bool {
	if cakeAlwaysOpen {
		return true
	}
	p, _ := birthdayPhase()
	return p == 2
}

func initCakeSchema() {
		mustExec("create bday_stats", `
		CREATE TABLE IF NOT EXISTS bday_stats (
			username        TEXT PRIMARY KEY REFERENCES users(username) ON DELETE CASCADE,
			balloons_popped INT         NOT NULL DEFAULT 0,
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
	softExec("index idx_bday_stats_popped", `CREATE INDEX IF NOT EXISTS idx_bday_stats_popped ON bday_stats(balloons_popped DESC)`)
	mustExec("create cake_eaters", `
		CREATE TABLE IF NOT EXISTS cake_eaters (
			username     TEXT PRIMARY KEY REFERENCES users(username) ON DELETE CASCADE,
			last_eaten   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			total_slices INT         NOT NULL DEFAULT 0,
			total_points BIGINT      NOT NULL DEFAULT 0
		)`)
	mustExec("create cake_wishes", `
		CREATE TABLE IF NOT EXISTS cake_wishes (
			id         SERIAL PRIMARY KEY,
			username   TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			wish       TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
	mustExec("create bday_stats", `
		CREATE TABLE IF NOT EXISTS bday_stats (
			username        TEXT PRIMARY KEY REFERENCES users(username) ON DELETE CASCADE,
			balloons_popped INT         NOT NULL DEFAULT 0,
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
	softExec("index idx_cake_eaters_last", `CREATE INDEX IF NOT EXISTS idx_cake_eaters_last ON cake_eaters(last_eaten DESC)`)
	softExec("index idx_cake_wishes_created", `CREATE INDEX IF NOT EXISTS idx_cake_wishes_created ON cake_wishes(created_at DESC)`)
	// keep each user's first wish, drop spam duplicates, then lock it in
	softExec("dedupe cake_wishes", `DELETE FROM cake_wishes a USING cake_wishes b WHERE a.username = b.username AND a.id > b.id`)
	softExec("unique cake_wishes user", `CREATE UNIQUE INDEX IF NOT EXISTS uq_cake_wishes_user ON cake_wishes(username)`)
}

type cakeGuest struct {
	U string `json:"u"`
	T string `json:"t"`
	I string `json:"i"`
}
type cakeTop struct {
	U string `json:"u"`
	N int    `json:"n"`
	I string `json:"i"`
}
type cakeWish struct {
	U string `json:"u"`
	W string `json:"w"`
	T string `json:"t"`
	I string `json:"i"`
}
type cakeGlobal struct {
	Total  int         `json:"total"`
	Recent []cakeGuest `json:"recent"`
	Top    []cakeTop   `json:"top"`
	Wishes []cakeWish  `json:"wishes"`
}
type cakeSnap struct {
	Wait   int64
	Mine   int
	Points int
	Wished bool
	G      cakeGlobal
}

func cakeFix(g cakeGlobal) cakeGlobal {
	if g.Recent == nil {
		g.Recent = []cakeGuest{}
	}
	if g.Top == nil {
		g.Top = []cakeTop{}
	}
	if g.Wishes == nil {
		g.Wishes = []cakeWish{}
	}
	return g
}

// Data shared by everybody (totals, recent eaters, champions, wishes).
func queryCakeGlobal() cakeGlobal {
	g := cakeFix(cakeGlobal{})
	qctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	db.QueryRow(qctx, `SELECT COALESCE(SUM(total_slices),0)::int FROM cake_eaters`).Scan(&g.Total)

	if rows, err := db.Query(qctx, `
		SELECT e.username, e.last_eaten, COALESCE(u.profile_image,'')
		FROM cake_eaters e JOIN users u ON u.username = e.username
		ORDER BY e.last_eaten DESC LIMIT 8`); err == nil {
		for rows.Next() {
			var u, img string
			var t time.Time
			if rows.Scan(&u, &t, &img) == nil {
				g.Recent = append(g.Recent, cakeGuest{U: u, T: formatLastSeen(t), I: img})
			}
		}
		rows.Close()
	} else {
		log.Printf("cake recent error: %v", err)
	}

	if rows, err := db.Query(qctx, `
		SELECT e.username, e.total_slices, COALESCE(u.profile_image,'')
		FROM cake_eaters e JOIN users u ON u.username = e.username
		ORDER BY e.total_slices DESC, e.last_eaten ASC LIMIT 5`); err == nil {
		for rows.Next() {
			var t cakeTop
			if rows.Scan(&t.U, &t.N, &t.I) == nil {
				g.Top = append(g.Top, t)
			}
		}
		rows.Close()
	}

	if rows, err := db.Query(qctx, `
		SELECT w.username, w.wish, w.created_at, COALESCE(u.profile_image,'')
		FROM cake_wishes w JOIN users u ON u.username = w.username
		ORDER BY w.id DESC LIMIT 60`); err == nil {
		for rows.Next() {
			var w cakeWish
			var t time.Time
			if rows.Scan(&w.U, &w.W, &t, &w.I) == nil {
				w.T = formatLastSeen(t)
				g.Wishes = append(g.Wishes, w)
			}
		}
		rows.Close()
	}
	return g
}

// 5-second shared cache + singleflight so polling never hammers Postgres.
func cakeGlobalData() cakeGlobal {
	var g cakeGlobal
	if v, ok := CacheGet(cakeGlobalKey); ok && json.Unmarshal([]byte(v), &g) == nil {
		return cakeFix(g)
	}
	raw := sfGroup.do(cakeGlobalKey, func() string {
		ng := queryCakeGlobal()
		b, _ := json.Marshal(ng)
		CacheSet(cakeGlobalKey, string(b), 2*time.Second)
		return string(b)
	})
	g = cakeGlobal{}
	json.Unmarshal([]byte(raw), &g)
	return cakeFix(g)
}

func cakeSnapshot(user string, fresh bool) cakeSnap {
	var s cakeSnap
	if fresh {
		s.G = queryCakeGlobal()
		if b, err := json.Marshal(s.G); err == nil {
			CacheSet(cakeGlobalKey, string(b), 5*time.Second)
		}
	} else {
		s.G = cakeGlobalData()
	}

	qctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var elapsed float64
	var mine, pts int
	var wished bool
	err := db.QueryRow(qctx, `
		SELECT COALESCE(EXTRACT(EPOCH FROM (NOW()-e.last_eaten))::float8, 1e9),
		       COALESCE(e.total_slices,0), COALESCE(u.points,0),
		       EXISTS(SELECT 1 FROM cake_wishes w WHERE w.username = u.username)
		FROM users u LEFT JOIN cake_eaters e ON e.username = u.username
		WHERE u.username = $1`, user).Scan(&elapsed, &mine, &pts, &wished)
	if err == nil {
		s.Mine, s.Points, s.Wished = mine, pts, wished
		if w := int64(cakeCooldownSecs) - int64(elapsed); w > 0 {
			s.Wait = w
		}
	}
	return s
}

func (s cakeSnap) fields() fiber.Map {
	return fiber.Map{
		"wait_sec": s.Wait, "mine": s.Mine, "points": s.Points, "wished": s.Wished,
		"total": s.G.Total, "recent": s.G.Recent, "top": s.G.Top, "wishes": s.G.Wishes,
	}
}

func cakeReply(m fiber.Map, s cakeSnap) fiber.Map {
	for k, v := range s.fields() {
		m[k] = v
	}
	return m
}

// GET /birthday-cake/state (polled every 12s)
func birthdayCakeStateHandler(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	user := getCurrentUser(c)
	if user == "" {
		return c.Status(401).JSON(fiber.Map{"ok": false, "msg": "Pehle login karein"})
	}
	return c.JSON(cakeReply(fiber.Map{"ok": true, "open": cakeOpen()}, cakeSnapshot(user, false)))
}

// POST /birthday-cake/eat
// The 2-hour rule is ONE atomic statement: the row is inserted/updated only if
// last_eaten is older than the cooldown. Points are added in the same
// transaction, so a slice and its points can never get out of sync.
func birthdayCakeEatHandler(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	user := getCurrentUser(c)
	if user == "" {
		return c.Status(401).JSON(fiber.Map{"ok": false, "msg": "Pehle login karein"})
	}
	if !cakeOpen() {
		return c.JSON(fiber.Map{"ok": false, "msg": "🎂 Cake abhi band hai — birthday celebration ke dauran aayein"})
	}

	tctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	tx, err := db.Begin(tctx)
	if err != nil {
		log.Printf("cake eat begin error: %v", err)
		return c.JSON(fiber.Map{"ok": false, "msg": "Server busy hai — thori der baad try karein"})
	}
	defer tx.Rollback(context.Background())

	var mine int
	err = tx.QueryRow(tctx, `
		INSERT INTO cake_eaters (username, last_eaten, total_slices, total_points)
		VALUES ($1, NOW(), 1, $2)
		ON CONFLICT (username) DO UPDATE
		SET last_eaten   = NOW(),
		    total_slices = cake_eaters.total_slices + 1,
		    total_points = cake_eaters.total_points + $2
		WHERE cake_eaters.last_eaten <= NOW() - ($3 * INTERVAL '1 second')
		RETURNING total_slices`,
		user, cakeSlicePoints, cakeCooldownSecs).Scan(&mine)

	if errors.Is(err, pgx.ErrNoRows) {
		// Still inside the 2-hour window: nothing written, nothing granted.
		tx.Rollback(context.Background())
		return c.JSON(cakeReply(fiber.Map{
			"ok": false, "msg": "⏳ Aap ne abhi slice kha li hai — 2 ghante baad dobara aayein",
		}, cakeSnapshot(user, false)))
	}
	if err != nil {
		log.Printf("cake eat upsert error (%s): %v", user, err)
		return c.JSON(fiber.Map{"ok": false, "msg": "Slice nahi mil saki — dobara try karein"})
	}

	var pts int
	if err := tx.QueryRow(tctx,
		`UPDATE users SET points = LEAST(points::bigint + $1, 2000000000) WHERE username=$2 RETURNING points`,
		cakeSlicePoints, user).Scan(&pts); err != nil {
		log.Printf("cake eat points error (%s): %v", user, err)
		return c.JSON(fiber.Map{"ok": false, "msg": "Points add nahi ho sake — dobara try karein"})
	}
	if err := tx.Commit(tctx); err != nil {
		log.Printf("cake eat commit error (%s): %v", user, err)
		return c.JSON(fiber.Map{"ok": false, "msg": "Save nahi ho saka — dobara try karein"})
	}

	CacheDel(rankCacheKey, authorMetaCacheKey, cakeGlobalKey)
	return c.JSON(cakeReply(fiber.Map{"ok": true, "reward": cakeSlicePoints}, cakeSnapshot(user, true)))
}

// POST /birthday-cake/wish  (1 wish per 45s per user, max 80 chars)
func birthdayCakeWishHandler(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	user := getCurrentUser(c)
	if user == "" {
		return c.Status(401).JSON(fiber.Map{"ok": false, "msg": "Pehle login karein"})
	}
	if !cakeOpen() {
		return c.JSON(fiber.Map{"ok": false, "msg": "🎂 Cake abhi band hai"})
	}
	w := strings.Join(strings.Fields(c.FormValue("wish")), " ")
	r := []rune(w)
	if len(r) == 0 {
		return c.JSON(fiber.Map{"ok": false, "msg": "Pehle wish likhein ✍️"})
	}
	if len(r) > 80 {
		w = string(r[:80])
	}

	ictx, icancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer icancel()
	// The UNIQUE index makes this atomic: double-clicks / multiple tabs can't slip a 2nd wish in.
	tag, err := db.Exec(ictx,
		`INSERT INTO cake_wishes (username, wish) VALUES ($1, $2) ON CONFLICT (username) DO NOTHING`, user, w)
	if err != nil {
		log.Printf("cake wish insert error (%s): %v", user, err)
		return c.JSON(fiber.Map{"ok": false, "msg": "Wish save nahi ho saki"})
	}
	if tag.RowsAffected() == 0 {
		return c.JSON(cakeReply(fiber.Map{
			"ok": false, "msg": "💌 Aap apni wish pehle hi bhej chuke hain — har user sirf 1 wish bhej sakta hai",
		}, cakeSnapshot(user, false)))
	}
	CacheDel(cakeGlobalKey)
	return c.JSON(cakeReply(fiber.Map{"ok": true, "wish": w, "msg": "💖 Aapki wish Denz tak pahunch gayi!"}, cakeSnapshot(user, true)))
}

// GET /birthday-cake
func birthdayCakePage(c *fiber.Ctx) error {
	user := getCurrentUser(c)
	if user == "" {
		return c.Redirect("/login")
	}
	st := cakeSnapshot(user, false).fields()
	st["open"] = cakeOpen()
	st["cd"] = cakeCooldownSecs
	st["reward"] = cakeSlicePoints
	st["name"] = birthdayName
	st["popped"] = balloonsPoppedOf(user)
	st["me"] = user
	b, _ := json.Marshal(st) // Marshal escapes < > & so wishes can't break out of <script>

	page := strings.Replace(cakePageHTML, "__CAKE_CONTROLS__", cakeControlsHTML(), 1)
	page = strings.Replace(page, "__CAKE_SCRIPT__", cakeScript, 1)
	page = strings.Replace(page, "__CAKE_STATE__", string(b), 1)
	c.Set("Content-Type", "text/html")
	c.Set("Cache-Control", "no-store")
	return c.SendString(Layout("Denz Birthday Cake", page, user))
}

func cakeBtn(act, icon, iconCls, label string) string {
	return `<button type="button" data-act="` + act + `" style="display:inline-flex;align-items:center;gap:6px;padding:9px 13px;border-radius:999px;border:1px solid rgba(236,72,153,.4);background:rgba(236,72,153,.12);color:#db2777;font-size:12px;font-weight:800;cursor:pointer;-webkit-tap-highlight-color:transparent;touch-action:manipulation;">` +
		`<span class="` + iconCls + `" style="display:inline-block;font-size:16px;line-height:1;">` + icon + `</span><span id="cake-lbl-` + act + `">` + label + `</span></button>`
}

func cakeControlsHTML() string {
	return cakeBtn("blow", "💨", "animate-pulse", "Sab Bujhayein") +
		cakeBtn("mic", "🎤", "animate-pulse", "Phoonk Maro") +
		cakeBtn("relight", "🔥", "animate-bounce", "Dobara Jalayein") +
		cakeBtn("lights", "🌙", "animate-pulse", "Lights Off") +
		cakeBtn("spin", "🔄", "animate-pulse", "Auto-Ghumao: ON") +
		cakeBtn("tune", "🎵", "animate-bounce", "Birthday Tune") +
		cakeBtn("confetti", "🎊", "animate-bounce", "Confetti") +
		cakeBtn("balloons", "🎈", "animate-bounce", "Ghubbaray") +
		`<a href="/profile/` + birthdayUsername() + `" style="display:inline-flex;align-items:center;gap:6px;padding:9px 13px;border-radius:999px;border:1px solid rgba(245,158,11,.5);background:rgba(245,158,11,.14);color:#d97706;font-size:12px;font-weight:800;text-decoration:none;"><span class="animate-bounce" style="display:inline-block;font-size:16px;line-height:1;">🎁</span>Denz ko Wish Karein</a>`
}

const cakePageHTML = `
<div id="cake-page" style="max-width:820px;margin:0 auto;padding:10px 10px 30px;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;">

  <div style="text-align:center;padding:6px 0 10px;">
    <div class="animate-bounce" style="font-size:40px;line-height:1;">🎂</div>
    <h1 style="margin:6px 0 2px;font-weight:900;font-size:clamp(22px,6.4vw,34px);background-image:linear-gradient(90deg,#ec4899,#f59e0b,#8b5cf6,#ec4899);-webkit-background-clip:text;background-clip:text;color:transparent;">Denz ka Birthday Cake</h1>
    <p style="margin:0;font-size:12.5px;font-weight:700;opacity:.75;">Har 2 ghante mein 1 slice khayein aur <span style="color:#f59e0b;font-weight:900;">10,00,000 points</span> jeetein <span class="animate-pulse" style="display:inline-block;">🍰</span></p>
  </div>

  <div style="display:grid;grid-template-columns:repeat(3,1fr);gap:8px;margin-bottom:10px;">
    <div style="padding:9px 6px;border-radius:16px;text-align:center;background:rgba(128,128,128,.08);border:1px solid rgba(128,128,128,.2);"><div style="font-size:9.5px;font-weight:800;text-transform:uppercase;letter-spacing:.08em;opacity:.65;">🍰 Meri Slices</div><div id="cake-mine" style="font-size:20px;font-weight:900;color:#ec4899;">0</div></div>
    <div style="padding:9px 6px;border-radius:16px;text-align:center;background:rgba(128,128,128,.08);border:1px solid rgba(128,128,128,.2);"><div style="font-size:9.5px;font-weight:800;text-transform:uppercase;letter-spacing:.08em;opacity:.65;">🌍 Sab ki Slices</div><div id="cake-total" style="font-size:20px;font-weight:900;color:#8b5cf6;">0</div></div>
    <div style="padding:9px 6px;border-radius:16px;text-align:center;background:rgba(128,128,128,.08);border:1px solid rgba(128,128,128,.2);"><div style="font-size:9.5px;font-weight:800;text-transform:uppercase;letter-spacing:.08em;opacity:.65;">⭐ Mere Points</div><div id="cake-pts" style="font-size:20px;font-weight:900;color:#f59e0b;">0</div></div>
  </div>

  <div id="cake-stage" style="position:relative;width:100%;height:clamp(400px,118vw,560px);border-radius:26px;overflow:hidden;background:radial-gradient(circle at 50% 30%,#6d28d9 0,#3b0764 42%,#1e1b4b 76%,#0b1020 100%);box-shadow:0 22px 50px -18px rgba(124,58,237,.75),inset 0 0 0 1px rgba(255,255,255,.14);user-select:none;-webkit-user-select:none;-webkit-touch-callout:none;cursor:grab;">
    <div id="cake-load" style="position:absolute;left:0;top:0;right:0;bottom:0;z-index:5;display:flex;align-items:center;justify-content:center;text-align:center;padding:20px;color:#fff;font-size:13px;font-weight:800;pointer-events:none;"><span class="animate-pulse">🎂 3D cake load ho raha hai…</span></div>
    <span class="animate-pulse" style="position:absolute;left:8%;top:6%;font-size:18px;pointer-events:none;z-index:6;">✨</span>
    <span class="animate-pulse" style="position:absolute;left:86%;top:12%;font-size:14px;pointer-events:none;z-index:6;">✨</span>
    <span class="animate-bounce" style="position:absolute;left:4%;top:70%;font-size:24px;pointer-events:none;z-index:6;">🎉</span>
    <span class="animate-bounce" style="position:absolute;right:4%;top:74%;font-size:24px;pointer-events:none;z-index:6;">🎁</span>
    <div id="cake-dim" style="position:absolute;left:0;top:0;right:0;bottom:0;pointer-events:none;z-index:20;opacity:0;transition:opacity .8s;background:radial-gradient(circle at 50% 40%,rgba(0,0,0,0) 0,rgba(0,0,0,0) 22%,rgba(2,2,22,.88) 80%);"></div>
    <div id="cake-fx" style="position:absolute;left:0;top:0;right:0;bottom:0;pointer-events:none;z-index:30;overflow:hidden;"></div>
    <div style="position:absolute;left:10px;top:10px;z-index:25;padding:5px 10px;border-radius:999px;background:rgba(15,23,42,.6);color:#fff;font-size:11px;font-weight:800;pointer-events:none;">🎂 Cake <span id="cake-no">1</span> · 🍰 <span id="cake-left">12</span>/12 baqi</div>
    <div style="position:absolute;right:10px;top:10px;z-index:25;padding:5px 10px;border-radius:999px;background:rgba(15,23,42,.6);color:#fff;font-size:11px;font-weight:800;pointer-events:none;">🔥 <span id="cake-candles">0/0</span> candles</div>
    <div id="cake-pop" style="position:absolute;left:10px;bottom:34px;z-index:25;padding:4px 10px;border-radius:999px;background:rgba(15,23,42,.6);color:#fff;font-size:11px;font-weight:800;pointer-events:none;opacity:0;transition:opacity .3s;">🎈 Popped: 0</div>
    <div style="position:absolute;left:0;right:0;bottom:8px;z-index:25;text-align:center;color:rgba(255,255,255,.88);font-size:11px;font-weight:700;pointer-events:none;text-shadow:0 1px 3px rgba(0,0,0,.6);">👆 Drag = ghumao · Candle tap = bujhao · Ghubbaray tap = phorho 🎈</div>
  </div>

  <div id="cake-controls" style="display:flex;flex-wrap:wrap;gap:7px;justify-content:center;margin:12px 0 6px;">__CAKE_CONTROLS__</div>

  <div style="display:flex;flex-wrap:wrap;gap:6px;justify-content:center;align-items:center;margin-bottom:12px;">
    <span style="font-size:11px;font-weight:800;opacity:.6;text-transform:uppercase;letter-spacing:.08em;">🎨 Flavour</span>
    <span id="cake-flavs" style="display:inline-flex;flex-wrap:wrap;gap:6px;justify-content:center;">
      <button type="button" data-flav="strawberry" style="padding:7px 12px;border-radius:12px;border:1.5px solid #ec4899;background:rgba(236,72,153,.14);color:inherit;font-size:12px;font-weight:800;cursor:pointer;">🍓 Strawberry</button>
      <button type="button" data-flav="choco" style="padding:7px 12px;border-radius:12px;border:1.5px solid rgba(128,128,128,.3);background:rgba(128,128,128,.08);color:inherit;font-size:12px;font-weight:800;cursor:pointer;">🍫 Chocolate</button>
      <button type="button" data-flav="vanilla" style="padding:7px 12px;border-radius:12px;border:1.5px solid rgba(128,128,128,.3);background:rgba(128,128,128,.08);color:inherit;font-size:12px;font-weight:800;cursor:pointer;">🍦 Vanilla</button>
      <button type="button" data-flav="blueberry" style="padding:7px 12px;border-radius:12px;border:1.5px solid rgba(128,128,128,.3);background:rgba(128,128,128,.08);color:inherit;font-size:12px;font-weight:800;cursor:pointer;">🫐 Blueberry</button>
    </span>
  </div>

  <button id="cake-cta" type="button" hx-post="/birthday-cake/eat" hx-target="#cake-null" hx-swap="none"
    style="display:flex;align-items:center;justify-content:center;gap:10px;width:100%;min-height:60px;padding:12px 18px;border:none;border-radius:20px;color:#fff;font-size:clamp(15px,4.4vw,19px);font-weight:900;cursor:pointer;background-image:linear-gradient(100deg,#ec4899,#f59e0b);box-shadow:0 12px 26px -10px rgba(236,72,153,.85);-webkit-tap-highlight-color:transparent;touch-action:manipulation;">
    <span class="animate-bounce" style="display:inline-block;font-size:28px;line-height:1;">🍰</span>
    <span id="cake-cta-txt">Slice Khayein</span>
  </button>

  <div id="cake-msg" style="margin-top:10px;padding:11px 14px;border-radius:16px;font-size:13px;font-weight:800;text-align:center;background:rgba(128,128,128,.08);border:1px solid rgba(128,128,128,.2);">🎂 Cake ko ghumayein, candles bujhayein, aur slice khayein!</div>

  <div style="margin-top:14px;padding:12px;border-radius:20px;background:rgba(128,128,128,.06);border:1px solid rgba(128,128,128,.18);">
    <div style="font-size:11px;font-weight:900;text-transform:uppercase;letter-spacing:.14em;opacity:.65;margin-bottom:8px;"><span class="animate-bounce" style="display:inline-block;">🏮</span> Denz ke liye Wish likhein</div>
    <form id="cake-wish-form" hx-post="/birthday-cake/wish" hx-target="#cake-null" hx-swap="none" style="display:flex;gap:8px;margin:0;">
      <input id="cake-wish-in" name="wish" type="text" maxlength="80" required autocomplete="off" placeholder="Happy Birthday Denz! ..." style="flex:1;min-width:0;padding:11px 14px;border-radius:14px;border:1.5px solid rgba(236,72,153,.4);background:rgba(128,128,128,.08);color:inherit;font-size:13px;font-weight:600;outline:none;">
      <button type="submit" style="flex-shrink:0;padding:11px 16px;border:none;border-radius:14px;color:#fff;font-size:13px;font-weight:900;cursor:pointer;background-image:linear-gradient(100deg,#8b5cf6,#ec4899);">🏮 Bhejein</button>
    </form>
    <div id="cake-wish-done" style="display:none;padding:11px 14px;border-radius:14px;font-size:12.5px;font-weight:800;text-align:center;color:#be185d;background:rgba(236,72,153,.12);border:1px solid rgba(236,72,153,.35);">✅ Aapki wish bhej di gayi hai — shukriya! 💖</div>
    <div style="display:flex;align-items:center;justify-content:space-between;margin:12px 2px 6px;font-size:11px;font-weight:800;opacity:.7;"><span>💌 Sab ki wishes</span><span id="cake-wish-count" style="padding:2px 9px;border-radius:999px;background:rgba(236,72,153,.15);color:#db2777;">0</span></div>
    <div id="cake-wishes" style="display:none;"></div>
    <div id="cake-wall" style="display:flex;flex-direction:column;gap:7px;max-height:280px;overflow-y:auto;overscroll-behavior:contain;padding-right:4px;scrollbar-width:thin;scrollbar-color:#ec4899 transparent;-webkit-overflow-scrolling:touch;"></div>
  </div>

  <div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(250px,1fr));gap:10px;margin-top:12px;">
    <div style="padding:12px;border-radius:20px;background:rgba(128,128,128,.06);border:1px solid rgba(128,128,128,.18);">
      <div style="font-size:11px;font-weight:900;text-transform:uppercase;letter-spacing:.14em;opacity:.65;margin-bottom:8px;"><span class="animate-pulse" style="display:inline-block;">🏆</span> Cake Champions</div>
      <div id="cake-top" style="display:flex;flex-direction:column;gap:6px;"></div>
    </div>
    <div style="padding:12px;border-radius:20px;background:rgba(128,128,128,.06);border:1px solid rgba(128,128,128,.18);">
      <div style="font-size:11px;font-weight:900;text-transform:uppercase;letter-spacing:.14em;opacity:.65;margin-bottom:8px;"><span class="animate-pulse" style="display:inline-block;">🍴</span> Party ke Mehmaan</div>
      <div id="cake-guests" style="display:flex;flex-direction:column;gap:6px;"></div>
    </div>
  </div>

  <div style="margin-top:12px;font-size:11.5px;font-weight:600;line-height:1.7;opacity:.7;text-align:center;">
    💡 Candle tap = bujhao/jalao · 🎤 Phoonk Maro = mic mein asli phoonk · 🌙 Lights off karke candles ki roshni dekhein · Har user ko 2 ghante mein sirf 1 slice milti hai.
  </div>
</div>
<div id="cake-null" style="display:none;"></div>
<script>window.__CAKE=__CAKE_STATE__;</script>
<script>__CAKE_SCRIPT__</script>
<script>
(function(){
 var wall=document.getElementById('cake-wall');
 if(!wall)return;
 // >>> Change this if your avatar route is different <
 var AVATAR=function(u){return 'https://ui-avatars.com/api/?name='+encodeURIComponent(u)+'&background=random';};
 var me=(window.__CAKE&&window.__CAKE.me)||'';
 var form=document.getElementById('cake-wish-form');
 var done=document.getElementById('cake-wish-done');
 var inp=document.getElementById('cake-wish-in');
 var cnt=document.getElementById('cake-wish-count');
 var btn=form?form.querySelector('button[type=submit]'):null;
 var sig='';

 function hue(s){var h=0;for(var i=0;i<s.length;i++)h=(h*31+s.charCodeAt(i))%360;return h;}
 function avatar(u,img){
  var w=document.createElement('span');
  w.style.cssText='position:relative;flex-shrink:0;width:36px;height:36px;border-radius:50%;overflow:hidden;display:inline-flex;align-items:center;justify-content:center;color:#fff;font-size:15px;font-weight:900;background:hsl('+hue(u)+',65%,52%);box-shadow:0 0 0 2px rgba(236,72,153,.4);';
  w.textContent=(u||'?').charAt(0).toUpperCase();
  var im=new Image();
  im.alt='';im.loading='lazy';im.decoding='async';
  im.style.cssText='position:absolute;left:0;top:0;width:100%;height:100%;object-fit:cover;';
  im.onerror=function(){im.remove();};
  im.src=img||AVATAR(u);
  w.appendChild(im);
  return w;
 }
 function render(list){
  list=list||[];
  var s=JSON.stringify(list);
  if(s===sig)return;
  sig=s;
  var keep=wall.scrollTop;
  wall.textContent='';
  if(cnt)cnt.textContent=list.length;
  if(!list.length){
   var e=document.createElement('div');
   e.style.cssText='padding:14px;text-align:center;font-size:12px;font-weight:700;opacity:.6;';
   e.textContent='Abhi koi wish nahi — sab se pehle aap likhein 🎈';
   wall.appendChild(e);return;
  }
  list.forEach(function(x){
   var mine=x.u===me;
   var row=document.createElement('div');
   row.style.cssText='display:flex;gap:10px;align-items:flex-start;padding:9px 11px;border-radius:14px;background:'+(mine?'rgba(236,72,153,.13)':'rgba(128,128,128,.08)')+';border:1px solid '+(mine?'rgba(236,72,153,.45)':'rgba(128,128,128,.18)')+';';
   row.appendChild(avatar(x.u,x.i));
   var body=document.createElement('div');body.style.cssText='min-width:0;flex:1;';
   var head=document.createElement('div');head.style.cssText='display:flex;align-items:baseline;gap:6px;flex-wrap:wrap;';
   var a=document.createElement('a');
   a.href='/profile/'+encodeURIComponent(x.u);
   a.textContent=x.u+(mine?' (aap)':'');
   a.style.cssText='font-size:12.5px;font-weight:900;color:#db2777;text-decoration:none;';
   var t=document.createElement('span');
   t.textContent=x.t||'';t.style.cssText='font-size:10px;font-weight:700;opacity:.55;';
   head.appendChild(a);head.appendChild(t);
   var m=document.createElement('div');
   m.textContent=x.w;
   m.style.cssText='margin-top:2px;font-size:13px;font-weight:600;line-height:1.45;word-break:break-word;overflow-wrap:anywhere;';
   body.appendChild(head);body.appendChild(m);row.appendChild(body);
   wall.appendChild(row);
  });
  wall.scrollTop=keep;
 }

 var note=document.createElement('div');
 note.style.cssText='font-size:10.5px;font-weight:700;opacity:.6;text-align:right;margin-top:5px;';
 note.textContent='Sirf 1 wish milti hai · 0/80';
 if(form&&form.parentNode)form.parentNode.insertBefore(note,form.nextSibling);
 if(inp)inp.addEventListener('input',function(){note.textContent='Sirf 1 wish milti hai · '+inp.value.length+'/80';});

 var wished=false;
 function setWished(v){
  wished=v;
  if(form)form.style.display=v?'none':'flex';
  note.style.display=v?'none':'block';
  if(done)done.style.display=v?'block':'none';
 }
 var lsig='';
 function lrow(u,img,right){
  var r=document.createElement('div');
  r.style.cssText='display:flex;align-items:center;gap:8px;padding:6px 8px;border-radius:12px;background:rgba(128,128,128,.08);';
  var av=avatar(u,img);av.style.width='28px';av.style.height='28px';av.style.fontSize='12px';
  var a=document.createElement('a');a.href='/profile/'+encodeURIComponent(u);a.textContent=u;
  a.style.cssText='flex:1;min-width:0;font-size:12.5px;font-weight:800;color:inherit;text-decoration:none;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;';
  var s=document.createElement('span');s.textContent=right;
  s.style.cssText='font-size:11px;font-weight:800;opacity:.75;flex-shrink:0;';
  r.appendChild(av);r.appendChild(a);r.appendChild(s);return r;
 }
 function renderLists(d){
  var sg=JSON.stringify([d.top,d.recent]);
  if(sg===lsig)return;lsig=sg;
  var t=document.getElementById('cake-top'),g=document.getElementById('cake-guests');
  var medals=['🥇','🥈','🥉'];
  if(t){t.textContent='';
   (d.top||[]).forEach(function(x,i){t.appendChild(lrow(x.u,x.i,(medals[i]||'#'+(i+1))+' 🍰 '+x.n));});
   if(!(d.top||[]).length)t.textContent='Abhi koi nahi 🍰';}
  if(g){g.textContent='';
   (d.recent||[]).forEach(function(x){g.appendChild(lrow(x.u,x.i,x.t||''));});
   if(!(d.recent||[]).length)g.textContent='Abhi koi nahi 🍴';}
 }
 function renderStats(d){
  var m=document.getElementById('cake-mine'),t=document.getElementById('cake-total'),p=document.getElementById('cake-pts');
  if(m&&d.mine!=null)m.textContent=d.mine;
  if(t&&d.total!=null)t.textContent=d.total;
  if(p&&d.points!=null)p.textContent=Number(d.points).toLocaleString();
 }
 setTimeout(function(){
  var l=document.getElementById('cake-load');
  if(l&&l.style.display!=='none'&&l.offsetParent!==null){
   l.innerHTML='<span>🎂 3D cake load nahi ho saka — baqi sab chal raha hai, slice khayein!</span>';
  }
 },6000);
 function apply(d){
  if(!d)return;
  renderStats(d);renderLists(d);
  if(d.wishes)render(d.wishes);
  if(typeof d.wished==='boolean')setWished(d.wished);
 }

 if(form){
  form.addEventListener('htmx:beforeRequest',function(){if(btn){btn.disabled=true;btn.style.opacity='.6';}});
  form.addEventListener('htmx:afterRequest',function(){if(btn&&!wished){btn.disabled=false;btn.style.opacity='1';}});
 }
 function onReq(e){
  if(!wall.isConnected){document.removeEventListener('htmx:afterRequest',onReq);return;}
  var x=e.detail&&e.detail.xhr;
  var p=(e.detail&&e.detail.pathInfo&&e.detail.pathInfo.requestPath)||'';
  if(!x||p.indexOf('/birthday-cake')!==0)return;
  try{apply(JSON.parse(x.responseText));}catch(_){}
 }
 document.addEventListener('htmx:afterRequest',onReq);

 apply(window.__CAKE);
 var iv=setInterval(function(){
  if(!wall.isConnected){clearInterval(iv);return;}
  if(document.hidden)return;
  fetch('/birthday-cake/state',{credentials:'same-origin',cache:'no-store'})
   .then(function(r){return r.json();}).then(apply).catch(function(){});
 },15000);
})();
</script>
`

func balloonsPoppedOf(user string) int {
	qctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var n int
	db.QueryRow(qctx, `SELECT balloons_popped FROM bday_stats WHERE username=$1`, user).Scan(&n)
	return n
}

// POST /birthday/pop  (batched from the browser every ~4s)
func birthdayPopHandler(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	user := getCurrentUser(c)
	if user == "" {
		return c.Status(401).JSON(fiber.Map{"ok": false})
	}
	if !cakeOpen() {
		return c.JSON(fiber.Map{"ok": false})
	}
	n, _ := strconv.Atoi(c.FormValue("n"))
	if n < 1 {
		return c.JSON(fiber.Map{"ok": false})
	}
	if n > 40 {
		n = 40
	}
	ictx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var total int
	if err := db.QueryRow(ictx, `
		INSERT INTO bday_stats (username, balloons_popped) VALUES ($1, $2)
		ON CONFLICT (username) DO UPDATE
		SET balloons_popped = bday_stats.balloons_popped + $2, updated_at = NOW()
		RETURNING balloons_popped`, user, n).Scan(&total); err != nil {
		log.Printf("bday pop error (%s): %v", user, err)
		return c.Status(500).JSON(fiber.Map{"ok": false})
	}
	CacheDel("lb_section:balloons:" + user)
	return c.JSON(fiber.Map{"ok": true, "total": total})
}

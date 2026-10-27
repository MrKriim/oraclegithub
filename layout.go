package main

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

var bottomMenu string

func GetMobileBadge(count int) string {
	if count <= 0 {
		return ""
	}
	// This returns ONLY the red circle. No bell.
	return fmt.Sprintf(`
        <span class="bg-red-500 text-white text-[10px] w-5 h-5 flex items-center justify-center rounded-full font-bold animate-pulse shadow-sm border border-white dark:border-gray-900">
            %d
        </span>`, count)
}

// GetDecoratedUsername returns the HTML for a username with stars and shimmer if they are top tier.
// Previously ran a live, uncached full-table RANK() OVER() sort on EVERY
// call — meaning every single post rendered in a feed triggered its own
// full-table sort, independent of the authorMeta cache added elsewhere.
// This was very likely the still-remaining source of pool-exhaustion
// crashes even after the renderPostHTML fix, since this function sits on
// the same hot path but was never wired to the shared cache.
func GetDecoratedUsername(username string) string {
	meta := getCachedAuthorMeta(username)
	userType := meta.UserType
	rank := int(meta.Rank)
	err := error(nil)
	if userType == "" {
		err = fmt.Errorf("not found in cache")
	}

	nameClasses := "font-black hover:underline truncate "
	nameStyles := ""
	starHTML := ""

	if userType == "admin" {
		// ROYAL PURPLE - Added -webkit prefixes for visibility
		nameClasses += "text-purple-600 " // Fallback color
		nameStyles = `
            background-image: linear-gradient(to right, #9333ea, #db2777, #9333ea);
            -webkit-background-clip: text;
            background-clip: text;
            color: transparent;
            background-size: 200% auto;
        
            text-shadow: 0 0 10px rgba(168,85,247,0.2);`
		starHTML = ``

	} else if err == nil && rank >= 1 && rank <= 3 {
		if rank == 1 {
			// GOLD
			nameClasses += "text-amber-600 " // Fallback
			nameStyles = `
                background-image: linear-gradient(to right, #b8860b, #f1c40f, #b8860b);
                -webkit-background-clip: text;
                background-clip: text;
                color: transparent;
                background-size: 200% auto;
                animation: shimmer 2s linear infinite;`
			starHTML = `<span class="shrink-0 animate-bounce inline-block"></span>`

		} else if rank == 2 {
			// TIGER (Rank 2) - Fixed visibility
			nameClasses += "text-orange-600 " // Fallback
			nameStyles = `
                background-image: linear-gradient(to right, #ea580c, #fb923c, #ea580c);
                -webkit-background-clip: text;
                background-clip: text;
                color: transparent;
                background-size: 200% auto;
                animation: shimmer 2.5s linear infinite;`
			starHTML = `<span class="shrink-0 animate-pulse inline-block"></span>`

		} else if rank == 3 {
			// BRONZE
			nameClasses += "text-amber-800 " // Fallback
			nameStyles = `
                background-image: linear-gradient(to right, #a0522d, #e67e22, #a0522d);
                -webkit-background-clip: text;
                background-clip: text;
                color: transparent;
                background-size: 200% auto;
                animation: shimmer 4s linear infinite;`
			starHTML = `<span class="shrink-0 inline-block"></span>`
		}
	} else {
		nameClasses += "text-gray-900 dark:text-white "
	}

	return fmt.Sprintf(`
        <a href="/profile/%s" class="inline-flex items-center gap-1 min-w-0 max-w-full hover:no-underline group">
            <span class="%s" style="%s">%s</span>
            %s
        </a>`, username, nameClasses, nameStyles, username, starHTML)
}

// getCachedPendingCount reflects the REAL number of account_status='pending'
// rows, cached briefly. Previously this badge trusted a hand-incremented/
// hand-reset Redis counter maintained separately from the actual DB rows
// — any crash, timeout, or missed code path let that counter drift from
// reality permanently (a notification with no matching pending account
// behind it). Deriving it from a real COUNT query, refreshed every few
// seconds, makes it self-healing: it can never show a stale number for
// longer than the cache TTL, no matter what caused the drift.
const pendingCountCacheKey = "pending_count_live"
const pendingCountCacheTTL = 10 * time.Second

func getCachedPendingCount() int {
	if val, ok := CacheGet(pendingCountCacheKey); ok {
		if n, err := strconv.Atoi(val); err == nil {
			return n
		}
	}
	var n int
	dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := db.QueryRow(dbCtx, "SELECT COUNT(*) FROM users WHERE account_status='pending'").Scan(&n); err != nil {
		return 0
	}
	CacheSet(pendingCountCacheKey, strconv.Itoa(n), pendingCountCacheTTL)
	return n
}
func layoutBase(title, body, currentUser string) string {
	bottomMenu = ""
	count := getNotificationCount(currentUser)

	// Admin pending button — only visible to admins
	adminPendingBtn := ""
	if currentUser != "" {
		var utype string
		dbCtx, dbCancel := context.WithTimeout(ctx, 1*time.Second)
		defer dbCancel()
		db.QueryRow(dbCtx, "SELECT user_type FROM users WHERE username=$1", currentUser).Scan(&utype)
		if utype == "admin" {
			pendingCount := getCachedPendingCount()
			badge := ""
			if pendingCount > 0 {
				badge = fmt.Sprintf(`<span class="absolute -top-1 -right-1 bg-red-500 text-white text-[9px] w-4 h-4 flex items-center justify-center rounded-full font-black animate-pulse">%d</span>`, pendingCount)
			}
			adminPendingBtn = fmt.Sprintf(`
        <a href="/admin/dashboard" hx-boost="true" class="relative flex flex-col items-center justify-center w-[58px] h-full py-2 text-red-400 hover:bg-white/5 transition-all duration-300 font-bold group border-x border-transparent hover:border-white/10">
            %s
            <svg class="w-5 h-5 group-hover:scale-110 transition-transform" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2.5">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 3l7 3.5v5c0 4.5-3 8-7 9-4-1-7-4.5-7-9v-5L12 3z"/>
            </svg>
            <span class="leading-none mt-1 text-[9px]">Admin</span>
        </a>`, badge)
		}
	}

	darkToggle := `
        <button onclick="toggleDarkMode()" 
                class="w-9 h-9 flex items-center justify-center text-gray-500 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 rounded-2xl transition shrink-0"
                aria-label="Toggle dark mode">
            <span id="theme-icon" class="text-xl">🌙</span>
        </button>`

	nav := ""
	if currentUser != "" {

		bottomMenu = fmt.Sprintf(`
        <script>
    // Global flag jo track karega ke koi video chal rahi hai ya nahi
    window.isMediaPlaying = false;

    // 1. Native HTML5 Videos ko track karne ke liye (Direct MP4/WebM links)
    document.addEventListener('play', function(e) {
        if (e.target.tagName === 'VIDEO') {
            window.isMediaPlaying = true;
        }
    }, true);

    document.addEventListener('pause', function(e) {
        if (e.target.tagName === 'VIDEO') {
            checkAllMedia();
        }
    }, true);

    document.addEventListener('ended', function(e) {
        if (e.target.tagName === 'VIDEO') {
            checkAllMedia();
        }
    }, true);

    // 2. YouTube Videos ko track karne ke liye (Using your enablejsapi=1)
    window.addEventListener('message', function(e) {
        try {
            // YouTube postMessage API se data parse karein
            let data = JSON.parse(e.data);
            
            if (data.event === 'infoDelivery' && data.info && data.info.playerState !== undefined) {
                let state = data.info.playerState;
                
                if (state === 1) {
                    // 1 ka matlab hai: Video IS PLAYING
                    window.isMediaPlaying = true;
                } else if (state === 2 || state === 0) {
                    // 2 = Paused, 0 = Ended
                    checkAllMedia();
                }
            }
        } catch (err) {
            // Kisi aur message par error skip karne ke liye
        }
    });

    // Ek safety function jo check karega ke waqai saare native videos paused hain
    function checkAllMedia() {
        let nativeVideos = document.querySelectorAll('video');
        let anyPlaying = false;
        
        nativeVideos.forEach(v => {
            if (!v.paused && !v.ended) {
                anyPlaying = true;
            }
        });
        
        // Agar native video bhi nahi chal rahi, toh flag false kar do
        if (!anyPlaying) {
            window.isMediaPlaying = false;
        }
    }

    // 3. HTMX Interception (Asal jadu yahan hai)
    document.body.addEventListener('htmx:beforeRequest', function(evt) {
        // Agar koi video chal rahi hai, aur request auto-polling ('every') ki wajah se ho rahi hai
        if (window.isMediaPlaying && evt.detail.trigger && evt.detail.trigger.startsWith('every')) {
            // HTMX request ko yahin zabardasti cancel (stop) kar do!
            evt.preventDefault();
            console.log("🚫 HTMX Refresh blocked because media is playing...");
        }
    });
</script>
<div class="fixed left-0 right-0 z-[1000] w-full bg-gradient-to-r from-slate-900 via-indigo-950 to-slate-900 border-t border-white/10 pb-safe shadow-[0_-10px_40px_-10px_rgba(0,0,0,0.5)]" 
     style="bottom: 0 !important; top: auto !important; position: fixed !important;">
     <style>
    @keyframes shimmer {
        0%% { background-position: 0%% center; }
        100%% { background-position: 200%% center; }
    }
</style>
    <div class="flex items-center justify-around h-16 max-w-2xl mx-auto px-2">



 
        <a href="/" hx-boost="true" class="flex flex-col items-center justify-center w-full h-full text-amber-400 hover:bg-white/5 transition-all duration-300 group border-x border-transparent hover:border-white/10">
            <svg class="group-hover:scale-110 transition-transform" width="20px" height="20px" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                <path d="M9.00195 22.001V14.001H15.002V22.001M19.002 9.171V20.001C19.002 20.5314 18.7913 21.0401 18.4162 21.4152C18.0411 21.7903 17.5324 22.001 17.002 22.001H7.00195C6.47152 22.001 5.96281 21.7903 5.58774 21.4152C5.21267 21.0401 5.00195 20.5314 5.00195 20.001V9.171M19.002 9.171L13.602 4.151C13.1613 3.7424 12.5915 3.51465 12.002 3.51465C11.4124 3.51465 10.8426 3.7424 10.402 4.151L5.00195 9.171M19.002 9.171V5.001H16.002V6.381" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            <span class="text-[10px] font-bold uppercase mt-1">Home</span>
        </a>
               <a href="/earn-points" hx-boost="true" class="flex flex-col items-center justify-center w-full h-full text-amber-400 hover:bg-white/5 transition-all duration-300 group border-x border-transparent hover:border-white/10">
    <svg class="group-hover:scale-110 transition-transform" width="20px" height="20px" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
        <path stroke-linecap="round" stroke-linejoin="round" d="M12 6v12m-3-2.818.879.113M15 7.182l-.879-.113M15 11.454l-6 .727M15 10l-6 .727M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z" />
    </svg>
    <span class="text-[10px] font-bold uppercase mt-1">Points+</span>
</a>
        

        <a href="/share" hx-boost="true" class="flex flex-col items-center justify-center w-full h-full text-amber-400 hover:bg-white/5 transition-all duration-300 group border-x border-transparent hover:border-white/10">
    <svg class="group-hover:scale-110 transition-transform" width="20px" height="20px" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
        <path d="M12 5V19M5 12H19" stroke-linecap="round" stroke-linejoin="round"/>
    </svg>
    <span class="text-[10px] font-bold uppercase mt-1">Share</span>
</a>

        <a href="/notifications" hx-boost="true" class="relative flex flex-col items-center justify-center w-full h-full text-amber-400 hover:bg-white/5 transition-all duration-300 group border-x border-transparent hover:border-white/10">
            <div id="inbox-badge-bottom" 
                 hx-get="/notifications/count" 
                 hx-trigger="every 1s" 
                 hx-swap="innerHTML"
                 hx-disinherit="*"
                 class="absolute -top-1 right-3 z-10 pointer-events-none">
                %s 
            </div>
            <svg class="group-hover:scale-110 transition-transform" width="20px" height="20px" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                <path d="M17 20.5H7C4 20.5 2 19 2 15.5V8.5C2 5 4 3.5 7 3.5H17C20 3.5 22 5 22 8.5V15.5C22 19 20 20.5 17 20.5Z" stroke-linecap="round" stroke-linejoin="round"/>
                <path d="M17 9L13.87 11.5C12.84 12.32 11.15 12.32 10.12 11.5L7 9" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            <span class="text-[10px] font-bold uppercase mt-1">Inbox</span>
        </a>

        <a href="/invites" hx-boost="true" class="relative flex flex-col items-center justify-center w-full h-full text-amber-400 hover:bg-white/5 transition-all duration-300 group border-x border-transparent hover:border-white/10">
            <div id="invite-badge-bottom" 
                 hx-get="/invites/count" 
                 hx-trigger="load, every 10s" 
                 hx-swap="innerHTML"
                 class="absolute -top-1 right-3 z-10 pointer-events-none">
            </div>
            <svg class="group-hover:scale-110 transition-transform" width="20px" height="20px" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                <path d="M8.5 19H15.5C18.28 19 20.5 16.78 20.5 14V8C20.5 5.22 18.28 3 15.5 3H8.5C5.72 3 3.5 5.22 3.5 8V14C3.5 16.78 5.72 19 8.5 19Z" stroke-linecap="round" stroke-linejoin="round"/>
                <path d="M7 8H17M7 13H13" stroke-linecap="round"/>
            </svg>
            <span class="text-[10px] font-bold uppercase mt-1 whitespace-nowrap">1 on 1</span>
        </a>
        <style>
        :root {
    --post-bg: #ffffff;
    --post-border: #e5e7eb;
    --post-text: #111827;
}

.dark {
    --post-bg: #111827; /* Tailwind gray-900 */
    --post-border: #1f2937; /* Tailwind gray-800 */
    --post-text: #f9fafb; /* Tailwind gray-50 */
}
    </style>

  
        <a href="/settings" hx-boost="true" class="flex flex-col items-center justify-center w-full h-full text-amber-400 hover:bg-white/5 transition-all duration-300 group border-x border-transparent hover:border-white/10">
            <svg class="group-hover:scale-110 transition-transform" width="20px" height="20px" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                <circle cx="5" cy="12" r="2"/>
                <circle cx="12" cy="12" r="2"/>
                <circle cx="19" cy="12" r="2"/>
            </svg>
            <span class="text-[10px] font-bold uppercase mt-1">More</span>
        </a>
        %s
    </div>
</div>`, GetMobileBadge(count), adminPendingBtn)

		nav = fmt.Sprintf(`
<nav class="w-full flex items-stretch justify-between bg-gradient-to-r from-slate-900 via-indigo-950 to-slate-900 border-b border-white/10 shadow-2xl sticky top-0 z-50">
    <div class="flex items-center gap-3 pl-4 py-2 shrink-0">
        <a href="/" class="font-black text-2xl tracking-tighter text-transparent bg-clip-text bg-gradient-to-br from-blue-400 to-emerald-400" hx-boost="true">
            Dostana
        </a>
        <div class="flex items-center justify-center w-8 h-8">
            %s
        </div>
    </div>

    <div class="flex items-center gap-2 pr-4 text-[10px] sm:text-[11px]">
        <a href="/profile" class="flex flex-col items-center justify-center w-[58px] h-full py-2 text-amber-400 hover:bg-white/5 transition-all duration-300 font-bold group border-x border-transparent hover:border-white/10" hx-boost="true">
            <svg class="w-5 h-5 group-hover:scale-110 transition-transform" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2.5">
                <path d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z" />
            </svg>
            <span class="leading-none mt-1">Profile</span>
        </a>

        <a href="/categories" class="flex flex-col items-center justify-center w-[58px] h-full py-2 text-amber-400 hover:bg-white/5 transition-all duration-300 font-bold group border-x border-transparent hover:border-white/10" hx-boost="true">
        <svg class="w-5 h-5 group-hover:scale-110 transition-transform" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2.5">
            <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 6A2.25 2.25 0 0 1 6 3.75h2.25A2.25 2.25 0 0 1 10.5 6v2.25a2.25 2.25 0 0 1-2.25 2.25H6a2.25 2.25 0 0 1-2.25-2.25V6ZM3.75 15.75A2.25 2.25 0 0 1 6 13.5h2.25a2.25 2.25 0 0 1 2.25 2.25V18a2.25 2.25 0 0 1-2.25 2.25H6A2.25 2.25 0 0 1 3.75 18v-2.25ZM13.5 6a2.25 2.25 0 0 1 2.25-2.25H18A2.25 2.25 0 0 1 20.25 6v2.25A2.25 2.25 0 0 1 18 10.5h-2.25a2.25 2.25 0 0 1-2.25-2.25V6ZM13.5 15.75a2.25 2.25 0 0 1 2.25-2.25H18a2.25 2.25 0 0 1 2.25 2.25V18A2.25 2.25 0 0 1 18 20.25h-2.25A2.25 2.25 0 0 1 13.5 18v-2.25Z" />
        </svg>
        <span class="leading-none mt-1">Categories</span>
    </a>
<a href="/leaderboard" class="flex flex-col items-center justify-center w-[58px] h-full py-2 text-amber-400 hover:bg-white/5 transition-all duration-300 font-bold group border-x border-transparent hover:border-white/10" hx-boost="true">
            <span class="text-lg group-hover:rotate-12 transition-transform inline-block leading-none">🏆</span>
            <span class="leading-none mt-1">Leaderboard</span>
        </a>
    </div>
</nav>`, darkToggle)
	} else {
		nav = fmt.Sprintf(`
<nav class="w-full flex items-stretch justify-between bg-gradient-to-r from-slate-900 via-indigo-950 to-slate-900 border-b border-white/10 shadow-2xl sticky top-0 z-50">
    <div class="flex items-center gap-3 pl-4 py-2 shrink-0">
        <a href="/" class="font-black text-2xl tracking-tighter text-transparent bg-clip-text bg-gradient-to-br from-blue-400 to-emerald-400" hx-boost="true">
            Dostana
        </a>
        <div class="flex items-center justify-center w-8 h-8">
            %s
        </div>
    </div>

    <div class="flex items-center pr-4 text-[10px] sm:text-[11px]">
        <a href="/rules" class="flex flex-col items-center justify-center w-[65px] h-full py-2 text-amber-400 hover:bg-white/5 transition-all duration-300 font-bold group border-x border-transparent hover:border-white/10" hx-boost="true">
            <svg class="w-5 h-5 group-hover:scale-110 transition-transform" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2.5">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
            </svg>
            <span class="leading-none mt-1">Rules</span>
        </a>
        <a href="/login" class="flex flex-col items-center justify-center w-[65px] h-full py-2 text-blue-400 hover:bg-white/5 transition-all duration-300 font-bold group border-x border-transparent hover:border-white/10" hx-boost="true">
            <svg class="w-5 h-5 group-hover:scale-110 transition-transform" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2.5">
                <path d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" />
            </svg>
            <span class="leading-none mt-1">Login</span>
        </a>

        <a href="/signup" class="flex flex-col items-center justify-center w-[65px] h-full py-2 text-emerald-400 hover:bg-white/5 transition-all duration-300 font-bold group border-r border-transparent hover:border-white/10" hx-boost="true">
            <svg class="w-5 h-5 group-hover:scale-110 transition-transform" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2.5">
                <path d="M18 9v3m0 0v3m0-3h3m-3 0h-3m-2-5a4 4 0 11-8 0 4 4 0 018 0zM3 20a6 6 0 0112 0v1H3v-1z" />
            </svg>
            <span class="leading-none mt-1">Sign Up</span>
        </a>
    </div>
</nav>`, darkToggle)
	}

	return fmt.Sprintf(`
        <!DOCTYPE html>
        <html lang="en" class="h-full">
        <head>
            <meta charset="UTF-8">
            <meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=5.0, user-scalable=yes">
            <title>%s | Dostana - The #1 Damadampk Alternative</title>

            <meta name="description" content="Miss the old damadampk? Dostana is the ultimate Pakistani social network and perfect alternative to Damadam. Discover trending Mehfils, safe Urdu chat, gamification, and ranks. Created by Kriim. Tamam dost damadampk ke apko Dostana pe milenge! Join Pakistan's best community today.">
            <meta name="keywords" content="Dostana, Damadam, Damadam pk, damdampk, damadampk, Pakistani content sharing website, kriim, Pakistan social media, Urdu chat, Pakistani posters, Mehfil chat, gamification, Pakistan community, online friends Pakistan">
            <meta name="author" content="Kriim & Dostana Team">

            <meta property="og:title" content="Dostana - The New Home for Damadampk Users">
            <meta property="og:description" content="Miss the old Damadampk and your friends? Join Dostana. Create Mehfils, rank up, and chat safely. Tamam purane dost yahan hain. Fast, light, and made for Pakistan!">
            <meta property="og:image" content="/static/dostana.png"> 
            <meta property="og:url" content="https://dostana.biz">
            <meta property="og:type" content="website">
            <script async src="https://pagead2.googlesyndication.com/pagead/js/adsbygoogle.js?client=ca-pub-1646502426945071"
     crossorigin="anonymous"></script>

             <meta name="robots" content="index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1">
            <link rel="canonical" href="https://dostana.biz">

            <link rel="icon" type="image/png" sizes="32x32" href="/static/favicon/dostanafavicon.png">
            <link rel="icon" type="image/png" sizes="16x16" href="/static/favicon/dostanafavicon.png">
            <link rel="shortcut icon" href="/static/favicon/dostanafavicon.png">
            <link rel="apple-touch-icon" sizes="180x180" href="/static/favicon/dostanafavicon.png">

            <script>
                document.addEventListener('htmx:beforeOnLoad', function (evt) {
                    const activeEl = document.activeElement;
                    if (activeEl && (activeEl.tagName === 'TEXTAREA' || activeEl.tagName === 'INPUT')) {
                        if (evt.detail.target.contains(activeEl)) {
                            evt.preventDefault();
                            console.log("HTMX refresh blocked to protect typing...");
                        }
                    }
                });
            </script>
            <script>
// Global avatar fallback — fires on ANY img with data-avatar attribute
// or any img inside .os-avatar-ring, .profile-img, .chat-avatar etc.
document.addEventListener('error', function(e) {
    var img = e.target;
    if (img.tagName !== 'IMG') return;
    if (img.dataset.fallbackApplied) return; // prevent infinite loop
    img.dataset.fallbackApplied = '1';
    
    // Extract a name hint from alt, or nearest username data attr, or 'U'
    var name = img.alt || 
               img.closest('[data-username]')?.dataset.username || 
               'U';
    img.src = 'https://ui-avatars.com/api/?name=' + 
              encodeURIComponent(name) + '&background=random&size=128';
}, true); // 'true' = capture phase, catches errors before they bubble
</script>
            <link href="/static/css/style.css?v=1.0.5" rel="stylesheet"> 
            <script src="/static/js/htmx.min.js?v=2.0.4"></script>
            <script src="/static/js/idiomorph.min.js?v=1.0.1"></script>
            <script src="/static/js/csrf.js?v=2"></script>
            

            <style>
                @import url('https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600&display=swap');
                body { font-family: 'Inter', system-ui, sans-serif; }
                :root {
                    --post-bg: #ffffff;
                    --post-border: #e5e7eb;
                    --post-text: #111827;
                }
                .dark {
                    --post-bg: #111827;
                    --post-border: #1f2937;
                    --post-text: #f9fafb;
                }
                .mehfil-container { max-width: 800px; margin: 0 auto; padding: 20px 15px; }
                .page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 30px; }
                .page-header h1 { font-size: 22px; font-weight: 900; text-transform: uppercase; }
                .mehfil-list { display: flex; flex-direction: column; gap: 10px; }
                .mehfil-card {
                    background: rgba(128, 128, 128, 0.06);
                    border: 1px solid rgba(128, 128, 128, 0.12);
                    border-radius: 18px; padding: 15px 20px;
                    display: flex; align-items: center; gap: 15px;
                }
                .card-main { flex: 1; cursor: pointer; min-width: 0; }
                .topic-row { display: flex; align-items: baseline; gap: 6px; margin-bottom: 4px; }
                .topic-label { font-size: 10px; font-weight: 900; color: #10b981; text-transform: uppercase; }
                .topic-text { font-size: 15px; font-weight: 800; text-transform: uppercase; margin: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
                .card-footer { display: flex; flex-wrap: wrap; gap: 12px; font-size: 10px; font-weight: 600; opacity: 0.6; }
                .action-btn {
                    background: rgba(239, 68, 68, 0.1);
                    border: 1px solid rgba(239, 68, 68, 0.2);
                    color: var(--btn-color);
                    padding: 8px 12px; border-radius: 10px;
                    font-size: 9px; font-weight: 900; text-transform: uppercase;
                    cursor: pointer; transition: 0.2s;
                }
                .action-btn:hover { background: rgba(239, 68, 68, 0.2); }
                .empty-state { text-align: center; padding: 50px; opacity: 0.5; font-weight: 700; }
            </style>
            <script>
                function toggleDarkMode() {
                    const html = document.documentElement;
                    const isDark = !html.classList.contains('dark');
                    html.classList.toggle('dark', isDark);
                    const icon = document.getElementById('theme-icon');
                    if (icon) icon.textContent = isDark ? '☀️' : '🌙';
                    localStorage.setItem('darkMode', isDark);
                }
                function initDarkMode() {
                    const saved = localStorage.getItem('darkMode');
                    const html = document.documentElement;
                    const icon = document.getElementById('theme-icon');
                    if (saved === 'true') {
                        html.classList.add('dark');
                        if (icon) icon.textContent = '☀️';
                    } else {
                        html.classList.remove('dark');
                        if (icon) icon.textContent = '🌙';
                    }
                }
                window.addEventListener('DOMContentLoaded', initDarkMode);
                document.addEventListener('htmx:afterSwap', initDarkMode);
                document.addEventListener('htmx:load', initDarkMode);
                  
                document.addEventListener('htmx:trigger', function(evt) {
                    if (evt.detail.name === 'updateOnlineCount') {
                        const countEl = document.getElementById('online-count');
                        if (countEl) countEl.textContent = evt.detail.count;
                    }
                });

                document.body.addEventListener('htmx:afterRequest', function(evt) {
                    if (evt.detail.pathInfo && evt.detail.pathInfo.requestPath === '/post' && evt.detail.successful) {
                        const postForm = document.getElementById('post-form');
                        if (postForm) {
                            postForm.reset();
                            const ta = postForm.querySelector('textarea[name="content"]');
                            if (ta) { ta.value = ''; ta.style.height = ''; }
                        }
                    }
                });

                function toggleCommentReply(id) {
                    const form = document.getElementById('reply-to-' + id);
                    if (form) form.classList.toggle('hidden');
                }
            </script>
            <script>
                let isTyping = false;
                document.addEventListener('htmx:beforeRequest', function(evt) {
                    let activeEl = document.activeElement;
                    if (isTyping || activeEl.tagName === 'INPUT' || activeEl.tagName === 'TEXTAREA') {
                        if (evt.detail.target.id === 'feed-container' || evt.detail.target.id === 'posts-list') {
                            evt.preventDefault(); 
                        }
                    }
                });
                document.addEventListener('htmx:afterRequest', function(evt) {
                    if (evt.detail.successful) {
                        isTyping = false;
                    }
                });
            </script>
            

            
        </head>
        <body hx-boost="true" class="min-h-screen bg-gray-50 dark:bg-gray-950 text-gray-900 dark:text-gray-100 flex flex-col items-center">
        
            
            %s 

       

            <main class="w-full max-w-full flex-1" style="padding-bottom: 120px !important; display: block !important;"> 
                %s 
            </main>
            
            %s 

            <script>
(function(){
  'use strict';

  function fmt(s){
    s = Math.floor(s || 0);
    return Math.floor(s / 60) + ':' + (('0' + (s %% 60)).slice(-2));
  }

  function pauseOthers(exceptAudio){
    document.querySelectorAll('audio[id^="vp-audio-"]').forEach(function(a){
      if(a === exceptAudio || a.paused) return;
      a.pause();
      var xid = a.id.replace('vp-audio-', '');
      var xb = document.getElementById('vp-btn-' + xid);
      var xp = document.getElementById('vp-prog-' + xid);
      var xt = document.getElementById('vp-time-' + xid);
      if(xb) xb.innerHTML = '▶';
      if(xp) xp.style.width = '0%%';
      if(xt) xt.textContent = '0:00';
    });
  }

  function initPlayer(container){
    var id    = container.dataset.vp;
    var src   = container.dataset.vpSrc;
    var audio = document.getElementById('vp-audio-' + id);
    var btn   = document.getElementById('vp-btn-'   + id);
    var prog  = document.getElementById('vp-prog-'  + id);
    var bar   = document.getElementById('vp-bar-'   + id);
    var timer = document.getElementById('vp-time-'  + id);

    if(!audio || audio._vpReady) return;  // already initialized
    audio._vpReady = true;

    // Ensure src is set (belt-and-suspenders for HTMX swaps)
    if(!audio.src || audio.src === window.location.href) {
      audio.src = src;
    }

    audio.addEventListener('timeupdate', function(){
      if(!audio.duration) return;
      prog.style.width = (audio.currentTime / audio.duration * 100) + '%%';
      timer.textContent = fmt(audio.currentTime);
    });

    audio.addEventListener('ended', function(){
      btn.innerHTML = '▶';
      prog.style.width = '0%%';
      timer.textContent = '0:00';
      audio.currentTime = 0;
    });

    if (btn) btn.addEventListener('click', function(){
      if(audio.paused){
        pauseOthers(audio);
        audio.play().catch(function(e){ console.warn('play() failed:', e); });
        btn.innerHTML = '⏸';
      } else {
        audio.pause();
        btn.innerHTML = '▶';
      }
    });

    if (bar) bar.addEventListener('click', function(evt){
      if(!audio.duration) return;
      var rect = bar.getBoundingClientRect();
      var ratio = Math.max(0, Math.min(1, (evt.clientX - rect.left) / rect.width));
      audio.currentTime = ratio * audio.duration;
    });
  }

  function initAllPlayers(){
    document.querySelectorAll('[data-vp]').forEach(initPlayer);
  }

  // On hard load
  document.addEventListener('DOMContentLoaded', initAllPlayers);

  // After every HTMX swap (covers afterbegin, outerHTML, innerHTML swaps)
  document.addEventListener('htmx:afterSettle', initAllPlayers);
})();
</script>

        </body>
        </html>
    `, title, nav, body, bottomMenu)
}

func GetBadgeHTML(count int) string {
	badge := ""
	if count > 0 {
		badge = fmt.Sprintf(`
            <span class="absolute -top-1 -right-1 bg-red-500 text-white text-[10px] w-4 h-4 flex items-center justify-center rounded-full font-bold animate-pulse">%d</span>
        `, count)
	}
	return fmt.Sprintf(`
        <a href="/notifications" hx-boost="true" class="relative inline-flex items-center hover:scale-110 transition-transform">
            <span class="text-xl">🔔</span>
            %s
        </a>`, badge)
}

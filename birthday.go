package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	birthdayMonth = time.October
	birthdayDay   = 2
	birthdayName  = "Denz"
)

// Her username (lowercase). Override with BIRTHDAY_USERNAME in .env if it isn't "denz".
func birthdayUsername() string {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("BIRTHDAY_USERNAME"))); v != "" {
		return v
	}
	return "skywalker"
}

func isBirthdayUser(username string) bool { return strings.EqualFold(username, birthdayUsername()) }

var karachiLoc = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Karachi"); err == nil {
		return l
	}
	return time.FixedZone("PKT", 5*3600)
}()

// birthdayPhase: 0 = nothing, 1 = teaser (1-2 days before), 2 = birthday.
// Testing: BIRTHDAY_FORCE=1 (full theme) or BIRTHDAY_FORCE=teaser, then restart.
func birthdayPhase() (phase int, daysLeft int) {
	switch strings.TrimSpace(os.Getenv("BIRTHDAY_FORCE")) {
	case "1":
		return 2, 0
	case "teaser":
		return 1, 1
	}
	now := time.Now().In(karachiLoc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, karachiLoc)
	bday := time.Date(now.Year(), birthdayMonth, birthdayDay, 0, 0, 0, 0, karachiLoc)
	diff := int(bday.Sub(today) / (24 * time.Hour))
	// Full celebration theme starts 4 days early (Sep 28 -> Oct 2).
	if diff >= 0 && diff <= 4 {
		return 2, diff
	}
	return 0, 0
}

func isDenzBirthday() bool { p, _ := birthdayPhase(); return p == 2 }

// Layout is the public wrapper; layoutBase (in your layout file) builds the page.
func Layout(title, body, currentUser string) string {
	page := applyBirthdayTheme(layoutBase(title, body, currentUser))
	if currentUser != "" && isDenzBirthday() {
		page = strings.Replace(page, "<main class=",
			fmt.Sprintf(`<script>window.bdayServerTotal=%d;if(window.bdayScoreUpdate)window.bdayScoreUpdate();</script><main class=`,
				balloonsPoppedOf(currentUser)), 1)
	}
	return page
}

func applyBirthdayTheme(page string) string {
	phase, left := birthdayPhase()
	if phase == 0 {
		return page
	}
	page = strings.Replace(page, "</head>", birthdayHeadCSS+"</head>", 1)
	if phase == 2 {
		page = strings.Replace(page, `<body hx-boost="true" class="`, `<body hx-boost="true" class="bday-on `, 1)
		page = strings.Replace(page, "<title>", "<title>🎂 ", 1)
		page = strings.Replace(page, `placeholder="Quick thought…"`, `placeholder="🎂 `+birthdayName+` ko birthday wish karein…"`, 1)
	}
	return strings.Replace(page, "<main class=", birthdayBannerHTML(phase, left)+"<main class=", 1)
}

func birthdayBannerHTML(phase, left int) string {
	var b strings.Builder
	if phase == 2 {
		b.WriteString(birthdayLayerHTML())
	}
	title, sub := "", ""
	if phase == 2 && left > 0 {
		when := fmt.Sprintf("sirf %d din baqi", left)
		if left == 1 {
			when = "kal"
		}
		title = "🎂 " + birthdayName + " ki birthday " + when + "! 🎈"
		sub = "Celebration shuru ho chuka hai — hamari Admin " + birthdayName + " ke liye duain 💖"
	} else if phase == 2 {
		title = "Happy Birthday " + birthdayName + "! 🎉"
		sub = "Aaj hamari Admin " + birthdayName + " ki birthday hai — dher saari duain aur pyaar 💖"
	} else {
		when := fmt.Sprintf("sirf %d din baqi", left)
		if left == 1 {
			when = "kal"
		}
		title = "🎁 " + birthdayName + " ki birthday " + when + "!"
		sub = "Tayyar ho jayein, celebration aane wala hai 🎈"
	}
	b.WriteString(`<div id="bday-banner" class="bday-banner">` +
		`<div style="flex:1;min-width:0;"><div class="bday-t">` + title + `</div>` +
		`<div class="bday-s">` + sub + `</div></div>`)
	b.WriteString(`<button type="button" class="bday-btn" aria-label="Close" onclick="document.getElementById('bday-banner').style.display='none';try{sessionStorage.setItem('bdayHide','1')}catch(e){}">✕</button></div>`)
	b.WriteString(birthdayScript)
	return b.String()
}

func birthdayLayerHTML() string {
	balloon := []string{"#f43f5e", "#f59e0b", "#8b5cf6", "#06b6d4", "#22c55e", "#ec4899", "#3b82f6"}
	var b strings.Builder
	b.WriteString(`<div aria-hidden="true" class="bday-layer" style="position:fixed;inset:0;pointer-events:none;overflow:hidden;z-index:45;">`)

	for i := 0; i < 18; i++ {
		fmt.Fprintf(&b, `<i class="bday-c" style="--l:%d%%;--d:-%ds;--t:%ds;--c:%s;"></i>`,
			(i*53+7)%100, (i*5)%11, 7+(i%5)*2, balloon[i%len(balloon)])
	}
	b.WriteString(`</div>`)
	return b.String()
}

// Extra ribbon under Denz's own posts.
func birthdayPostBar() string {
	return `<div style="height:6px;width:100%;background-image:repeating-linear-gradient(90deg,#f43f5e 0 14px,#facc15 14px 28px,#22c55e 28px 42px,#3b82f6 42px 56px,#a855f7 56px 70px);"></div>` +
		`<div style="text-align:center;padding:5px 0 6px;background:linear-gradient(180deg,rgba(244,114,182,.18),transparent);">` +
		`<span style="font-size:11px;font-weight:900;letter-spacing:.06em;color:#ec4899;">🎂 HAPPY BIRTHDAY ` + strings.ToUpper(birthdayName) + ` 🎂</span></div>`
}

const birthdayScript = `<script>
(function(){
 if(!window.bdayReportPop){
  var pend=0,inflight=0,tm=null,fails=0;
  window.bdayServerTotal=window.bdayServerTotal||0;
  function upd(){try{if(window.bdayScoreUpdate)window.bdayScoreUpdate();}catch(e){}}
  function flush(){
   tm=null;
   if(inflight>0||!window.htmx){if(pend)tm=setTimeout(flush,1000);return;}
   if(!pend)return;
   inflight=pend;pend=0;
   try{htmx.ajax('POST','/birthday/pop',{target:'body',swap:'none',values:{n:inflight}});}
   catch(e){pend+=inflight;inflight=0;}
  }
  window.bdayReportPop=function(k){pend+=(k||1);upd();if(!tm)tm=setTimeout(flush,600);};
  window.bdayPending=function(){return pend+inflight;};
  document.addEventListener('visibilitychange',function(){
   if(document.hidden&&pend&&!inflight){if(tm)clearTimeout(tm);flush();}
  });
  document.addEventListener('htmx:afterRequest',function(e){
   var d=e.detail||{},p=(d.pathInfo&&d.pathInfo.requestPath)||'';
   if(p!=='/birthday/pop')return;
   var x=d.xhr,j=null;try{j=JSON.parse(x.responseText);}catch(_){}
   if(j&&j.ok){fails=0;if(typeof j.total==='number')window.bdayServerTotal=j.total;inflight=0;}
   else if(!x||x.status===0||x.status>=500){pend+=inflight;inflight=0;fails++;if(fails>5)pend=0;}
   else{console.warn('balloon pop not saved, HTTP '+(x&&x.status));inflight=0;}
   if(pend&&!tm)tm=setTimeout(flush,fails?3000:1500);
   upd();
  });
 }
 if(!window.bdayBurst){
  window.bdayBurst=function(){
   var cols=['#f43f5e','#f59e0b','#8b5cf6','#06b6d4','#22c55e','#ec4899','#facc15'];
   var wrap=document.createElement('div');
   wrap.style.cssText='position:fixed;left:0;top:0;width:100%;height:0;pointer-events:none;z-index:60;';
   for(var i=0;i<46;i++){
    var p=document.createElement('i');
    var a=Math.random()*Math.PI*2, d=90+Math.random()*180;
    p.style.cssText='position:absolute;left:50%;top:110px;width:8px;height:12px;border-radius:2px;background:'+cols[i%cols.length]+';opacity:1;transition:transform 1.4s cubic-bezier(.15,.8,.3,1),opacity 1.4s ease;';
    wrap.appendChild(p);
    (function(el,a,d){requestAnimationFrame(function(){requestAnimationFrame(function(){
     el.style.transform='translate('+(Math.cos(a)*d)+'px,'+(Math.sin(a)*d+140)+'px) rotate('+(Math.random()*720)+'deg)';
     el.style.opacity='0';});});})(p,a,d);
   }
   document.body.appendChild(wrap);
   setTimeout(function(){wrap.remove();},1600);
  };
 }
 try{
  if(sessionStorage.getItem('bdayHide')==='1'){var b=document.getElementById('bday-banner');if(b)b.style.display='none';}
  if(document.querySelector('.bday-layer')&&!sessionStorage.getItem('bdayShown')){sessionStorage.setItem('bdayShown','1');setTimeout(window.bdayBurst,700);}
 }catch(e){}
})();
</script>`

const birthdayHeadCSS = `<style id="bday-css">
@keyframes bdayRise{0%{transform:translate3d(0,0,0) rotate(-4deg)}50%{transform:translate3d(18px,-55vh,0) rotate(4deg)}100%{transform:translate3d(-8px,-125vh,0) rotate(-4deg)}}
@keyframes bdayDrop{0%{transform:translate3d(0,0,0) rotate(-6deg)}25%{transform:translate3d(16px,30vh,0) rotate(6deg)}50%{transform:translate3d(-14px,60vh,0) rotate(-6deg)}75%{transform:translate3d(14px,90vh,0) rotate(6deg)}100%{transform:translate3d(-8px,135vh,0) rotate(-4deg)}}
@keyframes bdayHatPop{0%,100%{transform:translateX(-50%) scale(1) rotate(0)}50%{transform:translateX(-50%) scale(1.2) rotate(-8deg)}}
@keyframes bdayFall{0%{transform:translate3d(0,0,0) rotate(0)}50%{transform:translate3d(14px,55vh,0) rotate(360deg)}100%{transform:translate3d(-10px,112vh,0) rotate(720deg)}}
@keyframes bdayShine{0%{background-position:0% 50%}100%{background-position:200% 50%}}
@keyframes bdayPop{0%,100%{transform:scale(1) rotate(0)}50%{transform:scale(1.18) rotate(-6deg)}}
.bday-b{position:absolute;top:-130px;left:var(--l);width:var(--s);height:calc(var(--s)*1.2);border-radius:50% 50% 48% 48%/58% 58% 42% 42%;background-color:var(--c);background-image:radial-gradient(circle at 32% 26%,rgba(255,255,255,.8) 0 10%,transparent 11%);opacity:.85;animation:bdayDrop var(--t) linear var(--d) infinite;will-change:transform}
.bday-b::before{content:"";position:absolute;left:50%;bottom:-4px;transform:translateX(-50%);border:4px solid transparent;border-bottom-color:var(--c);border-top:0}
.bday-b::after{content:"";position:absolute;left:50%;top:100%;margin-top:4px;width:1px;height:34px;background:rgba(100,116,139,.6)}
.bday-c{position:absolute;top:-20px;left:var(--l);width:7px;height:12px;border-radius:2px;background:var(--c);opacity:.85;animation:bdayFall var(--t) linear var(--d) infinite;will-change:transform}
.bday-banner{margin:6px 8px 0;padding:6px 10px;border-radius:14px;display:flex;align-items:center;gap:6px;color:#fff;text-shadow:0 1px 2px rgba(0,0,0,.45);background-image:linear-gradient(100deg,#db2777,#ea580c,#ca8a04,#db2777);background-size:200% 100%;animation:bdayShine 7s linear infinite;box-shadow:0 6px 18px -8px rgba(236,72,153,.7);width:calc(100% - 16px);max-width:780px;box-sizing:border-box}
html.dark .bday-banner{background-image:linear-gradient(100deg,#831843,#7c2d12,#713f12,#831843);box-shadow:0 6px 18px -8px rgba(217,70,239,.5)}
.bday-cake{font-size:24px;line-height:1;display:inline-block;animation:bdayPop 1.6s ease-in-out infinite}
.bday-btn{flex-shrink:0;background:rgba(255,255,255,.22);border:1px solid rgba(255,255,255,.5);color:#fff;padding:4px 9px;border-radius:999px;font-size:10px;font-weight:800;text-decoration:none;cursor:pointer;text-shadow:none;-webkit-tap-highlight-color:transparent}
.bday-btn:active{transform:scale(.92)}
.bday-t{font-size:12px;font-weight:900;line-height:1.2}
.bday-s{font-size:10px;font-weight:600;opacity:.95;line-height:1.3}
.bday-btn{white-space:nowrap}
@media (max-width:480px){.bday-s{display:none}.bday-cake{font-size:20px}.bday-t{font-size:11.5px}.bday-btn{padding:3px 7px;font-size:9.5px}}
@media (max-width:380px){.bday-hide-xs{display:none}}
body.bday-on{background-image:radial-gradient(circle at 12% 8%,rgba(244,114,182,.14),transparent 42%),radial-gradient(circle at 88% 18%,rgba(250,204,21,.14),transparent 40%)}
html.dark body.bday-on{background-image:radial-gradient(circle at 12% 8%,rgba(217,70,239,.16),transparent 45%),radial-gradient(circle at 88% 18%,rgba(251,146,60,.10),transparent 42%)}
body.bday-on nav.sticky{background-image:linear-gradient(100deg,#9d174d,#c026d3 45%,#7c3aed 75%,#be185d)!important;background-size:200% 100%;animation:bdayShine 12s linear infinite;border-bottom-color:rgba(250,204,21,.7)!important}
html.dark body.bday-on nav.sticky{background-image:linear-gradient(100deg,#500724,#701a75 45%,#3b0764 75%,#4c0519)!important}
body.bday-on nav.sticky::after{content:"";position:absolute;left:0;right:0;bottom:-12px;height:12px;pointer-events:none;background-image:conic-gradient(from -42deg at 50% 100%,#f472b6 0 84deg,transparent 0),conic-gradient(from -42deg at 50% 100%,#fbbf24 0 84deg,transparent 0);background-size:44px 12px,44px 12px;background-position:0 0,22px 0;background-repeat:repeat-x}
body.bday-on div[style*="top: auto !important"]{background-image:linear-gradient(100deg,#be185d,#7c3aed 55%,#9d174d)!important;border-top:2px solid rgba(250,204,21,.7)!important}
html.dark body.bday-on div[style*="top: auto !important"]{background-image:linear-gradient(100deg,#4c0519,#3b0764 55%,#500724)!important}
body.bday-on .home-card,body.bday-on .composer-card,body.bday-on .feed-toolbar{border-color:rgba(244,114,182,.45)}
html.dark body.bday-on .home-card,html.dark body.bday-on .composer-card,html.dark body.bday-on .feed-toolbar{border-color:rgba(232,121,249,.4)}
body.bday-on .post-card{box-shadow:0 0 0 1px rgba(244,114,182,.35),0 8px 20px -10px rgba(236,72,153,.45)}
html.dark body.bday-on .post-card{box-shadow:0 0 0 1px rgba(232,121,249,.35),0 8px 22px -10px rgba(192,38,211,.55)}
body.bday-on .post-card>div:first-child{position:relative}
body.bday-on .post-card>div:first-child::after{content:"🎈 🎉 🎈";position:absolute;left:50%;top:50%;transform:translate(-50%,-50%);font-size:11px;letter-spacing:2px;pointer-events:none;filter:drop-shadow(0 1px 1px rgba(0,0,0,.4))}
.bday-denz-post{box-shadow:0 0 0 2px rgba(244,114,182,.85),0 10px 28px -8px rgba(244,114,182,.8)!important}
/* ── Online users card ── */
body.bday-on .os-pill{background:linear-gradient(90deg,#fce7f3,#fef3c7);border-color:#f9a8d4;color:#be185d}
html.dark body.bday-on .os-pill{background:linear-gradient(90deg,#500724,#713f12);border-color:#9d174d;color:#f9a8d4}
body.bday-on .os-pill-dot{background:#ec4899}
body.bday-on .os-search-input:focus{border-color:#ec4899}
body.bday-on .os-toggle-btn.expanded{background:#fce7f3;color:#be185d;border-color:#ec4899}
body.bday-on .home-card{background-image:linear-gradient(180deg,rgba(244,114,182,.12),transparent 70px)}
body.bday-on .home-card::before{content:"🎈  🎉  🎂  🎉  🎈";display:block;text-align:center;font-size:12px;letter-spacing:4px;padding:5px 0 0;animation:bdayPop 2.4s ease-in-out infinite}
body.bday-on .os-card{transition:transform .15s}
body.bday-on .os-card:hover{transform:translateY(-3px)}
.os-card.os-bday{width:60px;background:linear-gradient(180deg,rgba(244,114,182,.22),transparent);border-radius:14px}
.os-bday .os-avatar-ring{border-color:#ec4899!important;box-shadow:0 0 0 2px rgba(244,114,182,.4),0 0 14px 3px rgba(244,114,182,.8)!important}
.os-bday .os-name{color:#ec4899!important}
.os-bday .os-pts{color:#db2777!important;font-weight:900}
.os-bday-hat{position:absolute;top:-14px;left:58%;width:clamp(20px,5.5vw,26px);height:clamp(20px,5.5vw,26px);z-index:30;pointer-events:none;line-height:0;transform:translateX(-50%) rotate(-14deg);transform-origin:50% 90%;animation:bdayHatPop 1.6s ease-in-out infinite}
/* ── Home toolbar / composer ── */
body.bday-on .sq-btn.f-active{background-image:linear-gradient(135deg,#ec4899,#8b5cf6);border-color:#ec4899;box-shadow:0 4px 12px rgba(236,72,153,.4)}
body.bday-on .cmp-send-btn{background-image:linear-gradient(135deg,#ec4899,#f59e0b);box-shadow:0 2px 8px rgba(236,72,153,.45)}
body.bday-on .composer-textarea:focus{border-color:#ec4899}
body.bday-on .lt-tab.lt-active{color:#db2777}
@media (prefers-reduced-motion:reduce){.bday-layer{display:none}.bday-banner,.bday-cake,.os-bday-hat,body.bday-on .home-card::before,body.bday-on nav.sticky{animation:none!important}}
</style>`

// ══════════════════════════════════════════════════════════════════════
// SKYWALKER BIRTHDAY — party hat, 3D profile frame, 3D celebration scene
// ══════════════════════════════════════════════════════════════════════

func isBirthdayProfile(username string) bool {
	return isDenzBirthday() && isBirthdayUser(username)
}

// Small responsive party hat (SVG scales to whatever box holds it).
func birthdayHatSVG() string {
	return `<svg viewBox="0 0 64 64" width="100%" height="100%" xmlns="http://www.w3.org/2000/svg" aria-hidden="true" style="display:block;overflow:visible;filter:drop-shadow(0 3px 3px rgba(0,0,0,.35));">` +
		`<defs><linearGradient id="bhatG" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#f472b6"/><stop offset="1" stop-color="#7c3aed"/></linearGradient></defs>` +
		`<path d="M32 7 L53 54 Q32 62 11 54 Z" fill="url(#bhatG)"/>` +
		`<path d="M25 24 L39 24 M21 34 L43 34 M17 44 L47 44" stroke="#fde047" stroke-width="3" stroke-linecap="round"/>` +
		`<circle cx="27" cy="29" r="1.6" fill="#fff"/><circle cx="38" cy="40" r="1.6" fill="#fff"/><circle cx="26" cy="49" r="1.6" fill="#fff"/>` +
		`<ellipse cx="32" cy="55" rx="23" ry="5.5" fill="#fbbf24"/>` +
		`<circle cx="32" cy="7" r="6" fill="#fde047"/><circle cx="30" cy="5" r="2" fill="#fff" opacity=".85"/>` +
		`</svg>`
}

func birthdayBuntingSVG() string {
	cols := []string{"#f43f5e", "#facc15", "#22c55e", "#3b82f6", "#a855f7", "#f97316"}
	var b strings.Builder
	b.WriteString(`<svg viewBox="0 0 300 50" width="100%" style="display:block;overflow:visible" aria-hidden="true">` +
		`<path d="M0 4 Q150 32 300 4" fill="none" stroke="#fff" stroke-width="1.6" opacity=".9"/>`)
	for i := 0; i < 10; i++ {
		t := (float64(i) + 0.5) / 10
		x := 300 * t
		y := 4 + 56*t*(1-t)
		fmt.Fprintf(&b, `<path d="M%.1f %.1f L%.1f %.1f L%.1f %.1f Z" fill="%s"/>`,
			x-11, y, x+11, y, x, y+24, cols[i%len(cols)])
	}
	b.WriteString(`</svg>`)
	return b.String()
}

func birthdayCubeHTML(cls string, faces [6]string) string {
	var b strings.Builder
	b.WriteString(`<div class="bp-cube ` + cls + `">`)
	for _, f := range faces {
		b.WriteString(`<b>` + f + `</b>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// Wraps the profile picture: decorated animated frame + party hat + bunting
// + spinning 3D gift cubes + sparkles. Returns avatar unchanged for everyone else.
func birthdayAvatarWrap(username, avatarInner string) string {
	if !isBirthdayProfile(username) {
		return avatarInner
	}
	var b strings.Builder
	b.WriteString(`<div class="bp-wrap"><div class="bp-tilt">`)
	b.WriteString(`<div class="bp-pic">` + avatarInner + `</div>`)
	b.WriteString(`<div class="bp-frame"></div>`)
	b.WriteString(`<div class="bp-bunting">` + birthdayBuntingSVG() + `</div>`)
	b.WriteString(`<div class="bp-hat">` + birthdayHatSVG() + `</div>`)
	b.WriteString(birthdayCubeHTML("bp-cube-l", [6]string{"🎁", "🎂", "🎈", "🎉", "💖", "🎊"}))
	b.WriteString(birthdayCubeHTML("bp-cube-r", [6]string{"🎂", "🎁", "🎊", "💖", "🎈", "🎉"}))
	sp := [][3]int{{6, 30, 0}, {88, 24, 5}, {2, 62, 9}, {93, 58, 3}, {16, 8, 7}, {80, 10, 2}}
	for _, s := range sp {
		fmt.Fprintf(&b, `<span class="bp-spark" style="left:%d%%;top:%d%%;animation-delay:-%d.%ds;">✨</span>`, s[0], s[1], s[2]%3, s[2])
	}
	b.WriteString(`</div></div>`)
	return b.String()
}

// Top-of-profile block: CSS + 3D falling confetti/balloons + 3D title + burst.
func birthdayProfileTop(username string) string {
	if !isBirthdayProfile(username) {
		return ""
	}
	pal := [][2]string{{"#f43f5e", "#fda4af"}, {"#f59e0b", "#fde68a"}, {"#8b5cf6", "#c4b5fd"},
		{"#06b6d4", "#a5f3fc"}, {"#22c55e", "#bbf7d0"}, {"#ec4899", "#fbcfe8"}}
	var b strings.Builder
	b.WriteString(birthdayProfileCSS)
	b.WriteString(`<div aria-hidden="true" class="bp-scene">`)
	for i := 0; i < 8; i++ {
		p := pal[i%len(pal)]
		fmt.Fprintf(&b, `<i class="bp-b3" style="--l:%d%%;--d:-%ds;--t:%ds;--c:%s;--c2:%s;--s:%dpx;"></i>`,
			(i*13+5)%90, i*3, 14+(i%4)*2, p[0], p[1], 34+(i%3)*8)
	}
	for i := 0; i < 24; i++ {
		fmt.Fprintf(&b, `<i class="bp-c3" style="--l:%d%%;--d:-%ds;--t:%ds;--c:%s;"></i>`,
			(i*41+7)%100, i%9, 6+(i%5)*2, pal[i%len(pal)][0])
	}
	b.WriteString(`</div>`)
	b.WriteString(`<div class="bp-title-wrap"><div class="bp-title">🎂 Happy Birthday ` + birthdayName + ` 🎂</div>` +
		`<div class="bp-sub">Aaj hamari Admin ` + birthdayName + ` ki birthday hai — dher saari duain aur pyaar 💖</div></div>`)
	b.WriteString(`<script>setTimeout(function(){try{if(window.bdayBurst)window.bdayBurst();` +
		`var w=document.querySelector('.bp-wrap');if(w&&!w.__b){w.__b=1;w.style.cursor='pointer';` +
		`w.addEventListener('click',function(){if(window.bdayBurst)window.bdayBurst();});}}catch(e){}},700);</script>`)
	return b.String()
}

const birthdayProfileCSS = `<style id="bp-css">
@keyframes bpShine{0%{background-position:0% 50%}100%{background-position:300% 50%}}
@keyframes bpSway{0%,100%{transform:rotateY(-9deg) rotateX(3deg)}50%{transform:rotateY(9deg) rotateX(-3deg)}}
@keyframes bpHat{0%,100%{transform:translateX(-50%) translateZ(46px) rotate(-8deg)}50%{transform:translateX(-50%) translateZ(54px) translateY(-5px) rotate(-2deg)}}
@keyframes bpSpin{from{transform:rotateX(-22deg) rotateY(0)}to{transform:rotateX(-22deg) rotateY(360deg)}}
@keyframes bpTwinkle{0%,100%{opacity:.2;transform:scale(.6) rotate(0)}50%{opacity:1;transform:scale(1.25) rotate(20deg)}}
@keyframes bpTitle{0%,100%{transform:perspective(600px) rotateX(14deg) rotateY(-6deg)}50%{transform:perspective(600px) rotateX(6deg) rotateY(6deg) translateY(-3px)}}
@keyframes bpRise{0%{transform:translate3d(0,0,0) rotateZ(-5deg)}50%{transform:translate3d(18px,-65vh,80px) rotateZ(5deg)}100%{transform:translate3d(-10px,-140vh,0) rotateZ(-5deg)}}
@keyframes bpFall{0%{transform:translate3d(0,0,0) rotate3d(1,1,0,0deg)}100%{transform:translate3d(-16px,115vh,120px) rotate3d(1,.6,.3,900deg)}}
.bp-avatar-box{overflow:visible!important;margin-top:74px!important;margin-bottom:34px!important}
.bp-wrap{position:relative;width:100%;align-self:stretch;perspective:900px}
.bp-tilt{position:absolute;inset:0;transform-style:preserve-3d;animation:bpSway 7s ease-in-out infinite}
.bp-pic{position:absolute;inset:0;border-radius:32px;overflow:hidden;background:#fdf2f8;box-shadow:0 18px 40px -10px rgba(236,72,153,.55)}
.bp-frame{position:absolute;inset:-8px;border-radius:40px;padding:8px;pointer-events:none;transform:translateZ(14px);
 background-image:linear-gradient(110deg,#f43f5e,#f59e0b,#facc15,#22c55e,#3b82f6,#a855f7,#f43f5e);background-size:300% 100%;animation:bpShine 6s linear infinite;
 -webkit-mask:linear-gradient(#000 0 0) content-box,linear-gradient(#000 0 0);-webkit-mask-composite:xor;
 mask:linear-gradient(#000 0 0) content-box,linear-gradient(#000 0 0);mask-composite:exclude;
 filter:drop-shadow(0 0 10px rgba(236,72,153,.75))}
.bp-hat{position:absolute;left:50%;top:-24%;width:36%;aspect-ratio:1;z-index:5;pointer-events:none;transform-origin:50% 90%;transform:translateX(-50%) translateZ(46px) rotate(-8deg);animation:bpHat 2.4s ease-in-out infinite}
.bp-bunting{position:absolute;left:-3%;width:106%;bottom:-5%;pointer-events:none;transform:translateZ(24px);filter:drop-shadow(0 3px 2px rgba(0,0,0,.25))}
.bp-cube{position:absolute;top:6%;width:34px;height:34px;transform-style:preserve-3d;animation:bpSpin 8s linear infinite;z-index:4}
.bp-cube-l{left:-6px}.bp-cube-r{right:-6px;animation-direction:reverse}
.bp-cube b{position:absolute;inset:0;display:flex;align-items:center;justify-content:center;font-size:18px;background:rgba(255,255,255,.92);border:1.5px solid #f472b6;border-radius:6px}
.bp-cube b:nth-child(1){transform:translateZ(17px)}
.bp-cube b:nth-child(2){transform:rotateY(90deg) translateZ(17px)}
.bp-cube b:nth-child(3){transform:rotateY(180deg) translateZ(17px)}
.bp-cube b:nth-child(4){transform:rotateY(-90deg) translateZ(17px)}
.bp-cube b:nth-child(5){transform:rotateX(90deg) translateZ(17px)}
.bp-cube b:nth-child(6){transform:rotateX(-90deg) translateZ(17px)}
.bp-spark{position:absolute;font-size:16px;z-index:6;pointer-events:none;animation:bpTwinkle 1.8s ease-in-out infinite}
.bp-title-wrap{text-align:center;margin:0 0 4px}
.bp-title{display:inline-block;font-weight:900;font-size:clamp(17px,5.6vw,26px);letter-spacing:.02em;color:#f43f5e;transform-origin:50% 100%;
 text-shadow:0 1px 0 #e11d48,0 2px 0 #be123c,0 3px 0 #9f1239,0 4px 0 #881337,0 5px 0 #7f1233,0 12px 14px rgba(157,23,77,.45);animation:bpTitle 4s ease-in-out infinite}
.bp-sub{font-size:11px;font-weight:700;color:#be185d;margin-top:8px}
.bp-scene{position:fixed;inset:0;pointer-events:none;overflow:hidden;z-index:44;perspective:800px}
.bp-b3{position:absolute;top:105vh;left:var(--l);width:var(--s);height:calc(var(--s)*1.22);border-radius:50% 50% 48% 48%/58% 58% 42% 42%;
 background:radial-gradient(circle at 32% 26%,rgba(255,255,255,.95) 0 7%,transparent 14%),radial-gradient(circle at 38% 34%,var(--c2),var(--c) 58%,rgba(0,0,0,.4) 130%);
 box-shadow:inset -5px -7px 10px rgba(0,0,0,.25),0 8px 14px rgba(0,0,0,.18);animation:bpRise var(--t) linear var(--d) infinite;will-change:transform}
.bp-b3::before{content:"";position:absolute;left:50%;bottom:-4px;transform:translateX(-50%);border:4px solid transparent;border-bottom-color:var(--c);border-top:0}
.bp-b3::after{content:"";position:absolute;left:50%;top:100%;margin-top:4px;width:1px;height:38px;background:rgba(100,116,139,.6)}
.bp-c3{position:absolute;top:-20px;left:var(--l);width:8px;height:14px;border-radius:2px;background:var(--c);animation:bpFall var(--t) linear var(--d) infinite;will-change:transform}
@media (prefers-reduced-motion:reduce){.bp-tilt,.bp-hat,.bp-cube,.bp-title,.bp-frame,.bp-spark{animation:none!important}.bp-scene{display:none}}
</style>`

// ══════════════════════════════════════════════════════════════════════
// INTERACTIVE BALLOONS — drag, throw, bounce, collide, tap to pop.
// Lives outside <body>, so it survives hx-boost page navigation.
// Replaces the old CSS-only balloons (.bday-b and .bp-b3) on every page.
// ══════════════════════════════════════════════════════════════════════
const birthdayBalloonScript = `<script>
(function(){
 if(window.__bdyOn||!document.querySelector('.bday-layer'))return;
 if(window.matchMedia&&matchMedia('(prefers-reduced-motion: reduce)').matches)return;
 window.__bdyOn=true;

 /* [main, light, dark] */
  var COLS=[['#e11d48','#fda4af','#6b0a26'],['#f59e0b','#fde68a','#7a3d00'],['#7c3aed','#c4b5fd','#2e1065'],
           ['#0891b2','#a5f3fc','#083344'],['#16a34a','#bbf7d0','#052e16'],['#db2777','#fbcfe8','#500724'],['#2563eb','#bfdbfe','#172554']];
 var css=document.createElement('style');
 css.textContent=[
  '.bday-b,.bp-b3{display:none!important}',
  '@keyframes bdyW{0%{opacity:1;transform:translate(-50%,-50%) scale(.6)}100%{opacity:0;transform:translate(-50%,-160%) scale(1.5)}}',
  '#bdy-layer{position:fixed;left:0;top:0;width:100%;height:100%;pointer-events:none;overflow:hidden;z-index:45}',
  '.bdy-b{position:absolute;left:0;top:0;pointer-events:auto;touch-action:none;cursor:grab;user-select:none;-webkit-user-select:none;-webkit-touch-callout:none;-webkit-tap-highlight-color:transparent;will-change:transform}',
  '.bdy-b.drag{cursor:grabbing;z-index:5}',
  '.bdy-in{position:absolute;left:0;top:0;width:100%;height:100%;will-change:transform;transform-origin:50% 55%}',
  '.bdy-body{position:absolute;left:0;top:0;width:100%;height:100%;border-radius:50% 50% 47% 47%/56% 56% 44% 44%;',
  'background:radial-gradient(ellipse at 34% 26%,var(--c2) 0,var(--c) 46%,var(--c3) 120%);',
  'box-shadow:inset -8px -12px 18px rgba(0,0,0,.28),inset 6px 8px 14px rgba(255,255,255,.22),0 10px 18px rgba(0,0,0,.2)}',
  '.bdy-body::before{content:"";position:absolute;left:16%;top:8%;width:24%;height:15%;border-radius:50%;background:radial-gradient(ellipse,rgba(255,255,255,.95),rgba(255,255,255,0) 72%);transform:rotate(-32deg)}',
  '.bdy-body::after{content:"";position:absolute;right:14%;bottom:15%;width:12%;height:22%;border-radius:50%;background:radial-gradient(ellipse,rgba(255,255,255,.35),rgba(255,255,255,0) 70%);transform:rotate(24deg)}',
  '.bdy-knot{position:absolute;left:50%;top:97%;width:15%;height:9%;margin-left:-7.5%;background:var(--c3);clip-path:polygon(50% 0,100% 100%,62% 84%,50% 100%,38% 84%,0 100%)}',
  '.bdy-txt{position:absolute;left:6%;right:6%;top:19%;text-align:center;color:#fff;font-family:"Trebuchet MS","Arial Rounded MT Bold",system-ui,sans-serif;font-weight:900;line-height:1.02;pointer-events:none;transform:rotate(-5deg);',
  'text-shadow:0 1px 0 var(--c3),0 -1px 0 var(--c3),1px 0 0 var(--c3),-1px 0 0 var(--c3),0 3px 6px rgba(0,0,0,.35)}',
  '.bdy-txt i,.bdy-txt b,.bdy-txt em{display:block;font-style:normal}',
  '.bdy-txt .t1{font-size:calc(var(--s)*.115);letter-spacing:.08em}',
  '.bdy-txt .t2{font-size:calc(var(--s)*.118);letter-spacing:.02em}',
  '.bdy-txt .t3{font-size:calc(var(--s)*.27);margin:1px 0 2px;color:#fff7c2}',
  '.bdy-txt .t4{display:inline-block;font-size:calc(var(--s)*.105);padding:1px 7px;border-radius:999px;background:rgba(0,0,0,.28);letter-spacing:.06em}',
  '.bdy-str{position:absolute;left:50%;top:100%;width:20px;height:calc(var(--s)*.8);margin-left:-10px;margin-top:8px;overflow:visible;transform-origin:50% 0;pointer-events:none}',
  '.bdy-p{position:absolute;pointer-events:none;border-radius:2px}',
  '.bdy-r{position:absolute;pointer-events:none;width:24px;height:24px;border-radius:50%;border:3px solid #fff;opacity:.95;transform:translate(-50%,-50%) scale(.3);transition:transform .5s ease-out,opacity .5s ease-out}',
  '.bdy-w{position:absolute;pointer-events:none;font:900 18px system-ui,sans-serif;color:#fff;text-shadow:0 2px 0 rgba(0,0,0,.4),0 0 8px rgba(0,0,0,.35);animation:bdyW .7s ease-out forwards}',
  '#bdy-score{position:absolute;left:10px;bottom:78px;padding:5px 11px;border-radius:999px;background:rgba(15,23,42,.78);color:#fff;font:800 11px system-ui,sans-serif;pointer-events:none;opacity:0;transition:opacity .25s,transform .15s}',
  '#bdy-score.on{opacity:1}'
 ].join('');
 document.head.appendChild(css);

 var layer=document.createElement('div');layer.id='bdy-layer';
 document.documentElement.appendChild(layer);
 var score=document.createElement('div');score.id='bdy-score';layer.appendChild(score);

 window.bdayScoreUpdate=function(){
  var n=(window.bdayServerTotal||0)+(window.bdayPending?window.bdayPending():0);
  if(n>0){score.textContent='🎈 Popped: '+n;score.className='on';}
 };
 window.bdayScoreUpdate();

 var list=[],last=performance.now(),actx=null,popped=0;
 function W(){return window.innerWidth}
 function H(){return window.innerHeight}
 function target(){return W()<520?3:5}
 function rnd(a,b){return a+Math.random()*(b-a)}
 function clampV(v){return Math.max(-1500,Math.min(1500,v))}

 function audio(){
  try{
   if(!actx){var AC=window.AudioContext||window.webkitAudioContext;if(AC)actx=new AC();}
   if(actx&&actx.state==='suspended')actx.resume();
  }catch(e){}
 }
 function sound(){
  if(!actx)return;
  try{
   var n=Math.floor(actx.sampleRate*.09),buf=actx.createBuffer(1,n,actx.sampleRate),d=buf.getChannelData(0);
   for(var i=0;i<n;i++)d[i]=(Math.random()*2-1)*Math.pow(1-i/n,3);
   var src=actx.createBufferSource();src.buffer=buf;
   var g=actx.createGain();g.gain.value=.55;
   src.connect(g);g.connect(actx.destination);src.start();
  }catch(e){}
 }
 function tone(f1,f2,d,v){
  if(!actx)return;
  try{
   var o=actx.createOscillator(),g=actx.createGain(),t=actx.currentTime;
   o.type='sine';o.frequency.setValueAtTime(f1,t);o.frequency.exponentialRampToValueAtTime(f2,t+d*.8);
   g.gain.setValueAtTime(.0001,t);g.gain.exponentialRampToValueAtTime(v,t+.02);g.gain.exponentialRampToValueAtTime(.0001,t+d);
   o.connect(g);g.connect(actx.destination);o.start(t);o.stop(t+d+.02);
  }catch(e){}
 }

 function fly(el,dx,dy,r,ms){
  layer.appendChild(el);
  requestAnimationFrame(function(){requestAnimationFrame(function(){
   el.style.transform='translate('+dx+'px,'+dy+'px) rotate('+r+'deg)';el.style.opacity='0';
  });});
  setTimeout(function(){el.remove();},ms+80);
 }

 function pop(b){
  var i=list.indexOf(b);if(i<0)return;
  list.splice(i,1);
  b.drag=false;b.press=false;
  var cx=b.x+b.w/2,cy=b.y+b.h*.45,k,p,a,d;
  b.el.remove();
  /* shockwave ring */
  var rg=document.createElement('i');rg.className='bdy-r';
  rg.style.left=cx+'px';rg.style.top=cy+'px';rg.style.borderColor=b.c2;
  layer.appendChild(rg);
  requestAnimationFrame(function(){requestAnimationFrame(function(){rg.style.transform='translate(-50%,-50%) scale(6)';rg.style.opacity='0';});});
  setTimeout(function(){rg.remove();},600);
  /* latex shards */
  for(k=0;k<9;k++){
   p=document.createElement('i');p.className='bdy-p';
   p.style.cssText='left:'+cx+'px;top:'+cy+'px;width:'+rnd(10,20)+'px;height:'+rnd(8,14)+'px;background:'+b.c+';border-radius:0 100% 0 100%;opacity:1;transition:transform 1s cubic-bezier(.15,.8,.3,1),opacity 1s ease;';
   a=Math.random()*6.283;d=rnd(50,140);
   fly(p,Math.cos(a)*d,Math.sin(a)*d+70,rnd(-360,360),1000);
  }
  /* confetti */
  for(k=0;k<16;k++){
   p=document.createElement('i');p.className='bdy-p';
   var sz=rnd(5,9);
   p.style.cssText='left:'+cx+'px;top:'+cy+'px;width:'+sz+'px;height:'+(sz*1.5)+'px;background:'+(k%3?b.c2:'#fde047')+';opacity:1;transition:transform .9s cubic-bezier(.15,.8,.3,1),opacity .9s ease;';
   a=Math.random()*6.283;d=rnd(40,130);
   fly(p,Math.cos(a)*d,Math.sin(a)*d+50,rnd(0,720),900);
  }
  var w=document.createElement('div');w.className='bdy-w';w.textContent='POP! +1';
  w.style.left=cx+'px';w.style.top=cy+'px';
  layer.appendChild(w);setTimeout(function(){w.remove();},750);
  sound();
  try{if(navigator.vibrate)navigator.vibrate([12,20,12]);}catch(e){}
  popped++;
  if(window.bdayReportPop)window.bdayReportPop(1);
  score.style.transform='scale(1.2)';setTimeout(function(){score.style.transform='';},160);
  if(popped%10===0&&window.bdayBurst)window.bdayBurst();
 }

 function attach(b){
  var el=b.el,ox=0,oy=0,sx=0,sy=0,t0=0,moved=false,lt=0,lx=0,ly=0;
  el.addEventListener('contextmenu',function(e){e.preventDefault();});
  el.addEventListener('pointerdown',function(e){
   e.preventDefault();
   try{el.setPointerCapture(e.pointerId);}catch(_){}
   audio();
   var r=el.getBoundingClientRect();
   b.inn.style.transformOrigin=Math.max(0,Math.min(100,(e.clientX-r.left)/r.width*100)).toFixed(0)+'% '+Math.max(0,Math.min(100,(e.clientY-r.top)/r.height*100)).toFixed(0)+'%';
   b.drag=true;b.press=true;b.hold=0;moved=false;
   el.classList.add('drag');
   b.touch=performance.now();t0=b.touch;b.t0=t0;lt=t0;
   ox=e.clientX-b.x;oy=e.clientY-b.y;sx=lx=e.clientX;sy=ly=e.clientY;b.vx=0;b.vy=0;
   tone(260,520,.14,.07);
   try{if(navigator.vibrate)navigator.vibrate(8);}catch(_){}
  });
  el.addEventListener('pointermove',function(e){
   if(!b.drag)return;
   var now=performance.now(),dt=Math.max(.008,(now-lt)/1000);
   if(!moved&&Math.hypot(e.clientX-sx,e.clientY-sy)>8){moved=true;b.press=false;b.sqv=-2;}
   if(moved){
    b.x=e.clientX-ox;b.y=e.clientY-oy;
    b.vx=b.vx*.5+((e.clientX-lx)/dt)*.5;
    b.vy=b.vy*.5+((e.clientY-ly)/dt)*.5;
   }
   lt=now;lx=e.clientX;ly=e.clientY;
  });
  function up(e,cancel){
   if(!b.drag)return;
   var now=performance.now(),held=now-t0;
   b.drag=false;b.press=false;el.classList.remove('drag');b.touch=now;
   try{el.releasePointerCapture(e.pointerId);}catch(_){}
   if(cancel){b.sqv=2;return;}
   if(!moved){
    if(held<320){pop(b);return;}          /* quick tap = POP */
    /* pressed & released = boop away with jelly wobble */
    var r=el.getBoundingClientRect();
    var dx=(e.clientX-(r.left+r.width/2))/(r.width/2),dy=(e.clientY-(r.top+r.height/2))/(r.height/2);
    b.vx-=dx*240;b.vy-=dy*240+30;b.sqv=-5;
    tone(220,120,.28,.08);
    return;
   }
   if(now-lt>90){b.vx*=.2;b.vy*=.2;}
   b.vx=clampV(b.vx);b.vy=clampV(b.vy);
  }
  el.addEventListener('pointerup',function(e){up(e,false);});
  el.addEventListener('pointercancel',function(e){up(e,true);});
 }

 function spawn(fromBottom){
  if(list.length>=18)return;
  var c=COLS[Math.floor(Math.random()*COLS.length)];
  var s=Math.round(rnd(96,120)*(W()<520?.9:1.05));
  var h=Math.round(s*1.2);
  var el=document.createElement('div');el.className='bdy-b bdy-x'+Math.floor(Math.random()*4);
  el.style.cssText='width:'+s+'px;height:'+h+'px;--s:'+s+'px;--c:'+c[0]+';--c2:'+c[1]+';--c3:'+c[2]+';';
  el.innerHTML='<div class="bdy-in"><div class="bdy-body"><div class="bdy-tex"></div></div><div class="bdy-knot"></div>'+
   '<div class="bdy-txt"><i class="t1">HAPPY BIRTHDAY</i><b class="t3">DENZ</b><em class="t4">BIRTHDAY GIRL</em><em class="t5">2 OCT</em></div></div>'+
   '<svg class="bdy-str" viewBox="0 0 20 80" preserveAspectRatio="none"><path d="M10 0 C1 14 19 27 10 40 S1 66 10 80" fill="none" stroke="rgba(120,130,150,.8)" stroke-width="1.3" stroke-linecap="round"/></svg>';
  layer.appendChild(el);
  var b={el:el,inn:el.querySelector('.bdy-in'),str:el.querySelector('.bdy-str'),c:c[0],c2:c[1],w:s,h:h,
   x:rnd(0,Math.max(1,W()-s)),
   y:fromBottom?H()+rnd(10,80):rnd(H()*.1,Math.max(H()*.1+1,H()*.7)),
   vx:rnd(-20,20),vy:-rnd(25,60),rot:0,ph:rnd(0,6.28),sw:rnd(.6,1.4),
   sq:0,sqv:0,press:false,hold:0,t0:0,drag:false,touch:-1e9};
  attach(b);list.push(b);
 }

 function tick(now){
  var dt=Math.min(.033,(now-last)/1000);last=now;
  var w=W(),h=H(),t=now/1000,i,j,b,toPop=[];

  for(i=0;i<list.length;i++){
   b=list[i];
   /* squish: pressed = squeeze toward finger; released = spring w/ overshoot */
   if(b.press){
    b.hold=(now-b.t0)/1000;
    var pr=Math.min(1,b.hold/1.0);
    b.sq+=((.08+.34*pr)-b.sq)*Math.min(1,14*dt);b.sqv=0;
    if(b.hold>=1)toPop.push(b);                 /* held too long = POP */
   }else{
    b.sqv+=(-170*b.sq-9*b.sqv)*dt;b.sq+=b.sqv*dt;
    if(b.sq>.5)b.sq=.5;else if(b.sq<-.5)b.sq=-.5;
   }
   if(b.drag)continue;
   b.vy+=(-38-b.vy)*.7*dt;
   b.vx+=Math.sin(t*b.sw+b.ph)*26*dt;
   b.vx*=1-.6*dt;
   b.x+=b.vx*dt;b.y+=b.vy*dt;
   var recent=now-b.touch<6000;
   if(b.x<0){b.x=0;b.sqv+=Math.min(4,Math.abs(b.vx)*.012);b.vx=Math.abs(b.vx)*.8;}
   else if(b.x>w-b.w){b.x=w-b.w;b.sqv+=Math.min(4,Math.abs(b.vx)*.012);b.vx=-Math.abs(b.vx)*.8;}
   if(recent&&b.y>h-b.h){b.y=h-b.h;b.sqv+=Math.min(4,Math.abs(b.vy)*.012);b.vy=-Math.abs(b.vy)*.6;}
   if(recent&&b.y<0){b.y=0;b.vy=Math.abs(b.vy)*.6;}
   else if(b.y<-b.h-60){b.y=h+20;b.x=rnd(0,Math.max(1,w-b.w));b.vx=rnd(-20,20);b.vy=-rnd(25,60);}
  }

  for(i=0;i<toPop.length;i++)pop(toPop[i]);

  for(i=0;i<list.length;i++){
   for(j=i+1;j<list.length;j++){
    var a=list[i],c=list[j];
    var dx=(c.x+c.w/2)-(a.x+a.w/2),dy=(c.y+c.h*.45)-(a.y+a.h*.45);
    var d=Math.sqrt(dx*dx+dy*dy)||.01,min=(a.w+c.w)*.48;
    if(d>=min)continue;
    var ma=a.drag?0:1,mc=c.drag?0:1,ms=ma+mc;
    if(!ms)continue;
    var nx=dx/d,ny=dy/d,ov=min-d;
    a.x-=nx*ov*ma/ms;a.y-=ny*ov*ma/ms;
    c.x+=nx*ov*mc/ms;c.y+=ny*ov*mc/ms;
    var rv=(c.vx-a.vx)*nx+(c.vy-a.vy)*ny;
    if(rv<0){
     var jn=-(1.75)*rv/ms;
     a.vx-=jn*ma*nx;a.vy-=jn*ma*ny;
     c.vx+=jn*mc*nx;c.vy+=jn*mc*ny;
     var kick=Math.min(5,-rv*.012);a.sqv+=kick;c.sqv+=kick;
    }
   }
  }

  for(i=0;i<list.length;i++){
   var q=list[i];
   if(!q.drag){q.vx=clampV(q.vx);q.vy=clampV(q.vy);}
   q.rot+=(Math.max(-22,Math.min(22,q.vx*.04))-q.rot)*Math.min(1,8*dt);
   var sxs=1+q.sq*.6+.012*Math.sin(t*2.1+q.ph),sys=1-q.sq*.75+.012*Math.cos(t*2.1+q.ph);
   var jit=(q.press&&q.hold>.5)?Math.sin(t*70)*Math.min(1,(q.hold-.5)*2.2)*3:0;
   q.inn.style.transform='scale('+sxs.toFixed(3)+','+sys.toFixed(3)+') rotate('+jit.toFixed(1)+'deg)';
   q.el.style.transform='translate3d('+q.x.toFixed(1)+'px,'+q.y.toFixed(1)+'px,0) rotate('+q.rot.toFixed(1)+'deg)'+(q.drag?' scale(1.04)':'');
   q.str.style.transform='rotate('+(Math.max(-28,Math.min(28,-q.vx*.05))+Math.sin(t*1.7+q.ph)*4).toFixed(1)+'deg)';
  }
  requestAnimationFrame(tick);
 }

 var ob=window.bdayBurst;
 window.bdayBurst=function(){if(ob)ob();if(list.length<target()+2)spawn(true);};
 window.bdayBalloons=function(n){for(var i=0;i<(n||3);i++)spawn(true);};

 for(var n=0;n<target();n++)spawn(false);
 setInterval(function(){if(!document.hidden&&list.length<target())spawn(true);},2500);
 requestAnimationFrame(tick);
})();
</script>`
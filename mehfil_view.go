package main

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// =============================================================================
// FLAT COMMENT STRUCT — matches every column we SELECT in GetMehfilMessages
// =============================================================================

type FlatComment struct {
	ID             int
	Author         string
	Content        string
	ImageUrl       string
	CreatedAt      time.Time
	ParentID       *int
	ParentAuthor   *string
	ParentSnippet  *string
	Points         int
	ProfileImage   string
	Reactions      string
	IsPinned       bool
	IsAnnouncement bool
	EditedAt       *time.Time
}

// =============================================================================
// TIME + ROLE HELPERS
// =============================================================================

func formatTimeAgo(t time.Time) string {
	loc, err := time.LoadLocation("Asia/Karachi")
	if err != nil {
		loc = time.FixedZone("PKT", 5*60*60)
	}
	now := time.Now().In(loc)
	then := t.In(loc)
	diff := now.Unix() - then.Unix()
	if diff < 15 {
		return "Abhi"
	}
	if diff < 60 {
		return fmt.Sprintf("%ds", diff)
	}
	if diff < 3600 {
		return fmt.Sprintf("%dm", diff/60)
	}
	if diff < 86400 {
		return fmt.Sprintf("%dh", diff/3600)
	}
	days := int(diff / 86400)
	if days < 7 {
		return fmt.Sprintf("%dd", days)
	}
	if days < 30 {
		return fmt.Sprintf("%dw", days/7)
	}
	return fmt.Sprintf("%dmo", days/30)
}

func getRoleEmoji(role string) string {
	switch role {
	case "admin":
		return "👑"
	case "afsar":
		return "⭐"
	case "sipahi":
		return "🛡️"
	default:
		return "👤"
	}
}

func getRoleLabel(role string) string {
	switch role {
	case "admin":
		return "Admin"
	case "afsar":
		return "Afsar"
	case "sipahi":
		return "Sipahi"
	default:
		return "Member"
	}
}

// avatarHTML renders either an <img> or an initial-letter div.
func avatarHTML(profileImage, author string, size int) string {
	ss := fmt.Sprintf("width:%dpx;height:%dpx;", size, size)
	if profileImage != "" && profileImage != "/static/default-pfp.png" {
		return fmt.Sprintf(
			`<img src="%s" style="%s flex-shrink:0;border-radius:12px;object-fit:cover;" alt="pfp">`,
			profileImage, ss)
	}
	initial := "?"
	if len(author) > 0 {
		initial = strings.ToUpper(string([]rune(author)[0]))
	}
	return fmt.Sprintf(
		`<div style="%s flex-shrink:0;border-radius:12px;background:linear-gradient(135deg,#10b981,#059669);display:flex;align-items:center;justify-content:center;color:white;font-weight:900;font-size:%dpx;">%s</div>`,
		ss, size/3, initial)
}

// RenderMehfilCard renders a small mehfil preview card used on profile pages,
// popular listings, etc.

// =============================================================================
// SHARED CSS
// =============================================================================

const mehfilCSS = `
<style>
:root {
  --mf-bg:#f9fafb; --mf-card:#ffffff; --mf-border:#e5e7eb;
  --mf-text:#111827; --mf-sub:#6b7280;
  --mf-accent:#10b981; --mf-accent2:#059669;
  --mf-danger:#ef4444; --mf-warn:#f59e0b; --mf-info:#3b82f6;
  --mf-radius:20px; --mf-radius-sm:12px;
}
.dark {
  --mf-bg:#0a0a0a; --mf-card:#111111;
  --mf-border:rgba(255,255,255,0.07);
  --mf-text:#f9fafb; --mf-sub:#9ca3af;
}
.mf-page        { max-width:680px;margin:0 auto;padding:12px 10px 120px; }
.mf-header      { background:var(--mf-card);border:1px solid var(--mf-border);border-radius:28px;padding:20px;margin-bottom:14px;position:relative;overflow:hidden; }
.mf-header-glow { position:absolute;top:-60px;right:-60px;width:200px;height:200px;border-radius:50%;filter:blur(60px);opacity:.15;pointer-events:none; }
.mf-section     { background:var(--mf-card);border:1px solid var(--mf-border);border-radius:var(--mf-radius);padding:16px;margin-bottom:10px; }
.mf-label       { font-size:9px;font-weight:900;letter-spacing:.2em;text-transform:uppercase;color:var(--mf-sub);margin-bottom:8px;display:block; }
.mf-tabs        { display:flex;gap:6px;padding:6px;background:var(--mf-bg);border:1px solid var(--mf-border);border-radius:16px;margin-bottom:12px;overflow-x:auto;-webkit-overflow-scrolling:touch;scrollbar-width:none; }
.mf-tabs::-webkit-scrollbar { display:none; }
.mf-tab         { flex-shrink:0;padding:8px 14px;border-radius:11px;font-size:11px;font-weight:800;border:none;cursor:pointer;background:transparent;color:var(--mf-sub);transition:all .2s;white-space:nowrap; }
.mf-tab.active  { background:var(--mf-accent);color:#fff;box-shadow:0 4px 12px rgba(16,185,129,.3); }
.mf-compose     { background:var(--mf-card);border:1.5px solid var(--mf-border);border-radius:var(--mf-radius);overflow:hidden;margin-bottom:12px;transition:border-color .2s; }
.mf-compose:focus-within { border-color:var(--mf-accent); }
.mf-compose textarea { width:100%;padding:14px 16px;background:transparent;border:none;outline:none;color:var(--mf-text);font-size:14px;resize:none;min-height:72px;line-height:1.5; }
.mf-compose-bar { display:flex;align-items:center;justify-content:space-between;padding:8px 12px;border-top:1px solid var(--mf-border);background:rgba(0,0,0,.02); }
.dark .mf-compose-bar { background:rgba(255,255,255,.02); }
.mf-msg         { background:var(--mf-card);border:1px solid var(--mf-border);border-radius:14px;padding:8px 11px;margin-bottom:6px;transition:border-color .15s;position:relative; }
.hidden         { display:none; }
.mf-msg:hover   { border-color:rgba(16,185,129,.25); }
.mf-msg.pinned  { border-left:3px solid var(--mf-accent);background:rgba(16,185,129,.03); }
.mf-msg.announce{ border-left:3px solid var(--mf-warn);background:rgba(245,158,11,.04); }
.mf-msg-header  { display:flex;align-items:center;gap:8px;margin-bottom:8px; }
.mf-msg-author  { font-size:13px;font-weight:800;color:var(--mf-text);line-height:1; }
.mf-msg-time    { font-size:9px;font-weight:700;color:var(--mf-sub);margin-left:auto; }
.mf-msg-body    { font-size:13px;color:var(--mf-text);line-height:1.55;white-space:pre-wrap;word-break:break-word; }
.mf-msg-img     { margin-top:8px;border-radius:14px;overflow:hidden;border:1px solid var(--mf-border); }
.mf-msg-img img { width:100%;max-height:260px;object-fit:cover;display:block;cursor:pointer; }
.mf-quote       { display:flex;gap:8px;align-items:stretch;margin-bottom:8px; }
.mf-quote-bar   { width:3px;border-radius:3px;background:var(--mf-accent);flex-shrink:0; }
.mf-quote-body  { background:rgba(16,185,129,.06);border-radius:10px;padding:6px 10px;flex:1;min-width:0; }
.mf-quote-author{ font-size:9px;font-weight:900;color:var(--mf-accent);text-transform:uppercase;letter-spacing:.1em; }
.mf-quote-text  { font-size:11px;color:var(--mf-sub);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:260px; }
.mf-badge       { display:inline-flex;align-items:center;gap:3px;padding:2px 7px;border-radius:8px;font-size:8px;font-weight:900;text-transform:uppercase;letter-spacing:.08em;flex-shrink:0; }
.mf-badge-green { background:rgba(16,185,129,.15);color:#059669; }
.mf-badge-amber { background:rgba(245,158,11,.15);color:#d97706; }
.mf-badge-blue  { background:rgba(59,130,246,.15);color:#2563eb; }
.mf-badge-gray  { background:rgba(107,114,128,.12);color:#6b7280; }
.mf-badge-red   { background:rgba(239,68,68,.12);color:#dc2626; }
.dark .mf-badge-green { background:rgba(16,185,129,.2);color:#34d399; }
.dark .mf-badge-amber { background:rgba(245,158,11,.2);color:#fbbf24; }
.dark .mf-badge-blue  { background:rgba(59,130,246,.2);color:#60a5fa; }
.dark .mf-badge-gray  { background:rgba(255,255,255,.08);color:#9ca3af; }
.mf-btn         { display:inline-flex;align-items:center;justify-content:center;gap:5px;padding:8px 14px;border-radius:12px;font-size:11px;font-weight:800;border:none;cursor:pointer;transition:all .2s;white-space:nowrap; }
.mf-btn:active  { transform:scale(.96); }
.mf-btn-green   { background:var(--mf-accent);color:#fff;box-shadow:0 4px 14px rgba(16,185,129,.25); }
.mf-btn-green:hover { background:var(--mf-accent2); }
.mf-btn-gray    { background:var(--mf-bg);color:var(--mf-text);border:1px solid var(--mf-border); }
.mf-btn-gray:hover  { background:var(--mf-border); }
.mf-btn-red     { background:rgba(239,68,68,.1);color:#dc2626;border:1px solid rgba(239,68,68,.2); }
.mf-btn-red:hover   { background:rgba(239,68,68,.18); }
.mf-btn-amber   { background:rgba(245,158,11,.1);color:#d97706;border:1px solid rgba(245,158,11,.2); }
.mf-btn-blue    { background:rgba(59,130,246,.1);color:#2563eb;border:1px solid rgba(59,130,246,.2); }
.mf-btn-sm      { padding:5px 10px;font-size:10px;border-radius:9px; }
.mf-btn-xs      { padding:4px 8px;font-size:9px;border-radius:8px; }
.mf-btn-icon    { padding:7px;border-radius:11px;aspect-ratio:1; }
.mf-action-bar  { display:flex;align-items:center;gap:4px;margin-top:8px;flex-wrap:wrap; }
.mf-stats       { display:flex;gap:14px;flex-wrap:wrap; }
.mf-stat        { display:flex;flex-direction:column;align-items:center; }
.mf-stat-val    { font-size:20px;font-weight:900;color:var(--mf-text);line-height:1; }
.mf-stat-lbl    { font-size:8px;font-weight:800;text-transform:uppercase;letter-spacing:.15em;color:var(--mf-sub);margin-top:2px; }
.mf-member-row  { display:flex;align-items:center;gap:10px;padding:10px 0;border-bottom:1px solid var(--mf-border); }
.mf-member-row:last-child { border-bottom:none; }
.mf-member-name { font-size:13px;font-weight:700;color:var(--mf-text);flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap; }
.mf-member-actions { display:flex;gap:5px;flex-shrink:0; }
.mf-online-dot  { width:7px;height:7px;border-radius:50%;background:#22c55e;flex-shrink:0;box-shadow:0 0 0 2px rgba(34,197,94,.3); }
.mf-offline-dot { width:7px;height:7px;border-radius:50%;background:#6b7280;flex-shrink:0; }
.mf-reactions   { display:flex;gap:4px;flex-wrap:wrap;margin-top:6px; }
.mf-reaction    { display:inline-flex;align-items:center;gap:3px;padding:3px 8px;border-radius:20px;font-size:12px;background:var(--mf-bg);border:1px solid var(--mf-border);cursor:pointer;transition:all .15s;font-weight:700; }
.mf-reaction:hover { border-color:var(--mf-accent);background:rgba(16,185,129,.06); }
.mf-reaction.mine  { border-color:var(--mf-accent);background:rgba(16,185,129,.12); }
.mf-pinned-banner  { display:flex;align-items:center;gap:8px;background:rgba(16,185,129,.07);border:1px solid rgba(16,185,129,.2);border-radius:14px;padding:10px 14px;margin-bottom:12px; }
.mf-announce-banner{ display:flex;align-items:center;gap:8px;background:rgba(245,158,11,.07);border:1px solid rgba(245,158,11,.25);border-radius:14px;padding:10px 14px;margin-bottom:12px; }
.mf-reply-form  { margin-top:10px;padding-top:10px;border-top:1px solid var(--mf-border); }
.mf-reply-input { display:flex;gap:6px;align-items:center; }
.mf-reply-input input { flex:1;background:var(--mf-bg);border:1px solid var(--mf-border);border-radius:12px;padding:8px 12px;font-size:13px;color:var(--mf-text);outline:none;transition:border-color .2s; }
.mf-reply-input input:focus { border-color:var(--mf-accent); }
.mf-panel       { position:fixed;top:0;right:0;width:min(340px,95vw);height:100dvh;background:var(--mf-card);border-left:1px solid var(--mf-border);z-index:200;transform:translateX(100%);transition:transform .3s cubic-bezier(.4,0,.2,1);overflow-y:auto;box-shadow:-8px 0 40px rgba(0,0,0,.12); }
.mf-panel.open  { transform:translateX(0); }
.mf-overlay     { position:fixed;inset:0;background:rgba(0,0,0,.4);z-index:199;opacity:0;pointer-events:none;transition:opacity .3s;backdrop-filter:blur(2px); }
.mf-overlay.open{ opacity:1;pointer-events:auto; }
.mf-panel-head  { position:sticky;top:0;background:var(--mf-card);border-bottom:1px solid var(--mf-border);padding:16px;display:flex;align-items:center;justify-content:space-between;z-index:1; }
.mf-muted-bar   { background:rgba(239,68,68,.08);border:1px solid rgba(239,68,68,.2);border-radius:12px;padding:8px 14px;margin-bottom:10px;font-size:11px;font-weight:700;color:#dc2626;display:flex;align-items:center;gap:6px; }
.mf-locked-bar  { background:rgba(245,158,11,.08);border:1px solid rgba(245,158,11,.25);border-radius:12px;padding:8px 14px;margin-bottom:10px;font-size:11px;font-weight:700;color:#d97706;display:flex;align-items:center;gap:6px; }
.mf-spin        { width:20px;height:20px;border:2px solid var(--mf-border);border-top-color:var(--mf-accent);border-radius:50%;animation:mf-spin .7s linear infinite; }
@keyframes mf-spin { to { transform:rotate(360deg); } }
.mf-feed        { scrollbar-width:thin;scrollbar-color:var(--mf-border) transparent; }
.mf-welcome     { text-align:center;padding:24px 16px;opacity:.5; }
@media(max-width:390px){
  .mf-page{padding:8px 6px 110px;}
  .mf-header{padding:14px;}
  .mf-msg-body{font-size:13px;}
  .mf-btn{padding:7px 11px;font-size:10px;}
}
</style>`

// =============================================================================
// SHARED JS
// =============================================================================

const mehfilJS = `
<script>
function mfOpenPanel(id){
  document.getElementById(id).classList.add('open');
  document.getElementById('mf-overlay').classList.add('open');
}
function mfClosePanel(){
  document.querySelectorAll('.mf-panel').forEach(p=>p.classList.remove('open'));
  var ov=document.getElementById('mf-overlay');
  if(ov) ov.classList.remove('open');
}
document.addEventListener('keydown',e=>{if(e.key==='Escape')mfClosePanel();});

function mfShowChat(){
  document.getElementById('mf-chat-wrap').classList.remove('hidden');
  document.getElementById('mf-detail-wrap').classList.add('hidden');
  document.getElementById('mf-tab-chat').classList.add('active');
  document.getElementById('mf-tab-info').classList.remove('active');
}
function mfShowInfo(){
  document.getElementById('mf-chat-wrap').classList.add('hidden');
  document.getElementById('mf-detail-wrap').classList.remove('hidden');
  document.getElementById('mf-tab-info').classList.add('active');
  document.getElementById('mf-tab-chat').classList.remove('active');
}

function toggleMehfilReply(id){
  var form=document.getElementById('rf-'+id);
  var poller=document.getElementById('mf-poller');
  if(!form) return;
  var opening=form.classList.contains('hidden');
  document.querySelectorAll('.mf-reply-form').forEach(f=>f.classList.add('hidden'));
  if(opening){
    form.classList.remove('hidden');
    var inp=form.querySelector('input');
    if(inp) inp.focus();
    if(poller){poller.removeAttribute('hx-trigger');htmx.process(poller);}
  } else {
    if(poller){poller.setAttribute('hx-trigger','every 8s');htmx.process(poller);}
  }
}

document.addEventListener('htmx:beforeRequest',function(e){
  var active=document.querySelector('.mf-reply-form:not(.hidden)');
  if(active && e.detail.requestConfig && e.detail.requestConfig.method==='GET'
     && e.detail.target && e.detail.target.id==='mf-feed'){
    e.preventDefault();
  }
});

document.addEventListener('htmx:afterRequest',function(e){
  var t=e.detail.target;
  if(t && t.id==='mf-feed' && e.detail.requestConfig &&
     (e.detail.requestConfig.method==='POST'||e.detail.requestConfig.method==='PUT')){
    var poller=document.getElementById('mf-poller');
    if(poller){
      poller.removeAttribute('hx-trigger');htmx.process(poller);
      setTimeout(()=>{poller.setAttribute('hx-trigger','every 8s');htmx.process(poller);},100);
    }
  }
});

function mfLightbox(src){
  var d=document.createElement('div');
  d.style.cssText='position:fixed;inset:0;background:rgba(0,0,0,.88);z-index:999;display:flex;align-items:center;justify-content:center;cursor:zoom-out;backdrop-filter:blur(4px);';
  d.innerHTML='<img src="'+src+'" style="max-width:96vw;max-height:92dvh;border-radius:16px;box-shadow:0 30px 80px rgba(0,0,0,.6);">';
  d.onclick=()=>d.remove();
  document.body.appendChild(d);
}

function mfReactPicker(msgId){
  var existing=document.getElementById('rpick-'+msgId);
  if(existing){existing.remove();return;}
  var emojis=['❤️','😂','😮','😢','👍','🔥','🙏','💯'];
  var div=document.createElement('div');
  div.id='rpick-'+msgId;
  div.style.cssText='position:absolute;bottom:calc(100% + 6px);left:0;background:var(--mf-card);border:1px solid var(--mf-border);border-radius:50px;padding:6px 10px;display:flex;gap:6px;z-index:50;box-shadow:0 8px 32px rgba(0,0,0,.15);';
  emojis.forEach(em=>{
    var b=document.createElement('button');
    b.textContent=em;
    b.style.cssText='font-size:20px;background:none;border:none;cursor:pointer;transition:transform .15s;line-height:1;';
    b.onmouseenter=()=>b.style.transform='scale(1.35)';
    b.onmouseleave=()=>b.style.transform='scale(1)';
    b.onclick=()=>{
      htmx.ajax('POST','/mehfil/react/'+msgId,{values:{emoji:em},swap:'none'});
      div.remove();
      var box=document.querySelector('#mf-reactions-'+msgId);
      if(box){
        var ex=[...box.querySelectorAll('.mf-reaction')].find(r=>r.dataset.emoji===em);
        if(ex){var c=ex.querySelector('.mf-rc');if(c)c.textContent=parseInt(c.textContent||0)+1;}
        else{var r=document.createElement('span');r.className='mf-reaction mine';r.dataset.emoji=em;r.innerHTML=em+' <span class="mf-rc">1</span>';box.appendChild(r);}
      }
    };
    div.appendChild(b);
  });
  var card=document.getElementById('msg-'+msgId);
  if(card){card.style.position='relative';card.appendChild(div);}
  setTimeout(()=>document.addEventListener('click',()=>div.remove(),{once:true}),100);
}

function mfCopy(text){
  navigator.clipboard.writeText(text).then(()=>{
    var t=document.createElement('div');
    t.textContent='Copied!';
    t.style.cssText='position:fixed;bottom:90px;left:50%;transform:translateX(-50%);background:#111;color:#fff;padding:8px 20px;border-radius:50px;font-size:12px;font-weight:800;z-index:999;pointer-events:none;';
    document.body.appendChild(t);
    setTimeout(()=>{t.style.opacity='0';setTimeout(()=>t.remove(),400);},1500);
  });
}

function mfSearchMembers(val){
  document.querySelectorAll('.mf-member-row').forEach(row=>{
    var name=row.dataset.name||'';
    row.style.display=name.toLowerCase().includes(val.toLowerCase())?'':'none';
  });
}

document.addEventListener('input',e=>{
  if(e.target.tagName==='TEXTAREA'){
    e.target.style.height='auto';
    e.target.style.height=Math.min(e.target.scrollHeight,200)+'px';
  }
});
</script>`

// =============================================================================
// VIEW PAGE — full mehfil page handler
// =============================================================================

// =============================================================================
// VIEW PAGE — full mehfil page handler
// =============================================================================

func ViewPage(c *fiber.Ctx) error {
	id := c.Params("id")
	currentUser := getCurrentUser(c)

	var topic, rules, admin, welcomeMsg, mehfilColor string
	var joiningFee, maxMembers int
	var isLocked bool
	var adminPfp string
	var adminPoints int
	var createdAt time.Time
	var memberCount, msgCount int

	err := db.QueryRow(ctx, `
		SELECT m.topic, COALESCE(m.rules,''), m.admin_username,
		       COALESCE(m.joining_fee,0),
		       COALESCE(m.welcome_msg,''),
		       COALESCE(m.mehfil_color,'#10b981'),
		       COALESCE(m.max_members,0),
		       COALESCE(m.is_locked,false),
		       m.created_at,
		       COALESCE(u.profile_image,''), COALESCE(u.points,0),
		       (SELECT COUNT(*) FROM mehfil_members WHERE mehfil_id=m.id),
		       (SELECT COUNT(*) FROM mehfil_messages WHERE mehfil_id=m.id AND is_hidden=false)
		FROM mehfils m JOIN users u ON m.admin_username=u.username
		WHERE m.id=$1`, id).
		Scan(&topic, &rules, &admin, &joiningFee, &welcomeMsg, &mehfilColor,
			&maxMembers, &isLocked, &createdAt,
			&adminPfp, &adminPoints, &memberCount, &msgCount)
	if err != nil {
		return c.Redirect("/popular")
	}

	topic = html.EscapeString(topic)
	rules = html.EscapeString(rules)

	// ── Viewer role ────────────────────────────────────────────────────────────
	var isMember bool
	var viewerRole string
	isAdmin := currentUser == admin

	if currentUser != "" && !isAdmin {
		db.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM mehfil_members WHERE mehfil_id=$1 AND username=$2)",
			id, currentUser).Scan(&isMember)
		if isMember {
			db.QueryRow(ctx,
				"SELECT COALESCE(role,'member') FROM mehfil_members WHERE mehfil_id=$1 AND username=$2",
				id, currentUser).Scan(&viewerRole)
		}
	}
	if isAdmin {
		viewerRole = "admin"
		isMember = true
	}

	canPost := (isMember || isAdmin) && !isLocked
	canModerate := viewerRole == "admin" || viewerRole == "afsar" || viewerRole == "sipahi"

	// ── Mute check ─────────────────────────────────────────────────────────────
	var mutedUntil *time.Time
	if currentUser != "" {
		var mu time.Time
		if db.QueryRow(ctx,
			"SELECT muted_until FROM mehfil_members WHERE mehfil_id=$1 AND username=$2 AND muted_until > NOW()",
			id, currentUser).Scan(&mu) == nil {
			mutedUntil = &mu
			canPost = false
		}
	}

	// ── Pinned message ──────────────────────────────────────────────────────────
	var pinnedID int
	var pinnedAuthor, pinnedContent string
	hasPinned := db.QueryRow(ctx,
		"SELECT id, author, LEFT(content,80) FROM mehfil_messages WHERE mehfil_id=$1 AND is_pinned=true ORDER BY id DESC LIMIT 1",
		id).Scan(&pinnedID, &pinnedAuthor, &pinnedContent) == nil

	// ── Announcement ───────────────────────────────────────────────────────────
	var announceContent string
	hasAnnounce := db.QueryRow(ctx,
		"SELECT content FROM mehfil_messages WHERE mehfil_id=$1 AND is_announcement=true ORDER BY id DESC LIMIT 1",
		id).Scan(&announceContent) == nil

	// ── Online count ───────────────────────────────────────────────────────────
	var onlineCount int
	db.QueryRow(ctx, `
		SELECT COUNT(*) FROM mehfil_members mm
		JOIN users u ON mm.username=u.username
		WHERE mm.mehfil_id=$1 AND u.last_active > NOW() - INTERVAL '5 minutes'`, id).Scan(&onlineCount)

	// ── Header ──────────────────────────────────────────────────────────────────
	lockedBadge := ""
	if isLocked {
		lockedBadge = `<span class="mf-badge mf-badge-amber">🔒 Locked</span>`
	}
	feeBadge := ""
	if joiningFee > 0 {
		feeBadge = fmt.Sprintf(`<span class="mf-badge mf-badge-blue">💰 %d pts</span>`, joiningFee)
	}
	modBtn := ""
	if canModerate {
		modBtn = `<button class="mf-btn mf-btn-amber mf-btn-sm" onclick="mfOpenPanel('panel-mod')">⚙️ Control</button>`
	}
	leaveBtn := ""
	if isMember && !isAdmin {
		leaveBtn = fmt.Sprintf(
			`<button class="mf-btn mf-btn-red mf-btn-sm" hx-post="/mehfil/leave/%s" hx-confirm="Mehfil chhorein?" hx-swap="none" onclick="setTimeout(()=>window.location='/popular',500)">🚪 Chorho</button>`, id)
	}

	headerSection := fmt.Sprintf(`
	<div class="mf-header">
		<div class="mf-header-glow" style="background:%s;"></div>
		<div style="display:flex;align-items:flex-start;justify-content:space-between;gap:10px;margin-bottom:14px;">
			<div style="flex:1;min-width:0;">
				<span class="mf-label">Mehfil</span>
				<h1 id="mehfil-title" style="font-size:clamp(22px,5vw,34px);font-weight:900;line-height:1.1;color:var(--mf-text);margin:0 0 6px;word-break:break-word;">%s</h1>
				<div style="display:flex;flex-wrap:wrap;gap:5px;align-items:center;">
					<span class="mf-badge mf-badge-green">🟢 %d online</span>
					<span class="mf-badge mf-badge-gray">👥 %d andar</span>
					<span class="mf-badge mf-badge-gray">💬 %d baatein</span>
					%s %s
				</div>
			</div>
			<a href="/profile/%s" style="flex-shrink:0;display:flex;align-items:center;gap:8px;background:var(--mf-bg);border:1px solid var(--mf-border);border-radius:14px;padding:8px 10px;text-decoration:none;min-width:0;max-width:130px;">
				%s
				<div style="min-width:0;">
					<div style="font-size:8px;font-weight:900;color:var(--mf-sub);text-transform:uppercase;letter-spacing:.12em;">Admin</div>
					<div style="font-size:12px;font-weight:800;color:var(--mf-text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">@%s</div>
					<div style="font-size:9px;color:var(--mf-accent);font-weight:700;">%d pts</div>
				</div>
			</a>
		</div>
		<div style="font-size:10px;color:var(--mf-sub);font-weight:600;margin-bottom:10px;">Bana: %s</div>
		<div style="background:var(--mf-bg);border-radius:14px;padding:12px;margin-bottom:12px;border:1px solid var(--mf-border);">
			<div class="mf-label" style="margin-bottom:4px;">📜 Usool o Zawaabit</div>
			<p id="rules-text" style="font-size:13px;color:var(--mf-sub);line-height:1.5;margin:0;font-style:italic;">%s</p>
		</div>
		<div style="display:flex;gap:6px;flex-wrap:wrap;align-items:center;">
			<div style="display:flex;gap:5px;flex:1;flex-wrap:wrap;">
				<button class="mf-btn mf-btn-gray mf-btn-sm" onclick="mfOpenPanel('panel-members')">👥 Members</button>
				<button class="mf-btn mf-btn-gray mf-btn-sm" onclick="mfOpenPanel('panel-info')">ℹ️ Info</button>
				%s
			</div>
			%s
		</div>
	</div>`,
		mehfilColor, topic,
		onlineCount, memberCount, msgCount,
		lockedBadge, feeBadge,
		admin, avatarHTML(adminPfp, admin, 36), admin, adminPoints,
		createdAt.Format("2 Jan 2006"),
		rules,
		modBtn, leaveBtn,
	)

	// ── Banners ─────────────────────────────────────────────────────────────────
	banners := ""
	if hasAnnounce {
		banners += fmt.Sprintf(`
		<div class="mf-announce-banner">
			<span style="font-size:18px;">📢</span>
			<div>
				<div style="font-size:8px;font-weight:900;text-transform:uppercase;color:#d97706;letter-spacing:.12em;margin-bottom:2px;">Izlamiey</div>
				<div style="font-size:12px;font-weight:600;color:var(--mf-text);">%s</div>
			</div>
		</div>`, html.EscapeString(announceContent))
	}
	if hasPinned {
		ellipsis := ""
		if len(pinnedContent) >= 80 {
			ellipsis = "…"
		}
		banners += fmt.Sprintf(`
		<div class="mf-pinned-banner" style="cursor:pointer;"
		     onclick="var el=document.getElementById('msg-%d');if(el)el.scrollIntoView({behavior:'smooth',block:'center'});">
			<span style="font-size:18px;">📌</span>
			<div>
				<div style="font-size:8px;font-weight:900;text-transform:uppercase;color:var(--mf-accent);letter-spacing:.12em;margin-bottom:2px;">Pinned — @%s</div>
				<div style="font-size:12px;font-weight:600;color:var(--mf-text);">%s%s</div>
			</div>
		</div>`, pinnedID, pinnedAuthor, pinnedContent, ellipsis)
	}
	if mutedUntil != nil {
		loc, _ := time.LoadLocation("Asia/Karachi")
		banners += fmt.Sprintf(`<div class="mf-muted-bar">🔇 Ap muted hain — %s tak</div>`,
			mutedUntil.In(loc).Format("15:04"))
	}
	if isLocked && !isAdmin {
		banners += `<div class="mf-locked-bar">🔒 Yeh Mehfil philhal band hai. Sirf Admin post kar sakta hai.</div>`
	}

	// ── Compose / Join ─────────────────────────────────────────────────────────
	composeSection := ""
	if canPost {
		composeSection = fmt.Sprintf(`
		<div class="mf-compose" id="compose-box">
			<form hx-post="/mehfil/send/%s"
			      hx-encoding="multipart/form-data"
			      hx-target="#mf-feed"
			      hx-swap="afterbegin"
			      onsubmit="setTimeout(()=>{this.reset();document.getElementById('img-label').textContent='📷 Tasveeer';},80)">
				<textarea name="content" placeholder="Kuch share karein..." rows="2"
					style="width:100%%;padding:14px 16px 6px;background:transparent;border:none;outline:none;color:var(--mf-text);font-size:14px;resize:none;min-height:64px;line-height:1.5;box-sizing:border-box;"></textarea>
				<div class="mf-compose-bar">
					<label style="cursor:pointer;">
						<input type="file" name="image" accept="image/*" class="hidden"
						       onchange="document.getElementById('img-label').textContent='✅ '+this.files[0].name.substring(0,18)">
						<span id="img-label" class="mf-btn mf-btn-gray mf-btn-sm" style="pointer-events:none;">📷 Tasveeer</span>
					</label>
					<button type="submit" class="mf-btn mf-btn-green mf-btn-sm">Bhejo ➤</button>
				</div>
			</form>
		</div>`, id)
	} else if !isMember && !isAdmin {
		composeSection = buildJoinSection(id, joiningFee, maxMembers, memberCount, currentUser)
	}

	// ── Online strip (scrollable avatars under compose) ────────────────────────
	type onlineUser struct{ Username, Pfp string }
	var onlineUsers []onlineUser
	if orows, _ := db.Query(ctx, `
		SELECT mm.username, COALESCE(u.profile_image,'')
		FROM mehfil_members mm JOIN users u ON mm.username=u.username
		WHERE mm.mehfil_id=$1 AND u.last_active > NOW() - INTERVAL '5 minutes'
		ORDER BY u.last_active DESC LIMIT 40`, id); orows != nil {
		for orows.Next() {
			var ou onlineUser
			orows.Scan(&ou.Username, &ou.Pfp)
			onlineUsers = append(onlineUsers, ou)
		}
		orows.Close()
	}
	onlineStrip := ""
	if len(onlineUsers) > 0 {
		var ob strings.Builder
		ob.WriteString(`<div style="display:flex;gap:10px;overflow-x:auto;padding:6px 2px 10px;-webkit-overflow-scrolling:touch;scrollbar-width:none;">`)
		for _, ou := range onlineUsers {
			ob.WriteString(fmt.Sprintf(`
			<a href="/profile/%s" style="flex-shrink:0;display:flex;flex-direction:column;align-items:center;gap:3px;text-decoration:none;width:42px;">
				<div style="position:relative;">%s<span style="position:absolute;bottom:-1px;right:-1px;width:9px;height:9px;border-radius:50%%;background:#22c55e;border:2px solid var(--mf-card);"></span></div>
				<span style="font-size:8px;font-weight:700;color:var(--mf-sub);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:42px;">%s</span>
			</a>`, ou.Username, avatarHTML(ou.Pfp, ou.Username, 34), ou.Username))
		}
		ob.WriteString(`</div>`)
		onlineStrip = fmt.Sprintf(`<span class="mf-label" style="margin-bottom:2px;">🟢 Online (%d)</span>%s`, len(onlineUsers), ob.String())
	}

	// ── Inline "Tafseel" (details) view — swaps in place of the chat ───────────
	detailInline := fmt.Sprintf(`
	<div class="mf-stats" style="justify-content:space-around;margin-bottom:16px;">
		<div class="mf-stat"><span class="mf-stat-val">%d</span><span class="mf-stat-lbl">Members</span></div>
		<div class="mf-stat"><span class="mf-stat-val">%d</span><span class="mf-stat-lbl">Online</span></div>
		<div class="mf-stat"><span class="mf-stat-val">%d</span><span class="mf-stat-lbl">Msgs</span></div>
	</div>
	<div style="margin-bottom:10px;"><span class="mf-label">Topic</span><div style="font-size:14px;font-weight:800;">%s</div></div>
	<div style="margin-bottom:10px;"><span class="mf-label">Usool</span><div style="font-size:13px;color:var(--mf-sub);font-style:italic;">%s</div></div>
	<div><span class="mf-label">Admin</span> <a href="/profile/%s" style="font-size:13px;font-weight:800;color:var(--mf-accent);text-decoration:none;">@%s</a></div>`,
		memberCount, onlineCount, msgCount, topic, rules, admin, admin)

	// ── Chat / Tafseel tabs ──────────────────────────────────────────────────────
	feedSection := fmt.Sprintf(`
	<div class="mf-tabs" style="margin-bottom:8px;">
		<button id="mf-tab-chat" class="mf-tab active" onclick="mfShowChat()">💬 Baatein</button>
		<button id="mf-tab-info" class="mf-tab" onclick="mfShowInfo()">ℹ️ Tafseel</button>
	</div>
	<div id="mf-chat-wrap">
		%s
		<div id="mf-feed" class="mf-feed"
		     hx-get="/mehfil/messages/%s" hx-trigger="load" hx-swap="innerHTML">
			<div class="mf-welcome"><div class="mf-spin" style="margin:0 auto;"></div></div>
		</div>
		<div id="mf-poller" class="hidden"
		     hx-get="/mehfil/messages/%s" hx-trigger="every 8s"
		     hx-target="#mf-feed" hx-swap="innerHTML"></div>
	</div>
	<div id="mf-detail-wrap" class="mf-section hidden">%s</div>`, onlineStrip, id, id, detailInline)

	// ── Panels ─────────────────────────────────────────────────────────────────
	membersPanel := buildMembersPanel(id, admin, currentUser, viewerRole)
	infoPanel := buildInfoPanel(id, topic, rules, admin, joiningFee, maxMembers,
		memberCount, msgCount, onlineCount, createdAt, welcomeMsg, mehfilColor)
	adminPanel := buildAdminControlPanel(id, isAdmin, viewerRole, joiningFee, isLocked,
		joiningFee, mehfilColor, maxMembers)
	invitePanel := ""
	if canModerate {
		invitePanel = buildInvitePanel(id, currentUser)
	}

	page := fmt.Sprintf(`
	%s
	<div class="mf-page">
		%s
		%s
		%s
		%s
	</div>
	<div id="mf-overlay" class="mf-overlay" onclick="mfClosePanel()"></div>
	%s %s %s %s
	%s`,
		mehfilCSS,
		headerSection, banners, composeSection, feedSection,
		membersPanel, infoPanel, adminPanel, invitePanel,
		mehfilJS)

	return c.Type("html").SendString(Layout(topic, page, currentUser))
}

// =============================================================================
// JOIN SECTION
// =============================================================================

func buildJoinSection(id string, fee, maxMembers, memberCount int, currentUser string) string {
	if currentUser == "" {
		return `<div class="mf-section" style="text-align:center;padding:24px;">
			<div style="font-size:36px;margin-bottom:8px;">🔒</div>
			<div style="font-size:13px;font-weight:700;color:var(--mf-sub);">Login karein join karne ke liye</div>
		</div>`
	}
	if maxMembers > 0 && memberCount >= maxMembers {
		return fmt.Sprintf(`<div class="mf-section" style="text-align:center;padding:24px;">
			<div style="font-size:36px;margin-bottom:8px;">🚫</div>
			<div style="font-size:13px;font-weight:800;color:var(--mf-text);">Mehfil bhar gayi!</div>
			<div style="font-size:11px;color:var(--mf-sub);margin-top:4px;">Max %d/%d members</div>
		</div>`, memberCount, maxMembers)
	}
	feeText := "Free Join"
	if fee > 0 {
		feeText = fmt.Sprintf("%d Points", fee)
	}
	maxStr := "∞"
	if maxMembers > 0 {
		maxStr = strconv.Itoa(maxMembers)
	}
	return fmt.Sprintf(`
	<div id="join-container" class="mf-section" style="text-align:center;padding:24px;">
		<div style="font-size:40px;margin-bottom:10px;">🚪</div>
		<div style="font-size:15px;font-weight:900;color:var(--mf-text);margin-bottom:4px;">Mehfil Join Karein</div>
		<div style="font-size:11px;color:var(--mf-sub);margin-bottom:16px;">Join fee: <b>%s</b> • %d/%s members</div>
		<button hx-post="/mehfil/join/%s" hx-target="#join-container" hx-swap="outerHTML"
			class="mf-btn mf-btn-green" style="width:100%%;justify-content:center;padding:14px;font-size:13px;">
			✅ Join Karein • %s
		</button>
	</div>`, feeText, memberCount, maxStr, id, feeText)
}

// =============================================================================
// INFO PANEL
// =============================================================================

func buildInfoPanel(id, topic, rules, admin string, fee, maxMembers, memberCount, msgCount, onlineCount int, createdAt time.Time, welcomeMsg, mehfilColor string) string {
	capacityHTML := ""
	if maxMembers > 0 {
		pct := (memberCount * 100) / maxMembers
		capacityHTML = fmt.Sprintf(`
		<div style="margin-bottom:12px;">
			<span class="mf-label">Capacity</span>
			<div style="background:var(--mf-border);border-radius:8px;height:8px;overflow:hidden;">
				<div style="width:%d%%;height:100%%;background:var(--mf-accent);border-radius:8px;"></div>
			</div>
			<div style="font-size:10px;color:var(--mf-sub);margin-top:4px;font-weight:600;">%d / %d members</div>
		</div>`, pct, memberCount, maxMembers)
	}
	welcomeHTML := ""
	if welcomeMsg != "" {
		welcomeHTML = fmt.Sprintf(`<div style="background:rgba(16,185,129,.07);border:1px solid rgba(16,185,129,.2);border-radius:14px;padding:12px;margin-bottom:14px;font-size:12px;color:var(--mf-text);">👋 %s</div>`, html.EscapeString(welcomeMsg))
	}
	return fmt.Sprintf(`
	<div id="panel-info" class="mf-panel">
		<div class="mf-panel-head">
			<span style="font-weight:900;font-size:14px;">ℹ️ Mehfil Info</span>
			<button onclick="mfClosePanel()" class="mf-btn mf-btn-gray mf-btn-icon">✕</button>
		</div>
		<div style="padding:16px;">
			<div style="width:100%%;height:5px;border-radius:5px;background:%s;margin-bottom:16px;"></div>
			<div class="mf-stats" style="margin-bottom:20px;justify-content:space-around;">
				<div class="mf-stat"><span class="mf-stat-val">%d</span><span class="mf-stat-lbl">Members</span></div>
				<div class="mf-stat"><span class="mf-stat-val">%d</span><span class="mf-stat-lbl">Online</span></div>
				<div class="mf-stat"><span class="mf-stat-val">%d</span><span class="mf-stat-lbl">Msgs</span></div>
				<div class="mf-stat"><span class="mf-stat-val">%d</span><span class="mf-stat-lbl">Fee</span></div>
			</div>
			%s %s
			<div style="margin-bottom:12px;"><span class="mf-label">Topic</span><div style="font-size:14px;font-weight:800;color:var(--mf-text);">%s</div></div>
			<div style="margin-bottom:12px;"><span class="mf-label">Usool</span><div style="font-size:13px;color:var(--mf-sub);font-style:italic;">%s</div></div>
			<div style="margin-bottom:12px;"><span class="mf-label">Bani</span><div style="font-size:13px;font-weight:700;color:var(--mf-text);">%s</div></div>
			<div style="margin-bottom:12px;"><span class="mf-label">Admin</span><a href="/profile/%s" style="font-size:13px;font-weight:800;color:var(--mf-accent);text-decoration:none;">@%s</a></div>
			%s
		</div>
	</div>`,
		mehfilColor,
		memberCount, onlineCount, msgCount, fee,
		welcomeHTML, capacityHTML,
		topic, rules,
		createdAt.Format("2 Jan 2006"),
		admin, admin,
		"",
	)
}

// =============================================================================
// INVITE PANEL — currently-online users (not already members) with avatar,
// username, points, and a one-tap Invite button next to each.
// =============================================================================

func buildInvitePanel(mehfilID, currentUser string) string {
	online := getOnlineUsers()
	if len(online) == 0 {
		return `
	<div id="panel-invite" class="mf-panel">
		<div class="mf-panel-head">
			<span style="font-weight:900;font-size:14px;">➕ Invite Karein</span>
			<button onclick="mfClosePanel()" class="mf-btn mf-btn-gray mf-btn-icon">✕</button>
		</div>
		<div style="padding:16px;">
			<div style="text-align:center;padding:32px 12px;color:var(--mf-sub);">
				<div style="font-size:32px;margin-bottom:8px;">😴</div>
				<div style="font-size:12px;font-weight:600;">Abhi koi bhi user online nahi hai</div>
			</div>
		</div>
	</div>`
	}

	rows, err := db.Query(ctx, `
		SELECT username, COALESCE(profile_image,''), COALESCE(points,0)
		FROM users
		WHERE username = ANY($1)
		  AND username != $2
		  AND username NOT IN (SELECT username FROM mehfil_members WHERE mehfil_id=$3)
		ORDER BY points DESC
		LIMIT 60`, online, currentUser, mehfilID)

	var listHTML strings.Builder
	count := 0
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var uname, pfp string
			var pts int
			if rows.Scan(&uname, &pfp, &pts) != nil {
				continue
			}
			count++
			listHTML.WriteString(fmt.Sprintf(`
			<div id="invite-row-%s" class="mf-member-row" data-name="%s">
				<span class="mf-online-dot"></span> %s
				<div style="flex:1;min-width:0;">
					<div class="mf-member-name">@%s</div>
					<div style="font-size:9px;color:var(--mf-sub);font-weight:600;margin-top:2px;">%d points</div>
				</div>
				<button hx-post="/mehfil/invite/%s" hx-vals='{"username":"%s"}'
					hx-target="#invite-row-%s" hx-swap="outerHTML"
					class="mf-btn mf-btn-green mf-btn-xs">➕ Invite</button>
			</div>`, uname, uname, avatarHTML(pfp, uname, 34), uname, pts, mehfilID, uname, uname))
		}
	}

	if count == 0 {
		listHTML.WriteString(`
		<div style="text-align:center;padding:32px 12px;color:var(--mf-sub);">
			<div style="font-size:32px;margin-bottom:8px;">✅</div>
			<div style="font-size:12px;font-weight:600;">Jitne log online hain, sab pehle hi Mehfil mein hain</div>
		</div>`)
	}

	return fmt.Sprintf(`
	<div id="panel-invite" class="mf-panel">
		<div class="mf-panel-head">
			<span style="font-weight:900;font-size:14px;">➕ Invite Karein <span style="color:var(--mf-sub);font-weight:600;">(%d online)</span></span>
			<button onclick="mfClosePanel()" class="mf-btn mf-btn-gray mf-btn-icon">✕</button>
		</div>
		<div style="padding:16px;">
			<input type="search" placeholder="Search online users..." oninput="mfSearchMembers(this.value)"
				style="width:100%%;padding:10px 14px;border:1px solid var(--mf-border);border-radius:12px;background:var(--mf-bg);color:var(--mf-text);font-size:13px;outline:none;margin-bottom:12px;box-sizing:border-box;">
			%s
		</div>
	</div>`, count, listHTML.String())
}

// =============================================================================
// MEMBERS PANEL
// =============================================================================

func buildMembersPanel(id, adminName, currentUser, viewerRole string) string {
	rows, err := db.Query(ctx, `
		SELECT mm.username, COALESCE(mm.role,'member'), COALESCE(mm.custom_title,''),
		       COALESCE(u.profile_image,''), COALESCE(u.points,0),
		       (u.last_active > NOW() - INTERVAL '5 minutes') as is_online
		FROM mehfil_members mm
		JOIN users u ON mm.username=u.username
		WHERE mm.mehfil_id=$1
		ORDER BY (u.last_active > NOW() - INTERVAL '5 minutes') DESC,
		         CASE mm.role WHEN 'afsar' THEN 1 WHEN 'sipahi' THEN 2 ELSE 3 END, 
		         u.points DESC
		LIMIT 200`, id)

	var listHTML strings.Builder
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var uname, role, customTitle, pfp string
			var points int
			var isOnline bool
			rows.Scan(&uname, &role, &customTitle, &pfp, &points, &isOnline)

			dot := `<span class="mf-offline-dot"></span>`
			if isOnline {
				dot = `<span class="mf-online-dot"></span>`
			}

			badgeCls := "mf-badge-gray"
			switch role {
			case "admin":
				badgeCls = "mf-badge-green"
			case "afsar":
				badgeCls = "mf-badge-amber"
			case "sipahi":
				badgeCls = "mf-badge-blue"
			}

			title := getRoleLabel(role)
			if customTitle != "" {
				title = customTitle
			}

			actionBtns := ""
			if currentUser == adminName && uname != adminName {
				promoteBtn := fmt.Sprintf(`<button hx-post="/mehfil/role/%s/%s/afsar" hx-target="closest .mf-member-row" hx-swap="outerHTML" class="mf-btn mf-btn-amber mf-btn-xs">⭐</button>`, id, uname)
				if role == "afsar" {
					promoteBtn = fmt.Sprintf(`<button hx-post="/mehfil/role/%s/%s/member" hx-target="closest .mf-member-row" hx-swap="outerHTML" class="mf-btn mf-btn-amber mf-btn-xs">★→👤</button>`, id, uname)
				}
				sipahiBtn := fmt.Sprintf(`<button hx-post="/mehfil/role/%s/%s/sipahi" hx-target="closest .mf-member-row" hx-swap="outerHTML" class="mf-btn mf-btn-blue mf-btn-xs">🛡️</button>`, id, uname)
				if role == "sipahi" {
					sipahiBtn = fmt.Sprintf(`<button hx-post="/mehfil/role/%s/%s/member" hx-target="closest .mf-member-row" hx-swap="outerHTML" class="mf-btn mf-btn-blue mf-btn-xs">🛡→👤</button>`, id, uname)
				}
				muteBtn := fmt.Sprintf(`<button hx-post="/mehfil/mute/%s/%s" hx-target="closest .mf-member-row" hx-swap="outerHTML" hx-prompt="Kitni dair? (minutes):" class="mf-btn mf-btn-gray mf-btn-xs">🔇</button>`, id, uname)
				kickBtn := fmt.Sprintf(`<button hx-delete="/mehfil/kick/%s/%s" hx-target="closest .mf-member-row" hx-swap="outerHTML" hx-confirm="Kick karein?" class="mf-btn mf-btn-red mf-btn-xs">🚪</button>`, id, uname)
				actionBtns = promoteBtn + sipahiBtn + muteBtn + kickBtn
			} else if viewerRole == "afsar" && uname != adminName && uname != currentUser && role != "afsar" {
				actionBtns = fmt.Sprintf(`<button hx-delete="/mehfil/kick/%s/%s" hx-target="closest .mf-member-row" hx-swap="outerHTML" hx-confirm="Kick karein?" class="mf-btn mf-btn-red mf-btn-xs">🚪</button>`, id, uname)
			}

			listHTML.WriteString(fmt.Sprintf(`
			<div class="mf-member-row" data-name="%s" %s>
				%s %s
				<div style="flex:1;min-width:0;">
					<div class="mf-member-name"><a href="/profile/%s" style="color:inherit;text-decoration:none;">@%s</a></div>
					<div style="display:flex;gap:5px;align-items:center;margin-top:2px;">
						<span class="mf-badge %s" style="font-size:7px;">%s %s</span>
						<span style="font-size:9px;color:var(--mf-sub);font-weight:600;">%d pts</span>
					</div>
				</div>
				<div class="mf-member-actions">%s</div>
			</div>`,
				uname,
				map[bool]string{true: `style="background:rgba(16,185,129,0.08);"`}[isOnline],
				dot, avatarHTML(pfp, uname, 34),
				uname, uname,
				badgeCls, getRoleEmoji(role), title,
				points, actionBtns))
		}
	}

	return fmt.Sprintf(`
	<div id="panel-members" class="mf-panel">
		<div class="mf-panel-head">
			<span style="font-weight:900;font-size:14px;">👥 Members</span>
			<button onclick="mfClosePanel()" class="mf-btn mf-btn-gray mf-btn-icon">✕</button>
		</div>
		<div style="padding:16px;">
			<input type="search" placeholder="Search members..." oninput="mfSearchMembers(this.value)"
				style="width:100%%;padding:10px 14px;border:1px solid var(--mf-border);border-radius:12px;background:var(--mf-bg);color:var(--mf-text);font-size:13px;outline:none;margin-bottom:12px;box-sizing:border-box;">
			%s
		</div>
	</div>`, listHTML.String())
}

// =============================================================================
// ADMIN CONTROL PANEL
// =============================================================================

func buildAdminControlPanel(id string, isAdmin bool, viewerRole string, fee int, isLocked bool, joiningFee int, mehfilColor string, maxMembers int) string {
	if !isAdmin && viewerRole != "afsar" && viewerRole != "sipahi" {
		return ""
	}

	modSection := ""
	if isAdmin || viewerRole == "afsar" {
		modSection = fmt.Sprintf(`
		<div style="margin-bottom:16px;">
			<span class="mf-label">🔎 Jaldi Dekhein</span>
			<div style="display:grid;grid-template-columns:1fr 1fr;gap:8px;margin-bottom:14px;">
				<button onclick="mfClosePanel();mfShowInfo();" class="mf-btn mf-btn-gray">ℹ️ Tafseel</button>
				<button onclick="mfClosePanel();mfOpenPanel('panel-members')" class="mf-btn mf-btn-gray">👥 Members</button>
			</div>
			<span class="mf-label">🛡️ Moderation</span>
			<button hx-post="/mehfil/broadcast/%s" hx-prompt="Izlamiey likhein:" hx-swap="none"
				class="mf-btn mf-btn-amber" style="width:100%%;margin-bottom:8px;">📢 Izlamiey Bhejo</button>
			<button onclick="mfClosePanel();mfOpenPanel('panel-invite')" class="mf-btn mf-btn-green" style="width:100%%;">➕ Online Users Invite Karein</button>
		</div>`, id)
	}

	adminOnlySection := ""
	if isAdmin {
		lockText, lockCls := "🔓 Kholo Mehfil", "mf-btn-green"
		if isLocked {
			lockText, lockCls = "🔒 Band Karo Mehfil", "mf-btn-amber"
		}
		maxStr := "∞"
		if maxMembers > 0 {
			maxStr = strconv.Itoa(maxMembers)
		}

		var colorBtns strings.Builder
		for _, col := range []string{"#10b981", "#3b82f6", "#f59e0b", "#ef4444", "#8b5cf6", "#ec4899", "#06b6d4", "#f97316"} {
			border := col
			if col == mehfilColor {
				border = "white"
			}
			colorBtns.WriteString(fmt.Sprintf(
				`<button hx-post="/mehfil/set-color/%s" hx-vals='{"color":"%s"}' hx-swap="none"
					onclick="document.querySelector('.mf-header-glow').style.background='%s'"
					style="width:28px;height:28px;border-radius:50%%;background:%s;border:3px solid %s;cursor:pointer;transition:transform .2s;"
					onmouseenter="this.style.transform='scale(1.2)'" onmouseleave="this.style.transform='scale(1)'"></button>`,
				id, col, col, col, border))
		}

		adminOnlySection = fmt.Sprintf(`
		<div style="padding-top:16px;border-top:1px solid var(--mf-border);">
			<span class="mf-label">⚙️ Admin Controls</span>
			<div style="display:grid;grid-template-columns:1fr 1fr;gap:8px;margin-bottom:12px;">
				<button hx-put="/mehfil/update-topic/%s" hx-prompt="Naya Topic:" hx-target="#mehfil-title" class="mf-btn mf-btn-gray">✏️ Topic</button>
				<button hx-put="/mehfil/update-rules/%s" hx-prompt="Naye Usool:" hx-target="#rules-text" class="mf-btn mf-btn-gray">📜 Usool</button>
				<button hx-put="/mehfil/update-fee/%s" hx-prompt="Joining Fee (0-1000 Points):" hx-target="#fee-display" class="mf-btn mf-btn-blue">💰 Fee: <span id="fee-display">%d</span></button>
				<button hx-put="/mehfil/toggle-lock/%s" hx-swap="none" onclick="window.location.reload()" class="mf-btn %s">%s</button>
				<button hx-put="/mehfil/update-welcome/%s" hx-prompt="Welcome message:" hx-swap="none" class="mf-btn mf-btn-gray">👋 Welcome</button>
				<button hx-put="/mehfil/update-maxmembers/%s" hx-prompt="Max Members (0=unlimited):" hx-swap="none" class="mf-btn mf-btn-gray">🎯 Max: %s</button>
			</div>
			<span class="mf-label">Mehfil Color</span>
			<div style="display:flex;gap:6px;flex-wrap:wrap;margin-bottom:8px;">%s</div>
		</div>`,
			id, id, id, joiningFee, id, lockCls, lockText, id, id, maxStr,
			colorBtns.String())
	}

	return fmt.Sprintf(`
	<div id="panel-mod" class="mf-panel">
		<div class="mf-panel-head">
			<span style="font-weight:900;font-size:14px;">⚙️ Control Panel</span>
			<button onclick="mfClosePanel()" class="mf-btn mf-btn-gray mf-btn-icon">✕</button>
		</div>
		<div style="padding:16px;">%s%s</div>
	</div>`, modSection, adminOnlySection)
}

// =============================================================================
// RENDER MEHFIL MESSAGE
// =============================================================================

func renderMehfilMessage(sb *strings.Builder, m FlatComment, role, currentUser, mehfilID string) {
	parentQuote := ""
	if m.ParentID != nil && *m.ParentID != 0 {
		pAuthor := "user"
		pSnippet := "..."
		if m.ParentAuthor != nil && *m.ParentAuthor != "" {
			pAuthor = *m.ParentAuthor
		}
		if m.ParentSnippet != nil && *m.ParentSnippet != "" {
			pSnippet = html.EscapeString(*m.ParentSnippet)
		}
		parentQuote = fmt.Sprintf(`
		<div class="mf-quote">
			<div class="mf-quote-bar"></div>
			<div class="mf-quote-body">
				<div class="mf-quote-author">↩ @%s</div>
				<div class="mf-quote-text">%s</div>
			</div>
		</div>`, pAuthor, pSnippet)
	}

	imgHTML := ""
	if m.ImageUrl != "" {
		imgHTML = fmt.Sprintf(`<div class="mf-msg-img"><img src="%s" onclick="mfLightbox(this.src)" loading="lazy" alt="image"></div>`, m.ImageUrl)
	}

	reactHTML := renderReactions(m.ID, m.Reactions, currentUser)

	specialTag := ""
	if m.IsPinned {
		specialTag += `<span class="mf-badge mf-badge-green" style="font-size:7px;">📌 Pinned</span> `
	}
	if m.IsAnnouncement {
		specialTag += `<span class="mf-badge mf-badge-amber" style="font-size:7px;">📢 Izlamiey</span> `
	}

	editedTag := ""
	if m.EditedAt != nil {
		editedTag = `<span style="font-size:8px;color:var(--mf-sub);font-style:italic;">(edited)</span>`
	}

	badgeCls := "mf-badge-gray"
	switch role {
	case "admin":
		badgeCls = "mf-badge-green"
	case "afsar":
		badgeCls = "mf-badge-amber"
	case "sipahi":
		badgeCls = "mf-badge-blue"
	}

	cardCls := "mf-msg"
	if m.IsPinned {
		cardCls += " pinned"
	}
	if m.IsAnnouncement {
		cardCls += " announce"
	}

	ctrlHTML := buildMsgControls(m, role, currentUser, mehfilID)
	escapedContent := html.EscapeString(m.Content)
	jsContent := "`" + strings.ReplaceAll(escapedContent, "`", "\\`") + "`"

	sb.WriteString(fmt.Sprintf(`
	<div id="msg-%d" class="%s">
		<div class="mf-msg-header">
			%s
			<div style="flex:1;min-width:0;">
				<div style="display:flex;align-items:center;gap:5px;flex-wrap:wrap;">
				<a href="/profile/%s" class="mf-msg-author" style="text-decoration:none;color:var(--mf-text);">@%s</a>%s
					<span class="mf-badge %s">%s %s</span>
					%s
				</div>
				<div style="display:flex;align-items:center;gap:5px;margin-top:1px;">
					<span style="font-size:9px;color:var(--mf-sub);font-weight:600;">%d pts</span>
					<span style="font-size:8px;color:var(--mf-border);">•</span>
					<span class="mf-msg-time">%s</span>
					%s
				</div>
			</div>
		</div>
		%s
		<div class="mf-msg-body">%s</div>
		%s %s
		<div class="mf-action-bar">
			<button onclick="toggleMehfilReply(%d)" class="mf-btn mf-btn-gray mf-btn-xs">↩ Reply</button>
			<button onclick="mfReactPicker(%d)" class="mf-btn mf-btn-gray mf-btn-xs" style="position:relative;">😊</button>
			<button onclick="mfCopy(%s)" class="mf-btn mf-btn-gray mf-btn-xs">📋</button>
			%s
		</div>
		<div id="rf-%d" class="mf-reply-form hidden">
			<form hx-post="/mehfil/send/%s?parent_id=%d"
			      hx-target="#mf-feed" hx-swap="afterbegin"
			      onsubmit="setTimeout(()=>{this.reset();toggleMehfilReply(%d);},60)">
				<div class="mf-reply-input">
					<input name="content" placeholder="Reply likhein..." autocomplete="off">
					<button type="submit" class="mf-btn mf-btn-green mf-btn-sm">➤</button>
				</div>
			</form>
		</div>
	</div>`,
		m.ID, cardCls,
		avatarHTML(m.ProfileImage, m.Author, 38),
		m.Author, m.Author, TrendingStarHTML(m.Author),
		badgeCls, getRoleEmoji(role), getRoleLabel(role),
		specialTag,
		m.Points, formatTimeAgo(m.CreatedAt), editedTag,
		parentQuote,
		escapedContent,
		imgHTML, reactHTML,
		m.ID, m.ID, jsContent, ctrlHTML,
		m.ID, mehfilID, m.ID, m.ID,
	))
}

func renderReactions(msgID int, rawReactions, currentUser string) string {
	if rawReactions == "" {
		return fmt.Sprintf(`<div id="mf-reactions-%d" class="mf-reactions"></div>`, msgID)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<div id="mf-reactions-%d" class="mf-reactions">`, msgID))
	for _, p := range strings.Split(rawReactions, ",") {
		kv := strings.SplitN(p, ":", 2)
		if len(kv) != 2 {
			continue
		}
		emoji, count := kv[0], kv[1]
		sb.WriteString(fmt.Sprintf(
			`<span class="mf-reaction" data-emoji="%s"
			      onclick="htmx.ajax('POST','/mehfil/react/%d',{values:{emoji:'%s'},swap:'none'})">%s <span class="mf-rc">%s</span></span>`,
			emoji, msgID, emoji, emoji, count))
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

func buildMsgControls(m FlatComment, role, currentUser, mehfilID string) string {
	// Own message delete button
	if currentUser == m.Author {
		return fmt.Sprintf(
			`<div style="margin-top:4px;"><button hx-delete="/mehfil/delete-own/%d" hx-target="#msg-%d" hx-swap="outerHTML" hx-confirm="Delete karein?" class="mf-btn mf-btn-red mf-btn-xs">🗑️</button></div>`,
			m.ID, m.ID)
	}
	if currentUser == "" {
		return ""
	}

	var adminName string
	db.QueryRow(ctx, "SELECT admin_username FROM mehfils WHERE id=$1", mehfilID).Scan(&adminName)

	var viewerRole string
	if currentUser == adminName {
		viewerRole = "admin"
	} else {
		db.QueryRow(ctx,
			"SELECT COALESCE(role,'member') FROM mehfil_members WHERE mehfil_id=$1 AND username=$2",
			mehfilID, currentUser).Scan(&viewerRole)
	}

	isTargetAdmin := m.Author == adminName
	canHide := !isTargetAdmin && (viewerRole == "admin" || viewerRole == "afsar" || viewerRole == "sipahi")
	canPin := viewerRole == "admin"
	canAnnounce := viewerRole == "admin"

	if !canHide && !canPin && !canAnnounce {
		return ""
	}

	var btns strings.Builder
	btns.WriteString(`<div style="display:flex;gap:4px;margin-top:4px;flex-wrap:wrap;">`)
	if canHide {
		btns.WriteString(fmt.Sprintf(
			`<button hx-delete="/mehfil/hide/%d" hx-target="#msg-%d" hx-swap="outerHTML" hx-confirm="Hide karein?" class="mf-btn mf-btn-red mf-btn-xs">👁️ Hide</button>`,
			m.ID, m.ID))
	}
	if canPin {
		lbl := "📌 Pin"
		if m.IsPinned {
			lbl = "📌 Unpin"
		}
		btns.WriteString(fmt.Sprintf(
			`<button hx-post="/mehfil/pin/%d" hx-target="#msg-%d" hx-swap="outerHTML" class="mf-btn mf-btn-gray mf-btn-xs">%s</button>`,
			m.ID, m.ID, lbl))
	}
	if canAnnounce {
		lbl := "📢 Announce"
		if m.IsAnnouncement {
			lbl = "📢 Un-announce"
		}
		btns.WriteString(fmt.Sprintf(
			`<button hx-post="/mehfil/announce/%d" hx-target="#msg-%d" hx-swap="outerHTML" class="mf-btn mf-btn-amber mf-btn-xs">%s</button>`,
			m.ID, m.ID, lbl))
	}
	btns.WriteString(`</div>`)
	return btns.String()
}

// =============================================================================
// GET MESSAGES
// =============================================================================

func GetMehfilMessages(c *fiber.Ctx) error {
	mehfilID := c.Params("id")
	currentUser := getCurrentUser(c)
	if currentUser != "" {
		_, _ = db.Exec(ctx, "UPDATE users SET last_active=NOW() WHERE username=$1", currentUser)
	}

	rows, err := db.Query(ctx, `
		SELECT m.id, m.author, m.content,
		       COALESCE(m.image_url,''),
		       m.created_at, m.parent_id,
		       p.author, LEFT(p.content,50),
		       COALESCE(u.points,0), COALESCE(u.profile_image,''),
		       COALESCE(mem.role, CASE WHEN meh.admin_username=m.author THEN 'admin' ELSE 'member' END),
		       COALESCE(m.reactions,''),
		       COALESCE(m.is_pinned,false),
		       COALESCE(m.is_announcement,false),
		       m.edited_at
		FROM mehfil_messages m
		LEFT JOIN mehfil_messages p ON m.parent_id=p.id
		LEFT JOIN users u ON m.author=u.username
		LEFT JOIN mehfils meh ON m.mehfil_id=meh.id
		LEFT JOIN mehfil_members mem ON m.author=mem.username AND m.mehfil_id=mem.mehfil_id
		WHERE m.mehfil_id=$1 AND COALESCE(m.is_hidden,false)=false
		ORDER BY m.created_at DESC LIMIT 80`, mehfilID)
	if err != nil {
		return c.Status(500).SendString(`<div class="mf-welcome">Messages load karne mein error</div>`)
	}
	defer rows.Close()

	var sb strings.Builder
	count := 0
	for rows.Next() {
		var m FlatComment
		var role string
		if err := rows.Scan(
			&m.ID, &m.Author, &m.Content, &m.ImageUrl,
			&m.CreatedAt, &m.ParentID, &m.ParentAuthor, &m.ParentSnippet,
			&m.Points, &m.ProfileImage, &role,
			&m.Reactions, &m.IsPinned, &m.IsAnnouncement, &m.EditedAt,
		); err != nil {
			continue
		}
		renderMehfilMessage(&sb, m, role, currentUser, mehfilID)
		count++
	}

	if count == 0 {
		var welcomeMsg string
		db.QueryRow(ctx, "SELECT COALESCE(welcome_msg,'') FROM mehfils WHERE id=$1", mehfilID).Scan(&welcomeMsg)
		msg := "Pehli baat karein! 🎉"
		if welcomeMsg != "" {
			msg = welcomeMsg
		}
		return c.SendString(fmt.Sprintf(
			`<div class="mf-welcome"><div style="font-size:32px;margin-bottom:8px;">👋</div><div style="font-size:13px;font-weight:700;color:var(--mf-sub);">%s</div></div>`,
			html.EscapeString(msg)))
	}
	return c.SendString(sb.String())
}

// =============================================================================
// SEND MESSAGE
// =============================================================================

func SendMehfilMessage(c *fiber.Ctx) error {
	mehfilID := c.Params("id")
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Status(401).SendString("")
	}

	var muteCheck time.Time
	if db.QueryRow(ctx,
		"SELECT muted_until FROM mehfil_members WHERE mehfil_id=$1 AND username=$2 AND muted_until > NOW()",
		mehfilID, currentUser).Scan(&muteCheck) == nil {
		return c.SendString(`<div class="mf-muted-bar">🔇 Ap muted hain — post nahi kar saktay</div>`)
	}

	// ── Existing spam ban? Show remaining time ─────────────────────────────────
	var spamBannedUntil time.Time
	if db.QueryRow(ctx,
		"SELECT spam_banned_until FROM mehfil_members WHERE mehfil_id=$1 AND username=$2 AND spam_banned_until > NOW()",
		mehfilID, currentUser).Scan(&spamBannedUntil) == nil {
		loc, _ := time.LoadLocation("Asia/Karachi")
		remaining := int(time.Until(spamBannedUntil).Minutes()) + 1
		return c.SendString(fmt.Sprintf(
			`<div class="mf-muted-bar">🚫 Apko tez tez likhne ki wja se 5 minute kai liye ban kar dia gya hai — %d minute baqi (%s tak)</div>`,
			remaining, spamBannedUntil.In(loc).Format("15:04")))
	}

	// ── Spam detection: same user posting again too fast ───────────────────────
	var lastMsgAt time.Time
	hasLastMsg := db.QueryRow(ctx,
		"SELECT created_at FROM mehfil_messages WHERE mehfil_id=$1 AND author=$2 ORDER BY created_at DESC LIMIT 1",
		mehfilID, currentUser).Scan(&lastMsgAt) == nil
	if hasLastMsg && time.Since(lastMsgAt) < 2*time.Second {
		banUntil := time.Now().Add(5 * time.Minute)
		_, _ = db.Exec(ctx, "UPDATE mehfil_members SET spam_banned_until=$1 WHERE mehfil_id=$2 AND username=$3",
			banUntil, mehfilID, currentUser)
		return c.SendString(`<div class="mf-muted-bar">🚫 Apko tez tez likhne ki wja se 5 minute kai liye ban kar dia gya hai</div>`)
	}

	var isLocked bool
	var adminName string
	db.QueryRow(ctx, "SELECT COALESCE(is_locked,false), admin_username FROM mehfils WHERE id=$1", mehfilID).
		Scan(&isLocked, &adminName)
	if isLocked && currentUser != adminName {
		return c.SendString(`<div class="mf-locked-bar">🔒 Mehfil band hai</div>`)
	}

	content := html.EscapeString(strings.TrimSpace(c.FormValue("content")))
	if len(content) > 1000 {
		content = content[:1000]
	}

	var imageUrl string
	if file, err := c.FormFile("image"); err == nil && file.Size <= 10*1024*1024 {
		dir := "./uploads/mehfil_images"
		os.MkdirAll(dir, 0755)
		filename := fmt.Sprintf("%d_%s", time.Now().UnixMilli(), filepath.Base(file.Filename))
		if err := c.SaveFile(file, filepath.Join(dir, filename)); err == nil {
			imageUrl = "/uploads/mehfil_images/" + filename
		}
	}

	if content == "" && imageUrl == "" {
		return c.Status(400).SendString("")
	}

	var parentID *int
	if pidStr := c.Query("parent_id"); pidStr != "" {
		if pid, err := strconv.Atoi(pidStr); err == nil {
			parentID = &pid
		}
	}

	var newMsgID int
	if err := db.QueryRow(ctx, `
		INSERT INTO mehfil_messages (mehfil_id, author, content, image_url, parent_id, created_at)
		VALUES ($1,$2,$3,$4,$5,CURRENT_TIMESTAMP) RETURNING id`,
		mehfilID, currentUser, content, imageUrl, parentID).Scan(&newMsgID); err != nil {
		return c.SendString(`<div class="mf-muted-bar">Message bhejne mein error hua</div>`)
	}

	_, _ = db.Exec(ctx, "UPDATE users SET last_active=NOW() WHERE username=$1", currentUser)

	// +5 points for every Mehfil comment/message sent.
	_, _ = db.Exec(ctx, "UPDATE users SET points=points+5 WHERE username=$1", currentUser)

	if parentID != nil {
		var recipient string
		if db.QueryRow(ctx, "SELECT author FROM mehfil_messages WHERE id=$1", *parentID).Scan(&recipient) == nil &&
			recipient != currentUser {
			var mehfilTopic string
			db.QueryRow(ctx, "SELECT topic FROM mehfils WHERE id=$1", mehfilID).Scan(&mehfilTopic)
			snippet := content
			if len(snippet) > 30 {
				snippet = snippet[:27] + "..."
			}
			_, _ = db.Exec(ctx, `
				INSERT INTO notifications (recipient, sender, type, content, mehfil_id, is_read, created_at)
				VALUES ($1,$2,'mehfil_reply',$3,$4,false,NOW())`,
				recipient, currentUser,
				fmt.Sprintf("<b>@%s</b> ne reply kia: '%s' — '%s' mehfil mein", currentUser, snippet, mehfilTopic),
				mehfilID)

			Push(recipient, currentUser,
				fmt.Sprintf("ne reply kia: '%s' — '%s' mehfil mein", snippet, mehfilTopic),
				mehfilMsgLink(mehfilID, newMsgID))
		}
	}

	var m FlatComment
	var role string
	db.QueryRow(ctx, `
		SELECT m.id, m.author, m.content, COALESCE(m.image_url,''), m.created_at, m.parent_id,
		       p.author, LEFT(p.content,50),
		       COALESCE(u.points,0), COALESCE(u.profile_image,''),
		       COALESCE(mem.role, CASE WHEN meh.admin_username=m.author THEN 'admin' ELSE 'member' END),
		       COALESCE(m.reactions,''),
		       COALESCE(m.is_pinned,false),
		       COALESCE(m.is_announcement,false),
		       m.edited_at
		FROM mehfil_messages m
		LEFT JOIN mehfil_messages p ON m.parent_id=p.id
		LEFT JOIN users u ON m.author=u.username
		LEFT JOIN mehfils meh ON m.mehfil_id=meh.id
		LEFT JOIN mehfil_members mem ON m.author=mem.username AND m.mehfil_id=mem.mehfil_id
		WHERE m.id=$1`, newMsgID).
		Scan(&m.ID, &m.Author, &m.Content, &m.ImageUrl, &m.CreatedAt, &m.ParentID,
			&m.ParentAuthor, &m.ParentSnippet, &m.Points, &m.ProfileImage, &role,
			&m.Reactions, &m.IsPinned, &m.IsAnnouncement, &m.EditedAt)

	var sb strings.Builder
	renderMehfilMessage(&sb, m, role, currentUser, mehfilID)
	return c.SendString(sb.String())
}

// =============================================================================
// REACTION
// =============================================================================

func ReactToMessage(c *fiber.Ctx) error {
	msgID := c.Params("id")
	currentUser := getCurrentUser(c)
	emoji := c.FormValue("emoji")
	if currentUser == "" || emoji == "" {
		return c.SendStatus(400)
	}
	var current string
	db.QueryRow(ctx, "SELECT COALESCE(reactions,'') FROM mehfil_messages WHERE id=$1", msgID).Scan(&current)
	counts := map[string]int{}
	if current != "" {
		for _, p := range strings.Split(current, ",") {
			kv := strings.SplitN(p, ":", 2)
			if len(kv) == 2 {
				n, _ := strconv.Atoi(kv[1])
				counts[kv[0]] = n
			}
		}
	}
	counts[emoji]++
	var parts []string
	for k, v := range counts {
		parts = append(parts, fmt.Sprintf("%s:%d", k, v))
	}
	_, _ = db.Exec(ctx, "UPDATE mehfil_messages SET reactions=$1 WHERE id=$2", strings.Join(parts, ","), msgID)
	return c.SendStatus(200)
}

// =============================================================================
// PIN / ANNOUNCE / LOCK / MUTE / WELCOME / MAX-MEMBERS / COLOR / BROADCAST
// =============================================================================

func PinMessage(c *fiber.Ctx) error {
	msgID := c.Params("id")
	currentUser, err := requireUser(c)
	if err != nil {
		return err
	}
	var mehfilID int
	var adminName string
	db.QueryRow(ctx,
		"SELECT mm.mehfil_id, meh.admin_username FROM mehfil_messages mm JOIN mehfils meh ON mm.mehfil_id=meh.id WHERE mm.id=$1",
		msgID).Scan(&mehfilID, &adminName)
	if currentUser != adminName {
		return c.SendStatus(403)
	}
	var isPinned bool
	db.QueryRow(ctx, "SELECT COALESCE(is_pinned,false) FROM mehfil_messages WHERE id=$1", msgID).Scan(&isPinned)
	_, _ = db.Exec(ctx, "UPDATE mehfil_messages SET is_pinned=false WHERE mehfil_id=$1", mehfilID)
	if !isPinned {
		_, _ = db.Exec(ctx, "UPDATE mehfil_messages SET is_pinned=true WHERE id=$1", msgID)
	}
	return rerenderMessage(c, msgID, currentUser, strconv.Itoa(mehfilID))
}

func AnnounceMessage(c *fiber.Ctx) error {
	msgID := c.Params("id")
	currentUser, err := requireUser(c)
	if err != nil {
		return err
	}
	var mehfilID int
	var adminName string
	db.QueryRow(ctx,
		"SELECT mm.mehfil_id, meh.admin_username FROM mehfil_messages mm JOIN mehfils meh ON mm.mehfil_id=meh.id WHERE mm.id=$1",
		msgID).Scan(&mehfilID, &adminName)
	if currentUser != adminName {
		return c.SendStatus(403)
	}
	var isSA bool
	db.QueryRow(ctx, "SELECT COALESCE(is_announcement,false) FROM mehfil_messages WHERE id=$1", msgID).Scan(&isSA)
	_, _ = db.Exec(ctx, "UPDATE mehfil_messages SET is_announcement=false WHERE mehfil_id=$1", mehfilID)
	if !isSA {
		_, _ = db.Exec(ctx, "UPDATE mehfil_messages SET is_announcement=true WHERE id=$1", msgID)
	}
	return rerenderMessage(c, msgID, currentUser, strconv.Itoa(mehfilID))
}

func ToggleMehfilLock(c *fiber.Ctx) error {
	id := c.Params("id")
	currentUser, err := requireUser(c)
	if err != nil {
		return err
	}
	var adminName string
	db.QueryRow(ctx, "SELECT admin_username FROM mehfils WHERE id=$1", id).Scan(&adminName)
	if currentUser != adminName {
		return c.SendStatus(403)
	}
	_, _ = db.Exec(ctx, "UPDATE mehfils SET is_locked = NOT COALESCE(is_locked,false) WHERE id=$1", id)
	return c.SendStatus(200)
}

func MuteMember(c *fiber.Ctx) error {
	mehfilID := c.Params("id")
	target := c.Params("username")
	currentUser, err := requireUser(c)
	if err != nil {
		return err
	}
	minutesStr := c.Get("HX-Prompt", "30")
	var adminName string
	db.QueryRow(ctx, "SELECT admin_username FROM mehfils WHERE id=$1", mehfilID).Scan(&adminName)
	var vr string
	if currentUser == adminName {
		vr = "admin"
	} else {
		db.QueryRow(ctx, "SELECT COALESCE(role,'member') FROM mehfil_members WHERE mehfil_id=$1 AND username=$2", mehfilID, currentUser).Scan(&vr)
	}
	if vr != "admin" && vr != "afsar" {
		return c.SendStatus(403)
	}
	minutes, _ := strconv.Atoi(minutesStr)
	if minutes <= 0 {
		minutes = 30
	}
	muteUntil := time.Now().Add(time.Duration(minutes) * time.Minute)
	_, _ = db.Exec(ctx, "UPDATE mehfil_members SET muted_until=$1 WHERE mehfil_id=$2 AND username=$3", muteUntil, mehfilID, target)
	var topic string
	db.QueryRow(ctx, "SELECT topic FROM mehfils WHERE id=$1", mehfilID).Scan(&topic)
	_, _ = db.Exec(ctx, `INSERT INTO notifications (recipient,sender,type,content,mehfil_id,is_read,created_at) VALUES ($1,$2,'mehfil_mute',$3,$4,false,NOW())`,
		target, currentUser, fmt.Sprintf("Apko '%s' mehfil mein %d minute ke liye mute kar dia gaya.", topic, minutes), mehfilID)
	Push(target, currentUser, fmt.Sprintf("Apko '%s' mehfil mein %d minute ke liye mute kar dia gaya.", topic, minutes), mehfilLink(mehfilID))
	return c.SendString(fmt.Sprintf(`<div class="mf-member-row" data-name="%s" style="opacity:.5;">%s<div class="mf-member-name">@%s <span style="font-size:9px;color:#ef4444;">(muted %dm)</span></div></div>`,
		target, avatarHTML("", target, 34), target, minutes))
}

func UpdateWelcomeMsg(c *fiber.Ctx) error {
	id := c.Params("id")
	currentUser, err := requireUser(c)
	if err != nil {
		return err
	}
	msg := html.EscapeString(strings.TrimSpace(c.Get("HX-Prompt")))
	var adminName string
	db.QueryRow(ctx, "SELECT admin_username FROM mehfils WHERE id=$1", id).Scan(&adminName)
	if currentUser != adminName {
		return c.SendStatus(403)
	}
	_, _ = db.Exec(ctx, "UPDATE mehfils SET welcome_msg=$1 WHERE id=$2", msg, id)
	return c.SendStatus(200)
}

func UpdateMaxMembers(c *fiber.Ctx) error {
	id := c.Params("id")
	currentUser, err := requireUser(c)
	if err != nil {
		return err
	}
	var adminName string
	db.QueryRow(ctx, "SELECT admin_username FROM mehfils WHERE id=$1", id).Scan(&adminName)
	if currentUser != adminName {
		return c.SendStatus(403)
	}
	n, _ := strconv.Atoi(c.Get("HX-Prompt", "0"))
	if n < 0 {
		n = 0
	}
	_, _ = db.Exec(ctx, "UPDATE mehfils SET max_members=$1 WHERE id=$2", n, id)
	return c.SendStatus(200)
}

func SetMehfilColor(c *fiber.Ctx) error {
	id := c.Params("id")
	currentUser, err := requireUser(c)
	if err != nil {
		return err
	}
	color := c.FormValue("color")
	var adminName string
	db.QueryRow(ctx, "SELECT admin_username FROM mehfils WHERE id=$1", id).Scan(&adminName)
	if currentUser != adminName {
		return c.SendStatus(403)
	}
	if len(color) != 7 || color[0] != '#' {
		return c.SendStatus(400)
	}
	_, _ = db.Exec(ctx, "UPDATE mehfils SET mehfil_color=$1 WHERE id=$2", color, id)
	return c.SendStatus(200)
}

func BroadcastAnnouncement(c *fiber.Ctx) error {
	id := c.Params("id")
	currentUser, err := requireUser(c)
	if err != nil {
		return err
	}
	msg := html.EscapeString(strings.TrimSpace(c.Get("HX-Prompt")))
	if msg == "" {
		return c.SendStatus(400)
	}
	var adminName, topic string
	db.QueryRow(ctx, "SELECT admin_username, topic FROM mehfils WHERE id=$1", id).Scan(&adminName, &topic)
	var vr string
	if currentUser == adminName {
		vr = "admin"
	} else {
		db.QueryRow(ctx, "SELECT COALESCE(role,'member') FROM mehfil_members WHERE mehfil_id=$1 AND username=$2", id, currentUser).Scan(&vr)
	}
	if vr != "admin" && vr != "afsar" {
		return c.SendStatus(403)
	}
	goSafe("mehfil broadcast notif", func() {
		_, _ = db.Exec(ctx, `
			INSERT INTO notifications (recipient,sender,type,content,mehfil_id,is_read,created_at)
			SELECT username,$1,'mehfil_broadcast',$2,$3,false,NOW()
			FROM mehfil_members WHERE mehfil_id=$3 AND username!=$1`,
			currentUser, fmt.Sprintf("📢 '%s' mehfil mein izlamiey: %s", topic, msg), id)
	})
	PushMehfilMembers(id, currentUser, fmt.Sprintf("📢 '%s' mehfil mein izlamiey: %s", topic, msg), mehfilLink(id))
	return c.SendStatus(200)
}

// =============================================================================
// LEAVE / DELETE OWN / KICK / HIDE
// =============================================================================

func LeaveMehfil(c *fiber.Ctx) error {
	id := c.Params("id")
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.SendStatus(401)
	}
	var adminName string
	db.QueryRow(ctx, "SELECT admin_username FROM mehfils WHERE id=$1", id).Scan(&adminName)
	if currentUser == adminName {
		return c.SendString("Admin mehfil nahi chor sakta")
	}
	_, _ = db.Exec(ctx, "DELETE FROM mehfil_members WHERE mehfil_id=$1 AND username=$2", id, currentUser)
	return c.SendStatus(200)
}

func DeleteOwnMessage(c *fiber.Ctx) error {
	msgID := c.Params("id")
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.SendStatus(401)
	}

	var author string
	db.QueryRow(ctx, "SELECT author FROM mehfil_messages WHERE id=$1", msgID).Scan(&author)
	if author != currentUser {
		return c.SendStatus(403)
	}

	_, _ = db.Exec(ctx, "DELETE FROM mehfil_messages WHERE id=$1", msgID)
	return c.SendString("")
}
func KickMember(c *fiber.Ctx) error {
	mehfilID := c.Params("id")
	target := c.Params("username")
	currentUser := getCurrentUser(c)
	if currentUser == "" || currentUser == target {
		return c.SendStatus(400)
	}
	var adminName, topic string
	db.QueryRow(ctx, "SELECT admin_username, topic FROM mehfils WHERE id=$1", mehfilID).Scan(&adminName, &topic)
	var vr string
	if currentUser == adminName {
		vr = "admin"
	} else {
		db.QueryRow(ctx, "SELECT COALESCE(role,'member') FROM mehfil_members WHERE mehfil_id=$1 AND username=$2", mehfilID, currentUser).Scan(&vr)
	}
	if (vr != "admin" && vr != "afsar") || target == adminName {
		return c.SendStatus(403)
	}
	res, _ := db.Exec(ctx, "DELETE FROM mehfil_members WHERE mehfil_id=$1 AND username=$2", mehfilID, target)
	if res.RowsAffected() == 0 {
		return c.SendStatus(404)
	}
	_, _ = db.Exec(ctx, "UPDATE users SET points=points-500 WHERE username=$1", target)
	_, _ = db.Exec(ctx, `INSERT INTO notifications (recipient,sender,type,content,mehfil_id,is_read,created_at) VALUES ($1,$2,'mehfil_kick',$3,$4,false,NOW())`,
		target, currentUser, fmt.Sprintf("Apko '%s' mehfil se kick kar dia gaya. (-500 pts)", topic), mehfilID)
	Push(target, currentUser, fmt.Sprintf("Apko '%s' mehfil se kick kar dia gaya. (-500 pts)", topic), mehfilLink(mehfilID))
	return c.SendString(fmt.Sprintf(
		`<div class="mf-member-row" style="opacity:.4;text-decoration:line-through;"><span class="mf-offline-dot"></span><div class="mf-member-name">@%s (kicked)</div></div>`, target))
}

func HideComment(c *fiber.Ctx) error {
	msgID := c.Params("id")
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.SendStatus(401)
	}

	var author string
	var mehfilID int
	err := db.QueryRow(ctx, "SELECT author, mehfil_id FROM mehfil_messages WHERE id=$1", msgID).Scan(&author, &mehfilID)
	if err != nil {
		return c.SendStatus(404)
	}

	var adminName, topic string
	db.QueryRow(ctx, "SELECT admin_username, topic FROM mehfils WHERE id=$1", mehfilID).Scan(&adminName, &topic)

	var vr string
	if currentUser == adminName {
		vr = "admin"
	} else {
		db.QueryRow(ctx, "SELECT COALESCE(role,'member') FROM mehfil_members WHERE mehfil_id=$1 AND username=$2", mehfilID, currentUser).Scan(&vr)
	}

	deduction := 0
	switch vr {
	case "admin":
		deduction = 50
	case "afsar":
		deduction = 30
	case "sipahi":
		deduction = 10
	default:
		return c.SendStatus(403)
	}

	if deduction > 0 && author != currentUser {
		_, _ = db.Exec(ctx, "UPDATE users SET points=points-$1 WHERE username=$2", deduction, author)
		_, _ = db.Exec(ctx, `INSERT INTO notifications (recipient,sender,type,content,mehfil_id,is_read,created_at) VALUES ($1,$2,'mehfil_hide',$3,$4,false,NOW())`,
			author, currentUser, fmt.Sprintf("'%s' mehfil mein apki baat chhupa di gayi.", topic), mehfilID)
		Push(author, currentUser, fmt.Sprintf("'%s' mehfil mein apki baat chhupa di gayi.", topic), mehfilLink(mehfilID))
	}

	// SOFT DELETE - This was the main bug
	_, _ = db.Exec(ctx, "UPDATE mehfil_messages SET is_hidden = true WHERE id = $1", msgID)
	return c.SendString("")
}

// =============================================================================
// ROLE UPDATE
// =============================================================================

func UpdateUserRole(c *fiber.Ctx) error {
	mehfilID := c.Params("id")
	target := c.Params("username")
	newRole := c.Params("role")
	currentUser, err := requireUser(c)
	if err != nil {
		return err
	}
	var adminName, topic string
	if err := db.QueryRow(ctx, "SELECT admin_username, topic FROM mehfils WHERE id=$1", mehfilID).
		Scan(&adminName, &topic); err != nil || currentUser != adminName {
		return c.Status(403).SendString("Unauthorized")
	}
	if _, err := db.Exec(ctx, "UPDATE mehfil_members SET role=$1 WHERE mehfil_id=$2 AND username=$3", newRole, mehfilID, target); err != nil {
		return c.Status(500).SendString("Error")
	}
	notifMsgs := map[string]string{
		"afsar":  fmt.Sprintf("'%s' mehfil mein apko Afsar bana dia gaya! ⭐", topic),
		"sipahi": fmt.Sprintf("'%s' mehfil mein apko Sipahi bana dia gaya! 🛡️", topic),
		"member": fmt.Sprintf("'%s' mehfil mein apko ohday se hata dia gaya.", topic),
	}
	if msg, ok := notifMsgs[newRole]; ok {
		Push(target, currentUser, msg, mehfilLink(mehfilID))
		_, _ = db.Exec(ctx, `INSERT INTO notifications (recipient,sender,type,content,mehfil_id,is_read,created_at) VALUES ($1,$2,$3,$4,$5,false,NOW())`,
			target, currentUser, "mehfil_role", msg, mehfilID)
	}
	var pfp string
	var points int
	var isOnline bool
	db.QueryRow(ctx, "SELECT COALESCE(profile_image,''), COALESCE(points,0), (last_active > NOW() - INTERVAL '5 minutes') FROM users WHERE username=$1", target).
		Scan(&pfp, &points, &isOnline)
	dot := `<span class="mf-offline-dot"></span>`
	if isOnline {
		dot = `<span class="mf-online-dot"></span>`
	}
	badgeCls := "mf-badge-gray"
	switch newRole {
	case "afsar":
		badgeCls = "mf-badge-amber"
	case "sipahi":
		badgeCls = "mf-badge-blue"
	}
	return c.SendString(fmt.Sprintf(`
	<div class="mf-member-row" data-name="%s">
		%s %s
		<div style="flex:1;min-width:0;">
			<div class="mf-member-name"><a href="/profile/%s" style="color:inherit;text-decoration:none;">@%s</a></div>
			<div style="display:flex;gap:5px;align-items:center;margin-top:2px;">
				<span class="mf-badge %s" style="font-size:7px;">%s %s</span>
				<span style="font-size:9px;color:var(--mf-sub);font-weight:600;">%d</span>
			</div>
		</div>
	</div>`, target, dot, avatarHTML(pfp, target, 34), target, target,
		badgeCls, getRoleEmoji(newRole), getRoleLabel(newRole), points))
}

// =============================================================================
// JOIN MEHFIL
// =============================================================================

func JoinMehfil(c *fiber.Ctx) error {
	mehfilID := c.Params("id")
	username := getCurrentUser(c)
	if username == "" {
		return c.Redirect("/signup")
	}
	var adminName, topic string
	var fee, maxMembers, currentMembers int
	db.QueryRow(ctx, "SELECT admin_username, joining_fee, topic, COALESCE(max_members,0) FROM mehfils WHERE id=$1", mehfilID).
		Scan(&adminName, &fee, &topic, &maxMembers)
	db.QueryRow(ctx, "SELECT COUNT(*) FROM mehfil_members WHERE mehfil_id=$1", mehfilID).Scan(&currentMembers)
	if maxMembers > 0 && currentMembers >= maxMembers {
		return c.SendString(`<div class="mf-section" style="text-align:center;padding:20px;"><b>Mehfil bhar gayi! 🚫</b></div>`)
	}
	if username != adminName && fee > 0 {
		var userPoints int
		db.QueryRow(ctx, "SELECT points FROM users WHERE username=$1", username).Scan(&userPoints)
		if userPoints < fee {
			return c.SendString(fmt.Sprintf(`<div class="mf-section" style="text-align:center;padding:20px;background:rgba(239,68,68,.08);border-radius:16px;">
				<div style="font-size:24px;margin-bottom:6px;">💸</div>
				<b style="color:#dc2626;">Points kum hain</b>
				<div style="font-size:12px;color:var(--mf-sub);margin-top:4px;">Chahiye: %d — Aapke paas: %d</div>
			</div>`, fee, userPoints))
		}
		tx, _ := db.Begin(ctx)
		e1 := func() error {
			_, err := tx.Exec(ctx, "UPDATE users SET points=points-$1 WHERE username=$2", fee, username)
			return err
		}()
		e2 := func() error {
			_, err := tx.Exec(ctx, "UPDATE users SET points=points+$1 WHERE username=$2", fee, adminName)
			return err
		}()
		if e1 != nil || e2 != nil {
			tx.Rollback(ctx)
			return c.SendString("Payment fail ho gai")
		}
		tx.Commit(ctx)
	}
	_, err := db.Exec(ctx, "INSERT INTO mehfil_members (mehfil_id, username, role) VALUES ($1,$2,'member') ON CONFLICT DO NOTHING", mehfilID, username)
	if err != nil {
		return c.SendString("Join karne mein masla hua")
	}
	if username != adminName {
		_, _ = db.Exec(ctx, `INSERT INTO notifications (recipient,sender,type,content,is_read) VALUES ($1,$2,'mehfil_join',$3,false)`,
			adminName, username, fmt.Sprintf("@%s ne '%s' mehfil join ki", username, topic))
		Push(adminName, username, fmt.Sprintf("ne '%s' mehfil join ki", topic), mehfilLink(mehfilID))
	}
	return c.SendString(`<div class="mf-section" style="text-align:center;padding:20px;">
		<div style="font-size:28px;margin-bottom:6px;">✅</div>
		<b style="color:var(--mf-accent);">Mehfil join ho gai!</b>
		<div style="font-size:12px;color:var(--mf-sub);margin-top:4px;">Page reload karein messages dekhne ke liye</div>
		<button onclick="window.location.reload()" class="mf-btn mf-btn-green" style="margin-top:12px;width:100%%;">🔄 Reload</button>
	</div>`)
}

// =============================================================================
// TOPIC / RULES / FEE UPDATERS
// =============================================================================

func UpdateMehfilTopic(c *fiber.Ctx) error {
	id := c.Params("id")
	currentUser, err := requireUser(c)
	if err != nil {
		return err
	}
	newTopic := html.EscapeString(strings.TrimSpace(c.Get("HX-Prompt")))
	if newTopic == "" {
		return c.Status(400).SendString("Topic khaali nahi ho sakta")
	}
	var oldTopic string
	if err := db.QueryRow(ctx, "SELECT topic FROM mehfils WHERE id=$1 AND admin_username=$2", id, currentUser).Scan(&oldTopic); err != nil {
		return c.Status(403).SendString("Unauthorized")
	}
	if _, err := db.Exec(ctx, "UPDATE mehfils SET topic=$1 WHERE id=$2 AND admin_username=$3", newTopic, id, currentUser); err != nil {
		return c.Status(500).SendString("Error")
	}
	goSafe("mehfil topic notif", func() {
		_, _ = db.Exec(ctx, `INSERT INTO notifications (recipient,sender,type,content,mehfil_id,is_read,created_at)
			SELECT username,$1,'mehfil_topic_update',$2,$3,false,NOW()
			FROM mehfil_members WHERE mehfil_id=$3 AND username!=$1`,
			currentUser, fmt.Sprintf("Admin ne '%s' mehfil ka naam badal ke '%s' rakh dia.", oldTopic, newTopic), id)
	})
	PushMehfilMembers(id, currentUser, fmt.Sprintf("Admin ne '%s' mehfil ka naam badal ke '%s' rakh dia.", oldTopic, newTopic), mehfilLink(id))
	return c.SendString(newTopic)
}

func UpdateMehfilRules(c *fiber.Ctx) error {
	id := c.Params("id")
	currentUser, err := requireUser(c)
	if err != nil {
		return err
	}
	newRules := html.EscapeString(strings.TrimSpace(c.Get("HX-Prompt")))
	if newRules == "" {
		return c.Status(400).SendString("Usool khaale nahi ho saktay")
	}
	var topic string
	if err := db.QueryRow(ctx, "SELECT topic FROM mehfils WHERE id=$1 AND admin_username=$2", id, currentUser).Scan(&topic); err != nil {
		return c.Status(403).SendString("Unauthorized")
	}
	if _, err := db.Exec(ctx, "UPDATE mehfils SET rules=$1 WHERE id=$2 AND admin_username=$3", newRules, id, currentUser); err != nil {
		return c.Status(500).SendString("Error")
	}
	goSafe("mehfil rules notif", func() {
		_, _ = db.Exec(ctx, `INSERT INTO notifications (recipient,sender,type,content,mehfil_id,is_read,created_at)
			SELECT username,$1,'mehfil_rules_update',$2,$3,false,NOW()
			FROM mehfil_members WHERE mehfil_id=$3 AND username!=$1`,
			currentUser, fmt.Sprintf("'%s' mehfil ke usool badal diye: %s", topic, newRules), id)
	})
	PushMehfilMembers(id, currentUser, fmt.Sprintf("'%s' mehfil ke usool badal diye: %s", topic, newRules), mehfilLink(id))
	return c.SendString(fmt.Sprintf(`"%s"`, newRules))
}

func UpdateJoiningFee(c *fiber.Ctx) error {
	id := c.Params("id")
	currentUser, err := requireUser(c)
	if err != nil {
		return err
	}
	newFee, err := strconv.Atoi(strings.TrimSpace(c.Get("HX-Prompt")))
	if err != nil || newFee < 0 {
		return c.Status(400).SendString("Sahi number likhein")
	}
	if newFee > 1000 {
		newFee = 1000
	}
	var topic string
	if err2 := db.QueryRow(ctx, "SELECT topic FROM mehfils WHERE id=$1 AND admin_username=$2", id, currentUser).Scan(&topic); err2 != nil {
		return c.Status(403).SendString("Unauthorized")
	}
	_, _ = db.Exec(ctx, "UPDATE mehfils SET joining_fee=$1 WHERE id=$2 AND admin_username=$3", newFee, id, currentUser)
	goSafe("mehfil fee notif", func() {
		_, _ = db.Exec(ctx, `INSERT INTO notifications (recipient,sender,type,content,mehfil_id,is_read,created_at)
			SELECT username,$1,'mehfil_fee_update',$2,$3,false,NOW()
			FROM mehfil_members WHERE mehfil_id=$3 AND username!=$1`,
			currentUser, fmt.Sprintf("'%s' mehfil ki joining fee %d points ho gayi.", topic, newFee), id)
	})
	PushMehfilMembers(id, currentUser, fmt.Sprintf("'%s' mehfil ki joining fee %d points ho gayi.", topic, newFee), mehfilLink(id))
	return c.SendString(fmt.Sprintf("%d", newFee))
}

// =============================================================================
// IMAGE UPLOAD
// =============================================================================

func UploadMehfilImage(c *fiber.Ctx) error {
	if _, err := requireUser(c); err != nil {
		return err
	}
	file, err := c.FormFile("image")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Image nahi mili"})
	}
	if file.Size > 10*1024*1024 {
		return c.Status(400).JSON(fiber.Map{"error": "10MB se bari file nahi chalegi"})
	}
	if !map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true}[file.Header.Get("Content-Type")] {
		return c.Status(400).JSON(fiber.Map{"error": "Sirf images allowed hain"})
	}
	dir := "./uploads/mehfil_images"
	os.MkdirAll(dir, 0755)
	filename := fmt.Sprintf("%d_%s", time.Now().UnixMilli(), filepath.Base(file.Filename))
	if err := c.SaveFile(file, filepath.Join(dir, filename)); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Save karne mein masla"})
	}
	return c.JSON(fiber.Map{"url": "/uploads/mehfil_images/" + filename})
}

// =============================================================================
// RE-RENDER HELPER
// =============================================================================

func rerenderMessage(c *fiber.Ctx, msgID, currentUser, mehfilID string) error {
	var m FlatComment
	var role string
	if err := db.QueryRow(ctx, `
		SELECT m.id, m.author, m.content, COALESCE(m.image_url,''), m.created_at, m.parent_id,
		       p.author, LEFT(p.content,50),
		       COALESCE(u.points,0), COALESCE(u.profile_image,''),
		       COALESCE(mem.role, CASE WHEN meh.admin_username=m.author THEN 'admin' ELSE 'member' END),
		       COALESCE(m.reactions,''),
		       COALESCE(m.is_pinned,false),
		       COALESCE(m.is_announcement,false),
		       m.edited_at
		FROM mehfil_messages m
		LEFT JOIN mehfil_messages p ON m.parent_id=p.id
		LEFT JOIN users u ON m.author=u.username
		LEFT JOIN mehfils meh ON m.mehfil_id=meh.id
		LEFT JOIN mehfil_members mem ON m.author=mem.username AND m.mehfil_id=mem.mehfil_id
		WHERE m.id=$1`, msgID).
		Scan(&m.ID, &m.Author, &m.Content, &m.ImageUrl, &m.CreatedAt, &m.ParentID,
			&m.ParentAuthor, &m.ParentSnippet, &m.Points, &m.ProfileImage, &role,
			&m.Reactions, &m.IsPinned, &m.IsAnnouncement, &m.EditedAt); err != nil {
		return c.SendString("")
	}
	var sb strings.Builder
	renderMehfilMessage(&sb, m, role, currentUser, mehfilID)
	return c.SendString(sb.String())
}

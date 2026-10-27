package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// ═══════════════════════════════════════════════════════════════════════════
// DELETE FULL CONVERSATION HANDLER
// Route: DELETE /chat/conversation/:username
// ═══════════════════════════════════════════════════════════════════════════
func DeleteConversationHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	otherUser := strings.ToLower(strings.TrimSpace(c.Params("username")))

	if currentUser == "" || otherUser == "" {
		return c.Status(400).SendString("invalid users")
	}

	CacheSet(chatClearedKey(currentUser, otherUser), fmt.Sprintf("%d", time.Now().UnixNano()), 0)

	log.Printf("[DeleteConversation] cleared for viewer only, no DB writes: %s → %s", currentUser, otherUser)
	return c.SendStatus(200)
}

// ═══════════════════════════════════════════════════════════════════════════
// CHAT VIEW HANDLER
// Route: GET /chat/:username
// ═══════════════════════════════════════════════════════════════════════════
func ChatViewHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Redirect("/login")
	}
	otherUser := strings.ToLower(strings.TrimSpace(c.Params("username")))

	log.Printf("[Chat] %s → %s", currentUser, otherUser)

	var status string
	var profileImage string

	err := db.QueryRow(ctx, `
        SELECT 
            u.profile_image, 
            COALESCE((
                SELECT status FROM chat_invites 
                WHERE (LOWER(sender) = LOWER($1) AND LOWER(recipient) = LOWER($2)) 
                   OR (LOWER(sender) = LOWER($2) AND LOWER(recipient) = LOWER($1))
                LIMIT 1
            ), 'none') AS invite_status
        FROM users u 
        WHERE LOWER(u.username) = $2`,
		currentUser, otherUser).Scan(&profileImage, &status)

	if err != nil {
		return c.SendString(fmt.Sprintf("User not found: %s", otherUser))
	}

	if status != "accepted" {
		return c.Redirect("/invites?error=not_authorized")
	}

	imgSrc := profileImage
	if imgSrc == "" {
		imgSrc = "https://ui-avatars.com/api/?name=" + otherUser + "&background=random"
	}
	avatarHTML := fmt.Sprintf(
		`<img src="%s" alt="%s" class="chv-avatar-img" onerror="this.onerror=null;this.src='https://ui-avatars.com/api/?name='+encodeURIComponent(this.alt)+'&background=random'">`,
		imgSrc, otherUser,
	)

	chatPage := fmt.Sprintf(`
<style>
/* ═══════════════════════════════════════════
   DOSTANA CHAT — WHATSAPP-INSPIRED MOBILE UI
   ═══════════════════════════════════════════ */

:root {
  --chat-bg:        #ECE5DD;
  --bubble-me:      #DCF8C6;
  --bubble-other:   #ffffff;
  --header-bg:      #075E54;
  --footer-bg:      #f0f2f5;
  --accent:         #25D366;
  --accent2:        #128C7E;
  --text-main:      #111b21;
  --text-meta:      #667781;
  --tick-sent:      #667781;
  --tick-read:      #53bdeb;
  --border:         #d1d7db;
  --radius-bubble:  7.5px;
  --font-chat:      -apple-system, 'SF Pro Display', 'Segoe UI', Arial, sans-serif;
}

/* ── Force hide ALL bottom navs when on chat page ─────────────────────── */
body.chv-active .fixed.bottom-0,
body.chv-active [class*="bottom-nav"],
body.chv-active [id*="bottom-nav"],
body.chv-active [class*="bottomNav"],
body.chv-active [id*="bottomNav"],
body.chv-active [class*="bottom-bar"],
body.chv-active [id*="bottom-bar"],
body.chv-active div[class*="fixed"][class*="left-0"][class*="right-0"]:not(.chv-shell):not(.chv-shell *),
body.chv-active nav:not(.chv-shell nav) {
  display: none !important;
  visibility: hidden !important;
}

body.chv-active {
  overflow: hidden !important;
}

/* ── Layout Shell ─────────────────────────────────────────────────────── */
.chv-shell {
  display: flex;
  flex-direction: column;
  position: fixed;
  top: 0; left: 0; right: 0; bottom: 0;
  z-index: 99999;
  background: var(--chat-bg);
  font-family: var(--font-chat);
  overflow: hidden;
}

/* ── Header ──────────────────────────────────── */
.chv-header {
  flex: none;
  background: var(--header-bg);
  padding: 8px 12px;
  padding-top: calc(8px + env(safe-area-inset-top, 0px));
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 56px;
  box-shadow: 0 1px 3px rgba(0,0,0,0.2);
  position: relative;
  z-index: 10;
}

.chv-header-back {
  color: white;
  text-decoration: none;
  display: flex;
  align-items: center;
  padding: 4px;
  margin-left: -4px;
  flex: none;
  min-width: 44px;
  min-height: 44px;
  justify-content: center;
}
.chv-header-back svg { width: 22px; height: 22px; }

.chv-avatar-wrap { position: relative; flex: none; }
.chv-avatar-img {
  width: 40px; height: 40px;
  border-radius: 50%%;
  object-fit: cover;
  border: 2px solid rgba(255,255,255,0.3);
  display: block;
}

.chv-online-dot {
  position: absolute;
  bottom: 1px; right: 1px;
  width: 10px; height: 10px;
  border-radius: 50%%;
  background: var(--accent);
  border: 2px solid var(--header-bg);
  display: none;
}
.chv-online-dot.visible { display: block; }

.chv-header-info { flex: 1; min-width: 0; }
.chv-header-name {
  color: white;
  font-size: 16px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.chv-header-status {
  font-size: 12px;
  color: rgba(255,255,255,0.75);
  margin-top: 1px;
}

.chv-header-actions { display: flex; gap: 4px; flex: none; }
.chv-icon-btn {
  color: white;
  background: none;
  border: none;
  cursor: pointer;
  padding: 8px;
  border-radius: 50%%;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background 0.15s;
  -webkit-tap-highlight-color: transparent;
  min-width: 44px;
  min-height: 44px;
}
.chv-icon-btn:active { background: rgba(255,255,255,0.15); }
.chv-icon-btn svg { width: 22px; height: 22px; }

/* ── More / Kebab Dropdown ────────────────────── */
.chv-more-menu {
  position: absolute;
  top: calc(56px + env(safe-area-inset-top, 0px));
  right: 8px;
  background: white;
  border-radius: 10px;
  box-shadow: 0 8px 32px rgba(0,0,0,0.22);
  z-index: 99999;
  min-width: 210px;
  overflow: hidden;
  display: none;
  animation: ctxIn 0.12s ease;
}
.chv-more-menu.open { display: block; }
.chv-more-menu-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 18px;
  font-size: 14px;
  font-family: var(--font-chat);
  color: var(--text-main);
  cursor: pointer;
  background: none;
  border: none;
  width: 100%%;
  text-align: left;
  -webkit-tap-highlight-color: transparent;
  transition: background 0.12s;
}
.chv-more-menu-item:active { background: rgba(0,0,0,0.07); }
.chv-more-menu-item svg { width: 18px; height: 18px; flex: none; }
.chv-more-menu-item.danger { color: #f44336; }
.chv-more-menu-item.danger svg { color: #f44336; }

/* ── Chat Background ──────────────────────────── */
.chv-messages {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 8px 6px 12px;
  -webkit-overflow-scrolling: touch;
  background-color: var(--chat-bg);
  min-height: 0;
  background-image: url("data:image/svg+xml,%%3Csvg width='60' height='60' viewBox='0 0 60 60' xmlns='http://www.w3.org/2000/svg'%%3E%%3Cg fill='none' fill-rule='evenodd'%%3E%%3Cg fill='%%23128C7E' fill-opacity='0.05'%%3E%%3Cpath d='M36 34v-4h-2v4h-4v2h4v4h2v-4h4v-2h-4zm0-30V0h-2v4h-4v2h4v4h2V6h4V4h-4zM6 34v-4H4v4H0v2h4v4h2v-4h4v-2H6zM6 4V0H4v4H0v2h4v4h2V6h4V4H6z'/%3E%%3C/g%%3E%%3C/g%%3E%%3C/svg%%3E");
}
.chv-messages::-webkit-scrollbar { width: 3px; }
.chv-messages::-webkit-scrollbar-thumb { background: rgba(0,0,0,0.15); border-radius: 3px; }

/* ── Load More Spinner (top of list) ─────────────── */
.chv-load-more-spinner {
  display: flex;
  justify-content: center;
  padding: 12px;
}
.chv-load-more-spinner .chv-spinner {
  width: 22px; height: 22px;
  border: 3px solid rgba(18,140,126,0.2);
  border-top-color: var(--accent2);
  border-radius: 50%%;
  animation: spin 0.7s linear infinite;
}

/* ── Date Divider ─────────────────────────────── */
.chv-date-divider {
  display: flex;
  justify-content: center;
  margin: 10px 0 6px;
}
.chv-date-divider span {
  background: rgba(255,255,255,0.85);
  color: #667781;
  font-size: 11.5px;
  font-weight: 600;
  padding: 3px 10px;
  border-radius: 8px;
  box-shadow: 0 1px 2px rgba(0,0,0,0.13);
}

/* ── Message Row ──────────────────────────────── */
.chv-msg-row {
  display: flex;
  margin-bottom: 2px;
  animation: msgIn 0.18s ease;
}
@keyframes msgIn {
  from { opacity: 0; transform: translateY(6px); }
  to   { opacity: 1; transform: translateY(0); }
}
.chv-msg-row.me    { justify-content: flex-end; }
.chv-msg-row.other { justify-content: flex-start; }
.chv-msg-row.me + .chv-msg-row.other,
.chv-msg-row.other + .chv-msg-row.me { margin-top: 6px; }

/* ── Seen Avatar ─────────────────────────────── */
.chv-msg-seen-avatar {
  width: 16px; height: 16px;
  border-radius: 50%%;
  object-fit: cover;
  align-self: flex-end;
  margin-right: 3px;
  flex: none;
  opacity: 0;
  transition: opacity 0.3s;
}
.chv-msg-seen-avatar.visible { opacity: 1; }

/* ── Bubble ───────────────────────────────────── */
.chv-bubble-wrap {
  display: flex;
  flex-direction: column;
  max-width: min(80vw, 320px);
  position: relative;
}
.chv-msg-row.me .chv-bubble-wrap    { align-items: flex-end; }
.chv-msg-row.other .chv-bubble-wrap { align-items: flex-start; }

.chv-bubble {
  position: relative;
  padding: 6px 9px 18px;
  border-radius: var(--radius-bubble);
  word-break: break-word;
  font-size: 14.5px;
  line-height: 1.45;
  color: var(--text-main);
  box-shadow: 0 1px 1px rgba(0,0,0,0.13);
  -webkit-tap-highlight-color: transparent;
  user-select: text;
  min-width: 60px;
}
.chv-msg-row.me .chv-bubble    { background: var(--bubble-me);    border-bottom-right-radius: 2px; }
.chv-msg-row.other .chv-bubble { background: var(--bubble-other); border-bottom-left-radius: 2px; }

.chv-msg-row.me .chv-bubble::after {
  content: '';
  position: absolute;
  bottom: 0; right: -7px;
  border-style: solid;
  border-width: 0 0 8px 8px;
  border-color: transparent transparent var(--bubble-me) transparent;
}
.chv-msg-row.other .chv-bubble::after {
  content: '';
  position: absolute;
  bottom: 0; left: -7px;
  border-style: solid;
  border-width: 0 8px 8px 0;
  border-color: transparent var(--bubble-other) transparent transparent;
}

/* ── Bubble Image ─────────────────────────────── */
.chv-bubble-img {
  max-width: 100%%;
  max-height: 260px;
  border-radius: 6px;
  object-fit: cover;
  display: block;
  margin-bottom: 4px;
  cursor: zoom-in;
}

/* ── Voice Message ────────────────────────────── */
.chv-voice-player {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 200px;
  padding: 2px 0;
}
.chv-voice-play-btn {
  width: 36px; height: 36px;
  border-radius: 50%%;
  background: var(--accent2);
  border: none;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  flex: none;
  -webkit-tap-highlight-color: transparent;
  transition: transform 0.1s;
}
.chv-voice-play-btn:active { transform: scale(0.92); }
.chv-voice-play-btn svg { width: 18px; height: 18px; color: white; }
.chv-voice-waveform {
  flex: 1;
  height: 28px;
  display: flex;
  align-items: center;
  gap: 2px;
  overflow: hidden;
}
.chv-voice-bar {
  flex: 1;
  background: var(--accent2);
  opacity: 0.5;
  border-radius: 2px;
  min-height: 3px;
  transition: opacity 0.15s;
}
.chv-voice-bar.active { opacity: 1; }
.chv-voice-time { font-size: 11px; color: var(--text-meta); white-space: nowrap; min-width: 28px; }

/* ── Reply Quote ──────────────────────────────── */
.chv-reply-quote {
  background: rgba(0,0,0,0.07);
  border-left: 3px solid var(--accent2);
  border-radius: 4px;
  padding: 4px 8px;
  margin-bottom: 4px;
  font-size: 12.5px;
  cursor: pointer;
}
.chv-reply-quote-author { color: var(--accent2); font-weight: 600; font-size: 12px; margin-bottom: 2px; }
.chv-reply-quote-text   { color: var(--text-meta); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 220px; }

/* ── Bubble Meta ─────────────────────────────── */
.chv-bubble-meta {
  position: absolute;
  bottom: 3px; right: 7px;
  display: flex;
  align-items: center;
  gap: 3px;
  pointer-events: none;
}
.chv-bubble-time { font-size: 10.5px; color: var(--text-meta); white-space: nowrap; }
.chv-ticks { display: flex; align-items: center; }
.chv-ticks svg { width: 15px; height: 15px; }
.chv-ticks.sent svg { color: var(--tick-sent); }
.chv-ticks.read  svg { color: var(--tick-read); }

/* ── Deleted Message ──────────────────────────── */
.chv-bubble-deleted { font-style: italic; color: var(--text-meta) !important; opacity: 0.8; }

/* ── Sending state bubble ─────────────────────── */
.chv-bubble-sending {
  opacity: 0.65;
}
.chv-bubble-error {
  border: 1px solid rgba(244,67,54,0.4);
}
.chv-retry-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-top: 4px;
  padding: 3px 10px;
  border-radius: 12px;
  border: none;
  background: rgba(244,67,54,0.12);
  color: #c62828;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
}
.chv-retry-btn:active { background: rgba(244,67,54,0.22); }

/* ── Per-bubble delete button ─────────────────── */
.chv-del-btn {
  display: none;
  align-items: center;
  justify-content: center;
  width: 26px; height: 26px;
  border-radius: 50%%;
  border: none;
  background: rgba(244,67,54,0.12);
  color: #f44336;
  cursor: pointer;
  flex: none;
  margin-top: 2px;
  -webkit-tap-highlight-color: transparent;
  transition: background 0.15s, transform 0.1s;
  touch-action: manipulation;
}
.chv-del-btn svg { width: 13px; height: 13px; pointer-events: none; }
.chv-del-btn:active { background: rgba(244,67,54,0.25); transform: scale(0.9); }
.chv-msg-row:hover .chv-del-btn { display: flex; }
.chv-msg-row.chv-show-del .chv-del-btn { display: flex; }
.chv-msg-row.me    { align-items: flex-end; }
.chv-msg-row.other { align-items: flex-end; }

/* ── Context Menu ─────────────────────────────── */
.chv-ctx-overlay {
  position: fixed; top: 0; left: 0; right: 0; bottom: 0;
  z-index: 99997; display: none;
}
.chv-ctx-overlay.open { display: block; }

.chv-ctx-menu {
  position: fixed;
  background: white;
  border-radius: 10px;
  box-shadow: 0 8px 32px rgba(0,0,0,0.22);
  overflow: hidden;
  z-index: 99998;
  min-width: 160px;
  display: none;
  animation: ctxIn 0.12s ease;
}
@keyframes ctxIn {
  from { opacity: 0; transform: scale(0.92); }
  to   { opacity: 1; transform: scale(1); }
}
.chv-ctx-menu.open { display: block; }
.chv-ctx-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 13px 18px;
  font-size: 14px;
  color: var(--text-main);
  cursor: pointer;
  transition: background 0.12s;
  border: none;
  background: none;
  width: 100%%;
  text-align: left;
  -webkit-tap-highlight-color: transparent;
}
.chv-ctx-item:active { background: rgba(0,0,0,0.07); }
.chv-ctx-item svg { width: 18px; height: 18px; color: var(--accent2); }
.chv-ctx-item.danger { color: #f44336; }
.chv-ctx-item.danger svg { color: #f44336; }

/* ── Reply Bar ────────────────────────────────── */
.chv-reply-bar {
  display: none;
  padding: 8px 12px;
  background: var(--footer-bg);
  border-top: 1px solid var(--border);
  align-items: center;
  gap: 10px;
  flex: none;
}
.chv-reply-bar.active { display: flex; }
.chv-reply-bar-content {
  flex: 1;
  border-left: 3px solid var(--accent2);
  padding: 3px 8px;
  min-width: 0;
}
.chv-reply-bar-author { font-size: 12px; font-weight: 700; color: var(--accent2); }
.chv-reply-bar-text   { font-size: 12.5px; color: var(--text-meta); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.chv-reply-bar-close {
  background: none; border: none; color: var(--text-meta);
  cursor: pointer; padding: 4px; -webkit-tap-highlight-color: transparent;
  min-width: 44px; min-height: 44px;
  display: flex; align-items: center; justify-content: center;
}
.chv-reply-bar-close svg { width: 20px; height: 20px; }

/* ── Footer ───────────────────────────────────── */
.chv-footer {
  flex: none;
  background: var(--footer-bg);
  padding: 8px;
  padding-bottom: calc(8px + env(safe-area-inset-bottom, 0px));
  border-top: 1px solid var(--border);
  z-index: 10;
}

.chv-file-indicator {
  display: none;
  background: #e8f5e9;
  border-radius: 8px;
  padding: 6px 10px;
  margin-bottom: 6px;
  font-size: 12px;
  color: #2e7d32;
  align-items: center;
  gap: 6px;
  font-weight: 600;
}
.chv-file-indicator.show { display: flex; }
.chv-file-indicator-clear {
  margin-left: auto; background: none; border: none;
  font-size: 18px; color: #e53935; cursor: pointer; line-height: 1; padding: 0;
}

.chv-input-row {
  display: flex;
  align-items: flex-end;
  gap: 6px;
}

.chv-attach-btn {
  flex: none;
  width: 44px; height: 44px;
  border-radius: 50%%;
  background: none;
  border: none;
  color: var(--text-meta);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  -webkit-tap-highlight-color: transparent;
  transition: color 0.15s;
  touch-action: manipulation;
}
.chv-attach-btn:active { color: var(--accent2); }
.chv-attach-btn svg { width: 24px; height: 24px; }

/* ── Input Field ──────────────────────────────── */
.chv-input-wrap {
  flex: 1;
  display: flex;
  align-items: flex-end;
  gap: 6px;
  min-width: 0;
}

.chv-input-field {
  flex: 1;
  min-width: 0;
  background: white;
  border: none;
  border-radius: 22px;
  padding: 11px 16px;
  font-family: var(--font-chat);
  color: var(--text-main);
  outline: none;
  resize: none;
  max-height: 100px;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
  line-height: 1.4;
  box-shadow: 0 1px 2px rgba(0,0,0,0.1);
  font-size: 16px;
  -webkit-appearance: none;
}
.chv-input-field::placeholder { color: #aaa; font-size: 15px; }

/* ── Send Button ──────────────────────────────── */
.chv-send-btn {
  flex: none;
  width: 44px; height: 44px;
  border-radius: 50%%;
  background: var(--accent);
  border: none;
  cursor: pointer;
  align-items: center;
  justify-content: center;
  -webkit-tap-highlight-color: transparent;
  transition: background 0.15s, transform 0.1s;
  box-shadow: 0 2px 6px rgba(37,211,102,0.35);
  touch-action: manipulation;
}
.chv-send-btn:active { transform: scale(0.9); background: var(--accent2); }
.chv-send-btn svg { width: 22px; height: 22px; color: white; }
.chv-send-btn:disabled { opacity: 0.5; cursor: not-allowed; transform: none; }

/* ── Voice Button ─────────────────────────────── */
.chv-voice-btn {
  flex: none;
  width: 44px; height: 44px;
  border-radius: 50%%;
  background: var(--accent);
  border: none;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  -webkit-tap-highlight-color: transparent;
  transition: background 0.15s, transform 0.1s;
  box-shadow: 0 2px 6px rgba(37,211,102,0.35);
  touch-action: manipulation;
}
.chv-voice-btn:active { transform: scale(0.9); }
.chv-voice-btn svg { width: 22px; height: 22px; color: white; }
.chv-voice-btn.recording {
  background: #f44336;
  animation: pulse-record 0.8s infinite;
  box-shadow: 0 2px 12px rgba(244,67,54,0.5);
}
@keyframes pulse-record {
  0%%,100%% { transform: scale(1); }
  50%%       { transform: scale(1.08); }
}

/* ── Recording Row ────────────────────────────── */
.chv-voice-recording-row {
  display: none;
  align-items: center;
  gap: 8px;
  flex: 1;
}
.chv-voice-recording-row.active { display: flex; }
.chv-rec-dot {
  width: 10px; height: 10px;
  border-radius: 50%%;
  background: #f44336;
  animation: blink 0.7s infinite;
}
@keyframes blink { 0%%,100%% { opacity:1; } 50%% { opacity:0.2; } }
.chv-voice-cancel {
  background: none; border: none; color: var(--text-meta);
  font-size: 13px; font-weight: 600; cursor: pointer;
  -webkit-tap-highlight-color: transparent; padding: 4px 8px;
}

/* ── Image Permission Banner ──────────────────── */
.chv-img-permission-banner {
  display: none;
  background: #fff3e0;
  border-left: 3px solid #ff9800;
  border-radius: 8px;
  padding: 8px 12px;
  margin-bottom: 6px;
  font-size: 12.5px;
  color: #e65100;
  align-items: center;
  gap: 8px;
  font-weight: 500;
}
.chv-img-permission-banner.show { display: flex; }

/* ── Emoji Panel ──────────────────────────────── */
.chv-emoji-overlay {
  position: absolute;
  top: 0; left: 0; right: 0; bottom: 0;
  z-index: 100;
  display: none;
  background: rgba(0,0,0,0.3);
  pointer-events: none;
}
.chv-emoji-overlay.open {
  display: block;
  pointer-events: auto;
}

.chv-emoji-panel {
  position: absolute;
  bottom: 0; left: 0; right: 0;
  background: white;
  border-radius: 20px 20px 0 0;
  padding: 16px 16px;
  padding-bottom: calc(16px + env(safe-area-inset-bottom, 0px));
  z-index: 101;
  box-shadow: 0 -4px 24px rgba(0,0,0,0.15);
  display: none;
  animation: slideUp 0.25s ease;
  max-height: 70vh;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
}
@keyframes slideUp {
  from { transform: translateY(100%%); }
  to   { transform: translateY(0); }
}
.chv-emoji-panel.open { display: block; }

.chv-emoji-panel-title {
  font-size: 12px; font-weight: 700; color: var(--text-meta);
  text-align: center; margin-bottom: 12px; margin-top: 12px;
  text-transform: uppercase; letter-spacing: 0.05em;
}
.chv-emoji-status-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 10px;
  margin-bottom: 16px;
}
.chv-emoji-status-btn {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 5px;
  background: #f0f2f5;
  border-radius: 12px;
  padding: 12px 6px;
  border: none;
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  transition: background 0.15s, transform 0.1s;
  touch-action: manipulation;
}
.chv-emoji-status-btn:active { transform: scale(0.92); background: #e8f5e9; }
.chv-emoji-status-btn .em { font-size: 30px; line-height: 1; }
.chv-emoji-status-btn .label { font-size: 10px; font-weight: 600; color: var(--text-meta); text-align: center; }
.chv-emoji-quick { display: flex; gap: 6px; flex-wrap: wrap; }
.chv-emoji-quick-btn {
  font-size: 22px; background: none; border: none; cursor: pointer;
  padding: 8px; border-radius: 8px; -webkit-tap-highlight-color: transparent;
  transition: background 0.12s; line-height: 1;
  touch-action: manipulation; min-width: 40px; min-height: 40px;
  display: flex; align-items: center; justify-content: center;
}
.chv-emoji-quick-btn:active { background: rgba(0,0,0,0.07); }

/* ── Scroll FAB ───────────────────────────────── */
.chv-scroll-fab {
  position: absolute;
  bottom: 80px; right: 12px;
  width: 38px; height: 38px;
  border-radius: 50%%;
  background: white;
  box-shadow: 0 2px 10px rgba(0,0,0,0.2);
  display: none;
  align-items: center;
  justify-content: center;
  cursor: pointer; border: none;
  z-index: 20;
  -webkit-tap-highlight-color: transparent;
}
.chv-scroll-fab.show { display: flex; }
.chv-scroll-fab svg { width: 20px; height: 20px; color: var(--accent2); }

/* ── PAUSED Badge ─────────────────────────────── */
.chv-paused-badge {
  position: absolute;
  top: 60px; left: 50%%;
  transform: translateX(-50%%);
  background: rgba(0,0,0,0.7);
  color: white; font-size: 11px; font-weight: 700;
  padding: 4px 12px; border-radius: 20px;
  display: none; z-index: 50;
}
.chv-paused-badge.show { display: block; }

/* ── Loading Spinner (initial load) ──────────── */
.chv-loading { display: flex; justify-content: center; padding: 24px; }
.chv-spinner {
  width: 28px; height: 28px;
  border: 3px solid rgba(18,140,126,0.2);
  border-top-color: var(--accent2);
  border-radius: 50%%;
  animation: spin 0.7s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }

/* ── Perm Request ─────────────────────────────── */
.chv-perm-request-msg {
  background: #fff8e1; border: 1px solid #ffd54f; border-radius: 10px;
  padding: 10px 14px; font-size: 13px; color: #5d4037;
  text-align: center; max-width: 260px; margin: 0 auto;
}
.chv-perm-approve-btn, .chv-perm-deny-btn {
  display: inline-block; margin-top: 8px; padding: 6px 14px;
  border-radius: 16px; font-size: 12.5px; font-weight: 700;
  cursor: pointer; border: none; -webkit-tap-highlight-color: transparent;
  touch-action: manipulation;
}
.chv-perm-approve-btn { background: var(--accent); color: white; margin-right: 6px; }
.chv-perm-deny-btn    { background: #eee; color: #666; }

/* ── Lightbox ─────────────────────────────────── */
.chv-lightbox {
  position: fixed; top: 0; left: 0; right: 0; bottom: 0;
  background: rgba(0,0,0,0.95);
  z-index: 999999; display: none;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  padding: 20px;
}
.chv-lightbox.open { display: flex; }
.chv-lightbox img { max-width: 100%%; max-height: 80vh; object-fit: contain; border-radius: 8px; }
.chv-lightbox-close {
  position: absolute; top: 16px; right: 16px;
  color: white; background: rgba(255,255,255,0.15); border: none;
  width: 44px; height: 44px; border-radius: 50%%; font-size: 20px;
  cursor: pointer; display: flex; align-items: center; justify-content: center;
}

/* ── Offline Banner ───────────────────────────── */
.chv-offline-banner {
  display: none;
  background: #fbe9e7;
  border-bottom: 1px solid #ffccbc;
  padding: 6px 12px;
  font-size: 12px;
  color: #bf360c;
  font-weight: 600;
  text-align: center;
  flex: none;
}
.chv-offline-banner.show { display: block; }
</style>

<div class="chv-shell" id="chv-shell">

  <!-- HEADER -->
  <header class="chv-header">
    <a href="/invites" class="chv-header-back" id="chv-back-btn">
      <svg fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M15 19l-7-7 7-7"/></svg>
    </a>
    <div class="chv-avatar-wrap">
      <a href="/profile/%[1]s">%[2]s</a>
      <div class="chv-online-dot" id="chv-online-dot"></div>
    </div>
    <div class="chv-header-info">
      <div class="chv-header-name">@%[1]s%[6]s</div>
      <div class="chv-header-status" id="chv-header-status"
           hx-get="/user/status/%[1]s"
           hx-trigger="load, every 20s"
           hx-swap="innerHTML">
        Connecting...
      </div>
    </div>
    <div class="chv-header-actions">
      <button class="chv-icon-btn" id="chv-emoji-header-btn" title="Status emoji">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14.828 14.828a4 4 0 01-5.656 0M9 10h.01M15 10h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/></svg>
      </button>
      <button class="chv-icon-btn" id="chv-more-btn" title="More options">
        <svg viewBox="0 0 24 24" fill="currentColor">
          <circle cx="12" cy="5"  r="1.8"/>
          <circle cx="12" cy="12" r="1.8"/>
          <circle cx="12" cy="19" r="1.8"/>
        </svg>
      </button>
    </div>

    <div class="chv-more-menu" id="chv-more-menu">
      <button class="chv-more-menu-item danger" type="button" id="chv-delete-convo-btn">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"/>
        </svg>
        Delete Conversation
      </button>
    </div>
  </header>

  <!-- OFFLINE BANNER -->
  <div class="chv-offline-banner" id="chv-offline-banner">⚠ No connection — messages will be retried automatically</div>

  <!-- PAUSED BADGE -->
  <div class="chv-paused-badge" id="chv-paused">⏸ Scroll paused</div>

  <!-- MESSAGES AREA -->
  <div class="chv-messages" id="chv-messages">
    <div class="chv-loading" id="chv-initial-spinner"><div class="chv-spinner"></div></div>
  </div>

  <!-- SCROLL FAB -->
  <button class="chv-scroll-fab" id="chv-scroll-fab" type="button">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M19 9l-7 7-7-7"/></svg>
  </button>

  <!-- REPLY BAR -->
  <div class="chv-reply-bar" id="chv-reply-bar">
    <div class="chv-reply-bar-content">
      <div class="chv-reply-bar-author" id="chv-reply-author"></div>
      <div class="chv-reply-bar-text" id="chv-reply-text"></div>
    </div>
    <button class="chv-reply-bar-close" type="button" id="chv-reply-close">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/></svg>
    </button>
  </div>

  <!-- FOOTER -->
  <footer class="chv-footer">
    <div class="chv-img-permission-banner" id="chv-img-perm-banner">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z"/></svg>
      <span id="chv-img-perm-text">Requesting image permission…</span>
    </div>

    <div class="chv-file-indicator" id="chv-file-indicator">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14"/></svg>
      <span id="chv-file-name">file.jpg</span>
      <button class="chv-file-indicator-clear" type="button" id="chv-file-clear">&times;</button>
    </div>

    <div class="chv-input-row">
      <button class="chv-attach-btn" type="button" id="chv-emoji-btn" title="Emoji">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14.828 14.828a4 4 0 01-5.656 0M9 10h.01M15 10h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/></svg>
      </button>

      <div class="chv-voice-recording-row" id="chv-recording-row">
        <div class="chv-rec-dot"></div>
        <span style="flex:1;font-size:14px;font-weight:600;color:#f44336;" id="chv-rec-timer">0:00</span>
        <button class="chv-voice-cancel" type="button" id="chv-cancel-rec">✕ Cancel</button>
      </div>

      <div class="chv-input-wrap" id="chv-input-wrap">
        <input type="hidden" id="chv-reply-id" value="">
        <input type="file" id="chv-file-input" accept="image/*" style="display:none">
        <textarea id="chv-input"
                  rows="1"
                  autocomplete="off"
                  autocorrect="on"
                  autocapitalize="sentences"
                  placeholder="Type a message"
                  class="chv-input-field"
                  enterkeyhint="send"></textarea>
        <button type="button" class="chv-send-btn" id="chv-send-btn" style="display:none">
          <svg viewBox="0 0 24 24" fill="currentColor"><path d="M2 21l21-9L2 3v7l15 2-15 2v7z"/></svg>
        </button>
      </div>

      <button class="chv-attach-btn" type="button" id="chv-img-btn" title="Send image">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z"/></svg>
      </button>

      <button class="chv-voice-btn" type="button" id="chv-voice-btn">
        <svg id="chv-mic-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 11a7 7 0 01-7 7m0 0a7 7 0 01-7-7m7 7v4m0 0H8m4 0h4m-4-8a3 3 0 01-3-3V5a3 3 0 116 0v6a3 3 0 01-3 3z"/></svg>
      </button>
    </div>
  </footer>

  <!-- EMOJI PANEL -->
  <div class="chv-emoji-overlay" id="chv-emoji-overlay"></div>
  <div class="chv-emoji-panel" id="chv-emoji-panel">
    <div style="position:absolute;top:8px;left:50%%;transform:translateX(-50%%);width:40px;height:4px;background:#ddd;border-radius:2px;"></div>
    <div class="chv-emoji-panel-title">Share your vibe</div>
    <div class="chv-emoji-status-grid">
      <button class="chv-emoji-status-btn" type="button" data-emoji="😊" data-status="feeling happy"><span class="em">😊</span><span class="label">Happy</span></button>
      <button class="chv-emoji-status-btn" type="button" data-emoji="😢" data-status="crying"><span class="em">😢</span><span class="label">Crying</span></button>
      <button class="chv-emoji-status-btn" type="button" data-emoji="😔" data-status="feeling sad"><span class="em">😔</span><span class="label">Sad</span></button>
      <button class="chv-emoji-status-btn" type="button" data-emoji="😴" data-status="sleepy"><span class="em">😴</span><span class="label">Sleepy</span></button>
      <button class="chv-emoji-status-btn" type="button" data-emoji="😡" data-status="feeling angry"><span class="em">😡</span><span class="label">Angry</span></button>
      <button class="chv-emoji-status-btn" type="button" data-emoji="🤩" data-status="excited"><span class="em">🤩</span><span class="label">Excited</span></button>
      <button class="chv-emoji-status-btn" type="button" data-emoji="😂" data-status="laughing"><span class="em">😂</span><span class="label">Laughing</span></button>
      <button class="chv-emoji-status-btn" type="button" data-emoji="❤️" data-status="in love"><span class="em">❤️</span><span class="label">In Love</span></button>
    </div>
    <div style="font-size:11px;font-weight:700;color:var(--text-meta);margin-bottom:8px;">QUICK EMOJI</div>
    <div class="chv-emoji-quick">
      <button class="chv-emoji-quick-btn" type="button" data-insert="😊">😊</button>
      <button class="chv-emoji-quick-btn" type="button" data-insert="❤️">❤️</button>
      <button class="chv-emoji-quick-btn" type="button" data-insert="😂">😂</button>
      <button class="chv-emoji-quick-btn" type="button" data-insert="🙏">🙏</button>
      <button class="chv-emoji-quick-btn" type="button" data-insert="👍">👍</button>
      <button class="chv-emoji-quick-btn" type="button" data-insert="😍">😍</button>
      <button class="chv-emoji-quick-btn" type="button" data-insert="🔥">🔥</button>
      <button class="chv-emoji-quick-btn" type="button" data-insert="😢">😢</button>
      <button class="chv-emoji-quick-btn" type="button" data-insert="😎">😎</button>
      <button class="chv-emoji-quick-btn" type="button" data-insert="✨">✨</button>
      <button class="chv-emoji-quick-btn" type="button" data-insert="😴">😴</button>
      <button class="chv-emoji-quick-btn" type="button" data-insert="🤗">🤗</button>
    </div>
    <button type="button" id="chv-emoji-close" style="display:block;margin:16px auto 0;padding:10px 32px;background:#f0f2f5;border:none;border-radius:20px;font-size:14px;font-weight:600;color:var(--text-meta);cursor:pointer;-webkit-tap-highlight-color:transparent;touch-action:manipulation;">Close</button>
  </div>

</div>

<!-- CONTEXT MENU -->
<div class="chv-ctx-overlay" id="chv-ctx-overlay"></div>
<div class="chv-ctx-menu" id="chv-ctx-menu">
  <button class="chv-ctx-item" type="button" id="ctx-reply-btn">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 10h10a8 8 0 018 8v2M3 10l6 6m-6-6l6-6"/></svg>
    Reply
  </button>
  <button class="chv-ctx-item" type="button" id="ctx-copy-btn">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"/></svg>
    Copy
  </button>
  <button class="chv-ctx-item danger" type="button" id="ctx-delete-btn">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"/></svg>
    Delete
  </button>
</div>

<!-- LIGHTBOX -->
<div class="chv-lightbox" id="chv-lightbox">
  <button class="chv-lightbox-close" type="button" id="chv-lightbox-close">&times;</button>
  <img id="chv-lightbox-img" src="" alt="image">
</div>

<script>
(function() {
'use strict';

// ── Constants ──────────────────────────────────────────────────────────────
var CURRENT_USER    = %[3]q;
var OTHER_USER      = %[4]q;
var OTHER_AVATAR    = %[5]q;

// Send retry config
var SEND_MAX_RETRIES   = 5;           // max attempts before marking failed
var SEND_BASE_DELAY_MS = 800;         // first retry after 800 ms
var SEND_MAX_DELAY_MS  = 12000;       // cap each backoff at 12 s
var POLL_INTERVAL_MS   = 3000;        // normal poll cadence
var POLL_INFLIGHT_GUARD= false;       // true while a poll fetch is active

// ── State ──────────────────────────────────────────────────────────────────
var isPaused        = false;
var replyToId       = null;
var ctxMsgId        = null;
var ctxMsgAuthor    = '';
var ctxMsgText      = '';
var mediaRecorder   = null;
var audioChunks     = [];
var recTimerInt     = null;
var recSeconds      = 0;
var imgPermGranted  = false;
var imgPermPending  = false;
var lastMsgTs       = 0;
var oldestMsgId     = 0;
var pollTimer       = null;
var newMsgCount     = 0;
var isRecording     = false;
var longPressTimer  = null;
var seenMsgIds      = {};
var isLoadingOld    = false;
var noMoreOld       = false;
var isOffline       = false;
var lastDeletedTs   = Math.floor(Date.now() / 1000);
var seenDeletedIds  = {};

// ── Send queue ─────────────────────────────────────────────────────────────
// Each entry: { id, text, replyId, file, retries, rowEl, bubbleEl }
var sendQueue  = [];
var isSending  = false;   // true while a fetch is in-flight for the queue head

// ── DOM refs ───────────────────────────────────────────────────────────────
var shell     = document.getElementById('chv-shell');
var msgBox    = document.getElementById('chv-messages');
var inputEl   = document.getElementById('chv-input');
var sendBtn   = document.getElementById('chv-send-btn');
var voiceBtn  = document.getElementById('chv-voice-btn');
var fileInput = document.getElementById('chv-file-input');
var moreMenu  = document.getElementById('chv-more-menu');

// ═══════════════════════════════════════════════════════════════════════════
// OFFLINE DETECTION
// ═══════════════════════════════════════════════════════════════════════════
function setOnlineState(online) {
  if (isOffline === !online) return;   // no change
  isOffline = !online;
  var banner = document.getElementById('chv-offline-banner');
  if (banner) banner.classList.toggle('show', isOffline);
  if (online && !isSending) drainQueue();
}

window.addEventListener('online',  function() { setOnlineState(true); });
window.addEventListener('offline', function() { setOnlineState(false); });

// ═══════════════════════════════════════════════════════════════════════════
// BOTTOM NAV HIDE / RESTORE
// ═══════════════════════════════════════════════════════════════════════════
var hiddenEls = [];
var savedScrollY = 0;

function hideBottomNav() {
  document.body.classList.add('chv-active');
  savedScrollY = window.scrollY;
  document.body.style.top = '-' + savedScrollY + 'px';

  var all = document.querySelectorAll('*');
  for (var i = 0; i < all.length; i++) {
    var el = all[i];
    if (el === shell || shell.contains(el)) continue;
    try {
      var cs = window.getComputedStyle(el);
      if (cs.position !== 'fixed' && cs.position !== 'sticky') continue;
      var r = el.getBoundingClientRect();
      if (r.bottom >= window.innerHeight - 20 && r.height > 30 && r.height < 200) {
        el.dataset.chvOrigDisplay = el.style.display || '';
        el.style.setProperty('display', 'none', 'important');
        hiddenEls.push(el);
      }
    } catch(e) {}
  }
}

function restoreBottomNav() {
  document.body.classList.remove('chv-active');
  document.body.style.top = '';
  window.scrollTo(0, savedScrollY);

  for (var i = 0; i < hiddenEls.length; i++) {
    var el = hiddenEls[i];
    el.style.removeProperty('display');
    if (el.dataset.chvOrigDisplay) el.style.display = el.dataset.chvOrigDisplay;
    delete el.dataset.chvOrigDisplay;
  }
  hiddenEls = [];
}

// ═══════════════════════════════════════════════════════════════════════════
// INITIAL LOAD
// ═══════════════════════════════════════════════════════════════════════════
function loadMessages(initial) {
  // Guard: skip poll if a previous poll is still in-flight (prevents pileup)
  if (!initial && POLL_INFLIGHT_GUARD) return;

  var url = '/chat/messages/' + OTHER_USER +
            ((!initial && lastMsgTs) ? '?after=' + lastMsgTs : '');

  if (!initial) POLL_INFLIGHT_GUARD = true;

  fetch(url, { credentials: 'same-origin' })
    .then(function(r) {
      if (!r.ok) throw new Error('HTTP ' + r.status);
      return r.text();
    })
    .then(function(html) {
      setOnlineState(true);

      if (initial) {
        if (!html || !html.trim()) {
          msgBox.innerHTML = '';
        } else {
          msgBox.innerHTML = html;
        }
        var allRows = msgBox.querySelectorAll('.chv-msg-row[data-msg-id]');
        for (var k = 0; k < allRows.length; k++) {
          var mid = parseInt(allRows[k].getAttribute('data-msg-id'), 10);
          seenMsgIds[mid] = true;
          if (!oldestMsgId || mid < oldestMsgId) oldestMsgId = mid;
        }
        updateLastTs();
        initWaveforms();
        injectDeleteButtons();
        checkPermState();
        var imgs = msgBox.querySelectorAll('img');
        if (!imgs.length) { scrollToBottom(); return; }
        var loaded = 0;
        function onImgDone() { loaded++; if (loaded >= imgs.length) scrollToBottom(); }
        for (var ii = 0; ii < imgs.length; ii++) {
          if (imgs[ii].complete) { onImgDone(); }
          else { imgs[ii].addEventListener('load', onImgDone); imgs[ii].addEventListener('error', onImgDone); }
        }
        scrollToBottom();
      } else {
        if (!html || !html.trim()) return;
        var tmp = document.createElement('div');
        tmp.innerHTML = html;
        var rows = tmp.querySelectorAll('.chv-msg-row');
        if (!rows.length) return;

        var atBot = isAtBottom();
        var added = 0;
        for (var i = 0; i < rows.length; i++) {
          var row = rows[i];
          var mid2 = parseInt(row.getAttribute('data-msg-id'), 10);
          if (mid2 && seenMsgIds[mid2]) continue;
          if (mid2) seenMsgIds[mid2] = true;
          msgBox.appendChild(row);
          added++;

          var actionEl = row.querySelector('[data-chv-action]');
          if (actionEl) {
            var action = actionEl.getAttribute('data-chv-action');
            if (action === 'img-permission-granted') onImgPermissionGranted();
            if (action === 'img-permission-denied')  onImgPermissionDenied();
          }
        }
        if (!added) return;

        initWaveforms();
        injectDeleteButtons();
        updateLastTs();

        if (atBot || !isPaused) {
          scrollToBottom();
          newMsgCount = 0;
        } else {
          newMsgCount += added;
        }
        updateScrollFab();
      }
    })
    .catch(function(e) {
      console.warn('Poll error:', e);
      setOnlineState(false);
    })
    .finally(function() {
      if (!initial) POLL_INFLIGHT_GUARD = false;
    });
}

// ═══════════════════════════════════════════════════════════════════════════
// LAZY LOAD OLDER MESSAGES
// ═══════════════════════════════════════════════════════════════════════════
function loadOlderMessages() {
  if (isLoadingOld || noMoreOld || !oldestMsgId) return;
  isLoadingOld = true;

  var spinner = document.createElement('div');
  spinner.className = 'chv-load-more-spinner';
  spinner.id = 'chv-old-spinner';
  spinner.innerHTML = '<div class="chv-spinner"></div>';
  msgBox.insertBefore(spinner, msgBox.firstChild);

  var scrollHeightBefore = msgBox.scrollHeight;
  var scrollTopBefore    = msgBox.scrollTop;

  fetch('/chat/messages/' + OTHER_USER + '?before_id=' + oldestMsgId, { credentials: 'same-origin' })
    .then(function(r) {
      if (!r.ok) throw new Error('HTTP ' + r.status);
      return r.text();
    })
    .then(function(html) {
      var sp = document.getElementById('chv-old-spinner');
      if (sp) sp.remove();

      if (!html || !html.trim()) {
        noMoreOld = true;
        isLoadingOld = false;
        return;
      }

      var tmp = document.createElement('div');
      tmp.innerHTML = html;
      var rows = tmp.querySelectorAll('.chv-msg-row');
      if (!rows.length) {
        noMoreOld = true;
        isLoadingOld = false;
        return;
      }

      var frag = document.createDocumentFragment();
      var addedCount = 0;
      for (var i = 0; i < rows.length; i++) {
        var row = rows[i];
        var mid = parseInt(row.getAttribute('data-msg-id'), 10);
        if (mid && seenMsgIds[mid]) continue;
        if (mid) {
          seenMsgIds[mid] = true;
          if (!oldestMsgId || mid < oldestMsgId) oldestMsgId = mid;
        }
        frag.appendChild(row);
        addedCount++;
      }

      if (!addedCount) {
        noMoreOld = true;
        isLoadingOld = false;
        return;
      }

      var firstRow = msgBox.querySelector('.chv-msg-row');
      if (firstRow) {
        msgBox.insertBefore(frag, firstRow);
      } else {
        msgBox.appendChild(frag);
      }

      initWaveforms();
      injectDeleteButtons();

      var scrollHeightAfter = msgBox.scrollHeight;
      msgBox.scrollTop = scrollTopBefore + (scrollHeightAfter - scrollHeightBefore);

      isLoadingOld = false;
    })
    .catch(function(e) {
      console.warn('Load older error:', e);
      var sp = document.getElementById('chv-old-spinner');
      if (sp) sp.remove();
      isLoadingOld = false;
    });
}

// ═══════════════════════════════════════════════════════════════════════════
// SCROLL HELPERS
// ═══════════════════════════════════════════════════════════════════════════
function updateLastTs() {
  var rows = msgBox.querySelectorAll('.chv-msg-row[data-ts]');
  for (var i = 0; i < rows.length; i++) {
    var ts = parseInt(rows[i].getAttribute('data-ts') || '0', 10);
    if (ts > lastMsgTs) lastMsgTs = ts;
  }
}

function isAtBottom() {
  return msgBox.scrollHeight - msgBox.scrollTop - msgBox.clientHeight < 80;
}

function isNearTop() {
  return msgBox.scrollTop < 120;
}

function scrollToBottom() {
  msgBox.scrollTop = msgBox.scrollHeight;
}

function onScroll() {
  if (isNearTop()) {
    loadOlderMessages();
  }

  if (isAtBottom()) {
    isPaused = false;
    newMsgCount = 0;
    document.getElementById('chv-paused').classList.remove('show');
    updateScrollFab();
  } else {
    isPaused = true;
    document.getElementById('chv-paused').classList.add('show');
    document.getElementById('chv-scroll-fab').classList.add('show');
  }
}

function updateScrollFab() {
  var fab = document.getElementById('chv-scroll-fab');
  if (newMsgCount > 0 || isPaused) {
    fab.classList.add('show');
  } else {
    fab.classList.remove('show');
  }
}

// ═══════════════════════════════════════════════════════════════════════════
// OPTIMISTIC SEND BUBBLE
// Creates a temporary bubble in the UI immediately so the user sees
// their message before the server confirms it.
// ═══════════════════════════════════════════════════════════════════════════
function createOptimisticBubble(text, hasFile, pendingId) {
  var now = new Date();
  var timeStr = now.getHours() + ':' + ('0' + now.getMinutes()).slice(-2);

  var row = document.createElement('div');
  row.className = 'chv-msg-row me';
  row.setAttribute('data-pending-id', pendingId);

  var preview = hasFile
    ? '<em style="color:var(--text-meta);font-size:13px">📷 Sending image…</em>'
    : escapeHtml(text);

  row.innerHTML =
    '<div class="chv-bubble-wrap">' +
      '<div class="chv-bubble chv-bubble-sending">' +
        preview +
        '<div class="chv-bubble-meta">' +
          '<span class="chv-bubble-time">' + timeStr + '</span>' +
          '<span class="chv-ticks sent" title="Sending">' +
            '<svg viewBox="0 0 18 18" fill="none"><path d="M2 9l5 5 9-10" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>' +
          '</span>' +
        '</div>' +
      '</div>' +
    '</div>';

  msgBox.appendChild(row);
  scrollToBottom();
  return row;
}

function escapeHtml(s) {
  return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;')
          .replace(/"/g,'&quot;').replace(/'/g,'&#39;');
}

// Replace optimistic bubble with real server HTML (or mark it failed)
function replaceOptimisticBubble(pendingId, realHtml) {
  var row = msgBox.querySelector('[data-pending-id="' + pendingId + '"]');
  if (!row) return;

  if (!realHtml) {
    // Server returned empty — keep the optimistic bubble, remove sending style
    var bubble = row.querySelector('.chv-bubble');
    if (bubble) bubble.classList.remove('chv-bubble-sending');
    return;
  }

  var tmp = document.createElement('div');
  tmp.innerHTML = realHtml;
  var newRows = tmp.querySelectorAll('.chv-msg-row');
  if (newRows.length) {
    for (var i = 0; i < newRows.length; i++) {
      var mid = parseInt(newRows[i].getAttribute('data-msg-id'), 10);
      if (mid) seenMsgIds[mid] = true;
      row.parentNode.insertBefore(newRows[i], row);
    }
    row.remove();
    initWaveforms();
    injectDeleteButtons();
    updateLastTs();
  }
}

function markBubbleFailed(pendingId) {
  var row = msgBox.querySelector('[data-pending-id="' + pendingId + '"]');
  if (!row) return;
  var bubble = row.querySelector('.chv-bubble');
  if (!bubble) return;
  bubble.classList.remove('chv-bubble-sending');
  bubble.classList.add('chv-bubble-error');

  // Inject a retry button below the bubble text
  if (!bubble.querySelector('.chv-retry-btn')) {
    var retryBtn = document.createElement('button');
    retryBtn.type = 'button';
    retryBtn.className = 'chv-retry-btn';
    retryBtn.innerHTML = '↺ Tap to retry';
    retryBtn.setAttribute('data-retry-pending', pendingId);
    bubble.appendChild(retryBtn);
  }
}

// ═══════════════════════════════════════════════════════════════════════════
// SEND QUEUE — enqueue + drain
// ═══════════════════════════════════════════════════════════════════════════
var _pendingSeq = 0;

function enqueueSend(text, replyId, file) {
  var pendingId = 'p' + (++_pendingSeq);
  var rowEl = createOptimisticBubble(text, !!file, pendingId);

  sendQueue.push({
    id:      pendingId,
    text:    text,
    replyId: replyId,
    file:    file || null,
    retries: 0,
    rowEl:   rowEl
  });

  drainQueue();
}

function drainQueue() {
  // Nothing to do, or already sending, or offline
  if (!sendQueue.length || isSending || isOffline) return;

  var item = sendQueue[0];
  isSending = true;
  sendBtn.disabled = true;

  attemptSend(item, function onSuccess(html) {
    sendQueue.shift();
    isSending = false;
    sendBtn.disabled = false;
    replaceOptimisticBubble(item.id, html);
    drainQueue();           // send next queued message immediately
  }, function onFailure() {
    isSending = false;
    sendBtn.disabled = false;

    if (item.retries < SEND_MAX_RETRIES) {
      // Exponential backoff: 800ms, 1.6s, 3.2s, 6.4s, 12s
      var delay = Math.min(SEND_BASE_DELAY_MS * Math.pow(2, item.retries), SEND_MAX_DELAY_MS);
      item.retries++;
      console.warn('[send] retry ' + item.retries + '/' + SEND_MAX_RETRIES + ' in ' + delay + 'ms');
      setTimeout(function() {
        if (!isOffline) drainQueue();
      }, delay);
    } else {
      // Give up — remove from queue and show error on bubble
      sendQueue.shift();
      markBubbleFailed(item.id);
      drainQueue();   // keep draining for subsequent messages
    }
  });
}

function attemptSend(item, onSuccess, onFailure) {
  var fd = new FormData();
  fd.append('content', item.text);
  if (item.replyId) fd.append('reply_to_id', item.replyId);
  if (item.file)    fd.append('image', item.file);

  // 30 s timeout via AbortController (fetch has no built-in timeout)
  var controller = typeof AbortController !== 'undefined' ? new AbortController() : null;
  var timeoutId = controller
    ? setTimeout(function() { controller.abort(); }, 30000)
    : null;

  var opts = { method: 'POST', credentials: 'same-origin', body: fd };
  if (controller) opts.signal = controller.signal;

  fetch('/chat/send/' + OTHER_USER, opts)
    .then(function(r) {
      if (timeoutId) clearTimeout(timeoutId);
      // 4xx are unrecoverable (bad request, auth) — don't retry
      if (r.status >= 400 && r.status < 500) {
        console.error('[send] unrecoverable HTTP ' + r.status);
        return r.text().then(function() { onFailure(); });
      }
      if (!r.ok) throw new Error('HTTP ' + r.status);
      return r.text();
    })
    .then(function(html) {
      if (html === undefined) return;   // unrecoverable path already handled
      setOnlineState(true);
      onSuccess(html || '');
    })
    .catch(function(e) {
      if (timeoutId) clearTimeout(timeoutId);
      console.warn('[send] fetch error:', e);
      setOnlineState(false);
      onFailure();
    });
}

// ═══════════════════════════════════════════════════════════════════════════
// PUBLIC sendMessage — called by button / Enter key
// ═══════════════════════════════════════════════════════════════════════════
function sendMessage() {
  var text    = inputEl.value.trim();
  var hasFile = fileInput.files && fileInput.files.length > 0;
  if (!text && !hasFile) return;

  // Snapshot file & reply state before clearing UI
  var file    = hasFile ? fileInput.files[0] : null;
  var replyId = replyToId;

  // Clear input immediately for snappy UX
  inputEl.value = '';
  autoResize(inputEl);
  clearFile();
  clearReply();
  updateSendBtn();
  isPaused = false;
  scrollToBottom();

  enqueueSend(text, replyId, file);
}

// Retry a failed message when user taps the retry button
function retryFailedMessage(pendingId) {
  // Find the queued item (was shifted out on failure, re-add it)
  var row = msgBox.querySelector('[data-pending-id="' + pendingId + '"]');
  if (!row) return;

  // Extract text from the bubble (best-effort; images can't be re-read)
  var bubble = row.querySelector('.chv-bubble');
  if (!bubble) return;
  // Remove the retry button before reading text
  var retryBtn = bubble.querySelector('.chv-retry-btn');
  var retryText = retryBtn ? retryBtn.innerHTML : '';
  if (retryBtn) retryBtn.remove();

  var text = bubble.textContent.replace(retryText, '').trim();
  bubble.classList.remove('chv-bubble-error');
  bubble.classList.add('chv-bubble-sending');

  sendQueue.unshift({
    id:      pendingId,
    text:    text,
    replyId: null,
    file:    null,
    retries: 0,
    rowEl:   row
  });
  drainQueue();
}

// ═══════════════════════════════════════════════════════════════════════════
// INPUT
// ═══════════════════════════════════════════════════════════════════════════
function autoResize(el) {
  el.style.height = 'auto';
  el.style.height = Math.min(el.scrollHeight, 100) + 'px';
}

function updateSendBtn() {
  var hasText = inputEl.value.trim().length > 0;
  var hasFile = fileInput.files && fileInput.files.length > 0;
  if (hasText || hasFile) {
    sendBtn.style.display = 'flex';
    voiceBtn.style.display = 'none';
  } else {
    sendBtn.style.display = 'none';
    voiceBtn.style.display = 'flex';
  }
}

// ═══════════════════════════════════════════════════════════════════════════
// FILE / IMAGE
// ═══════════════════════════════════════════════════════════════════════════
function onFileSelect() {
  if (fileInput.files && fileInput.files.length > 0) {
    document.getElementById('chv-file-name').textContent = fileInput.files[0].name;
    document.getElementById('chv-file-indicator').classList.add('show');
    updateSendBtn();
  }
}

function clearFile() {
  fileInput.value = '';
  document.getElementById('chv-file-indicator').classList.remove('show');
  updateSendBtn();
}

// ═══════════════════════════════════════════════════════════════════════════
// IMAGE PERMISSION
// ═══════════════════════════════════════════════════════════════════════════
function requestImagePermission() {
  if (imgPermGranted) { fileInput.click(); return; }
  if (imgPermPending) {
    var banner = document.getElementById('chv-img-perm-banner');
    document.getElementById('chv-img-perm-text').textContent = 'Permission request already sent. Waiting for approval…';
    banner.classList.add('show');
    setTimeout(function() { banner.classList.remove('show'); }, 3000);
    return;
  }

  imgPermPending = true;
  var imgBtn = document.getElementById('chv-img-btn');
  imgBtn.disabled = true;
  imgBtn.style.opacity = '0.4';

  enqueueSend('__IMG_PERMISSION_REQUEST__', null, null);
  var banner2 = document.getElementById('chv-img-perm-banner');
  document.getElementById('chv-img-perm-text').textContent = 'Image permission requested. Waiting for approval…';
  banner2.classList.add('show');
  setTimeout(function() { banner2.classList.remove('show'); }, 5000);
}

function approveImagePermission(msgId, requesterUser) {
  fetch('/chat/img-permission/' + (requesterUser || OTHER_USER) + '/approve/' + msgId, {
    method: 'POST', credentials: 'same-origin'
  }).then(function(r) {
    if (r.ok) {
      var row = msgBox.querySelector('[data-msg-id="' + msgId + '"]');
      if (row) row.remove();
    }
  });
}

function denyImagePermission(msgId, requesterUser) {
  fetch('/chat/img-permission/' + (requesterUser || OTHER_USER) + '/deny/' + msgId, {
    method: 'POST', credentials: 'same-origin'
  }).then(function(r) {
    if (r.ok) {
      var row = msgBox.querySelector('[data-msg-id="' + msgId + '"]');
      if (row) row.remove();
    }
  });
  imgPermPending = false;
  var imgBtn = document.getElementById('chv-img-btn');
  imgBtn.disabled = false;
  imgBtn.style.opacity = '';
}

function onImgPermissionGranted() {
  imgPermGranted = true;
  imgPermPending = false;
  var imgBtn = document.getElementById('chv-img-btn');
  imgBtn.disabled = false;
  imgBtn.style.opacity = '';
  var banner = document.getElementById('chv-img-perm-banner');
  document.getElementById('chv-img-perm-text').textContent = '✓ Image permission granted! Tap 📷 to send.';
  banner.classList.add('show');
  setTimeout(function() { banner.classList.remove('show'); }, 4000);
}

function onImgPermissionDenied() {
  imgPermPending = false;
  var imgBtn = document.getElementById('chv-img-btn');
  imgBtn.disabled = false;
  imgBtn.style.opacity = '';
  var banner = document.getElementById('chv-img-perm-banner');
  document.getElementById('chv-img-perm-text').textContent = '✗ Image permission denied.';
  banner.classList.add('show');
  setTimeout(function() { banner.classList.remove('show'); }, 4000);
}

// ═══════════════════════════════════════════════════════════════════════════
// CONTEXT MENU
// ═══════════════════════════════════════════════════════════════════════════
function openCtxMenu(msgId, author, text, x, y) {
  ctxMsgId = msgId; ctxMsgAuthor = author; ctxMsgText = text;
  var menu = document.getElementById('chv-ctx-menu');
  var overlay = document.getElementById('chv-ctx-overlay');
  document.getElementById('ctx-copy-btn').style.display = text ? 'flex' : 'none';
  menu.classList.add('open');
  overlay.classList.add('open');
  menu.style.left = '0'; menu.style.top = '0';
  var mw = menu.offsetWidth || 180;
  var mh = menu.offsetHeight || 130;
  menu.style.left = Math.max(8, Math.min(x, window.innerWidth - mw - 8)) + 'px';
  menu.style.top  = Math.max(8, Math.min(y, window.innerHeight - mh - 8)) + 'px';
}
function closeCtxMenu() {
  document.getElementById('chv-ctx-menu').classList.remove('open');
  document.getElementById('chv-ctx-overlay').classList.remove('open');
}

// ═══════════════════════════════════════════════════════════════════════════
// LONG PRESS
// ═══════════════════════════════════════════════════════════════════════════
function startLongPress(el, msgId, author, text) {
  longPressTimer = setTimeout(function() {
    var r = el.getBoundingClientRect();
    openCtxMenu(msgId, author, text, r.left + r.width / 2, r.top + 10);
  }, 500);
}
function cancelLongPress() {
  if (longPressTimer) { clearTimeout(longPressTimer); longPressTimer = null; }
}

// ═══════════════════════════════════════════════════════════════════════════
// REPLY
// ═══════════════════════════════════════════════════════════════════════════
function setReply(id, author, text) {
  replyToId = id;
  document.getElementById('chv-reply-id').value = id;
  document.getElementById('chv-reply-author').textContent = author;
  document.getElementById('chv-reply-text').textContent = text || '📷 Image';
  document.getElementById('chv-reply-bar').classList.add('active');
  inputEl.focus();
}
function clearReply() {
  replyToId = null;
  document.getElementById('chv-reply-id').value = '';
  document.getElementById('chv-reply-bar').classList.remove('active');
}
function jumpToMsg(id) {
  var el = msgBox.querySelector('[data-msg-id="' + id + '"]');
  if (!el) return;
  el.scrollIntoView({ behavior: 'smooth', block: 'center' });
  var origBg = el.style.background;
  el.style.background = 'rgba(18,140,126,0.18)';
  setTimeout(function() { el.style.background = origBg; }, 1500);
}

// ═══════════════════════════════════════════════════════════════════════════
// EMOJI PANEL
// ═══════════════════════════════════════════════════════════════════════════
function openEmojiPanel() {
  document.getElementById('chv-emoji-panel').classList.add('open');
  document.getElementById('chv-emoji-overlay').classList.add('open');
}
function closeEmojiPanel() {
  document.getElementById('chv-emoji-panel').classList.remove('open');
  document.getElementById('chv-emoji-overlay').classList.remove('open');
}
function sendStatusEmoji(emoji, status) {
  closeEmojiPanel();
  inputEl.value = CURRENT_USER + ' is ' + status + ' ' + emoji;
  updateSendBtn();
  inputEl.focus();
}
function insertEmoji(emoji) {
  var start = inputEl.selectionStart;
  var end   = inputEl.selectionEnd;
  inputEl.value = inputEl.value.substring(0, start) + emoji + inputEl.value.substring(end);
  inputEl.selectionStart = inputEl.selectionEnd = start + emoji.length;
  autoResize(inputEl);
  updateSendBtn();
  inputEl.focus();
  closeEmojiPanel();
}

// ═══════════════════════════════════════════════════════════════════════════
// MORE MENU
// ═══════════════════════════════════════════════════════════════════════════
function openMoreMenu() { moreMenu.classList.add('open'); }
function closeMoreMenu() { moreMenu.classList.remove('open'); }

// ═══════════════════════════════════════════════════════════════════════════
// DELETE FULL CONVERSATION
// ═══════════════════════════════════════════════════════════════════════════
function deleteConversation() {
  closeMoreMenu();

  if (!confirm('Clear this conversation with @' + OTHER_USER + ' from your view?\n\nThis only affects what you see — nothing is deleted for @' + OTHER_USER + '.')) return;

  fetch('/chat/conversation/' + OTHER_USER, {
    method: 'DELETE',
    credentials: 'same-origin'
  })
  .then(function(r) {
    if (!r.ok) throw new Error('HTTP ' + r.status);

    msgBox.innerHTML =
      '<div style="display:flex;flex-direction:column;align-items:center;justify-content:center;' +
      'height:100%%;padding:40px 20px;gap:12px;">' +
      '<svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="#ccc" stroke-width="1.5">' +
      '<path stroke-linecap="round" stroke-linejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862' +
      'a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"/></svg>' +
      '<p style="font-size:14px;color:#aaa;margin:0;text-align:center;">Your messages deleted</p>' +
      '</div>';

    clearInterval(pollTimer);
    pollTimer   = null;
    lastMsgTs   = 0;
    oldestMsgId = 0;
    seenMsgIds  = {};
    noMoreOld   = true;
    sendQueue   = [];          // discard any pending outgoing messages
  })
  .catch(function(e) {
    alert('Could not delete conversation. Please try again.');
    console.error('[deleteConversation]', e);
  });
}

// ═══════════════════════════════════════════════════════════════════════════
// VOICE
// ═══════════════════════════════════════════════════════════════════════════
function toggleVoice() {
  if (!isRecording) startRecording(); else stopRecording();
}

function startRecording() {
  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    alert('Voice messages are not supported on this browser.'); return;
  }
  navigator.mediaDevices.getUserMedia({ audio: true })
    .then(function(stream) {
      var mimeType = 'audio/webm;codecs=opus';
      if (!MediaRecorder.isTypeSupported(mimeType)) mimeType = 'audio/webm';
      if (!MediaRecorder.isTypeSupported(mimeType)) mimeType = 'audio/mp4';
      if (!MediaRecorder.isTypeSupported(mimeType)) mimeType = '';
      mediaRecorder = mimeType ? new MediaRecorder(stream, {mimeType: mimeType}) : new MediaRecorder(stream);
      audioChunks = [];
      mediaRecorder.ondataavailable = function(e) { if (e.data.size > 0) audioChunks.push(e.data); };
      mediaRecorder.onstop = sendVoiceMessage;
      mediaRecorder.start();
      isRecording = true;
      recSeconds = 0;
      voiceBtn.classList.add('recording');
      document.getElementById('chv-mic-icon').innerHTML = '<rect x="4" y="4" width="16" height="16" rx="2" fill="currentColor"/>';
      document.getElementById('chv-recording-row').classList.add('active');
      document.getElementById('chv-input-wrap').style.display = 'none';
      document.getElementById('chv-img-btn').style.display = 'none';
      recTimerInt = setInterval(function() {
        recSeconds++;
        var m = Math.floor(recSeconds / 60);
        var s = recSeconds %% 60;
        document.getElementById('chv-rec-timer').textContent = m + ':' + (s < 10 ? '0' : '') + s;
      }, 1000);
    })
    .catch(function() { alert('Microphone access denied.'); });
}

function stopRecording() {
  if (mediaRecorder && isRecording) {
    mediaRecorder.stop();
    mediaRecorder.stream.getTracks().forEach(function(t) { t.stop(); });
    isRecording = false;
    clearInterval(recTimerInt);
    resetVoiceUI();
  }
}

function cancelRecording() {
  if (mediaRecorder && isRecording) {
    mediaRecorder.ondataavailable = null;
    mediaRecorder.onstop = null;
    mediaRecorder.stop();
    mediaRecorder.stream.getTracks().forEach(function(t) { t.stop(); });
    isRecording = false;
    clearInterval(recTimerInt);
    audioChunks = [];
    resetVoiceUI();
  }
}

function resetVoiceUI() {
  voiceBtn.classList.remove('recording');
  document.getElementById('chv-mic-icon').innerHTML = '<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 11a7 7 0 01-7 7m0 0a7 7 0 01-7-7m7 7v4m0 0H8m4 0h4m-4-8a3 3 0 01-3-3V5a3 3 0 116 0v6a3 3 0 01-3 3z"/>';
  document.getElementById('chv-recording-row').classList.remove('active');
  document.getElementById('chv-input-wrap').style.display = '';
  document.getElementById('chv-img-btn').style.display = '';
  updateSendBtn();
}

function sendVoiceMessage() {
  if (!audioChunks.length) return;
  var mimeUsed = (mediaRecorder && mediaRecorder.mimeType) ? mediaRecorder.mimeType : 'audio/webm';
  var blob = new Blob(audioChunks, { type: mimeUsed });
  var ext = mimeUsed.indexOf('mp4') !== -1 ? '.mp4' : '.webm';
  var fd = new FormData();
  fd.append('voice', blob, 'voice_' + Date.now() + ext);
  fd.append('content', '');

  // Voice uses the same retry helper (3 retries, no optimistic bubble for now)
  var retries = 0;
  function attempt() {
    fetch('/chat/send-voice/' + OTHER_USER, {
      method: 'POST', credentials: 'same-origin', body: fd
    }).catch(function(e) {
      console.error('Voice send error:', e);
      if (retries < 3) { retries++; setTimeout(attempt, 2000 * retries); }
    });
  }
  attempt();
}

// ═══════════════════════════════════════════════════════════════════════════
// WAVEFORM
// ═══════════════════════════════════════════════════════════════════════════
var WV_HEIGHTS = [3,6,10,14,18,22,18,12,8,16,24,18,10,6,14,20,16,10,7,13,20,17,11,8,15,22,16,9];

function makeWaveform(container) {
  if (container.dataset.wvDone) return;
  container.dataset.wvDone = '1';
  for (var i = 0; i < 28; i++) {
    var b = document.createElement('div');
    b.className = 'chv-voice-bar';
    b.style.height = WV_HEIGHTS[i %% WV_HEIGHTS.length] + 'px';
    container.appendChild(b);
  }
}

function initWaveforms() {
  var wvs = msgBox.querySelectorAll('.chv-voice-waveform');
  for (var i = 0; i < wvs.length; i++) makeWaveform(wvs[i]);
}

// ═══════════════════════════════════════════════════════════════════════════
// PER-BUBBLE DELETE BUTTON
// ═══════════════════════════════════════════════════════════════════════════
function injectDeleteButtons() {
  var rows = msgBox.querySelectorAll('.chv-msg-row.me[data-msg-id]');
  for (var i = 0; i < rows.length; i++) {
    var row = rows[i];
    if (row.querySelector('.chv-del-btn')) continue;

    var msgId = row.getAttribute('data-msg-id');
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'chv-del-btn';
    btn.title = 'Delete message';
    btn.setAttribute('data-del-id', msgId);
    btn.innerHTML =
      '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">' +
      '<path stroke-linecap="round" stroke-linejoin="round" ' +
      'd="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7' +
      'm5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"/>' +
      '</svg>';

    row.appendChild(btn);
  }
}

function wireBubbleTapReveal() {
  msgBox.addEventListener('click', function(e) {
    // Handle retry button clicks
    var retryBtn = e.target.closest('.chv-retry-btn');
    if (retryBtn) {
      e.stopPropagation();
      var pendingId = retryBtn.getAttribute('data-retry-pending');
      if (pendingId) retryFailedMessage(pendingId);
      return;
    }

    var btn = e.target.closest('.chv-del-btn');
    if (btn) {
      e.stopPropagation();
      var id = btn.getAttribute('data-del-id');
      if (id) deleteMessage(parseInt(id, 10));
      return;
    }

    var row = e.target.closest('.chv-msg-row.me');
    if (!row) {
      var open = msgBox.querySelectorAll('.chv-msg-row.chv-show-del');
      for (var i = 0; i < open.length; i++) open[i].classList.remove('chv-show-del');
      return;
    }

    var isShown = row.classList.contains('chv-show-del');
    var open2 = msgBox.querySelectorAll('.chv-msg-row.chv-show-del');
    for (var j = 0; j < open2.length; j++) open2[j].classList.remove('chv-show-del');
    if (!isShown) row.classList.add('chv-show-del');
  });
}

function playVoice(btn, audioSrc, waveEl) {
  var audio = btn._audio;
  if (!audio) { audio = new Audio(audioSrc); btn._audio = audio; }
  if (!audio.paused) {
    audio.pause();
    btn.querySelector('svg').innerHTML = '<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/>';
    return;
  }
  var bars = waveEl ? waveEl.querySelectorAll('.chv-voice-bar') : [];
  btn.querySelector('svg').innerHTML = '<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 9v6m4-6v6"/>';
  audio.play().catch(function() {});
  var upd = setInterval(function() {
    if (!audio.duration) return;
    var prog = audio.currentTime / audio.duration;
    var active = Math.floor(prog * bars.length);
    for (var i = 0; i < bars.length; i++) bars[i].classList.toggle('active', i <= active);
  }, 100);
  audio.onended = function() {
    clearInterval(upd);
    for (var i = 0; i < bars.length; i++) bars[i].classList.remove('active');
    btn.querySelector('svg').innerHTML = '<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/>';
  };
}

// ═══════════════════════════════════════════════════════════════════════════
// LIGHTBOX
// ═══════════════════════════════════════════════════════════════════════════
function openLightbox(src) {
  document.getElementById('chv-lightbox-img').src = src;
  document.getElementById('chv-lightbox').classList.add('open');
}
function closeLightbox() { document.getElementById('chv-lightbox').classList.remove('open'); }

// ═══════════════════════════════════════════════════════════════════════════
// DELETE SINGLE MESSAGE
// ═══════════════════════════════════════════════════════════════════════════
function deleteMessage(msgId) {
  var row = msgBox.querySelector('[data-msg-id="' + msgId + '"]');
  var isMine = row && row.classList.contains('me');
  var promptText = isMine
    ? 'Delete this message for everyone?'
    : 'Delete this message for you? The other person can still see it.';
  if (!confirm(promptText)) return;

  fetch('/chat/delete/' + OTHER_USER + '/' + msgId, {
    method: 'DELETE', credentials: 'same-origin'
  }).then(function(r) {
    if (!r.ok) return;
    var row2 = msgBox.querySelector('[data-msg-id="' + msgId + '"]');
    if (row2) row2.remove();
  });
}

// ═══════════════════════════════════════════════════════════════════════════
// TICKS / READ RECEIPTS
// ═══════════════════════════════════════════════════════════════════════════
function refreshTicks() {
  fetch('/chat/ticks/' + OTHER_USER, { credentials: 'same-origin' })
    .then(function(r) { return r.json(); })
    .then(function(data) {
      if (!data || !data.read_ids) return;
      for (var i = 0; i < data.read_ids.length; i++) {
        var mid = data.read_ids[i];
        var row = msgBox.querySelector('.chv-msg-row.me[data-msg-id="' + mid + '"]');
        if (!row) continue;
        var ticks = row.querySelector('.chv-ticks');
        if (ticks && ticks.classList.contains('sent')) {
          ticks.classList.remove('sent');
          ticks.classList.add('read');
          ticks.title = 'Seen';
          ticks.innerHTML = '<svg viewBox="0 0 18 18" fill="none"><path d="M1 9l4 4L12 4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/><path d="M5 9l4 4 7-9" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>';
        }
        var avatar = row.querySelector('.chv-msg-seen-avatar');
        if (avatar && OTHER_AVATAR) { avatar.src = OTHER_AVATAR; avatar.classList.add('visible'); }
      }
    })
    .catch(function() {});
}

function refreshDeleted() {
  fetch('/chat/deleted/' + OTHER_USER + '?since=' + lastDeletedTs, { credentials: 'same-origin' })
    .then(function(r) { return r.json(); })
    .then(function(data) {
      if (!data || !data.deleted_ids) return;
      for (var i = 0; i < data.deleted_ids.length; i++) {
        var id = data.deleted_ids[i];
        if (seenDeletedIds[id]) continue;
        seenDeletedIds[id] = true;
        var row = msgBox.querySelector('[data-msg-id="' + id + '"]');
        if (row) row.remove();
      }
      lastDeletedTs = Math.floor(Date.now() / 1000);
    })
    .catch(function() {});
}

// ═══════════════════════════════════════════════════════════════════════════
// HTMX online-dot hook
// ═══════════════════════════════════════════════════════════════════════════
document.addEventListener('htmx:afterSwap', function(e) {
  if (e.detail && e.detail.target && e.detail.target.id === 'chv-header-status') {
    var txt = e.detail.target.textContent.toLowerCase();
    var dot = document.getElementById('chv-online-dot');
    if (dot) dot.classList.toggle('visible', txt.indexOf('online') !== -1 || txt.indexOf('active') !== -1);
  }
});

// ═══════════════════════════════════════════════════════════════════════════
// EVENT WIRING
// ═══════════════════════════════════════════════════════════════════════════
function wireEvents() {
  // Send button — no _guardTap() needed; isSending prevents double-submission
  sendBtn.addEventListener('click', function(e) {
    e.preventDefault();
    sendMessage();
  });

  inputEl.addEventListener('input', function() { autoResize(this); updateSendBtn(); });
  inputEl.addEventListener('keydown', function(e) {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); sendMessage(); }
  });

  voiceBtn.addEventListener('click', function() { toggleVoice(); });

  document.getElementById('chv-emoji-header-btn').addEventListener('click', function(e) {
    e.stopPropagation();
    closeMoreMenu();
    openEmojiPanel();
  });

  document.getElementById('chv-more-btn').addEventListener('click', function(e) {
    e.stopPropagation();
    if (moreMenu.classList.contains('open')) { closeMoreMenu(); } else { openMoreMenu(); }
  });

  document.getElementById('chv-delete-convo-btn').addEventListener('click', function(e) {
    e.stopPropagation();
    deleteConversation();
  });

  document.addEventListener('click', function(e) {
    if (moreMenu.classList.contains('open') && !moreMenu.contains(e.target)) closeMoreMenu();
  });

  document.getElementById('chv-emoji-btn').addEventListener('click', function(e) { e.stopPropagation(); openEmojiPanel(); });
  document.getElementById('chv-emoji-overlay').addEventListener('click', function(e) { if (e.target === this) closeEmojiPanel(); });
  document.getElementById('chv-emoji-close').addEventListener('click', function(e) { e.stopPropagation(); closeEmojiPanel(); });

  var statusBtns = document.querySelectorAll('.chv-emoji-status-btn');
  for (var i = 0; i < statusBtns.length; i++) {
    (function(btn) {
      btn.addEventListener('click', function(e) {
        e.stopPropagation();
        sendStatusEmoji(btn.getAttribute('data-emoji'), btn.getAttribute('data-status'));
      });
    })(statusBtns[i]);
  }

  var quickBtns = document.querySelectorAll('.chv-emoji-quick-btn');
  for (var j = 0; j < quickBtns.length; j++) {
    (function(btn) {
      btn.addEventListener('click', function(e) { e.stopPropagation(); insertEmoji(btn.getAttribute('data-insert')); });
    })(quickBtns[j]);
  }

  document.getElementById('chv-img-btn').addEventListener('click', function() { requestImagePermission(); });
  fileInput.addEventListener('change', onFileSelect);
  document.getElementById('chv-file-clear').addEventListener('click', clearFile);
  document.getElementById('chv-reply-close').addEventListener('click', clearReply);

  document.getElementById('chv-scroll-fab').addEventListener('click', function() {
    scrollToBottom(); isPaused = false; newMsgCount = 0; updateScrollFab();
  });

  msgBox.addEventListener('scroll', onScroll);
  wireBubbleTapReveal();

  document.getElementById('chv-ctx-overlay').addEventListener('click', closeCtxMenu);
  document.getElementById('ctx-reply-btn').addEventListener('click', function() { closeCtxMenu(); setReply(ctxMsgId, ctxMsgAuthor, ctxMsgText); });
  document.getElementById('ctx-copy-btn').addEventListener('click', function() {
    closeCtxMenu();
    if (ctxMsgText && navigator.clipboard) navigator.clipboard.writeText(ctxMsgText).catch(function() {});
  });
  document.getElementById('ctx-delete-btn').addEventListener('click', function() { closeCtxMenu(); deleteMessage(ctxMsgId); });

  document.getElementById('chv-cancel-rec').addEventListener('click', cancelRecording);

  document.getElementById('chv-back-btn').addEventListener('click', restoreBottomNav);
  document.getElementById('chv-lightbox-close').addEventListener('click', closeLightbox);
  document.getElementById('chv-lightbox').addEventListener('click', function(e) { if (e.target === this) closeLightbox(); });

  window.addEventListener('popstate', restoreBottomNav);
  window.addEventListener('beforeunload', restoreBottomNav);
  document.addEventListener('htmx:beforeNavigate', restoreBottomNav);
  document.addEventListener('htmx:beforeHistoryUpdate', restoreBottomNav);
}

// ═══════════════════════════════════════════════════════════════════════════
// GLOBAL EXPORTS
// ═══════════════════════════════════════════════════════════════════════════
window.startLongPress         = startLongPress;
window.cancelLongPress        = cancelLongPress;
window.openCtxMenu            = openCtxMenu;
window.jumpToMsg              = jumpToMsg;
window.openLightbox           = openLightbox;
window.approveImagePermission = approveImagePermission;
window.denyImagePermission    = denyImagePermission;
window.onImgPermissionGranted = onImgPermissionGranted;
window.onImgPermissionDenied  = onImgPermissionDenied;
window.playVoice              = playVoice;
window.makeWaveform           = makeWaveform;
window.injectDeleteButtons    = injectDeleteButtons;

// ═══════════════════════════════════════════════════════════════════════════
// BOOT
// ═══════════════════════════════════════════════════════════════════════════
function checkPermState() {
  var marker = document.getElementById('chv-img-perm-state');
  if (marker && marker.getAttribute('data-granted') === '1') onImgPermissionGranted();
}

function boot() {
  hideBottomNav();
  wireEvents();
  updateSendBtn();
  loadMessages(true);
  // Use setTimeout-based self-scheduling instead of setInterval
  // so polls never pile up if the server is slow
  function schedulePoll() {
    pollTimer = setTimeout(function() {
      loadMessages(false);
      refreshTicks();
      refreshDeleted();
      schedulePoll();
    }, POLL_INTERVAL_MS);
  }
  schedulePoll();
}

boot();

})();
</script>
`, otherUser, avatarHTML, currentUser, otherUser,
		func() string {
			if profileImage != "" {
				return profileImage
			}
			return ""
		}(),
		TrendingStarHTML(otherUser))

	c.Set("Content-Type", "text/html")
	return c.SendString(Layout("Chat", chatPage, currentUser))
}

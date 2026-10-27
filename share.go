package main

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// ─────────────────────────────────────────────────────────────────────────────
// SHARE PAGE — dedicated "create a post" experience
// ─────────────────────────────────────────────────────────────────────────────
// Route: GET /share
// Reuses the existing Layout() for header/footer/nav, and posts to the same
// /post and /post/voice endpoints as the home composer, so no backend
// handlers need to change. This file only adds a new page + its own design.
//
// UX (rewrite):
//   • Three top-level mode boxes: Photo or Video  / Text Post / Voice Message.
//     Choosing one HIDES the other two and shows a compact "Change" chip
//     to go back to the picker — instead of all three competing for space.
//   • Voice is now fully independent of Text mode (previously voice only
//     lived inside text mode). Selecting Voice shows just the recorder +
//     an optional short caption + category + reactions + submit.
//   • Sizing is tightened throughout (smaller paddings/fonts/heights) so
//     the whole flow fits comfortably on a small screen (iPhone 6 class,
//     375×667 logical px) without scrolling for most steps.
//   • Contrast raised: darker borders/text on light mode, brighter accents
//     on dark mode, stronger focus/active states.
//
// To wire it up, add this route where your other routes are registered:
//   app.Get("/share", shareGetHandler)
// ─────────────────────────────────────────────────────────────────────────────

func shareGetHandler(c *fiber.Ctx) error {
	// Was reading the raw "user" cookie directly. The rest of the site
	// resolves the logged-in user through getCurrentUser(c), which goes
	// through the opaque session-token lookup (see AccountGateCheck /
	// lookupSession in main.go) rather than a plain cookie value —
	// reading the raw cookie here can disagree with what /post itself
	// expects when handling the submitted form.
	user := getCurrentUser(c)
	if user == "" {
		return c.Redirect("/signup")
	}

	// Direct-linkable mode: /share/photo, /share/text, /share/voice render
	// the same composer but arrive with that mode already picked — the
	// same idea as /dpcompete being its own bookmarkable page, instead of
	// every mode only being reachable by clicking a box after landing on
	// the bare /share picker.
	initialMode := ""
	switch c.Path() {
	case "/share/photo":
		initialMode = "photo"
	case "/share/text":
		initialMode = "text"
	case "/share/voice":
		initialMode = "voice"
	}

	c.Set("Content-Type", "text/html")

	shareContent := `
<style>
/* ══════════════════════════════════════════════════════════
   SHARE PAGE — dedicated post composer (compact / high-contrast)
   ══════════════════════════════════════════════════════════ */

:root {
  --sh-radius: 16px;
  --sh-radius-sm: 10px;
  --sh-px: 12px;
  --sh-bg: #ffffff;
  --sh-border: #d8dce3;
  --sh-surface: #f4f5f7;
  --sh-text: #0a0a0a;
  --sh-muted: #52525b;
  --sh-accent: #1d4ed8;
  --sh-accent-soft: #e8f0fe;
  --sh-green: #15803d;
  --sh-green-soft: #eafcf0;
  --sh-red: #b91c1c;
  --sh-red-soft: #fef0f0;
}
.dark {
  --sh-bg: #111827;
  --sh-border: #374151;
  --sh-surface: #1a2332;
  --sh-text: #f9fafb;
  --sh-muted: #cbd5e1;
  --sh-accent: #60a5fa;
  --sh-accent-soft: #1e3a5f;
  --sh-green: #4ade80;
  --sh-green-soft: #052e16;
  --sh-red: #f87171;
  --sh-red-soft: #450a0a;
}

* { box-sizing: border-box; }

.share-wrap {
  max-width: 480px;
  margin: 0 auto;
  padding: 10px 8px 24px;
}

.share-hero {
  text-align: center;
  padding: 2px 6px 12px;
}
.share-hero h1 {
  font-size: 17px;
  font-weight: 900;
  color: var(--sh-text);
  letter-spacing: -0.01em;
  margin: 0 0 2px;
}
.share-hero p {
  font-size: 11px;
  color: var(--sh-muted);
  margin: 0;
  font-weight: 600;
}

/* ── MAIN CARD ── */
.share-card {
  background: var(--sh-bg);
  border: 1px solid var(--sh-border);
  border-radius: var(--sh-radius);
  overflow: hidden;
  box-shadow: 0 1px 2px rgba(0,0,0,.05), 0 6px 18px rgba(0,0,0,.06);
}

/* ── MODE PICKER: three compact boxes, equal width ── */
.share-mode-row {
  display: flex;
  gap: 6px;
  padding: 12px var(--sh-px);
}
.share-mode-box {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
  padding: 12px 4px;
  border-radius: 12px;
  border: 1.5px solid var(--sh-border);
  background: var(--sh-surface);
  color: var(--sh-text);
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  transition: all .15s;
}
.share-mode-box:active { transform: scale(0.95); }
.share-mode-icon { font-size: 22px; line-height: 1; }
.share-mode-label { font-size: 10.5px; font-weight: 800; }
.share-mode-box.mode-photo.active {
  background: var(--sh-accent-soft);
  border-color: #7c3aed;
  color: #7c3aed;
}
.share-mode-box.mode-text.active {
  background: var(--sh-accent-soft);
  border-color: var(--sh-accent);
  color: var(--sh-accent);
}
.share-mode-box.mode-voice.active {
  background: var(--sh-green-soft);
  border-color: var(--sh-green);
  color: var(--sh-green);
}
.share-mode-box.mode-dp {
  color: #ff6b00;
}
.share-mode-box.mode-dp:hover {
  border-color: #ff6b00;
}

/* Once a mode is chosen: picker row collapses to a single "Change mode" chip */
.share-mode-row.picked .share-mode-box:not(.active) { display: none; }
.share-mode-row.picked .share-mode-box.active {
  flex-direction: row;
  gap: 6px;
  padding: 8px 12px;
  font-size: 11.5px;
  cursor: default;
}
.share-mode-row.picked .share-mode-box.active .share-mode-icon { font-size: 15px; }
.share-change-btn {
  flex-shrink: 0;
  margin-left: auto;
  font-size: 10px;
  font-weight: 800;
  color: var(--sh-accent);
  background: var(--sh-bg);
  border: 1.5px solid var(--sh-accent);
  border-radius: 999px;
  padding: 4px 10px;
  cursor: pointer;
  display: none;
}
.share-mode-row.picked .share-change-btn { display: inline-flex; align-items: center; }

/* Everything below the mode picker is hidden until a mode is chosen */
.share-body { display: none; }
.share-body.visible { display: block; }

/* ── STEP LABEL ── */
.share-step {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 10px var(--sh-px) 5px;
}
.share-step-num {
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--sh-accent);
  color: #fff;
  font-size: 9px;
  font-weight: 900;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.share-step-label {
  font-size: 11px;
  font-weight: 800;
  color: var(--sh-text);
}
.share-step-sub {
  font-size: 9.5px;
  color: var(--sh-muted);
  margin-left: auto;
  font-weight: 600;
}

/* ── CATEGORY SELECT ── */
.share-cat-select {
  font-size: 12px;
  font-weight: 700;
  color: var(--sh-accent);
  background: var(--sh-accent-soft);
  border: 1.5px solid var(--sh-accent);
  border-radius: var(--sh-radius-sm);
  padding: 8px 10px;
  outline: none;
  cursor: pointer;
  -webkit-appearance: none;
  width: 100%;
  margin: 0 0 4px;
}

/* ── FORMATTING TOOLBAR (text mode only) ── */
.share-format-row {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 5px var(--sh-px) 0;
  flex-wrap: wrap;
}
.share-format-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 7px;
  border: 1.5px solid var(--sh-border);
  background: var(--sh-surface);
  color: var(--sh-text);
  font-size: 12px;
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
}
.share-format-btn:active { transform: scale(0.9); }
.share-format-btn.b { font-weight: 900; }
.share-format-btn.i { font-style: italic; font-family: serif; }
.share-format-divider { width: 1px; height: 16px; background: var(--sh-border); margin: 0 1px; }

/* ── EMOJI ROW ── */
.share-emoji-row {
  display: flex;
  justify-content: center;
  gap: 5px;
  padding: 6px var(--sh-px) 0;
}
.share-emoji-btn {
  flex: 1;
  max-width: 30px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 14px;
  background: var(--sh-surface);
  border: 1.5px solid var(--sh-border);
  border-radius: 8px;
  cursor: pointer;
}
.share-emoji-btn:active { transform: scale(0.85); }

/* ── TEXTAREA ── */
.share-textarea-wrap { padding: 8px var(--sh-px) 2px; }
.share-textarea {
  display: block;
  width: 100%;
  padding: 10px 11px;
  background: var(--sh-surface);
  border: 1.5px solid var(--sh-border);
  border-radius: var(--sh-radius-sm);
  outline: none;
  font-size: 13.5px;
  line-height: 1.45;
  resize: none;
  color: var(--sh-text);
  min-height: 70px;
  max-height: 140px;
  -webkit-appearance: none;
}
.share-textarea:focus { border-color: var(--sh-accent); }
.share-char-count { text-align: right; font-size: 9.5px; color: var(--sh-muted); font-weight: 600; padding: 3px 2px 0; }
.share-textarea.caption-mode { min-height: 44px; }

/* ── MEDIA ACTIONS ── */
.share-media-row { display: flex; gap: 6px; padding: 8px var(--sh-px); flex-wrap: wrap; }
.share-media-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 7px 10px;
  border-radius: var(--sh-radius-sm);
  border: 1.5px solid var(--sh-border);
  background: var(--sh-surface);
  color: var(--sh-text);
  font-size: 11px;
  font-weight: 700;
  cursor: pointer;
  white-space: nowrap;
}
.share-media-btn:active { transform: scale(0.95); }
.share-media-btn.gif { color: var(--sh-accent); background: var(--sh-accent-soft); border-color: var(--sh-accent); }
.share-media-btn.mic { color: var(--sh-green); background: var(--sh-green-soft); border-color: var(--sh-green); }
.share-media-btn.mic.recording { color: var(--sh-red); background: var(--sh-red-soft); border-color: var(--sh-red); animation: sh-mic-pulse 1s ease-in-out infinite; }
@keyframes sh-mic-pulse { 0%,100% { box-shadow: 0 0 0 0 rgba(220,38,38,.3); } 50% { box-shadow: 0 0 0 5px rgba(220,38,38,0); } }
#sh-mic-timer { font-size: 10px; font-weight: 800; color: var(--sh-red); display: none; align-self: center; font-variant-numeric: tabular-nums; }
#sh-mic-timer.visible { display: inline-block; }

/* Voice-mode big recorder button (separate mode, so it's the hero action) */
.sh-voice-hero {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 14px var(--sh-px) 6px;
}
.sh-voice-hero-btn {
  width: 64px;
  height: 64px;
  border-radius: 50%;
  border: none;
  background: var(--sh-green);
  color: #fff;
  font-size: 24px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  box-shadow: 0 4px 14px rgba(21,128,61,.35);
}
.sh-voice-hero-btn.recording { background: var(--sh-red); animation: sh-mic-pulse 1s ease-in-out infinite; }
.sh-voice-hero-label { font-size: 11px; font-weight: 800; color: var(--sh-text); }

/* ── VOICE / THUMBNAIL PREVIEWS ── */
#sh-voice-preview {
  display: none;
  align-items: center;
  gap: 8px;
  margin: 0 var(--sh-px) 8px;
  padding: 8px 10px;
  background: var(--sh-green-soft);
  border: 1.5px solid var(--sh-green);
  border-radius: var(--sh-radius-sm);
}
#sh-voice-preview.visible { display: flex; }
#sh-voice-preview audio { height: 28px; flex: 1; min-width: 0; }
.sh-cancel-btn {
  background: var(--sh-red);
  color: white;
  border: none;
  border-radius: 50%;
  width: 20px;
  height: 20px;
  font-size: 9px;
  cursor: pointer;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0;
}
.share-thumb-row { display: flex; gap: 6px; padding: 0 var(--sh-px) 8px; }
.share-thumb-box { position: relative; width: 60px; height: 60px; border-radius: 10px; overflow: hidden; flex-shrink: 0; border: 1.5px solid var(--sh-border); }
.share-thumb-box img { width: 100%; height: 100%; object-fit: cover; }
.share-thumb-del {
  position: absolute; top: 2px; right: 2px;
  background: rgba(0,0,0,.65); color: white; border: none; border-radius: 50%;
  width: 16px; height: 16px; font-size: 9px; cursor: pointer;
  display: flex; align-items: center; justify-content: center; padding: 0;
}
#sh-image-preview { border-color: var(--sh-accent); }
#sh-gif-preview-container { border-color: var(--sh-green); }

.share-photo-dropzone {
  margin: 0 var(--sh-px) 8px;
  border: 2px dashed #7c3aed;
  border-radius: var(--sh-radius-sm);
  background: var(--sh-accent-soft);
  color: #7c3aed;
  padding: 16px 10px;
  text-align: center;
  cursor: pointer;
}
.share-photo-dropzone .dz-icon { font-size: 20px; line-height: 1; margin-bottom: 3px; }
.share-photo-dropzone .dz-label { font-size: 11px; font-weight: 800; }
.share-photo-dropzone .dz-sub { font-size: 9px; color: var(--sh-muted); font-weight: 600; margin-top: 1px; }
.share-photo-dropzone.hidden-dz { display: none; }

/* ── TOGGLES ── */
.share-toggles { display: flex; gap: 6px; padding: 4px var(--sh-px) 10px; }
.share-toggle-card {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 5px;
  padding: 8px 9px;
  border-radius: var(--sh-radius-sm);
  border: 1.5px solid var(--sh-border);
  background: var(--sh-surface);
  cursor: pointer;
}
.share-toggle-title { font-size: 10.5px; font-weight: 800; color: var(--sh-text); white-space: nowrap; }
.sh-toggle-track { position: relative; width: 30px; height: 17px; background: #9ca3af; border-radius: 999px; flex-shrink: 0; transition: background .2s; }
.sh-toggle-track::after {
  content: ''; position: absolute; top: 2px; left: 2px; width: 13px; height: 13px;
  background: white; border-radius: 50%; box-shadow: 0 1px 3px rgba(0,0,0,.3); transition: transform .2s;
}
.sh-toggle-input { display: none; }
.sh-toggle-input.jhappi:checked + .sh-toggle-track { background: var(--sh-green); }
.sh-toggle-input.chupair:checked + .sh-toggle-track { background: var(--sh-red); }
.sh-toggle-input:checked + .sh-toggle-track::after { transform: translateX(13px); }

/* ── SUBMIT BAR ── */
.share-submit-bar {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  padding: 10px var(--sh-px);
  border-top: 1.5px solid var(--sh-border);
  background: var(--sh-surface);
}
.share-cancel-link { font-size: 11px; font-weight: 700; color: var(--sh-muted); text-decoration: none; padding: 8px 10px; }
.share-post-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 9px 20px;
  background: var(--sh-accent);
  color: white;
  font-size: 12.5px;
  font-weight: 800;
  border-radius: 999px;
  border: none;
  cursor: pointer;
  box-shadow: 0 3px 10px rgba(29,78,216,.35);
}
.share-post-btn:active { transform: scale(0.95); }

#share-error { padding: 0 var(--sh-px); }

/* ── GIPHY MODAL ── */
#sh-giphy-picker {
  display: none; position: fixed; inset: 0; z-index: 200000;
  align-items: center; justify-content: center;
  background: rgba(0,0,0,.75); backdrop-filter: blur(6px); padding: 12px;
}
#sh-giphy-picker.open { display: flex; }
.sh-giphy-modal {
  background: var(--sh-bg); width: 100%; max-width: 340px; height: 70vh;
  border-radius: 16px; overflow: hidden; display: flex; flex-direction: column;
  box-shadow: 0 30px 60px rgba(0,0,0,.5);
}
.sh-giphy-header { padding: 10px 12px 8px; border-bottom: 1.5px solid var(--sh-border); }
.sh-giphy-title { display: flex; justify-content: space-between; align-items: center; margin-bottom: 6px; font-weight: 800; font-size: 12.5px; color: var(--sh-text); }
.sh-giphy-close { color: var(--sh-muted); font-size: 16px; background: none; border: none; cursor: pointer; line-height: 1; padding: 0; }
.sh-giphy-search { width: 100%; padding: 7px 10px; background: var(--sh-surface); border: 1.5px solid var(--sh-border); border-radius: 9px; font-size: 12px; outline: none; color: var(--sh-text); -webkit-appearance: none; }
#sh-giphy-results { flex: 1; padding: 6px; overflow-y: scroll; -webkit-overflow-scrolling: touch; }
.sh-giphy-footer { padding: 6px 10px; border-top: 1.5px solid var(--sh-border); }
.sh-giphy-footer-btn { width: 100%; padding: 8px; background: var(--sh-surface); border: none; border-radius: 9px; font-weight: 800; font-size: 11.5px; cursor: pointer; color: var(--sh-text); }

.hidden { display: none !important; }
.mention-dropdown { position:absolute; z-index:99999; display:none; background:var(--sh-bg); border:1.5px solid var(--sh-border); border-radius:10px; max-height:220px; overflow-y:auto; box-shadow:0 8px 24px rgba(0,0,0,.18); }
.mention-dropdown.open { display:block; }
.mention-suggestion { display:flex; align-items:center; gap:8px; padding:8px 10px; cursor:pointer; }
.mention-suggestion:hover { background:var(--sh-surface); }
.mention-avatar { width:24px;height:24px;border-radius:50%;object-fit:cover;flex-shrink:0; }
.mention-uname { font-weight:800; font-size:12px; color:var(--sh-accent); }
.mention-display { font-size:11px; color:var(--sh-muted); }
.mention-empty { padding:10px; font-size:11.5px; color:var(--sh-muted); text-align:center; }

/* Reaction toggle icons in Post Settings — one fixed size everywhere so
   every GIF (each with different native dimensions) lines up identically
   instead of stretching/squashing per-icon; scaled down on narrow
   screens via the media query below. */
.ps-reaction-icon { width: 20px; height: 20px; object-fit: contain; flex-shrink: 0; }

/* iPhone 6 / narrow screens (≤375px) — trim just a little further */
@media (max-width: 375px) {
  :root { --sh-px: 9px; }
  .share-mode-icon { font-size: 19px; }
  .share-mode-label { font-size: 9.5px; }
  .share-textarea { font-size: 13px; min-height: 60px; }
  .sh-voice-hero-btn { width: 56px; height: 56px; font-size: 20px; }
  .ps-reaction-icon { width: 17px; height: 17px; }
}
</style>

<div class="share-wrap" id="share-wrap-root">
  <div id="mention-dropdown" class="mention-dropdown"></div>
  <div class="share-hero">
    <h1>Make a Post</h1>
    <p>Say something to your friends on Dostana</p>
  </div>

  <div class="share-card">
    <div id="share-error"></div>

    <!-- Mode picker: Photo / Text / Voice — picking one hides the other two -->
    <div class="share-mode-row" id="share-mode-row">
      <a href="/share/photo" hx-boost="false" class="share-mode-box mode-photo" id="mode-photo-box" style="text-decoration:none;">
        <span class="share-mode-icon">📷</span>
        <span class="share-mode-label">Photo or Video </span>
      </a>
      <a href="/share/text" hx-boost="false" class="share-mode-box mode-text" id="mode-text-box" style="text-decoration:none;">
        <span class="share-mode-icon">✍️</span>
        <span class="share-mode-label">Text Post</span>
      </a>
      <a href="/share/voice" hx-boost="false" class="share-mode-box mode-voice" id="mode-voice-box" style="text-decoration:none;">
        <span class="share-mode-icon">🎙️</span>
        <span class="share-mode-label">Voice Message</span>
      </a>
      <a href="/dpcompete" hx-boost="false" class="share-mode-box mode-dp" style="text-decoration:none;">
        <span class="share-mode-icon">🖼️</span>
        <span class="share-mode-label">DP Competition</span>
      </a>
      <button type="button" class="share-change-btn" onclick="shareResetMode()">Change</button>
    </div>

    <div style="padding:0 12px 10px;">
      <button type="button" onclick="toggleUniversalSettings()"
              style="width:100%;display:flex;align-items:center;justify-content:center;gap:6px;padding:9px;border-radius:10px;border:1.5px solid var(--sh-accent);background:var(--sh-accent-soft);color:var(--sh-accent);font-size:11.5px;font-weight:800;cursor:pointer;">
        ⚙️ Post Settings
      </button>
      <div id="universal-settings-panel" class="hidden" style="margin-top:8px;padding:10px;border:1.5px solid var(--sh-border);border-radius:12px;background:var(--sh-bg);"></div>
    </div>

    <form id="share-form"
          hx-post="/post"
          hx-target="body"
          hx-swap="none"
          hx-disabled-elt="#share-submit-btn"
          enctype="multipart/form-data"
          onsubmit="return shareHandlePostSubmit(event)">

      <div class="share-body" id="share-body">

        <!-- Step: Category (shown in all modes) -->
        <div class="share-step">
          <span class="share-step-num">1</span>
          <span class="share-step-label">Category</span>
        </div>
        <select name="category" required class="share-cat-select" style="margin-left:12px;margin-right:12px;width:calc(100% - 24px);">
          <option value="General">🌍 General</option>
          <option value="Aseel">🐓 Aseel / Birds</option>
          <option value="Motovlog">🏍️ Motovlog / Bikes</option>
          <option value="Gaming">🎮 Gaming</option>
          <optgroup label="Poetry & Literature">
            <option value="Sad Poetry">🥺 Sad Poetry</option>
            <option value="Love Poetry">❤️ Love / Ishq</option>
            <option value="Urdu Ghazal">📜 Urdu Ghazal</option>
            <option value="Sufi Poetry">🕌 Sufi / Arfana</option>
            <option value="Deep Lines">🖋️ Deep Lines</option>
            <option value="Attitude Poetry">😎 Attitude</option>
            <option value="Funny Poetry">😂 Mazahiya</option>
            <option value="Punjabi Poetry">🚜 Punjabi</option>
            <option value="Islamic Poetry">🌙 Hamd &amp; Naat</option>
            <option value="Literature">📖 Urdu Adab</option>
          </optgroup>
          <optgroup label="Lifestyle">
            <option value="Photography">📸 Photography</option>
            <option value="Travel">✈️ Travel</option>
            <option value="Food">🍱 Food &amp; Cooking</option>
            <option value="Fashion">✨ Fashion</option>
            <option value="Fitness">💪 Fitness &amp; Gym</option>
            <option value="Vlog">🎥 Daily Vlogging</option>
            <option value="Pets">🐾 Pets</option>
            <option value="Gardening">🌻 Gardening</option>
          </optgroup>
          <optgroup label="Knowledge & Work">
            <option value="Tech">💻 Tech &amp; Coding</option>
            <option value="AI">🤖 AI &amp; Future</option>
            <option value="Education">📚 Education</option>
            <option value="Business">💼 Business</option>
            <option value="Crypto">💰 Crypto</option>
            <option value="Science">🧪 Science</option>
          </optgroup>
          <optgroup label="Entertainment">
            <option value="Memes">😂 Memes</option>
            <option value="Movies">🎬 Movies &amp; Series</option>
            <option value="Music">🎵 Music / Songs</option>
            <option value="Art">🎨 Art &amp; Sketching</option>
            <option value="Drama">📺 Drama</option>
            <option value="Anime">⛩️ Anime</option>
          </optgroup>
          <optgroup label="Community">
            <option value="News">📰 News</option>
            <option value="Sports">🏏 Sports</option>
            <option value="Automotive">🚗 Automotive</option>
            <option value="Politics">⚖️ Politics</option>
            <option value="Health">🏥 Health</option>
            <option value="Religion">🕋 Deen / Islamic</option>
          </optgroup>
        </select>

        <!-- ══════════════ PHOTO MODE ══════════════ -->
        <div id="photo-mode-fields" class="hidden">

          <div class="share-step">
            <span class="share-step-num">2</span>
            <span class="share-step-label">Add your photo</span>
          </div>

         <div class="share-photo-dropzone" id="sh-photo-dropzone" onclick="document.getElementById('sh-image-upload').click();">
            <div class="dz-icon">📷</div>
            <div class="dz-label">Tap to choose a photo or video</div>
           <div class="dz-sub">Video max 500MB — or add a GIF instead below</div>
          </div>
          <input type="file" name="image" accept="image/*,video/mp4,video/webm,video/quicktime,video/x-matroska" class="hidden" id="sh-image-upload" onchange="sharePreviewImage(this)">

          <div class="share-thumb-row">
            <div id="sh-image-preview" class="share-thumb-box hidden">
              <img id="sh-preview-img" src="#">
              <button type="button" onclick="shareClearPreview()" class="share-thumb-del">&#10005;</button>
            </div>
            <div id="sh-gif-preview-container" class="share-thumb-box hidden">
              <img id="sh-gif-preview-img" src="">
              <button type="button" onclick="shareRemoveSelectedGif()" class="share-thumb-del">&#10005;</button>
            </div>
          </div>

          <div class="share-media-row" style="padding-top:0;">
            <button type="button" onclick="shareToggleGiphy()" class="share-media-btn gif">🎞️ Add a GIF instead</button>
          </div>

          <div class="share-step">
            <span class="share-step-num">3</span>
            <span class="share-step-label">Caption</span>
            <span class="share-step-sub">Optional</span>
          </div>
          <div class="share-emoji-row">
            <button type="button" class="share-emoji-btn" onclick="shareInsertEmoji('😂')">😂</button>
            <button type="button" class="share-emoji-btn" onclick="shareInsertEmoji('❤️')">❤️</button>
            <button type="button" class="share-emoji-btn" onclick="shareInsertEmoji('🔥')">🔥</button>
            <button type="button" class="share-emoji-btn" onclick="shareInsertEmoji('😭')">😭</button>
            <button type="button" class="share-emoji-btn" onclick="shareInsertEmoji('👏')">👏</button>
          </div>
          <div class="share-textarea-wrap">
            <textarea name="content"
                      id="sh-content-photo"
                      placeholder="Likho is tasveer ke baare mein…"
                      oninput="this.style.height='';this.style.height=Math.min(this.scrollHeight,140)+'px';shareUpdateCharCount(this)"
                      class="share-textarea caption-mode"></textarea>
            <div class="share-char-count"><span id="sh-char-count">0</span> letters</div>
          </div>

        </div><!-- /photo-mode-fields -->

        <!-- ══════════════ TEXT MODE ══════════════ -->
        <div id="text-mode-fields" class="hidden">

          <div class="share-step">
            <span class="share-step-num">2</span>
            <span class="share-step-label">Write your post</span>
            <span class="share-step-sub">Select text, tap B / i</span>
          </div>

          <div class="share-format-row">
            <button type="button" class="share-format-btn b" onclick="shareApplyStyle('bold')" title="Bold">B</button>
            <button type="button" class="share-format-btn i" onclick="shareApplyStyle('italic')" title="Italic">i</button>
            <div class="share-format-divider"></div>
            <button type="button" class="share-format-btn" onclick="shareApplyStyle('clear')" title="Remove styling" style="font-size:10px;font-weight:700;">Aa</button>
          </div>

          <div class="share-emoji-row">
            <button type="button" class="share-emoji-btn" onclick="shareInsertEmoji('😂')">😂</button>
            <button type="button" class="share-emoji-btn" onclick="shareInsertEmoji('❤️')">❤️</button>
            <button type="button" class="share-emoji-btn" onclick="shareInsertEmoji('🔥')">🔥</button>
            <button type="button" class="share-emoji-btn" onclick="shareInsertEmoji('😭')">😭</button>
            <button type="button" class="share-emoji-btn" onclick="shareInsertEmoji('👏')">👏</button>
          </div>

          <div class="share-textarea-wrap">
            <textarea name="content"
                      id="sh-content-text"
                      placeholder="Kya soch rahe ho? Yaha likho…"
                      oninput="this.style.height='';this.style.height=Math.min(this.scrollHeight,180)+'px';shareUpdateCharCount(this)"
                      class="share-textarea"></textarea>
            <div class="share-char-count"><span id="sh-char-count-text">0</span> letters</div>
          </div>

        </div><!-- /text-mode-fields -->

        <!-- ══════════════ VOICE MODE (fully separate from text) ══════════════ -->
        <div id="voice-mode-fields" class="hidden">

          <div class="share-step">
            <span class="share-step-num">2</span>
            <span class="share-step-label">Record a voice message</span>
            <span class="share-step-sub">Max 2 min</span>
          </div>

          <div class="sh-voice-hero">
            <button type="button" id="sh-mic-btn" class="sh-voice-hero-btn" onclick="shareToggleMic()" title="Record voice">🎙️</button>
            <span class="sh-voice-hero-label" id="sh-mic-hero-label">Tap to record</span>
            <span id="sh-mic-timer">0:00</span>
          </div>

          <div class="share-step">
            <span class="share-step-num">3</span>
            <span class="share-step-label">Caption</span>
            <span class="share-step-sub">Optional</span>
          </div>
          <div class="share-textarea-wrap">
            <textarea name="content"
                      id="sh-content-voice"
                      placeholder="Kuch likhna chahte ho is voice ke sath?"
                      oninput="this.style.height='';this.style.height=Math.min(this.scrollHeight,100)+'px';shareUpdateCharCount(this)"
                      class="share-textarea caption-mode"></textarea>
          </div>

        </div><!-- /voice-mode-fields -->

        <!-- Voice preview — shared by voice mode (recorder output shown here) -->
        <div id="sh-voice-preview">
          <audio id="sh-voice-audio-preview" controls preload="none"></audio>
          <button type="button" class="sh-cancel-btn" onclick="shareCancelVoice()" title="Remove">&#10005;</button>
        </div>

        <!-- Hidden inputs -->
        <input type="hidden" name="source" value="share">
        <input type="hidden" id="sh-selected-gif-url" name="image_url">
        <input type="file" id="sh-voice-file-input" name="voice_ref" accept="audio/*" class="hidden">
        <input type="hidden" id="sh-voice-url-hidden" name="voice_url" value="">

        <!-- Reactions/comments/expiry are controlled by Universal Post
             Settings above — no per-post toggles needed here anymore. -->

        <!-- Upload progress — shown only while a request with a file body is in flight -->
        <div id="share-upload-progress" class="hidden" style="padding:0 var(--sh-px) 6px;">
          <div style="height:4px;background:var(--sh-border);border-radius:999px;overflow:hidden;">
            <div id="share-upload-progress-bar" style="height:100%;width:0%;background:var(--sh-accent);transition:width .1s linear;"></div>
          </div>
          <div id="share-upload-progress-label" style="font-size:9.5px;color:var(--sh-muted);font-weight:700;margin-top:3px;">Uploading… 0%</div>
        </div>

        <!-- Submit -->
        <div class="share-submit-bar">
          <a href="/" class="share-cancel-link">Cancel</a>
          <button type="submit" id="share-submit-btn" class="share-post-btn">
            <span id="share-submit-btn-label">Post</span> <span>→</span>
          </button>
        </div>

      </div><!-- /share-body -->
    </form>
  </div>
</div>

<!-- GIPHY modal -->
<div id="sh-giphy-picker">
  <div class="sh-giphy-modal">
    <div class="sh-giphy-header">
      <div class="sh-giphy-title">
        <span>Pick a GIF</span>
        <button type="button" onclick="shareToggleGiphy()" class="sh-giphy-close">&#10005;</button>
      </div>
      <input type="text" id="sh-giphy-search-input" placeholder="Search for a GIF…" class="sh-giphy-search" oninput="shareGiphyDebounce(this.value)">
    </div>
    <div id="sh-giphy-results"></div>
  <div class="sh-giphy-footer">
      <button type="button" onclick="shareToggleGiphy()" class="sh-giphy-footer-btn">Close</button>
    </div>
  </div>
</div>

<!-- Bootstrap trick: <script> tags inserted via htmx's innerHTML swap
     (how boosted navigation lands on this page) never execute — that's
     a deliberate browser rule, not a bug, and it's why this page only
     worked after a hard refresh (a real page load DOES execute <script>
     tags normally). Inline event-handler ATTRIBUTES like onerror below
     are not affected by that rule and fire normally either way, so this
     invisible image manually re-creates the script element (which the
     browser then executes for real) whenever it detects the tag below
     never ran on its own. -->
<img src="data:," alt="" style="display:none" width="0" height="0"
     onerror="this.remove(); (function(){
       if (window.initSharePage) return;
       var s = document.getElementById('share-boot-script');
       if (!s) return;
       var ns = document.createElement('script');
       ns.textContent = s.textContent;
       document.body.appendChild(ns);
       document.body.removeChild(ns);
     })();">

<script id="share-boot-script">
function initSharePage(){
  'use strict';

   var currentMode = null; // 'photo' | 'text' | 'voice'
   // Filled in server-side from the URL path (/share/photo, /share/text,
   // /share/voice). Empty string means the bare /share picker.
   var __shareInitialMode = "__SHARE_INITIAL_MODE__";

  // If the panel was mid-open when a stale js_ok cookie forced a full
  // reload (server restarted since this page loaded — see
  // JSRequiredMiddleware in main.go), reopen it automatically instead of
  // leaving it closed and empty. sessionStorage only — cleared when the
  // tab closes, not a persisted secret.
  if (sessionStorage.getItem('dostana_reopen_settings') === '1') {
    setTimeout(function(){ window.toggleUniversalSettings(); }, 0);
  }

  var universalSettingsLoaded = false;
  window.toggleUniversalSettings = function(){
    var panel = document.getElementById('universal-settings-panel');
    if (!panel) return;
    panel.classList.toggle('hidden');
    if (!panel.classList.contains('hidden') && !universalSettingsLoaded) {
      sessionStorage.setItem('dostana_reopen_settings', '1');
      universalSettingsLoaded = true;
      htmx.ajax('GET', '/post-settings', {target:'#universal-settings-panel', swap:'innerHTML'})
        .then(function(){ sessionStorage.removeItem('dostana_reopen_settings'); });
    }
  };

  /* ── Mode picker: choosing one HIDES the other two, shows a Change chip ── */
  window.shareChooseMode = function(mode){
    currentMode = mode;

    var row = document.getElementById('share-mode-row');
    row.classList.add('picked');

    document.getElementById('mode-photo-box').classList.toggle('active', mode === 'photo');
    document.getElementById('mode-text-box').classList.toggle('active',  mode === 'text');
    document.getElementById('mode-voice-box').classList.toggle('active', mode === 'voice');

    document.getElementById('share-body').classList.add('visible');
    document.getElementById('photo-mode-fields').classList.toggle('hidden', mode !== 'photo');
    document.getElementById('text-mode-fields').classList.toggle('hidden',  mode !== 'text');
    document.getElementById('voice-mode-fields').classList.toggle('hidden', mode !== 'voice');

    // Only the textarea matching the active mode should submit as "content".
    var photoTa = document.getElementById('sh-content-photo');
    var textTa  = document.getElementById('sh-content-text');
    var voiceTa = document.getElementById('sh-content-voice');
    if (photoTa) photoTa.disabled = (mode !== 'photo');
    if (textTa)  textTa.disabled  = (mode !== 'text');
    if (voiceTa) voiceTa.disabled = (mode !== 'voice');

    var body = document.getElementById('share-body');
    if (body) body.scrollIntoView({behavior:'smooth', block:'start'});
  };

  /* ── Change mode: back to the 3-box picker ── */
  window.shareResetMode = function(){
    currentMode = null;
    var row = document.getElementById('share-mode-row');
    row.classList.remove('picked');
    document.getElementById('mode-photo-box').classList.remove('active');
    document.getElementById('mode-text-box').classList.remove('active');
    document.getElementById('mode-voice-box').classList.remove('active');
    document.getElementById('share-body').classList.remove('visible');
    document.getElementById('photo-mode-fields').classList.add('hidden');
    document.getElementById('text-mode-fields').classList.add('hidden');
    document.getElementById('voice-mode-fields').classList.add('hidden');
  };

  // Landed directly on /share/photo, /share/text, or /share/voice —
  // pre-select that mode instead of leaving the picker up.
  if (__shareInitialMode === 'photo' || __shareInitialMode === 'text' || __shareInitialMode === 'voice') {
    window.shareChooseMode(__shareInitialMode);
  }

  window.shareUpdateCharCount = function(ta){
    var el = ta.id === 'sh-content-photo'
      ? document.getElementById('sh-char-count')
      : document.getElementById('sh-char-count-text');
    if (el) el.textContent = ta.value.length;
  };

    /* ── @mention autocomplete ── */
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
      setTimeout(function(){ document.getElementById('mention-dropdown').classList.remove('open'); }, 150);
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
    document.getElementById('mention-dropdown').classList.remove('open');
    shareUpdateCharCount(ta);
  };

  attachMentionWatcher(document.getElementById('sh-content-photo'));
  attachMentionWatcher(document.getElementById('sh-content-text'));
  attachMentionWatcher(document.getElementById('sh-content-voice'));

  /* ── Emoji insert at cursor — targets whichever textarea is active ── */
  window.shareInsertEmoji = function(emoji){
    var ta = currentMode === 'photo'
      ? document.getElementById('sh-content-photo')
      : document.getElementById('sh-content-text');
    if (!ta) return;
    var start = ta.selectionStart, end = ta.selectionEnd;
    var val = ta.value;
    ta.value = val.slice(0, start) + emoji + val.slice(end);
    var newPos = start + emoji.length;
    ta.focus();
    ta.setSelectionRange(newPos, newPos);
    ta.style.height = '';
    ta.style.height = Math.min(ta.scrollHeight, 180) + 'px';
    shareUpdateCharCount(ta);
  };

  /* ── Bold / Italic via Unicode math alphanumeric symbols (text mode) ── */
  var boldMap = {}, italicMap = {};
  (function(){
    var upper = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ';
    var lower = 'abcdefghijklmnopqrstuvwxyz';
    var digits = '0123456789';
    for (var i = 0; i < upper.length; i++) {
      boldMap[upper[i]]   = String.fromCodePoint(0x1D400 + i);
      boldMap[lower[i]]   = String.fromCodePoint(0x1D41A + i);
      italicMap[upper[i]] = String.fromCodePoint(0x1D434 + i);
      italicMap[lower[i]] = String.fromCodePoint(0x1D44E + i);
    }
    for (var d = 0; d < digits.length; d++) {
      boldMap[digits[d]] = String.fromCodePoint(0x1D7CE + d);
    }
  })();
  var boldReverse = {}, italicReverse = {};
  Object.keys(boldMap).forEach(function(k){ boldReverse[boldMap[k]] = k; });
  Object.keys(italicMap).forEach(function(k){ italicReverse[italicMap[k]] = k; });

  function transform(text, map){
    var out = '';
    for (var i = 0; i < text.length; i++){ out += map[text[i]] || text[i]; }
    return out;
  }
  function plainify(text){
    var out = '';
    for (var i = 0; i < text.length; i++){
      var ch = text[i];
      out += boldReverse[ch] || italicReverse[ch] || ch;
    }
    return out;
  }

  window.shareApplyStyle = function(kind){
    var ta = document.getElementById('sh-content-text');
    if (!ta) return;
    var start = ta.selectionStart, end = ta.selectionEnd;
    if (start === end) return;
    var val = ta.value;
    var selected = val.slice(start, end);
    var replaced;
    if (kind === 'bold')   replaced = transform(selected, boldMap);
    else if (kind === 'italic') replaced = transform(selected, italicMap);
    else replaced = plainify(selected);
    ta.value = val.slice(0, start) + replaced + val.slice(end);
    ta.focus();
    ta.setSelectionRange(start, start + replaced.length);
    ta.style.height = '';
    ta.style.height = Math.min(ta.scrollHeight, 180) + 'px';
    shareUpdateCharCount(ta);
  };

   window.sharePreviewImage = function(input){
    if(!input.files||!input.files[0]) return;
    var file = input.files[0];
    var dz = document.getElementById('sh-photo-dropzone');
    var previewBox = document.getElementById('sh-image-preview');
    var previewImg = document.getElementById('sh-preview-img');

    if (file.type.indexOf('video/') === 0) {
       if (file.size > 500 * 1024 * 1024) {
        alert('Video 500MB se zyada hai. Chhoti video try karein.');
        input.value = '';
        return;
      }
      var vid = document.getElementById('sh-preview-video');
      if (!vid) {
        vid = document.createElement('video');
        vid.id = 'sh-preview-video';
        vid.muted = true;
        vid.setAttribute('playsinline', '');
        vid.style.width = '100%';
        vid.style.height = '100%';
        vid.style.objectFit = 'cover';
        previewBox.insertBefore(vid, previewImg);
      }
      previewImg.classList.add('hidden');
      vid.classList.remove('hidden');
      // Object URL, not FileReader.readAsDataURL — reading a 200MB file
      // into a base64 string would hang the tab.
      vid.src = URL.createObjectURL(file);
      previewBox.classList.remove('hidden');
      if (dz) dz.classList.add('hidden-dz');
      return;
    }

    var existingVid = document.getElementById('sh-preview-video');
    if (existingVid) existingVid.classList.add('hidden');
    previewImg.classList.remove('hidden');

    var r = new FileReader();
    r.onload = function(e){
      previewImg.src = e.target.result;
      previewBox.classList.remove('hidden');
      if (dz) dz.classList.add('hidden-dz');
    };
    r.readAsDataURL(file);
  };
  window.shareClearPreview = function(){
    var up = document.getElementById('sh-image-upload');
    if(up) up.value = '';
    var ip = document.getElementById('sh-image-preview');
    if(ip) ip.classList.add('hidden');
    var dz = document.getElementById('sh-photo-dropzone');
    if (dz) dz.classList.remove('hidden-dz');
  };

  window.shareToggleGiphy = function(){
    var picker = document.getElementById('sh-giphy-picker');
    if(!picker) return;
    var isOpen = picker.classList.contains('open');
    if(isOpen){
      picker.classList.remove('open');
      document.body.style.overflow = '';
    } else {
      picker.classList.add('open');
      document.body.style.overflow = 'hidden';
      var si = document.getElementById('sh-giphy-search-input');
      if(si) si.focus();
      var results = document.getElementById('sh-giphy-results');
      if(results && !results.children.length){
        htmx.ajax('GET', '/giphy/search', {target:'#sh-giphy-results'});
      }
    }
  };

  window.shareSelectGiphy = function(url){
    document.getElementById('sh-selected-gif-url').value = url;
    document.getElementById('sh-gif-preview-img').src = url;
    document.getElementById('sh-gif-preview-container').classList.remove('hidden');
    var dz = document.getElementById('sh-photo-dropzone');
    if (dz) dz.classList.add('hidden-dz');
    window.shareToggleGiphy();
  };

  // Shared /giphy/search partial hardcodes onclick="selectGiphy('...')" —
  // alias to our page handler so clicking a GIF actually sets image_url.
  window.selectGiphy = function(url){ window.shareSelectGiphy(url); };

  window.shareRemoveSelectedGif = function(){
    var el = document.getElementById('sh-selected-gif-url');
    if(el) el.value = '';
    var cp = document.getElementById('sh-gif-preview-container');
    if(cp) cp.classList.add('hidden');
    var img = document.getElementById('sh-image-preview');
    var dz = document.getElementById('sh-photo-dropzone');
    if (dz && (!img || img.classList.contains('hidden'))) dz.classList.remove('hidden-dz');
  };

  var _shGiphyDebTimer = null;
  window.shareGiphyDebounce = function(val){
    clearTimeout(_shGiphyDebTimer);
    _shGiphyDebTimer = setTimeout(function(){
      var url = '/giphy/search' + (val ? '?q=' + encodeURIComponent(val) : '');
      htmx.ajax('GET', url, {target:'#sh-giphy-results'});
    }, 500);
  };

  /* ── Voice recorder (now used ONLY by voice mode) ── */
  var mediaRecorder = null;
  var audioChunks   = [];
  var recordedBlob  = null;
  var timerInterval = null;
  var recSeconds    = 0;
  var MAX_REC_SECS  = 120;

  function fmtTime(s){ var m=Math.floor(s/60),sec=s%60; return (m<10?'0':'')+m+':'+(sec<10?'0':'')+sec; }

  function startTimer(){
    recSeconds = 0;
    var el = document.getElementById('sh-mic-timer');
    if(el){ el.textContent='0:00'; el.classList.add('visible'); }
    timerInterval = setInterval(function(){
      recSeconds++;
      var t = document.getElementById('sh-mic-timer');
      if(t) t.textContent = fmtTime(recSeconds);
      if(recSeconds >= MAX_REC_SECS) stopRecording();
    }, 1000);
  }
  function stopTimer(){
    clearInterval(timerInterval);
    var el = document.getElementById('sh-mic-timer');
    if(el) el.classList.remove('visible');
    recSeconds = 0;
  }
  function getMicBtn(){ return document.getElementById('sh-mic-btn'); }
  function getMicLabel(){ return document.getElementById('sh-mic-hero-label'); }

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
          var ae = document.getElementById('sh-voice-audio-preview');
          if(ae) ae.src = URL.createObjectURL(recordedBlob);
          var vp = document.getElementById('sh-voice-preview');
          if(vp) vp.classList.add('visible');
          var ext  = finalMime.includes('ogg') ? '.ogg' : '.webm';
          var file = new File([recordedBlob], 'voice'+ext, {type:finalMime});
          try{
            var dt = new DataTransfer();
            dt.items.add(file);
            var vi = document.getElementById('sh-voice-file-input');
            if(vi) vi.files = dt.files;
          } catch(e){ console.warn('DataTransfer not supported'); }
        };
        mediaRecorder.start(250);
        startTimer();
        var btn = getMicBtn();
        if(btn) btn.classList.add('recording');
        var lbl = getMicLabel();
        if(lbl) lbl.textContent = 'Recording… tap to stop';
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
    var lbl = getMicLabel();
    if(lbl) lbl.textContent = 'Tap to record again';
  }
  window.shareToggleMic = function(){
    if(!mediaRecorder||mediaRecorder.state==='inactive') startRecording();
    else stopRecording();
  };
  window.shareCancelVoice = function(){
    recordedBlob = null; audioChunks = [];
    var vi = document.getElementById('sh-voice-file-input');
    if(vi) vi.value = '';
    var vp = document.getElementById('sh-voice-preview');
    if(vp) vp.classList.remove('visible');
    var ae = document.getElementById('sh-voice-audio-preview');
    if(ae) ae.src = '';
    if(mediaRecorder&&mediaRecorder.state!=='inactive') stopRecording();
    var btn = getMicBtn();
    if(btn) btn.classList.remove('recording');
    var lbl = getMicLabel();
    if(lbl) lbl.textContent = 'Tap to record';
  };

  window.shareHandlePostSubmit = function(evt){
    if (!currentMode) {
      evt.preventDefault();
      alert('Please choose Photo or Video , Text Post, or Voice Message first.');
      return false;
    }
    var vi      = document.getElementById('sh-voice-file-input');
    var hasFile = vi && vi.files && vi.files.length > 0;
    var hasBlob = !hasFile && recordedBlob;
    if(!hasFile && !hasBlob) return true;
    evt.preventDefault();

    var btn = document.getElementById('share-submit-btn');
    var lbl = document.getElementById('share-submit-btn-label');
    if (btn) btn.disabled = true;
    if (lbl) lbl.textContent = 'Uploading voice…';

    var fd = new FormData();
    if(hasFile){ fd.append('voice', vi.files[0]); }
    else { var ext = recordedBlob.type.includes('ogg')?'.ogg':'.webm'; fd.append('voice', recordedBlob, 'voice'+ext); }
    fetch('/post/voice',{method:'POST',body:fd})
      .then(function(res){ if(!res.ok) return res.text().then(function(t){throw new Error(t);}); return res.json(); })
      .then(function(data){
        var hui = document.getElementById('sh-voice-url-hidden');
        if(hui) hui.value = data.url;
        window.shareCancelVoice();
        if (lbl) lbl.textContent = 'Posting…';
        htmx.trigger(document.getElementById('share-form'),'submit');
      })
      .catch(function(err){
        console.error('Voice upload failed:',err);
        alert('Voice upload failed: '+err.message);
        if (btn) btn.disabled = false;
        if (lbl) lbl.textContent = 'Post';
      });
    return false;
  };

  var shareForm = document.getElementById('share-form');
  var shareSubmitLabel = document.getElementById('share-submit-btn-label');
  var shareProgressWrap = document.getElementById('share-upload-progress');
  var shareProgressBar = document.getElementById('share-upload-progress-bar');
  var shareProgressLabel = document.getElementById('share-upload-progress-label');

  function shareResetProgress(){
    if (shareProgressWrap) shareProgressWrap.classList.add('hidden');
    if (shareProgressBar) shareProgressBar.style.width = '0%';
    if (shareProgressLabel) shareProgressLabel.textContent = 'Uploading… 0%';
  }

  if (shareForm && !shareForm.__shareBound) {
    shareForm.__shareBound = true;
    shareForm.addEventListener('htmx:beforeRequest', function(){
      if (shareSubmitLabel) shareSubmitLabel.textContent = 'Posting…';
      var imgInput = document.getElementById('sh-image-upload');
      var gifUrl = document.getElementById('sh-selected-gif-url');
      var hasUpload = (imgInput && imgInput.files && imgInput.files.length > 0) ||
                      (gifUrl && gifUrl.value);
      if (hasUpload) {
        shareResetProgress();
        if (shareProgressWrap) shareProgressWrap.classList.remove('hidden');
      }
    });
    // htmx exposes real upload progress only on the XHR path — this fires
    // continuously while the multipart body (e.g. the photo) is uploading.
    shareForm.addEventListener('htmx:xhr:progress', function(evt){
      if (!evt.detail || !evt.detail.lengthComputable) return;
      var pct = Math.round((evt.detail.loaded / evt.detail.total) * 100);
      if (shareProgressBar) shareProgressBar.style.width = pct + '%';
      if (shareProgressLabel) shareProgressLabel.textContent = 'Uploading… ' + pct + '%';
      if (shareProgressWrap) shareProgressWrap.classList.remove('hidden');
    });

    // Bound via addEventListener (NOT the hx-on::after-request attribute)
    // because htmx's hx-on:* implementation builds its handler with a
    // dynamic Function() constructor internally — effectively an eval —
    // which the site's Content-Security-Policy blocks since script-src
    // only allows 'unsafe-inline', not 'unsafe-eval'. Under that CSP the
    // hx-on attribute silently fails to attach at all: the /post request
    // itself still goes through fine (fetch/XHR isn't governed by
    // script-src), the post gets created, but nothing ever calls
    // window.location redirect afterwards — exactly the "post appears on
    // home, but never redirected there" symptom. A real addEventListener
    // call is ordinary JS, not eval, so it isn't affected.
    shareForm.addEventListener('htmx:afterRequest', function(evt){
      window._shareOnRequestDone(evt);
    });
  }

  // Single handler for every /post attempt. Previously only the success
  // branch did anything — a failed request (expired session, validation
  // error, server error) left the button looking perfectly normal with
  // zero feedback, so the person just clicked Post again and again, each
  // click firing an independent request (which is how a slow/failing
  // first attempt turned into "had to click 3 times" and, occasionally,
  // duplicate posts once a retry landed). Every outcome is now shown, and
  // hx-disabled-elt above already re-enables the button once the request
  // settles either way.
  window._shareOnRequestDone = function(evt){
    shareResetProgress();
    if (shareSubmitLabel) shareSubmitLabel.textContent = 'Post';

    if (evt && evt.detail && evt.detail.successful) {
      window._shareOnSuccess();
      return;
    }

    var errBox = document.getElementById('share-error');
    if (errBox) {
      var status = evt && evt.detail && evt.detail.xhr ? evt.detail.xhr.status : 0;
      var msg = 'Post nahi ho saka, dobara koshish karein.';
      if (status === 401) msg = 'Aapka session khatam ho gaya hai — dobara login karein.';
      else if (status >= 500) msg = 'Server mein masla hai, thori dair mein dobara koshish karein.';
      else if (evt && evt.detail && evt.detail.xhr && evt.detail.xhr.responseText) {
        var t = evt.detail.xhr.responseText.trim();
        if (t && t.length < 200) msg = t;
      }
      errBox.innerHTML = '<div style="color:#dc2626;padding:8px 12px;font-size:11.5px;font-weight:700;">❌ ' + msg + '</div>';
      errBox.scrollIntoView({behavior:'smooth', block:'start'});
    }
  };

  window._shareOnSuccess = function(){
    window.location.href = '/';
  };
}

// ── Runs on every arrival at /share, whether via full page load or a
//    boosted navigation swap — see the bootstrap <img onerror> above.
// /share can be reached either by a full page load (hard refresh, first
// /share can be reached either by a full page load (hard refresh, first
// visit) or by an hx-boost'd nav link from elsewhere on the site. Either
// way, this hydrator runs initSharePage() exactly once per real arrival
// at /share — never on unrelated swaps happening elsewhere on the page
// (the inbox badge polls every 1s, invites every 10s, etc. — all of
// those also fire htmx:afterSettle, bubbled from deep inside the page,
// but none of them touch #share-wrap-root, so the guard below skips
// them). The guard lives on the DOM node itself (data-share-inited),
// not in a JS variable, so it's correct regardless of whether this
// script tag itself gets re-executed by the swap — even if it doesn't,
// the persisting document-level listener (attached once, the first time
// /share ever loaded in this tab) still fires on every later swap and
// still calls the same initSharePage closure to rehydrate a fresh
// #share-wrap-root node.
function __shareHydrate(){
  var root = document.getElementById('share-wrap-root');
  if (!root || root.dataset.shareInited === '1') return;
  root.dataset.shareInited = '1';
  initSharePage();
}

__shareHydrate();

if (window.__shareAfterSettleHandler) {
  document.removeEventListener('htmx:afterSettle', window.__shareAfterSettleHandler);
}
window.__shareAfterSettleHandler = __shareHydrate;
document.addEventListener('htmx:afterSettle', window.__shareAfterSettleHandler);
</script>
`

	shareContent = strings.Replace(shareContent, "__SHARE_INITIAL_MODE__", initialMode, 1)

	return c.SendString(Layout("Create Post", shareContent, user))
}

// ─────────────────────────────────────────────────────────────────────────────
// UNIVERSAL POST SETTINGS — persistent per-user defaults for every future post
// ─────────────────────────────────────────────────────────────────────────────
// GET  /post-settings              -> returns the tabbed settings panel (defaults to "Nayi Posts" tab)
// POST /post-settings              -> "Nayi Posts" tab: saves the 4 defaults + expiry for future posts
//                                       (or, if "Reset" is ticked, clears them all back to "allowed")
// POST /post-settings/apply-existing -> "Purani Posts" tab: applies the 4 toggles to posts already made
//                                       (or, if "Reset" is ticked, sets every existing post back to
//                                       fully allowed, ignoring the other 4 boxes on that submit)
//
// Register alongside the existing routes:
//   app.Get ("/post-settings", postSettingsGetHandler)
//   app.Post("/post-settings", postSettingsSaveHandler)
//   app.Post("/post-settings/apply-existing", postSettingsApplyExistingHandler)
// ─────────────────────────────────────────────────────────────────────────────

type universalPostSettings struct {
	DisabledReactions map[string]bool
	DisableComments   bool
	FollowersOnly     bool
	ExpiresIn         string // "", "24h", "3d"
	RepostPolicy      string // "public", "followers", "none"
	ThoughtsPolicy    string // "public", "followers", "none"
	MentionPolicy     string // "everyone", "followers", "none"
}

func fetchUniversalPostSettings(username string) universalPostSettings {
	var disabledCSV string
	s := universalPostSettings{DisabledReactions: map[string]bool{}}
	db.QueryRow(ctx, `
		SELECT COALESCE(default_disabled_reactions,''),
		       COALESCE(default_disable_comments,false),
		       COALESCE(default_followers_only,false),
		       COALESCE(default_expires_in,''),
		       COALESCE(default_repost_policy,'public'),
		       COALESCE(default_thoughts_policy,'public'),
		       COALESCE(mention_policy,'everyone')
		FROM users WHERE username = $1`, username,
	).Scan(&disabledCSV, &s.DisableComments, &s.FollowersOnly, &s.ExpiresIn, &s.RepostPolicy, &s.ThoughtsPolicy, &s.MentionPolicy)
	for _, k := range strings.Split(disabledCSV, ",") {
		if k = strings.TrimSpace(k); k != "" {
			s.DisabledReactions[k] = true
		}
	}
	return s
}

// oldPostToggles carries the four on/off switches used ONLY to update posts
// that are already published. These are never saved as the user's defaults —
// they're a one-time action, so we don't need a DB row for them.
type oldPostToggles struct {
	DisabledReactions map[string]bool
	DisableComments   bool
	FollowersOnly     bool
	RepostPolicy      string // "" = don't change; else "public"/"followers"/"none"
	ThoughtsPolicy    string // "" = don't change; else "public"/"followers"/"none"
}

func postSettingsGetHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.SendStatus(fiber.StatusUnauthorized)
	}
	s := fetchUniversalPostSettings(currentUser)
	return c.SendString(renderPostSettingsForm(s, oldPostToggles{}, "new", ""))
}

// postSettingsSaveHandler — "Nayi Posts" (New Posts) tab.
// Saves the four defaults that will be used for every post the user makes
// from now on. Does NOT touch posts that already exist.
//
// If the "Reset" tickbox is checked, it overrides everything else on this
// submit: all four defaults go back to off and expiry is cleared, so every
// future post is fully allowed again. The user can then tick individual
// boxes again (without Reset) on a later save to turn specific things off.
func postSettingsSaveHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.SendStatus(fiber.StatusUnauthorized)
	}

	disabled := map[string]bool{}
	for _, r := range reactionList {
		if c.FormValue("disable_"+r.Key) == "on" {
			disabled[r.Key] = true
		}
	}
	disableComments := c.FormValue("disable_comments") == "on"
	followersOnly := c.FormValue("followers_only") == "on"
	resetAll := c.FormValue("reset_all") == "on"
	expiresIn := c.FormValue("expires_in")
	switch expiresIn {
	case "24h", "3d":
	default:
		expiresIn = ""
	}

	repostPolicy := c.FormValue("repost_policy")
	switch repostPolicy {
	case "public", "followers", "none":
	default:
		repostPolicy = "public"
	}
	thoughtsPolicy := c.FormValue("thoughts_policy")
	switch thoughtsPolicy {
	case "public", "followers", "none":
	default:
		thoughtsPolicy = "public"
	}

	mentionPolicy := c.FormValue("mention_policy")
	switch mentionPolicy {
	case "everyone", "followers", "none":
	default:
		mentionPolicy = "everyone"
	}

	if resetAll {
		disabled = map[string]bool{}
		disableComments = false
		followersOnly = false
		expiresIn = ""
		repostPolicy = "public"
		thoughtsPolicy = "public"
		mentionPolicy = "everyone"
	}

	var disabledKeys []string
	for k := range disabled {
		disabledKeys = append(disabledKeys, k)
	}
	disabledCSV := strings.Join(disabledKeys, ",")

	_, err := db.Exec(ctx, `
		UPDATE users SET
			default_disabled_reactions = $1,
			default_disable_comments = $2,
			default_followers_only = $3,
			default_expires_in = $4,
			default_repost_policy = $5,
			default_thoughts_policy = $6,
			mention_policy = $7
		WHERE username = $8`,
		disabledCSV, disableComments, followersOnly, expiresIn, repostPolicy, thoughtsPolicy, mentionPolicy, currentUser)
	if err != nil {
		return c.Status(500).SendString(`<div style="color:#dc2626;padding:10px;font-size:12px;font-weight:700;">❌ Settings save nahi hui, dobara koshish karein.</div>`)
	}
	InvalidateMentionPolicy(currentUser)

	s := universalPostSettings{disabled, disableComments, followersOnly, expiresIn, repostPolicy, thoughtsPolicy, mentionPolicy}
	msg := "✅ Ho gaya! Ab se har nayi post pe yeh settings lagengi." + summarizeToggleStatus(disabled, disableComments, followersOnly)
	if resetAll {
		msg = "♻️ Reset ho gaya — ab se har nayi post par sab kuch allow hoga. Jo option band karni hai, wo dobara tick karke save karein." + summarizeToggleStatus(disabled, disableComments, followersOnly)
	}
	return c.SendString(renderPostSettingsForm(s, oldPostToggles{}, "new", msg))
}

// postSettingsApplyExistingHandler — "Purani Posts" (Old Posts) tab.
// Immediately updates every post the user has ALREADY made — but ONLY for
// the boxes that are actually ticked. A box left unticked is NOT touched at
// all: it does not get reset back to "allowed" just because it wasn't
// selected this time. This is a one-time action and is completely separate
// from the future-post defaults above — nothing here is saved to the user's
// row.
//
// If "Reset" is ticked, it takes priority over the other four boxes on this
// submit: every existing post is set back to fully allowed
// (allow_jhappi/allow_chupair/allow_comments = true, followers_only = false),
// regardless of what else is checked. The user can tick a specific box again
// on a later submit to re-restrict just that thing.
func postSettingsApplyExistingHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.SendStatus(fiber.StatusUnauthorized)
	}

	resetAll := c.FormValue("reset_all") == "on"
	s := fetchUniversalPostSettings(currentUser)

	if resetAll {
		_, err := db.Exec(ctx, `
			UPDATE posts SET
				disabled_reactions = '',
				allow_comments = TRUE,
				followers_only = FALSE,
				repost_policy = 'public',
				thoughts_policy = 'public'
			WHERE author = $1`, currentUser)
		if err != nil {
			return c.SendString(renderPostSettingsForm(s, oldPostToggles{}, "old", "⚠️ Reset nahi ho saka, dobara koshish karein."))
		}
		return c.SendString(renderPostSettingsForm(s, oldPostToggles{}, "old",
			"♻️ Ho gaya! Apki sab purani posts par ab sab kuch allow hai. Jo option band karni hai, wo dobara tick karke lagayein."))
	}

	toggles := oldPostToggles{DisabledReactions: map[string]bool{}}
	for _, r := range reactionList {
		if c.FormValue("disable_"+r.Key) == "on" {
			toggles.DisabledReactions[r.Key] = true
		}
	}
	toggles.DisableComments = c.FormValue("disable_comments") == "on"
	toggles.FollowersOnly = c.FormValue("followers_only") == "on"
	if v := c.FormValue("repost_policy"); v == "public" || v == "followers" || v == "none" {
		toggles.RepostPolicy = v
	}
	if v := c.FormValue("thoughts_policy"); v == "public" || v == "followers" || v == "none" {
		toggles.ThoughtsPolicy = v
	}

	var setClauses []string
	var args []any
	argN := 1
	addClause := func(col string, val any) {
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", col, argN))
		args = append(args, val)
		argN++
	}
	if len(toggles.DisabledReactions) > 0 {
		var keys []string
		for k := range toggles.DisabledReactions {
			keys = append(keys, k)
		}
		addClause("disabled_reactions", strings.Join(keys, ","))
	}
	if toggles.DisableComments {
		addClause("allow_comments", false)
	}
	if toggles.FollowersOnly {
		addClause("followers_only", true)
	}
	if toggles.RepostPolicy != "" {
		addClause("repost_policy", toggles.RepostPolicy)
	}
	if toggles.ThoughtsPolicy != "" {
		addClause("thoughts_policy", toggles.ThoughtsPolicy)
	}

	if len(setClauses) == 0 {
		return c.SendString(renderPostSettingsForm(s, toggles, "old",
			"⚠️ Koi bhi feature select nahi hui. Pehle kam az kam 1 box tick karein."))
	}

	query := "UPDATE posts SET " + strings.Join(setClauses, ", ") + fmt.Sprintf(" WHERE author = $%d", argN)
	args = append(args, currentUser)

	_, err := db.Exec(ctx, query, args...)
	if err != nil {
		return c.SendString(renderPostSettingsForm(s, toggles, "old", "⚠️ Purani posts update nahi ho sakin, dobara koshish karein."))
	}
	return c.SendString(renderPostSettingsForm(s, toggles, "old",
		"✅ Ho gaya! Sirf jo box tick ki thi wahi apki purani posts par laga di gayi hai — baqi jaisi thi waisi hi hai."+
			summarizeToggleStatus(toggles.DisabledReactions, toggles.DisableComments, toggles.FollowersOnly)))
}

// renderPostSettingsForm draws the tabbed "Post Settings" panel:
//   - Nayi Posts (New Posts) — defaults saved for every future post
//   - Purani Posts (Old Posts) — a one-time action applied to existing posts
//
// Both tabs end with a "Reset" tickbox: ticking it and saving clears all
// restrictions back to "everything allowed" for that tab's scope.
//
// activeTab is "new" or "old" — whichever tab should be open when this HTML
// is (re)rendered, e.g. after a save so the person sees where they just were.
func renderPostSettingsForm(s universalPostSettings, old oldPostToggles, activeTab string, message string) string {
	checked := func(b bool) string {
		if b {
			return "checked"
		}
		return ""
	}
	sel := func(v, want string) string {
		if v == want {
			return "selected"
		}
		return ""
	}
	hiddenIf := func(cond bool) string {
		if cond {
			return "hidden"
		}
		return ""
	}
	activeIf := func(cond bool) string {
		if cond {
			return "ps-tab-active"
		}
		return ""
	}
	isNew := activeTab != "old"

	msgHTML := ""
	if message != "" {
		color := "#059669"
		if strings.Contains(message, "⚠️") || strings.Contains(message, "❌") {
			color = "#dc2626"
		}
		msgHTML = `<div style="text-align:center;color:` + color + `;font-size:11.5px;font-weight:800;margin-top:8px;">` + message + `</div>`
	}

	return `
<div style="display:flex;flex-direction:column;gap:10px;">

  <div style="font-size:10.5px;color:var(--sh-muted);font-weight:700;text-align:center;">
    Yahan se decide karein ke aapki posts par log kya kar sakein
  </div>

  <!-- Two big, obvious tabs — plain language, no jargon -->
  <div style="display:flex;gap:6px;">
    <button type="button" onclick="psSwitchTab('new')"
            class="ps-tab-btn ` + activeIf(isNew) + `"
            style="flex:1;padding:10px 6px;border-radius:10px;border:1.5px solid var(--sh-accent);background:` + ifStr(isNew, "var(--sh-accent)", "var(--sh-accent-soft)") + `;color:` + ifStr(isNew, "#fff", "var(--sh-accent)") + `;font-size:12px;font-weight:800;cursor:pointer;">
      🆕 Nayi Posts
    </button>
    <button type="button" onclick="psSwitchTab('old')"
            class="ps-tab-btn ` + activeIf(!isNew) + `"
            style="flex:1;padding:10px 6px;border-radius:10px;border:1.5px solid var(--sh-accent);background:` + ifStr(!isNew, "var(--sh-accent)", "var(--sh-accent-soft)") + `;color:` + ifStr(!isNew, "#fff", "var(--sh-accent)") + `;font-size:12px;font-weight:800;cursor:pointer;">
      📜 Purani Posts
    </button>
  </div>

  <!-- ═══ TAB 1: Nayi Posts — defaults for every post from now on ═══ -->
  <div id="ps-panel-new" class="` + hiddenIf(!isNew) + `" style="display:flex;flex-direction:column;gap:8px;">

    <div style="font-size:10.5px;color:var(--sh-muted);font-weight:700;">
      Yeh settings AB SE har nayi post par khud-ba-khud lagengi
    </div>

    <form id="ps-form-new"
          hx-post="/post-settings"
          hx-target="#universal-settings-panel"
          hx-swap="innerHTML"
          style="display:flex;flex-direction:column;gap:8px;">

      ` + renderReactionToggleGrid(s.DisabledReactions) + `

      <label style="display:flex;align-items:center;justify-content:space-between;padding:10px;border:1.5px solid var(--sh-border);border-radius:10px;background:var(--sh-surface);">
        <span style="font-size:12px;font-weight:800;">💬 Comments Band Karein</span>
        <input type="checkbox" name="disable_comments" ` + checked(s.DisableComments) + `>
      </label>

      <label style="display:flex;align-items:center;justify-content:space-between;padding:10px;border:1.5px solid var(--sh-border);border-radius:10px;background:var(--sh-surface);">
        <span style="font-size:12px;font-weight:800;">🔒 Sirf Followers Reply Kar Sakein</span>
        <input type="checkbox" name="followers_only" ` + checked(s.FollowersOnly) + `>
      </label>

      <div style="padding:10px;border:1.5px solid var(--sh-border);border-radius:10px;background:var(--sh-surface);">
        <div style="font-size:12px;font-weight:800;margin-bottom:6px;">🔁 Kaun Repost Kar Sakta Hai?</div>
        <select name="repost_policy" style="width:100%;padding:7px 8px;border-radius:8px;border:1.5px solid var(--sh-border);font-size:12px;">
          <option value="public" ` + sel(s.RepostPolicy, "public") + `>Har Koi (Public)</option>
          <option value="followers" ` + sel(s.RepostPolicy, "followers") + `>Sirf Followers</option>
          <option value="none" ` + sel(s.RepostPolicy, "none") + `>Koi Nahi</option>
        </select>
      </div>

      <div style="padding:10px;border:1.5px solid var(--sh-border);border-radius:10px;background:var(--sh-surface);">
        <div style="font-size:12px;font-weight:800;margin-bottom:6px;">💭 Kaun Khayal (Thought) Ke Sath Repost Kar Sakta Hai?</div>
        <select name="thoughts_policy" style="width:100%;padding:7px 8px;border-radius:8px;border:1.5px solid var(--sh-border);font-size:12px;">
          <option value="public" ` + sel(s.ThoughtsPolicy, "public") + `>Har Koi (Public)</option>
          <option value="followers" ` + sel(s.ThoughtsPolicy, "followers") + `>Sirf Followers</option>
          <option value="none" ` + sel(s.ThoughtsPolicy, "none") + `>Koi Nahi</option>
        </select>
      </div>

      <div style="padding:10px;border:1.5px solid var(--sh-border);border-radius:10px;background:var(--sh-surface);">
        <div style="font-size:12px;font-weight:800;margin-bottom:6px;">📛 Kaun Aapko @mention Kar Sakta Hai?</div>
        <select name="mention_policy" style="width:100%;padding:7px 8px;border-radius:8px;border:1.5px solid var(--sh-border);font-size:12px;">
          <option value="everyone" ` + sel(s.MentionPolicy, "everyone") + `>Har Koi (Public)</option>
          <option value="followers" ` + sel(s.MentionPolicy, "followers") + `>Sirf Followers/Following</option>
          <option value="none" ` + sel(s.MentionPolicy, "none") + `>Koi Nahi</option>
        </select>
      </div>

      <div style="padding:10px;border:1.5px solid var(--sh-border);border-radius:10px;background:var(--sh-surface);">
        <div style="font-size:12px;font-weight:800;margin-bottom:6px;">⏳ Post Kitni Der Rahegi?</div>
        <select name="expires_in" style="width:100%;padding:7px 8px;border-radius:8px;border:1.5px solid var(--sh-border);font-size:12px;">
          <option value="" ` + sel(s.ExpiresIn, "") + `>Hamesha Rahegi</option>
          <option value="24h" ` + sel(s.ExpiresIn, "24h") + `>24 Ghante Baad Khatam</option>
          <option value="3d" ` + sel(s.ExpiresIn, "3d") + `>3 Din Baad Khatam</option>
        </select>
      </div>

      <label style="display:flex;align-items:center;justify-content:space-between;padding:10px;border:1.5px dashed #b91c1c;border-radius:10px;background:var(--sh-red-soft);">
        <span style="font-size:12px;font-weight:800;color:#b91c1c;">♻️ Sab Reset Karein (Sab Kuch Allow)</span>
        <input type="checkbox" name="reset_all">
      </label>

      <button type="submit" style="margin-top:2px;padding:11px;border:none;border-radius:999px;background:var(--sh-accent);color:#fff;font-weight:800;font-size:12.5px;cursor:pointer;">
        ✅ Nayi Posts Ke Liye Save Karein
      </button>
    </form>
  </div>

  <!-- ═══ TAB 2: Purani Posts — one-time action on posts already made ═══ -->
  <div id="ps-panel-old" class="` + hiddenIf(isNew) + `" style="display:flex;flex-direction:column;gap:8px;">

    <div style="font-size:11px;color:#92400e;font-weight:800;padding:8px 10px;border:1.5px dashed #f59e0b;border-radius:10px;background:#fffbeb;">
      ⚠️ Yahan jo bhi ON karenge, wo FAURAN apki SAB PURANI (pehle wali) posts par laga diya jayega.
    </div>

    <form id="ps-form-old"
          hx-post="/post-settings/apply-existing"
          hx-target="#universal-settings-panel"
          hx-swap="innerHTML"
          style="display:flex;flex-direction:column;gap:8px;">

      ` + renderReactionToggleGrid(old.DisabledReactions) + `

      <label style="display:flex;align-items:center;justify-content:space-between;padding:10px;border:1.5px solid var(--sh-border);border-radius:10px;background:var(--sh-surface);">
        <span style="font-size:12px;font-weight:800;">💬 Comments Band Karein</span>
        <input type="checkbox" name="disable_comments" ` + checked(old.DisableComments) + `>
      </label>

      <label style="display:flex;align-items:center;justify-content:space-between;padding:10px;border:1.5px solid var(--sh-border);border-radius:10px;background:var(--sh-surface);">
        <span style="font-size:12px;font-weight:800;">🔒 Sirf Followers Reply Kar Sakein</span>
        <input type="checkbox" name="followers_only" ` + checked(old.FollowersOnly) + `>
      </label>

      <div style="padding:10px;border:1.5px solid var(--sh-border);border-radius:10px;background:var(--sh-surface);">
        <div style="font-size:12px;font-weight:800;margin-bottom:6px;">🔁 Kaun Repost Kar Sakta Hai?</div>
        <select name="repost_policy" style="width:100%;padding:7px 8px;border-radius:8px;border:1.5px solid var(--sh-border);font-size:12px;">
          <option value="">Tabdeel Na Karein</option>
          <option value="public" ` + sel(old.RepostPolicy, "public") + `>Har Koi (Public)</option>
          <option value="followers" ` + sel(old.RepostPolicy, "followers") + `>Sirf Followers</option>
          <option value="none" ` + sel(old.RepostPolicy, "none") + `>Koi Nahi</option>
        </select>
      </div>

      <div style="padding:10px;border:1.5px solid var(--sh-border);border-radius:10px;background:var(--sh-surface);">
        <div style="font-size:12px;font-weight:800;margin-bottom:6px;">💭 Kaun Khayal Ke Sath Repost Kar Sakta Hai?</div>
        <select name="thoughts_policy" style="width:100%;padding:7px 8px;border-radius:8px;border:1.5px solid var(--sh-border);font-size:12px;">
          <option value="">Tabdeel Na Karein</option>
          <option value="public" ` + sel(old.ThoughtsPolicy, "public") + `>Har Koi (Public)</option>
          <option value="followers" ` + sel(old.ThoughtsPolicy, "followers") + `>Sirf Followers</option>
          <option value="none" ` + sel(old.ThoughtsPolicy, "none") + `>Koi Nahi</option>
        </select>
      </div>

      <label style="display:flex;align-items:center;justify-content:space-between;padding:10px;border:1.5px dashed #b91c1c;border-radius:10px;background:var(--sh-red-soft);">
        <span style="font-size:12px;font-weight:800;color:#b91c1c;">♻️ Sab Reset Karein (Sab Kuch Allow)</span>
        <input type="checkbox" name="reset_all">
      </label>

      <button type="submit" style="margin-top:2px;padding:11px;border:none;border-radius:999px;background:#b45309;color:#fff;font-weight:800;font-size:12.5px;cursor:pointer;">
        🔁 Purani Posts Par Abhi Laga Dein
      </button>
    </form>
  </div>

  ` + msgHTML + `

</div>

<script>
  window.psSwitchTab = function(tab){
    var panelNew = document.getElementById('ps-panel-new');
    var panelOld = document.getElementById('ps-panel-old');
    if (panelNew) panelNew.classList.toggle('hidden', tab !== 'new');
    if (panelOld) panelOld.classList.toggle('hidden', tab !== 'old');
    document.querySelectorAll('.ps-tab-btn').forEach(function(btn, i){
      var isActiveBtn = (i === 0 && tab === 'new') || (i === 1 && tab === 'old');
      btn.style.background = isActiveBtn ? 'var(--sh-accent)' : 'var(--sh-accent-soft)';
      btn.style.color = isActiveBtn ? '#fff' : 'var(--sh-accent)';
    });
  };
</script>`
}

// renderReactionToggleGrid draws the 9 reaction toggles (each with its
// GIF icon) shared by both the "Nayi Posts" and "Purani Posts" tabs.
func renderReactionToggleGrid(disabled map[string]bool) string {
	var sb strings.Builder
	sb.WriteString(`<div style="display:grid;grid-template-columns:1fr 1fr;gap:6px;">`)
	for _, r := range reactionList {
		chk := ""
		if disabled[r.Key] {
			chk = "checked"
		}
		sb.WriteString(fmt.Sprintf(`
      <label style="display:flex;align-items:center;justify-content:space-between;gap:6px;padding:8px;border:1.5px solid var(--sh-border);border-radius:10px;background:var(--sh-surface);">
        <span style="display:flex;align-items:center;gap:5px;font-size:11px;font-weight:800;"><img src="%s" loading="lazy" class="ps-reaction-icon" alt="">%s</span>
        <input type="checkbox" name="disable_%s" %s>
      </label>`, r.Gif, r.Label, r.Key, chk))
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

// summarizeToggleStatus builds a compact ON/OFF badge row for every
// individual setting, so a save confirmation doesn't just say "saved" —
// it shows exactly which switches ended up ON (restricted) vs OFF
// (allowed), since a generic "✅ Ho gaya!" gave no way to tell which
// state each toggle actually landed in after the submit.
func summarizeToggleStatus(disabled map[string]bool, disableComments, followersOnly bool) string {
	badge := func(label string, on bool) string {
		if on {
			return fmt.Sprintf(`<span style="display:inline-block;margin:2px 3px;padding:2px 7px;border-radius:999px;background:#fef2f2;color:#b91c1c;font-weight:800;">%s: ON</span>`, label)
		}
		return fmt.Sprintf(`<span style="display:inline-block;margin:2px 3px;padding:2px 7px;border-radius:999px;background:#ecfdf5;color:#059669;font-weight:800;">%s: OFF</span>`, label)
	}
	var sb strings.Builder
	for _, r := range reactionList {
		sb.WriteString(badge(r.Label+" band", disabled[r.Key]))
	}
	sb.WriteString(badge("Comments band", disableComments))
	sb.WriteString(badge("Followers-only", followersOnly))
	return `<div style="font-size:9.5px;text-align:center;margin-top:6px;">` + sb.String() + `</div>`
}

// ifStr is a tiny inline ternary helper used only for building style strings.
func ifStr(cond bool, ifTrue, ifFalse string) string {
	if cond {
		return ifTrue
	}
	return ifFalse
}

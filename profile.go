package main

import (
	"fmt"
	"html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"mime/multipart"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

const (
	maxUploadSize = 10 * 1024 * 1024 // 10MB
	uploadDir     = "./uploads/profile_images"

	// Infinite-scroll tuning. Posts are loaded in small batches instead of
	// numbered pages, and the DOM window is capped so a long scrolling
	// session never has to hold thousands of post nodes at once.
	postsInitialBatch  = 12
	postsLoadMoreBatch = 12
	postsDomWindowCap  = 60 // once the on-screen list passes this many posts, older ones off-screen get trimmed
)

// Pakistan cities list
var pakistanCities = []string{
	"Islamabad", "Karachi", "Lahore", "Faisalabad", "Rawalpindi", "Gujranwala",
	"Peshawar", "Multan", "Saidu Sharif", "Hyderabad", "Quetta", "Bahawalpur",
	"Sargodha", "Sialkot", "Sukkur", "Larkana", "Sheikhupura", "Rahim Yar Khan",
	"Jhang", "Dera Ghazi Khan", "Gujrat", "Sahiwal", "Wah Cantonment", "Mardan",
	"Kasur", "Okara", "Mingora", "Nawabshah", "Chiniot", "Kotri", "Kāmoke",
	"Hafizabad", "Sadiqabad", "Mirpur Khas", "Burewala", "Kohat", "Khanewal",
	"Dera Ismail Khan", "Turbat", "Muzaffargarh", "Abbottabad", "Mandi Bahauddin",
	"Shikarpur", "Jacobabad", "Jhelum", "Khanpur", "Khairpur", "Khuzdar",
	"Pakpattan", "Hub", "Daska", "Gojra", "Dadu", "Muridke", "Bahawalnagar",
	"Samundri", "Tando Allahyar", "Tando Adam", "Jaranwala", "Chishtian",
	"Attock", "Vehari", "Kot Abdul Malik", "Ferozwala", "Chakwal",
	"Kamalia", "Ahmedpur East", "Kot Addu", "Wazirabad", "Mansehra", "Layyah",
	"Swabi", "Chaman", "Taxila", "Nowshera", "Khushab", "Shahdadkot", "Mianwali",
	"Kabal", "Lodhran", "Hasilpur", "Charsadda", "Bhakkar", "Badin", "Arif Wala",
	"Ghotki", "Sambrial", "Jatoi", "Haroonabad", "Daharki", "Narowal",
	"Tando Muhammad Khan", "Kamber Ali Khan", "Mirpur Mathelo", "Kandhkot",
	"Bhalwal", "Other",
}

func initUploadDir() {
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		log.Fatal("Failed to create upload directory:", err)
	}
}

func calculateAge(birthDate time.Time) int {
	now := time.Now()
	age := now.Year() - birthDate.Year()
	if now.YearDay() < birthDate.YearDay() {
		age--
	}
	if age < 0 {
		return 0
	}
	return age
}

func validateImageUpload(file *multipart.FileHeader) (string, error) {
	if file.Size > maxUploadSize {
		return "", fmt.Errorf("file size exceeds 10MB limit")
	}

	ext := strings.ToLower(filepath.Ext(file.Filename))
	allowedExts := map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	}
	if !allowedExts[ext] {
		return "", fmt.Errorf("only image formats (JPG, PNG, GIF, WEBP) are allowed")
	}

	mimeType := file.Header.Get("Content-Type")
	allowedMimes := map[string]bool{
		"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true,
	}
	if !allowedMimes[mimeType] {
		return "", fmt.Errorf("invalid file type")
	}

	src, err := file.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	cfg, format, err := image.DecodeConfig(src)
	if err != nil {
		return "", fmt.Errorf("invalid or corrupted image file")
	}
	if imageTooLarge(cfg.Width, cfg.Height) {
		return "", fmt.Errorf("image dimensions too large")
	}

	allowedFormats := map[string]bool{"jpeg": true, "png": true, "gif": true}
	if !allowedFormats[format] {
		return "", fmt.Errorf("unsupported image format: %s", format)
	}

	return ext, nil
}

func saveUploadedImage(file *multipart.FileHeader, username string) (string, error) {
	if _, err := validateImageUpload(file); err != nil {
		return "", err
	}

	compressed, err := CompressUpload(file)
	if err != nil {
		return "", err
	}

	timestamp := time.Now().Unix()
	filename := fmt.Sprintf("%s_%d.jpg", username, timestamp) // always .jpg
	fp := filepath.Join(uploadDir, filename)

	if err := os.WriteFile(fp, compressed, 0644); err != nil {
		return "", err
	}

	return "/uploads/" + filename, nil
}

// ── toggleProfilePrivacyHandler ────────────────────────────────────────────
func toggleProfilePrivacyHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.SendStatus(401)
	}

	var isPrivate bool
	err := db.QueryRow(ctx, "SELECT COALESCE(is_private, false) FROM users WHERE username = $1", currentUser).Scan(&isPrivate)
	if err != nil {
		return c.SendStatus(500)
	}

	newPrivate := !isPrivate
	_, err = db.Exec(ctx, "UPDATE users SET is_private = $1 WHERE username = $2", newPrivate, currentUser)
	if err != nil {
		return c.SendStatus(500)
	}

	return c.SendString(renderPrivacyToggleButton(newPrivate))
}

// renderPrivacyToggleButton describes the ACTION the click will perform
// ("Make Profile Public/Private") rather than just the current state, so
// it reads like a normal settings control instead of a status label.
func renderPrivacyToggleButton(isPrivate bool) string {
	if isPrivate {
		return `<button hx-post="/toggle-profile-privacy" hx-swap="outerHTML"
			onmouseover="this.style.background='#fde9b0'" onmouseout="this.style.background='#fef3c7'"
			onfocus="this.style.outline='2px solid #b45309';this.style.outlineOffset='2px'" onblur="this.style.outline='none'"
			style="display:flex;align-items:center;gap:10px;background:#fef3c7;color:#b45309;padding:14px 20px;border-radius:16px;font-size:13px;font-weight:700;border:1px solid #fcd34d;cursor:pointer;transition:background .15s;width:100%;box-sizing:border-box;text-align:left;">
			<span style="font-size:18px;">🔒</span>
			<span style="flex:1;text-align:left;">
				<span style="display:block;text-align:left;">Profile is Private</span>
				<span style="display:block;font-size:11px;font-weight:500;color:#92400e;margin-top:2px;text-align:left;">Tap to make it public — anyone will be able to view it</span>
			</span>
			<span style="font-size:11px;font-weight:800;background:#fff;padding:6px 12px;border-radius:10px;white-space:nowrap;">Make Profile Public</span>
		</button>`
	}
	return `<button hx-post="/toggle-profile-privacy" hx-swap="outerHTML"
		onmouseover="this.style.background='#bcf3d8'" onmouseout="this.style.background='#d1fae5'"
		onfocus="this.style.outline='2px solid #047857';this.style.outlineOffset='2px'" onblur="this.style.outline='none'"
		style="display:flex;align-items:center;gap:10px;background:#d1fae5;color:#047857;padding:14px 20px;border-radius:16px;font-size:13px;font-weight:700;border:1px solid #6ee7b7;cursor:pointer;transition:background .15s;width:100%;box-sizing:border-box;text-align:left;">
		<span style="font-size:18px;">🌐</span>
		<span style="flex:1;text-align:left;">
			<span style="display:block;text-align:left;">Profile is Public</span>
			<span style="display:block;font-size:11px;font-weight:500;color:#065f46;margin-top:2px;text-align:left;">Tap to make it private — only you (and approved cases) can view it</span>
		</span>
		<span style="font-size:11px;font-weight:800;background:#fff;padding:6px 12px;border-radius:10px;white-space:nowrap;">Make Profile Private</span>
	</button>`
}

// ── editProfileFormHandler ──────────────────────────────────────────────────
func editProfileFormHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.SendStatus(401)
	}

	var userBio, userCity, userGender, profileImage, displayName, displayColor, displayFont, displayEffects string
	var birthDate time.Time

	err := db.QueryRow(ctx, `
		SELECT COALESCE(bio, ''), COALESCE(city, ''), COALESCE(gender, ''),
		       COALESCE(birth_date, '2000-01-01'::date), COALESCE(profile_image, ''),
		       COALESCE(display_name, ''), COALESCE(display_name_color, ''), COALESCE(display_name_font, ''),
		       COALESCE(display_name_effects, '')
		FROM users WHERE username = $1`, currentUser).
		Scan(&userBio, &userCity, &userGender, &birthDate, &profileImage, &displayName, &displayColor, &displayFont, &displayEffects)
	if err != nil {
		log.Println("Error fetching user data:", err)
	}

	cityOptions := ""
	for _, city := range pakistanCities {
		selected := ""
		if city == userCity {
			selected = "selected"
		}
		cityOptions += fmt.Sprintf(`<option value="%s" %s>%s</option>`, city, selected, city)
	}

	previewName := displayName
	if previewName == "" {
		previewName = currentUser
	}
	previewHTML := buildStyledNameHTML(previewName, displayColor, displayFont, displayEffects)

	formHTML := fmt.Sprintf(`
		<form hx-post="/update-profile"
			  hx-target="#profile-info"
			  hx-swap="outerHTML"
			  hx-encoding="multipart/form-data"
			  style="display:flex;flex-direction:column;gap:18px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">
			<div style="display:flex;flex-direction:column;gap:18px;">
				<div>
					<label style="display:block;font-size:13px;font-weight:700;color:#374151;margin-bottom:8px;">Profile Image</label>
				<div style="display:flex;flex-direction:column;gap:14px;">
    <div style="width:100%%;max-width:220px;aspect-ratio:1/1;height:auto;margin:0 auto;border-radius:24px;display:flex;align-items:center;justify-content:center;box-shadow:0 6px 24px rgba(0,0,0,0.12);overflow:hidden;background:#f3f4f6;">
        %s
    </div>
    <div style="width:100%%;">
        <input type="file" name="profile_image" accept="image/jpeg,image/png,image/gif,image/webp"
               style="width:100%%;padding:10px;background:#fff;border:1px solid #d1d5db;border-radius:14px;font-size:13px;color:#6b7280;box-sizing:border-box;">
        <p style="font-size:11px;color:#9ca3af;margin-top:6px;">Max 10MB • JPG, PNG, GIF, WEBP • shown in full, never cropped</p>
    </div>
</div>
				</div>

				<div>
					<label style="display:block;font-size:13px;font-weight:700;color:#374151;margin-bottom:6px;">City</label>
					<select name="city"
						style="width:100%%;padding:11px 14px;background:#fff;border:1px solid #d1d5db;border-radius:14px;font-size:14px;box-sizing:border-box;outline:none;">
						<option value="">Select your city</option>
						%s
					</select>
				</div>

				<div>
					<label style="display:block;font-size:13px;font-weight:700;color:#374151;margin-bottom:6px;">Gender</label>
					<select name="gender"
						style="width:100%%;padding:11px 14px;background:#fff;border:1px solid #d1d5db;border-radius:14px;font-size:14px;box-sizing:border-box;outline:none;">
						<option value="" %s>Not specified</option>
						<option value="Male" %s>Male</option>
						<option value="Female" %s>Female</option>
						<option value="Other" %s>Other</option>
					</select>
				</div>

				<div>
					<label style="display:block;font-size:13px;font-weight:700;color:#374151;margin-bottom:6px;">Birth Date</label>
					<input type="date" name="birth_date" value="%s"
						style="width:100%%;padding:11px 14px;background:#fff;border:1px solid #d1d5db;border-radius:14px;font-size:14px;box-sizing:border-box;outline:none;">
				</div>

				<div>
					<label style="display:block;font-size:13px;font-weight:700;color:#374151;margin-bottom:6px;">Bio (max 300 chars)</label>
					<textarea name="bio" rows="4" maxlength="300"
						style="width:100%%;padding:11px 14px;background:#fff;border:1px solid #d1d5db;border-radius:14px;font-size:14px;box-sizing:border-box;outline:none;resize:vertical;">%s</textarea>
					<p style="font-size:11px;color:#9ca3af;margin-top:6px;"><span id="bio-count">%d</span>/300 characters</p>
				</div>

				<div id="dn-editor" style="padding:16px;border-radius:18px;border:1px solid #e5e7eb;background:#f9fafb;display:flex;flex-direction:column;gap:14px;">
					<div>
						<label style="display:block;font-size:13px;font-weight:700;color:#374151;margin-bottom:6px;">
							Display Name (<span id="dn-count">%d</span>/%d chars, shown instead of your username)
						</label>
						<input type="text" id="dn-input" name="display_name" maxlength="%d" value="%s"
							placeholder="Leave empty to use your username"
							style="width:100%%;padding:11px 14px;background:#fff;border:1px solid #d1d5db;border-radius:14px;font-size:14px;box-sizing:border-box;outline:none;">
					</div>

					<div>
						<label style="display:block;font-size:13px;font-weight:700;color:#374151;margin-bottom:6px;">Preview</label>
						<div id="dn-preview" style="padding:12px 16px;border-radius:14px;background:#fff;border:1px solid #e5e7eb;font-size:18px;">%s</div>
					</div>

					<div>
						<label style="display:block;font-size:13px;font-weight:700;color:#374151;margin-bottom:6px;">Name Color</label>
						<div style="display:flex;flex-wrap:wrap;gap:8px;">%s</div>
					</div>
					<div>
						<label style="display:block;font-size:13px;font-weight:700;color:#374151;margin-bottom:6px;">Name Font</label>
						<div style="display:flex;flex-wrap:wrap;gap:8px;">%s</div>
					</div>
					<div>
						<label style="display:block;font-size:13px;font-weight:700;color:#374151;margin-bottom:6px;">Effects</label>
						<div style="display:flex;flex-wrap:wrap;gap:8px;">%s</div>
					</div>

					<input type="hidden" name="display_effects" id="dn-effects-hidden" value="%s">
				</div>
			</div>

			<div style="display:flex;gap:12px;padding-top:6px;">
				<button type="submit"
					style="background:#059669;color:#fff;padding:12px 26px;border-radius:14px;font-size:14px;font-weight:700;border:none;cursor:pointer;box-shadow:0 6px 16px rgba(5,150,105,0.25);">
					Save Changes
				</button>
				<button type="button" hx-get="/profile-info/%s" hx-target="#profile-info" hx-swap="outerHTML"
					style="background:#f3f4f6;color:#374151;padding:12px 26px;border-radius:14px;font-size:14px;font-weight:700;border:1px solid #e5e7eb;cursor:pointer;">
					Cancel
				</button>
			</div>
			<script>
				(function(){
					const textarea = document.querySelector('textarea[name="bio"]');
					const counter = document.getElementById('bio-count');
					if (textarea && counter) {
						textarea.addEventListener('input', function() { counter.textContent = this.value.length; });
					}
				})();
			</script>
		</form>
	`, getAvatarInner(profileImage, "contain"), cityOptions,
		selectedAttr(userGender == ""),
		selectedAttr(userGender == "Male"),
		selectedAttr(userGender == "Female"),
		selectedAttr(userGender == "Other"),
		birthDate.Format("2006-01-02"),
		userBio, len(userBio),
		len([]rune(displayName)), maxDisplayNameRunes, maxDisplayNameRunes,
		html.EscapeString(displayName),
		previewHTML,
		renderColorSwatches(displayColor),
		renderFontOptions(displayFont),
		renderEffectCheckboxes(displayEffects),
		displayEffects,
		currentUser) + dnLivePreviewScript()

	return c.SendString(formHTML)
}

// ── small helpers used by the fields above ──────────────────────────────────

func triStateOptions(name string, current *bool) string {
	yesSel, noSel, unsetSel := "", "", "selected"
	if current != nil {
		if *current {
			yesSel, unsetSel = "selected", ""
		} else {
			noSel, unsetSel = "selected", ""
		}
	}
	return fmt.Sprintf(`
		<select name="%s" style="width:100%%;padding:11px 14px;background:#fff;border:1px solid #d1d5db;border-radius:14px;font-size:14px;box-sizing:border-box;outline:none;">
			<option value="" %s>Prefer not to say</option>
			<option value="true" %s>Yes</option>
			<option value="false" %s>No</option>
		</select>`, name, unsetSel, yesSel, noSel)
}

func scoreSelectOptions(name string, current *int16, min, max int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<select name="%s" style="width:100%%;padding:11px 14px;background:#fff;border:1px solid #d1d5db;border-radius:14px;font-size:14px;box-sizing:border-box;outline:none;">`, name))
	unsetSel := "selected"
	if current != nil {
		unsetSel = ""
	}
	b.WriteString(fmt.Sprintf(`<option value="" %s>Prefer not to say</option>`, unsetSel))
	for i := min; i <= max; i++ {
		sel := ""
		if current != nil && int(*current) == i {
			sel = "selected"
		}
		b.WriteString(fmt.Sprintf(`<option value="%d" %s>%d</option>`, i, sel, i))
	}
	b.WriteString(`</select>`)
	return b.String()
}

func floatOrEmpty(f *float64) string {
	if f == nil {
		return ""
	}
	return strconv.FormatFloat(*f, 'f', -1, 64)
}

func int16OrEmpty(i *int16) string {
	if i == nil {
		return ""
	}
	return strconv.Itoa(int(*i))
}

// parseOptionalBool parses "true"/"false"/"" into *bool (nil = unset).
func parseOptionalBool(v string) *bool {
	switch v {
	case "true":
		b := true
		return &b
	case "false":
		b := false
		return &b
	default:
		return nil
	}
}

// parseOptionalFloat parses a form float field, returns nil if empty or invalid.
func parseOptionalFloat(v string, min, max float64) *float64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < min || f > max {
		return nil
	}
	return &f
}

// parseOptionalInt16 parses a form int field within [min,max], nil otherwise.
func parseOptionalInt16(v string, min, max int) *int16 {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		return nil
	}
	r := int16(n)
	return &r
}

func clampText(s string, max int) string {
	s = html.EscapeString(strings.TrimSpace(s))
	if len(s) > max {
		s = s[:max]
	}
	return s
}

func getProfileImagePreview(path string) string {
	return getAvatarInner(path, "contain")
}

// ── updateProfileHandler ────────────────────────────────────────────────────
func updateProfileHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.SendStatus(401)
	}

	bio := html.EscapeString(strings.TrimSpace(c.FormValue("bio")))
	city := html.EscapeString(c.FormValue("city"))
	gender := html.EscapeString(c.FormValue("gender"))
	birthDate := c.FormValue("birth_date")
	displayName := sanitizeDisplayName(c.FormValue("display_name"))
	if displayNameCollidesWithUsername(displayName, currentUser) {
		// Someone else already owns this exact username (case-insensitive) —
		// refuse to save it as a display name so it can't be used to
		// impersonate them. Falls back to showing their real username.
		displayName = ""
	}
	displayColor := c.FormValue("display_color")
	if _, ok := allowedNameColors[displayColor]; !ok {
		displayColor = ""
	}
	displayFont := c.FormValue("display_font")
	if _, ok := allowedNameFonts[displayFont]; !ok {
		displayFont = ""
	}
	var displayEffectsRaw []string
	for _, b := range c.Context().QueryArgs().PeekMulti("display_effects") {
		displayEffectsRaw = append(displayEffectsRaw, string(b))
	}
	displayEffects := parseDisplayNameEffects(strings.Join(displayEffectsRaw, ","))
	if displayEffects == "" {
		// Fall back to plain FormValue in case PeekMulti misses
		// non-query (multipart form) fields — c.FormValue only returns
		// the first value for a repeated checkbox name, so try that too
		// as a single-value fallback for browsers/setups that only send one.
		displayEffects = parseDisplayNameEffects(c.FormValue("display_effects"))
	}

	if len(bio) > 300 {
		bio = bio[:300]
	}

	var err error
	file, fileErr := c.FormFile("profile_image")
	if fileErr == nil {
		imagePath, upErr := saveUploadedImage(file, currentUser)
		if upErr != nil {
			log.Println("Upload error:", upErr)
			return c.SendString(fmt.Sprintf("<div style='color:#dc2626;padding:16px;'>Error: %s</div>", upErr.Error()))
		}
		_, err = db.Exec(ctx, `
			UPDATE users SET bio=$1, city=$2, gender=$3, birth_date=$4, profile_image=$5,
				display_name=$6, display_name_color=$7, display_name_font=$8, display_name_effects=$9,
				last_profile_image_change=CURRENT_TIMESTAMP
			WHERE username=$10`,
			bio, city, gender, birthDate, imagePath, displayName, displayColor, displayFont, displayEffects, currentUser)
	} else {
		_, err = db.Exec(ctx, `
			UPDATE users SET bio=$1, city=$2, gender=$3, birth_date=$4,
				display_name=$5, display_name_color=$6, display_name_font=$7, display_name_effects=$8
			WHERE username=$9`,
			bio, city, gender, birthDate, displayName, displayColor, displayFont, displayEffects, currentUser)
	}

	if err != nil {
		log.Println("Update error:", err)
		return c.SendString("<div style='color:#dc2626;padding:16px;'>Error updating profile</div>")
	}

	// Invalidate the 90s author-meta cache so the new display name/color/
	// font shows immediately instead of waiting for the next refresh.
	CacheDel(authorMetaCacheKey)

	return profileInfoHandler(c)
}

// ── profileInfoHandler ──────────────────────────────────────────────────────
func profileInfoHandler(c *fiber.Ctx) error {
	username := c.Params("username")
	currentUser := getCurrentUser(c)
	if username == "" {
		username = currentUser
	}

	var userBio, userCity, userGender, userProfileImage, userType string
	var userBirthDate time.Time
	var userPoints int
	var isBanned, isPrivate bool

	err := db.QueryRow(ctx, `
		SELECT COALESCE(bio,''), COALESCE(city,''), COALESCE(gender,''),
		       COALESCE(birth_date,'2000-01-01'::date), COALESCE(points,0),
		       COALESCE(profile_image,''), COALESCE(is_banned,false), COALESCE(is_private,false)
		FROM users WHERE username=$1`, username).
		Scan(&userBio, &userCity, &userGender, &userBirthDate, &userPoints,
			&userProfileImage, &isBanned, &isPrivate)
	if err != nil {
		log.Println("Query error:", err)
		return c.SendString("<div style='color:#dc2626;padding:16px;'>Error loading profile</div>")
	}

	db.QueryRow(ctx, "SELECT user_type FROM users WHERE username=$1", currentUser).Scan(&userType)
	isAdmin := userType == "admin"
	isOwnProfile := currentUser == username

	// Hard block: banned user visible only to admin
	if isBanned && !isAdmin {
		return c.SendString(fmt.Sprintf(`
			<div id="profile-info" style="background:#f9fafb;border-radius:24px;padding:24px;box-shadow:0 1px 3px rgba(0,0,0,0.06);border:1px solid #f3f4f6;">
				%s
			</div>`, renderBanNoticeHTML(username)))
	}

	// Hard block: private profile (not own, not admin, not top-3 scorer)
	if isPrivate && !isOwnProfile && !isAdmin && !isTopThreeScorer(currentUser) {
		var blockedByOwner bool
		db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM blocks WHERE blocker=$1 AND blocked=$2)`,
			username, currentUser).Scan(&blockedByOwner)
		if blockedByOwner {
			return c.SendString(fmt.Sprintf(`
				<div id="profile-info" style="background:#f9fafb;border-radius:24px;padding:24px;box-shadow:0 1px 3px rgba(0,0,0,0.06);border:1px solid #f3f4f6;">
					<div style="text-align:center;padding:40px 20px;background:#fff7ed;border:2px solid #fed7aa;border-radius:32px;">
						<div style="font-size:50px;margin-bottom:15px;">🚷</div>
						<h2 style="color:#c2410c;margin:0;font-size:22px;font-weight:800;">%s ne apko block kia hua hai</h2>
						<p style="color:#fb923c;font-size:14px;margin-top:8px;">Ap yeh profile nahi dekh saktay.</p>
						%s
					</div>
				</div>`, username, renderBlockUnblockButton(currentUser, username)))
		}
		return c.SendString(fmt.Sprintf(`
			<div id="profile-info" style="background:#f9fafb;border-radius:24px;padding:24px;box-shadow:0 1px 3px rgba(0,0,0,0.06);border:1px solid #f3f4f6;">
				<div style="text-align:center;padding:40px 20px;background:#f0f9ff;border:2px solid #bae6fd;border-radius:32px;">
					<div style="font-size:50px;margin-bottom:15px;">🔒</div>
					<h2 style="color:#0369a1;margin:0;font-size:22px;font-weight:800;">Yeh Profile Private Hai</h2>
					<p style="color:#0284c7;font-size:14px;margin-top:8px;">%s ki profile private hai. Sirf woh khud dekh saktay hain.</p>
					%s
				</div>
			</div>`, username, renderBlockUnblockButton(currentUser, username)))
	}

	followersCount, followingCount := getFollowCounts(username)

	userStarSign := calculateStarSign(userBirthDate)
	age := calculateAge(userBirthDate)

	if userBio == "" {
		userBio = "Not set"
	}
	if userCity == "" {
		userCity = "Not set"
	}
	if userGender == "" {
		userGender = "Not set"
	}

	followSectionHTML := RenderFollowSection(currentUser, username, followersCount, followingCount)

	bannedHeader := ""
	if isBanned && isAdmin {
		bannedHeader = `<div style="background-color:#fee2e2;border:1px solid #ef4444;color:#b91c1c;padding:12px;border-radius:16px;margin-bottom:20px;font-weight:bold;text-align:center;">
			ADMIN VIEW: Yeh user ban hai.
		</div>`
	}
	if isPrivate && isAdmin && !isOwnProfile {
		bannedHeader += `<div style="background-color:#fef3c7;border:1px solid #f59e0b;color:#92400e;padding:12px;border-radius:16px;margin-bottom:20px;font-weight:bold;text-align:center;">
			ADMIN VIEW: Yeh profile private hai.
		</div>`
	}

	// Admin-only panel: ban management, dashboard link, points editor, and
	// the Identity Intelligence trigger. Only shown to an admin viewing
	// someone else's profile — this was previously built inside
	// getProfileInfoDisplay, which nothing calls anymore now that the
	// Profile tab lazy-loads via this handler instead.
	adminPanel := ""
	if isAdmin && currentUser != username {
		adminPanel = fmt.Sprintf(`
		<div style="margin-top:24px;padding:20px;border:2px dashed #fca5a5;border-radius:24px;background-color:rgba(254,242,242,0.5);">
			<div style="display:flex;align-items:center;gap:8px;margin-bottom:16px;">
				<span style="width:8px;height:8px;background-color:#ef4444;border-radius:50%%;animation:pulse 2s infinite;"></span>
				<h3 style="margin:0;font-size:10px;font-weight:900;color:#dc2626;text-transform:uppercase;letter-spacing:0.2em;">Admin Command Center</h3>
			</div>
			<div style="display:flex;flex-direction:column;gap:16px;">
							<div style="display:flex;flex-wrap:wrap;gap:12px;">
					<a href="/admin/bans?username=%[1]s"
					        style="display:flex;align-items:center;justify-content:center;background-color:#111827;color:white;padding:12px 20px;border-radius:16px;text-decoration:none;font-size:12px;font-weight:bold;flex:1;">
						🛡️ Manage Ban
					</a>
					<a href="/admin/dashboard"
					        style="display:flex;align-items:center;justify-content:center;background-color:#374151;color:white;padding:12px 20px;border-radius:16px;text-decoration:none;font-size:12px;font-weight:bold;flex:1;">
						📊 Dashboard
					</a>
				</div>
				<div style="display:flex;flex-direction:column;gap:6px;">
					<label style="font-size:9px;font-weight:bold;color:#9ca3af;text-transform:uppercase;margin-left:8px;">Update Points</label>
					<div style="display:flex;gap:8px;">
						<input type="number" id="admin-points-input" value="%[2]d"
						       style="width:100%%;max-width:100px;padding:10px 16px;font-size:14px;border-radius:16px;border:1px solid #e5e7eb;outline:none;background:white;">
						<button onclick="const p=document.getElementById('admin-points-input').value;if(p){htmx.ajax('POST','/admin/set-points/%[1]s',{target:'#profile-info',values:{points:p}})}"
						        style="background-color:#2563eb;color:white;padding:12px 20px;border-radius:16px;border:none;font-size:12px;font-weight:bold;cursor:pointer;flex-grow:1;">
							Set Score
						</button>
					</div>
			</div>
		</div>
	</div>`, username, userPoints)

		adminPanel += renderAdminIdentityTrigger(username)
	}

	result := fmt.Sprintf(`
		<div id="profile-info" style="background:#f9fafb;border-radius:24px;padding:24px;box-shadow:0 1px 3px rgba(0,0,0,0.06);border:1px solid #f3f4f6;">
			%s
			<div style="display:flex;flex-direction:column;gap:24px;">
				<div style="padding-bottom:24px;border-bottom:1px solid #e5e7eb;">
					%s
				</div>
				<div style="display:flex;flex-direction:column;gap:2px;">
					<div style="display:flex;align-items:center;padding:12px 4px;border-bottom:1px solid #e5e7eb;">
						<span style="font-weight:600;color:#6b7280;width:110px;flex-shrink:0;font-size:13px;">City</span>
						<span style="color:#1f2937;flex:1;font-size:14px;">%s</span>
					</div>
					<div style="display:flex;align-items:center;padding:12px 4px;border-bottom:1px solid #e5e7eb;">
						<span style="font-weight:600;color:#6b7280;width:110px;flex-shrink:0;font-size:13px;">Gender</span>
						<span style="color:#1f2937;flex:1;font-size:14px;">%s</span>
					</div>
					<div style="display:flex;align-items:center;padding:12px 4px;border-bottom:1px solid #e5e7eb;">
						<span style="font-weight:600;color:#6b7280;width:110px;flex-shrink:0;font-size:13px;">Age</span>
						<span style="color:#1f2937;flex:1;font-size:14px;">%d years</span>
					</div>
					<div style="display:flex;align-items:center;padding:12px 4px;border-bottom:1px solid #e5e7eb;">
						<span style="font-weight:600;color:#6b7280;width:110px;flex-shrink:0;font-size:13px;">Star Sign</span>
						<span style="color:#1f2937;flex:1;font-size:14px;">%s</span>
					</div>
					<div style="display:flex;align-items:center;padding:12px 4px;border-bottom:1px solid #e5e7eb;">
						<span style="font-weight:600;color:#6b7280;width:110px;flex-shrink:0;font-size:13px;">Points</span>
						<span style="color:#059669;font-weight:700;flex:1;font-size:14px;">%d points</span>
					</div>
					<div style="padding:14px 4px 4px 4px;">
						<span style="font-weight:600;color:#6b7280;display:block;margin-bottom:8px;font-size:13px;">Bio</span>
						<span style="color:#1f2937;word-break:break-word;display:block;font-size:14px;line-height:1.6;">%s</span>
					</div>
				</div>
			</div>
			%s
		</div>`,
		bannedHeader, followSectionHTML, userCity, userGender, age, userStarSign, userPoints, userBio, adminPanel)

	return c.SendString(result)
}

// getFollowCounts fetches follower/following totals once so callers that
// need the number in more than one place (tab labels + the follow-section
// body) don't each issue their own COUNT query.
func getFollowCounts(username string) (followers int, following int) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		db.QueryRow(ctx, "SELECT COUNT(*) FROM followers WHERE followee=$1", username).Scan(&followers)
	}()
	go func() {
		defer wg.Done()
		db.QueryRow(ctx, "SELECT COUNT(*) FROM followers WHERE follower=$1", username).Scan(&following)
	}()
	wg.Wait()
	return
}// ── RenderFollowSection ─────────────────────────────────────────────────────
// Follower/Following counts are clickable stats that switch to the
// dedicated Followers / Following tabs (see profileHandler) instead of
// expanding an inline list — keeps this section compact regardless of
// context (own profile, a stranger's profile, or after a follow/unfollow
// swap of just #follow-section). Counts are passed in (see getFollowCounts)
// so this never re-queries them itself.
func RenderFollowSection(currentUser, profileUser string, followersCount, followingCount int) string {
	statsHTML := fmt.Sprintf(`
		<div style="display:flex;gap:40px;">
			<div style="text-align:center;cursor:pointer;" onclick="var b=document.getElementById('tabbtn-followers'); if(b) b.click();">
				<span style="display:block;font-weight:900;color:#111827;font-size:22px;">%d</span>
				<span style="font-size:10px;text-transform:uppercase;font-weight:700;color:#9ca3af;letter-spacing:0.1em;">Followers</span>
			</div>
			<div style="text-align:center;cursor:pointer;" onclick="var b=document.getElementById('tabbtn-following'); if(b) b.click();">
				<span style="display:block;font-weight:900;color:#111827;font-size:22px;">%d</span>
				<span style="font-size:10px;text-transform:uppercase;font-weight:700;color:#9ca3af;letter-spacing:0.1em;">Following</span>
			</div>
		</div>`, followersCount, followingCount)

	if currentUser == profileUser {
		return fmt.Sprintf(`
		<div id="follow-section" style="display:flex;flex-direction:column;align-items:center;gap:16px;">
			%s
		</div>`, statsHTML)
	}

	if currentUser == "" {
		return fmt.Sprintf(`
		<div id="follow-section" style="display:flex;flex-direction:column;align-items:center;gap:16px;">
			%s
		</div>`, statsHTML)
	}

	var isBanned bool
	if err := db.QueryRow(ctx, "SELECT is_banned FROM users WHERE username=$1", profileUser).Scan(&isBanned); err == nil && isBanned {
		return ""
	}

	var isBlockedByProfileOwner bool
	db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM blocks WHERE blocker=$1 AND blocked=$2)`,
		profileUser, currentUser).Scan(&isBlockedByProfileOwner)
	if isBlockedByProfileOwner {
		return fmt.Sprintf(`
		<div id="follow-section" style="display:flex;flex-direction:column;align-items:center;gap:14px;">
			<span style="color:#ef4444;font-size:13px;font-weight:600;padding:10px 20px;background:#fef2f2;border-radius:14px;text-align:center;border:1px solid #fecaca;">
				%s ne apko block kia hua he
			</span>
		</div>`, profileUser)
	}

	var followCount int
	db.QueryRow(ctx, "SELECT COUNT(*) FROM followers WHERE follower=$1 AND followee=$2",
		currentUser, profileUser).Scan(&followCount)

	var followButton string
	if followCount > 0 {
		followButton = fmt.Sprintf(`
			<button hx-post="/unfollow/%s" hx-target="#follow-section" hx-swap="outerHTML"
				onmouseover="this.style.background='#e5e7eb'" onmouseout="this.style.background='#f3f4f6'"
				onfocus="this.style.outline='2px solid #111827';this.style.outlineOffset='2px'" onblur="this.style.outline='none'"
				style="background:#f3f4f6;color:#1f2937;padding:11px 30px;border-radius:14px;font-weight:700;border:none;cursor:pointer;font-size:14px;transition:background .15s;">
				Unfollow
			</button>`, profileUser)
	} else {
		var requiresApproval bool
		db.QueryRow(ctx, "SELECT COALESCE(require_follow_approval,false) FROM users WHERE username=$1", profileUser).Scan(&requiresApproval)

		if !requiresApproval {
			followButton = fmt.Sprintf(`
				<button hx-post="/follow/%s" hx-target="#follow-section" hx-swap="outerHTML"
					style="background:#059669;color:#fff;padding:11px 30px;border-radius:14px;font-weight:700;border:none;cursor:pointer;font-size:14px;box-shadow:0 6px 16px rgba(5,150,105,0.25);">
					Follow
				</button>`, profileUser)
		} else {
			var reqStatus string
			var resolvedAt *time.Time
			err := db.QueryRow(ctx, `SELECT status, resolved_at FROM follow_requests WHERE requester=$1 AND target=$2`,
				currentUser, profileUser).Scan(&reqStatus, &resolvedAt)

			switch {
			case err == nil && reqStatus == "pending":
				followButton = `
				<button disabled
					style="background:#fef3c7;color:#b45309;padding:11px 30px;border-radius:14px;font-weight:700;border:1px solid #fcd34d;cursor:default;font-size:14px;">
					⏳ Requested
				</button>`
			case err == nil && reqStatus == "rejected" && resolvedAt != nil && time.Since(*resolvedAt) < 24*time.Hour:
				remaining := 24*time.Hour - time.Since(*resolvedAt)
				hoursLeft := int(remaining.Hours()) + 1
				followButton = fmt.Sprintf(`
				<button disabled
					style="background:#f3f4f6;color:#9ca3af;padding:11px 30px;border-radius:14px;font-weight:700;border:1px solid #e5e7eb;cursor:default;font-size:14px;"
					title="Dobara request %dh baad kar sakte hain">
					🚫 Try Again Later
				</button>`, hoursLeft)
			default:
				followButton = fmt.Sprintf(`
				<button hx-post="/follow-request/%s" hx-target="#follow-section" hx-swap="outerHTML"
					style="background:#059669;color:#fff;padding:11px 30px;border-radius:14px;font-weight:700;border:none;cursor:pointer;font-size:14px;box-shadow:0 6px 16px rgba(5,150,105,0.25);">
					🔐 Request to Follow
				</button>`, profileUser)
			}
		}
	}

	// Block / Unblock button — always visible regardless of privacy, unless
	// the profile owner is a top-3 scorer, in which case blocking is disabled.
	var iHaveBlocked bool
	db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM blocks WHERE blocker=$1 AND blocked=$2)`,
		currentUser, profileUser).Scan(&iHaveBlocked)

	var blockButton string
	if !iHaveBlocked && blockProtected(currentUser, profileUser) {
		blockButton = `<span style="background:#f3f4f6;color:#9ca3af;padding:11px 20px;border-radius:14px;font-size:13px;font-weight:700;cursor:not-allowed;" title="Ap is user ko block nahi kar saktay kuinke ye top 3 scorer me se aik hai">🚫 Protected</span>`
	} else {
		blockBtnText := "🚫 Block"
		blockConfirm := fmt.Sprintf("Are you sure you want to block %s?", html.EscapeString(profileUser))
		if iHaveBlocked {
			blockBtnText = "✅ Unblock"
			blockConfirm = fmt.Sprintf("Unblock %s?", html.EscapeString(profileUser))
		}
		blockButton = fmt.Sprintf(
			`<button hx-post="/block-toggle/%s" hx-target="#follow-section" hx-swap="outerHTML" hx-confirm="%s"
				style="background:#fff7ed;color:#c2410c;padding:11px 20px;border-radius:14px;font-weight:700;font-size:13px;border:1px solid #fed7aa;cursor:pointer;">
				%s
			</button>`, profileUser, blockConfirm, blockBtnText)
	}

	chatButton := renderChatButtonInline(currentUser, profileUser)

	return fmt.Sprintf(`
	<div id="follow-section" style="display:flex;flex-direction:column;align-items:center;gap:18px;">
		%s
		<div style="display:flex;flex-wrap:wrap;align-items:center;justify-content:center;gap:10px;width:100%%;">
			%s %s %s
		</div>
	</div>`, statsHTML, followButton, chatButton, blockButton)
}

// renderBlockUnblockButton renders a standalone Block/Unblock control for
// contexts where the full follow section can't be shown — e.g. the private
// profile placeholder, where bio/posts/follow-stats stay hidden but the
// viewer still needs a way to block or undo a block.
func renderBlockUnblockButton(currentUser, profileUser string) string {
	if currentUser == "" || currentUser == profileUser {
		return ""
	}

	var iHaveBlocked bool
	db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM blocks WHERE blocker=$1 AND blocked=$2)`,
		currentUser, profileUser).Scan(&iHaveBlocked)

	if !iHaveBlocked && blockProtected(currentUser, profileUser) {
		return `	<div style="margin-top:16px;display:flex;justify-content:center;">
		<span style="background:#f3f4f6;color:#9ca3af;border:1.5px solid #e5e7eb;padding:10px 20px;border-radius:14px;font-size:13px;font-weight:800;cursor:not-allowed;" title="Ap is user ko block nahi kar saktay kuinke ye top 3 scorer me se aik hai">
			🚫 Protected
		</span>
	</div>`
	}

	btnText := "🚫 Block This User"
	btnConfirm := fmt.Sprintf("Are you sure you want to block %s?", html.EscapeString(profileUser))
	if iHaveBlocked {
		btnText = "✅ Unblock This User"
		btnConfirm = fmt.Sprintf("Unblock %s?", html.EscapeString(profileUser))
	}

	return fmt.Sprintf(`
	<div style="margin-top:16px;display:flex;justify-content:center;">
		<button hx-post="/block-toggle/%s" hx-swap="none" hx-confirm="%s"
		        hx-on::after-request="if(event.detail.successful){ location.reload(); }"
		        style="background:#fff7ed;color:#c2410c;border:1.5px solid #fed7aa;padding:10px 20px;border-radius:14px;font-size:13px;font-weight:800;cursor:pointer;">
			%s
		</button>
	</div>`, profileUser, btnConfirm, btnText)
}

func renderChatButtonInline(currentUser, profileUser string) string {
	if currentUser == "" || currentUser == profileUser {
		return ""
	}

	var isBanned bool
	if err := db.QueryRow(ctx, "SELECT is_banned FROM users WHERE username=$1", profileUser).Scan(&isBanned); err == nil && isBanned {
		return ""
	}

	var blocked bool
	db.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM blocks
		WHERE (blocker=$1 AND blocked=$2) OR (blocker=$2 AND blocked=$1))`,
		currentUser, profileUser).Scan(&blocked)
	if blocked {
		return `<span style="color:#ef4444;font-size:10px;font-weight:800;text-transform:uppercase;letter-spacing:0.05em;padding:5px 10px;background:#fef2f2;border-radius:10px;">Blocked</span>`
	}

	var status string
	err := db.QueryRow(ctx, `
		SELECT status FROM chat_invites
		WHERE (sender=$1 AND recipient=$2) OR (sender=$2 AND recipient=$1)`,
		currentUser, profileUser).Scan(&status)
	if err == nil {
		if status == "accepted" {
			return fmt.Sprintf(`
				<a href="/chat/view/%s" style="display:inline-flex;align-items:center;gap:6px;background:#2563eb;color:#fff;padding:10px 20px;border-radius:14px;font-size:14px;font-weight:700;text-decoration:none;box-shadow:0 6px 16px rgba(37,99,235,0.25);">
					<span>💬</span> View Chat
				</a>`, profileUser)
		}
		return `<span style="background:#f3f4f6;color:#6b7280;font-size:14px;padding:10px 20px;border-radius:14px;font-weight:700;">Invite Pending...</span>`
	}

	return fmt.Sprintf(`
		<button hx-post="/chat/invite/%s" hx-swap="outerHTML"
			style="display:inline-flex;align-items:center;gap:6px;background:rgba(5,150,105,0.1);color:#059669;padding:10px 20px;border-radius:14px;font-size:14px;font-weight:700;border:1px solid rgba(5,150,105,0.2);cursor:pointer;">
			<span>💬</span> 1-on-1
		</button>`, profileUser)
}

func RenderChatButton(currentUser, profileUser string) string {
	return renderChatButtonInline(currentUser, profileUser)
}

// renderProfileSEOTags builds <title>, meta description, canonical, Open Graph,
// Twitter Card, and JSON-LD Person schema for a public, indexable profile.
// Only call this for profiles that are NOT banned and NOT private.
func renderProfileSEOTags(username, bio, city string, points, totalPosts int, profileImage string, joinDate time.Time) string {
	displayBio := bio
	if displayBio == "" || displayBio == "Not set" {
		displayBio = fmt.Sprintf("%s ki Dostana profile — %d points, %d posts. Dostana par judiye aur nayi dosti banaiye.", username, points, totalPosts)
	}
	if len(displayBio) > 160 {
		displayBio = displayBio[:157] + "..."
	}
	metaDesc := html.EscapeString(displayBio)

	pageTitle := fmt.Sprintf("%s — Dostana Profile", username)
	if city != "" && city != "Not set" {
		pageTitle = fmt.Sprintf("%s (%s) — Dostana Profile", username, city)
	}
	pageTitle = html.EscapeString(pageTitle)

	canonicalURL := fmt.Sprintf("https://dostana.biz/profile/%s", username)

	imageURL := profileImage
	if imageURL == "" {
		imageURL = "https://ui-avatars.com/api/?name=" + username + "&background=random&size=512"
	} else if strings.HasPrefix(imageURL, "/") {
		imageURL = "https://dostana.biz" + imageURL
	}

	jsonLD := fmt.Sprintf(`{
		"@context": "https://schema.org",
		"@type": "ProfilePage",
		"dateCreated": "%s",
		"mainEntity": {
			"@type": "Person",
			"name": "%s",
			"alternateName": "@%s",
			"description": "%s",
			"image": "%s",
			"url": "%s"
		}
	}`,
		joinDate.Format(time.RFC3339),
		html.EscapeString(username), html.EscapeString(username),
		metaDesc, imageURL, canonicalURL)

	return fmt.Sprintf(`
	<title>%s</title>
	<meta name="description" content="%s">
	<link rel="canonical" href="%s">
	<meta name="robots" content="index, follow, max-image-preview:large">

	<meta property="og:type" content="profile">
	<meta property="og:title" content="%s">
	<meta property="og:description" content="%s">
	<meta property="og:image" content="%s">
	<meta property="og:image:width" content="512">
	<meta property="og:image:height" content="512">
	<meta property="og:url" content="%s">
	<meta property="og:site_name" content="Dostana">
	<meta property="profile:username" content="%s">

	<meta name="twitter:card" content="summary_large_image">
	<meta name="twitter:title" content="%s">
	<meta name="twitter:description" content="%s">
	<meta name="twitter:image" content="%s">

	<script type="application/ld+json">%s</script>
	`,
		pageTitle, metaDesc, canonicalURL,
		pageTitle, metaDesc, imageURL, canonicalURL, username,
		pageTitle, metaDesc, imageURL,
		jsonLD)
}

func selectedAttr(condition bool) string {
	if condition {
		return "selected"
	}
	return ""
}

// dnEffectsCSSMap builds a JS object literal for the live preview script.
func dnEffectsCSSMap() string {
	var pairs []string
	for _, key := range orderedEffectKeys {
		pairs = append(pairs, fmt.Sprintf(`"%s":"%s"`, key, allowedNameEffects[key]))
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func dnColorsJSMap() string {
	var pairs []string
	for k, v := range allowedNameColors {
		if k == "" {
			continue
		}
		pairs = append(pairs, fmt.Sprintf(`"%s":"%s"`, k, v))
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func dnFontsJSMap() string {
	var pairs []string
	for k, v := range allowedNameFonts {
		if k == "" || v == "" {
			continue
		}
		pairs = append(pairs, fmt.Sprintf(`"%s":"%s"`, k, v))
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func renderEffectCheckboxes(current string) string {
	active := map[string]bool{}
	for _, k := range strings.Split(current, ",") {
		active[strings.TrimSpace(k)] = true
	}
	labels := map[string]string{
		"bold": "Bold", "italic": "Italic", "underline": "Underline",
		"uppercase": "UPPERCASE", "spaced": "S p a c e d", "glow": "✨ Glow",
	}
	var b strings.Builder
	for _, key := range orderedEffectKeys {
		checked := ""
		style := "background:#f3f4f6;color:#374151;"
		if active[key] {
			checked = "checked"
			style = "background:#059669;color:#fff;"
		}
		b.WriteString(fmt.Sprintf(`
		<label class="dn-effect-pill" data-effect="%s" style="cursor:pointer;padding:7px 14px;border-radius:10px;font-size:13px;font-weight:600;%s">
			<input type="checkbox" value="%s" class="dn-effect-checkbox" style="display:none;" %s>
			%s
		</label>`, key, style, key, checked, labels[key]))
	}
	return b.String()
}

// dnLivePreviewScript keeps the Preview box, hidden effects field, and
// swatch/pill active states in sync as the user types/clicks — pure
// client-side convenience; the server re-validates everything on save
// regardless (sanitizeDisplayName / allow-list lookups / length cap).
func dnLivePreviewScript() string {
	return fmt.Sprintf(`
	<script>
	(function(){
		var COLORS = %s;
		var FONTS = %s;
		var EFFECTS = %s;

		var input = document.getElementById('dn-input');
		var count = document.getElementById('dn-count');
		var preview = document.getElementById('dn-preview');
		var hiddenEffects = document.getElementById('dn-effects-hidden');
		if (!input || !preview) return;

		function getSelectedColor(){
			var el = document.querySelector('input[name="display_color"]:checked');
			return el ? el.value : '';
		}
		function getSelectedFont(){
			var el = document.querySelector('input[name="display_font"]:checked');
			return el ? el.value : '';
		}
		function getSelectedEffects(){
			var boxes = document.querySelectorAll('.dn-effect-checkbox:checked');
			var out = [];
			boxes.forEach(function(b){ out.push(b.value); });
			return out;
		}

		function render(){
			var name = input.value || '(your username)';
			count.textContent = input.value.length;

			var color = getSelectedColor();
			var font = getSelectedFont();
			var effects = getSelectedEffects();

			var styleParts = [];
			effects.forEach(function(e){ if (EFFECTS[e]) styleParts.push(EFFECTS[e]); });

			var isRainbow = color === 'rainbow';
			if (!isRainbow && COLORS[color]) styleParts.push('color:' + COLORS[color]);
			if (FONTS[font]) styleParts.push('font-family:' + FONTS[font]);

			if (isRainbow) {
				preview.innerHTML = '<span class="rainbow-name" style="' + styleParts.join(';') + '">' + escapeHtml(name) + '</span>';
			} else {
				preview.innerHTML = '<span style="' + styleParts.join(';') + '">' + escapeHtml(name) + '</span>';
			}

			hiddenEffects.value = effects.join(',');

			// visual "selected" state on effect pills (inline-style toggling)
			document.querySelectorAll('.dn-effect-pill').forEach(function(pill){
				var box = pill.querySelector('.dn-effect-checkbox');
				if (box && box.checked) {
					pill.style.background = '#059669';
					pill.style.color = '#fff';
				} else {
					pill.style.background = '#f3f4f6';
					pill.style.color = '#374151';
				}
			});
		}

		function escapeHtml(s){
			var d = document.createElement('div');
			d.innerText = s;
			return d.innerHTML;
		}

		input.addEventListener('input', render);
		document.querySelectorAll('input[name="display_color"], input[name="display_font"]').forEach(function(el){
			el.addEventListener('change', render);
		});
		document.querySelectorAll('.dn-effect-pill').forEach(function(pill){
			pill.addEventListener('click', function(e){
				e.preventDefault();
				var box = pill.querySelector('.dn-effect-checkbox');
				box.checked = !box.checked;
				render();
			});
		});

		render();
	})();
	</script>`, dnColorsJSMap(), dnFontsJSMap(), dnEffectsCSSMap())
}

func renderColorSwatches(current string) string {
	order := []string{"", "red", "orange", "amber", "emerald", "sky", "blue", "violet", "pink", "gold", "rainbow"}
	var b strings.Builder
	for _, key := range order {
		hex := allowedNameColors[key]
		label := key
		if key == "" {
			label = "Default"
		}
		checked := ""
		ring := "border:2px solid transparent;"
		if key == current {
			checked = "checked"
			ring = "border:2px solid #111827;box-shadow:0 0 0 2px #fff, 0 0 0 4px #111827;"
		}
		swatchStyle := "background:#9ca3af;"
		if hex == "rainbow" {
			swatchStyle = "background:linear-gradient(90deg,#ef4444,#f59e0b,#10b981,#3b82f6,#8b5cf6,#ec4899);"
		} else if hex != "" {
			swatchStyle = "background:" + hex + ";"
		}
		checkmark := ""
		if key == current {
			checkmark = `<span style="position:absolute;inset:0;display:flex;align-items:center;justify-content:center;color:#fff;font-size:12px;font-weight:900;text-shadow:0 1px 2px rgba(0,0,0,.6);pointer-events:none;">✓</span>`
		}
		b.WriteString(fmt.Sprintf(`
		<label style="cursor:pointer;position:relative;display:inline-block;">
			<input type="radio" name="display_color" value="%s" style="display:none;" %s>
			<span style="display:block;width:32px;height:32px;border-radius:50%%;transition:all .15s;%s%s" title="%s"></span>
			%s
		</label>`, key, checked, swatchStyle, ring, label, checkmark))
	}
	return b.String()
}

func renderFontOptions(current string) string {
	order := []string{"", "serif", "mono", "condensed", "boldsans"}
	labels := map[string]string{"": "Default", "serif": "Serif", "mono": "Mono", "condensed": "Condensed", "boldsans": "Bold"}
	var b strings.Builder
	for _, key := range order {
		checked := ""
		style := "background:#f3f4f6;color:#374151;"
		if key == current {
			checked = "checked"
			style = "background:#059669;color:#fff;"
		}
		fam := allowedNameFonts[key]
		fontStyle := ""
		if fam != "" {
			fontStyle = `style="font-family:` + fam + `"`
		}
		b.WriteString(fmt.Sprintf(`
		<label style="cursor:pointer;padding:7px 14px;border-radius:10px;font-size:13px;font-weight:600;%s">
			<input type="radio" name="display_font" value="%s" style="display:none;" %s>
			<span %s>%s</span>
		</label>`, style, key, checked, fontStyle, labels[key]))
	}
	return b.String()
}

// ── renderUserAvatarRow ──────────────────────────────────────────────────
// Left-aligned "user chip" (avatar + username + points, optional trailing
// action) shared by every user list in this file — Blocked, Follow
// Requests, and (by the same pattern) /follow_list Followers/Following.
// Always renders the account's REAL profile_image when one is set, and
// only falls back to a placeholder glyph when it truly has none — this is
// the single source of truth other lists should reuse instead of
// re-implementing their own avatar/alignment markup.
func renderUserAvatarRow(rowID, username, profileImage string, points int, trailing string) string {
	avatar := `<div style="width:42px;height:42px;border-radius:12px;background:#e5e7eb;display:flex;align-items:center;justify-content:center;font-size:17px;flex-shrink:0;">👤</div>`
	if profileImage != "" {
		avatar = fmt.Sprintf(`<img src="%s" alt="%s" style="width:42px;height:42px;border-radius:12px;object-fit:cover;flex-shrink:0;display:block;">`, profileImage, html.EscapeString(username))
	}

	return fmt.Sprintf(`
	<div id="%s" style="display:flex;align-items:center;justify-content:space-between;gap:12px;padding:12px;border-radius:16px;background:#fff;border:1px solid #f3f4f6;text-align:left;">
		<a href="/profile/%s" style="display:flex;align-items:center;gap:10px;min-width:0;flex:1;text-decoration:none;text-align:left;">
			%s
			<div style="min-width:0;text-align:left;">
				<span style="display:block;font-size:14px;font-weight:700;color:#111827;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;text-align:left;">%s</span>
				<span style="display:block;font-size:10px;font-weight:600;color:#6b7280;text-align:left;">%d points</span>
			</div>
		</a>
		%s
	</div>`, rowID, username, avatar, username, points, trailing)
}

// ── renderBlockedUsersSection ───────────────────────────────────────────────
// Renders the list of accounts the given user has blocked, each with a quick
// Unblock action. Used as the content of the dedicated "Blocked" tab
// (owner-only). Mobile-first single column, two columns from ~480px up via
// an auto-fit grid so no media query is needed. Rows are left-aligned and
// always show the real profile picture via renderUserAvatarRow.
func renderBlockedUsersSection(currentUser string) string {
	rows, err := db.Query(ctx, `
		SELECT u.username, COALESCE(u.profile_image,''), COALESCE(u.points,0)
		FROM blocks b
		JOIN users u ON u.username = b.blocked
		WHERE b.blocker = $1
		ORDER BY b.created_at DESC`, currentUser)
	if err != nil {
		log.Println("renderBlockedUsersSection query error:", err)
		return ""
	}
	defer rows.Close()

	var rowsHTML strings.Builder
	count := 0
	for rows.Next() {
		var uname, img string
		var pts int
		if err := rows.Scan(&uname, &img, &pts); err != nil {
			continue
		}
		count++

		unblockBtn := fmt.Sprintf(`
			<button hx-post="/block-toggle/%[1]s" hx-swap="none" hx-confirm="Unblock %[1]s?"
			        hx-on::after-request="if(event.detail.successful){
			            const r=document.getElementById('blocked-row-%[1]s'); if(r) r.remove();
			            const list=document.getElementById('blocked-users-list');
			            const cnt=document.getElementById('blocked-users-count');
			            const empty=document.getElementById('blocked-users-empty');
			            if(cnt && list) cnt.textContent = list.children.length;
			            if(list && list.children.length===0 && empty) empty.style.display='block';
			        }"
			        onmouseover="this.style.background='#e5e7eb'" onmouseout="this.style.background='#f3f4f6'"
			        onfocus="this.style.outline='2px solid #059669';this.style.outlineOffset='2px'" onblur="this.style.outline='none'"
			        style="flex-shrink:0;display:inline-flex;align-items:center;gap:5px;padding:8px 14px;border-radius:10px;font-weight:800;font-size:11px;text-transform:uppercase;letter-spacing:0.03em;background:#f3f4f6;color:#374151;border:none;cursor:pointer;transition:background .15s;">
				✅ Unblock
			</button>`, uname)

		rowsHTML.WriteString(renderUserAvatarRow("blocked-row-"+uname, uname, img, pts, unblockBtn))
	}

	if count == 0 {
		return `
		<div style="text-align:center;padding:48px 20px;color:#6b7280;">
			<div style="font-size:44px;margin-bottom:12px;">🚫</div>
			<p id="blocked-users-empty" style="font-size:13px;font-style:italic;">You haven't blocked anyone.</p>
		</div>`
	}

	return fmt.Sprintf(`
	<div>
		<div style="font-size:12px;font-weight:800;color:#6b7280;text-transform:uppercase;letter-spacing:0.15em;margin-bottom:14px;text-align:left;">
			🚫 Blocked Users (<span id="blocked-users-count">%d</span>)
		</div>
		<div id="blocked-users-list" style="display:grid;grid-template-columns:repeat(auto-fit,minmax(230px,1fr));gap:10px;">
			%s
		</div>
		<p id="blocked-users-empty" style="display:none;font-size:13px;color:#6b7280;font-style:italic;margin-top:12px;">You haven't blocked anyone.</p>
	</div>`, count, rowsHTML.String())
}

// ── renderFollowApprovalToggle / toggleFollowApprovalHandler ───────────────
// A real preference switch — "require approval before someone can follow
// me" — lives here at the top of the dedicated Follow Requests tab, right
// next to the requests it governs, instead of being buried in a generic
// Profile Settings module. This is the piece that used to be missing:
// the app could only ever show pending requests, never let the owner turn
// approval-gating on/off.
func toggleFollowApprovalHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.SendStatus(401)
	}

	var requiresApproval bool
	err := db.QueryRow(ctx, "SELECT COALESCE(require_follow_approval, false) FROM users WHERE username = $1", currentUser).Scan(&requiresApproval)
	if err != nil {
		return c.SendStatus(500)
	}

	newVal := !requiresApproval
	_, err = db.Exec(ctx, "UPDATE users SET require_follow_approval = $1 WHERE username = $2", newVal, currentUser)
	if err != nil {
		return c.SendStatus(500)
	}

	return c.SendString(renderFollowApprovalToggle(newVal))
}

// renderFollowApprovalToggle renders a compact iOS-style switch rather than
// a button, since this is a binary preference, not a one-shot action.
func renderFollowApprovalToggle(requiresApproval bool) string {
	knobLeft, trackBg := "3px", "#e5e7eb"
	if requiresApproval {
		knobLeft, trackBg = "23px", "#059669"
	}
	return fmt.Sprintf(`
	<div id="follow-approval-toggle" style="display:flex;align-items:center;justify-content:space-between;gap:16px;padding:16px 18px;border-radius:16px;background:#f9fafb;border:1px solid #e5e7eb;margin-bottom:18px;text-align:left;">
		<div style="min-width:0;text-align:left;">
			<span style="display:block;font-size:13px;font-weight:700;color:#111827;text-align:left;">Require approval to follow</span>
			<span style="display:block;font-size:11px;color:#6b7280;margin-top:2px;text-align:left;">When on, new followers need your OK before they can follow you</span>
		</div>
		<button hx-post="/toggle-follow-approval" hx-target="#follow-approval-toggle" hx-swap="outerHTML"
			role="switch" aria-checked="%t" aria-label="Require approval to follow"
			onfocus="this.style.outline='2px solid #059669';this.style.outlineOffset='2px'" onblur="this.style.outline='none'"
			style="flex-shrink:0;position:relative;width:44px;height:26px;border-radius:999px;border:none;cursor:pointer;background:%s;transition:background .15s;padding:0;">
			<span style="position:absolute;top:3px;left:%s;width:20px;height:20px;border-radius:50%%;background:#fff;box-shadow:0 1px 3px rgba(0,0,0,0.25);transition:left .15s;"></span>
		</button>
	</div>`, requiresApproval, trackBg, knobLeft)
}

// ── renderFollowRequestsSection ─────────────────────────────────────────────
// Renders the Follow Requests tab: the approval-preference switch above,
// then pending incoming follow requests below, each with Accept/Reject
// actions. This has its own tab so it isn't hidden behind an extra click,
// and Settings stays focused on account controls (password, privacy,
// edit profile).
//
// NOTE: this posts to /follow-request/accept/:username and
// /follow-request/reject/:username. Wire those up alongside the existing
// /follow-request/:username (create) and the new /toggle-follow-approval
// route if they don't already exist.
func renderFollowRequestsSection(currentUser string) string {
	var requiresApproval bool
	db.QueryRow(ctx, "SELECT COALESCE(require_follow_approval,false) FROM users WHERE username=$1", currentUser).Scan(&requiresApproval)
	toggleHTML := renderFollowApprovalToggle(requiresApproval)

	rows, err := db.Query(ctx, `
		SELECT u.username, COALESCE(u.profile_image,''), COALESCE(u.points,0)
		FROM follow_requests fr
		JOIN users u ON u.username = fr.requester
		WHERE fr.target = $1 AND fr.status = 'pending'
		ORDER BY fr.created_at DESC`, currentUser)
	if err != nil {
		log.Println("renderFollowRequestsSection query error:", err)
		return toggleHTML
	}
	defer rows.Close()

	var rowsHTML strings.Builder
	count := 0
	for rows.Next() {
		var uname, img string
		var pts int
		if err := rows.Scan(&uname, &img, &pts); err != nil {
			continue
		}
		count++

		actions := fmt.Sprintf(`
			<div style="display:flex;gap:6px;flex-shrink:0;">
				<button hx-post="/follow-request/accept/%[1]s" hx-swap="none"
				        hx-on::after-request="if(event.detail.successful){
				            const r=document.getElementById('freq-row-%[1]s'); if(r) r.remove();
				            reqPanelSync();
				        }"
				        onmouseover="this.style.background='#047857'" onmouseout="this.style.background='#059669'"
				        onfocus="this.style.outline='2px solid #111827';this.style.outlineOffset='2px'" onblur="this.style.outline='none'"
				        style="padding:8px 14px;border-radius:10px;font-weight:800;font-size:11px;text-transform:uppercase;letter-spacing:0.03em;background:#059669;color:#fff;border:none;cursor:pointer;transition:background .15s;">
					✅ Accept
				</button>
				<button hx-post="/follow-request/reject/%[1]s" hx-swap="none"
				        hx-on::after-request="if(event.detail.successful){
				            const r=document.getElementById('freq-row-%[1]s'); if(r) r.remove();
				            reqPanelSync();
				        }"
				        onmouseover="this.style.background='#e5e7eb'" onmouseout="this.style.background='#f3f4f6'"
				        onfocus="this.style.outline='2px solid #111827';this.style.outlineOffset='2px'" onblur="this.style.outline='none'"
				        style="padding:8px 14px;border-radius:10px;font-weight:800;font-size:11px;text-transform:uppercase;letter-spacing:0.03em;background:#f3f4f6;color:#374151;border:none;cursor:pointer;transition:background .15s;">
					✖ Reject
				</button>
			</div>`, uname)

		rowsHTML.WriteString(renderUserAvatarRow("freq-row-"+uname, uname, img, pts, actions))
	}

	syncScript := `
	<script>
	function reqPanelSync(){
		const list = document.getElementById('follow-requests-list');
		const cnt = document.getElementById('follow-requests-count');
		const empty = document.getElementById('follow-requests-empty');
		const badge = document.getElementById('tabbtn-requests-badge');
		const n = list ? list.children.length : 0;
		if (cnt) cnt.textContent = n;
		if (list && n === 0 && empty) empty.style.display = 'block';
		if (badge) { if (n === 0) { badge.style.display = 'none'; } else { badge.textContent = n; } }
	}
	</script>`

	if count == 0 {
		return toggleHTML + `
		<div style="text-align:center;padding:48px 20px;color:#6b7280;">
			<div style="font-size:44px;margin-bottom:12px;">🔔</div>
			<p id="follow-requests-empty" style="font-size:13px;font-style:italic;">No pending follow requests.</p>
		</div>` + syncScript
	}

	return toggleHTML + fmt.Sprintf(`
	<div>
		<div style="font-size:12px;font-weight:800;color:#6b7280;text-transform:uppercase;letter-spacing:0.15em;margin-bottom:14px;text-align:left;">
			🔔 Pending Follow Requests (<span id="follow-requests-count">%d</span>)
		</div>
		<div id="follow-requests-list" style="display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:10px;">
			%s
		</div>
		<p id="follow-requests-empty" style="display:none;font-size:13px;color:#6b7280;font-style:italic;margin-top:12px;">No pending follow requests.</p>
	</div>%s`, count, rowsHTML.String(), syncScript)
}

// ── tabButtonHTML ────────────────────────────────────────────────────────
// Renders one tab-bar button. Tab switching is pure client-side (toggles
// inline display + inline active colors on the already-rendered #tab-*
// panels — no extra request) except where extraAttrs wires up a lazy
// hx-get load (used by Followers / Following / Posts, which fetch content
// only the first time the tab is opened, or Requests, whose count badge
// also needs a stable id so the accept/reject script above can update it).
func tabButtonHTML(id, label, activeTab string, allIDs []string, extraAttrs string, badgeCount int) string {
	textColor, borderColor := "#6b7280", "transparent"
	if activeTab == id {
		textColor, borderColor = "#059669", "#059669"
	}
	var hideOthers strings.Builder
	for _, other := range allIDs {
		if other == id {
			continue
		}
		hideOthers.WriteString(fmt.Sprintf("var e%[1]s=document.getElementById('tab-%[1]s'); if(e%[1]s) e%[1]s.style.display='none';", other))
	}
	onclick := fmt.Sprintf(`
		var cur=document.getElementById('tab-%[1]s'); if(cur) cur.style.display='block';
		%[2]s
		document.querySelectorAll('.dostana-tab-btn').forEach(function(b){
			b.style.color='#6b7280'; b.style.borderBottomColor='transparent';
		});
		this.style.color='#059669'; this.style.borderBottomColor='#059669';
	`, id, hideOthers.String())

	badge := ""
	badgeDisplay := "display:none;"
	if badgeCount > 0 {
		badgeDisplay = ""
	}
	badge = fmt.Sprintf(`<span id="tabbtn-%s-badge" style="%sbackground:#dc2626;color:#fff;font-size:9px;font-weight:900;padding:1px 6px;border-radius:999px;margin-left:5px;vertical-align:2px;">%d</span>`, id, badgeDisplay, badgeCount)

	return fmt.Sprintf(`
		<button type="button" id="tabbtn-%s" onclick="%s" %s
			class="dostana-tab-btn"
			onfocus="this.style.outline='2px solid #059669';this.style.outlineOffset='-2px'" onblur="this.style.outline='none'"
			style="flex:0 0 auto;padding:14px 16px;font-size:12px;font-weight:800;text-transform:uppercase;letter-spacing:0.04em;border:none;background:transparent;border-bottom:3px solid %s;color:%s;cursor:pointer;white-space:nowrap;transition:color .15s,border-color .15s;">
			%s%s
		</button>`, id, onclick, extraAttrs, borderColor, textColor, label, badge)
}

// hiddenStyle returns the inline "display:none;" needed on a tab panel
// that isn't the active tab, or "" (block, the default) when it is.
func hiddenStyle(id, activeTab string) string {
	if activeTab == id {
		return "display:block;"
	}
	return "display:none;"
}

// ── Post model + shared query/render helpers ────────────────────────────────

type postRow struct {
	id, authorPoints, dpCompID                    int
	author, content, img, cat, voice              string
	createdAt                                     time.Time
	allowJ, allowC, allowComments, followersOnly  bool
	expiresAt                                     *time.Time
}

const postSelectCols = `p.id, p.author, p.content, p.created_at, p.allow_jhappi, p.allow_chupair,
	COALESCE(p.image_url,''), COALESCE(p.category,'General'), u.points, COALESCE(p.voice_url,''),
	COALESCE(p.allow_comments, true), COALESCE(p.followers_only, false), p.expires_at,
	COALESCE(p.dp_competition_id, 0)`

func scanPostRow(rows interface {
	Scan(dest ...interface{}) error
}) (postRow, error) {
	var r postRow
	err := rows.Scan(&r.id, &r.author, &r.content, &r.createdAt, &r.allowJ, &r.allowC,
		&r.img, &r.cat, &r.authorPoints, &r.voice, &r.allowComments, &r.followersOnly,
		&r.expiresAt, &r.dpCompID)
	return r, err
}

func renderPosts(rows []postRow, currentUser string) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(`<div class="post-item" data-post-id="`)
		b.WriteString(strconv.Itoa(r.id))
		b.WriteString(`" style="margin:0;">`)
		b.WriteString(renderPostHTML(r.id, r.author, r.content, r.createdAt, currentUser, r.authorPoints,
			r.allowJ, r.allowC, r.img, r.cat, r.voice, r.allowComments, r.followersOnly, r.expiresAt, r.dpCompID))
		b.WriteString(`</div>`)
	}
	return b.String()
}

const trendingConditionBare = `is_trending = true
	AND created_at > NOW() - INTERVAL '7 days'
	AND LOWER(COALESCE(category,'General')) != 'general'
	AND COALESCE(category,'') != ''
	AND (
	      (COALESCE(image_url,'') != '')
	      OR
	      (COALESCE(image_url,'') = '' AND CHAR_LENGTH(TRIM(content)) >= 80)
	    )`

// filterWhereClause returns the extra WHERE fragment (aliased to p.) and
// whether this filter is ordered by trending score rather than by id —
// cursor-based (id) pagination only applies to the non-trending filters.
func filterWhereClause(filter string) (clause string, orderByTrending bool) {
	const sourceClause = " AND p.source = 'share'"
	switch filter {
	case "pics":
		return sourceClause + " AND p.image_url IS NOT NULL AND p.image_url != '' AND p.image_url !~* '\\.(mp4|webm|mov|mkv)$'", false
	case "videos":
		return sourceClause + " AND p.image_url ~* '\\.(mp4|webm|mov|mkv)$'", false
	case "text":
		return sourceClause + " AND (p.image_url IS NULL OR p.image_url = '')", false
	case "trending":
		return sourceClause + " AND p." + trendingConditionBare, true
	default:
		return sourceClause, false
	}
}

// postFilterCounts holds the per-filter totals shown inline on the filter
// pills. Computed with one aggregated query (conditional COUNT FILTERs)
// instead of five separate COUNT(*) round-trips.
type postFilterCounts struct {
	All, Text, Pics, Videos, Trending int
}

func getPostFilterCounts(username string) postFilterCounts {
	var c postFilterCounts
	db.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE image_url IS NULL OR image_url = ''),
			COUNT(*) FILTER (WHERE image_url IS NOT NULL AND image_url != '' AND image_url !~* '\.(mp4|webm|mov|mkv)$'),
			COUNT(*) FILTER (WHERE image_url ~* '\.(mp4|webm|mov|mkv)$'),
			COUNT(*) FILTER (WHERE `+trendingConditionBare+`)
		FROM posts WHERE author=$1 AND source = 'share'`, username).
		Scan(&c.All, &c.Text, &c.Pics, &c.Videos, &c.Trending)
	return c
}

// queryPostsInitial fetches the first batch for a tab/filter switch, newest
// first. hasMore tells the caller whether to render a bottom "load more"
// sentinel.
func queryPostsInitial(username, filter string, limit int) (rows []postRow, hasMore bool) {
	clause, trending := filterWhereClause(filter)
	order := "ORDER BY p.id DESC"
	if trending {
		order = `ORDER BY CASE WHEN COALESCE(p.image_url,'') != '' THEN 0 ELSE 1 END, p.trending_score DESC, p.created_at DESC`
	}
	q := `SELECT ` + postSelectCols + ` FROM posts p JOIN users u ON p.author = u.username
		WHERE p.author=$1` + clause + ` ` + order + ` LIMIT $2`
	dbRows, err := db.Query(ctx, q, username, limit+1)
	if err != nil {
		return nil, false
	}
	defer dbRows.Close()
	for dbRows.Next() {
		r, err := scanPostRow(dbRows)
		if err != nil {
			continue
		}
		rows = append(rows, r)
	}
	if len(rows) > limit {
		rows = rows[:limit]
		hasMore = true
	}
	return
}

// queryHomeChatPosts fetches Quick Thought posts (posted from the Home
// composer) for the dedicated "Home Chat" profile tab — kept fully
// separate from the Posts tab, which only shows posts made via /share.
func queryHomeChatPosts(username string, limit int) (rows []postRow) {
	q := `SELECT ` + postSelectCols + ` FROM posts p JOIN users u ON p.author = u.username
		WHERE p.author=$1 AND p.source = 'quickthought' ORDER BY p.id DESC LIMIT $2`
	dbRows, err := db.Query(ctx, q, username, limit)
	if err != nil {
		return nil
	}
	defer dbRows.Close()
	for dbRows.Next() {
		r, err := scanPostRow(dbRows)
		if err != nil {
			continue
		}
		rows = append(rows, r)
	}
	return
}

// queryPostsOlder ("scroll down" / load more) fetches posts with id below
// the given cursor — an indexed range scan (WHERE id < $cursor ORDER BY id
// DESC LIMIT n), which stays fast at any scroll depth, unlike OFFSET
// pagination which gets slower the further a user pages in. Not available
// for the trending filter (ordered by score, not id).
func queryPostsOlder(username, filter string, cursorID, limit int) (rows []postRow, hasMore bool) {
	clause, trending := filterWhereClause(filter)
	if trending {
		return nil, false
	}
	q := `SELECT ` + postSelectCols + ` FROM posts p JOIN users u ON p.author = u.username
		WHERE p.author=$1 AND p.id < $2` + clause + ` ORDER BY p.id DESC LIMIT $3`
	dbRows, err := db.Query(ctx, q, username, cursorID, limit+1)
	if err != nil {
		return nil, false
	}
	defer dbRows.Close()
	for dbRows.Next() {
		r, err := scanPostRow(dbRows)
		if err != nil {
			continue
		}
		rows = append(rows, r)
	}
	if len(rows) > limit {
		rows = rows[:limit]
		hasMore = true
	}
	return
}

// queryPostsNewer ("scroll up" past the trimmed window) fetches posts with
// id above the given cursor, oldest-of-the-batch first so the caller can
// prepend them in the right newest-at-top order.
func queryPostsNewer(username, filter string, cursorID, limit int) (rows []postRow, hasMore bool) {
	clause, trending := filterWhereClause(filter)
	if trending {
		return nil, false
	}
	q := `SELECT ` + postSelectCols + ` FROM posts p JOIN users u ON p.author = u.username
		WHERE p.author=$1 AND p.id > $2` + clause + ` ORDER BY p.id ASC LIMIT $3`
	dbRows, err := db.Query(ctx, q, username, cursorID, limit+1)
	if err != nil {
		return nil, false
	}
	defer dbRows.Close()
	for dbRows.Next() {
		r, err := scanPostRow(dbRows)
		if err != nil {
			continue
		}
		rows = append(rows, r)
	}
	if len(rows) > limit {
		rows = rows[:limit]
		hasMore = true
	}
	// reverse so the batch is newest-first internally, matching how it will
	// sit once prepended above the current top of the list
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return
}

func bottomSentinelHTML(username, filter string, cursorID int) string {
	return fmt.Sprintf(`<div id="posts-bottom-sentinel"
		hx-get="/posts-load-more/%s?filter=%s&after_id=%d"
		hx-trigger="revealed" hx-swap="outerHTML" hx-target="this"
		style="padding:18px 0;text-align:center;color:#c7cdd6;font-size:11px;">Loading more…</div>`,
		username, filter, cursorID)
}

func topSentinelHTML(username, filter string, cursorID int) string {
	return fmt.Sprintf(`<div id="posts-top-sentinel"
		hx-get="/posts-load-more/%s?filter=%s&before_id=%d"
		hx-trigger="revealed" hx-swap="outerHTML" hx-target="this"
		style="padding:6px 0;text-align:center;color:transparent;font-size:1px;">&nbsp;</div>`,
		username, filter, cursorID)
}

// dostanaScrollTrimScript caps how many post nodes stay mounted at once —
// once the on-screen list grows past postsDomWindowCap it trims the far
// end opposite whichever sentinel just fired, so a long infinite-scroll
// session never bogs the page down. Trimming from the bottom always keeps
// the (freshly re-added) bottom sentinel; trimming from the top always
// keeps the top sentinel, and re-adds a top sentinel with an updated
// cursor if one isn't already present so scrolling back up still works.
func dostanaScrollTrimScript(cap int) string {
	return fmt.Sprintf(`
	<script>
	(function(){
		var container = document.getElementById('posts-container');
		if (!container || container.dataset.trimWired) return;
		container.dataset.trimWired = '1';
		document.body.addEventListener('htmx:afterSwap', function(evt){
			var id = evt.detail && evt.detail.target && evt.detail.target.id;
			if (id !== 'posts-bottom-sentinel' && id !== 'posts-top-sentinel') return;
			var items = container.querySelectorAll('.post-item');
			if (items.length <= %d) return;
			var excess = items.length - %d;
			if (id === 'posts-bottom-sentinel') {
				// grew at the bottom — trim oldest-mounted from the top,
				// leaving the top sentinel (or inserting one) so scrolling
				// back up still re-loads what was trimmed.
				for (var i = 0; i < excess; i++) { items[i].remove(); }
				var firstLeft = container.querySelector('.post-item');
				if (firstLeft && !document.getElementById('posts-top-sentinel')) {
					var s = document.createElement('div');
					s.outerHTML = ''; // placeholder, server renders real sentinel on next load
				}
			} else {
				// grew at the top — trim newest-mounted-so-far from the
				// bottom, leaving the bottom sentinel intact.
				for (var i = 0; i < excess; i++) { items[items.length - 1 - i].remove(); }
			}
		});
	})();
	</script>`, cap, cap)
}

// ── posts tab: filter switch / initial tab load ─────────────────────────────
// Serves just the filter bar + first batch of posts + sentinel, so clicking
// a filter (or opening the Posts tab) swaps a small fragment instead of
// reloading the whole profile page — this is what makes filtering fast.
func postsTabHandler(c *fiber.Ctx) error {
	username := c.Params("username")
	currentUser := getCurrentUser(c)
	filter := c.Query("filter", "all")

	rows, hasMore := queryPostsInitial(username, filter, postsInitialBatch)
	counts := getPostFilterCounts(username)

	body := renderPostsTabBody(username, filter, currentUser, rows, hasMore, counts)
	return c.SendString(body)
}

// postsLoadMoreHandler serves one more batch for infinite scroll — either
// older posts (after_id, scrolling down) or newer posts previously trimmed
// from the DOM (before_id, scrolling back up).
func postsLoadMoreHandler(c *fiber.Ctx) error {
	username := c.Params("username")
	currentUser := getCurrentUser(c)
	filter := c.Query("filter", "all")

	if afterStr := c.Query("after_id"); afterStr != "" {
		afterID, _ := strconv.Atoi(afterStr)
		rows, hasMore := queryPostsOlder(username, filter, afterID, postsLoadMoreBatch)
		html := renderPosts(rows, currentUser)
		if hasMore && len(rows) > 0 {
			html += bottomSentinelHTML(username, filter, rows[len(rows)-1].id)
		}
		return c.SendString(html)
	}

	if beforeStr := c.Query("before_id"); beforeStr != "" {
		beforeID, _ := strconv.Atoi(beforeStr)
		rows, hasMore := queryPostsNewer(username, filter, beforeID, postsLoadMoreBatch)
		html := renderPosts(rows, currentUser)
		if hasMore && len(rows) > 0 {
			html = topSentinelHTML(username, filter, rows[0].id) + html
		}
		return c.SendString(html)
	}

	return c.SendString("")
}

func renderPostsTabBody(username, filter, currentUser string, rows []postRow, hasMore bool, counts postFilterCounts) string {
	filterStyle := func(active bool) string {
		if active {
			return "background:#059669;color:#fff;box-shadow:0 4px 10px rgba(5,150,105,0.25);"
		}
		return "background:#f3f4f6;color:#374151;"
	}
	trendActiveStyle := func(active bool) string {
		if active {
			return "background:linear-gradient(135deg,#f97316,#ef4444);color:#fff;box-shadow:0 4px 10px rgba(239,68,68,0.25);"
		}
		return "background:#f3f4f6;color:#374151;"
	}
	// pillBtn renders one filter chip with its label and an exact-count
	// badge beside it, styled like the red count badge on the tab bar
	// (tabButtonHTML above) — a solid rounded pill rather than a plain
	// number — so the two "count" affordances in this UI look related.
	pillBtn := func(f, label string, n int, style string, active bool) string {
		return fmt.Sprintf(`
		<button type="button" hx-get="/posts-tab/%s?filter=%s" hx-target="#posts-tab-body" hx-swap="innerHTML"
			onmouseover="this.style.filter='brightness(0.96)'" onmouseout="this.style.filter='none'"
			onfocus="this.style.outline='2px solid #111827';this.style.outlineOffset='2px'" onblur="this.style.outline='none'"
			style="flex-shrink:0;display:inline-flex;align-items:center;gap:6px;padding:8px 14px;border-radius:12px;font-size:12px;font-weight:700;border:none;cursor:pointer;transition:filter .15s;white-space:nowrap;%s">
			<span>%s</span>
			<span style="background:#dc2626;color:#fff;font-size:9px;font-weight:900;padding:1px 6px;border-radius:999px;line-height:1.6;">%d</span>
		</button>`, username, f, style, label, n)
	}

	filterBar := fmt.Sprintf(`
	<div style="display:flex;align-items:center;gap:6px;margin-bottom:0;flex-wrap:nowrap;overflow-x:auto;-webkit-overflow-scrolling:touch;scrollbar-width:none;padding:0 16px 4px 16px;margin-left:-16px;margin-right:-16px;">
		%s%s%s%s%s
	</div>`,
		pillBtn("all", "All", counts.All, filterStyle(filter == "all" || filter == ""), filter == "all" || filter == ""),
		pillBtn("text", "📝 Text", counts.Text, filterStyle(filter == "text"), filter == "text"),
		pillBtn("pics", "🖼️ Pics", counts.Pics, filterStyle(filter == "pics"), filter == "pics"),
		pillBtn("videos", "🎥 Videos", counts.Videos, filterStyle(filter == "videos"), filter == "videos"),
		pillBtn("trending", "🔥 Trending", counts.Trending, trendActiveStyle(filter == "trending"), filter == "trending"))

	sentinel := ""
	if hasMore && len(rows) > 0 {
		sentinel = bottomSentinelHTML(username, filter, rows[len(rows)-1].id)
	}

	empty := ""
	if len(rows) == 0 {
		empty = `<div style="text-align:center;padding:48px 20px;color:#6b7280;">
			<div style="font-size:44px;margin-bottom:12px;">📭</div>
			<p style="font-size:13px;">No posts match this filter yet.</p>
		</div>`
	}

	return fmt.Sprintf(`
	<div id="posts-tab-body">
		%s
		<div id="posts-container" style="display:flex;flex-direction:column;gap:2px;width:calc(100%% + 32px);margin-left:-16px;margin-right:-16px;">
			%s
			%s
		</div>
		%s
	</div>%s`, filterBar, renderPosts(rows, currentUser), sentinel, empty, dostanaScrollTrimScript(postsDomWindowCap))
}

// ── profileHandler (main page) ──────────────────────────────────────────────
func profileHandler(c *fiber.Ctx) error {
	usernameParam := c.Params("username")
	currentUser := getCurrentUser(c)
	activeTab := c.Query("tab", "posts") // "profile" | "posts" | "games" | "mehfils" | "followers" | "following" | "blocked" | "requests" | "settings"

	if usernameParam == "" {
		if currentUser == "" {
			return c.Redirect("/signup")
		}
		usernameParam = currentUser
	}

	// ── Security checks ──────────────────────────────────────────────────────
	var isBanned, isPrivate bool
	var viewerType string
	var userBio, userCity, userGender, userProfileImage string
	var userBirthDate time.Time
	var userPoints, totalPosts int
	var joinDate time.Time
	if err := db.QueryRow(ctx, `
		SELECT COALESCE(is_banned,false), COALESCE(is_private,false),
		       COALESCE(bio,''), COALESCE(city,''), COALESCE(gender,''),
		       COALESCE(birth_date,'2000-01-01'::date), COALESCE(points,0),
		       COALESCE(profile_image,''), created_at,
		       (SELECT COUNT(*) FROM posts WHERE author=$1)
		FROM users WHERE username=$1`, usernameParam).
		Scan(&isBanned, &isPrivate, &userBio, &userCity, &userGender, &userBirthDate,
			&userPoints, &userProfileImage, &joinDate, &totalPosts); err != nil {
		log.Println("User data query error:", err)
	}
	db.QueryRow(ctx, "SELECT user_type FROM users WHERE username=$1", currentUser).Scan(&viewerType)
	isAdmin := viewerType == "admin"
	isOwnProfile := currentUser == usernameParam

	// Settings and Blocked and Requests tabs only make sense for the owner —
	// fall back to the profile tab if a non-owner somehow lands here with
	// that ?tab=.
	if (activeTab == "settings" || activeTab == "blocked" || activeTab == "requests") && !isOwnProfile {
		activeTab = "profile"
	}
	validTabs := map[string]bool{
		"profile": true, "posts": true, "homechat": true, "games": true, "mehfils": true,
		"followers": true, "following": true, "blocked": true, "requests": true, "settings": true,
	}
	if !validTabs[activeTab] {
		activeTab = "posts"
	}

	// Hard block: banned
	if isBanned && !isAdmin {
		c.Set("X-Robots-Tag", "noindex, nofollow")
		bannedUI := fmt.Sprintf(`
		<div style="background:#fff;border-radius:24px;box-shadow:0 10px 40px rgba(0,0,0,0.08);padding:40px;border:1px solid #f3f4f6;text-align:center;">
			<div style="font-size:70px;margin-bottom:20px;">🚫</div>
			<h1 style="font-size:28px;font-weight:900;color:#111827;margin-bottom:8px;">%s</h1>
			%s
		</div>`, usernameParam, renderBanNoticeHTML(usernameParam))
		c.Set("Content-Type", "text/html")
		return c.SendString(Layout("Banned • "+usernameParam, bannedUI, currentUser))
	}

	// Hard block: viewer is blocked by profile owner
	if !isOwnProfile && !isAdmin && currentUser != "" {
		var blockedByOwner bool
		db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM blocks WHERE blocker=$1 AND blocked=$2)`,
			usernameParam, currentUser).Scan(&blockedByOwner)
		if blockedByOwner {
			c.Set("X-Robots-Tag", "noindex, nofollow")
			blockedUI := fmt.Sprintf(`
			<div style="background:#fff;border-radius:24px;box-shadow:0 10px 40px rgba(0,0,0,0.08);padding:40px;border:1px solid #f3f4f6;text-align:center;">
				<div style="font-size:70px;margin-bottom:20px;">🚷</div>
				<h1 style="font-size:28px;font-weight:900;color:#111827;margin-bottom:8px;">%s</h1>
				<div style="background:#fff7ed;border:2px solid #fed7aa;border-radius:24px;padding:32px;margin-top:24px;">
					<h2 style="color:#c2410c;margin:0;font-size:22px;font-weight:800;">Apko Block Kia Gaya Hai</h2>
					<p style="color:#fb923c;font-size:14px;margin-top:8px;">%s ne apko block kar diya hai, is liye ap yeh profile nahi dekh saktay.</p>
				</div>
			</div>`, usernameParam, usernameParam)
			c.Set("Content-Type", "text/html")
			return c.SendString(Layout("Blocked • "+usernameParam, blockedUI, currentUser))
		}
	}

	// Hard block: private profile — but NOT if the viewer is the one who blocked the owner
	var viewerHasBlockedOwner bool
	if currentUser != "" && !isOwnProfile {
		db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM blocks WHERE blocker=$1 AND blocked=$2)`,
			currentUser, usernameParam).Scan(&viewerHasBlockedOwner)
	}
	viewerIsTopThree := isTopThreeScorer(currentUser)
	if isPrivate && !isOwnProfile && !isAdmin && !viewerHasBlockedOwner && !viewerIsTopThree {
		c.Set("X-Robots-Tag", "noindex, nofollow")
		privateUI := fmt.Sprintf(`
		<div style="background:#fff;border-radius:24px;box-shadow:0 10px 40px rgba(0,0,0,0.08);padding:40px;border:1px solid #f3f4f6;text-align:center;">
			<div style="font-size:70px;margin-bottom:20px;">🔒</div>
			<h1 style="font-size:28px;font-weight:900;color:#111827;margin-bottom:8px;">%s</h1>
			<div style="background:#eff6ff;border:2px solid #bfdbfe;border-radius:24px;padding:32px;margin-top:24px;">
				<h2 style="color:#1d4ed8;margin:0;font-size:22px;font-weight:800;">Yeh Profile Private Hai</h2>
				<p style="color:#3b82f6;font-size:14px;margin-top:8px;">Is user ki profile private hai. Sirf woh khud dekh saktay hain.</p>
			</div>
			%s
		</div>`, usernameParam, renderBlockUnblockButton(currentUser, usernameParam))
		c.Set("Content-Type", "text/html")
		return c.SendString(Layout("Private • "+usernameParam, privateUI, currentUser))
	}

	// ── Normal data fetch ────────────────────────────────────────────────────
	followersCount, followingCount := getFollowCounts(usernameParam)

	// ── Posts tab: first batch only, rendered server-side for a fast first
	// paint (and so search engines see real post content); everything past
	// that loads via infinite scroll (see postsLoadMoreHandler), and
	// switching filters swaps just this fragment via postsTabHandler instead
	// of reloading the page. ──────────────────────────────────────────────
	postFilter := c.Query("filter", "all")
	postRows, postsHasMore := queryPostsInitial(usernameParam, postFilter, postsInitialBatch)
	filterCounts := getPostFilterCounts(usernameParam)
	postsTabBodyHTML := renderPostsTabBody(usernameParam, postFilter, currentUser, postRows, postsHasMore, filterCounts)

	// ── Games tab content — DP Battles this user has started, plus a CTA to
	// start a new one. Kept out of the Posts tab entirely so posts stay
	// focused on ordinary content. ──────────────────────────────────────────
	// Games tab is lazy-loaded on click (see gamesTabHandler / /games-tab/:username)
	// instead of being queried on every profile page load.
	gamesTabHTML := `<div style="min-height:80px;text-align:center;padding:32px;color:#6b7280;font-size:13px;">Loading…</div>`
	// ── Home Chat tab content — Quick Thought posts made from the Home
	// composer, kept fully separate from the Posts tab (which only shows
	// posts made via the dedicated /share page). ───────────────────────────
	homeChatTabHTML := `<div style="min-height:80px;text-align:center;padding:32px;color:#6b7280;font-size:13px;">Loading…</div>`

	// ── Mehfils tab content — computed unconditionally now (independent tab) ─
	// Mehfils tab is lazy-loaded on click (see mehfilsTabHandler / /mehfils-tab/:username).
	mehfilsTabHTML := `<div style="min-height:80px;text-align:center;padding:32px;color:#6b7280;font-size:13px;">Loading…</div>`
	// ── Followers / Following tabs — lazily loaded (fetched once, the first
	// time the tab is opened) via the existing /follow_list endpoint. The
	// hx-target is the tab panel itself (#tab-followers / #tab-following),
	// NOT a nested wrapper div — hx-swap="innerHTML" replaces only the
	// panel's contents and leaves the panel's own id/style (used by the
	// tab show/hide script above) untouched, so the extra wrapper div that
	// used to sit here purely to have "a" target id is unnecessary.
	// IMPORTANT: the /follow_list handler (not in this file) should render
	// its rows with the same renderUserAvatarRow pattern used above for
	// Blocked / Follow Requests, so every user list in the app shares one
	// left-aligned, real-avatar row style. ──────────────────────────────
	followersTabHTML := `<div style="min-height:80px;text-align:center;padding:32px;color:#6b7280;font-size:13px;">Loading followers…</div>`
	followingTabHTML := `<div style="min-height:80px;text-align:center;padding:32px;color:#6b7280;font-size:13px;">Loading following…</div>`

	postCountDisplay := fmt.Sprintf("%d", totalPosts)
	if totalPosts > 9999 {
		postCountDisplay = "9999+"
	}

	// ── Blocked tab content (owner only) ──────────────────────────────────────
	blockedTabHTML := ""
	if isOwnProfile {
		blockedTabHTML = renderBlockedUsersSection(currentUser)
	}

	// ── Follow Requests tab content (owner only) — approval-preference
	// switch + pending requests list, see renderFollowRequestsSection. ─────
	requestsTabHTML := ""
	var pendingFollowReqCount int
	if isOwnProfile {
		db.QueryRow(ctx, "SELECT COUNT(*) FROM follow_requests WHERE target=$1 AND status='pending'", currentUser).Scan(&pendingFollowReqCount)
		requestsTabHTML = renderFollowRequestsSection(currentUser)
	}

	// ── Settings tab content (owner only). Ordered by how often each
	// action is actually used: Edit Profile (most frequent) first, the
	// public/private toggle next (occasional, high-consequence), and
	// Change Password last (rare). The old generic "Profile Settings"
	// button and the follow-request shortcut have both been removed —
	// follow requests now live in their own Requests tab above. ────────
	settingsTabHTML := ""
	if isOwnProfile {
		var isPrivateCurrent bool
		db.QueryRow(ctx, "SELECT COALESCE(is_private,false) FROM users WHERE username=$1", currentUser).Scan(&isPrivateCurrent)

		editProfileBtn := `
			<button hx-get="/edit-profile-form"
			        hx-target="#profile-info"
			        hx-swap="innerHTML"
			        onclick="var b=document.getElementById('tabbtn-profile'); if(b) b.click();"
			        onmouseover="this.style.background='#a7f3d0'" onmouseout="this.style.background='#d1fae5'"
			        onfocus="this.style.outline='2px solid #047857';this.style.outlineOffset='2px'" onblur="this.style.outline='none'"
			        style="display:flex;align-items:center;gap:10px;background:#d1fae5;color:#047857;padding:14px 20px;border-radius:16px;font-size:13px;font-weight:700;border:none;cursor:pointer;text-align:left;transition:background .15s;">
				✏️ Edit Profile
			</button>`

		changePasswordLink := `
			<a href="/change-password"
			   onmouseover="this.style.background='#e5e7eb'" onmouseout="this.style.background='#f3f4f6'"
			   onfocus="this.style.outline='2px solid #111827';this.style.outlineOffset='2px'" onblur="this.style.outline='none'"
			   style="display:flex;align-items:center;gap:10px;background:#f3f4f6;color:#374151;padding:14px 20px;border-radius:16px;font-size:13px;font-weight:700;text-decoration:none;transition:background .15s;">
				🔑 Change Password
			</a>`

		settingsTabHTML = fmt.Sprintf(`
		<div style="display:flex;flex-direction:column;gap:12px;max-width:420px;margin:0 auto;">
			%s
			%s
			%s
		</div>`, editProfileBtn, renderPrivacyToggleButton(isPrivateCurrent), changePasswordLink)
	}

	// ── Admin warning banner (top of page, outside tabs) ──────────────────────
	adminWarning := ""
	if isBanned && isAdmin {
		adminWarning = `<div style="background-color:#fee2e2;border:1px solid #ef4444;color:#b91c1c;padding:12px;border-radius:16px;margin-bottom:20px;font-weight:bold;text-align:center;">
			ADMIN VIEW: Yeh user ban hai. Sirf apko details nazar aa rahi hain.
		</div>`
	}
	if isPrivate && isAdmin && !isOwnProfile {
		adminWarning += `<div style="background-color:#fef3c7;border:1px solid #f59e0b;color:#92400e;padding:12px;border-radius:16px;margin-bottom:20px;font-weight:bold;text-align:center;">
			ADMIN VIEW: Yeh profile private hai.
		</div>`
	}
	if isPrivate && !isOwnProfile && !isAdmin && viewerIsTopThree {
		adminWarning += `<div style="background-color:#f5f3ff;border:1px solid #8b5cf6;color:#6d28d9;padding:12px;border-radius:16px;margin-bottom:20px;font-weight:bold;text-align:center;">
			🏆 Ap top 3 scorer me se aik hain, isi liye ap ye private profile bhi dekh sakte hain.
		</div>`
	}

	// ── Tab switcher (Profile / Posts / Games / Mehfils / Followers / Following / Blocked / Requests / Settings) ─
	allTabIDs := []string{"profile", "posts", "homechat", "games", "mehfils", "followers", "following"}
	if isOwnProfile {
		allTabIDs = append(allTabIDs, "blocked", "requests", "settings")
	}

	var tabButtons strings.Builder
	tabButtons.WriteString(tabButtonHTML("profile", "👤 Profile", activeTab, allTabIDs,
		fmt.Sprintf(`hx-get="/profile-info/%s" hx-target="#profile-info" hx-swap="outerHTML" hx-trigger="click once"`, usernameParam), 0))
	tabButtons.WriteString(tabButtonHTML("posts", "📋 Posts", activeTab, allTabIDs, "", 0))
	tabButtons.WriteString(tabButtonHTML("homechat", "🏠 Home Chat", activeTab, allTabIDs,
		fmt.Sprintf(`hx-get="/homechat-tab/%s" hx-target="#tab-homechat" hx-swap="innerHTML" hx-trigger="click once"`, usernameParam), 0))
	tabButtons.WriteString(tabButtonHTML("games", "🥊 Games", activeTab, allTabIDs,
		fmt.Sprintf(`hx-get="/games-tab/%s" hx-target="#tab-games" hx-swap="innerHTML" hx-trigger="click once"`, usernameParam), 0))
	tabButtons.WriteString(tabButtonHTML("mehfils", "🎪 Mehfils", activeTab, allTabIDs,
		fmt.Sprintf(`hx-get="/mehfils-tab/%s" hx-target="#tab-mehfils" hx-swap="innerHTML" hx-trigger="click once"`, usernameParam), 0))
	tabButtons.WriteString(tabButtonHTML("followers", fmt.Sprintf("Followers (%d)", followersCount), activeTab, allTabIDs,
		fmt.Sprintf(`hx-get="/follow_list/%s?type=followers" hx-target="#tab-followers" hx-swap="innerHTML" hx-trigger="click once"`, usernameParam), 0))
	tabButtons.WriteString(tabButtonHTML("following", fmt.Sprintf("Following (%d)", followingCount), activeTab, allTabIDs,
		fmt.Sprintf(`hx-get="/follow_list/%s?type=following" hx-target="#tab-following" hx-swap="innerHTML" hx-trigger="click once"`, usernameParam), 0))
	if isOwnProfile {
		tabButtons.WriteString(tabButtonHTML("blocked", "🚫 Blocked", activeTab, allTabIDs, "", 0))
		tabButtons.WriteString(tabButtonHTML("requests", "🔔 Follow Requests", activeTab, allTabIDs, "", pendingFollowReqCount))
		tabButtons.WriteString(tabButtonHTML("settings", "⚙️ Settings", activeTab, allTabIDs, "", 0))
	}

	tabSwitcherHTML := fmt.Sprintf(`
	<div style="display:flex;border-bottom:1px solid #e5e7eb;margin-bottom:4px;overflow-x:auto;-webkit-overflow-scrolling:touch;">
		%s
	</div>`, tabButtons.String())

	// ── Avatar block: small "X's Profile" / "My Profile" label, then the
	// image itself shown in full (object-fit:contain, never cropped),
	// with no dead space above it. ──────────────────────────────────────────
	profileLabel := fmt.Sprintf("%s's Profile", usernameParam)
	if isOwnProfile {
		profileLabel = "My Profile"
	}

	// ── Final profile HTML ───────────────────────────────────────────────────
	profileHTML := fmt.Sprintf(`
	<div style="background:#fff;border-radius:28px;box-shadow:0 10px 40px rgba(0,0,0,0.06);padding:20px 16px;border:1px solid #f3f4f6;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">
		%s
		<div style="display:flex;flex-direction:column;align-items:center;text-align:center;margin-bottom:16px;">
			<span style="font-size:11px;font-weight:800;color:#6b7280;text-transform:uppercase;letter-spacing:0.12em;margin-bottom:8px;">%s</span>
			<div class="bp-avatar-box" style="width:100%%;max-width:320px;aspect-ratio:1/1;height:auto;margin:0 auto 14px auto;border-radius:32px;display:flex;align-items:center;justify-content:center;box-shadow:0 12px 36px rgba(0,0,0,0.14);overflow:hidden;">
				%s
			</div>
			<h1 style="font-size:clamp(24px,5vw,36px);font-weight:900;color:#111827;margin:0;">%s%s</h1>
			<div style="display:flex;align-items:center;gap:12px;color:#059669;font-size:16px;margin-top:10px;font-weight:600;">
				<span>%d points</span>
				<span style="color:#d1d5db;">•</span>
				<span>%s posts</span>
			</div>
			<p style="font-size:13px;color:#6b7280;margin-top:8px;font-style:italic;">%s</p>
		</div>

		%s

		<div id="tab-profile" style="%s">
			<div id="profile-info">
				%s
			</div>
		</div>

		<div id="tab-posts" style="%s">
			%s
		</div>

		<div id="tab-homechat" style="%s">
			%s
		</div>

		<div id="tab-games" style="%s">
			%s
		</div>

		<div id="tab-mehfils" style="%s">
			%s
		</div>

		<div id="tab-followers" style="%s">
			%s
		</div>

		<div id="tab-following" style="%s">
			%s
		</div>

		<div id="tab-blocked" style="%s">
			%s
		</div>

		<div id="tab-requests" style="%s">
			%s
		</div>

		<div id="tab-settings" style="%s">
			%s
		</div>
	</div>`,
		birthdayProfileTop(usernameParam)+adminWarning,
		profileLabel,
		birthdayAvatarWrap(usernameParam, getAvatarInner(userProfileImage, "contain")),
		RenderDisplayName(usernameParam), TrendingStarHTML(usernameParam), userPoints, postCountDisplay, formatJoinDuration(joinDate),
		tabSwitcherHTML,
		hiddenStyle("profile", activeTab),
		`<div style="min-height:80px;text-align:center;padding:32px;color:#6b7280;font-size:13px;">Loading…</div>`,
		hiddenStyle("posts", activeTab),
		postsTabBodyHTML,
		hiddenStyle("homechat", activeTab),
		homeChatTabHTML,
		hiddenStyle("games", activeTab),
		gamesTabHTML,
		hiddenStyle("mehfils", activeTab),
		mehfilsTabHTML,
		hiddenStyle("followers", activeTab),
		followersTabHTML,
		hiddenStyle("following", activeTab),
		followingTabHTML,
		hiddenStyle("blocked", activeTab),
		blockedTabHTML,
		hiddenStyle("requests", activeTab),
		requestsTabHTML,
		hiddenStyle("settings", activeTab),
		settingsTabHTML)

	c.Set("Content-Type", "text/html")
	seoTags := renderProfileSEOTags(usernameParam, userBio, userCity, userPoints, totalPosts, userProfileImage, joinDate)
	fullPage := Layout("Profile • "+usernameParam, profileHTML, currentUser)
	if strings.Contains(fullPage, "</head>") {
		fullPage = strings.Replace(fullPage, "</head>", seoTags+"</head>", 1)
	} else if strings.Contains(fullPage, "<head>") {
		fullPage = strings.Replace(fullPage, "<head>", "<head>"+seoTags, 1)
	}
	return c.SendString(fullPage)
}
// gamesTabHandler / mehfilsTabHandler / homeChatTabHandler — lazy-loaded on
// tab click instead of running these queries on every profile page load.

func gamesTabHandler(c *fiber.Ctx) error {
	username := c.Params("username")
	currentUser := getCurrentUser(c)
	return c.SendString(renderGamesTabHTML(username, currentUser))
}

func mehfilsTabHandler(c *fiber.Ctx) error {
	username := c.Params("username")
	currentUser := getCurrentUser(c)
	return c.SendString(renderMehfilsTabHTML(username, currentUser))
}

func homeChatTabHandler(c *fiber.Ctx) error {
	username := c.Params("username")
	currentUser := getCurrentUser(c)
	rows := queryHomeChatPosts(username, 50)
	html := renderPosts(rows, currentUser)
	if len(rows) == 0 {
		label := fmt.Sprintf("%s hasn't posted any Quick Thoughts yet.", username)
		if currentUser == username {
			label = "You haven't posted any Quick Thoughts yet."
		}
		html = fmt.Sprintf(`
		<div style="text-align:center;padding:48px 20px;color:#6b7280;">
			<div style="font-size:44px;margin-bottom:12px;">💭</div>
			<p style="font-size:13px;">%s</p>
		</div>`, label)
	}
	return c.SendString(html)
}

func renderGamesTabHTML(usernameParam, currentUser string) string {
	isOwnProfile := currentUser == usernameParam

	var gamesTotal int
	db.QueryRow(ctx, `SELECT COUNT(*) FROM posts WHERE author=$1 AND COALESCE(dp_competition_id,0) > 0`, usernameParam).Scan(&gamesTotal)

	gRows, gErr := db.Query(ctx, `
		SELECT `+postSelectCols+`
		FROM posts p
		JOIN users u ON p.author = u.username
		WHERE p.author=$1 AND COALESCE(p.dp_competition_id,0) > 0
		ORDER BY p.id DESC LIMIT 25`, usernameParam)

	var gamesRows []postRow
	if gErr == nil {
		defer gRows.Close()
		for gRows.Next() {
			r, err := scanPostRow(gRows)
			if err != nil {
				continue
			}
			gamesRows = append(gamesRows, r)
		}
	}

	startBattleCTA := ""
	if isOwnProfile {
		startBattleCTA = `
		<a href="/dpcompete" style="display:inline-flex;align-items:center;gap:8px;color:#fff;padding:12px 24px;border-radius:16px;font-size:14px;font-weight:800;text-decoration:none;background:linear-gradient(135deg,#ff6b00,#ff0040);box-shadow:0 8px 20px rgba(255,0,64,0.25);">
			🥊 Start a DP Battle
		</a>`
	}

	emptyGames := ""
	if gamesTotal == 0 {
		label := fmt.Sprintf("%s hasn't started any DP Battles yet.", usernameParam)
		if isOwnProfile {
			label = "You haven't started any DP Battles yet."
		}
		emptyGames = fmt.Sprintf(`
		<div style="text-align:center;padding:48px 20px;color:#6b7280;">
			<div style="font-size:44px;margin-bottom:12px;">🥊</div>
			<p style="font-size:13px;">%s</p>
		</div>`, label)
	}

	return fmt.Sprintf(`
	<div style="display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:12px;margin-bottom:16px;">
		<span style="font-size:12px;font-weight:800;color:#6b7280;text-transform:uppercase;letter-spacing:0.15em;">🥊 DP Battles (%d)</span>
		%s
	</div>
	<div style="display:flex;flex-direction:column;gap:4px;">
		%s
	</div>
	%s`, gamesTotal, startBattleCTA, renderPosts(gamesRows, currentUser), emptyGames)
}

func renderMehfilsTabHTML(usernameParam, currentUser string) string {
	isOwnProfile := currentUser == usernameParam

	var mehfilCards strings.Builder
	mehfilCount := 0
	mRows, mErr := db.Query(ctx, `
		SELECT id, topic, admin_username,
		EXTRACT(EPOCH FROM (NOW()-created_at))/3600 as hours_ago,
		(SELECT COUNT(*) FROM mehfil_members WHERE mehfil_id=mehfils.id),
		(SELECT COUNT(*) FROM mehfil_messages WHERE mehfil_id=mehfils.id)
		FROM mehfils WHERE admin_username=$1 ORDER BY id DESC`, usernameParam)
	if mErr == nil {
		defer mRows.Close()
		for mRows.Next() {
			var mId, mMemCount, mMsgCount int
			var mTopic, mAdmin string
			var mHoursAgo float64
			mRows.Scan(&mId, &mTopic, &mAdmin, &mHoursAgo, &mMemCount, &mMsgCount)
			mehfilCount++
			timeStr := "Just now"
			if mHoursAgo >= 1 {
				timeStr = fmt.Sprintf("%dh ago", int(mHoursAgo))
			} else if mHoursAgo > 0 {
				timeStr = fmt.Sprintf("%dm ago", int(mHoursAgo*60))
			}
			mehfilCards.WriteString(RenderMehfilCard(mId, 0, mTopic, mAdmin, currentUser, timeStr, mMemCount, mMsgCount))
		}
	}

	if mehfilCount > 0 {
		label := "Created Mehfils"
		if isOwnProfile {
			label = "Your Created Mehfils"
		}
		return fmt.Sprintf(`
		<div style="margin-bottom:16px;">
			<div style="display:flex;align-items:center;gap:10px;margin-bottom:16px;">
				<span style="font-size:12px;font-weight:800;color:#6b7280;text-transform:uppercase;letter-spacing:0.15em;">%s</span>
				<span style="height:1px;flex:1;background:#f3f4f6;"></span>
			</div>
			<div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:16px;">%s</div>
		</div>`, label, mehfilCards.String())
	}

	emptyText := fmt.Sprintf("%s hasn't created any Mehfils yet.", usernameParam)
	if isOwnProfile {
		emptyText = "You haven't created any Mehfils yet."
	}
	return fmt.Sprintf(`
	<div style="text-align:center;padding:48px 20px;color:#6b7280;">
		<div style="font-size:44px;margin-bottom:12px;">🎪</div>
		<p style="font-size:13px;">%s</p>
	</div>`, emptyText)
}
func adminIdentityPanelHandler(c *fiber.Ctx) error {
	currentUser, err := requireAdmin(c)
	if err != nil {
		return err
	}
	_ = currentUser

	target := c.Params("username")

	var ipAddr, subnet, deviceKey, hardwareID string
	var isBanned, isHWBanned bool
	err = db.QueryRow(ctx, `
		SELECT
			COALESCE(ip_address,''),
			COALESCE(subnet,''),
			COALESCE(device_key,''),
			COALESCE(hardware_id,''),
			COALESCE(is_banned,false),
			COALESCE(is_hardware_banned,false)
		FROM users WHERE username=$1`, target).
		Scan(&ipAddr, &subnet, &deviceKey, &hardwareID, &isBanned, &isHWBanned)
	if err != nil {
		return c.Status(404).SendString("<div style='color:#dc2626;padding:16px;'>User not found</div>")
	}

	type ipEntry struct {
		IP        string
		Subnet    string
		UserAgent string
		SeenAt    time.Time
	}
	var ipHistory []ipEntry
	rows, err := db.Query(ctx, `
		SELECT DISTINCT ON (ip_address)
			ip_address, COALESCE(subnet,''), COALESCE(user_agent,''), seen_at
		FROM user_ip_history
		WHERE username=$1
		ORDER BY ip_address, seen_at DESC
		LIMIT 50`, target)
	if err != nil {
		log.Println("IP history query error:", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var e ipEntry
			rows.Scan(&e.IP, &e.Subnet, &e.UserAgent, &e.SeenAt)
			ipHistory = append(ipHistory, e)
		}
	}

	type linkedAccount struct {
		Username   string
		Reason     string
		IsBanned   bool
		IsHWBanned bool
	}
	var linked []linkedAccount

	addLinked := func(reason, col, val string) {
		if val == "" || val == "127.0.0.1" {
			return
		}
		lrows, lerr := db.Query(ctx, fmt.Sprintf(`
			SELECT username,
			       COALESCE(is_banned,false),
			       COALESCE(is_hardware_banned,false)
			FROM users
			WHERE %s = $1
			  AND username != $2
			LIMIT 20`, col), val, target)
		if lerr != nil {
			return
		}
		defer lrows.Close()
		for lrows.Next() {
			var la linkedAccount
			la.Reason = reason
			lrows.Scan(&la.Username, &la.IsBanned, &la.IsHWBanned)
			linked = append(linked, la)
		}
	}

	addLinked("device_key", "device_key", deviceKey)
	addLinked("hardware_id", "hardware_id", hardwareID)
	addLinked("ip_address", "ip_address", ipAddr)
	addLinked("subnet", "subnet", subnet)

	seen := map[string]bool{}
	var dedupLinked []linkedAccount
	for _, la := range linked {
		if !seen[la.Username] {
			seen[la.Username] = true
			dedupLinked = append(dedupLinked, la)
		}
	}
	linked = dedupLinked

	isBannedID := func(kind, val string) bool {
		if val == "" {
			return false
		}
		var exists bool
		db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM banned_identifiers WHERE kind=$1 AND identifier=$2)`,
			kind, val).Scan(&exists)
		return exists
	}

	ipBanned := isBannedID("ip", ipAddr)
	subnetBanned := isBannedID("subnet", subnet)
	deviceBanned := isBannedID("device_key", deviceKey)

	loc, _ := time.LoadLocation("Asia/Karachi")

	var b strings.Builder

	b.WriteString(`<div id="admin-identity-panel" style="
		font-family:'JetBrains Mono',ui-monospace,monospace;
		background:linear-gradient(135deg,#0f172a 0%,#1e1b4b 100%);
		border:1px solid rgba(139,92,246,0.3);
		border-radius:20px;
		padding:20px;
		margin-top:16px;
		color:#e2e8f0;
		position:relative;
		overflow:hidden;">`)

	b.WriteString(`<div style="position:absolute;top:-40px;right:-40px;width:120px;height:120px;
		background:radial-gradient(circle,rgba(139,92,246,0.25) 0%,transparent 70%);
		pointer-events:none;"></div>`)

	b.WriteString(`<div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:18px;flex-wrap:wrap;gap:8px;">
		<div style="display:flex;align-items:center;gap:8px;">
			<span style="font-size:18px;">🛡️</span>
			<span style="font-size:11px;font-weight:900;color:#a78bfa;letter-spacing:0.2em;text-transform:uppercase;">Identity Intelligence</span>
			<span style="font-size:10px;color:#475569;">` + target + `</span>
		</div>`)

	b.WriteString(`<div style="display:flex;gap:6px;flex-wrap:wrap;">`)
	if isBanned {
		b.WriteString(`<span style="background:#7f1d1d;color:#fca5a5;font-size:9px;font-weight:900;padding:3px 10px;border-radius:999px;border:1px solid #ef4444;">⛔ BANNED</span>`)
	}
	if isHWBanned {
		b.WriteString(`<span style="background:#3b0764;color:#d8b4fe;font-size:9px;font-weight:900;padding:3px 10px;border-radius:999px;border:1px solid #9333ea;">🛡️ HW BANNED</span>`)
	}
	if !isBanned && !isHWBanned {
		b.WriteString(`<span style="background:#052e16;color:#86efac;font-size:9px;font-weight:900;padding:3px 10px;border-radius:999px;border:1px solid #22c55e;">✅ ACTIVE</span>`)
	}
	b.WriteString(`</div></div>`)

	b.WriteString(`<div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:10px;margin-bottom:16px;">`)

	renderIDCard := func(icon, label, value, badgeText, badgeColor string) {
		if value == "" {
			value = "—"
		}
		badge := ""
		if badgeText != "" {
			badge = fmt.Sprintf(`<span style="background:%s;color:white;font-size:8px;font-weight:900;padding:2px 6px;border-radius:4px;margin-left:6px;">%s</span>`, badgeColor, badgeText)
		}
		b.WriteString(fmt.Sprintf(`
		<div style="background:rgba(255,255,255,0.04);border:1px solid rgba(255,255,255,0.08);border-radius:12px;padding:12px 14px;">
			<div style="font-size:9px;color:#64748b;text-transform:uppercase;letter-spacing:0.15em;margin-bottom:6px;">%s %s%s</div>
			<div style="font-size:11px;color:#e2e8f0;word-break:break-all;line-height:1.5;">%s</div>
		</div>`, icon, label, badge, value))
	}

	ipBadge, ipBadgeColor := "", ""
	if ipBanned {
		ipBadge, ipBadgeColor = "BANNED", "#dc2626"
	}
	subnetBadge, subnetBadgeColor := "", ""
	if subnetBanned {
		subnetBadge, subnetBadgeColor = "BANNED", "#dc2626"
	}
	devBadge, devBadgeColor := "", ""
	if deviceBanned {
		devBadge, devBadgeColor = "BANNED", "#dc2626"
	}

	renderIDCard("🌐", "IP Address", ipAddr, ipBadge, ipBadgeColor)
	renderIDCard("🔗", "Subnet", subnet, subnetBadge, subnetBadgeColor)
	renderIDCard("📱", "Device Key", deviceKey, devBadge, devBadgeColor)
	renderIDCard("🔩", "Hardware ID", hardwareID, "", "")

	b.WriteString(`</div>`)

	b.WriteString(`<div style="margin-bottom:16px;">`)
	b.WriteString(fmt.Sprintf(`<div style="font-size:9px;font-weight:900;color:#94a3b8;text-transform:uppercase;letter-spacing:0.2em;margin-bottom:8px;">
		📡 IP History <span style="color:#475569;font-weight:400;">(%d unique IPs)</span>
	</div>`, len(ipHistory)))

	if len(ipHistory) == 0 {
		b.WriteString(`<div style="color:#475569;font-size:11px;padding:12px;background:rgba(255,255,255,0.03);border-radius:10px;text-align:center;">No IP history recorded</div>`)
	} else {
		b.WriteString(`<div style="max-height:200px;overflow-y:auto;border:1px solid rgba(255,255,255,0.07);border-radius:12px;
			scrollbar-width:thin;scrollbar-color:#334155 transparent;">`)
		b.WriteString(`<table style="width:100%;border-collapse:collapse;font-size:10px;">`)
		b.WriteString(`<thead><tr style="background:rgba(139,92,246,0.15);position:sticky;top:0;z-index:1;">
			<th style="padding:8px 10px;text-align:left;color:#a78bfa;font-weight:700;white-space:nowrap;">IP</th>
			<th style="padding:8px 10px;text-align:left;color:#a78bfa;font-weight:700;white-space:nowrap;">Subnet</th>
			<th style="padding:8px 10px;text-align:left;color:#a78bfa;font-weight:700;white-space:nowrap;">Last Seen (PKT)</th>
			<th style="padding:8px 10px;text-align:left;color:#a78bfa;font-weight:700;">User Agent</th>
		</tr></thead>`)
		b.WriteString(`<tbody>`)
		for i, e := range ipHistory {
			rowBg := "transparent"
			if i%2 == 0 {
				rowBg = "rgba(255,255,255,0.025)"
			}
			ipColor := "#cbd5e1"
			if e.IP == ipAddr {
				ipColor = "#4ade80"
				rowBg = "rgba(74,222,128,0.07)"
			}
			ua := e.UserAgent
			if len(ua) > 55 {
				ua = ua[:55] + "…"
			}
			b.WriteString(fmt.Sprintf(`<tr style="background:%s;border-top:1px solid rgba(255,255,255,0.04);">
				<td style="padding:7px 10px;color:%s;font-weight:600;white-space:nowrap;">%s</td>
				<td style="padding:7px 10px;color:#94a3b8;white-space:nowrap;">%s</td>
				<td style="padding:7px 10px;color:#64748b;white-space:nowrap;">%s</td>
				<td style="padding:7px 10px;color:#475569;font-size:9px;">%s</td>
			</tr>`, rowBg, ipColor, e.IP, e.Subnet, e.SeenAt.In(loc).Format("02 Jan 06 15:04"), ua))
		}
		b.WriteString(`</tbody></table></div>`)
	}
	b.WriteString(`</div>`)

	b.WriteString(`<div>`)
	b.WriteString(fmt.Sprintf(`<div style="font-size:9px;font-weight:900;color:#94a3b8;text-transform:uppercase;letter-spacing:0.2em;margin-bottom:8px;">
		🔍 Linked Accounts <span style="color:#475569;font-weight:400;">(%d found)</span>
	</div>`, len(linked)))

	if len(linked) == 0 {
		b.WriteString(`<div style="color:#475569;font-size:11px;padding:12px;background:rgba(255,255,255,0.03);border-radius:10px;text-align:center;">No shared device/IP/subnet accounts found</div>`)
	} else {
		reasonColors := map[string]string{
			"device_key":  "#8b5cf6",
			"hardware_id": "#ec4899",
			"ip_address":  "#f59e0b",
			"subnet":      "#06b6d4",
		}
		reasonIcons := map[string]string{
			"device_key":  "📱",
			"hardware_id": "🔩",
			"ip_address":  "🌐",
			"subnet":      "🔗",
		}

		b.WriteString(`<div style="max-height:180px;overflow-y:auto;display:flex;flex-direction:column;gap:6px;
			scrollbar-width:thin;scrollbar-color:#334155 transparent;padding-right:4px;">`)

		for _, la := range linked {
			statusIcons := ""
			if la.IsBanned {
				statusIcons += ` <span style="font-size:9px;">⛔</span>`
			}
			if la.IsHWBanned {
				statusIcons += ` <span style="font-size:9px;">🛡️</span>`
			}
			rColor := reasonColors[la.Reason]
			if rColor == "" {
				rColor = "#64748b"
			}
			rIcon := reasonIcons[la.Reason]
			if rIcon == "" {
				rIcon = "🔗"
			}

			b.WriteString(fmt.Sprintf(`
			<div style="display:flex;align-items:center;justify-content:space-between;
				background:rgba(255,255,255,0.04);border:1px solid rgba(255,255,255,0.07);
				border-radius:10px;padding:9px 12px;gap:8px;">
				<div style="display:flex;align-items:center;gap:8px;min-width:0;">
					<a href="/profile/%[1]s"
					   style="color:#93c5fd;font-weight:700;font-size:11px;text-decoration:none;white-space:nowrap;"
					   target="_blank">%[1]s</a>
					%[2]s
				</div>
				<div style="display:flex;align-items:center;gap:4px;flex-shrink:0;">
					<span style="font-size:9px;">%[3]s</span>
					<span style="background:%[4]s22;color:%[4]s;border:1px solid %[4]s55;
						font-size:8px;font-weight:900;padding:2px 7px;border-radius:999px;
						text-transform:uppercase;letter-spacing:0.08em;white-space:nowrap;">
						%[5]s
					</span>
				</div>
			</div>`, la.Username, statusIcons, rIcon, rColor, strings.ReplaceAll(la.Reason, "_", " ")))
		}
		b.WriteString(`</div>`)
	}

	b.WriteString(`</div>`)
	b.WriteString(`</div>`)

	return c.SendString(b.String())
}

func formatJoinDuration(joined time.Time) string {
	now := time.Now()
	years := now.Year() - joined.Year()
	months := int(now.Month()) - int(joined.Month())
	if months < 0 {
		years--
		months += 12
	}
	totalHours := int(now.Sub(joined).Hours())
	totalDays := totalHours / 24
	weeks := totalDays / 7

	since := joined.Format("Jan 2006")
	switch {
	case years >= 1:
		if months > 0 {
			return fmt.Sprintf("%s · %dy %dmo ago", since, years, months)
		}
		return fmt.Sprintf("%s · %dy ago", since, years)
	case months >= 1:
		return fmt.Sprintf("%s · %dmo ago", since, months)
	case weeks >= 1:
		return fmt.Sprintf("%s · %dw ago", since, weeks)
	case totalDays >= 1:
		return fmt.Sprintf("%s · %dd ago", since, totalDays)
	default:
		return fmt.Sprintf("%s · %dh ago", since, totalHours)
	}
}

func renderAdminIdentityTrigger(username string) string {
	return fmt.Sprintf(`
	<div style="margin-top:16px;">
		<button
			hx-get="/admin/identity/%[1]s"
			hx-target="#admin-identity-panel"
			hx-swap="outerHTML"
			hx-trigger="click once"
			onclick="this.innerHTML='⏳ Loading…';this.disabled=true"
			style="width:100%%;background:linear-gradient(135deg,#1e1b4b,#0f172a);
				color:#a78bfa;border:1px solid rgba(139,92,246,0.4);
				padding:11px 18px;border-radius:12px;font-size:11px;font-weight:900;
				cursor:pointer;letter-spacing:0.15em;text-transform:uppercase;
				transition:all 0.2s;font-family:ui-monospace,monospace;"
			onmouseover="this.style.borderColor='rgba(139,92,246,0.8)'"
			onmouseout="this.style.borderColor='rgba(139,92,246,0.4)'">
			🛡️ View Identity Intelligence
		</button>
		<div id="admin-identity-panel"></div>
	</div>`, username)
}

func getProfileInfoDisplay(currentUser, username, bio, city, gender string, birthDate time.Time, points int, starSign string, followersCount, followingCount int) string {
	var isBanned, isPrivate bool
	db.QueryRow(ctx,
		"SELECT COALESCE(is_banned,false), COALESCE(is_private,false) FROM users WHERE username=$1",
		username).Scan(&isBanned, &isPrivate)

	var currentUserType string
	db.QueryRow(ctx, "SELECT user_type FROM users WHERE username=$1", currentUser).Scan(&currentUserType)
	isAdmin := currentUserType == "admin"
	isOwnProfile := currentUser == username

	bannedDialogue := renderBanNoticeHTML(username)

	if isBanned && !isAdmin {
		return fmt.Sprintf(`<div style="background:#f9fafb;border-radius:24px;padding:24px;">%s</div>`, bannedDialogue)
	}

	if isPrivate && !isOwnProfile && !isAdmin && !isTopThreeScorer(currentUser) {
		return fmt.Sprintf(`<div style="background:#f9fafb;border-radius:24px;padding:24px;">
			<div style="text-align:center;padding:40px 20px;background:#f0f9ff;border:2px solid #bae6fd;border-radius:32px;">
				<div style="font-size:50px;margin-bottom:15px;">🔒</div>
				<h2 style="color:#0369a1;margin:0;font-size:22px;font-weight:800;">Yeh Profile Private Hai</h2>
				<p style="color:#0284c7;font-size:14px;margin-top:8px;">Is user ki profile private hai.</p>
				%s
			</div>
		</div>`, renderBlockUnblockButton(currentUser, username))
	}

	age := calculateAge(birthDate)
	if bio == "" {
		bio = "Not set"
	}
	if city == "" {
		city = "Not set"
	}
	if gender == "" {
		gender = "Not set"
	}

	adminPanel := ""
	if isAdmin && currentUser != username {
		adminPanel = fmt.Sprintf(`
		<div style="margin-top:24px;padding:20px;border:2px dashed #fca5a5;border-radius:24px;background-color:rgba(254,242,242,0.5);">
			<div style="display:flex;align-items:center;gap:8px;margin-bottom:16px;">
				<span style="width:8px;height:8px;background-color:#ef4444;border-radius:50%%;animation:pulse 2s infinite;"></span>
				<h3 style="margin:0;font-size:10px;font-weight:900;color:#dc2626;text-transform:uppercase;letter-spacing:0.2em;">Admin Command Center</h3>
			</div>
			<div style="display:flex;flex-direction:column;gap:16px;">
							<div style="display:flex;flex-wrap:wrap;gap:12px;">
					<a href="/admin/bans?username=%[1]s"
					        style="display:flex;align-items:center;justify-content:center;background-color:#111827;color:white;padding:12px 20px;border-radius:16px;text-decoration:none;font-size:12px;font-weight:bold;flex:1;">
						🛡️ Manage Ban
					</a>
					<a href="/admin/dashboard"
					        style="display:flex;align-items:center;justify-content:center;background-color:#374151;color:white;padding:12px 20px;border-radius:16px;text-decoration:none;font-size:12px;font-weight:bold;flex:1;">
						📊 Dashboard
					</a>
				</div>
				<div style="display:flex;flex-direction:column;gap:6px;">
					<label style="font-size:9px;font-weight:bold;color:#9ca3af;text-transform:uppercase;margin-left:8px;">Update Points</label>
					<div style="display:flex;gap:8px;">
						<input type="number" id="admin-points-input" value="%[2]d"
						       style="width:100%%;max-width:100px;padding:10px 16px;font-size:14px;border-radius:16px;border:1px solid #e5e7eb;outline:none;background:white;">
						<button onclick="const p=document.getElementById('admin-points-input').value;if(p){htmx.ajax('POST','/admin/set-points/%[1]s',{target:'#profile-info',values:{points:p}})}"
						        style="background-color:#2563eb;color:white;padding:12px 20px;border-radius:16px;border:none;font-size:12px;font-weight:bold;cursor:pointer;flex-grow:1;">
							Set Score
						</button>
					</div>
			</div>
		</div>
	</div>`, username, points)

		adminPanel += renderAdminIdentityTrigger(username)
	}

	followSectionHTML := RenderFollowSection(currentUser, username, followersCount, followingCount)

	headerDialogue := ""
	if isBanned && isAdmin {
		headerDialogue = bannedDialogue
	}
	if isPrivate && isAdmin && !isOwnProfile {
		headerDialogue += `<div style="text-align:center;padding:16px;background:#fef3c7;border:2px solid #f59e0b;border-radius:16px;margin-bottom:16px;">
			<span style="font-weight:bold;color:#92400e;">⚠️ ADMIN VIEW: Profile Private Hai</span>
		</div>`
	}
	if isPrivate && !isOwnProfile && !isAdmin && isTopThreeScorer(currentUser) {
		headerDialogue += `<div style="text-align:center;padding:16px;background:#f5f3ff;border:2px solid #8b5cf6;border-radius:16px;margin-bottom:16px;">
			<span style="font-weight:bold;color:#6d28d9;">🏆 Ap top 3 scorer me se aik hain, isi liye ap ye private profile bhi dekh sakte hain.</span>
		</div>`
	}

	return fmt.Sprintf(`
	<div style="background:#f9fafb;border-radius:24px;padding:24px;">
		%s
		<div style="margin-bottom:24px;padding-bottom:24px;border-bottom:1px solid #e5e7eb;">
			%s
		</div>
		<div style="display:flex;flex-direction:column;gap:2px;">
			<div style="display:flex;align-items:center;padding:12px 4px;border-bottom:1px solid #e5e7eb;">
				<span style="font-weight:600;color:#6b7280;width:110px;flex-shrink:0;font-size:13px;">City</span>
				<span style="color:#1f2937;flex:1;font-size:14px;">%s</span>
			</div>
			<div style="display:flex;align-items:center;padding:12px 4px;border-bottom:1px solid #e5e7eb;">
				<span style="font-weight:600;color:#6b7280;width:110px;flex-shrink:0;font-size:13px;">Gender</span>
				<span style="color:#1f2937;flex:1;font-size:14px;">%s</span>
			</div>
			<div style="display:flex;align-items:center;padding:12px 4px;border-bottom:1px solid #e5e7eb;">
				<span style="font-weight:600;color:#6b7280;width:110px;flex-shrink:0;font-size:13px;">Age</span>
				<span style="color:#1f2937;flex:1;font-size:14px;">%d years</span>
			</div>
			<div style="display:flex;align-items:center;padding:12px 4px;border-bottom:1px solid #e5e7eb;">
				<span style="font-weight:600;color:#6b7280;width:110px;flex-shrink:0;font-size:13px;">Star Sign</span>
				<span style="color:#1f2937;flex:1;font-size:14px;">%s</span>
			</div>
			<div style="display:flex;align-items:center;padding:12px 4px;border-bottom:1px solid #e5e7eb;">
				<span style="font-weight:600;color:#6b7280;width:110px;flex-shrink:0;font-size:13px;">Points</span>
				<span style="color:#059669;font-weight:700;flex:1;font-size:14px;">%d points</span>
			</div>
			<div style="padding:14px 4px 4px 4px;">
				<span style="font-weight:600;color:#6b7280;display:block;margin-bottom:8px;font-size:13px;">Bio</span>
				<span style="color:#1f2937;word-break:break-word;display:block;font-size:14px;line-height:1.6;">%s</span>
			</div>
		</div>
		%s
	</div>`,
		headerDialogue,
		followSectionHTML,
		city, gender, age, starSign, points, bio,
		adminPanel)
}

// ── getAvatarInner ───────────────────────────────────────────────────────
// Renders just the image (or placeholder) that fills whatever avatar box
// the caller has set up. fit is "contain" (show the whole image,
// letterboxed on a neutral background — used everywhere now so avatars are
// never cropped) or "cover" (fill and crop — kept as an option but no
// longer used by default).
func getAvatarInner(path, fit string) string {
	if path == "" {
		return `<div style="width:100%;height:100%;display:flex;align-items:center;justify-content:center;background:#f3f4f6;">
			<span style="font-size:3.5rem;">👤</span>
		</div>`
	}
	return fmt.Sprintf(`<div style="width:100%%;height:100%%;background:#f3f4f6;display:flex;align-items:center;justify-content:center;">
		<img src="%s" style="width:100%%;height:100%%;object-fit:%s;display:block;" alt="Profile Image">
	</div>`, path, fit)
}

// ── getProfileImageDisplay ──────────────────────────────────────────────────
// Renders the avatar image/frame. The parent container (set by the caller,
// see profileHandler / editProfileFormHandler) constrains the actual
// on-screen size — the box itself scales responsively via aspect-ratio, and
// the image inside is always shown with object-fit:contain, so nothing is
// ever cut off top/bottom/left/right regardless of the source aspect ratio.
func getProfileImageDisplay(username string, path string) string {
	var isBanned bool
	db.QueryRow(ctx, "SELECT COALESCE(is_banned,false) FROM users WHERE username=$1", username).Scan(&isBanned)

	if isBanned {
		return `<div style="width:100%;height:100%;display:flex;align-items:center;justify-content:center;background:#fee2e2;border-radius:1.5rem;border:2px solid #ef4444;">
			<span style="font-size:2.5rem;">🚫</span>
		</div>`
	}

	var rank int
	db.QueryRow(ctx, `
		SELECT rank FROM (
			SELECT username, DENSE_RANK() OVER (ORDER BY points DESC) as rank
			FROM users
				WHERE COALESCE(user_type, '') != 'admin' AND LOWER(username) != 'heera'
		) as ranked_users WHERE username=$1`, username).Scan(&rank)
	displayPath := path
	if displayPath == "" {
		displayPath = "https://ui-avatars.com/api/?name=" + username + "&background=random"
	}

	if rank <= 0 || rank > 20 {
		return fmt.Sprintf(`<div style="width:100%%;height:100%%;background:#f3f4f6;border-radius:1.5rem;border:1px solid #e5e7eb;display:flex;align-items:center;justify-content:center;overflow:hidden;">
			<img src="%s" style="width:100%%;height:100%%;object-fit:contain;display:block;">
		</div>`, displayPath)
	}

	t := GetRankTheme(rank)
	currentColor := t.Hex
	currentEmoji := t.Creature
	animName := fmt.Sprintf("beauty-%d-%s", rank, username)
	var keyframes string

	// Was a fixed rank-1..10 list; cycling on rank%4 makes the same 4
	// motion patterns cover ranks 1-20 (and beyond) instead of leaving
	// rank 11-20 with no keyframes at all (a silently static emoji).
	switch rank % 4 {
	case 1:
		keyframes = fmt.Sprintf(`@keyframes %s {
			0%%,100%% { top:8%%;left:8%%;transform:rotate(0deg); }
			25%% { top:8%%;left:75%%;transform:rotate(10deg); }
			50%% { top:75%%;left:75%%;transform:rotate(0deg); }
			75%% { top:75%%;left:8%%;transform:rotate(-10deg); }
		}`, animName)
	case 2:
		keyframes = fmt.Sprintf(`@keyframes %s {
			0%%,100%% { bottom:8%%;right:8%%;transform:rotate(-5deg) scale(1); }
			50%% { bottom:12%%;right:12%%;transform:rotate(5deg) scale(1.1); }
		}`, animName)
	case 3:
		keyframes = fmt.Sprintf(`@keyframes %s {
			0%%,100%% { left:8%%;top:25%%; }
			50%% { left:8%%;top:65%%; }
		}`, animName)
	case 0:
		keyframes = fmt.Sprintf(`@keyframes %s {
			0%% { left:5%%;top:8%%; }
			50%% { left:75%%;top:8%%; }
			100%% { left:5%%;top:8%%; }
		}`, animName)
	}

	borderStyle := "solid"
	if rank <= 3 {
		borderStyle = "double"
	} else if rank >= 7 && rank <= 8 {
		borderStyle = "dashed"
	} else if rank >= 9 {
		borderStyle = "dotted"
	}

	return fmt.Sprintf(`
	<div style="position:relative;width:100%%;height:100%%;padding:8px;border-radius:2.6rem;background:rgba(0,0,0,0.05);overflow:hidden;">
		<style>%s</style>
		<div style="position:absolute;z-index:100;font-size:2.2rem;pointer-events:none;filter:drop-shadow(0 0 10px %s);animation:%s 6s infinite ease-in-out;">
			%s
		</div>
		<div style="width:100%%;height:100%%;background:#f3f4f6;border-radius:2.1rem;position:relative;z-index:10;overflow:hidden;display:flex;align-items:center;justify-content:center;">
			<img src="%s" style="width:100%%;height:100%%;object-fit:contain;display:block;">
		</div>
		<div style="position:absolute;inset:0;border-radius:2.6rem;z-index:20;pointer-events:none;border:6px %s %s;box-shadow:0 0 15px %s,inset 0 0 10px %s;opacity:0.8;"></div>
	</div>`,
		keyframes, currentColor, animName, currentEmoji, displayPath, borderStyle, currentColor, currentColor, currentColor)
}

// ── deletePost ──────────────────────────────────────────────────────────────
func deletePost(c *fiber.Ctx) error {
	postID := c.Params("id")
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.SendStatus(401)
	}

	var author string
	if err := db.QueryRow(ctx, "SELECT author FROM posts WHERE id=$1", postID).Scan(&author); err != nil {
		return c.SendString("Post not found")
	}
	if author != currentUser {
		var viewerType string
		db.QueryRow(ctx, "SELECT user_type FROM users WHERE username=$1", currentUser).Scan(&viewerType)
		if viewerType != "admin" {
			return c.SendString("You can only delete your own posts")
		}
	}

	if _, err := db.Exec(ctx, "DELETE FROM posts WHERE id=$1", postID); err != nil {
		log.Println("Error deleting post:", err)
		return c.SendString("Error deleting post")
	}

	return c.SendString("")
}
package main

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

const (
	maxUploadSize = 10 * 1024 * 1024 // 10MB
	uploadDir     = "./uploads/profile_images"
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
	"Attock", "Vehari", "Kot Abdul Malik", "Ferozwala", "Chakwal", "Gujranwala",
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

	_, format, err := image.DecodeConfig(src)
	if err != nil {
		return "", fmt.Errorf("invalid or corrupted image file")
	}

	allowedFormats := map[string]bool{"jpeg": true, "png": true, "gif": true}
	if !allowedFormats[format] {
		return "", fmt.Errorf("unsupported image format: %s", format)
	}

	return ext, nil
}

func saveUploadedImage(file *multipart.FileHeader, username string) (string, error) {
	ext, err := validateImageUpload(file)
	if err != nil {
		return "", err
	}

	timestamp := time.Now().Unix()
	filename := fmt.Sprintf("%s_%d%s", username, timestamp, ext)
	filepath := filepath.Join(uploadDir, filename)

	src, err := file.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	dst, err := os.Create(filepath)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err = io.Copy(dst, src); err != nil {
		return "", err
	}

	return "/uploads/" + filename, nil
}

func editProfileFormHandler(c *fiber.Ctx) error {
	currentUser := c.Cookies("user")
	if currentUser == "" {
		return c.SendStatus(401)
	}

	var userBio, userCity, userGender, profileImage string
	var birthDate time.Time

	err := db.QueryRow(ctx, `
		SELECT COALESCE(bio, ''), COALESCE(city, ''), COALESCE(gender, ''), 
		       COALESCE(birth_date, '2000-01-01'::date), COALESCE(profile_image, '')
		FROM users WHERE username = $1`, currentUser).
		Scan(&userBio, &userCity, &userGender, &birthDate, &profileImage)

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

	formHTML := fmt.Sprintf(`
		<form hx-post="/update-profile" 
			  hx-target="#profile-info" 
			  hx-swap="outerHTML" 
			  hx-encoding="multipart/form-data" 
			  class="space-y-4">
			<div class="space-y-4">
				<div>
					<label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">Profile Image</label>
					<div class="flex items-start gap-4">
						<div class="w-32 h-32 bg-gradient-to-br from-blue-500 to-emerald-500 rounded-3xl flex items-center justify-center shadow-inner overflow-hidden">
							%s
						</div>
						<div class="flex-1">
							<input type="file" name="profile_image" accept="image/jpeg,image/png,image/gif,image/webp"
								   class="w-full px-4 py-2 bg-white dark:bg-gray-700 border border-gray-300 dark:border-gray-600 rounded-xl 
										  file:mr-3 file:py-2 file:px-4 file:rounded-lg file:border-0 
										  file:text-sm file:font-medium file:bg-emerald-50 file:text-emerald-700 
										  hover:file:bg-emerald-100 dark:file:bg-emerald-900 dark:file:text-emerald-300
										  text-sm text-gray-500 dark:text-gray-400">
							<p class="text-xs text-gray-500 dark:text-gray-400 mt-1">Max 10MB • JPG, PNG, GIF, WEBP</p>
						</div>
					</div>
				</div>

				<div>
					<label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Bio (max 300 chars)</label>
					<textarea name="bio" rows="4" maxlength="300"
						class="w-full px-4 py-2 bg-white dark:bg-gray-700 border border-gray-300 dark:border-gray-600 rounded-xl focus:ring-2 focus:ring-emerald-500 outline-none">%s</textarea>
					<p class="text-xs text-gray-500 dark:text-gray-400 mt-1"><span id="bio-count">%d</span>/300 characters</p>
				</div>

				<div>
					<label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">City</label>
					<select name="city" 
						class="w-full px-4 py-2 bg-white dark:bg-gray-700 border border-gray-300 dark:border-gray-600 rounded-xl focus:ring-2 focus:ring-emerald-500 outline-none">
						<option value="">Select your city</option>
						%s
					</select>
				</div>

				<div>
					<label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Gender</label>
					<select name="gender" 
						class="w-full px-4 py-2 bg-white dark:bg-gray-700 border border-gray-300 dark:border-gray-600 rounded-xl focus:ring-2 focus:ring-emerald-500 outline-none">
						<option value="" %s>Not specified</option>
						<option value="Male" %s>Male</option>
						<option value="Female" %s>Female</option>
						<option value="Other" %s>Other</option>
					</select>
				</div>

				<div>
					<label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Birth Date</label>
					<input type="date" name="birth_date" value="%s" 
						class="w-full px-4 py-2 bg-white dark:bg-gray-700 border border-gray-300 dark:border-gray-600 rounded-xl focus:ring-2 focus:ring-emerald-500 outline-none">
				</div>
			</div>

			<div class="flex gap-3 pt-4">
				<button type="submit" 
					class="bg-emerald-600 hover:bg-emerald-700 text-white px-6 py-2.5 rounded-xl font-medium transition">
					Save Changes
				</button>
				<button type="button" hx-get="/profile-info/%s" hx-target="#profile-info" hx-swap="outerHTML"
					class="bg-gray-200 hover:bg-gray-300 dark:bg-gray-700 dark:hover:bg-gray-600 text-gray-700 dark:text-gray-300 px-6 py-2.5 rounded-xl font-medium transition">
					Cancel
				</button>
			</div>
			<script>
				const textarea = document.querySelector('textarea[name="bio"]');
				const counter = document.getElementById('bio-count');
				if (textarea && counter) {
					textarea.addEventListener('input', function() {
						counter.textContent = this.value.length;
					});
				}
			</script>
		</form>
	`, getProfileImagePreview(profileImage), userBio, len(userBio), cityOptions,
		selectedAttr(userGender == ""),
		selectedAttr(userGender == "Male"),
		selectedAttr(userGender == "Female"),
		selectedAttr(userGender == "Other"),
		birthDate.Format("2006-01-02"),
		currentUser)

	return c.SendString(formHTML)
}

func getProfileImagePreview(path string) string {
	if path == "" {
		return `<span class="text-6xl">👤</span>`
	}
	return fmt.Sprintf(`<img src="%s" class="w-full h-full object-cover">`, path)
}

func updateProfileHandler(c *fiber.Ctx) error {
	currentUser := c.Cookies("user")
	if currentUser == "" {
		return c.SendStatus(401)
	}

	bio := c.FormValue("bio")
	city := c.FormValue("city")
	gender := c.FormValue("gender")
	birthDate := c.FormValue("birth_date")

	if len(bio) > 300 {
		bio = bio[:300]
	}

	var profileImagePath string
	file, err := c.FormFile("profile_image")
	if err == nil {
		imagePath, err := saveUploadedImage(file, currentUser)
		if err != nil {
			log.Println("Upload error:", err)
			return c.SendString(fmt.Sprintf("<div class='text-red-500 p-4'>Error: %s</div>", err.Error()))
		}
		profileImagePath = imagePath

		_, err = db.Exec(ctx, `
			UPDATE users 
			SET bio = $1, city = $2, gender = $3, birth_date = $4, profile_image = $5,
				last_profile_image_change = CURRENT_TIMESTAMP
			WHERE username = $6`,
			bio, city, gender, birthDate, profileImagePath, currentUser)
	} else {
		_, err = db.Exec(ctx, `
			UPDATE users 
			SET bio = $1, city = $2, gender = $3, birth_date = $4
			WHERE username = $5`,
			bio, city, gender, birthDate, currentUser)
	}

	if err != nil {
		log.Println("Update error:", err)
		return c.SendString("<div class='text-red-500 p-4'>Error updating profile</div>")
	}

	return profileInfoHandler(c)
}

// Updated profileInfoHandler function
func profileInfoHandler(c *fiber.Ctx) error {
	username := c.Params("username")
	if username == "" {
		username = c.Cookies("user")
	}

	var userBio, userCity, userGender, userStarSign, userProfileImage string
	var userBirthDate time.Time
	var userPoints int

	err := db.QueryRow(ctx, `
		SELECT COALESCE(bio, ''), COALESCE(city, ''), COALESCE(gender, ''), 
		       COALESCE(birth_date, '2000-01-01'::date), COALESCE(points, 0),
		       COALESCE(profile_image, '')
		FROM users WHERE username = $1`, username).
		Scan(&userBio, &userCity, &userGender, &userBirthDate, &userPoints, &userProfileImage)

	if err != nil {
		log.Println("Query error:", err)
		return c.SendString("<div class='text-red-500 p-4'>Error loading profile</div>")
	}

	userStarSign = calculateStarSign(userBirthDate)
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

	html := fmt.Sprintf(`
		<div id="profile-info" class="bg-gray-50 dark:bg-gray-800 rounded-3xl p-6">
			<div class="space-y-4">
				<div class="flex flex-col space-y-3">
					<div class="flex items-center py-2 border-b border-gray-200 dark:border-gray-700">
						<span class="font-medium text-gray-600 dark:text-gray-400 w-28 flex-shrink-0">City:</span>
						<span class="text-gray-800 dark:text-gray-200 flex-1">%s</span>
					</div>
					
					<div class="flex items-center py-2 border-b border-gray-200 dark:border-gray-700">
						<span class="font-medium text-gray-600 dark:text-gray-400 w-28 flex-shrink-0">Gender:</span>
						<span class="text-gray-800 dark:text-gray-200 flex-1">%s</span>
					</div>
					
					<div class="flex items-center py-2 border-b border-gray-200 dark:border-gray-700">
						<span class="font-medium text-gray-600 dark:text-gray-400 w-28 flex-shrink-0">Age:</span>
						<span class="text-gray-800 dark:text-gray-200 flex-1">%d years</span>
					</div>
					
					<div class="flex items-center py-2 border-b border-gray-200 dark:border-gray-700">
						<span class="font-medium text-gray-600 dark:text-gray-400 w-28 flex-shrink-0">Star Sign:</span>
						<span class="text-gray-800 dark:text-gray-200 flex-1">%s</span>
					</div>
					
					<div class="flex items-center py-2 border-b border-gray-200 dark:border-gray-700">
						<span class="font-medium text-gray-600 dark:text-gray-400 w-28 flex-shrink-0">Points:</span>
						<span class="text-emerald-600 dark:text-emerald-400 font-medium flex-1">%d points</span>
					</div>
					
					<div class="py-2">
						<span class="font-medium text-gray-600 dark:text-gray-400 block mb-2">Bio:</span>
						<span class="text-gray-800 dark:text-gray-200 break-words block">%s</span>
					</div>
				</div>
			</div>
		</div>
	`, userCity, userGender, age, userStarSign, userPoints, userBio)

	return c.SendString(html)
}
func selectedAttr(condition bool) string {
	if condition {
		return "selected"
	}
	return ""
}

func profileHandler(c *fiber.Ctx) error {
	usernameParam := c.Params("username")
	currentUser := c.Cookies("user")
	page := c.QueryInt("page", 1)
	const limit = 25

	if usernameParam == "" {
		if currentUser == "" {
			return c.Redirect("/signup")
		}
		usernameParam = currentUser
	}

	offset := (page - 1) * limit
	isOwnProfile := currentUser == usernameParam

	var userBio, userCity, userGender, userStarSign, userProfileImage string
	var userBirthDate time.Time
	var userPoints, totalPosts int
	var joinDate time.Time

	if page == 1 {
		err := db.QueryRow(ctx, `
			SELECT 
				COALESCE(bio, ''), 
				COALESCE(city, ''), 
				COALESCE(gender, ''), 
				COALESCE(birth_date, '2000-01-01'::date),
				COALESCE(points, 0),
				COALESCE(profile_image, ''),
				created_at,
				(SELECT COUNT(*) FROM posts WHERE author = $1)
			FROM users 
			WHERE username = $1`, usernameParam).
			Scan(&userBio, &userCity, &userGender, &userBirthDate, &userPoints, &userProfileImage, &joinDate, &totalPosts)
		if err != nil {
			log.Println("User data query error:", err)
		}
		userStarSign = calculateStarSign(userBirthDate)
	}

	rows, err := db.Query(ctx, `
		SELECT id, author, content, created_at
		FROM posts
		WHERE author = $1
		ORDER BY id DESC
		LIMIT $2 OFFSET $3`, usernameParam, limit, offset)
	if err != nil {
		log.Println("Query error:", err)
		return c.SendString("Database error")
	}
	defer rows.Close()

	var postsHTML strings.Builder
	postCount := 0

	for rows.Next() {
		var id int
		var author, content string
		var createdAt time.Time
		rows.Scan(&id, &author, &content, &createdAt)

		var points int
		db.QueryRow(ctx, "SELECT COALESCE(points, 0) FROM users WHERE username = $1", author).Scan(&points)

		postsHTML.WriteString(renderPostHTML(id, author, content, createdAt, currentUser, points))
		postCount++
	}

	infiniteScrollTrigger := ""
	if postCount == limit {
		infiniteScrollTrigger = fmt.Sprintf(`
			<div id="load-more"
				 hx-get="/profile/%s?page=%d"
				 hx-trigger="intersect once"
				 hx-target="#posts-container"
				 hx-swap="beforeend"
				 class="py-8 flex items-center justify-center">
				<div id="loading-indicator" class="htmx-indicator flex items-center gap-3 text-gray-500 dark:text-gray-400">
					<svg class="animate-spin h-5 w-5" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
						<circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
						<path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"></path>
					</svg>
					<span class="text-sm font-medium">Loading more posts...</span>
				</div>
			</div>`, usernameParam, page+1)
	} else if page > 1 {
		infiniteScrollTrigger = `<div class="py-8 text-center text-gray-500 dark:text-gray-400 text-sm">🎉 No more posts</div>`
	}

	postCountDisplay := fmt.Sprintf("%d", totalPosts)
	if totalPosts > 9999 {
		postCountDisplay = "9999+"
	}

	if page == 1 {
		editButtons := ""
		if isOwnProfile {
			editButtons = `
				<div class="flex flex-wrap gap-3 justify-center mb-6">
					<a href="/change-password" 
					   class="flex items-center gap-2 bg-gray-100 hover:bg-gray-200 dark:bg-gray-800 dark:hover:bg-gray-700 text-gray-700 dark:text-gray-300 px-5 py-2.5 rounded-2xl text-sm font-medium transition">
						🔑 Change Password
					</a>
					<button hx-get="/edit-profile-form"
							hx-target="#profile-info"
							hx-swap="innerHTML"
							class="flex items-center gap-2 bg-emerald-100 hover:bg-emerald-200 dark:bg-emerald-900 dark:hover:bg-emerald-800 text-emerald-700 dark:text-emerald-300 px-5 py-2.5 rounded-2xl text-sm font-medium transition">
						✏️ Edit Profile
					</button>
				</div>`
		}

		profileHTML := fmt.Sprintf(`
			<div class="bg-white dark:bg-gray-900 rounded-3xl shadow-xl p-6 sm:p-8 border border-gray-100 dark:border-gray-700">
				<div class="flex flex-col items-center text-center mb-6">
					<div class="w-64 h-64 sm:w-80 sm:h-80 bg-gradient-to-br from-blue-500 to-emerald-500 rounded-3xl flex items-center justify-center shadow-xl overflow-hidden mb-4">
						%s
					</div>
					<h1 class="text-3xl sm:text-4xl font-bold text-gray-900 dark:text-white">@%s</h1>
					<div class="flex items-center gap-3 text-emerald-600 dark:text-emerald-500 text-lg mt-2">
						<span class="font-medium">%d points</span>
						<span class="text-gray-300 dark:text-gray-600">•</span>
						<span class="font-medium">%s posts</span>
					</div>
					<p class="text-sm text-gray-500 dark:text-gray-400 mt-2">Member since %s</p>
				</div>

				%s

				<div id="profile-info">
					%s
				</div>

				<div class="mt-8">
					<h2 class="text-xl font-semibold mb-4 text-gray-800 dark:text-gray-200">Posts</h2>
					<div id="posts-container" 
						 class="space-y-5 sm:space-y-6"
						 hx-get="/profile/%s?page=1"
						 hx-trigger="every 1s"
						 hx-swap="innerHTML"
						 hx-select="#posts-container > *">
						%s
						%s
					</div>
				</div>
			</div>
		`, getProfileImageDisplay(userProfileImage),
			usernameParam,
			userPoints,
			postCountDisplay,
			joinDate.Format("January 2006"),
			editButtons,
			getProfileInfoDisplay(usernameParam, userBio, userCity, userGender, userBirthDate, userPoints, userStarSign),
			usernameParam,
			postsHTML.String(),
			infiniteScrollTrigger)

		c.Set("Content-Type", "text/html")
		return c.SendString(Layout("Profile • @"+usernameParam, profileHTML, currentUser))
	}

	c.Set("Content-Type", "text/html")
	return c.SendString(postsHTML.String() + infiniteScrollTrigger)
}

// Updated getProfileInfoDisplay function
func getProfileInfoDisplay(username, bio, city, gender string, birthDate time.Time, points int, starSign string) string {
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

	return fmt.Sprintf(`
		<div class="bg-gray-50 dark:bg-gray-800 rounded-3xl p-6">
			<div class="space-y-4">
				<div class="flex flex-col space-y-3">
					<div class="flex items-center py-2 border-b border-gray-200 dark:border-gray-700">
						<span class="font-medium text-gray-600 dark:text-gray-400 w-28 flex-shrink-0">City:</span>
						<span class="text-gray-800 dark:text-gray-200 flex-1">%s</span>
					</div>
					
					<div class="flex items-center py-2 border-b border-gray-200 dark:border-gray-700">
						<span class="font-medium text-gray-600 dark:text-gray-400 w-28 flex-shrink-0">Gender:</span>
						<span class="text-gray-800 dark:text-gray-200 flex-1">%s</span>
					</div>
					
					<div class="flex items-center py-2 border-b border-gray-200 dark:border-gray-700">
						<span class="font-medium text-gray-600 dark:text-gray-400 w-28 flex-shrink-0">Age:</span>
						<span class="text-gray-800 dark:text-gray-200 flex-1">%d years</span>
					</div>
					
					<div class="flex items-center py-2 border-b border-gray-200 dark:border-gray-700">
						<span class="font-medium text-gray-600 dark:text-gray-400 w-28 flex-shrink-0">Star Sign:</span>
						<span class="text-gray-800 dark:text-gray-200 flex-1">%s</span>
					</div>
					
					<div class="flex items-center py-2 border-b border-gray-200 dark:border-gray-700">
						<span class="font-medium text-gray-600 dark:text-gray-400 w-28 flex-shrink-0">Points:</span>
						<span class="text-emerald-600 dark:text-emerald-400 font-medium flex-1">%d points</span>
					</div>
					
					<div class="py-2">
						<span class="font-medium text-gray-600 dark:text-gray-400 block mb-2">Bio:</span>
						<span class="text-gray-800 dark:text-gray-200 break-words block">%s</span>
					</div>
				</div>
			</div>
		</div>
	`, city, gender, age, starSign, points, bio)
}

func getProfileImageDisplay(path string) string {
	if path == "" {
		return `<div class="w-full h-full bg-gradient-to-br from-blue-500 to-emerald-500 flex items-center justify-center text-8xl">👤</div>`
	}
	return fmt.Sprintf(`<img src="%s" class="w-full h-full object-cover rounded-3xl">`, path)
}

func deletePost(c *fiber.Ctx) error {
	postID := c.Params("id")
	currentUser := c.Cookies("user")

	if currentUser == "" {
		return c.SendStatus(401)
	}

	var author string
	err := db.QueryRow(ctx, "SELECT author FROM posts WHERE id = $1", postID).Scan(&author)
	if err != nil {
		log.Println("Post not found:", err)
		return c.SendString("Post not found")
	}

	if author != currentUser {
		log.Println("Unauthorized delete attempt by:", currentUser)
		return c.SendString("You can only delete your own posts")
	}

	_, err = db.Exec(ctx, "DELETE FROM posts WHERE id = $1", postID)
	if err != nil {
		log.Println("Error deleting post:", err)
		return c.SendString("Error deleting post")
	}

	return c.SendString("")
}
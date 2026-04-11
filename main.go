package main

import (

	"log"
	"strconv" 
	"time"

	// Ensure this matches your go.mod module name
		"mysocialmediawebsite/pkg/platform"
		goredis "github.com/redis/go-redis/v9"
	"github.com/gofiber/fiber/v2"
	
)

func OnlineMiddleware(c *fiber.Ctx) error {
    username := c.Cookies("user")
    if username != "" {
        // Non-blocking update to Redis
        go func(name string) {
            platform.RDB.ZAdd(platform.Ctx, "active_sessions", goredis.Z{
                Score:  float64(time.Now().Unix()),
                Member: name,
            })
        }(username)
    }
    return c.Next()
}
func main() {
	// 1. Initialize Databases (SQL and Redis)
	initDatabase()
	platform.InitRedis()

	app := fiber.New()
	app.Use(OnlineMiddleware)





	app.Static("/static", "./static")
	app.Static("/uploads", "./uploads")
	initUploadDir()
	app.Static("/uploads", "./uploads/profile_images")

	// --- 1. Auth Routes ---
	app.Get("/signup", signupGet)
	app.Post("/signup", signupPost)
	app.Get("/login", loginGet)
	app.Post("/login", loginPost)
	app.Get("/logout", logoutHandler)

	// --- 2. Main Feed & Post Routes ---
	app.Get("/", homeGet)
	app.Post("/post", postHandler)
	app.Get("/feed", feedHandler)
	app.Get("/post/:id", postViewHandler)
	app.Delete("/post/:id", deletePost)

	// --- 3. Notification Routes ---
	app.Get("/notifications/count", func(c *fiber.Ctx) error {
		currentUser := c.Cookies("user")
		if currentUser == "" {
			return c.SendStatus(204)
		}

		countKey := "notif_count:" + currentUser
		val, err := platform.Redis.Get(countKey)
		
		var finalCount int

		if err != nil {
			// Not in Redis: Get int from SQL
			finalCount = getNotificationCount(currentUser)
			
			// Save to Redis (Convert int to string for storage)
			platform.Redis.Set(countKey, []byte(strconv.Itoa(finalCount)), 5*time.Minute)
		} else {
			// Found in Redis: Convert stored string back to int
			finalCount, _ = strconv.Atoi(string(val))
		}

		// Disable browser caching for live updates
		c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
		
		// finalCount is now an int, matching GetBadgeHTML's expectations
		return c.Type("html").SendString(GetBadgeHTML(finalCount))
	})

	app.Get("/notifications", notificationsHandler)
	app.Get("/notifications/:id", notificationViewHandler)
	app.Get("/notifications/:id/skip", skipNotificationHandler)

	// --- 4. Profile & Settings ---
	app.Get("/profile/:username?", profileHandler)
	app.Get("/change-password", changePasswordGet)
	app.Post("/change-password", changePasswordPost)
	app.Get("/edit-profile-form", editProfileFormHandler)
	app.Post("/update-profile", updateProfileHandler)
	app.Get("/profile-info/:username?", profileInfoHandler)

	// --- 5. Comments ---
	app.Post("/comment/:id", postCommentHandler)
	app.Post("/comment/:id/reply", replyToCommentHandler)
	app.Delete("/comment/:id", deleteCommentHandler)
	app.Get("/post/:id/comments", commentsOnlyHandler)

	// --- 6. Online Status ---
	app.Get("/online-slider", onlineSliderHandler)

	log.Fatal(app.Listen(":3000"))
}
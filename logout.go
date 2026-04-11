package main

import (
"github.com/gofiber/fiber/v2"
"mysocialmediawebsite/pkg/platform"

)


func logoutHandler(c *fiber.Ctx) error {
    // 1. Get the username from the cookie
    username := c.Cookies("user")
    
    if username != "" {
        // 2. Remove the user from the Redis Sorted Set immediately
        // This ensures they disappear from the 'Online' slider instantly
        platform.RDB.ZRem(platform.Ctx, "active_sessions", username)
        
        // 3. Clear the Notification Count Cache (Optional but recommended)
        platform.Redis.Delete("notif_count:" + username)
    }

    // 4. Clear the authentication cookie
    c.ClearCookie("user")

    // 5. Security: Tell the browser to clear any cached sensitive pages
    c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
    c.Set("Pragma", "no-cache")
    c.Set("Expires", "0")

    return c.Redirect("/login") // Usually better to redirect to login than signup
}
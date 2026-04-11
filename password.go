package main

import (
	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
	"strings"
)

func changePasswordGet(c *fiber.Ctx) error {
	currentUser := c.Cookies("user")
	if currentUser == "" {
		return c.Redirect("/login")
	}

	form := `
        <div class="bg-white dark:bg-gray-900 p-10 rounded-3xl shadow-xl w-full max-w-md mx-auto border border-gray-100 dark:border-gray-700">
            <h2 class="text-3xl font-semibold text-center mb-8 text-gray-900 dark:text-white">Change Password</h2>
            <form hx-post="/change-password" hx-target="#result" hx-swap="innerHTML" class="space-y-6">
                <div>
                    <input name="old_password" type="password" placeholder="Current Password" required
                           class="w-full px-5 py-4 bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-2xl focus:border-blue-500 focus:ring-4 focus:ring-blue-100 outline-none text-gray-900 dark:text-gray-100">
                </div>
                <div>
                    <input name="new_password" type="password" placeholder="New Password (min 8 chars)" required minlength="8"
                           class="w-full px-5 py-4 bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-2xl focus:border-blue-500 focus:ring-4 focus:ring-blue-100 outline-none text-gray-900 dark:text-gray-100">
                </div>
                <button type="submit" 
                        class="w-full bg-blue-600 hover:bg-blue-700 transition text-white font-semibold py-4 rounded-2xl text-lg">
                    Update Password
                </button>
            </form>
            <div id="result" class="mt-6 text-center"></div>
            <div class="text-center mt-6">
                <a href="/profile" class="text-blue-600 dark:text-blue-400 hover:underline">← Back to Profile</a>
            </div>
        </div>`

	return c.SendString(Layout("Change Password", form, currentUser))
}

func changePasswordPost(c *fiber.Ctx) error {
	currentUser := c.Cookies("user")
	if currentUser == "" {
		return c.SendString(`<div class="text-red-600">Not logged in</div>`)
	}

	oldPass := c.FormValue("old_password")
	newPass := strings.TrimSpace(c.FormValue("new_password"))

	// Verify old password
	var dbPass string
	err := db.QueryRow(ctx, "SELECT password FROM users WHERE username=$1", currentUser).Scan(&dbPass)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(dbPass), []byte(oldPass)) != nil {
		return c.SendString(`<div class="text-red-600">❌ Incorrect current password</div>`)
	}

	if len(newPass) < 8 {
		return c.SendString(`<div class="text-red-600">❌ New password must be at least 8 characters</div>`)
	}

	// Update password
	hash, _ := bcrypt.GenerateFromPassword([]byte(newPass), 10)
	db.Exec(ctx, "UPDATE users SET password = $1 WHERE username = $2", string(hash), currentUser)

	return c.SendString(`<div class="text-green-600 font-medium">✅ Password changed successfully!</div>`)
}
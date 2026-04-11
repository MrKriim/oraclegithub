package main

import (
	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
	"regexp"
	"strings"
	"time"
)

var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{3,20}$`)

func getCurrentUser(c *fiber.Ctx) string {
	return c.Cookies("user")
}

// ====================== SIGNUP FORM ======================
func renderSignupForm(c *fiber.Ctx, errorMsg string, partial bool) error {
	c.Set("Content-Type", "text/html")

	card := `
        <div id="auth-card" class="bg-white dark:bg-gray-900 p-10 rounded-3xl shadow-xl w-full max-w-md mx-auto border border-gray-100 dark:border-gray-700">
            <div class="text-center mb-8">
                <div class="inline-flex items-center justify-center w-12 h-12 bg-blue-100 dark:bg-blue-900 text-blue-600 dark:text-blue-400 rounded-2xl text-3xl mb-4">👥</div>
                <h2 class="text-3xl font-semibold text-gray-900 dark:text-white">Join Dostana</h2>
                <p class="text-gray-500 dark:text-gray-400 mt-2">Connect with friends in seconds</p>
            </div>`

	if errorMsg != "" {
		card += `<div class="mb-6 bg-red-50 dark:bg-red-900/30 border border-red-200 dark:border-red-700 text-red-600 dark:text-red-400 px-5 py-3 rounded-2xl text-sm">` + errorMsg + `</div>`
	}

	card += `
            <form action="/signup" method="POST" 
                  hx-post="/signup" 
                  hx-target="#auth-card" 
                  hx-swap="outerHTML"
                  class="space-y-6">
                <div>
                    <input name="username" placeholder="Username (3-20 chars)" required maxlength="20" pattern="^[a-zA-Z0-9_]{3,20}$"
                           class="w-full px-5 py-4 bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-2xl focus:border-blue-500 focus:ring-4 focus:ring-blue-100 outline-none text-lg text-gray-900 dark:text-gray-100">
                </div>
                <div>
                    <input name="password" type="password" placeholder="Password (min 8 characters)" required minlength="8"
                           class="w-full px-5 py-4 bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-2xl focus:border-blue-500 focus:ring-4 focus:ring-blue-100 outline-none text-lg text-gray-900 dark:text-gray-100">
                </div>
                <button type="submit" 
                        class="w-full bg-blue-600 hover:bg-blue-700 transition text-white font-semibold py-4 rounded-2xl text-lg shadow-lg">
                    Create account
                </button>
            </form>

            <p class="mt-8 text-center text-sm text-gray-500 dark:text-gray-400">
                Already have an account? 
                <a href="/login" class="text-blue-600 dark:text-blue-400 hover:text-blue-700 dark:hover:text-blue-300 font-medium">Login here</a>
            </p>
        </div>`

	if partial {
		return c.SendString(card)
	}
	return c.SendString(Layout("Sign Up", card, ""))
}

// ====================== LOGIN FORM ======================
func renderLoginForm(c *fiber.Ctx, errorMsg string, partial bool) error {
	c.Set("Content-Type", "text/html")

	card := `
        <div id="auth-card" class="bg-white dark:bg-gray-900 p-10 rounded-3xl shadow-xl w-full max-w-md mx-auto border border-gray-100 dark:border-gray-700">
            <div class="text-center mb-8">
                <div class="inline-flex items-center justify-center w-12 h-12 bg-blue-100 dark:bg-blue-900 text-blue-600 dark:text-blue-400 rounded-2xl text-3xl mb-4">🔑</div>
                <h2 class="text-3xl font-semibold text-gray-900 dark:text-white">Welcome back to Dostana</h2>
                <p class="text-gray-500 dark:text-gray-400 mt-2">Log in to continue</p>
            </div>`

	if errorMsg != "" {
		card += `<div class="mb-6 bg-red-50 dark:bg-red-900/30 border border-red-200 dark:border-red-700 text-red-600 dark:text-red-400 px-5 py-3 rounded-2xl text-sm">` + errorMsg + `</div>`
	}

	card += `
            <form action="/login" method="POST" 
                  hx-post="/login" 
                  hx-target="#auth-card" 
                  hx-swap="outerHTML"
                  class="space-y-6">
                <div>
                    <input name="username" placeholder="Username" required
                           class="w-full px-5 py-4 bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-2xl focus:border-blue-500 focus:ring-4 focus:ring-blue-100 outline-none text-lg text-gray-900 dark:text-gray-100">
                </div>
                <div>
                    <input name="password" type="password" placeholder="Password" required
                           class="w-full px-5 py-4 bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-2xl focus:border-blue-500 focus:ring-4 focus:ring-blue-100 outline-none text-lg text-gray-900 dark:text-gray-100">
                </div>
                <button type="submit" 
                        class="w-full bg-blue-600 hover:bg-blue-700 transition text-white font-semibold py-4 rounded-2xl text-lg shadow-lg">
                    Login
                </button>
            </form>

            <p class="mt-8 text-center text-sm text-gray-500 dark:text-gray-400">
                Don't have an account? 
                <a href="/signup" class="text-blue-600 dark:text-blue-400 hover:text-blue-700 dark:hover:text-blue-300 font-medium">Sign up free</a>
            </p>
        </div>`

	if partial {
		return c.SendString(card)
	}
	return c.SendString(Layout("Login", card, ""))
}

func signupGet(c *fiber.Ctx) error {
	if user := getCurrentUser(c); user != "" {
		return c.Redirect("/")
	}
	return renderSignupForm(c, "", false)
}

func signupPost(c *fiber.Ctx) error {
	username := strings.TrimSpace(c.FormValue("username"))
	password := strings.TrimSpace(c.FormValue("password"))

	if len(username) < 3 || len(username) > 20 || !usernameRegex.MatchString(username) {
		return renderSignupForm(c, "❌ Username must be 3-20 characters and can only contain letters, numbers, and underscores.", true)
	}
	if len(password) < 8 {
		return renderSignupForm(c, "❌ Password must be at least 8 characters long.", true)
	}

	var exists int
	err := db.QueryRow(ctx, "SELECT 1 FROM users WHERE username=$1", username).Scan(&exists)
	if err == nil {
		return renderSignupForm(c, "❌ Username is already taken. Please choose another one.", true)
	}

	hash, _ := bcrypt.GenerateFromPassword([]byte(password), 10)
	_, err = db.Exec(ctx, "INSERT INTO users (username, password) VALUES ($1, $2)", username, string(hash))
	if err != nil {
		return renderSignupForm(c, "❌ Something went wrong. Please try again.", true)
	}

	c.Cookie(&fiber.Cookie{Name: "user", Value: username, Expires: time.Now().Add(24 * time.Hour)})
	c.Set("HX-Redirect", "/")
	return c.SendString(`<div id="auth-card" class="bg-white dark:bg-gray-900 p-10 rounded-3xl shadow-xl w-full max-w-md mx-auto text-center text-green-600 dark:text-green-400 font-medium">✅ Account created successfully!<br>You are now logged in.<br>Redirecting to home...</div>`)
}

func loginGet(c *fiber.Ctx) error {
	if user := getCurrentUser(c); user != "" {
		return c.Redirect("/")
	}
	return renderLoginForm(c, "", false)
}

func loginPost(c *fiber.Ctx) error {
	username := strings.TrimSpace(c.FormValue("username"))
	password := c.FormValue("password")

	var dbPass string
	err := db.QueryRow(ctx, "SELECT password FROM users WHERE username=$1", username).Scan(&dbPass)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(dbPass), []byte(password)) != nil {
		return renderLoginForm(c, "❌ Invalid username or password", true)
	}

	c.Cookie(&fiber.Cookie{Name: "user", Value: username, Expires: time.Now().Add(24 * time.Hour)})
	c.Set("HX-Redirect", "/")
	return c.SendString(`<div id="auth-card" class="bg-white dark:bg-gray-900 p-10 rounded-3xl shadow-xl w-full max-w-md mx-auto text-center text-green-600 dark:text-green-400 font-medium">✅ Login successful!<br>Redirecting to home...</div>`)
}
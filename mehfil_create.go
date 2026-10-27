package main

import (
	"fmt"
	"html"
	"log"
	"strings"

	"github.com/gofiber/fiber/v2"
)

func CreatePage(c *fiber.Ctx) error {
	user, err := requireUser(c)
	if err != nil {
		return err
	}
	errorMessage := c.Query("error")

	// 1. Check how many mehfils the user has already created
	var mehfilCount int
	err = db.QueryRow(ctx, "SELECT COUNT(*) FROM mehfils WHERE admin_username = $1", user).Scan(&mehfilCount)
	if err != nil {
		log.Println("Error counting mehfils:", err)
	}

	// 2. If they hit the limit, set the Urdu error message
	isLimitReached := mehfilCount >= 3
	if isLimitReached {
		errorMessage = "Ap 3 mehfils bana chuke he ab or nahi bana skte"
	}

	errorDiv := ""
	if errorMessage != "" {
		// Using your specific red alert style
		errorDiv = fmt.Sprintf(`
            <div class="mb-6 p-4 bg-red-50 border-l-4 border-red-500 rounded-2xl animate-bounce">
                <div class="flex items-center">
                    <svg class="h-5 w-5 text-red-500" viewBox="0 0 20 20" fill="currentColor">
                        <path fill-rule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM8.707 7.293a1 1 0 00-1.414 1.414L8.586 10l-1.293 1.293a1 1 0 101.414 1.414L10 11.414l1.293 1.293a1 1 0 001.414-1.414L11.414 10l1.293-1.293a1 1 0 00-1.414-1.414L10 8.586 8.707 7.293z" clip-rule="evenodd" />
                    </svg>
                    <p class="ml-3 text-sm font-black text-red-700 uppercase tracking-tight">%s</p>
                </div>
            </div>`, errorMessage)
	}

	// 3. Logic to disable/hide the button if limit reached
	submitButton := `
        <button type="submit" class="w-full bg-emerald-600 text-white py-5 rounded-3xl font-black uppercase tracking-widest hover:bg-emerald-700 transition-all active:scale-95 shadow-xl shadow-emerald-500/20 mt-4">
            Create Mehfil
        </button>`

	if isLimitReached {
		submitButton = `
        <div class="w-full bg-gray-200 dark:bg-gray-800 text-gray-400 py-5 rounded-3xl font-black uppercase tracking-widest text-center mt-4 cursor-not-allowed">
            Limit Reached 🚫
        </div>`
	}

	formContent := fmt.Sprintf(`
    <div class="flex items-center justify-center min-h-[70vh] p-4">
        <form action="/create" method="POST" class="bg-white dark:bg-gray-900 p-8 md:p-10 rounded-[45px] shadow-2xl w-full max-w-md border border-gray-100 dark:border-gray-800">
            <h2 class="text-3xl font-black mb-6 uppercase tracking-tighter text-emerald-600 italic">Mehfil Banao</h2>
            
            %s

            <div class="space-y-4">
                <div>
                    <label class="text-[10px] font-black text-emerald-600 uppercase ml-2 tracking-widest">Mehfil Topic</label>
                    <input type="text" name="topic" placeholder="e.g. Late Night Coding" required 
                        %s
                        class="w-full p-5 bg-gray-50 dark:bg-gray-800 border-none rounded-2xl outline-none focus:ring-2 focus:ring-emerald-500 text-sm font-bold dark:text-white">
                </div>

                <div>
                    <label class="text-[10px] font-black text-emerald-600 uppercase ml-2 tracking-widest">Mehfil Rules</label>
                    <textarea name="rules" placeholder="Be respectful..." required 
                        %s
                        class="w-full p-5 bg-gray-50 dark:bg-gray-800 border-none rounded-2xl h-32 outline-none focus:ring-2 focus:ring-emerald-500 text-sm font-bold resize-none dark:text-white"></textarea>
                </div>

                %s
            </div>

            <p class="text-center text-[10px] text-gray-400 mt-6 font-black uppercase tracking-widest">Cost: 5000 Points</p>
        </form>
    </div>`,
		errorDiv,
		disabledAttr(isLimitReached),
		disabledAttr(isLimitReached),
		submitButton)

	return c.Type("html").SendString(Layout("Mehfil Banao", formContent, user))
}

// Helper function to handle the disabled attribute
func disabledAttr(condition bool) string {
	if condition {
		return "disabled"
	}
	return ""
}
func CreateAction(c *fiber.Ctx) error {
	topic := html.EscapeString(strings.TrimSpace(c.FormValue("topic")))
	rules := html.EscapeString(strings.TrimSpace(c.FormValue("rules")))
	user := getCurrentUser(c)

	if user == "" {
		return c.Redirect("/create?error=Login required")
	}

	// 1. CHECK USER POINTS BALANCE
	var currentPoints int
	err := db.QueryRow(ctx, "SELECT points FROM users WHERE username = $1", user).Scan(&currentPoints)
	if err != nil {
		return c.Redirect("/create?error=Database Error")
	}

	// 2. ENFORCE THE 5,000 POINT LIMIT
	if currentPoints < 5000 {
		return c.Redirect(fmt.Sprintf("/create?error=Insufficient points (Have: %d)", currentPoints))
	}

	// 3. START TRANSACTION
	tx, err := db.Begin(ctx)
	if err != nil {
		return c.Redirect("/create?error=Server Error")
	}
	defer tx.Rollback(ctx)

	// Deduct the points
	_, err = tx.Exec(ctx, "UPDATE users SET points = points - 5000 WHERE username = $1", user)
	if err != nil {
		return c.Redirect("/create?error=Failed to deduct points")
	}

	// Create the Mehfil
	var id int
	err = tx.QueryRow(ctx, "INSERT INTO mehfils (topic, rules, admin_username) VALUES ($1, $2, $3) RETURNING id",
		topic, rules, user).Scan(&id)

	if err != nil {
		return c.Redirect("/create?error=Creation failed")
	}

	// Commit the transaction
	err = tx.Commit(ctx)
	if err != nil {
		return c.Redirect("/create?error=Failed to save")
	}

	return c.Redirect(fmt.Sprintf("/view/%d", id))
}

package main

import (
	"github.com/gofiber/fiber/v2"
)

func SettingsPage(c *fiber.Ctx) error {
	// 1. Get the current user to keep the Layout menus visible
	currentUser := getCurrentUser(c)

	if currentUser == "" {
		return c.Redirect("/login")
	}

	// 2. Pure Inline CSS for the Layout
	settingsBody := `
        <div style="width: 100%; display: flex; flex-direction: column; align-items: center; justify-content: flex-start; min-height: 60vh; padding: 20px; box-sizing: border-box;">
            
            <div style="width: 100%; max-width: 400px; background: #ffffff; border: 1px solid #eeeeee; border-radius: 40px; padding: 30px 20px; box-sizing: border-box; box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.1); text-align: center; display: flex; flex-direction: column; gap: 16px;">
                
                <h2 style="margin: 0 0 10px 0; font-family: sans-serif; color: #333; font-size: 18px; font-weight: 800; text-transform: uppercase; letter-spacing: 2px;">Menu</h2>

                <a href="/popular" hx-boost="true"
                   style="display: flex; align-items: center; justify-content: center; width: 100%; padding: 18px 20px; box-sizing: border-box; background: linear-gradient(to right, #f59e0b, #ea580c); color: white; text-decoration: none; border-radius: 20px; font-weight: 800; text-transform: uppercase; letter-spacing: 1.5px; font-size: 14px; box-shadow: 0 10px 15px -3px rgba(245, 158, 11, 0.3);">
                    
                    <svg style="width: 22px; height: 22px; margin-right: 12px;" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" stroke-linecap="round" stroke-linejoin="round"/>
                        <circle cx="9" cy="7" r="4" stroke-linecap="round" stroke-linejoin="round"/>
                        <path d="M23 21v-2a4 4 0 0 0-3-3.87" stroke-linecap="round" stroke-linejoin="round"/>
                        <path d="M16 3.13a4 4 0 0 1 0 7.75" stroke-linecap="round" stroke-linejoin="round"/>
                    </svg>
                    
                    <span>Mehfil</span>
                </a>

                <a href="/search" hx-boost="true"
                   style="display: flex; align-items: center; justify-content: center; width: 100%; padding: 18px 20px; box-sizing: border-box; background: linear-gradient(to right, #3b82f6, #2563eb); color: white; text-decoration: none; border-radius: 20px; font-weight: 800; text-transform: uppercase; letter-spacing: 1.5px; font-size: 14px; box-shadow: 0 10px 15px -3px rgba(59, 130, 246, 0.3);">
                    
                    <svg style="width: 22px; height: 22px; margin-right: 12px;" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <circle cx="11.5" cy="11.5" r="9.5"/>
                        <path d="M22 22L20 20" stroke-linecap="round"/>
                    </svg>
                    
                   <span>Search</span>
                 </a>

                <a href="/emojis" hx-boost="true"
                   style="display: flex; align-items: center; justify-content: center; width: 100%; padding: 18px 20px; box-sizing: border-box; background: linear-gradient(to right, #10b981, #059669); color: white; text-decoration: none; border-radius: 20px; font-weight: 800; text-transform: uppercase; letter-spacing: 1.5px; font-size: 14px; box-shadow: 0 10px 15px -3px rgba(16, 185, 129, 0.3);">
                    <span>😀 Emojis</span>
                </a>

                <a href="/rules" hx-boost="true"
                   style="display: flex; align-items: center; justify-content: center; width: 100%; padding: 18px 20px; box-sizing: border-box; background: linear-gradient(to right, #6b7280, #4b5563); color: white; text-decoration: none; border-radius: 20px; font-weight: 800; text-transform: uppercase; letter-spacing: 1.5px; font-size: 14px; box-shadow: 0 10px 15px -3px rgba(107, 114, 128, 0.3);">
                    <span>📜 Rules</span>
                </a>

                <a href="/about" hx-boost="true"
                   style="display: flex; align-items: center; justify-content: center; width: 100%; padding: 18px 20px; box-sizing: border-box; background: linear-gradient(to right, #6b7280, #4b5563); color: white; text-decoration: none; border-radius: 20px; font-weight: 800; text-transform: uppercase; letter-spacing: 1.5px; font-size: 14px; box-shadow: 0 10px 15px -3px rgba(107, 114, 128, 0.3);">
                    <span>ℹ️ About Us</span>
                </a>

                <a href="/terms" hx-boost="true"
                   style="display: flex; align-items: center; justify-content: center; width: 100%; padding: 18px 20px; box-sizing: border-box; background: linear-gradient(to right, #6b7280, #4b5563); color: white; text-decoration: none; border-radius: 20px; font-weight: 800; text-transform: uppercase; letter-spacing: 1.5px; font-size: 14px; box-shadow: 0 10px 15px -3px rgba(107, 114, 128, 0.3);">
                    <span>📄 Terms & Conditions</span>
                </a>

                <a href="/privacy" hx-boost="true"
                   style="display: flex; align-items: center; justify-content: center; width: 100%; padding: 18px 20px; box-sizing: border-box; background: linear-gradient(to right, #6b7280, #4b5563); color: white; text-decoration: none; border-radius: 20px; font-weight: 800; text-transform: uppercase; letter-spacing: 1.5px; font-size: 14px; box-shadow: 0 10px 15px -3px rgba(107, 114, 128, 0.3);">
                    <span>🔒 Privacy Policy</span>
                </a>

                <div style="height: 1px; background: #e5e7eb; margin: 10px 0; border-radius: 1px;"></div> 

                <a href="/logout"
                   style="display: flex; align-items: center; justify-content: center; width: 100%; padding: 18px 20px; box-sizing: border-box; background: linear-gradient(to right, #ef4444, #e11d48); color: white; text-decoration: none; border-radius: 20px; font-weight: 800; text-transform: uppercase; letter-spacing: 1.5px; font-size: 14px; box-shadow: 0 10px 15px -3px rgba(239, 68, 68, 0.3);">
                    
                    <svg style="width: 22px; height: 22px; margin-right: 12px;" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
                        <path d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1" />
                    </svg>
                    
                    <span>Logout</span>
                </a>

            </div>

        </div>`

	return c.Type("html").SendString(Layout("Settings", settingsBody, currentUser))
}

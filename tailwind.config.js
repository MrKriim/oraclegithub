/** @type {import('tailwindcss').Config} */
module.exports = {
  // 1. Ensure it scans everything. 
  // If you have files in subfolders, use "**/*.go"
  content: [
    "./*.go", 
    "./templates/**/*.html", 
    "./static/**/*.js"
  ],
  
  // 2. IMPORTANT: You are using a 'dark' class toggle in layout.go.
  // Without this line, all "dark:" classes are ignored!
  darkMode: 'class', 
  
  theme: {
    extend: {
      screens: {
        'xs': '480px', // Re-adding the custom screen you had in layout.go
      },
    },
  },
  plugins: [],
}
/** @type {import('tailwindcss').Config} */
module.exports = {
    darkMode: 'class',
    content: [
        "./**/*.go",
        "./static/js/**/*.js",
    ],
    // Manual Safelist: These will be forced into style.css no matter what
    safelist: [
        'dark',
        'dark:bg-gray-950',
        'dark:bg-gray-900',
        'dark:text-gray-100',
        'dark:text-white',
        'dark:border-gray-800'
    ],
    theme: {
        extend: {},
    },
    plugins: [],
}
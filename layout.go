package main


import "fmt"

func Layout(title, body, currentUser string) string {
    
    darkToggle := `
        <button onclick="toggleDarkMode()" 
                class="w-9 h-9 flex items-center justify-center text-gray-500 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 rounded-2xl transition shrink-0"
                aria-label="Toggle dark mode">
            <span id="theme-icon" class="text-xl">🌙</span>
        </button>`

    nav := ""
    if currentUser != "" {
        count := getNotificationCount(currentUser)
        
        // This div now knocks on the server's door every 1 second.
        // hx-disinherit="*" stops the global body boost from stopping the timer.
        notificationBadge := fmt.Sprintf(`
            <div id="notif-badge-container" 
                 hx-get="/notifications/count" 
                 hx-trigger="every 1s" 
                 hx-swap="innerHTML"
                 hx-disinherit="*">
                %s
            </div>`, GetBadgeHTML(count))

        nav = fmt.Sprintf(`
            <nav class="w-full max-w-2xl flex flex-wrap items-center justify-between gap-3 mb-8 bg-white dark:bg-gray-900 p-4 rounded-3xl shadow-sm border border-gray-100 dark:border-gray-700">
                <a href="/" class="font-bold text-2xl tracking-tight text-blue-600 dark:text-blue-400 flex items-center gap-2 shrink-0" hx-boost="true">
                    <span>Dostana</span>
                </a>
                <div class="flex items-center gap-3 sm:gap-5 text-sm flex-wrap">
                    %s
                    <a href="/profile" class="text-gray-600 hover:text-blue-600 dark:text-gray-300 dark:hover:text-blue-400 transition" hx-boost="true">Profile</a>
                    %s
                    <a href="/logout" class="text-red-500 hover:text-red-600 dark:hover:text-red-400 transition font-medium" hx-boost="true">Logout</a>
                </div>
            </nav>`, notificationBadge, darkToggle)
    } else {
        nav = fmt.Sprintf(`
            <nav class="w-full max-w-2xl flex flex-wrap items-center justify-between gap-3 mb-8 bg-white dark:bg-gray-900 p-4 rounded-3xl shadow-sm border border-gray-100 dark:border-gray-700">
                <a href="/" class="font-bold text-2xl tracking-tight text-blue-600 dark:text-blue-400 flex items-center gap-2 shrink-0" hx-boost="true">
                    <span>Dostana</span>
                </a>
                <div class="flex items-center gap-3 sm:gap-6 text-sm flex-wrap">
                    <a href="/login" class="text-gray-600 hover:text-blue-600 dark:text-gray-300 dark:hover:text-blue-400 transition" hx-boost="true">Login</a>
                    <a href="/signup" class="text-blue-600 hover:text-blue-700 dark:text-blue-400 dark:hover:text-blue-300 transition font-medium" hx-boost="true">Sign Up</a>
                    %s
                </div>
            </nav>`, darkToggle)
    }

    return fmt.Sprintf(`
        <!DOCTYPE html>
        <html lang="en" class="h-full">
        <head>
            <meta charset="UTF-8">
            <meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=5.0, user-scalable=yes">
            <title>Dostana - %s</title>
            <link href="/static/css/style.css" rel="stylesheet">
            <script src="/static/js/idiomorph.min.js"></script>
<script src="/static/js/morphdom.js"></script>
            <script>
                tailwind.config = {
                    darkMode: 'class',
                    theme: { extend: { screens: { 'xs': '480px' } } }
                }
            </script>
          <script src="/static/js/htmx.min.js"></script>
            <style>
                @import url('https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600&display=swap');
                body { font-family: 'Inter', system-ui, sans-serif; }
            </style>
            <script>
                function toggleDarkMode() {
                    const html = document.documentElement;
                    const isDark = !html.classList.contains('dark');
                    html.classList.toggle('dark', isDark);
                    document.getElementById('theme-icon').textContent = isDark ? '☀️' : '🌙';
                    localStorage.setItem('darkMode', isDark);
                }
                function initDarkMode() {
                    const saved = localStorage.getItem('darkMode');
                    const html = document.documentElement;
                    const icon = document.getElementById('theme-icon');
                    if (saved === 'true') {
                        html.classList.add('dark');
                        if (icon) icon.textContent = '☀️';
                    } else {
                        html.classList.remove('dark');
                        if (icon) icon.textContent = '🌙';
                    }
                }
                window.addEventListener('DOMContentLoaded', initDarkMode);
                document.addEventListener('htmx:afterSwap', initDarkMode);
                document.addEventListener('htmx:load', initDarkMode);
                  
                document.addEventListener('htmx:trigger', function(evt) {
                    if (evt.detail.name === 'updateOnlineCount') {
                        const countEl = document.getElementById('online-count');
                        if (countEl) countEl.textContent = evt.detail.count;
                    }
                });

                document.body.addEventListener('htmx:afterRequest', function(evt) {
                    if (evt.detail.pathInfo.requestPath === '/post' && evt.detail.successful) {
                        const postInput = document.querySelector('input[name="content"]');
                        if (postInput) postInput.value = '';
                    }
                });

                function toggleCommentReply(id) {
                    const form = document.getElementById('reply-to-' + id);
                    if (form) form.classList.toggle('hidden');
                }
            </script>




<script>
    let isTyping = false;

    document.addEventListener('htmx:beforeRequest', function(evt) {
        let activeEl = document.activeElement;
        // Pause feed updates if user is focused on an input or we have flagged isTyping
        if (isTyping || activeEl.tagName === 'INPUT' || activeEl.tagName === 'TEXTAREA') {
            // Adjust the target ID to match your main feed container
            if (evt.detail.target.id === 'feed-container' || evt.detail.target.id === 'posts-list') {
                evt.preventDefault(); 
            }
        }
    });

    // Reset flag after any successful HTMX request
    document.addEventListener('htmx:afterRequest', function(evt) {
        if (evt.detail.successful) {
            isTyping = false;
        }
    });
</script>

        </head>
        <body hx-boost="true" class="h-full bg-gray-50 dark:bg-gray-950 text-gray-900 dark:text-gray-100 flex flex-col items-center p-3 sm:p-4 md:p-6">
            %s
            <main class="w-full max-w-2xl flex-1">
                %s
            </main>
        </body>
        </html>
    `, title, nav, body)
}




func GetBadgeHTML(count int) string {
    badge := ""
    if count > 0 {
        badge = fmt.Sprintf(`
            <span class="absolute -top-1 -right-1 bg-red-500 text-white text-[10px] w-4 h-4 flex items-center justify-center rounded-full font-bold animate-pulse">%d</span>
        `, count)
    }
    return fmt.Sprintf(`
        <a href="/notifications" hx-boost="true" class="relative inline-flex items-center hover:scale-110 transition-transform">
            <span class="text-xl">🔔</span>
            %s
        </a>`, badge)
}
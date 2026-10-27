package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/gofiber/fiber/v2"
)

func formatCommas(num int) string {
	str := fmt.Sprintf("%d", num)
	if len(str) <= 3 {
		return str
	}
	var result []byte
	length := len(str)
	for i, runeValue := range str {
		if i > 0 && (length-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(runeValue))
	}
	return string(result)
}

func AuthRequiredGuard(c *fiber.Ctx) error {
	if _, err := requireUser(c); err != nil {
		return err
	}
	return c.Next()
}

func PointsEarnPageHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Redirect("/login")
	}

	var currentPoints int
	var lastClaimTime *time.Time
	query := "SELECT points, last_points_claim FROM users WHERE username = $1"
	err := db.QueryRow(context.Background(), query, currentUser).Scan(&currentPoints, &lastClaimTime)
	if err != nil {
		log.Printf("Error fetching user metrics for %s: %v", currentUser, err)
		currentPoints = 0
	}

	initialSecondsLeft := 0
	initialMessage := ""
	if lastClaimTime != nil {
		elapsed := time.Since(*lastClaimTime)
		if elapsed < 2*time.Hour {
			timeLeft := (2 * time.Hour) - elapsed
			initialSecondsLeft = int(timeLeft.Seconds())
			hours := int(timeLeft.Hours())
			minutes := int(timeLeft.Minutes()) % 60
			initialMessage = fmt.Sprintf("Aap apna reward le chukay hain. Agla reward %d ghantay aur %d minute baad milega.", hours, minutes)
		}
	}

	formattedPoints := formatCommas(currentPoints)

	hideNoticeClass := ""
	if initialSecondsLeft <= 0 {
		hideNoticeClass = "hidden"
	}

	disabledButtonClass := ""
	if initialSecondsLeft > 0 {
		disabledButtonClass = "opacity-50 cursor-not-allowed pointer-events-none"
	}

	body := fmt.Sprintf(`
    <div class="w-full max-w-lg mx-auto p-4 mt-6 px-4">

        <!-- Adblock Notice -->
        <div id="adblock-notice" class="hidden w-full bg-white dark:bg-slate-900 border-2 border-dashed border-amber-500 rounded-3xl p-8 text-center shadow-2xl space-y-5 transition-all duration-300">
            <div class="inline-flex items-center justify-center w-16 h-16 rounded-full bg-amber-500/10 text-amber-500 mb-2">
                <svg width="32" height="32" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
                </svg>
            </div>
            <h2 class="text-xl font-black text-slate-800 dark:text-slate-100 tracking-tight">Ad-Blocker Detected</h2>
            <p class="text-xs text-slate-600 dark:text-slate-400 leading-relaxed font-medium">
                Points claim karne ke liye apna <b>Ad-Blocker, Brave Shield ya browser extension off karein</b>, phir page reload karein.
            </p>
            <div class="pt-2">
                <button onclick="window.location.reload();" class="w-full bg-amber-500 hover:bg-amber-600 text-slate-950 font-black py-3 px-6 rounded-2xl text-xs uppercase tracking-widest shadow-md transition-all">
                    🔄 Off Kar Diya — Reload Karein
                </button>
            </div>
        </div>

        <!-- Main Dashboard -->
        <div id="main-dashboard-content" class="flex flex-col items-center space-y-6">

            <!-- Balance Card -->
            <div class="w-full bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-3xl p-6 text-center shadow-xl transition-colors duration-300">
                <div class="inline-flex items-center justify-center w-12 h-12 rounded-full bg-amber-500/10 text-amber-500 mb-2 animate-pulse">
                    <svg width="24" height="24" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
                    </svg>
                </div>
                <h2 class="text-2xl font-black text-slate-800 dark:text-amber-400 tracking-tight">⚡ Free Points Dashboard</h2>
                <p class="text-xs text-slate-500 dark:text-slate-400 mt-1 font-medium">Chotay kaam poore karein aur muft points kamaen</p>
                <div class="mt-6 inline-block bg-slate-50 dark:bg-slate-950 px-10 py-4 rounded-2xl border border-slate-200 dark:border-slate-800 shadow-inner w-full sm:w-auto">
                    <span class="text-slate-400 dark:text-slate-500 text-[11px] font-bold block uppercase tracking-widest mb-1">Aap ka mojuda balance</span>
                    <span id="display-balance" class="text-3xl font-black text-slate-800 dark:text-slate-100 tracking-tight tabular-nums">%[1]s Points</span>
                </div>
            </div>

            <!-- Claim Card -->
            <div class="w-full bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-3xl p-6 flex flex-col items-center text-center space-y-5 shadow-xl transition-colors duration-300">
                <div class="space-y-2">
                    <div class="bg-emerald-500/10 text-emerald-500 px-3 py-1 rounded-full text-[10px] font-black tracking-wide inline-block uppercase animate-bounce">
                        Bari Offer
                    </div>
                    <h3 class="font-black text-lg text-slate-800 dark:text-slate-100">Ek click par 10,000 points payen</h3>
                    <p class="text-xs text-slate-500 dark:text-slate-400 max-w-sm mx-auto leading-relaxed font-medium">
                        Neeche diye gaye button par click karein. Ad link khulega aur aap ke account mein points aa jayenge.
                    </p>
                </div>

                <div id="rate-limit-notice" class="%[2]s w-full text-center text-xs font-bold text-red-500 bg-red-500/10 border border-red-500/20 py-2.5 px-4 rounded-xl">
                    %[3]s
                </div>

                <div class="w-full pt-2">
                    <a href="https://www.effectivecpmnetwork.com/rhp94w3rfg?key=3f6989cfd0a68fc635fc80c2297096fa"
                       target="_blank"
                       rel="noopener noreferrer"
                       id="claim-btn-node"
                       onclick="executePointClaimSequence(event);"
                       class="%[4]s w-full flex items-center justify-center bg-gradient-to-r from-amber-400 to-amber-500 hover:from-amber-500 hover:to-amber-600 text-slate-950 font-black py-4 px-6 rounded-2xl text-xs uppercase tracking-widest shadow-lg active:scale-[0.98] transition-all duration-150 cursor-pointer">
                       🔥 Abhi Points Hasil Karein
                    </a>
                </div>
            </div>

            <!-- Support Tip -->
            <div class="w-full bg-gradient-to-br from-amber-500/5 to-orange-500/5 dark:from-amber-500/10 dark:to-orange-500/10 border border-amber-500/20 rounded-3xl p-5 text-center shadow-md">
                <h4 class="font-black text-sm text-amber-600 dark:text-amber-400 uppercase tracking-wider mb-1">Dostana Support Tip</h4>
                <p class="text-xs text-slate-600 dark:text-slate-400 leading-relaxed max-w-sm mx-auto">
                    Dostana ko support karne ke liye neche diye gaye ads daily open karein — jaise hi ad khule, wapas Dostana par aa jayein.
                </p>
            </div>

            <!-- Native Ad Slot -->
            <div class="w-full flex justify-center items-center overflow-hidden rounded-2xl bg-slate-100 dark:bg-slate-900/50 p-2 border border-slate-200 dark:border-slate-800">
                <div id="container-7c044f7129511a96700c27356fdf1c3f"></div>
            </div>

        </div>
    </div>

    <!-- Adsterra Native Ad Script -->
    <script async="async" data-cfasync="false" src="https://pl21109922.effectivecpmnetwork.com/7c044f7129511a96700c27356fdf1c3f/invoke.js"></script>

    <!-- Adsterra Popunder Script -->
    <script src="https://pl21109900.effectivecpmnetwork.com/d7/05/81/d705818c81a8336f13765edc7c8861eb.js"></script>

    <!-- Adblock Detection -->
    <script type="text/javascript">
        window.addEventListener('DOMContentLoaded', () => {
            setTimeout(() => {
                const dashboardNode = document.getElementById('main-dashboard-content');
                const adblockNoticeNode = document.getElementById('adblock-notice');
                const nativeAdContainer = document.getElementById('container-7c044f7129511a96700c27356fdf1c3f');

                let isAdblockActive = false;

                if (nativeAdContainer && nativeAdContainer.offsetHeight === 0) {
                    isAdblockActive = true;
                }

                let bait = document.createElement('div');
                bait.className = 'adsbox ads google-ads ads-server advertisement';
                bait.setAttribute('style', 'width: 1px; height: 1px; position: absolute; top: -10px; left: -10px;');
                document.body.appendChild(bait);
                if (bait.offsetHeight === 0 || bait.clientWidth === 0 || window.getComputedStyle(bait).display === 'none') {
                    isAdblockActive = true;
                }
                bait.remove();

                if (isAdblockActive) {
                    if (dashboardNode) dashboardNode.classList.add('hidden');
                    if (adblockNoticeNode) adblockNoticeNode.classList.remove('hidden');
                }
            }, 1200);
        });
    </script>

    <!-- Countdown & Claim Logic -->
    <script type="text/javascript">
        let secondsLeft = %[5]d;
        const noticeNode = document.getElementById('rate-limit-notice');
        const buttonNode = document.getElementById('claim-btn-node');

        if (secondsLeft > 0) {
            startCountdownEngine();
        }

        function formatRomanUrduTime(totalSeconds) {
            let h = Math.floor(totalSeconds / 3600);
            let m = Math.floor((totalSeconds %% 3600) / 60);
            return "Aap apna reward le chukay hain. Agla reward " + h + " ghantay aur " + m + " minute baad milega.";
        }

        function startCountdownEngine() {
            if (buttonNode) buttonNode.classList.add('opacity-50', 'cursor-not-allowed', 'pointer-events-none');

            let interval = setInterval(() => {
                secondsLeft--;
                if (secondsLeft <= 0) {
                    clearInterval(interval);
                    if (buttonNode) buttonNode.classList.remove('opacity-50', 'cursor-not-allowed', 'pointer-events-none');
                    if (noticeNode) noticeNode.classList.add('hidden');
                } else {
                    if (noticeNode) noticeNode.innerText = formatRomanUrduTime(secondsLeft);
                }
            }, 1000);
        }

        function executePointClaimSequence(event) {
            if (secondsLeft > 0) {
                event.preventDefault();
                return false;
            }

            secondsLeft = 7200;
            startCountdownEngine();
            if (noticeNode) noticeNode.classList.remove('hidden');

            fetch('/claim-bonus-points', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' }
            })
            .then(res => {
                if (res.ok) {
                    const balanceElement = document.getElementById('display-balance');
                    if (balanceElement) {
                        let currentVal = parseInt(balanceElement.innerText.replace(/,/g, '')) || 0;
                        let updatedSum = currentVal + 10000;
                        balanceElement.innerText = updatedSum.toLocaleString() + " Points";
                    }
                } else if (res.status === 429) {
                    res.json().then(data => {
                        secondsLeft = data.seconds_left || 7200;
                        if (noticeNode) {
                            noticeNode.innerText = data.error_msg;
                            noticeNode.classList.remove('hidden');
                        }
                    });
                } else {
                    console.error("Server pipeline error.");
                }
            })
            .catch(err => {
                console.error("Network sync failure:", err);
            });
        }
    </script>
    `,
		formattedPoints,     // %[1]s
		hideNoticeClass,     // %[2]s
		initialMessage,      // %[3]s
		disabledButtonClass, // %[4]s
		initialSecondsLeft,  // %[5]d
	)

	htmlResponse := Layout("Points Kamaen", body, currentUser)
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(htmlResponse)
}

// PostClaimPointsHandler processes the asynchronous wallet addition back-end logic via Fiber POST route
func PostClaimPointsHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	if currentUser == "" {
		return c.Status(fiber.StatusUnauthorized).SendString("Unauthorized session context")
	}

	// SERVER-SIDE 2-HOUR ENFORCEMENT
	var lastClaimTime *time.Time
	checkQuery := "SELECT last_points_claim FROM users WHERE username = $1"
	err := db.QueryRow(context.Background(), checkQuery, currentUser).Scan(&lastClaimTime)

	if err == nil && lastClaimTime != nil {
		elapsed := time.Since(*lastClaimTime)
		if elapsed < 2*time.Hour {
			timeLeft := (2 * time.Hour) - elapsed
			hoursLeft := int(timeLeft.Hours())
			minutesLeft := int(timeLeft.Minutes()) % 60
			secondsLeft := int(math.Ceil(timeLeft.Seconds()))

			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"seconds_left": secondsLeft,
				"error_msg":    fmt.Sprintf("Sabar Karein: Aap aglay %d ghantay aur %d minute baad hi koshish kar saktay hain.", hoursLeft, minutesLeft),
			})
		}
	}

	// +10000 points
	query := `
        UPDATE users 
        SET points = points + 10000,
            last_points_claim = NOW()
        WHERE username = $1;
    `
	_, err = db.Exec(context.Background(), query, currentUser)
	if err != nil {
		log.Printf("Critical: Database reward increment crash for %s: %v", currentUser, err)
		return c.Status(fiber.StatusInternalServerError).SendString("Internal state sync failure")
	}

	return c.Status(fiber.StatusOK).SendString("Points processed successfully")
}

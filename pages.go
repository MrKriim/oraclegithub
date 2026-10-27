package main

import (
	"github.com/gofiber/fiber/v2"
)

// ─────────────────────────────────────────────────────────────────────────────
// STATIC INFO PAGES — About Us / Terms & Conditions / Privacy Policy
// ─────────────────────────────────────────────────────────────────────────────
// Plain static content, no DB reads. Content-Type is set explicitly on
// every one — see the comment on rulesPageHandler for why that matters.

func aboutPageHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	c.Set("Content-Type", "text/html")

	body := `
	<div class="mehfil-container" style="max-width:720px;">
		<div style="background:#fff;border-radius:24px;padding:28px 22px;border:1px solid #f3f4f6;box-shadow:0 10px 40px rgba(0,0,0,0.06);">
			<h1 style="font-size:26px;font-weight:900;margin-bottom:14px;">ℹ️ About Dostana</h1>
			<div style="font-size:14px;color:#374151;line-height:1.8;">
				<p>Dostana is a Pakistani social networking website built for people who want a simple,
				lightweight place to share posts, photos, videos, and voice messages, chat with friends,
				and hang out in topic-based group chats called Mehfils.</p>
				<p>Dostana was created and is maintained by <strong>Kriim</strong>. It was built as a
				modern, actively-maintained alternative for the community that grew up around older
				Pakistani social platforms like Damadam.</p>
				<p>The site is intentionally simple: create an account with a username and password,
				start posting, and join in. There are no unnecessary steps and no selling of personal
				data.</p>
				<p>Have a question, found a bug, or want to report abuse? See our
				<a href="/rules" style="color:#2563eb;font-weight:700;">Rules</a> page, or reach out
				using the contact details on our <a href="/privacy" style="color:#2563eb;font-weight:700;">Privacy Policy</a> page.</p>
			</div>
		</div>
	</div>`

	return c.SendString(Layout("About Us • Dostana", body, currentUser))
}

func termsPageHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	c.Set("Content-Type", "text/html")

	body := `
	<div class="mehfil-container" style="max-width:720px;">
		<div style="background:#fff;border-radius:24px;padding:28px 22px;border:1px solid #f3f4f6;box-shadow:0 10px 40px rgba(0,0,0,0.06);">
			<h1 style="font-size:26px;font-weight:900;margin-bottom:14px;">📄 Terms &amp; Conditions</h1>
			<div style="font-size:14px;color:#374151;line-height:1.8;">
				<p>By creating an account or using Dostana ("the website", "we", "us"), you agree to
				these Terms &amp; Conditions and to our
				<a href="/rules" style="color:#2563eb;font-weight:700;">Website Rules</a>, which are
				incorporated into these terms by reference.</p>

				<h2 style="font-size:16px;font-weight:800;margin-top:20px;">1. Eligibility</h2>
				<p>You must provide accurate account information and are responsible for keeping your
				password confidential and for all activity under your account.</p>

				<h2 style="font-size:16px;font-weight:800;margin-top:20px;">2. Content</h2>
				<p>You are solely responsible for anything you post, upload, or send on Dostana.
				Content that violates our Rules — including nudity, gore, harassment, hate speech,
				misinformation, or spam — may be removed, and the responsible account may be warned,
				restricted, or permanently banned at our discretion.</p>

				<h2 style="font-size:16px;font-weight:800;margin-top:20px;">3. Account Suspension &amp; Bans</h2>
				<p>We may suspend or permanently ban any account that violates our Rules or these
				Terms, with or without prior notice, for a fixed duration or permanently, as described
				on the Rules page.</p>

				<h2 style="font-size:16px;font-weight:800;margin-top:20px;">4. Advertising</h2>
				<p>Dostana displays advertising, including through Google AdSense, to help cover the
				cost of running the service. See our
				<a href="/privacy" style="color:#2563eb;font-weight:700;">Privacy Policy</a> for
				details on how advertising partners may use cookies.</p>

				<h2 style="font-size:16px;font-weight:800;margin-top:20px;">5. No Warranty</h2>
				<p>Dostana is provided "as is" without warranties of any kind. We do not guarantee
				uninterrupted or error-free service.</p>

				<h2 style="font-size:16px;font-weight:800;margin-top:20px;">6. Changes</h2>
				<p>We may update these Terms or our Rules at any time. Continued use of Dostana after a
				change means you accept the updated terms.</p>

				<h2 style="font-size:16px;font-weight:800;margin-top:20px;">7. Governing Law</h2>
				<p>These Terms are governed by the laws of Pakistan.</p>
			</div>
		</div>
	</div>`

	return c.SendString(Layout("Terms & Conditions • Dostana", body, currentUser))
}

func privacyPageHandler(c *fiber.Ctx) error {
	currentUser := getCurrentUser(c)
	c.Set("Content-Type", "text/html")

	body := `
	<div class="mehfil-container" style="max-width:720px;">
		<div style="background:#fff;border-radius:24px;padding:28px 22px;border:1px solid #f3f4f6;box-shadow:0 10px 40px rgba(0,0,0,0.06);">
			<h1 style="font-size:26px;font-weight:900;margin-bottom:14px;">🔒 Privacy Policy</h1>
			<div style="font-size:14px;color:#374151;line-height:1.8;">
				<p>This Privacy Policy explains what information Dostana collects and how it is used.</p>

				<h2 style="font-size:16px;font-weight:800;margin-top:20px;">1. What We Collect</h2>
				<p>To create an account, we only collect a <strong>username</strong> and
				<strong>password</strong>. Your password is stored as a one-way cryptographic hash and
				is never stored or visible in plain text. An email address is not currently required;
				optional email verification may be introduced in the future, and it will always remain
				optional unless we clearly state otherwise at that time.</p>
				<p>For security and anti-abuse purposes (preventing spam accounts, ban evasion, and
				fraud), we also automatically record technical signals such as your IP address, a
				derived network subnet, browser/device characteristics, and timestamps of activity.
				This information is used only for moderation and security, and is not sold to third
				parties.</p>

				<h2 style="font-size:16px;font-weight:800;margin-top:20px;">2. Content You Post</h2>
				<p>Posts, comments, photos, videos, and voice messages you choose to upload are stored
				so the service can display them to other users, according to the visibility settings
				you choose (e.g. followers-only).</p>

				<h2 style="font-size:16px;font-weight:800;margin-top:20px;">3. Cookies &amp; Advertising</h2>
				<p>Dostana uses cookies necessary for login sessions and basic site functionality.</p>
				<p>We also display advertising through <strong>Google AdSense</strong>. Google, as a
				third-party vendor, uses cookies to serve ads based on a user's prior visits to this
				and other websites. This enables Google and its partners to serve ads based on a
				visit to this site and/or other sites on the Internet. You may opt out of personalized
				advertising by visiting
				<a href="https://adssettings.google.com/" target="_blank" rel="noopener" style="color:#2563eb;font-weight:700;">Google Ads Settings</a>.</p>

				<h2 style="font-size:16px;font-weight:800;margin-top:20px;">4. How We Use Information</h2>
				<p>We use the information above to operate the service, keep accounts secure, enforce
				our <a href="/rules" style="color:#2563eb;font-weight:700;">Rules</a>, respond to abuse
				reports, and improve the site. We do not sell your personal information.</p>

				<h2 style="font-size:16px;font-weight:800;margin-top:20px;">5. Data Retention</h2>
				<p>Account and content data is retained for as long as your account is active. You may
				contact us to request account deletion.</p>

				<h2 style="font-size:16px;font-weight:800;margin-top:20px;">6. Children's Privacy</h2>
				<p>Dostana is not directed at children under 13, and we do not knowingly collect
				personal information from children under 13.</p>

				<h2 style="font-size:16px;font-weight:800;margin-top:20px;">7. Changes to This Policy</h2>
				<p>We may update this Privacy Policy from time to time. Changes will be posted on this
				page.</p>

				<h2 style="font-size:16px;font-weight:800;margin-top:20px;">8. Contact</h2>
				<p>Questions about this policy can be sent to <strong>support@dostana.biz</strong>.</p>
			</div>
		</div>
	</div>`

	return c.SendString(Layout("Privacy Policy • Dostana", body, currentUser))
}
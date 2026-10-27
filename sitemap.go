package main

import (
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// ─────────────────────────────────────────────────────────────────────────────
// SITEMAP INDEX — /sitemap.xml
// Points Google at the separate profile and post sitemaps below instead of
// listing everything in one file. This is Google's own recommended pattern
// once a site has more than a few thousand indexable URLs, and it lets each
// sub-sitemap be regenerated/cached independently.
// ─────────────────────────────────────────────────────────────────────────────

func SitemapHandler(c *fiber.Ctx) error {
	now := time.Now().Format("2006-01-02")

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")

	b.WriteString(fmt.Sprintf(`  <sitemap>
    <loc>https://dostana.biz/sitemap-profiles.xml</loc>
    <lastmod>%s</lastmod>
  </sitemap>
`, now))

	// Posts are paginated (500/page) so no single sitemap file ever exceeds
	// Google's 50,000-URL / 50MB limit even as the site grows.
	var totalPosts int
	db.QueryRow(ctx, `
		SELECT COUNT(*) FROM posts p
		JOIN users u ON u.username = p.author
		WHERE COALESCE(u.is_banned,false) = false
		  AND COALESCE(u.is_private,false) = false
		  AND COALESCE(u.account_status,'approved') = 'approved'
		  AND (p.expires_at IS NULL OR p.expires_at > NOW())`).Scan(&totalPosts)

	const perPage = 500
	pages := (totalPosts + perPage - 1) / perPage
	if pages == 0 {
		pages = 1
	}
	for i := 1; i <= pages; i++ {
		b.WriteString(fmt.Sprintf(`  <sitemap>
    <loc>https://dostana.biz/sitemap-posts.xml?page=%d</loc>
    <lastmod>%s</lastmod>
  </sitemap>
`, i, now))
	}

	b.WriteString(`</sitemapindex>`)
	c.Set("Content-Type", "application/xml")
	return c.SendString(b.String())
}

// ─────────────────────────────────────────────────────────────────────────────
// PROFILE SITEMAP — /sitemap-profiles.xml
// ─────────────────────────────────────────────────────────────────────────────

func SitemapProfilesHandler(c *fiber.Ctx) error {
	rows, err := db.Query(ctx, `
        SELECT username, COALESCE(last_active, created_at, NOW())
        FROM users
        WHERE COALESCE(is_banned, false) = false
          AND COALESCE(is_private, false) = false
          AND COALESCE(account_status, 'approved') = 'approved'
        ORDER BY username`)
	if err != nil {
		return c.Status(500).SendString("sitemap error")
	}
	defer rows.Close()

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")

	for rows.Next() {
		var username string
		var lastMod time.Time
		if err := rows.Scan(&username, &lastMod); err != nil {
			continue
		}
		b.WriteString(fmt.Sprintf(`  <url>
    <loc>https://dostana.biz/profile/%s</loc>
    <lastmod>%s</lastmod>
    <changefreq>weekly</changefreq>
    <priority>0.6</priority>
  </url>
`, html.EscapeString(username), lastMod.Format("2006-01-02")))
	}

	b.WriteString(`</urlset>`)
	c.Set("Content-Type", "application/xml")
	return c.SendString(b.String())
}

// ─────────────────────────────────────────────────────────────────────────────
// POST SITEMAP — /sitemap-posts.xml?page=N
// Covers both text and image posts. Image posts also get proper
// <image:image> tags so Google can index them in Image Search too.
// Excludes posts from banned/private/unapproved authors and posts that
// have already expired (self-destructing posts), so nothing dead or
// hidden ever ends up in the index.
// ─────────────────────────────────────────────────────────────────────────────

func SitemapPostsHandler(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	if page < 1 {
		page = 1
	}
	const perPage = 500
	offset := (page - 1) * perPage

	rows, err := db.Query(ctx, `
		SELECT p.id, p.author, p.content, COALESCE(p.image_url,''), p.created_at
		FROM posts p
		JOIN users u ON u.username = p.author
		WHERE COALESCE(u.is_banned,false) = false
		  AND COALESCE(u.is_private,false) = false
		  AND COALESCE(u.account_status,'approved') = 'approved'
		  AND (p.expires_at IS NULL OR p.expires_at > NOW())
		ORDER BY p.id DESC
		LIMIT $1 OFFSET $2`, perPage, offset)
	if err != nil {
		return c.Status(500).SendString("sitemap error")
	}
	defer rows.Close()

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:image="http://www.google.com/schemas/sitemap-image/1.1">` + "\n")

	for rows.Next() {
		var id int
		var author, content, imageURL string
		var createdAt time.Time
		if err := rows.Scan(&id, &author, &content, &imageURL, &createdAt); err != nil {
			continue
		}

		// Short plain-text caption for the image sitemap entry, if any.
		// Truncate by RUNE, not byte — content is often Urdu/Arabic script,
		// where slicing by byte index can cut a multi-byte character in
		// half and produce invalid UTF-8, which breaks XML well-formedness
		// (this is what caused the "not well-formed" / ￿ parser errors).
		caption := strings.TrimSpace(content)
		if r := []rune(caption); len(r) > 100 {
			caption = string(r[:100])
		}
		caption = html.EscapeString(caption)

		imageBlock := ""
		if imageURL != "" {
			fullImg := imageURL
			if strings.HasPrefix(fullImg, "/") {
				fullImg = "https://dostana.biz" + fullImg
			}
			imageBlock = fmt.Sprintf(`
    <image:image>
      <image:loc>%s</image:loc>
      <image:title>%s</image:title>
      <image:caption>%s</image:caption>
    </image:image>`, html.EscapeString(fullImg), caption, caption)
		}

		// Text-only posts need a minimum length to be worth indexing —
		// mirrors the same "≥80 chars" trending-eligibility bar used
		// elsewhere, so throwaway one-liners don't dilute crawl budget.
		if imageURL == "" && len([]rune(content)) < 80 {
			continue
		}

		priority := "0.5"
		if imageURL != "" {
			priority = "0.6"
		}

		b.WriteString(fmt.Sprintf(`  <url>
    <loc>https://dostana.biz/post/%d</loc>
    <lastmod>%s</lastmod>
    <changefreq>monthly</changefreq>
    <priority>%s</priority>%s
  </url>
`, id, createdAt.Format("2006-01-02"), priority, imageBlock))
	}

	b.WriteString(`</urlset>`)
	c.Set("Content-Type", "application/xml")
	return c.SendString(b.String())
}

// ─────────────────────────────────────────────────────────────────────────────
// ROBOTS.TXT
// ─────────────────────────────────────────────────────────────────────────────

func RobotsHandler(c *fiber.Ctx) error {
	body := `User-agent: *
Allow: /profile/
Allow: /post/
Allow: /uploads/profile_images/
Allow: /uploads/posts/
Disallow: /admin/
Disallow: /chat/
Disallow: /settings
Disallow: /login
Disallow: /signup
Disallow: /pending-review
Disallow: /change-password

Sitemap: https://dostana.biz/sitemap.xml
`
	c.Set("Content-Type", "text/plain")
	return c.SendString(body)
}
